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

- **Contract Shard** (Orchestrator): Coordinates cross-shard transactions via 2PC
  - Produces blocks with `tpc_result` (commit decisions) and `ct_to_order` (new txs)
  - Broadcasts blocks to all State Shards
  - **Stateless**: No account balances or contract storage

- **State Shards** (0-5): Independent blockchain state
  - Produce blocks with `tx_ordering` (executed txs) and `tpc_prepare` (2PC votes)
  - Send blocks to Contract Shard
  - Maintain EVM state (balances, contracts, storage)

### Block-Based 2PC Flow

```
Round N:
  Contract Shard Block N: ct_to_order = [tx1, tx2]
         ↓ broadcast to all State Shards
  State Shards: validate, execute, prepare
         ↓ send blocks to Contract Shard
  State Shard Blocks: tpc_prepare = {tx1: true, tx2: false}

Round N+1:
  Contract Shard Block N+1: tpc_result = {tx1: true, tx2: false}
         ↓ broadcast
  State Shards: commit tx1, abort tx2
```

**Key**: 2PC state lives in blocks, not HTTP responses.

### Communication Pattern

```
Contract Shard → State Shards (broadcast)
State Shards → Contract Shard (unicast)
State Shards ↮ State Shards (NONE - isolated)
```

## Implementation Status

### ✅ Implemented

- Block-based 2PC coordination
- State sharding (contracts on one shard, state distributed by address % 6)
- Cross-shard atomic transactions
- EVM execution (geth's vm + state packages)
- JSON-RPC compatibility (Foundry tools)

### ⚠️ Deferred (Documented Assumptions)

**Merkle Proof Validation**:
- ReadSetItem.Proof field exists but empty (`[][]byte{}`)
- Trust StateShardBlock.StateRoot without cryptographic verification
- **Rationale**: Focus on 2PC mechanics before cryptographic validation
- **Future**: Implement MPT/Verkle proof generation and validation

**Light Client Protocol**:
- Contract Shard trusts State Shard blocks without verification
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
# Balance
GET http://shard-0:8545/balance/0x1234...

# Local transfer
POST http://shard-0:8545/transfer
{"from": "0x...", "to": "0x...", "amount": "1000"}

# Faucet
POST http://shard-0:8545/faucet
{"address": "0x...", "amount": "1000000000000000000000"}

# Cross-shard transfer
POST http://shard-0:8545/cross-shard/transfer
{"from": "0x...", "to": "0x...", "to_shard": 1, "amount": "100000000000000000"}

# EVM
POST http://shard-0:8545/evm/deploy
{"from": "0x...", "bytecode": "0x608060...", "gas": 3000000}

POST http://shard-0:8545/evm/call
{"from": "0x...", "to": "0x...", "data": "0xa9059cbb...", "gas": 1000000}

# JSON-RPC (Foundry compatible)
POST http://shard-0:8545/
{"jsonrpc": "2.0", "method": "eth_sendTransaction", "params": [...], "id": 1}
```

### Contract Shard Endpoints

```bash
# Transaction status
GET http://shard-orch:8080/cross-shard/status/{txid}

# Shard list
GET http://shard-orch:8080/shards

# Health
GET http://shard-orch:8080/health
```

## Data Structures

```go
// CrossShardTransaction (design.md compliant)
type CrossShardTransaction struct {
    TxHash common.Hash
    From   common.Address
    To     common.Address
    Value  *big.Int
    Data   []byte
    RwSet  []RwVariable  // ReadSet/WriteSet
}

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

// Contract Shard Block
type ContractShardBlock struct {
    Height    uint64
    PrevHash  BlockHash
    Timestamp uint64
    TpcResult map[string]bool              // txID → committed
    CtToOrder []CrossShardTransaction      // New cross-shard txs
}

// State Shard Block
type StateShardBlock struct {
    Height     uint64
    PrevHash   BlockHash
    Timestamp  uint64
    StateRoot  common.Hash                 // Merkle root
    TxOrdering []TxRef                     // Executed txs
    TpcPrepare map[string]bool             // txID → can_commit
}
```

## File Structure

```
internal/
├── protocol/
│   ├── types.go       # ReadSet/WriteSet structures
│   └── block.go       # Block definitions
├── shard/
│   ├── server.go      # HTTP handlers + block producer
│   ├── chain.go       # State Shard blockchain
│   ├── evm.go         # Standalone EVM state
│   ├── state.go       # LockManager (2PC lock tracking)
│   └── jsonrpc.go     # JSON-RPC compatibility
└── orchestrator/
    ├── service.go     # HTTP handlers + block producer
    └── chain.go       # Contract Shard blockchain

cmd/
├── shard/main.go
└── orchestrator/main.go

contracts/              # Foundry project (normal Solidity)
scripts/                # Test scripts
```

## Testing

```bash
# Cross-shard transaction
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
