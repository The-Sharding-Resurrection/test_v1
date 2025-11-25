# Block-Based Two-Phase Commit Protocol

This document describes the block-based 2PC protocol used for cross-shard transactions.

## Overview

Traditional 2PC uses synchronous HTTP request/response. This implementation uses **block-based 2PC** where:
- Prepare requests are embedded in Contract Shard blocks (`CtToOrder`)
- Prepare responses are embedded in State Shard blocks (`TpcPrepare`)
- Commit/abort decisions are embedded in Contract Shard blocks (`TpcResult`)

This approach provides:
- Audit trail (all 2PC state is in blocks)
- Batch processing (multiple txs per round)
- Eventual consistency (async shard communication)

## Protocol Phases

### Phase 1: Prepare

**Contract Shard:**
```
1. Receives cross-shard tx via /cross-shard/submit
2. Adds tx to pendingTxs
3. On block production:
   - Includes tx in CtToOrder
   - Moves tx to awaitingVotes
4. Broadcasts block to all State Shards
```

**Source State Shard (FromShard):**
```
1. Receives Contract Shard block
2. For each tx in CtToOrder where FromShard == myShardID:
   a. Attempt to debit sender: evmState.Debit(tx.From, tx.Value)
   b. If success:
      - Lock funds: chain.LockFunds(tx.ID, tx.From, tx.Value)
      - Vote yes: chain.AddPrepareResult(tx.ID, true)
   c. If fail (insufficient balance):
      - Vote no: chain.AddPrepareResult(tx.ID, false)
3. Include votes in next State Shard block's TpcPrepare
4. Send block to Contract Shard
```

**Destination State Shard(s) (from RwSet):**
```
1. Receives Contract Shard block
2. For each tx where RwSet contains entry for myShardID:
   - Store pending credit: chain.StorePendingCredit(tx.ID, address, tx.Value)
3. (No vote needed - only source shard votes)
```

### Phase 2: Commit/Abort

**Contract Shard:**
```
1. Receives State Shard blocks with TpcPrepare votes
2. For each vote:
   - If tx in awaitingVotes: chain.RecordVote(tx.ID, canCommit)
   - Move to pendingResult
3. On next block production:
   - Include all pendingResult in TpcResult
   - Broadcast block
```

**Source State Shard:**
```
1. Receives Contract Shard block with TpcResult
2. For each tx where we have locked funds:
   a. If committed (TpcResult[tx.ID] == true):
      - Clear lock: chain.ClearLock(tx.ID)
      - Funds stay debited (transfer complete)
   b. If aborted (TpcResult[tx.ID] == false):
      - Refund: evmState.Credit(lock.Address, lock.Amount)
      - Clear lock: chain.ClearLock(tx.ID)
```

**Destination State Shard(s):**
```
1. Receives Contract Shard block with TpcResult
2. For each tx where we have pending credit:
   a. If committed:
      - Apply credit: evmState.Credit(credit.Address, credit.Amount)
      - Clear pending: chain.ClearPendingCredit(tx.ID)
   b. If aborted:
      - Discard: chain.ClearPendingCredit(tx.ID)
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

**Source Shard:**
```
                    receive CtToOrder
                           │
                           ▼
              ┌────────────────────┐
              │ Check balance      │
              └─────────┬──────────┘
                        │
           ┌────────────┴────────────┐
           │                         │
     sufficient              insufficient
           │                         │
           ▼                         ▼
    ┌─────────────┐           ┌───────────┐
    │ Debit       │           │ Vote: NO  │
    │ Lock funds  │           └───────────┘
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
│ Clear  │  │ Refund   │
│ lock   │  │ Clear    │
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

### Contract Shard

```go
// Orchestrator tracking
pendingTxs    []CrossShardTx           // Not yet in a block
awaitingVotes map[string]*CrossShardTx // In block, waiting for vote
pendingResult map[string]bool          // Votes received, for next TpcResult

// Block content
TpcResult map[string]bool   // Commit decisions from previous round
CtToOrder []CrossShardTx    // New transactions this round
```

### State Shard

```go
// 2PC state
prepares       map[string]bool         // Votes to include in next block
locked         map[string]*LockedFunds // Escrowed funds (source)
pendingCredits map[string]*PendingCredit // Pending credits (dest)

// Block content
TpcPrepare map[string]bool  // Our votes
```

## Timing Diagram

```
Time   Contract Shard          State Shard (Source)      State Shard (Dest)
─────────────────────────────────────────────────────────────────────────────
 0s    Block N produced
       CtToOrder: [tx1]
       ──────────────────────►  Receive block
                                Debit, lock, vote=yes
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
                                Clear lock               Apply credit
                                                         Clear pending

 9s    tx1 COMMITTED
```

## Edge Cases

### Vote Timeout

**Problem:** State Shard never sends vote (crash, network partition).

**Current behavior:** Transaction stays in `awaitingVotes` forever.

**TODO:** Implement timeout mechanism to abort after N blocks.

### Duplicate Votes

**Problem:** Same TpcPrepare vote received in multiple State Shard blocks.

**Current behavior:** Later votes overwrite earlier (map semantics).

**Consideration:** First vote should win, or use block height to validate.

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
