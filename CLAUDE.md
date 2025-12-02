# Ethereum Sharding Experiment

## Project Overview

Experimental blockchain sharding simulation focused on cross-shard communication and message passing. Each shard has a single validator (no consensus complexity).

## Architecture

- **6 Shard Nodes** (`shard-0` to `shard-5`): Independent state, Go-based
- **1 Orchestrator** (`shard-orch`): Stateless coordinator for cross-shard transactions

See `docs/architecture.md` for detailed implementation architecture.

## Key Directories

```
cmd/
├── shard/           # Shard node entry point
└── orchestrator/    # Orchestrator entry point

internal/
├── shard/           # Shard state management and HTTP server
├── orchestrator/    # Cross-shard coordination service
└── protocol/        # Shared message types for inter-shard communication

contracts/           # Foundry project (normal Solidity contracts)
scripts/             # Python test scripts
docs/                # Architecture documentation
```

## Documentation

**IMPORTANT: Keep documentation in sync with code changes.**

**After completing any milestone or implementing a feature, you MUST update ALL relevant files in docs/ directory to keep documentation up-to-date at all times.**

When making changes to:
- **2PC protocol flow** → Update `docs/2pc-protocol.md`
- **Data structures** → Update `docs/architecture.md`
- **API endpoints** → Update `docs/architecture.md` and `README.md`
- **File structure** → Update `docs/architecture.md` and this file
- **Completing a TODO item** → Update `docs/TODO.md` (check off item, update status)
- **Any feature implementation** → Review and update all docs/ files for consistency

Documentation files:
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

## Development Notes

- Cross-shard communication uses **block-based 2PC** (not HTTP-based)
- Destinations derived from `RwSet` in `CrossShardTx` (no To/ToShard fields)
- Contracts are normal Ethereum contracts (no cross-shard logic in Solidity)
- All state is in-memory (no persistence)

## Commands

```bash
# Start network
docker compose up --build -d

# View logs
docker compose logs -f shard-0
docker compose logs -f shard-orch

# Stop
docker compose down

# Test (Python)
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
    RwSet     []RwVariable  // Target shards/addresses
    Status    TxStatus
}

// Orchestrator Shard block
type OrchestratorShardBlock struct {
    TpcResult map[string]bool  // Commit decisions (previous round)
    CtToOrder []CrossShardTx   // New transactions (this round)
}

// State Shard block
type StateShardBlock struct {
    TpcPrepare map[string]bool  // Prepare votes
    StateRoot  common.Hash
}
```

## 2PC Flow Summary

```
Round N:
  Orchestrator Shard: CtToOrder=[tx1], TpcResult={}
       ↓ broadcast
  State Shards: debit, lock, vote → TpcPrepare={tx1:true}
       ↓ send blocks

Round N+1:
  Orchestrator Shard: CtToOrder=[tx2], TpcResult={tx1:true}
       ↓ broadcast
  State Shards: commit tx1 (clear lock, apply credit), prepare tx2
```

See `docs/2pc-protocol.md` for detailed protocol documentation.
