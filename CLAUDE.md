# Ethereum Sharding Experiment

## Project Overview

Experimental blockchain sharding simulation focused on cross-shard communication and message passing. Each shard has a single validator (no consensus complexity).

## Architecture

- **6 Shard Nodes** (`shard-0` to `shard-5`): Independent state, Go-based
- **1 Orchestrator** (`shard-orch`): Coordinator with EVM simulation for cross-shard transactions

See `docs/architecture.md` for detailed implementation architecture.

## Key Directories

```
cmd/
├── shard/           # Shard node entry point
└── orchestrator/    # Orchestrator entry point

internal/
├── shard/           # Shard state management and HTTP server
│   ├── server.go        # HTTP handlers, unified /tx/submit endpoint
│   ├── server_test.go   # Unit tests for /tx/submit endpoint
│   ├── chain.go         # Block chain state
│   ├── chain_test.go    # Unit tests for chain operations
│   ├── evm.go           # EVM state + SimulateCall for cross-shard detection
│   └── tracking_statedb.go  # StateDB wrapper that tracks accessed addresses
├── orchestrator/    # Cross-shard coordination service
│   ├── service.go       # HTTP handlers, block producer
│   ├── chain.go         # Orchestrator chain state
│   ├── chain_test.go    # Unit tests for orchestrator chain
│   ├── simulator.go     # EVM simulation worker
│   ├── statedb.go       # vm.StateDB implementation for simulation
│   └── statefetcher.go  # State fetching/locking from shards
├── protocol/        # Shared message types for inter-shard communication
└── test/            # Integration tests
    └── integration_test.go

contracts/           # Foundry project (normal Solidity contracts)
scripts/             # Python test scripts
docs/                # Architecture documentation
```

## Documentation

**IMPORTANT: Keep documentation in sync with code changes.**

**After ANY progress (not just milestones), you MUST update ALL relevant files in docs/ directory immediately. Documentation should always reflect the current state of the codebase.**

When making changes to:
- **2PC protocol flow** → Update `docs/2pc-protocol.md`
- **Data structures** → Update `docs/architecture.md`
- **API endpoints** → Update `docs/architecture.md` and `README.md`
- **File structure** → Update `docs/architecture.md` and this file
- **Completing a TODO item** → Update `docs/TODO.md` (check off item, update status)
- **Any feature implementation** → Review and update all docs/ files for consistency

Documentation files:
- `docs/V2.md` - **V2 Protocol Specification** (target architecture)
- `docs/architecture.md` - System overview, data flow, file structure
- `docs/2pc-protocol.md` - Block-based 2PC protocol details
- `docs/TODO.md` - Design vs implementation gap analysis, implementation roadmap
- `docs/design.md` - Original design specification (Korean)
- `README.md` - User-facing docs, API reference, TODOs

## Docker Network

Services communicate via Docker DNS:
- `shard-0:8545`, `shard-1:8545`, ..., `shard-5:8545`
- `shard-orch:8080`

External ports: Orchestrator on 8080, shards on 8545-8550

## Git Workflow

**IMPORTANT:**
- **DO NOT push to GitHub** unless explicitly told to
- **DO NOT include co-author lines** in commits (no "Co-Authored-By: Claude" or similar)

## Development Notes

- Cross-shard communication uses **block-based 2PC** (not HTTP-based)
- Destinations derived from `RwSet` in `CrossShardTx` (no To/ToShard fields)
- Contracts are normal Ethereum contracts (no cross-shard logic in Solidity)
- All state is in-memory (no persistence)

**V2 Migration:** See `docs/V2.md` for the target protocol. Key differences:
- Entry point shifts to State Shard (of `To` address) → local simulation first
- Iterative re-execution with Merkle proofs for cross-shard state
- Explicit transaction types: `Finalize → Unlock → Lock → Local` ordering

## Commands

```bash
# Start network
docker compose up --build -d

# View logs
docker compose logs -f shard-0
docker compose logs -f shard-orch

# Stop
docker compose down

# Run Go tests
go test ./...
go test -v ./internal/shard/...
go test -v ./internal/orchestrator/...
go test -v ./test/...

# Test (Python - requires running network)
python scripts/test_cross_shard.py
python scripts/test_state_sharding.py

# Or use the runner
python scripts/run.py
```

## Tech Stack

- Go 1.25 + go-ethereum
- Foundry for contracts
- Docker Compose for orchestration
- Python 3 for test scripts

## Key Data Structures

```go
// Cross-shard transaction - destinations from RwSet
type CrossShardTx struct {
    ID        string
    FromShard int
    From      common.Address
    Value     *big.Int
    Data      []byte
    RwSet     []RwVariable  // Target shards/addresses (populated by simulation)
    Status    TxStatus
}

// Orchestrator Shard block
type OrchestratorShardBlock struct {
    TpcResult map[string]bool  // Commit decisions (previous round)
    CtToOrder []CrossShardTx   // New transactions (this round)
}

// State Shard block
type StateShardBlock struct {
    ShardID    int               // Which shard produced this block
    TpcPrepare map[string]bool   // Prepare votes
    StateRoot  common.Hash
}
```

## Transaction Flow

```
User submits POST /tx/submit to their State Shard
       │
       ▼ auto-detection
  ┌────┴────┐
  │         │
local    cross-shard
  │         │
  ▼         ▼
Execute   Forward to Orchestrator → 2PC
immediately
```

## 2PC Flow Summary (Cross-Shard Only)

```
Round N:
  Orchestrator Shard: CtToOrder=[tx1], TpcResult={}
       ↓ broadcast
  State Shards: lock (no debit), validate, vote → TpcPrepare={tx1:true}
       ↓ send blocks with ShardID
  Orchestrator: collect votes from ALL involved shards

Round N+1:
  Orchestrator Shard: CtToOrder=[tx2], TpcResult={tx1:true}
       ↓ broadcast
  State Shards: commit tx1 (debit+clear lock, apply credits), prepare tx2
```

**Key Features:**
- **Transparent routing**: Users submit to their shard, system detects cross-shard
- Lock-only 2PC: funds locked on prepare, debited on commit (no refund needed on abort)
- Multi-shard voting: all involved shards (source + destinations) must vote
- First NO vote aborts immediately; commits when all expected shards vote YES

See `docs/2pc-protocol.md` for detailed protocol documentation.
