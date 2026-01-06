# 2PC with EVM Calls - Complete Demonstration

This document demonstrates the Two-Phase Commit (2PC) protocol for cross-shard transactions with EVM contract calls. It traces through the complete lifecycle with concrete examples and expected outputs.

---

## Table of Contents

1. [Overview](#overview)
2. [Setup](#setup)
3. [Scenario 1: Simple Cross-Shard Transfer](#scenario-1-simple-cross-shard-transfer)
4. [Scenario 2: Cross-Shard Contract Call](#scenario-2-cross-shard-contract-call)
5. [Scenario 3: Abort Flow](#scenario-3-abort-flow)
6. [Log Output Reference](#log-output-reference)
7. [State Transitions Summary](#state-transitions-summary)

---

## Overview

The 2PC protocol ensures atomic cross-shard transactions:

```
┌─────────────────────────────────────────────────────────────────┐
│                    COMPLETE 2PC LIFECYCLE                        │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  User → Shard → Orchestrator → Simulation → 2PC → Commit/Abort  │
│                                                                  │
│  Timeline (minimum):                                             │
│  T+0s:  User submits tx                                         │
│  T+0s:  Shard detects cross-shard, forwards to orchestrator     │
│  T+1s:  Simulation runs, RwSet discovered                       │
│  T+3s:  Orchestrator block with CtToOrder (PREPARE phase)       │
│  T+3s:  Shards lock funds and vote                              │
│  T+6s:  Orchestrator collects votes                             │
│  T+6s:  Orchestrator block with TpcResult (COMMIT/ABORT)        │
│  T+6s:  Shards queue typed transactions                         │
│  T+9s:  Shards execute in ProduceBlock                          │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**Key Components:**
- **6 State Shards**: Each manages a partition of addresses (addr % 6)
- **1 Orchestrator**: Coordinates cross-shard transactions
- **EVM Simulator**: Pre-executes to discover RwSet

---

## Setup

### Start the Network

```bash
# Start all containers
docker compose up --build -d

# Verify health
curl http://localhost:8080/health
# Output: {"status":"ok","pending_txs":0,"awaiting_votes":0}

curl http://localhost:8545/health
# Output: {"status":"ok","shard_id":0,"height":1}
```

### Test Addresses

Addresses map to shards via `last_byte % 6`:

| Address | Shard |
|---------|-------|
| `0x0000000000000000000000000000000000000000` | 0 |
| `0x0000000000000000000000000000000000000001` | 1 |
| `0x0000000000000000000000000000000000000002` | 2 |
| `0x1234567890123456789012345678901234567890` | 0 (0x90 % 6 = 0) |
| `0xabcdabcdabcdabcdabcdabcdabcdabcdabcdab01` | 1 (0x01 % 6 = 1) |

---

## Scenario 1: Simple Cross-Shard Transfer

**Goal**: Transfer 500 wei from address on Shard 0 to address on Shard 1.

### Step 1: Fund Source Account

```bash
curl -X POST http://localhost:8545/faucet \
  -H "Content-Type: application/json" \
  -d '{"address": "0x0000000000000000000000000000000000000000", "amount": "1000"}'
```

**Expected Output:**
```json
{"success": true, "balance": "1000"}
```

**Shard 0 Log:**
```
Shard 0: Faucet credited 1000 to 0x0000000000000000000000000000000000000000
```

### Step 2: Verify Initial Balances

```bash
# Source (Shard 0)
curl http://localhost:8545/balance/0x0000000000000000000000000000000000000000
# Output: {"balance": "1000"}

# Destination (Shard 1)
curl http://localhost:8546/balance/0x0000000000000000000000000000000000000001
# Output: {"balance": "0"}
```

### Step 3: Submit Transaction via /tx/submit

```bash
curl -X POST http://localhost:8545/tx/submit \
  -H "Content-Type: application/json" \
  -d '{
    "from": "0x0000000000000000000000000000000000000000",
    "to": "0x0000000000000000000000000000000000000001",
    "value": "500",
    "gas": 21000
  }'
```

**Expected Output:**
```json
{
  "success": true,
  "tx_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "status": "pending",
  "cross_shard": true
}
```

**Shard 0 Log (Detection):**
```
Shard 0: Received tx/submit from=0x0000...0000 to=0x0000...0001 value=500
Shard 0: Detected cross-shard tx (to shard 1 != from shard 0)
Shard 0: Forwarding to orchestrator
```

### Step 4: Orchestrator Receives and Queues for Simulation

**Orchestrator Log:**
```
Orchestrator: Received cross-shard call tx a1b2c3d4-...
Orchestrator: Queuing tx a1b2c3d4-... for simulation
Simulator: Queued tx a1b2c3d4-... for simulation
```

### Step 5: Simulation Runs

**Orchestrator Log:**
```
Simulator: Running simulation for tx a1b2c3d4-...
StateFetcher: Locking address 0x0000...0000 on shard 0 for tx a1b2c3d4-...
StateFetcher: Locking address 0x0000...0001 on shard 1 for tx a1b2c3d4-...
Simulator: Tx a1b2c3d4-... succeeded, gas used: 21000
Simulation complete for a1b2c3d4-..., RwSet has 2 entries
```

### Step 6: Orchestrator Produces Block (PREPARE Phase)

**Orchestrator Log:**
```
Orchestrator: Producing block 1
Orchestrator: Block 1 has 1 CtToOrder, 0 TpcResult
Orchestrator: Broadcasting block to 8 shards
```

**Block Content:**
```json
{
  "height": 1,
  "ct_to_order": [
    {
      "id": "a1b2c3d4-...",
      "from_shard": 0,
      "from": "0x0000...0000",
      "to": "0x0000...0001",
      "value": "500",
      "rw_set": [
        {"address": "0x0000...0000", "reference_block": {"shard_num": 0}},
        {"address": "0x0000...0001", "reference_block": {"shard_num": 1}}
      ]
    }
  ],
  "tpc_result": {}
}
```

### Step 7: Shards Process Block and Vote

**Shard 0 Log (Source - Lock Funds):**
```
Shard 0: Received Orchestrator Shard block 1 with 1 txs, 0 results
Shard 0: Processing CtToOrder for tx a1b2c3d4-...
Shard 0: I am source shard (FromShard=0)
Shard 0: Checking balance: available=1000, required=500
Shard 0: Prepare a1b2c3d4-... = true (lock 500 from 0x0000...0000)
```

**Shard 1 Log (Destination - Validate RwSet):**
```
Shard 1: Received Orchestrator Shard block 1 with 1 txs, 0 results
Shard 1: Processing CtToOrder for tx a1b2c3d4-...
Shard 1: Validate RwSet for a1b2c3d4-... on 0x0000...0001 = true
Shard 1: Combined RwSet vote for a1b2c3d4-... = true (1 entries)
Shard 1: Pending credit a1b2c3d4-... for 0x0000...0001 (value=500)
```

### Step 8: Shards Produce Blocks with Votes

**Shard 0 Block:**
```json
{
  "shard_id": 0,
  "height": 2,
  "tpc_prepare": {"a1b2c3d4-...": true},
  "tx_ordering": []
}
```

**Shard 1 Block:**
```json
{
  "shard_id": 1,
  "height": 2,
  "tpc_prepare": {"a1b2c3d4-...": true},
  "tx_ordering": []
}
```

### Step 9: Orchestrator Collects Votes

**Orchestrator Log:**
```
Orchestrator: Received state shard block from shard 0
Orchestrator: Recording vote for a1b2c3d4-... from shard 0: true
Orchestrator: Received state shard block from shard 1
Orchestrator: Recording vote for a1b2c3d4-... from shard 1: true
Orchestrator: All 2 expected shards voted YES for a1b2c3d4-..., COMMIT
```

### Step 10: Orchestrator Produces Block (COMMIT Phase)

**Orchestrator Log:**
```
Orchestrator: Producing block 2
Orchestrator: Block 2 has 0 CtToOrder, 1 TpcResult
Orchestrator: Broadcasting block to 8 shards
```

**Block Content:**
```json
{
  "height": 2,
  "ct_to_order": [],
  "tpc_result": {"a1b2c3d4-...": true}
}
```

### Step 11: Shards Queue Typed Transactions

**Shard 0 Log (Source - Queue Debit):**
```
Shard 0: Received Orchestrator Shard block 2 with 0 txs, 1 results
Shard 0: Processing TpcResult: a1b2c3d4-... = COMMIT
Shard 0: Queued debit for a1b2c3d4-... (500 from 0x0000...0000)
```

**Shard 1 Log (Destination - Queue Credit):**
```
Shard 1: Received Orchestrator Shard block 2 with 0 txs, 1 results
Shard 1: Processing TpcResult: a1b2c3d4-... = COMMIT
Shard 1: Queued credit for a1b2c3d4-... (500 to 0x0000...0001)
```

### Step 12: Shards Execute in ProduceBlock

**Shard 0 Log:**
```
Chain 0: ProduceBlock executing 1 transactions
Chain 0: Executing tx (type=cross_debit, id=debit-xxx)
Chain 0: Debited 500 from 0x0000...0000
Chain 0: Cleared lock for a1b2c3d4-...
```

**Shard 1 Log:**
```
Chain 1: ProduceBlock executing 1 transactions
Chain 1: Executing tx (type=cross_credit, id=credit-xxx)
Chain 1: Credited 500 to 0x0000...0001
Chain 1: Cleared pending credit for a1b2c3d4-... to 0x0000...0001
```

### Step 13: Verify Final Balances

```bash
# Source (Shard 0) - should be 500
curl http://localhost:8545/balance/0x0000000000000000000000000000000000000000
# Output: {"balance": "500"}

# Destination (Shard 1) - should be 500
curl http://localhost:8546/balance/0x0000000000000000000000000000000000000001
# Output: {"balance": "500"}
```

### Final State

| Account | Shard | Before | After |
|---------|-------|--------|-------|
| 0x0000...0000 | 0 | 1000 | 500 |
| 0x0000...0001 | 1 | 0 | 500 |

---

## Scenario 2: Cross-Shard Contract Call

**Goal**: Caller on Shard 1 calls a Storage contract on Shard 0 to set a value.

### Step 1: Deploy Contract on Shard 0

```bash
# Fund deployer
curl -X POST http://localhost:8545/faucet \
  -H "Content-Type: application/json" \
  -d '{"address": "0x0000000000000000000000000000000000000000", "amount": "10000000000000000000"}'

# Deploy Storage contract
# contract Storage { uint256 public value; function set(uint256 v) { value = v; } }
curl -X POST http://localhost:8545/evm/deploy \
  -H "Content-Type: application/json" \
  -d '{
    "from": "0x0000000000000000000000000000000000000000",
    "bytecode": "0x6080604052348015600f57600080fd5b5060ac8061001e6000396000f3fe6080604052348015600f57600080fd5b506004361060325760003560e01c806360fe47b11460375780636d4ce63c146049575b600080fd5b60476042366004605e565b600055565b005b60005460405190815260200160405180910390f35b600060208284031215606f57600080fd5b503591905056fea264697066735822122000000000000000000000000000000000000000000000000000000000000000006473",
    "gas": 500000
  }'
```

**Expected Output:**
```json
{
  "success": true,
  "address": "0x5fbdb2315678afecb367f032d93f642f64180aa3",
  "target_shard": 3
}
```

### Step 2: Fund Caller on Shard 1

```bash
curl -X POST http://localhost:8546/faucet \
  -H "Content-Type: application/json" \
  -d '{"address": "0x0000000000000000000000000000000000000001", "amount": "10000000000000000000"}'
```

### Step 3: Submit Cross-Shard Contract Call

```bash
# set(42) = 0x60fe47b1 + uint256(42)
curl -X POST http://localhost:8546/tx/submit \
  -H "Content-Type: application/json" \
  -d '{
    "from": "0x0000000000000000000000000000000000000001",
    "to": "0x5fbdb2315678afecb367f032d93f642f64180aa3",
    "value": "0",
    "gas": 100000,
    "data": "0x60fe47b1000000000000000000000000000000000000000000000000000000000000002a"
  }'
```

**Expected Output:**
```json
{
  "success": true,
  "tx_id": "contract-call-tx-id",
  "status": "pending",
  "cross_shard": true
}
```

### Step 4: Simulation Discovers Storage Access

**Orchestrator Log:**
```
Simulator: Running simulation for tx contract-call-tx-id
StateFetcher: Locking address 0x0000...0001 on shard 1 for tx contract-call-tx-id
StateFetcher: Locking address 0x5fbd...0aa3 on shard 3 for tx contract-call-tx-id
Simulator: EVM executing set(42) on Storage contract
Simulator: Storage SLOAD slot 0x0 = 0x0 (read for SSTORE)
Simulator: Storage SSTORE slot 0x0 = 0x2a (42)
Simulator: Tx contract-call-tx-id succeeded, gas used: 43524
Simulation complete, RwSet:
  - Address 0x5fbd...0aa3 (shard 3):
    ReadSet:  [slot=0x0, value=0x0]
    WriteSet: [slot=0x0, old=0x0, new=0x2a]
```

### Step 5: 2PC Commit with WriteSet Application

**Shard 3 Log (Contract Shard - On Commit):**
```
Shard 3: Received Orchestrator Shard block 2 with 0 txs, 1 results
Shard 3: Processing TpcResult: contract-call-tx-id = COMMIT
Shard 3: Queued writeset for contract-call-tx-id (1 storage writes)

Chain 3: ProduceBlock executing 1 transactions
Chain 3: Executing tx (type=cross_writeset, id=writeset-xxx)
Chain 3: Applying WriteSet for contract-call-tx-id
Chain 3: SetStorageAt(0x5fbd...0aa3, slot=0x0, value=0x2a)
Chain 3: Cleared pending call for contract-call-tx-id
```

### Step 6: Verify Storage Value Changed

```bash
# Read storage slot 0 via static call with get() = 0x6d4ce63c
curl -X POST http://localhost:8548/evm/static-call \
  -H "Content-Type: application/json" \
  -d '{
    "from": "0x0000000000000000000000000000000000000000",
    "to": "0x5fbdb2315678afecb367f032d93f642f64180aa3",
    "data": "0x6d4ce63c",
    "gas": 100000
  }'
```

**Expected Output:**
```json
{
  "success": true,
  "return": "0x000000000000000000000000000000000000000000000000000000000000002a"
}
```

The storage value is now `0x2a` (42 in decimal).

---

## Scenario 3: Abort Flow

**Goal**: Demonstrate what happens when a shard votes NO.

### Step 1: Setup - Sender Has Insufficient Funds

```bash
# Sender with only 100 wei
curl -X POST http://localhost:8545/faucet \
  -H "Content-Type: application/json" \
  -d '{"address": "0x0000000000000000000000000000000000000000", "amount": "100"}'
```

### Step 2: Submit Transfer Exceeding Balance

```bash
curl -X POST http://localhost:8545/tx/submit \
  -H "Content-Type: application/json" \
  -d '{
    "from": "0x0000000000000000000000000000000000000000",
    "to": "0x0000000000000000000000000000000000000001",
    "value": "500",
    "gas": 21000
  }'
```

### Step 3: Source Shard Votes NO

**Shard 0 Log:**
```
Shard 0: Received Orchestrator Shard block 1 with 1 txs, 0 results
Shard 0: Processing CtToOrder for tx abort-tx-id
Shard 0: I am source shard (FromShard=0)
Shard 0: Checking balance: available=100, required=500
Shard 0: Prepare abort-tx-id = false (lock 500 from 0x0000...0000)
```

**Shard 0 Block:**
```json
{
  "shard_id": 0,
  "tpc_prepare": {"abort-tx-id": false}
}
```

### Step 4: Orchestrator Aborts on First NO Vote

**Orchestrator Log:**
```
Orchestrator: Recording vote for abort-tx-id from shard 0: false
Orchestrator: First NO vote for abort-tx-id, immediate ABORT
```

### Step 5: Shards Queue Abort Transactions

**Shard 0 Log:**
```
Shard 0: Received Orchestrator Shard block 2 with 0 txs, 1 results
Shard 0: Processing TpcResult: abort-tx-id = ABORT
Shard 0: Queued abort for abort-tx-id
```

**Shard 1 Log:**
```
Shard 1: Received Orchestrator Shard block 2 with 0 txs, 1 results
Shard 1: Processing TpcResult: abort-tx-id = ABORT
Shard 1: No pending data for abort-tx-id (never prepared)
```

### Step 6: Cleanup in ProduceBlock

**Shard 0 Log:**
```
Chain 0: ProduceBlock executing 1 transactions
Chain 0: Executing tx (type=cross_abort, id=abort-xxx)
Chain 0: Aborted and cleared all metadata for abort-tx-id
```

### Step 7: Verify No State Changed

```bash
# Source balance unchanged (still 100)
curl http://localhost:8545/balance/0x0000000000000000000000000000000000000000
# Output: {"balance": "100"}

# Destination balance unchanged (still 0)
curl http://localhost:8546/balance/0x0000000000000000000000000000000000000001
# Output: {"balance": "0"}
```

---

## Log Output Reference

### Transaction Type Labels in Logs

| TxType | Log Label | Description |
|--------|-----------|-------------|
| `""` (local) | `type=local` | Normal EVM execution |
| `cross_debit` | `type=cross_debit` | Debit locked funds from source |
| `cross_credit` | `type=cross_credit` | Credit pending amount to destination |
| `cross_writeset` | `type=cross_writeset` | Apply storage writes |
| `cross_abort` | `type=cross_abort` | Cleanup metadata only |

### Key Log Patterns

**Detection:**
```
Shard X: Detected cross-shard tx (to shard Y != from shard X)
Shard X: Detected cross-shard access via simulation (addresses: [...])
```

**Simulation:**
```
Simulator: Queued tx TX_ID for simulation
Simulator: Running simulation for tx TX_ID
Simulator: Tx TX_ID succeeded, gas used: NNNN
Simulator: Tx TX_ID failed: ERROR_MESSAGE
```

**Prepare Phase:**
```
Shard X: Prepare TX_ID = true/false (lock AMOUNT from ADDRESS)
Shard X: Validate RwSet for TX_ID on ADDRESS = true/false
Shard X: Combined RwSet vote for TX_ID = true/false (N entries)
Shard X: Pending credit TX_ID for ADDRESS (value=AMOUNT)
```

**Commit/Abort Phase:**
```
Shard X: Queued debit for TX_ID (AMOUNT from ADDRESS)
Shard X: Queued credit for TX_ID (AMOUNT to ADDRESS)
Shard X: Queued writeset for TX_ID (N storage writes)
Shard X: Queued abort for TX_ID
```

**Execution:**
```
Chain X: ProduceBlock executing N transactions
Chain X: Executing tx (type=TYPE, id=ID)
Chain X: Cleared lock for TX_ID
Chain X: Cleared pending credit for TX_ID to ADDRESS
Chain X: Cleared pending call for TX_ID
Chain X: Aborted and cleared all metadata for TX_ID
```

---

## State Transitions Summary

### Transaction Status Flow

```
pending → prepared → committed
                  └→ aborted
```

### Shard-Side State

| Phase | Source Shard | Destination Shard |
|-------|--------------|-------------------|
| **Prepare** | LockFunds(txID, addr, amount) | StorePendingCredit(txID, addr, amount) |
|             | Vote YES if lock succeeded | StorePendingCall(txID, tx) |
|             | Vote NO if insufficient balance | Vote YES if ReadSet valid |
| **Commit** | Queue TxTypeCrossDebit | Queue TxTypeCrossCredit |
|            |                        | Queue TxTypeCrossWriteSet |
| **Execute** | Debit locked amount | Credit pending amount |
|             | ClearLock | Apply WriteSet, ClearPendingCall |
| **Abort** | Queue TxTypeCrossAbort | Queue TxTypeCrossAbort |
|           | Clear all metadata | Clear all metadata |

### Timing Guarantees

- Block production: Every 3 seconds (both orchestrator and shards)
- Minimum commit time: 6 seconds (2 block cycles)
- Typical commit time: 6-9 seconds
- Simulation lock TTL: 2 minutes (prevents deadlocks)

---

## Running the Demo

### Using Python Test Scripts

```bash
# Simple cross-shard transfer
python scripts/test_simulation.py

# Cross-shard contract call
python scripts/test_cross_shard_contract.py

# Full state sharding test
python scripts/test_state_sharding.py
```

### Using Go Integration Tests

```bash
# Run all integration tests
go test -v ./test/...

# Run specific test
go test -v ./test/... -run TestCrossShardTx_Simulation
```

### Manual Testing with curl

Follow the step-by-step commands in Scenarios 1-3 above.

---

**Document Version:** 1.0
**Last Updated:** 2025-12-18
