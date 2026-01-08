# Optimistic Locking Design (V2.4)

**Status: DESIGN DOCUMENT (Implementation is Pessimistic)**

## Current Implementation Status

> **Note:** This document describes the *designed* optimistic locking approach. The current codebase implements **pessimistic locking** where state is locked during the prepare phase, then validated. This approach is functionally correct but differs from the "validate then lock" approach described here.
>
> **What's implemented:**
> - Funds/state locked during 2PC prepare phase (`server.go:849`)
> - Lock transactions validate ReadSet at execution time (`evm.go:executeLock()`)
> - Transaction priority ordering: Finalize → Unlock → Lock → Local
> - WriteSet application on finalize (`evm.go:applyWriteSet()`)
>
> **Difference from this design:**
> - Design: Speculative execution with no upfront locks, validate at commit time
> - Implementation: Lock during prepare, validate during lock execution

## Overview

This document describes the slot-level optimistic locking *design* for the V2 protocol specification. The approach described below offers lower latency for non-conflicting transactions by deferring locks until commit validation.

## Flow

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                           OPTIMISTIC LOCKING FLOW                             │
└──────────────────────────────────────────────────────────────────────────────┘

1. USER SUBMISSION
   ┌─────────┐                    ┌─────────────┐
   │  User   │─── cross-shard ──▶│ Any Shard   │
   └─────────┘       tx          └──────┬──────┘
                                        │ detects cross-shard
                                        ▼
                                ┌───────────────┐
                                │ Orchestrator  │
                                └───────┬───────┘
                                        │
2. SPECULATIVE EXECUTION               │
   ┌────────────────────────────────────┴──────────────────────────────────────┐
   │  Orchestrator executes tx immediately (no upfront locks)                   │
   │                                                                            │
   │  Loop:                                                                     │
   │    1. Execute with current RwSet state                                     │
   │    2. On NoStateError (missing external state):                            │
   │       - Fetch state from corresponding shard                               │
   │       - Merge into RwSet                                                   │
   │       - Re-execute from beginning                                          │
   │    3. On definitive error → generate ErrorTx, add to mempool               │
   │    4. On success → record complete RwSet (ReadSet + WriteSet)              │
   └────────────────────────────────────┬──────────────────────────────────────┘
                                        │
3. ORCHESTRATOR BLOCK                   ▼
   ┌─────────────────────────────────────────────────────────────────────────┐
   │  OrchestratorShardBlock {                                                │
   │    CtToOrder: [{                                                         │
   │      ID: "tx-123",                                                       │
   │      RwSet: [{                                                           │
   │        Address: 0x...,                                                   │
   │        ReadSet: [{Slot: X, Value: V1}, ...],   // Values AT SIMULATION   │
   │        WriteSet: [{Slot: X, OldValue: V1, NewValue: V2}, ...]            │
   │      }]                                                                  │
   │    }]                                                                    │
   │  }                                                                       │
   └─────────────────────────────────────────────────────────────────────────┘
                                        │
                                        │ broadcast
                                        ▼
4. STATE SHARD LOCK PHASE
   ┌─────────────────────────────────────────────────────────────────────────┐
   │  For each tx in CtToOrder:                                               │
   │                                                                          │
   │  Generate LOCK TRANSACTION:                                              │
   │    for each ReadSetItem in tx.RwSet:                                     │
   │      currentValue := state.GetStorageAt(address, slot)                   │
   │      if currentValue != ReadSetItem.Value:                               │
   │        ABORT (state changed since simulation)                            │
   │      else:                                                               │
   │        lockState(address, slot)  // Prevent local tx modification        │
   │                                                                          │
   │  If ALL ReadSet validations pass → vote YES                              │
   │  If ANY ReadSet validation fails → vote NO                               │
   └─────────────────────────────────────────────────────────────────────────┘
                                        │
                                        │ StateShardBlock with votes
                                        ▼
5. ORCHESTRATOR COLLECTS VOTES
   ┌─────────────────────────────────────────────────────────────────────────┐
   │  If ALL involved shards vote YES → TpcResult[txID] = true (commit)      │
   │  If ANY shard votes NO → TpcResult[txID] = false (abort)                 │
   └─────────────────────────────────────────────────────────────────────────┘
                                        │
                                        │ broadcast TpcResult
                                        ▼
6. STATE SHARD FINALIZE PHASE
   ┌─────────────────────────────────────────────────────────────────────────┐
   │  For each txID in TpcResult:                                             │
   │                                                                          │
   │  if TpcResult[txID] == true (COMMIT):                                    │
   │    Generate FINALIZE TRANSACTION:                                        │
   │      for each WriteSetItem in tx.RwSet:                                  │
   │        state.SetStorageAt(address, slot, NewValue)                       │
   │    Generate UNLOCK TRANSACTION:                                          │
   │      unlockState(address, slots)                                         │
   │                                                                          │
   │  if TpcResult[txID] == false (ABORT):                                    │
   │    Generate UNLOCK TRANSACTION only (no state changes)                   │
   └─────────────────────────────────────────────────────────────────────────┘
```

## Key Differences: Current Implementation vs. Optimistic Design

| Aspect | Current Implementation (Pessimistic) | Optimistic Design |
|--------|--------------------------------------|-------------------|
| Lock timing | During prepare phase (before lock tx executes) | After simulation, at commit time |
| State validation | ReadSet validated during Lock tx execution | Same (ReadSet validated at lock time) |
| Abort trigger | Lock tx fails if ReadSet mismatch | Same mechanism |
| WriteSet | Applied during Finalize tx | Same (WriteSet applied on finalize) |
| Contention handling | State locked during prepare phase | Speculative execution, abort on conflict |

**Note:** Both approaches correctly implement 2PC. The difference is *when* locks are acquired, not *whether* validation occurs.

## Why Optimistic?

1. **Lower latency for non-conflicting transactions** - No waiting for locks during simulation
2. **Better throughput** - Multiple simulations can run in parallel
3. **Aligns with V2.md design** - Lock transactions validate ReadSet
4. **Simpler state management** - No long-lived simulation locks

## Implementation Changes

### 1. Orchestrator (simulator.go)

```go
// runSimulation now records full WriteSet with OldValue/NewValue
func (s *Simulator) runSimulation(job *simulationJob) {
    // ... execute EVM ...

    // After successful execution:
    // RwSet already contains ReadSet (what was read)
    // Now populate WriteSet with actual changes
    for addr, storage := range stateDB.GetDirtyStorage() {
        for slot, newValue := range storage {
            oldValue := stateDB.GetOriginalValue(addr, slot)
            // Add to WriteSet
        }
    }
}
```

### 2. State Shard (chain.go)

```go
// ProcessLockTransaction validates ReadSet before locking
func (c *Chain) ProcessLockTransaction(tx *protocol.Transaction) bool {
    for _, rw := range tx.RwSet {
        for _, item := range rw.ReadSet {
            currentValue := c.evmState.GetState(rw.Address, common.Hash(item.Slot))
            if !bytes.Equal(currentValue.Bytes(), item.Value) {
                return false // State changed, abort
            }
        }
        c.lockSlots(tx.CrossShardTxID, rw.Address, rw.ReadSet)
    }
    return true
}

// ProcessFinalizeTransaction applies WriteSet
func (c *Chain) ProcessFinalizeTransaction(tx *protocol.Transaction) {
    for _, rw := range tx.RwSet {
        for _, item := range rw.WriteSet {
            c.evmState.SetState(rw.Address, common.Hash(item.Slot), common.BytesToHash(item.NewValue))
        }
    }
}
```

### 3. Block Production Order (V2 spec)

```
TxOrdering = [
    Finalize transactions (priority 1),  // Apply committed WriteSet
    Unlock transactions (priority 2),    // Release locks
    Lock transactions (priority 3),      // Validate ReadSet & acquire locks
    Local transactions (priority 4)      // Fail if touching locked state
]
```

## Open Questions

1. **Slot-level vs Account-level locking?**
   - Current: Account-level locks for balance transfers
   - V2: Slot-level locks for contract storage

2. **ReadSet scope?**
   - Include balance reads? Or just storage slots?

3. **Error transaction format?**
   - How to record failed simulations in the block?
