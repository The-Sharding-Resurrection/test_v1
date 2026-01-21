# Optimistic Locking Design (V2.4)

**Status: IMPLEMENTED**

## Current Implementation Status

> **Note:** This document describes the optimistic locking approach which is now fully implemented.
>
> **What's implemented:**
> - No locks during simulation (read-only state fetching)
> - No pre-locking during CtToOrder processing (Lock tx is queued, not executed)
> - Lock tx validates ReadSet AND acquires slot locks atomically during `ProduceBlock`
> - Value transfers: balance validated and fund lock stored during Lock tx execution
> - Transaction priority ordering: Finalize → Unlock → Lock → Local
> - WriteSet application on finalize
>
> **Key files:**
> - `chain.go:ProduceBlock()` - Lock tx execution with atomic validate-and-lock
> - `chain.go:validateAndLockReadSetLocked()` - ReadSet validation and slot locking
> - `statefetcher.go` - Read-only state fetching (no locks)
> - `server.go:handleOrchestratorShardBlock()` - Queues Lock tx without pre-locking

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

## Implementation Summary

| Aspect | Implementation |
|--------|----------------|
| Simulation | Read-only state fetching, no locks acquired |
| CtToOrder processing | Lock tx queued (not executed), pending credits stored |
| Lock timing | During `ProduceBlock` when Lock tx executes |
| State validation | ReadSet validated atomically with lock acquisition |
| Abort trigger | Lock tx fails if ReadSet mismatch or slot already locked |
| WriteSet | Stored during Lock, applied during Finalize tx |
| Contention handling | Speculative execution, abort on conflict |

**Note:** This implements true optimistic locking - no state is locked until the Lock tx executes during block production.

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
