# Block-Based Two-Phase Commit Protocol

This document describes the block-based 2PC protocol used for cross-shard transactions.

## Transaction Submission

Users submit transactions to their local State Shard via the unified `/tx/submit` endpoint. The shard **automatically detects** whether the transaction is cross-shard:

```
POST /tx/submit to State Shard
         │
         ▼
┌─────────────────────────────────┐
│ Auto-detection:                 │
│ 1. Is 'to' on different shard?  │──► Cross-shard
│ 2. Is 'to' a contract?          │
│    └─► Simulate with            │
│        TrackingStateDB          │
│        └─► Cross-shard access?  │──► Cross-shard
│ 3. Otherwise                    │──► Local (execute now)
└─────────────────────────────────┘
         │
    (Cross-shard)
         │
         ▼
Forward to Orchestrator for 2PC
```

**Key benefit**: Users don't need to know about sharding - they just submit to their shard.

## Overview

Traditional 2PC uses synchronous HTTP request/response. This implementation uses **block-based 2PC** where:
- Prepare requests are embedded in Orchestrator Shard blocks (`CtToOrder`)
- Prepare responses are embedded in State Shard blocks (`TpcPrepare`)
- Commit/abort decisions are embedded in Orchestrator Shard blocks (`TpcResult`)

This approach provides:
- Audit trail (all 2PC state is in blocks)
- Batch processing (multiple txs per round)
- Eventual consistency (async shard communication)

## Protocol Phases

### Phase 1: Prepare

**Orchestrator Shard:**
```
1. Receives cross-shard tx via /cross-shard/submit
2. Adds tx to pendingTxs
3. On block production:
   - Includes tx in CtToOrder
   - Moves tx to awaitingVotes
4. Broadcasts block to all State Shards
```

**Source State Shard (FromShard) - Lock-Only:**
```
1. Receives Orchestrator Shard block
2. For each tx in CtToOrder where FromShard == myShardID:
   a. ATOMIC: Hold evmState.mu lock during check-and-lock
      - Check available balance: balance - locked >= tx.Value
      - If sufficient: Lock funds immediately while holding mutex
   b. If sufficient:
      - Lock funds (no debit): chain.LockFunds(tx.ID, tx.From, tx.Value)
      - Vote yes: chain.AddPrepareResult(tx.ID, true)
   c. If insufficient:
      - Vote no: chain.AddPrepareResult(tx.ID, false)
3. Include votes in next State Shard block's TpcPrepare
4. Send block to Orchestrator Shard

Note: Actual debit happens on commit, not prepare. This simplifies abort handling.
Note: The mutex ensures concurrent prepare requests can't both pass the balance check.
```

**Destination State Shard(s) (from RwSet):**
```
1. Receives Orchestrator Shard block
2. For each tx where RwSet contains entry for myShardID:
   a. Validate ReadSet values match current state
   b. Store pending credit: chain.StorePendingCredit(tx.ID, address, tx.Value)
   c. Vote: chain.AddPrepareResult(tx.ID, readSetValid)
3. Include votes in next State Shard block's TpcPrepare
4. Send block to Orchestrator Shard

Note: Multiple RwSet entries on same shard produce single combined vote (AND of all validations).
```

### Phase 2: Commit/Abort

**Orchestrator Shard:**
```
1. Receives State Shard blocks with TpcPrepare votes
2. For each vote:
   - Record vote with shard ID: chain.RecordVote(tx.ID, shardID, canCommit)
   - First NO vote immediately aborts the transaction
   - Only commits when ALL expected shards vote YES
   - Duplicate votes from same shard are ignored (first vote wins)
3. When all expected votes received:
   - Move decision to pendingResult
4. On next block production:
   - Include all pendingResult in TpcResult
   - Broadcast block
   - Release simulation locks via StateFetcher.UnlockAll()
```

**Source State Shard (Lock-Only):**
```
1. Receives Orchestrator Shard block with TpcResult
2. For each tx where we have locked funds:
   a. If committed (TpcResult[tx.ID] == true):
      - Debit now: evmState.Debit(lock.Address, lock.Amount)
      - Clear lock: chain.ClearLock(tx.ID)
   b. If aborted (TpcResult[tx.ID] == false):
      - Just clear lock: chain.ClearLock(tx.ID)
      - No refund needed (balance was never debited)
```

**Destination State Shard(s):**
```
1. Receives Orchestrator Shard block with TpcResult
2. For each tx where we have pending credits:
   a. If committed:
      - Apply ALL credits: for each credit in GetPendingCredits(tx.ID):
          evmState.Credit(credit.Address, credit.Amount)
      - Clear pending: chain.ClearPendingCredit(tx.ID)
   b. If aborted:
      - Discard: chain.ClearPendingCredit(tx.ID)

Note: Supports multiple recipients per tx (stored as list of pending credits).
```

## State Transitions

### Transaction Status

```
                    submit
                      │
                      ▼
                  ┌───────┐
                  │pending│
                  └───┬───┘
                      │ included in CtToOrder
                      ▼
                 ┌────────┐
                 │prepared│
                 └────┬───┘
                      │ vote received
            ┌─────────┴─────────┐
            │                   │
     vote=true            vote=false
            │                   │
            ▼                   ▼
      ┌─────────┐         ┌───────┐
      │committed│         │aborted│
      └─────────┘         └───────┘
```

### Shard State

**Source Shard (Lock-Only):**
```
                    receive CtToOrder
                           │
                           ▼
              ┌────────────────────┐
              │ Check available    │
              │ (balance - locked) │
              └─────────┬──────────┘
                        │
           ┌────────────┴────────────┐
           │                         │
     sufficient              insufficient
           │                         │
           ▼                         ▼
    ┌─────────────┐           ┌───────────┐
    │ Lock only   │           │ Vote: NO  │
    │ (no debit)  │           └───────────┘
    │ Vote: YES   │
    └──────┬──────┘
           │
           ▼ receive TpcResult
    ┌──────┴──────┐
    │             │
 commit        abort
    │             │
    ▼             ▼
┌────────┐  ┌──────────┐
│ Debit  │  │ Clear    │
│ Clear  │  │ lock     │
│ lock   │  │ (no undo)│
└────────┘  └──────────┘
```

**Destination Shard:**
```
                    receive CtToOrder
                           │
                           ▼
              ┌────────────────────┐
              │ Store pending      │
              │ credit             │
              └─────────┬──────────┘
                        │
                        ▼ receive TpcResult
                 ┌──────┴──────┐
                 │             │
              commit        abort
                 │             │
                 ▼             ▼
           ┌──────────┐  ┌──────────┐
           │ Apply    │  │ Discard  │
           │ credit   │  │ pending  │
           └──────────┘  └──────────┘
```

## Data Structures

### Orchestrator Shard

```go
// Orchestrator tracking
pendingTxs     []CrossShardTx           // Not yet in a block
awaitingVotes  map[string]*CrossShardTx // In block, waiting for votes
pendingResult  map[string]bool          // All votes received, for next TpcResult

// Multi-shard vote aggregation
votes          map[string]map[int]bool  // txID -> shardID -> vote
expectedVoters map[string][]int         // txID -> list of shard IDs that must vote

// Block content
TpcResult map[string]bool   // Commit decisions from previous round
CtToOrder []CrossShardTx    // New transactions this round
```

### State Shard

```go
// 2PC state (Lock-Only)
shardID        int                                   // This shard's ID (included in blocks)
prepares       map[string]bool                       // Votes to include in next block
prepareTxs     []Transaction                         // Prepare ops for crash recovery
locked         map[string]*LockedFunds               // Reserved funds by txID
lockedByAddr   map[common.Address][]*lockedEntry     // Index for available balance
pendingCredits map[string][]*PendingCredit           // Pending credits (dest) - supports multiple recipients

// Available balance = balance - sum(lockedByAddr[addr])

// Block content
TpcPrepare map[string]bool  // Our votes
PrepareTxs []Transaction    // Prepare ops for crash recovery (audit trail)
ShardID    int              // Which shard produced this block
```

**PrepareTxs Field:** Records prepare-phase operations with TxType set to one of:
- `TxTypePrepareDebit` - Source shard locked funds
- `TxTypePrepareCredit` - Destination shard stored pending credit
- `TxTypePrepareWriteSet` - Destination shard stored pending write set

This provides an audit trail for crash recovery - blocks can be replayed to reconstruct 2PC state.

## Timing Diagram

```
Time   Orchestrator Shard      State Shard (Source)      State Shard (Dest)
─────────────────────────────────────────────────────────────────────────────
 0s    Block N produced
       CtToOrder: [tx1]
       ──────────────────────►  Receive block
                                Lock only, vote=yes
                                                         Receive block
                                                         Store pending credit

 3s                             Block M produced
                                TpcPrepare: {tx1:yes}
       ◄──────────────────────

       Vote received
       pendingResult: {tx1:yes}

 6s    Block N+1 produced
       TpcResult: {tx1:yes}
       ──────────────────────►  Receive block           Receive block
                                Debit + clear lock       Apply credit
                                                         Clear pending

 9s    tx1 COMMITTED
```

## Simulation Locks

Before 2PC begins, cross-shard contract calls go through simulation to discover the RwSet:

```
1. User submits cross-shard call to Orchestrator (/cross-shard/call)
2. Orchestrator's Simulator fetches and LOCKS addresses from State Shards
3. Simulator runs EVM with SimulationStateDB to discover RwSet
4. On success: tx added to pending with populated RwSet
5. On failure: all locks released, tx marked failed
6. After TpcResult (commit or abort): locks released via StateFetcher.UnlockAll()
```

**Lock Properties:**
- Each address can only be locked by one transaction at a time
- Locks have 2-minute TTL (cleaned up by background goroutine)
- Lock includes account state: balance, nonce, code, codeHash
- State Shards validate that simulation locks exist during 2PC prepare

**Lock Validation in 2PC:**
```go
// In handleOrchestratorShardBlock() for destination shards:
func validateRwVariable(txID string, rw RwVariable) bool {
    lock, ok := chain.GetSimulationLockByAddr(rw.Address)
    if !ok {
        // No lock = abort (prevents bypass attacks)
        return false
    }
    // Validate ReadSet against locked state...
}
```

## Edge Cases

### Vote Timeout

**Problem:** State Shard never sends vote (crash, network partition).

**Current behavior:** Transaction stays in `awaitingVotes` forever.

**TODO:** Implement timeout mechanism to abort after N blocks.

### Duplicate Votes

**Problem:** Same TpcPrepare vote received in multiple State Shard blocks.

**Current behavior:** ✅ First vote wins - duplicate votes from same shard are ignored.

**Implementation:** `RecordVote()` checks if shard has already voted before recording.

### Multi-Shard Vote Aggregation

**Problem:** Cross-shard transactions touch multiple shards that all need to vote.

**Current behavior:** ✅ Orchestrator collects votes from ALL involved shards (source + destinations).

**Implementation:**
- `expectedVoters[txID]` computed from `tx.InvolvedShards()` when tx added to block
- `votes[txID][shardID]` stores each shard's vote
- First NO vote immediately aborts
- Commits only when all expected shards vote YES

### Vote Combining

**Problem:** Multiple RwSet entries on same shard could produce multiple votes.

**Current behavior:** ✅ Single combined vote per tx per shard.

**Implementation:** State shard collects all RwSet validations first, then adds single vote (AND of all validations).

### Out-of-Order Blocks

**Problem:** State Shard processes Block N+1 before Block N.

**Current behavior:** Not handled - assumes in-order delivery.

**TODO:** Track expected block height, buffer out-of-order blocks.

## Comparison with HTTP-Based 2PC

| Aspect | HTTP-Based | Block-Based |
|--------|-----------|-------------|
| Latency | ~100ms | ~6s (2 block intervals) |
| Throughput | Limited by round-trips | Batched per block |
| Audit trail | Logs only | Full block history |
| Failure recovery | Complex | Replay blocks |
| Implementation | Simpler | More complex state |

Block-based 2PC trades latency for consistency and auditability.
