# Implementation Architecture

This document describes the detailed implementation architecture of the Ethereum sharding experiment.

## System Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                         Users / DApps                            │
└─────────────────────────────┬───────────────────────────────────┘
                              │ HTTP API
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Contract Shard (Orchestrator)                 │
│  - Coordinates cross-shard transactions                          │
│  - Produces blocks with TpcResult + CtToOrder                    │
│  - Collects votes from State Shards                              │
│  - Stateless (no account balances)                               │
└─────────────────────────────┬───────────────────────────────────┘
                              │ Block Broadcast / Vote Collection
                              ▼
┌──────────┬──────────┬──────────┬──────────┬──────────┬──────────┐
│ Shard 0  │ Shard 1  │ Shard 2  │ Shard 3  │ Shard 4  │ Shard 5  │
│ (State)  │ (State)  │ (State)  │ (State)  │ (State)  │ (State)  │
└──────────┴──────────┴──────────┴──────────┴──────────┴──────────┘
  Each shard maintains independent EVM state
  Accounts assigned by: address % 6
```

## Component Details

### Contract Shard (Orchestrator)

**Location:** `internal/orchestrator/`

**Responsibilities:**
1. Accept cross-shard transaction submissions
2. Produce blocks containing new transactions (`CtToOrder`) and commit decisions (`TpcResult`)
3. Broadcast blocks to all State Shards
4. Collect prepare votes from State Shard blocks
5. Track transaction status

**Key Data Structures:**

```go
// internal/orchestrator/chain.go
type ContractChain struct {
    blocks        []*protocol.ContractShardBlock
    height        uint64
    pendingTxs    []protocol.CrossShardTx         // New txs for next block
    awaitingVotes map[string]*protocol.CrossShardTx // Txs waiting for votes
    pendingResult map[string]bool                  // Votes for next block's TpcResult
}
```

**Block Production Flow:**
```
1. ProduceBlock() called every 3 seconds
2. Creates block with:
   - TpcResult: commit/abort decisions from previous round
   - CtToOrder: new cross-shard transactions
3. Moves pendingTxs to awaitingVotes
4. Broadcasts block to all State Shards
5. Updates transaction statuses from TpcResult
```

### State Shards

**Location:** `internal/shard/`

**Responsibilities:**
1. Maintain EVM state (balances, contracts, storage)
2. Process Contract Shard blocks (prepare phase + commit/abort)
3. Produce blocks with prepare votes (`TpcPrepare`)
4. Handle local transactions and contract calls

**Key Data Structures:**

```go
// internal/shard/chain.go
type Chain struct {
    blocks         []*protocol.StateShardBlock
    height         uint64
    currentTxs     []protocol.TxRef              // Txs for next block
    prepares       map[string]bool               // Prepare votes for next block
    locked         map[string]*LockedFunds       // Escrowed funds (source shard)
    pendingCredits map[string]*PendingCredit     // Pending credits (dest shard)
}
```

**Block Processing Flow:**
```
On receiving Contract Shard block:

Phase 1: Process TpcResult (previous round's decisions)
  - Source shard: Clear lock (commit) or refund (abort)
  - Dest shard: Apply credit (commit) or discard (abort)

Phase 2: Process CtToOrder (new transactions)
  - Source shard: Debit sender, lock funds, record prepare vote
  - Dest shard: Store pending credit for later
```

## Block-Based 2PC Protocol

### Transaction Lifecycle

```
User submits tx ──► Orchestrator adds to pendingTxs
                           │
                           ▼
              ┌────────────────────────┐
              │ Contract Shard Block N │
              │ CtToOrder: [tx1]       │
              │ TpcResult: {}          │
              └──────────┬─────────────┘
                         │ broadcast
                         ▼
              ┌────────────────────────┐
              │ State Shards process:  │
              │ - Source: debit+lock   │
              │ - Dest: store pending  │
              └──────────┬─────────────┘
                         │ produce blocks
                         ▼
              ┌────────────────────────┐
              │ State Shard Blocks     │
              │ TpcPrepare: {tx1:true} │
              └──────────┬─────────────┘
                         │ send to orchestrator
                         ▼
              ┌────────────────────────┐
              │ Orchestrator collects  │
              │ votes, moves to        │
              │ pendingResult          │
              └──────────┬─────────────┘
                         │
                         ▼
              ┌────────────────────────┐
              │ Contract Shard Block   │
              │ N+1                    │
              │ TpcResult: {tx1:true}  │
              │ CtToOrder: [tx2, tx3]  │
              └──────────┬─────────────┘
                         │ broadcast
                         ▼
              ┌────────────────────────┐
              │ State Shards process:  │
              │ TpcResult: commit tx1  │
              │ CtToOrder: prepare     │
              │           tx2,tx3      │
              └────────────────────────┘
```

### Transaction States

```
pending ──► prepared ──► committed
                    └──► aborted
```

- **pending**: Submitted, not yet in a block
- **prepared**: In CtToOrder, waiting for votes
- **committed**: In TpcResult with true, finalized
- **aborted**: In TpcResult with false, rolled back

## Data Flow

### CrossShardTx Structure

```go
type CrossShardTx struct {
    ID        string           // UUID
    TxHash    common.Hash      // Optional Ethereum hash
    FromShard int              // Initiating shard
    From      common.Address   // Sender
    Value     *big.Int         // Transfer amount
    Data      []byte           // Calldata
    RwSet     []RwVariable     // Target shards/addresses
    Status    TxStatus
}
```

**Destinations are derived from RwSet:**
- Each `RwVariable` has an `Address` and `ReferenceBlock.ShardNum`
- `tx.TargetShards()` returns unique shard IDs from RwSet
- `tx.InvolvedShards()` returns FromShard + TargetShards

### Block Structures

**Contract Shard Block:**
```go
type ContractShardBlock struct {
    Height    uint64
    PrevHash  BlockHash
    Timestamp uint64
    TpcResult map[string]bool   // txID -> committed (from previous round)
    CtToOrder []CrossShardTx    // New txs (for this round)
}
```

**State Shard Block:**
```go
type StateShardBlock struct {
    Height     uint64
    PrevHash   BlockHash
    Timestamp  uint64
    StateRoot  common.Hash       // Merkle root of state
    TxOrdering []TxRef           // Executed transactions
    TpcPrepare map[string]bool   // txID -> can_commit (vote)
}
```

## File Structure

```
internal/
├── protocol/
│   ├── types.go         # CrossShardTx, RwVariable, PrepareRequest/Response
│   └── block.go         # ContractShardBlock, StateShardBlock
├── shard/
│   ├── server.go        # HTTP handlers, block producer, Contract Shard block processing
│   ├── chain.go         # State Shard blockchain, 2PC state (locks, pending credits)
│   ├── evm.go           # EVM state wrapper (geth vm + state packages)
│   ├── receipt.go       # Transaction receipt storage
│   └── jsonrpc.go       # JSON-RPC compatibility layer
└── orchestrator/
    ├── service.go       # HTTP handlers, block producer, vote collection
    └── chain.go         # Contract Shard blockchain, vote tracking
```

## API Endpoints

### State Shard (port 8545-8550)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/balance/{address}` | GET | Get account balance |
| `/faucet` | POST | Fund account (testnet) |
| `/transfer` | POST | Local transfer |
| `/cross-shard/transfer` | POST | Initiate cross-shard transfer |
| `/evm/deploy` | POST | Deploy contract |
| `/evm/call` | POST | Call contract (state-changing) |
| `/evm/staticcall` | POST | Call contract (read-only) |
| `/contract-shard/block` | POST | Receive Contract Shard block |
| `/` | POST | JSON-RPC (Foundry compatible) |

### Orchestrator (port 8080)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/shards` | GET | List all shards |
| `/cross-shard/submit` | POST | Submit cross-shard tx directly |
| `/cross-shard/status/{txid}` | GET | Get transaction status |
| `/state-shard/block` | POST | Receive State Shard block |

## Timing

- **Block production interval**: 3 seconds (both Contract Shard and State Shards)
- **2PC round**: ~6 seconds minimum (2 block intervals)
  - Round N: CtToOrder broadcast, State Shards prepare
  - Round N+1: TpcResult broadcast, State Shards commit/abort

## Assumptions and Limitations

1. **No consensus**: Single validator per shard, instant finality
2. **Synchronous blocks**: Fixed 3-second intervals, no clock sync
3. **In-memory state**: No persistence across restarts
4. **Trust model**: Contract Shard trusts State Shard blocks
5. **No Merkle proofs**: ReadSetItem.Proof is always empty
6. **Single-recipient Value**: Multi-recipient RwSet gives full Value to each

## Future Work

See README.md "TODOs and Open Issues" for detailed list including:
- Vote timeout handling
- Block height synchronization
- Persistent state
- Multi-recipient value distribution
- Merkle proof validation
