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

    // Vote timeout tracking
    voteStartBlock map[string]uint64                 // txID -> block height when voting started
    voteTimeout    uint64                            // Blocks to wait before auto-abort (default: 10)
}
```

**Block Production Flow:**
```
1. ProduceBlock() called every 3 seconds
2. checkTimeouts() aborts any timed-out transactions (added to pendingResult)
3. Creates block with:
   - TpcResult: commit/abort decisions from previous round
   - CtToOrder: new cross-shard transactions
4. Moves pendingTxs to awaitingVotes
5. Records voteStartBlock[txID] = newHeight for timeout tracking
6. Computes expectedVoters from tx.InvolvedShards()
7. Broadcasts block to all State Shards
8. Updates transaction statuses from TpcResult
9. Releases simulation locks (fetcher.UnlockAll)
```

**Vote Aggregation:**
- All involved shards (source + destinations) must vote
- First NO vote immediately aborts the transaction
- Only commits when ALL expected shards vote YES
- Duplicate votes from same shard are ignored
- Transactions auto-abort after `voteTimeout` blocks without all votes (default: 10 blocks)

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
    prepareTxs     []protocol.Transaction        // Prepare ops for next block (crash recovery)
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

## Persistence Modes

- **Default (persistent):** Each shard opens a LevelDB-backed state at `config.StorageDir/shard{ID}` seeded by `shard{ID}_root.txt`. Blocks commit directly into LevelDB so account balances/storage survive restarts.
- **Fallback (in-memory):** If the configured storage path/root is missing or LevelDB fails to open, the server logs a warning and uses an in-memory `NewMemoryEVMState` (used heavily in unit tests).
- **Scope of persistence:** Only EVM state is persisted. Chain metadata, receipts, and block history remain in-memory and are rebuilt on restart.

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
    PrepareTxs []Transaction     // Prepare ops for crash recovery (audit trail)
    TpcPrepare map[string]bool   // txID -> can_commit (vote)
}
```

**Note on PrepareTxs:** The `PrepareTxs` field records prepare-phase operations (fund locks, pending credits, pending write sets) for crash recovery. These operations are executed immediately when an orchestrator block is received, but also recorded in the block as an audit trail. This enables recovery by replaying blocks.

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
├── network/
│   ├── client.go        # HTTP client factory with network simulation support
│   ├── delayed_transport.go  # HTTP RoundTripper with configurable latency simulation
│   └── delayed_transport_test.go  # Tests for latency simulation
└── test/
    └── integration_test.go  # Integration tests for 2PC flow
```

## API Endpoints

### State Shard (port 8545-8552)

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
| `/rw-set` | POST | V2.2: Simulate sub-call and return RwSet |
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
3. EVM executes tx in SimulationStateDB with CrossShardTracer
4. If external call detected (to address on another shard):
   a. Record NoStateError with call details
   b. Send RwSetRequest to target State Shard
   c. Receive RwSetReply with sub-call RwSet
   d. Merge RwSet and re-execute from start
5. RwSet built from captured SLOAD/SSTORE operations
6. Tx moved to CtToOrder with populated RwSet
7. On TpcResult, simulation locks released
```

**Key Components:**
- `SimulationStateDB`: Implements geth's StateDB interface, captures all state access
- `StateFetcher`: Fetches and caches bytecode/state from State Shards
- `Simulator`: Background worker that processes simulation queue
- `CrossShardTracer`: EVM tracer that detects CALL operations to external shards (V2.2)

### V2.2 Iterative Re-execution

When the Orchestrator encounters a call to a contract on another shard:

```
┌─────────────────────────────────────────────────────────────┐
│              Orchestrator Simulation Loop                    │
│                                                              │
│  For each iteration (max 10):                                │
│    1. Create SimulationStateDB with accumulated RwSet        │
│    2. Verify RwSet consistency (values match current state)  │
│    3. Execute EVM with CrossShardTracer                      │
│    4. If external call detected:                             │
│       - Send RwSetRequest to target State Shard              │
│       - Merge returned RwSet                                 │
│       - Re-execute from start                                │
│    5. If no external calls: simulation complete              │
└─────────────────────────────────────────────────────────────┘
```

**RwSetRequest/RwSetReply Protocol:**
```go
// Request sent to State Shard
type RwSetRequest struct {
    Address        common.Address  // Contract to call
    Data           []byte          // Call data
    Value          *big.Int        // Value transferred
    Caller         common.Address  // Original caller
    ReferenceBlock Reference       // State snapshot reference
    TxID           string          // Parent transaction ID
}

// Reply from State Shard
type RwSetReply struct {
    Success bool           // Whether simulation succeeded
    RwSet   []RwVariable   // ReadSet + WriteSet from sub-call
    Error   string         // Error message if failed
    GasUsed uint64         // Gas consumed
}
```

**State Shard `/rw-set` Endpoint:**
- Receives `RwSetRequest` for sub-call simulation
- Uses `TrackingStateDB` to capture all state accesses
- Returns `RwSetReply` with complete RwSet for that sub-call

## Assumptions and Limitations

1. **No consensus**: Single validator per shard, instant finality
2. **Synchronous blocks**: Fixed 3-second intervals, no clock sync
3. **Persistence modes**: State defaults to LevelDB-backed persistence; tests/misconfig fall back to in-memory. Block/receipt history is still in-memory.
4. **Trust model**: Orchestrator Shard trusts State Shard blocks
5. **No Merkle proofs**: ReadSetItem.Proof is always empty
6. **Value distribution**: Multi-recipient RwSet gives full Value to each recipient (no Amount field yet)
7. **Block metadata opcodes**: EVM opcodes like `NUMBER`, `TIMESTAMP`, `BLOCKHASH` may return inconsistent values:
   - Each shard maintains its own block number/timestamp independently
   - Orchestrator simulation uses hardcoded values (e.g., `block.number=1`)
   - `BLOCKHASH` always returns zero (no block history)
   - See `docs/TODO.md#15` for full details

## Network Simulation

### Latency Simulation

The system supports configurable network latency simulation for realistic performance testing of cross-shard communication.

**Components:**
- `internal/network/delayed_transport.go`: HTTP RoundTripper with random delays
- `internal/network/client.go`: HTTP client factory with configuration support

**Configuration:**

Network latency is configured via `config.json`:

```json
{
  "network": {
    "delay_enabled": true,
    "min_delay_ms": 10,
    "max_delay_ms": 100
  }
}
```

- `delay_enabled`: Enable/disable latency simulation (default: `false`)
- `min_delay_ms`: Minimum delay per HTTP request in milliseconds
- `max_delay_ms`: Maximum delay per HTTP request in milliseconds

**Implementation Details:**

- Random delays are applied before each HTTP request
- Thread-safe: Uses `sync.Mutex` to protect RNG access
- Validation: Negative values disable simulation, swaps min/max if reversed
- Used by: Orchestrator's `StateFetcher` and `Service` for shard communication

**Thread Safety:**

The `DelayedRoundTripper` is safe for concurrent use:
```go
type DelayedRoundTripper struct {
    base   http.RoundTripper
    config DelayConfig
    mu     sync.Mutex  // protects rng
    rng    *rand.Rand
}
```

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

---

## V2 Protocol Migration

The V2 protocol (`docs/V2.md`) introduces significant architectural changes:

### Key Differences from Current Implementation

| Aspect | Current (v1) | V2 |
|--------|--------------|-----|
| Entry point | User → Orchestrator (for cross-shard) | User → State Shard of `To` address |
| Initial simulation | Orchestrator only | State Shard first, then Orchestrator |
| State fetching | On-demand RPC | Iterative re-execution with `RwSetRequest`/`RwSetReply` |
| Proof validation | Deferred (empty proofs) | Merkle proofs required |
| Transaction types | Implicit flags | Explicit: Finalize, Unlock, Lock, Local |
| Block ordering | No strict order | Finalize → Unlock → Lock → Local |

### V2 Transaction Flow

```
User submits tx → State Shard (of To address)
    │
    ▼ simulate locally
  ┌─────────────┐
  │ Local tx?   │
  └─────┬───────┘
        │
   Yes  │   No (NoStateError)
   ▼    │
Execute │   ▼
locally │   Build partial RwSet
        │   Forward to Orchestrator
        │        │
        │        ▼
        │   Orchestrator: iterative simulation
        │   ┌──────────────────────────────┐
        │   │ 1. Validate RwSet (Merkle)   │
        │   │ 2. Simulate with RwSet       │
        │   │ 3. NoStateError? → RwSetReq  │
        │   │ 4. Merge reply, re-execute   │
        │   │ 5. Complete → CtToOrder      │
        │   └──────────────────────────────┘
        │        │
        │        ▼
        │   Block-based 2PC (same as current)
        └────────┘
```

### V2 Block Processing Order

State Shards MUST process transactions in this order:
1. **Finalize transactions** - Apply WriteSet from committed cross-shard txs
2. **Unlock transactions** - Release locks after commit/abort
3. **Lock transactions** - Verify ReadSet and acquire locks for new cross-shard txs
4. **Local transactions** - Execute normally (fail if accessing locked state)

### Migration Status

| V2 Component | Status | Notes |
|--------------|--------|-------|
| V2.1: Entry Point Change | ✅ Not Needed | Current arch handles routing automatically |
| V2.2: Iterative Re-execution | ✅ Completed | CrossShardTracer, RwSetRequest/Reply, merge & re-execute |
| V2.3: Merkle Proof Validation | Pending | Requires light client implementation |
| V2.4: Explicit Transaction Types | ✅ Completed | Finalize/Unlock/Lock/Local ordering with optimistic locking |
| V2.5: RwSet Consistency Verification | ✅ Completed | VerifyRwSetConsistency() in simulator |
| V2.6: Terminology Updates | ✅ Completed | Worker Shard → State Shard |

See `docs/TODO.md` Phase V for detailed implementation tasks.
