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
│                       Orchestrator Shard                         │
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

### Orchestrator Shard

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
type OrchestratorChain struct {
    blocks         []*protocol.OrchestratorShardBlock
    height         uint64
    pendingTxs     []protocol.CrossShardTx           // New txs for next block
    awaitingVotes  map[string]*protocol.CrossShardTx // Txs waiting for votes
    pendingResult  map[string]bool                   // Votes for next block's TpcResult

    // Multi-shard vote aggregation
    votes          map[string]map[int]bool           // txID -> shardID -> vote
    expectedVoters map[string][]int                  // txID -> list of shard IDs that must vote
}
```

**Block Production Flow:**
```
1. ProduceBlock() called every 3 seconds
2. Creates block with:
   - TpcResult: commit/abort decisions from previous round
   - CtToOrder: new cross-shard transactions
3. Moves pendingTxs to awaitingVotes
4. Computes expectedVoters from tx.InvolvedShards()
5. Broadcasts block to all State Shards
6. Updates transaction statuses from TpcResult
7. Releases simulation locks (fetcher.UnlockAll)
```

**Vote Aggregation:**
- All involved shards (source + destinations) must vote
- First NO vote immediately aborts the transaction
- Only commits when ALL expected shards vote YES
- Duplicate votes from same shard are ignored

### State Shards

**Location:** `internal/shard/`

**Responsibilities:**
1. Maintain EVM state (balances, contracts, storage)
2. Process Orchestrator Shard blocks (prepare phase + commit/abort)
3. Produce blocks with prepare votes (`TpcPrepare`)
4. Handle local transactions and contract calls

**Key Data Structures:**

```go
// internal/shard/chain.go
type Chain struct {
    shardID        int                           // This shard's ID
    blocks         []*protocol.StateShardBlock
    height         uint64
    currentTxs     []protocol.Transaction        // Txs for next block
    prepares       map[string]bool               // Prepare votes for next block
    locked         map[string]*LockedFunds       // Reserved funds (source shard)
    lockedByAddr   map[common.Address][]*lockedEntry // For available balance calc
    pendingCredits map[string][]*PendingCredit   // Pending credits (dest shard) - supports multiple recipients
}
```

**Block Processing Flow (Lock-Only 2PC):**
```
On receiving Orchestrator Shard block:

Phase 1: Process TpcResult (previous round's decisions)
  - Source shard: Debit + clear lock (commit) or just clear lock (abort)
  - Dest shard: Apply credit (commit) or discard (abort)

Phase 2: Process CtToOrder (new transactions)
  - Source shard: Check available balance, lock funds (no debit), record vote
  - Dest shard: Store pending credit for later

Lock-Only Approach:
  - Prepare: Lock funds only (available = balance - locked)
  - Commit: Debit from balance, clear lock
  - Abort: Just clear lock (no refund needed - balance unchanged)
```

## Block-Based 2PC Protocol

### Transaction Lifecycle

```
User submits tx ──► Orchestrator adds to pendingTxs
                           │
                           ▼
              ┌────────────────────────┐
              │ Orchestrator Block N   │
              │ CtToOrder: [tx1]       │
              │ TpcResult: {}          │
              └──────────┬─────────────┘
                         │ broadcast
                         ▼
              ┌────────────────────────┐
              │ State Shards process:  │
              │ - Source: lock only    │
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
              │ Orchestrator Block     │
              │ N+1                    │
              │ TpcResult: {tx1:true}  │
              │ CtToOrder: [tx2, tx3]  │
              └──────────┬─────────────┘
                         │ broadcast
                         ▼
              ┌─────────────────────────┐
              │ State Shards process:   │
              │ TpcResult: debit+commit │
              │            tx1          │
              │ CtToOrder: lock tx2,tx3 │
              └─────────────────────────┘
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

**Orchestrator Shard Block:**
```go
type OrchestratorShardBlock struct {
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
    ShardID    int               // Which shard produced this block
    Height     uint64
    PrevHash   BlockHash
    Timestamp  uint64
    StateRoot  common.Hash       // Merkle root of state
    TxOrdering []Transaction     // Executed transactions
    TpcPrepare map[string]bool   // txID -> can_commit (vote)
}
```

## File Structure

```
internal/
├── protocol/
│   ├── types.go         # CrossShardTx, RwVariable, PrepareRequest/Response
│   └── block.go         # OrchestratorShardBlock, StateShardBlock
├── shard/
│   ├── server.go        # HTTP handlers, unified /tx/submit, block producer
│   ├── server_test.go   # Unit tests for /tx/submit endpoint
│   ├── chain.go         # State Shard blockchain, 2PC state (locks, pending credits)
│   ├── chain_test.go    # Unit tests for chain operations
│   ├── evm.go           # EVM state + SimulateCall for cross-shard detection
│   ├── tracking_statedb.go  # StateDB wrapper that tracks accessed addresses
│   ├── receipt.go       # Transaction receipt storage
│   ├── jsonrpc.go       # JSON-RPC compatibility layer
│   └── security_fixes_test.go  # Security fix tests (lock bypass, atomic balance, thread safety)
├── orchestrator/
│   ├── service.go       # HTTP handlers, block producer, vote collection
│   ├── service_test.go  # Security tests (pointer aliasing, broadcast concurrency)
│   ├── chain.go         # Orchestrator Shard blockchain, vote tracking
│   ├── chain_test.go    # Unit tests for orchestrator chain
│   ├── simulator.go     # EVM simulation for cross-shard transactions
│   ├── simulator_test.go  # Simulator tests including queue timeout
│   ├── statedb.go       # SimulationStateDB - EVM state interface for simulation
│   ├── statedb_test.go  # StateDB tests including SubRefund underflow
│   └── statefetcher.go  # StateFetcher - fetches/caches state from State Shards
└── test/
    └── integration_test.go  # Integration tests for 2PC flow
```

## API Endpoints

### State Shard (port 8545-8550)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/tx/submit` | POST | **Unified tx submission** - auto-detects cross-shard |
| `/balance/{address}` | GET | Get account balance |
| `/faucet` | POST | Fund account (testnet) |
| `/transfer` | POST | Local transfer (legacy) |
| `/cross-shard/transfer` | POST | Initiate cross-shard transfer (legacy) |
| `/evm/deploy` | POST | Deploy contract |
| `/evm/call` | POST | Call contract (state-changing) |
| `/evm/staticcall` | POST | Call contract (read-only) |
| `/evm/storage/{addr}/{slot}` | GET | Get storage slot value |
| `/state/lock` | POST | Lock address for simulation |
| `/state/unlock` | POST | Unlock address after simulation |
| `/orchestrator-shard/block` | POST | Receive Orchestrator Shard block |
| `/` | POST | JSON-RPC (Foundry compatible) |

### Orchestrator (port 8080)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check |
| `/shards` | GET | List all shards |
| `/cross-shard/submit` | POST | Submit cross-shard tx directly |
| `/cross-shard/call` | POST | Submit cross-shard contract call (with simulation) |
| `/cross-shard/simulation/{txid}` | GET | Get simulation status |
| `/cross-shard/status/{txid}` | GET | Get transaction status |
| `/state-shard/block` | POST | Receive State Shard block |

## Timing

- **Block production interval**: 3 seconds (both Orchestrator Shard and State Shards)
- **2PC round**: ~6 seconds minimum (2 block intervals)
  - Round N: CtToOrder broadcast, State Shards prepare
  - Round N+1: TpcResult broadcast, State Shards commit/abort

## Unified Transaction Submission

Users submit transactions to their local State Shard via `/tx/submit`. The shard auto-detects whether the tx is cross-shard:

```
User submits POST /tx/submit to State Shard
    │
    ▼
State Shard checks 'to' address
    │
    ├─► 'to' on different shard? → Forward to Orchestrator
    │
    └─► 'to' on this shard?
        │
        ├─► Simple transfer (no code) → Execute locally
        │
        └─► Contract call → Simulate with TrackingStateDB
            │
            ├─► All accessed addresses local? → Execute locally
            │
            └─► Cross-shard access detected? → Forward to Orchestrator
```

**Key Benefits:**
- Users don't need to know about sharding
- Transparent routing based on actual state access
- Local txs execute immediately (no 2PC overhead)

## EVM Simulation

The Orchestrator runs EVM simulation to discover RwSets for cross-shard contract calls:

```
1. Tx submitted to /cross-shard/call
2. Simulator fetches required state from State Shards (with locking)
3. EVM executes tx in SimulationStateDB
4. RwSet built from captured SLOAD/SSTORE operations
5. Tx moved to CtToOrder with populated RwSet
6. On TpcResult, simulation locks released
```

**Key Components:**
- `SimulationStateDB`: Implements geth's StateDB interface, captures all state access
- `StateFetcher`: Fetches and caches bytecode/state from State Shards
- `Simulator`: Background worker that processes simulation queue

## Assumptions and Limitations

1. **No consensus**: Single validator per shard, instant finality
2. **Synchronous blocks**: Fixed 3-second intervals, no clock sync
3. **In-memory state**: No persistence across restarts
4. **Trust model**: Orchestrator Shard trusts State Shard blocks
5. **No Merkle proofs**: ReadSetItem.Proof is always empty
6. **Value distribution**: Multi-recipient RwSet gives full Value to each recipient (no Amount field yet)
7. **Block metadata opcodes**: EVM opcodes like `NUMBER`, `TIMESTAMP`, `BLOCKHASH` may return inconsistent values:
   - Each shard maintains its own block number/timestamp independently
   - Orchestrator simulation uses hardcoded values (e.g., `block.number=1`)
   - `BLOCKHASH` always returns zero (no block history)
   - See `docs/TODO.md#15` for full details

## Security Hardening

The following security improvements have been implemented:

### Thread Safety

| Component | Protection | Details |
|-----------|------------|---------|
| `EVMState` | `sync.Mutex` | Atomic balance check + lock operations |
| `TrackingStateDB` | `sync.RWMutex` | Protected map access during simulation |
| `SimulationStateDB` | `sync.RWMutex` | Thread-safe refund and state access |

### Concurrency Controls

| Component | Control | Purpose |
|-----------|---------|---------|
| Block broadcast | Semaphore (max 3) + WaitGroup | Prevents goroutine leaks |
| Simulation queue | 5-second timeout | Prevents indefinite blocking |
| HTTP requests | Context with timeout | Bounded request lifetime |

### Data Integrity

| Fix | Problem | Solution |
|-----|---------|----------|
| Deep copy on storage | Pointer aliasing | `tx.DeepCopy()` before map storage |
| Lock bypass removed | Empty ReadSet skipped validation | All txs require valid simulation lock |
| SubRefund clamping | Panic on underflow | Clamp to zero instead |
| JSON error handling | Silent failures | Explicit error responses |

### Lock Ordering

To prevent deadlocks, locks are always acquired in this order:
```
evmState.mu → chain.mu
```

Never acquire `chain.mu` while holding `evmState.mu` in reverse order.

### 2PC Atomicity

- ReadSet validation happens during PREPARE phase only
- Once `TpcResult=true` is broadcast, ALL shards MUST commit
- No unilateral rejection at commit time (would break atomicity)

## Future Work

See README.md "TODOs and Open Issues" for detailed list including:
- Vote timeout handling
- Block metadata synchronization (consistent `block.number`/`block.timestamp` across shards)
- Persistent state
- Per-recipient amount specification (Amount field in RwVariable)
- Merkle proof validation
