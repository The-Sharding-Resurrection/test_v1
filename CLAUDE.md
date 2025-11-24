# Ethereum Sharding Experiment

## Project Overview

Experimental blockchain sharding simulation focused on cross-shard communication and message passing. Each shard has a single validator (no consensus complexity).

## Architecture

- **6 Shard Nodes** (`shard-0` to `shard-5`): Independent state, Go-based
- **1 Orchestrator** (`shard-orch`): Stateless coordinator for cross-shard transactions

## Key Directories

- `cmd/shard/` - Shard node entry point
- `cmd/orchestrator/` - Orchestrator entry point
- `internal/shard/` - Shard state management and HTTP server
- `internal/orchestrator/` - Cross-shard coordination service
- `internal/protocol/` - Shared message types for inter-shard communication
- `contracts/` - Foundry project for normal Solidity contracts (no cross-shard logic in contracts)

## Docker Network

Services communicate via Docker DNS:
- `shard-0:8545`, `shard-1:8545`, ..., `shard-5:8545`
- `shard-orch:8080`

External ports: Orchestrator on 8080, shards on 8545-8550

## Development Notes

- Cross-shard communication logic is handled at the node level, not in smart contracts
- Contracts are written as normal Ethereum contracts
- User will guide cross-shard implementation details

## Commands

```bash
# Start network
docker compose up --build -d

# View logs
docker compose logs -f shard-0
docker compose logs -f shard-orch

# Stop
docker compose down

# Test
./scripts/test-cross-shard.sh
```

## Tech Stack

- Go 1.21 + go-ethereum
- Foundry for contracts
- Docker Compose for orchestration
