# Ethereum State & Transaction Sharding

Block-based state and transaction sharding with 2PC coordination.

## Quick Start

```bash
# Start network (6 shards + orchestrator)
docker compose up --build -d

# Test cross-shard transactions
./scripts/test-cross-shard.sh

# View logs
docker compose logs -f shard-orch
docker compose logs -f shard-0

# Stop
docker compose down
```

## Architecture

### Components

- **Orchestrator Shard**: Coordinates cross-shard transactions via 2PC
  - Produces blocks with `tpc_result` (commit decisions) and `ct_to_order` (new txs)
  - Broadcasts blocks to all State Shards
  - **Stateless**: No account balances or contract storage

- **State Shards** (0-5): Independent blockchain state
  - Produce blocks with `tx_ordering` (executed txs) and `tpc_prepare` (2PC votes)
  - Send blocks to Orchestrator Shard
  - Maintain EVM state (balances, contracts, storage)

### Transaction Flow

Users submit transactions to their local State Shard. The shard **automatically detects** whether it's cross-shard:

```
User submits POST /tx/submit to State Shard
         │
         ▼
┌─────────────────────────────────────┐
│ State Shard auto-detection:         │
│ - 'to' on different shard?          │───► Cross-shard
│ - Contract accesses other shards?   │───► Cross-shard
│ - Otherwise                         │───► Local (execute immediately)
└─────────────────────────────────────┘
         │
    Cross-shard
         │
         ▼
   Forward to Orchestrator → 2PC Protocol
```

### Block-Based 2PC Flow (Cross-Shard Only)

```
Round N:
  1. Orchestrator Block N: ct_to_order = [tx1, tx2], tpc_result = {}
         ↓ broadcast to all State Shards

  2. State Shards process:
     - Source Shard: lock funds (no debit), vote
     - Dest Shard: validate ReadSet, store pending credit, vote
         ↓ produce blocks with votes

  3. State Shard Blocks: tpc_prepare = {tx1: true, tx2: false}, shard_id = N
         ↓ send to Orchestrator

  4. Orchestrator aggregates votes from ALL involved shards

Round N+1:
  5. Orchestrator Block N+1: tpc_result = {tx1: true, tx2: false}
         ↓ broadcast

  6. State Shards finalize:
     - Committed: Source debits + clears lock, Dest applies credits
     - Aborted: Source clears lock (no refund needed), Dest discards pending
```

**Key**: Users don't need to know about sharding - the system handles routing transparently.

### Communication Pattern

```
Orchestrator Shard → State Shards (broadcast)
State Shards → Orchestrator Shard (unicast)
State Shards ↮ State Shards (NONE - isolated)
```

## Implementation Status

### ✅ Implemented

- Block-based 2PC coordination
- State sharding (contracts on one shard, state distributed by address % 6)
- Cross-shard atomic transactions
- EVM execution (geth's vm + state packages)
- JSON-RPC compatibility (Foundry tools)
- EVM simulation for RwSet discovery (cross-shard contract calls)
- Multi-shard vote aggregation (all involved shards must vote)
- Multi-recipient credit handling (multiple recipients per tx)
- Destination shard voting with ReadSet validation
- Simulation lock lifecycle with TTL-based cleanup

### ⚠️ Deferred (Documented Assumptions)

**Merkle Proof Validation**:
- ReadSetItem.Proof field exists but empty (`[][]byte{}`)
- Trust StateShardBlock.StateRoot without cryptographic verification
- **Rationale**: Focus on 2PC mechanics before cryptographic validation
- **Future**: Implement MPT/Verkle proof generation and validation

**Light Client Protocol**:
- Orchestrator Shard trusts State Shard blocks without verification
- **Assumption**: State Shards are honest
- **Future**: Implement light client verification

**Consensus**:
- Single validator per shard (no BFT)
- Instant finality (no reorgs)
- Synchronous block production (3s intervals)
- **Assumption**: Consensus is solved, focus on cross-shard coordination
- **Future**: Add PBFT/HotStuff per shard

See design.md for full specification (Korean).

## API

### State Shard Endpoints

```bash
# Unified transaction submission (auto-detects cross-shard)
POST http://shard-0:8545/tx/submit
{"from": "0x...", "to": "0x...", "value": "1000", "data": "0x...", "gas": 100000}
# Returns: {"success": true, "cross_shard": false} for local tx
# Returns: {"success": true, "cross_shard": true, "tx_id": "..."} for cross-shard tx

# Balance
GET http://shard-0:8545/balance/0x1234...

# Local transfer (legacy - prefer /tx/submit)
POST http://shard-0:8545/transfer
{"from": "0x...", "to": "0x...", "amount": "1000"}

# Faucet
POST http://shard-0:8545/faucet
{"address": "0x...", "amount": "1000000000000000000000"}

# Cross-shard transfer (legacy - prefer /tx/submit)
POST http://shard-0:8545/cross-shard/transfer
{"from": "0x...", "to": "0x...", "to_shard": 1, "amount": "100000000000000000"}

# EVM
POST http://shard-0:8545/evm/deploy
{"from": "0x...", "bytecode": "0x608060...", "gas": 3000000}

POST http://shard-0:8545/evm/call
{"from": "0x...", "to": "0x...", "data": "0xa9059cbb...", "gas": 1000000}

# Storage access
GET http://shard-0:8545/evm/storage/{address}/{slot}

# Simulation locking (used by Orchestrator)
POST http://shard-0:8545/state/lock
{"tx_id": "...", "address": "0x..."}

POST http://shard-0:8545/state/unlock
{"tx_id": "...", "address": "0x..."}

# JSON-RPC (Foundry compatible)
POST http://shard-0:8545/
{"jsonrpc": "2.0", "method": "eth_sendTransaction", "params": [...], "id": 1}
```

### Orchestrator Shard Endpoints

```bash
# Health
GET http://shard-orch:8080/health

# Shard list
GET http://shard-orch:8080/shards

# Submit cross-shard contract call (triggers simulation)
POST http://shard-orch:8080/cross-shard/call
{"from": "0x...", "to": "0x...", "from_shard": 0, "value": "100", "data": "0xa9059cbb..."}

# Check simulation status
GET http://shard-orch:8080/cross-shard/simulation/{txid}

# Transaction status
GET http://shard-orch:8080/cross-shard/status/{txid}
```

## Data Structures

```go
// CrossShardTx represents a cross-shard transaction
// Destinations are derived from RwSet - each RwVariable specifies an address and shard
type CrossShardTx struct {
    ID        string         // Unique transaction ID (UUID)
    TxHash    common.Hash    // Optional: Ethereum tx hash
    FromShard int            // Source shard ID (initiator)
    From      common.Address // Sender address
    Value     *big.Int       // Transfer amount
    Data      []byte         // Optional: calldata
    RwSet     []RwVariable   // Target shards/addresses - destinations derived from this
    Status    TxStatus       // pending/prepared/committed/aborted
}

// Helper methods:
// tx.TargetShards() []int      - returns unique shard IDs from RwSet
// tx.InvolvedShards() []int    - returns FromShard + TargetShards

// Example: Simple transfer from shard 0 to shard 2
// CrossShardTx{
//     FromShard: 0,
//     From: sender,
//     Value: amount,
//     RwSet: []RwVariable{{
//         Address: recipient,
//         ReferenceBlock: Reference{ShardNum: 2},
//     }},
// }
//
// Example: Contract touching shards 1, 3, and 5
// CrossShardTx{
//     FromShard: 0,
//     From: caller,
//     RwSet: []RwVariable{
//         {Address: contract1, ReferenceBlock: Reference{ShardNum: 1}},
//         {Address: contract2, ReferenceBlock: Reference{ShardNum: 3}},
//         {Address: contract3, ReferenceBlock: Reference{ShardNum: 5}},
//     },
// }

type RwVariable struct {
    Address        common.Address
    ReferenceBlock Reference
    ReadSet        []ReadSetItem
    WriteSet       []Slot
}

type ReadSetItem struct {
    Slot  Slot
    Value []byte
    Proof [][]byte  // Empty for now (deferred)
}

// Orchestrator Shard Block
type OrchestratorShardBlock struct {
    Height    uint64
    PrevHash  BlockHash
    Timestamp uint64
    TpcResult map[string]bool  // txID → committed (from previous round)
    CtToOrder []CrossShardTx   // New cross-shard txs (for this round)
}

// State Shard Block
type StateShardBlock struct {
    ShardID    int               // Which shard produced this block
    Height     uint64
    PrevHash   BlockHash
    Timestamp  uint64
    StateRoot  common.Hash       // Merkle root
    TxOrdering []Transaction     // Executed txs
    TpcPrepare map[string]bool   // txID → can_commit (vote)
}
```

## File Structure

```
internal/
├── protocol/
│   ├── types.go       # CrossShardTx, RwVariable, ReadSetItem
│   └── block.go       # OrchestratorShardBlock, StateShardBlock definitions
├── shard/
│   ├── server.go      # HTTP handlers, unified /tx/submit, block producer
│   ├── server_test.go # Unit tests for /tx/submit endpoint
│   ├── chain.go       # State Shard blockchain + 2PC state (locks, pending credits)
│   ├── chain_test.go  # Unit tests for chain operations
│   ├── evm.go         # EVM state + SimulateCall for cross-shard detection
│   ├── tracking_statedb.go  # StateDB wrapper that tracks accessed addresses
│   ├── receipt.go     # Transaction receipt storage
│   └── jsonrpc.go     # JSON-RPC compatibility (Foundry)
├── orchestrator/
│   ├── service.go     # HTTP handlers + block producer + vote collection
│   ├── chain.go       # Orchestrator Shard blockchain + vote tracking
│   ├── chain_test.go  # Unit tests for orchestrator chain
│   ├── simulator.go   # EVM simulation for cross-shard transactions
│   ├── statedb.go     # SimulationStateDB - EVM state interface for simulation
│   └── statefetcher.go # StateFetcher - fetches/caches state from State Shards
└── test/
    └── integration_test.go  # Integration tests for 2PC flow

cmd/
├── shard/main.go
└── orchestrator/main.go

contracts/              # Foundry project (normal Solidity)
scripts/                # Test scripts
```

## Testing

```bash
# Run all Go tests
go test ./...

# Run with verbose output
go test -v ./...

# Run specific package tests
go test -v ./internal/shard/...
go test -v ./internal/orchestrator/...
go test -v ./test/...

# Cross-shard transaction (integration)
./scripts/test-cross-shard.sh

# State sharding (contract on one shard)
./scripts/test-state-sharding.sh
```

## Development

```bash
# Build
go build ./...

# Test
go test ./...

# Contracts (Foundry)
cd contracts
forge test
forge build
```

## Design Decisions

1. **Blocks are the unit of coordination** - 2PC state in blocks, not memory
2. **State Shards are isolated** - No State ↔ State communication
3. **Minimal correctness** - Focus on 2PC mechanics, assume consensus solved
4. **Incremental complexity** - Blocks → 2PC in blocks → State proofs (future)
5. **Standalone EVM** - geth's vm + state packages, not full node

## Ports

| Service | Internal | External |
|---------|----------|----------|
| Orchestrator | 8080 | 8080 |
| Shard 0 | 8545 | 8545 |
| Shard 1 | 8545 | 8546 |
| Shard 2 | 8545 | 8547 |
| Shard 3 | 8545 | 8548 |
| Shard 4 | 8545 | 8549 |
| Shard 5 | 8545 | 8550 |

## TODOs and Open Issues

### Critical (Correctness)

1. **Vote Timeout Handling**
   - Currently, if a State Shard never sends its vote, the tx stays in `awaitingVotes` forever
   - Need: Timeout mechanism to abort stale transactions after N blocks
   - Location: `orchestrator/chain.go:awaitingVotes`

2. **Block Height Synchronization**
   - Shards and orchestrator produce blocks independently every 3s
   - No guarantee that TpcResult is processed before next CtToOrder
   - Need: Either block height references in TpcResult, or sequence numbers

3. ~~**Duplicate Vote Prevention**~~ ✅ COMPLETED
   - ~~Multiple State Shard blocks could contain the same TpcPrepare vote~~
   - Implementation: First vote wins - `RecordVote()` ignores duplicate votes from same shard

4. **Transaction Replay Protection**
   - Same tx ID could be submitted multiple times
   - Need: Track processed tx IDs and reject duplicates

### High Priority (Reliability)

5. ~~**Graceful Error Handling in Block Processing**~~ ✅ COMPLETED
   - Implementation: Transaction-level error handling with fetch error tracking in SimulationStateDB

6. **HTTP Endpoint Deprecation**
   - Old HTTP 2PC endpoints still exist (`/cross-shard/prepare`, `/commit`, `/abort`, `/credit`)
   - These are now only used for manual testing, not block-based 2PC
   - Decision: Keep for debugging or remove for clarity?

7. **Persistent State**
   - All state is in-memory (maps)
   - Need: Persistence layer for production (LevelDB/RocksDB)

### Medium Priority (Features)

8. **Per-Recipient Amount Distribution**
   - Current: Each RwSet entry gets the full `tx.Value` credited
   - Workaround: Multi-recipient credits work (list), but value isn't split
   - Next step: Add `Amount` field to `RwVariable` for explicit amounts

9. ~~**RwSet Validation**~~ ✅ COMPLETED
   - Implementation: ReadSet populated during simulation, validated by State Shards before voting
   - Location: `internal/orchestrator/statedb.go`, `internal/shard/server.go:validateRwVariable()`

10. **Merkle Proof Generation**
    - `ReadSetItem.Proof` is always empty (`[][]byte{}`)
    - Need: MPT/Verkle proof generation for cross-shard state reads

11. **Block Pruning**
    - All blocks are kept in memory forever
    - Need: Prune old blocks after finality

### Low Priority (Enhancements)

12. **Metrics and Monitoring**
    - No Prometheus metrics or structured logging
    - Need: Add metrics for tx latency, success rate, block production time

13. **Configuration Management**
    - Hardcoded values (3s block time, 6 shards, ports)
    - Need: Config file or environment-based configuration

14. ~~**Testing**~~ ✅ COMPLETED
    - Implementation: Unit tests for chain.go, integration tests for 2PC flow
    - Location: `internal/shard/chain_test.go`, `internal/orchestrator/chain_test.go`, `test/integration_test.go`

### Architectural Questions

- **Same-shard transfers via 2PC?**
  Currently, same-shard transfers use direct debit/credit. Cross-shard goes through 2PC.
  Should same-shard also use 2PC for consistency?

- ~~**Contract execution across shards?**~~ ✅ COMPLETED
  Implementation: EVM simulation discovers RwSet, all involved shards vote.
  Location: `internal/orchestrator/simulator.go`

- **Light client verification?**
  Orchestrator Shard trusts State Shard blocks without verification.
  How to add light client proofs without full consensus?
