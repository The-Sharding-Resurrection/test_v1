# Baseline Cross-Shard Protocol Implementation

## Overview

This document describes the baseline iterative cross-shard protocol implementation per Issue #54. This is a simplified protocol for experimental comparison with the V2 protocol.

**Branch**: `claude/issue-54-20260119-1140`
**Status**: Core implementation complete

## Architecture

### State Shard (SS)

**Role**: Execution Engine & State Keeper

**Components**:
- `baseline_chain.go` - Chain state management, lock tracking, block production
- `baseline_evm.go` - EVM execution with NoStateError detection
- `baseline_overlay.go` - RwSet re-hydration overlay StateDB
- `baseline_handlers.go` - HTTP handlers for baseline protocol

**Responsibilities**:
1. Execute transactions locally
2. Detect NoStateError when calling external shards
3. Capture RwSet (reads/writes before error)
4. Generate Lock transactions for accessed state
5. Re-execute with RwSet overlay when receiving PENDING txs
6. Route to target shard via TargetShard field

### Orchestration Shard (OS)

**Role**: Stateless Coordinator & Data Bridge

**Components**:
- `baseline_service.go` - Simple aggregation and routing service

**Responsibilities**:
1. Receive blocks from State Shards
2. Aggregate PENDING, SUCCESS, FAIL transactions
3. Broadcast to all shards (no state fetching, no simulation)

## Transaction Status (CtStatus)

```go
const (
    CtStatusFail    = 0 // Transaction failed
    CtStatusSuccess = 1 // Transaction succeeded
    CtStatusPending = 2 // Waiting for next hop
)
```

## Protocol Flow

### Phase 1: Execution & Discovery (State Shard)

1. **Receive transaction** via `/tx/submit`
2. **Execute** with baseline EVM tracer
3. **Outcomes**:
   - **NoStateError**:
     - Capture RwSet (reads/writes before error)
     - Set `CtStatus=PENDING`, `TargetShard=<external shard>`
     - Generate Lock tx for accessed slots
     - Send block to Orchestrator
   - **Success**:
     - Set `CtStatus=SUCCESS`
     - Apply state changes
     - Send block to Orchestrator
   - **Revert**:
     - Set `CtStatus=FAIL`
     - Send block to Orchestrator

### Phase 2: Data Availability & Routing (Orchestrator)

1. **Receive blocks** from State Shards via `/state-shard/block`
2. **Aggregate** PENDING/SUCCESS/FAIL transactions
3. **Broadcast** in `CtToProcess` list to all shards

### Phase 3: Feedback & Re-Execution (State Shard)

1. **Receive** orchestrator block via `/orchestrator-shard/block`
2. **Process** `CtToProcess` list:
   - **SUCCESS**: Generate Finalize + Unlock txs
   - **FAIL**: Generate Unlock tx
   - **PENDING**:
     - If `TargetShard == MyShardID`: Add to mempool for re-execution
3. **Re-execution**:
   - Create overlay StateDB with RwSet
   - Execute with mocked state from previous hops
   - Continue until complete or hit next NoStateError

## Example: TravelAgency Flow

```
User → Shard A: bookTrainAndHotel()

Hop 0 (Shard A):
  Execute Agency contract
  ❌ NoStateError: Train.checkSeat() (Shard B)
  → RwSet: [Agency.slot1, Agency.slot2]
  → Lock Agency slots, CtStatus=PENDING, TargetShard=B

Orchestrator:
  Aggregate → Broadcast CtToProcess=[tx(PENDING, target=B)]

Hop 1 (Shard B):
  Re-execute with RwSet overlay (mock Agency state)
  Execute Train.checkSeat() ✅
  ❌ NoStateError: Hotel.checkRoom() (Shard C)
  → RwSet: [Agency.*, Train.slot1]
  → Lock Train slots, CtStatus=PENDING, TargetShard=C

Orchestrator:
  Aggregate → Broadcast CtToProcess=[tx(PENDING, target=C)]

Hop 2 (Shard C):
  Re-execute with RwSet overlay (mock Agency + Train state)
  Execute Hotel.checkRoom() ✅
  ❌ NoStateError: Train.book() (Shard B)
  → RwSet: [Agency.*, Train.slot1, Hotel.slot1]
  → Lock Hotel slots, CtStatus=PENDING, TargetShard=B

... (continues until execution completes)

Final Hop (Shard A):
  Re-execute with full RwSet overlay
  Execute customers[msg.sender] = true ✅
  → CtStatus=SUCCESS

Orchestrator:
  Broadcast CtToProcess=[tx(SUCCESS)]

All Shards:
  Generate Finalize (apply WriteSet) + Unlock txs
  Release locks, commit state
```

## Key Design Decisions

1. **NoStateError Detection**: EVM tracer intercepts CALL/STATICCALL/DELEGATECALL to external shards
2. **RwSet Accumulation**: Merged across hops using address-based deduplication
3. **Overlay Re-hydration**: StateDB wrapper injects RwSet reads as overlay
4. **Progressive Locking**: Locks acquired incrementally as execution progresses
5. **Slot-Level Granularity**: Fine-grained concurrency control (same as V2)

## Files Modified

### Protocol Types
- `internal/protocol/types.go` - Added `CtStatus`, `TargetShard` fields
- `internal/protocol/block.go` - Added `CtToProcess` field

### State Shard
- `internal/shard/baseline_utils.go` (10 lines) - AddressToShard utility
- `internal/shard/baseline_evm.go` (260 lines) - NoStateError detection
- `internal/shard/baseline_chain.go` (220 lines) - Chain state management
- `internal/shard/baseline_overlay.go` (200 lines) - RwSet overlay
- `internal/shard/baseline_handlers.go` (60 lines) - HTTP handlers
- `internal/shard/server.go` - Added BaselineChain field, updated routes

### Orchestrator
- `internal/orchestrator/baseline_service.go` (210 lines) - Stateless router

## Testing

### Unit Tests
- TODO: Add tests for baseline components

### Integration Tests
- TODO: Test TravelAgency contract scenario
- TODO: Compare performance with V2 protocol

## Differences from V2 Protocol

| Feature | V2 Protocol | Baseline Protocol |
|---------|-------------|-------------------|
| Orchestrator | Stateful (simulation, 2PC) | Stateless (routing only) |
| State Discovery | Simulation with fetching | Iterative execution |
| Locking | Optimistic (check on finalize) | Progressive (lock per hop) |
| RwSet Source | Simulation result | Execution trace |
| Cross-Shard Calls | Detected in simulation | Detected via EVM tracer |
| Proof Validation | Merkle proofs (V2.3) | None (trust-based) |
| Complexity | High (simulator, fetcher, 2PC) | Low (simple routing) |

## Next Steps

1. **Testing**: Write unit and integration tests
2. **Documentation**: Add API documentation
3. **Performance**: Benchmark vs V2 protocol
4. **Contract Examples**: Deploy TravelAgency, Train, Hotel contracts
5. **Experiment**: Compare latency, throughput, lock contention

## Known Limitations

- No Merkle proof validation (trust-based state)
- No crash recovery (would need to persist pending state)
- Basic lock conflict detection (no deadlock prevention)
- Simplified error handling (no retry logic)
