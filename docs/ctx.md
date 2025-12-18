# Cross-Shard Transaction Design

This document provides a comprehensive specification of all transaction types and their execution flows in the sharding system.

---

## Table of Contents

1. [Transaction Types](#transaction-types)
2. [Transaction Submission and Cross-Shard Detection](#transaction-submission-and-cross-shard-detection)
3. [Data Structures](#data-structures)
4. [State Transitions](#state-transitions)
5. [Execution Flow](#execution-flow)
6. [Unified Transaction Model (Implemented)](#unified-transaction-model-implemented)
7. [Design Principles](#design-principles)

---

## Transaction Types

The system supports four fundamental transaction types:

### 1. Local Transaction
**Purpose:** Execute a transaction where all state is within a single shard.

**When Used:**
- Simple transfers between accounts on the same shard
- Contract calls that only access local state
- Contract deployments (forwarded to correct shard if needed)

**Key Properties:**
- No cross-shard communication required
- Executed immediately in ProduceBlock
- Can fail and be reverted without affecting other shards
- Includes snapshot/rollback mechanism for failed executions

### 2. Cross-Shard Prepare (Lock Phase)
**Purpose:** Lock resources on source/destination shards for a pending cross-shard transaction.

**When Used:**
- When Orchestrator broadcasts CtToOrder with new cross-shard transactions
- Source shard locks funds without debiting
- Destination shards validate ReadSet and store pending credits

**Key Properties:**
- Atomic check-and-lock operation (prevents TOCTOU races)
- Lock-only approach: no debit occurs during prepare
- Multiple RwSet entries on same shard produce single vote
- ReadSet validation ensures serializability

### 3. Cross-Shard Commit
**Purpose:** Finalize a cross-shard transaction that received unanimous YES votes.

**When Used:**
- When Orchestrator broadcasts TpcResult with commit decision
- Source shard debits locked funds
- Destination shards apply pending credits
- WriteSet is applied to storage

**Key Properties:**
- Debit occurs only on commit (not prepare)
- Multiple pending credits can be applied per transaction
- WriteSet values come from simulation (no re-execution)
- Simulation locks are released after commit

### 4. Cross-Shard Abort
**Purpose:** Roll back a cross-shard transaction that received at least one NO vote.

**When Used:**
- When Orchestrator broadcasts TpcResult with abort decision
- Any prepare vote was NO, or timeout occurred

**Key Properties:**
- Source shard clears lock without debit (balance unchanged)
- Destination shards discard pending credits
- No refund needed (lock-only approach)
- Simulation locks are released after abort

---

## Transaction Submission and Cross-Shard Detection

This section specifies how transactions are submitted, how cross-shard transactions are detected, and what responses users receive.

### Submission Endpoint

All transactions are submitted via `POST /tx/submit` to the user's home shard (determined by `from_address % num_shards`).

**Request Format:**
```json
{
  "from": "0x1234567890123456789012345678901234567890",
  "to": "0x2345678901234567890123456789012345678901",
  "value": "1000000000000000000",
  "gas": 100000,
  "data": "0x..."
}
```

**Current Response Format (server.go lines 1081-1086):**
```json
// Local transaction success
{
  "success": true,
  "tx_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "queued",
  "cross_shard": false
}

// Cross-shard forwarded (orchestrator response + cross_shard flag)
{
  "success": true,
  "tx_id": "...",
  "status": "pending",
  "cross_shard": true
}

// Error
{
  "success": false,
  "error": "error message",
  "cross_shard": false
}
```

| success | cross_shard | Meaning |
|---------|-------------|---------|
| `true` | `false` | Local transaction, queued for next block |
| `true` | `true` | Cross-shard detected, forwarded to orchestrator |
| `false` | `false` | Validation/execution failed locally |

### Cross-Shard Detection Criteria

Detection happens **at submission time** on the source shard, before any state changes.

**Implementation:** `handleTxSubmit` in `server.go` lines 976-1033

#### Method 1: Address-Based Detection (Simple Transfers)

```go
// server.go lines 976-992
toShard := int(to[len(to)-1]) % NumShards

if toShard != s.shardID {
    // Simple case: recipient is on another shard
    isCrossShard = true
    crossShardAddrs = map[common.Address]int{to: toShard}
}
```

**Applies when:** Any transaction where `to` address maps to different shard.

#### Method 2: Simulation-Based Detection (Contract Calls)

```go
// server.go lines 993-1032
toCode := s.evmState.GetCode(to)
isContract := len(toCode) > 0

if isContract && len(data) > 0 {
    // Contract call on this shard - simulate to detect cross-shard access
    _, accessedAddrs, hasCrossShard, simErr := s.evmState.SimulateCall(
        from, to, data, value, simGas, s.shardID, NumShards)

    if simErr != nil {
        if isDefiniteLocalError(simErr.Error()) {
            // Sender-related error (balance, nonce) - fail locally
            simulationErr = simErr
        } else {
            // Unknown error - forward to orchestrator (conservative)
            isCrossShard = true
        }
    } else if hasCrossShard {
        isCrossShard = true
        // Build map of cross-shard addresses from accessedAddrs
    }
}
```

**Applies when:** `to` is a contract on this shard AND `data` is non-empty.

**SimulateCall** (evm.go lines 411-461):
1. Creates snapshot for rollback
2. Wraps StateDB with `TrackingStateDB` to monitor address access
3. Executes EVM call
4. Reverts state (simulation only)
5. Returns accessed addresses and cross-shard flag

**TrackingStateDB** tracks:
- All addresses accessed via `GetBalance`, `GetCode`, `GetState`, etc.
- Flags cross-shard if any accessed address maps to different shard

#### Error Classification (isDefiniteLocalError)

```go
// server.go lines 1147-1178
// Only sender-related errors are definite local failures:
localErrors := []string{
    "insufficient balance",  // Sender doesn't have enough funds
    "insufficient funds",    // Same as above
    "nonce too low",         // Sender's nonce is wrong
    "nonce too high",        // Sender's nonce is wrong
}

// NOT considered local errors (forwarded to orchestrator):
// - "execution reverted" - contract might be on another shard
// - "invalid opcode" - might be calling non-existent cross-shard contract
// - "out of gas" - might succeed with proper cross-shard state
```

**Rationale:** The sender is always on the local shard, so sender-related errors are definitive. Other errors might be due to missing cross-shard state.

### Detection Outcomes

| Scenario | Detection Result | Action |
|----------|------------------|--------|
| `to` on different shard | Cross-shard | Forward to orchestrator |
| `to` local, no data | Local | Queue locally |
| `to` local, not a contract, has data | Local | Queue locally (will fail) |
| `to` local contract, simulation OK, no cross-shard access | Local | Queue locally |
| `to` local contract, simulation detects cross-shard access | Cross-shard | Forward to orchestrator |
| `to` local contract, simulation fails (sender error) | Local Error | Return error |
| `to` local contract, simulation fails (other error) | Cross-shard | Forward to orchestrator |

**Conservative approach:** When in doubt, forward to orchestrator. Better to have orchestrator reject than miss cross-shard access.

### What Happens at Each Stage

#### Stage 1: User Submits to Source Shard

```
User → POST /tx/submit → Shard 0
                              │
                              ▼
                    ┌─────────────────────────┐
                    │ Validation (lines 952-974)
                    │ - Gas limits (21000-30M) │
                    │ - From on this shard     │
                    └────────────┬────────────┘
                                 │
                                 ▼
                    ┌─────────────────────────┐
                    │ Cross-shard detection    │
                    │ (lines 976-1033)         │
                    │                          │
                    │ 1. Check to shard        │
                    │ 2. If local contract:    │
                    │    SimulateCall()        │
                    └────────────┬────────────┘
                                 │
         ┌───────────────────────┼───────────────────────┐
         │                       │                       │
   Cross-shard              Local OK              Local Error
         │                       │                       │
         ▼                       ▼                       ▼
┌─────────────────┐   ┌─────────────────────┐   ┌─────────────┐
│forwardTo-       │   │Balance check        │   │Return error │
│Orchestrator()   │   │(best-effort TOCTOU) │   │{success:    │
│(lines 1097-1145)│   │                     │   │ false,      │
│                 │   │chain.AddTx()        │   │ error:"..."}│
│POST /cross-shard│   │                     │   └─────────────┘
│/call            │   │Response:            │
│                 │   │{success: true,      │
│Response from    │   │ tx_id: "...",       │
│orchestrator +   │   │ status: "queued",   │
│cross_shard:true │   │ cross_shard: false} │
└─────────────────┘   └─────────────────────┘
```

**State changes at submission:** NONE

- No funds locked
- No balance deducted
- No pending credits created
- Transaction is just forwarded (if cross-shard) or queued (if local)
- Balance check for local tx is best-effort (TOCTOU race possible)

#### forwardToOrchestrator Details

**Implementation:** `server.go` lines 1097-1145

```go
// Build minimal RwSet from detected cross-shard addresses
// Note: ReadSet/WriteSet are empty - Orchestrator will re-simulate
rwSet := make([]protocol.RwVariable, 0, len(crossShardAddrs))
for addr, shardID := range crossShardAddrs {
    rwSet = append(rwSet, protocol.RwVariable{
        Address: addr,
        ReferenceBlock: protocol.Reference{
            ShardNum: shardID,
        },
        // ReadSet and WriteSet are EMPTY here
    })
}

tx := protocol.CrossShardTx{
    ID:        uuid.New().String(),
    FromShard: s.shardID,
    From:      from,
    To:        to,
    Value:     protocol.NewBigInt(value),
    Gas:       gas,
    Data:      data,
    RwSet:     rwSet,  // Minimal - just addresses
}

// POST to orchestrator
resp, err := http.Post(s.orchestrator+"/cross-shard/call", ...)
```

**Key point:** The RwSet sent to orchestrator contains only addresses (no ReadSet/WriteSet values). The orchestrator will:
1. Lock these addresses on their respective shards
2. Fetch full state from shards
3. Re-simulate with complete state
4. Populate full ReadSet/WriteSet

This is necessary because local simulation doesn't have access to cross-shard state.

#### Stage 2: Orchestrator Receives Cross-Shard Transaction

```
Orchestrator receives POST /cross-shard/call
                              │
                              ▼
                    ┌─────────────────┐
                    │ Generate tx.ID  │
                    │ Set status =    │
                    │   "pending"     │
                    └────────┬────────┘
                             │
                             ▼
                    ┌─────────────────┐
                    │ Submit to       │
                    │ Simulator Queue │
                    └────────┬────────┘
                             │
                             ▼ (async)
                    ┌─────────────────┐
                    │ Simulator:      │
                    │ 1. Lock addrs   │
                    │ 2. Fetch state  │
                    │ 3. Run EVM      │
                    │ 4. Discover     │
                    │    full RwSet   │
                    └────────┬────────┘
                             │
              ┌──────────────┴──────────────┐
              │                             │
        Simulation OK               Simulation Failed
              │                             │
              ▼                             ▼
    ┌─────────────────┐           ┌─────────────────┐
    │ tx.SimStatus =  │           │ tx.SimStatus =  │
    │   "success"     │           │   "failed"      │
    │ tx.RwSet =      │           │ tx.SimError =   │
    │   discovered    │           │   "reason"      │
    │ Add to pending  │           │ Not included in │
    │ for next block  │           │ CtToOrder       │
    └─────────────────┘           └─────────────────┘
```

#### Stage 3: RwSet Reconciliation

**Question:** What if orchestrator's simulation discovers different addresses than source shard's detection?

**Answer:** Orchestrator's RwSet is authoritative. Source shard's detection is just a hint.

| Source Detection | Orchestrator Simulation | Result |
|------------------|------------------------|--------|
| Detected addr A, B | Discovered A, B, C | Use A, B, C (orchestrator wins) |
| Detected addr A | Discovered only A (same shard) | **Downgrade to local?** See below |
| Detected cross-shard | Simulation fails | Transaction aborted |

**Downgrade Case:** If orchestrator simulation discovers the transaction is actually local (all accessed addresses on source shard), the orchestrator has two options:

1. **Option A: Proceed as cross-shard anyway** (current approach)
   - Simpler, no special case
   - Slight overhead (2PC for local-only tx)
   - Transaction still succeeds

2. **Option B: Return to source shard for local execution** (not implemented)
   - More efficient but adds complexity
   - Requires new message type: "downgrade to local"
   - Risk of infinite loop if detection keeps flip-flopping

**Recommendation:** Use Option A (proceed as cross-shard). The overhead is acceptable for edge cases.

### Duplicate Transaction Handling

**Problem:** User submits same transaction twice (network retry, UI bug, etc.)

**Detection Points:**

1. **Source Shard (for local txs):**
   - Check `tx_hash` in recent transactions
   - If duplicate: return `{status: "error", message: "duplicate transaction"}`

2. **Orchestrator (for cross-shard txs):**
   - Check `tx_hash` in `pendingTxs` and `awaitingVotes`
   - If duplicate: return `{status: "error", message: "duplicate transaction"}`

**Transaction Hash Calculation:**
```go
// Deterministic hash from transaction content (not ID)
txHash = keccak256(rlp.Encode(from, to, value, nonce, data))
```

**Note:** Requires tracking sender nonce, which is not currently implemented. Alternative: use `(from, to, value, data, timestamp_bucket)` for approximate dedup.

### Error Responses

| Error | HTTP Status | Response |
|-------|-------------|----------|
| Invalid JSON | 400 | `{status: "error", message: "invalid request body"}` |
| Missing from address | 400 | `{status: "error", message: "from address required"}` |
| From not on this shard | 400 | `{status: "error", message: "from address belongs to shard X"}` |
| Gas below minimum | 400 | `{status: "error", message: "gas below minimum (21000)"}` |
| Gas above maximum | 400 | `{status: "error", message: "gas above maximum (30000000)"}` |
| Insufficient balance | 400 | `{status: "error", message: "insufficient balance"}` |
| Orchestrator unavailable | 503 | `{status: "error", message: "orchestrator unavailable"}` |
| Duplicate transaction | 409 | `{status: "error", message: "duplicate transaction"}` |

### User Experience Considerations

#### Tracking Cross-Shard Transaction Status

After receiving `{status: "forwarded", tx_id: "..."}`, users need a way to check status.

**Option 1: Poll Orchestrator**
```
GET /cross-shard/status/{tx_id}
Response: {
  "tx_id": "...",
  "status": "pending" | "prepared" | "committed" | "aborted",
  "sim_status": "pending" | "running" | "success" | "failed",
  "sim_error": "..." // if failed
}
```

**Option 2: Poll Source Shard**
```
GET /tx/status/{tx_id}
Response: {
  "tx_id": "...",
  "status": "forwarded" | "committed" | "aborted",
  "cross_shard": true,
  "orchestrator_status": "..." // fetched from orchestrator
}
```

**Option 3: WebSocket Subscription** (future)
```
WS /tx/subscribe/{tx_id}
Events: status_changed, committed, aborted
```

#### Expected Latency

| Transaction Type | Latency |
|-----------------|---------|
| Local (queued) | ~3 seconds (next block) |
| Cross-shard (forwarded) | ~6-12 seconds (2 block cycles minimum) |

**Breakdown for cross-shard:**
- T+0s: Forwarded to orchestrator
- T+0-3s: Simulation runs
- T+3s: Included in orchestrator block, broadcast to shards
- T+3-6s: Shards vote (prepare)
- T+6s: Votes collected, decision made
- T+6-9s: Commit/abort broadcast
- T+9s: State finalized

---

## Data Structures

### Local Transaction

```go
// internal/protocol/types.go (lines 150-160)
type Transaction struct {
    ID           string         `json:"id,omitempty"`
    TxHash       common.Hash    `json:"tx_hash,omitempty"`
    From         common.Address `json:"from"`
    To           common.Address `json:"to"`
    Value        *BigInt        `json:"value"`
    Gas          uint64         `json:"gas,omitempty"`
    Data         HexBytes       `json:"data,omitempty"`
    IsCrossShard bool           `json:"is_cross_shard"`
}
```

**Field Semantics:**
- `ID`: Unique transaction identifier (UUID)
- `TxHash`: Ethereum-style transaction hash (optional, for compatibility)
- `From`: Sender address (must be on local shard by address % NumShards)
- `To`: Recipient address or contract address
- `Value`: Amount to transfer (in wei)
- `Gas`: Gas limit for EVM execution (validated: MinGasLimit <= gas <= MaxGasLimit)
- `Data`: Contract call data (empty for simple transfer)
- `IsCrossShard`: Always false for local transactions

**Constraints:**
- From address must map to the shard processing this transaction
- Gas must be >= 21,000 for any transaction
- Value can be zero (e.g., for read-only contract calls with state changes)
- If Data is empty, transaction is a simple transfer

**JSON Example:**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "from": "0x1234567890123456789012345678901234567890",
  "to": "0x2345678901234567890123456789012345678901",
  "value": "1000000000000000000",
  "gas": 21000,
  "data": "0x",
  "is_cross_shard": false
}
```

### Cross-Shard Transaction

```go
// internal/protocol/types.go (lines 240-255)
type CrossShardTx struct {
    ID        string           `json:"id,omitempty"`
    TxHash    common.Hash      `json:"tx_hash,omitempty"`
    FromShard int              `json:"from_shard"`
    From      common.Address   `json:"from"`
    To        common.Address   `json:"to"`
    Value     *BigInt          `json:"value"`
    Gas       uint64           `json:"gas,omitempty"`
    Data      HexBytes         `json:"data,omitempty"`
    RwSet     []RwVariable     `json:"rw_set"`
    Status    TxStatus         `json:"status,omitempty"`
    SimStatus SimulationStatus `json:"sim_status,omitempty"`
    SimError  string           `json:"sim_error,omitempty"`
}
```

**Field Semantics:**
- `ID`: Unique transaction identifier (UUID)
- `TxHash`: Ethereum-style transaction hash (optional)
- `FromShard`: Source shard ID (0-5 in current configuration)
- `From`: Sender address
- `To`: Primary recipient address (receives Value)
- `Value`: Amount to transfer to To address
- `Gas`: Gas limit for orchestrator simulation
- `Data`: Contract call data
- `RwSet`: Read/write set discovered by orchestrator simulation (see RwVariable)
- `Status`: Transaction lifecycle status (pending → prepared → committed/aborted)
- `SimStatus`: Simulation state (pending → running → success/failed)
- `SimError`: Error message if simulation failed

**Constraints:**
- FromShard must be in range [0, NumShards)
- From address must map to FromShard
- RwSet is populated by orchestrator simulation (may be empty on submission)
- Status transitions: pending → prepared → committed/aborted
- Only To address receives Value (not every RwSet entry - prevents "money printer" bug)

**JSON Example:**
```json
{
  "id": "650e8400-e29b-41d4-a716-446655440001",
  "from_shard": 0,
  "from": "0x1234567890123456789012345678901234567890",
  "to": "0x2345678901234567890123456789012345678901",
  "value": "1000000000000000000",
  "gas": 1000000,
  "data": "0xa9059cbb000000000000000000000000...",
  "rw_set": [
    {
      "address": "0x2345678901234567890123456789012345678901",
      "reference_block": {
        "shard_num": 1,
        "block_hash": "0x0000000000000000000000000000000000000000000000000000000000000000",
        "block_height": 0
      },
      "read_set": [
        {
          "slot": "0x0000000000000000000000000000000000000000000000000000000000000001",
          "value": "0x0000000000000000000000000000000000000000000000000000000000000064",
          "proof": []
        }
      ],
      "write_set": [
        {
          "slot": "0x0000000000000000000000000000000000000000000000000000000000000001",
          "old_value": "0x0000000000000000000000000000000000000000000000000000000000000064",
          "new_value": "0x00000000000000000000000000000000000000000000000000000000000000c8"
        }
      ]
    }
  ],
  "status": "pending",
  "sim_status": "success"
}
```

### RwVariable (Read/Write Set Entry)

```go
// internal/protocol/types.go (lines 142-148)
type RwVariable struct {
    Address        common.Address `json:"address"`
    ReferenceBlock Reference      `json:"reference_block"`
    ReadSet        []ReadSetItem  `json:"read_set"`
    WriteSet       []WriteSetItem `json:"write_set"`
}
```

**Field Semantics:**
- `Address`: Contract address accessed during simulation
- `ReferenceBlock`: Identifies which shard holds this contract's state
- `ReadSet`: Storage slots read during simulation (for validation)
- `WriteSet`: Storage slots modified during simulation (for application)

**Purpose:**
- Discovered by orchestrator's EVM simulation
- Each entry represents one contract's state accessed
- ReadSet enables serializability checking (detect conflicts)
- WriteSet enables deterministic commit (no re-execution)
- Reference block identifies target shard (by shard_num)

### LockedFunds (Shard-Side State)

```go
// internal/shard/chain.go (lines 14-19)
type LockedFunds struct {
    Address common.Address
    Amount  *big.Int
}
```

**Field Semantics:**
- `Address`: Account whose funds are locked
- `Amount`: Amount locked (in wei)

**Lifetime:**
- Created: During prepare phase when source shard votes YES
- Indexed: In chain.locked[txID] and chain.lockedByAddr[address]
- Cleared: On commit (after debit) or abort (without debit)

**Atomicity:**
- Lock creation is atomic with balance check (holds evmState.mu)
- Prevents race: two concurrent prepares can't both lock if balance insufficient

### PendingCredit (Shard-Side State)

```go
// internal/shard/chain.go (lines 21-25)
type PendingCredit struct {
    Address common.Address
    Amount  *big.Int
}
```

**Field Semantics:**
- `Address`: Account to receive credit
- `Amount`: Amount to credit (in wei)

**Lifetime:**
- Created: During prepare phase when destination shard validates ReadSet
- Indexed: In chain.pendingCredits[txID] as a list (supports multiple recipients)
- Cleared: On commit (after applying credits) or abort (discarded)

**Important:**
- Only To address gets a pending credit for Value amount
- RwSet entries do NOT get pending credits (prevents "money printer" bug)
- Multiple credits per tx supported for future multi-recipient transfers

### SimulationLock (Shard-Side State)

```go
// internal/shard/chain.go (lines 31-42)
type SimulationLock struct {
    TxID      string
    Address   common.Address
    Balance   *big.Int
    Nonce     uint64
    Code      []byte
    CodeHash  common.Hash
    Storage   map[common.Hash]common.Hash
    CreatedAt time.Time
}

const SimulationLockTTL = 2 * time.Minute
```

**Field Semantics:**
- `TxID`: Transaction holding this lock
- `Address`: Locked account address
- `Balance`: Account balance at lock time
- `Nonce`: Account nonce at lock time
- `Code`: Contract code at lock time
- `CodeHash`: Hash of contract code
- `Storage`: Storage slots accessed during simulation (empty in current PoC)
- `CreatedAt`: Timestamp for TTL enforcement

**Lifetime:**
- Created: When orchestrator calls /state/lock during simulation
- Indexed: In chain.simLocks[txID][addr] and chain.simLocksByAddr[addr]
- Validated: During prepare phase (ReadSet must match locked state)
- Cleared: After commit/abort, or on TTL expiration (2 minutes)

**Purpose:**
- Prevents conflicting modifications between simulation and commit
- Ensures ReadSet validation is meaningful (state hasn't changed)
- TTL prevents deadlocks from orchestrator failures

---

## State Transitions

### Transaction Status Flow

```
                           User Submits
                                │
                                ▼
                          ┌─────────┐
                          │ pending │  (In Orchestrator's pendingTxs)
                          └────┬────┘
                               │ Included in Orchestrator Block
                               ▼
                         ┌──────────┐
                         │ prepared │  (In Orchestrator's awaitingVotes)
                         └─────┬────┘
                               │ All Votes Collected
                ┌──────────────┴──────────────┐
                │                             │
                ▼                             ▼
          ┌───────────┐                 ┌──────────┐
          │ committed │ (All YES)       │ aborted  │ (Any NO)
          └───────────┘                 └──────────┘
```

**Status Meanings:**

1. **pending**: Transaction queued in orchestrator, awaiting inclusion in CtToOrder
   - Orchestrator: In `pendingTxs` list
   - Next step: Included in next Orchestrator block's CtToOrder

2. **prepared**: Transaction sent to shards, awaiting votes
   - Orchestrator: In `awaitingVotes` map
   - State Shards: Funds locked (source) or credits pending (destination)
   - Next step: Votes returned in State Shard blocks

3. **committed**: Transaction finalized successfully
   - Orchestrator: In `TpcResult` as `true`
   - State Shards: Funds debited (source), credits applied (destination)
   - Terminal state

4. **aborted**: Transaction failed to prepare
   - Orchestrator: In `TpcResult` as `false`
   - State Shards: Locks cleared (source), pending credits discarded (destination)
   - Terminal state

### State Mutations Timeline

#### Source Shard (Lock-Only Approach)

```
Time T: Prepare Phase
├─ Receive CtToOrder with tx
├─ ATOMIC (hold evmState.mu):
│  ├─ Calculate: available = balance - lockedAmount
│  ├─ Check: available >= tx.Value
│  └─ If YES: chain.LockFunds(tx.ID, tx.From, tx.Value)
├─ Release evmState.mu
└─ Vote added to prepares[tx.ID]

Time T+1: Block Production
├─ State Shard produces block with TpcPrepare votes
└─ Block sent to Orchestrator

Time T+2: Commit Phase (if committed)
├─ Receive TpcResult with tx.ID = true
├─ Lookup lock: chain.GetLockedFunds(tx.ID)
├─ Debit NOW: evmState.Debit(lock.Address, lock.Amount)
├─ Clear lock: chain.ClearLock(tx.ID)
└─ Balance mutation: balance -= lock.Amount

Time T+2: Abort Phase (if aborted)
├─ Receive TpcResult with tx.ID = false
├─ Clear lock: chain.ClearLock(tx.ID)
└─ Balance mutation: NONE (balance unchanged)
```

**Key Insight:** Lock-only approach means balance is never debited during prepare. This eliminates refund logic on abort.

#### Destination Shard

```
Time T: Prepare Phase
├─ Receive CtToOrder with tx containing RwSet
├─ For each RwVariable where ReferenceBlock.ShardNum == myShardID:
│  ├─ Validate ReadSet: all reads match current state
│  ├─ Check simulation lock exists and belongs to tx.ID
│  └─ Vote: allRwValid = allRwValid && readSetMatches
├─ If tx.To maps to myShardID and allRwValid:
│  └─ Store pending credit: chain.StorePendingCredit(tx.ID, tx.To, tx.Value)
├─ If tx has Data and allRwValid:
│  └─ Store pending call: chain.StorePendingCall(&tx)
└─ Single vote per tx: prepares[tx.ID] = allRwValid

Time T+1: Block Production
├─ State Shard produces block with TpcPrepare vote
└─ Block sent to Orchestrator

Time T+2: Commit Phase (if committed)
├─ Receive TpcResult with tx.ID = true
├─ Apply pending credits:
│  ├─ For each credit in GetPendingCredits(tx.ID):
│  │  └─ evmState.Credit(credit.Address, credit.Amount)
│  └─ chain.ClearPendingCredit(tx.ID)
├─ Apply WriteSet from pending call:
│  ├─ For each write in pendingCall.RwSet[*].WriteSet:
│  │  └─ evmState.SetStorageAt(addr, slot, newValue)
│  └─ chain.ClearPendingCall(tx.ID)
├─ Release simulation locks: chain.UnlockAllForTx(tx.ID)
└─ Balance mutation: balance += credit.Amount

Time T+2: Abort Phase (if aborted)
├─ Receive TpcResult with tx.ID = false
├─ Discard pending credits: chain.ClearPendingCredit(tx.ID)
├─ Discard pending call: chain.ClearPendingCall(tx.ID)
├─ Release simulation locks: chain.UnlockAllForTx(tx.ID)
└─ State mutation: NONE
```

**Key Insight:** WriteSet is applied directly from simulation results. No re-execution occurs on commit (deterministic from RwSet).

### EVM State Mutations

#### Direct State Changes (Committed Immediately)
These occur during ProduceBlock and are committed with the block:

```go
// internal/shard/chain.go ProduceBlock (lines 246-289)
for _, tx := range c.currentTxs {
    snapshot := evmState.Snapshot()
    if err := evmState.ExecuteTx(&tx); err != nil {
        evmState.RevertToSnapshot(snapshot)  // Failed tx reverted
    } else {
        successfulTxs = append(successfulTxs, tx)  // Successful tx committed
    }
}
stateRoot, err := evmState.Commit(c.height + 1)  // All state changes committed
```

**What Gets Committed:**
- Balance changes: transfers, contract value transfers
- Storage changes: SSTORE operations
- Code deployment: contract creation
- Nonce increments: every transaction

**Rollback Mechanism:**
- Failed transactions use Snapshot() before execution
- RevertToSnapshot() on error discards all state changes
- Only successful transactions included in block

#### Pending State Changes (Applied Later)
These are tracked in Chain struct and applied when TpcResult arrives:

```go
// internal/shard/chain.go (lines 44-57)
type Chain struct {
    // ... other fields ...
    prepares       map[string]bool                 // Votes for this block
    locked         map[string]*LockedFunds         // Awaiting commit/abort
    pendingCredits map[string][]*PendingCredit     // Awaiting commit/abort
    pendingCalls   map[string]*protocol.CrossShardTx  // Awaiting commit/abort
    simLocks       map[string]map[common.Address]*SimulationLock  // Awaiting commit/abort
}
```

**Application on Commit (server.go lines 692-751):**
```go
// Phase 1: Process TpcResult
for txID, committed := range block.TpcResult {
    if lock, ok := s.chain.GetLockedFunds(txID); ok {
        if committed {
            s.evmState.Debit(lock.Address, lock.Amount)  // NOW debit
            s.chain.ClearLock(txID)
        } else {
            s.chain.ClearLock(txID)  // Just clear, no debit
        }
    }
    if credits, ok := s.chain.GetPendingCredits(txID); ok {
        if committed {
            for _, credit := range credits {
                s.evmState.Credit(credit.Address, credit.Amount)  // Apply
            }
        }
        s.chain.ClearPendingCredit(txID)
    }
    if pendingCall, ok := s.chain.GetPendingCall(txID); ok {
        if committed {
            // Apply WriteSet directly (no re-execution)
            for _, rw := range pendingCall.RwSet {
                for _, write := range rw.WriteSet {
                    s.evmState.SetStorageAt(rw.Address, slot, newValue)
                }
            }
        }
        s.chain.ClearPendingCall(txID)
    }
    s.chain.UnlockAllForTx(txID)  // Release simulation locks
}
```

**Critical:** These changes are NOT included in the current block being produced. They mutate EVM state directly in the HTTP handler. This is the **CURRENT** approach.

---

## Execution Flow

### Local Transaction Flow

```
User Request (POST /tx/submit)
    │
    ▼
┌─────────────────────────────────────┐
│ handleTxSubmit (server.go:928)      │
│ - Parse request                     │
│ - Validate gas limits               │
│ - Check sender is on this shard     │
│ - Detect if cross-shard             │
└─────────────┬───────────────────────┘
              │ (Local tx detected)
              ▼
┌─────────────────────────────────────┐
│ Pre-validation (lines 1052-1067)    │
│ - TOCTOU race warning:              │
│ - Check balance > value (best effort)│
│ - Balance may change before execution│
└─────────────┬───────────────────────┘
              │
              ▼
┌─────────────────────────────────────┐
│ Queue Transaction (lines 1069-1086) │
│ chain.AddTx(protocol.Transaction{   │
│     ID:           uuid.New()         │
│     From:         from               │
│     To:           to                 │
│     Value:        value              │
│     Gas:          gas                │
│     Data:         data               │
│     IsCrossShard: false              │
│ })                                   │
└─────────────┬───────────────────────┘
              │
              ▼ (Every 3 seconds)
┌─────────────────────────────────────┐
│ ProduceBlock (chain.go:249-289)     │
│ FOR each tx in currentTxs:          │
│   snapshot = evmState.Snapshot()    │
│   err = evmState.ExecuteTx(&tx)     │
│   IF err:                            │
│     evmState.RevertToSnapshot(snap) │
│     // Tx excluded from block       │
│   ELSE:                              │
│     successfulTxs.append(tx)        │
│                                      │
│ stateRoot = evmState.Commit(height) │
│ block.TxOrdering = successfulTxs    │
└─────────────┬───────────────────────┘
              │
              ▼
┌─────────────────────────────────────┐
│ ExecuteTx (evm.go:345-370)          │
│ IF len(tx.Data) > 0:                │
│   // Contract call                  │
│   evm.Call(from, to, data, gas, val)│
│ ELSE:                                │
│   // Simple transfer                │
│   evm.Transfer(from, to, value)     │
└─────────────────────────────────────┘
```

**Key Points:**
- Transactions are queued immediately, executed later
- Failed executions are reverted using snapshot mechanism
- Only successful transactions appear in final block
- State is committed atomically for entire block

### Cross-Shard Transaction Flow (Complete Lifecycle)

```
┌─────────────────────────────────────────────────────────────────────┐
│ STEP 1: User Submission to State Shard                               │
└─────────────────────────────────────────────────────────────────────┘

User Request (POST /tx/submit to Shard 0)
    │
    ▼
┌───────────────────────────────────────┐
│ handleTxSubmit (server.go:928)        │
│ - Validate sender on this shard       │
│ - Check if 'to' on different shard OR │
│ - Simulate contract call to detect    │
│   cross-shard access                  │
└─────────────┬─────────────────────────┘
              │ (Cross-shard detected)
              ▼
┌───────────────────────────────────────┐
│ forwardToOrchestrator (line 1097)     │
│ Build CrossShardTx:                   │
│   - RwSet from simulation (minimal)   │
│   - Or just recipient for transfers   │
│                                        │
│ POST /cross-shard/call                │
└─────────────┬─────────────────────────┘
              │
              ▼

┌─────────────────────────────────────────────────────────────────────┐
│ STEP 2: Orchestrator Receives and Simulates                         │
└─────────────────────────────────────────────────────────────────────┘

┌───────────────────────────────────────┐
│ handleCall (orchestrator/service.go:179)│
│ - Generate tx.ID if missing           │
│ - tx.Status = pending                 │
└─────────────┬─────────────────────────┘
              │
              ▼
┌───────────────────────────────────────┐
│ simulator.SubmitTx() (service.go:201) │
│ Background worker:                    │
│ 1. Lock all involved addresses        │
│ 2. Fetch state from shards            │
│ 3. Run EVM simulation                 │
│ 4. Discover full RwSet                │
│ 5. Call onSuccess callback            │
└─────────────┬─────────────────────────┘
              │ (Simulation success)
              ▼
┌───────────────────────────────────────┐
│ OnSuccess Callback (service.go:48-55) │
│ - Store in pending map                │
│ - chain.AddTransaction(tx)            │
│ - Log "Simulation complete"           │
└─────────────┬─────────────────────────┘
              │
              ▼ (Every 3 seconds)

┌─────────────────────────────────────────────────────────────────────┐
│ STEP 3: Orchestrator Produces Block (Prepare Phase)                 │
└─────────────────────────────────────────────────────────────────────┘

┌───────────────────────────────────────┐
│ blockProducer (service.go:88-110)     │
│ block = chain.ProduceBlock()          │
└─────────────┬─────────────────────────┘
              │
              ▼
┌───────────────────────────────────────┐
│ ProduceBlock (chain.go:129-158)       │
│ block.CtToOrder = pendingTxs          │
│ block.TpcResult = pendingResult       │
│                                        │
│ FOR each tx in pendingTxs:            │
│   awaitingVotes[tx.ID] = tx           │
│   expectedVoters[tx.ID] =             │
│       tx.InvolvedShards()             │
└─────────────┬─────────────────────────┘
              │
              ▼
┌───────────────────────────────────────┐
│ broadcastBlock (service.go:222-243)   │
│ FOR each shard in [0..NumShards):    │
│   POST /orchestrator-shard/block      │
└─────────────┬─────────────────────────┘
              │
              ▼ (Parallel to all shards)

┌─────────────────────────────────────────────────────────────────────┐
│ STEP 4A: Source Shard Processes Block (Prepare)                     │
└─────────────────────────────────────────────────────────────────────┘

┌───────────────────────────────────────┐
│ handleOrchestratorShardBlock          │
│ (server.go:681-841)                   │
└─────────────┬─────────────────────────┘
              │
              ▼
┌───────────────────────────────────────┐
│ Phase 2: Process CtToOrder (line 753)│
│ FOR each tx in block.CtToOrder:      │
│   IF tx.FromShard == myShardID:      │
└─────────────┬─────────────────────────┘
              │
              ▼
┌───────────────────────────────────────┐
│ Lock-Only Prepare (lines 758-780)    │
│ ATOMIC (hold evmState.mu):           │
│   lockedAmount = GetLockedAmount(From)│
│   canCommit = CanDebit(From, Value,  │
│                        lockedAmount)  │
│   IF canCommit:                       │
│     LockFunds(tx.ID, From, Value)    │
│ Release evmState.mu                   │
│                                        │
│ AddPrepareResult(tx.ID, canCommit)   │
└─────────────┬─────────────────────────┘
              │
              ▼ (Next block cycle)
┌───────────────────────────────────────┐
│ ProduceBlock (chain.go:249)           │
│ block.TpcPrepare = prepares           │
└─────────────┬─────────────────────────┘
              │
              ▼
┌───────────────────────────────────────┐
│ sendBlockToOrchestratorShard          │
│ (server.go:113)                       │
│ POST /state-shard/block               │
└───────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│ STEP 4B: Destination Shard Processes Block (Prepare)                │
└─────────────────────────────────────────────────────────────────────┘

┌───────────────────────────────────────┐
│ Phase 2: Process RwSet (line 784)    │
│ FOR each rw in tx.RwSet:              │
│   IF rw.ReferenceBlock.ShardNum ==   │
│      myShardID:                        │
└─────────────┬─────────────────────────┘
              │
              ▼
┌───────────────────────────────────────┐
│ Validate RwSet (lines 806-837)        │
│ rwCanCommit = validateRwVariable()    │
│   - Check simulation lock exists      │
│   - Verify ReadSet matches state      │
│                                        │
│ allRwValid = allRwValid && rwCanCommit│
│                                        │
│ IF allRwValid && toShard==myShardID: │
│   StorePendingCredit(tx.ID, tx.To,   │
│                      tx.Value)        │
│                                        │
│ IF allRwValid && len(tx.Data)>0:     │
│   StorePendingCall(&tx)               │
│                                        │
│ AddPrepareResult(tx.ID, allRwValid)  │
└─────────────┬─────────────────────────┘
              │
              ▼ (Next block cycle)
┌───────────────────────────────────────┐
│ ProduceBlock → Send to Orchestrator   │
└───────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│ STEP 5: Orchestrator Collects Votes                                 │
└─────────────────────────────────────────────────────────────────────┘

┌───────────────────────────────────────┐
│ handleStateShardBlock                 │
│ (service.go:258-291)                  │
│ FOR each vote in block.TpcPrepare:   │
│   RecordVote(txID, shardID, vote)    │
└─────────────┬─────────────────────────┘
              │
              ▼
┌───────────────────────────────────────┐
│ RecordVote (chain.go:56-114)          │
│ votes[txID][shardID] = canCommit      │
│                                        │
│ IF !canCommit:                         │
│   pendingResult[txID] = false         │
│   // Immediate abort                  │
│                                        │
│ IF all expected shards voted YES:     │
│   pendingResult[txID] = true          │
└─────────────┬─────────────────────────┘
              │
              ▼ (Next block cycle)

┌─────────────────────────────────────────────────────────────────────┐
│ STEP 6: Orchestrator Broadcasts Decision (Commit/Abort)             │
└─────────────────────────────────────────────────────────────────────┘

┌───────────────────────────────────────┐
│ blockProducer (service.go:88-110)     │
│ block = ProduceBlock()                │
│ block.TpcResult = pendingResult       │
│   // Contains commit/abort decisions  │
│                                        │
│ FOR each txID in TpcResult:           │
│   updateStatus(txID, committed/aborted)│
│   fetcher.UnlockAll(txID)             │
└─────────────┬─────────────────────────┘
              │
              ▼
┌───────────────────────────────────────┐
│ broadcastBlock to all shards          │
└─────────────┬─────────────────────────┘
              │
              ▼ (Parallel to all shards)

┌─────────────────────────────────────────────────────────────────────┐
│ STEP 7A: Source Shard Applies Decision                              │
└─────────────────────────────────────────────────────────────────────┘

┌───────────────────────────────────────┐
│ Phase 1: Process TpcResult (line 692)│
│ FOR each txID, committed in TpcResult:│
│   IF lock exists for txID:            │
└─────────────┬─────────────────────────┘
              │
              ▼
┌───────────────────────────────────────┐
│ IF committed (lines 695-700):         │
│   evmState.Debit(lock.Address,        │
│                  lock.Amount)         │
│   chain.ClearLock(txID)               │
│   Log: "debited X from Y"             │
│                                        │
│ ELSE (aborted) (lines 702-704):       │
│   chain.ClearLock(txID)               │
│   Log: "released lock"                │
└───────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│ STEP 7B: Destination Shard Applies Decision                         │
└─────────────────────────────────────────────────────────────────────┘

┌───────────────────────────────────────┐
│ Phase 1: Process TpcResult (line 709)│
│ IF credits exist for txID:            │
└─────────────┬─────────────────────────┘
              │
              ▼
┌───────────────────────────────────────┐
│ IF committed (lines 711-716):         │
│   FOR each credit in credits:         │
│     evmState.Credit(credit.Address,   │
│                     credit.Amount)    │
│   Log: "credited X to Y"              │
│                                        │
│ ELSE (aborted) (line 718):            │
│   Log: "credits discarded"            │
│                                        │
│ ClearPendingCredit(txID)              │
└─────────────┬─────────────────────────┘
              │
              ▼
┌───────────────────────────────────────┐
│ Apply WriteSet (lines 724-746)        │
│ IF pendingCall exists for txID:       │
│   IF committed:                        │
│     FOR each write in RwSet:          │
│       SetStorageAt(addr, slot, value) │
│   ClearPendingCall(txID)              │
└─────────────┬─────────────────────────┘
              │
              ▼
┌───────────────────────────────────────┐
│ Release Locks (line 750)              │
│ UnlockAllForTx(txID)                  │
└───────────────────────────────────────┘
```

### Critical Timing and Synchronization Points

**Block Production Intervals:**
- Both Orchestrator and State Shards produce blocks every 3 seconds
- This creates natural synchronization points
- Prepare → Vote → Commit cycle takes minimum 2 block intervals (6 seconds)

**Lock Acquisition Timing:**
```
T+0s: Orchestrator broadcasts CtToOrder
T+0s to T+3s: State Shards process and lock funds
T+3s: State Shards produce blocks with votes
T+3s to T+6s: Orchestrator receives and aggregates votes
T+6s: Orchestrator broadcasts TpcResult
T+6s to T+9s: State Shards apply commit/abort
```

**Atomicity Guarantees:**

1. **Balance Check + Lock (Source Shard):**
```go
// server.go lines 769-776
s.evmState.mu.Lock()
lockedAmount := s.chain.GetLockedAmountForAddress(tx.From)
canCommit := s.evmState.CanDebit(tx.From, tx.Value.ToBigInt(), lockedAmount)
if canCommit {
    s.chain.LockFunds(tx.ID, tx.From, tx.Value.ToBigInt())
}
s.evmState.mu.Unlock()
```
- Prevents TOCTOU race between balance check and lock
- Two concurrent prepares can't both succeed if balance insufficient

2. **Block Production + State Commit (All Shards):**
```go
// chain.go lines 249-289
c.mu.Lock()  // Hold chain lock for entire block production
defer c.mu.Unlock()

// Execute all transactions
// Commit EVM state
// Create block
// Update chain state
```
- Entire block production is atomic
- No concurrent modifications during ProduceBlock

---

## Unified Transaction Model (Implemented)

This section describes the unified transaction model that ensures all state mutations go through `ProduceBlock` with proper `TxOrdering` entries. This model was implemented to solve problems with the previous hybrid approach where cross-shard operations mutated state directly in HTTP handlers.

**Key Achievement:** All state mutations now go through `ProduceBlock` with proper TxOrdering entries.

---

### TxType Enum

Separate TxType values for each operation type (clearer than single type with field inspection):

```go
// internal/protocol/types.go (lines 150-164)

type TxType string

const (
    TxTypeLocal           TxType = ""              // Default: normal local EVM execution
    TxTypeCrossDebit      TxType = "cross_debit"   // Source shard: debit locked funds
    TxTypeCrossCredit     TxType = "cross_credit"  // Dest shard: credit pending amount
    TxTypeCrossWriteSet   TxType = "cross_writeset"// Dest shard: apply storage writes
    TxTypeCrossAbort      TxType = "cross_abort"   // Any shard: cleanup only (no state change)
)
```

**Why separate types?**
- Unambiguous: each type has exactly one operation
- No field inspection needed in ExecuteTx
- Clear audit trail in block's TxOrdering
- Easier to debug and test

### Transaction Struct (Extended)

```go
// internal/protocol/types.go (lines 166-184)
type Transaction struct {
    // Base fields (used by all transaction types)
    ID           string         `json:"id,omitempty"`
    TxHash       common.Hash    `json:"tx_hash,omitempty"`
    From         common.Address `json:"from"`
    To           common.Address `json:"to"`
    Value        *BigInt        `json:"value"`
    Gas          uint64         `json:"gas,omitempty"`
    Data         HexBytes       `json:"data,omitempty"`
    IsCrossShard bool           `json:"is_cross_shard"`

    // Cross-shard operation fields (used when TxType != TxTypeLocal)
    TxType         TxType       `json:"tx_type,omitempty"`           // Operation type (empty = local)
    CrossShardTxID string       `json:"cross_shard_tx_id,omitempty"` // Original cross-shard tx ID
    RwSet          []RwVariable `json:"rw_set,omitempty"`            // WriteSet for storage writes
}
```

### Field Usage Per TxType

| TxType | From | To | Value | RwSet | CrossShardTxID |
|--------|------|-----|-------|-------|----------------|
| `""` (local) | sender | recipient | transfer amount | - | - |
| `cross_debit` | lock.Address | - | lock.Amount | - | original tx ID |
| `cross_credit` | - | credit.Address | credit.Amount | - | original tx ID |
| `cross_writeset` | - | - | - | WriteSet data | original tx ID |
| `cross_abort` | - | - | - | - | original tx ID |

### handleOrchestratorShardBlock (Phase 1: TpcResult)

Cross-shard operations are queued as typed transactions instead of direct state mutation:

```go
// server.go handleOrchestratorShardBlock (lines 692-751)
// Phase 1: Queue commit/abort operations as typed transactions
for txID, committed := range block.TpcResult {
    if committed {
        // COMMIT: Queue actual state mutations
        if lock, ok := s.chain.GetLockedFunds(txID); ok {
            // Source shard: queue DEBIT
            s.chain.AddTx(protocol.Transaction{
                ID:             uuid.New().String(),
                TxType:         protocol.TxTypeCrossDebit,
                CrossShardTxID: txID,
                From:           lock.Address,
                Value:          protocol.NewBigInt(new(big.Int).Set(lock.Amount)),
                IsCrossShard:   true,
            })
        }
        if credits, ok := s.chain.GetPendingCredits(txID); ok {
            // Destination shard: queue CREDIT(s)
            for _, credit := range credits {
                s.chain.AddTx(protocol.Transaction{
                    ID:             uuid.New().String(),
                    TxType:         protocol.TxTypeCrossCredit,
                    CrossShardTxID: txID,
                    To:             credit.Address,
                    Value:          protocol.NewBigInt(new(big.Int).Set(credit.Amount)),
                    IsCrossShard:   true,
                })
            }
        }
        if pendingCall, ok := s.chain.GetPendingCall(txID); ok {
            // Destination shard: queue WRITESET
            s.chain.AddTx(protocol.Transaction{
                ID:             uuid.New().String(),
                TxType:         protocol.TxTypeCrossWriteSet,
                CrossShardTxID: txID,
                RwSet:          pendingCall.RwSet,
                IsCrossShard:   true,
            })
        }
    } else {
        // ABORT: Queue cleanup-only transaction (no state change)
        hasData := false
        if _, ok := s.chain.GetLockedFunds(txID); ok {
            hasData = true
        }
        if _, ok := s.chain.GetPendingCredits(txID); ok {
            hasData = true
        }
        if _, ok := s.chain.GetPendingCall(txID); ok {
            hasData = true
        }
        if hasData {
            s.chain.AddTx(protocol.Transaction{
                ID:             uuid.New().String(),
                TxType:         protocol.TxTypeCrossAbort,
                CrossShardTxID: txID,
                IsCrossShard:   true,
            })
        }
    }

    // Release simulation locks immediately (not deferred to ProduceBlock)
    s.chain.UnlockAllForTx(txID)
}
```

**Transaction Count Per Cross-Shard Operation:**

For a single cross-shard tx, the following transactions may be created:

| Scenario | # Transactions | Types |
|----------|----------------|-------|
| Simple transfer (commit) | 2 | 1 debit (source) + 1 credit (dest) |
| Contract call (commit) | 1-3 | 1 debit + 0-N credits + 1 writeset |
| Any abort | 1 | 1 abort per shard that has pending data |

### ExecuteTx

Clean switch statement with one operation per TxType:

```go
// evm.go ExecuteTx (lines 345-361)
func (e *EVMState) ExecuteTx(tx *protocol.Transaction) error {
    switch tx.TxType {
    case protocol.TxTypeLocal:
        return e.executeLocalTx(tx)
    case protocol.TxTypeCrossDebit:
        return e.Debit(tx.From, tx.Value.ToBigInt())
    case protocol.TxTypeCrossCredit:
        e.Credit(tx.To, tx.Value.ToBigInt())
        return nil
    case protocol.TxTypeCrossWriteSet:
        return e.applyWriteSet(tx.RwSet)
    case protocol.TxTypeCrossAbort:
        return nil // No-op for state; cleanup happens in ProduceBlock
    default:
        return fmt.Errorf("unknown transaction type: %s", tx.TxType)
    }
}

// evm.go applyWriteSet (lines 403-412)
func (e *EVMState) applyWriteSet(rwSet []protocol.RwVariable) error {
    for _, rw := range rwSet {
        for _, write := range rw.WriteSet {
            slot := common.Hash(write.Slot)
            newValue := common.BytesToHash(write.NewValue)
            e.SetStorageAt(rw.Address, slot, newValue)
        }
    }
    return nil
}
```

### ProduceBlock and Cleanup

**Cleanup Timing Rules:**

| TxType | When to Cleanup | What to Clear |
|--------|-----------------|---------------|
| `cross_debit` | After successful execution | `ClearLock(CrossShardTxID)` |
| `cross_credit` | After successful execution | `ClearPendingCreditForAddress(CrossShardTxID, To)` |
| `cross_writeset` | After successful execution | `ClearPendingCall(CrossShardTxID)` |
| `cross_abort` | After execution (always succeeds) | All: lock + credits + call |

**Important:** Multiple transactions may share the same `CrossShardTxID` (e.g., one debit + multiple credits). Each cleans up only its own metadata.

```go
// chain.go ProduceBlock (lines 249-295)
func (c *Chain) ProduceBlock(evmState *EVMState) (*protocol.StateShardBlock, error) {
    c.mu.Lock()
    defer c.mu.Unlock()

    successfulTxs := make([]protocol.Transaction, 0, len(c.currentTxs))

    for _, tx := range c.currentTxs {
        snapshot := evmState.Snapshot()
        if err := evmState.ExecuteTx(&tx); err != nil {
            log.Printf("Chain %d: Failed to execute tx %s: %v (reverted)", c.shardID, tx.ID, err)
            evmState.RevertToSnapshot(snapshot)
            continue
        }

        successfulTxs = append(successfulTxs, tx)

        // Cleanup metadata AFTER successful execution
        c.cleanupAfterExecutionLocked(&tx)
    }

    stateRoot, err := evmState.Commit(c.height + 1)
    // ... create block with TxOrdering = successfulTxs ...
}

// chain.go cleanupAfterExecutionLocked (lines 297-320)
func (c *Chain) cleanupAfterExecutionLocked(tx *protocol.Transaction) {
    if tx.CrossShardTxID == "" {
        return // Local tx, no cleanup needed
    }

    switch tx.TxType {
    case protocol.TxTypeCrossDebit:
        c.clearLockLocked(tx.CrossShardTxID)
    case protocol.TxTypeCrossCredit:
        c.clearPendingCreditForAddressLocked(tx.CrossShardTxID, tx.To)
    case protocol.TxTypeCrossWriteSet:
        c.clearPendingCallLocked(tx.CrossShardTxID)
    case protocol.TxTypeCrossAbort:
        c.clearLockLocked(tx.CrossShardTxID)
        c.clearPendingCreditLocked(tx.CrossShardTxID)
        c.clearPendingCallLocked(tx.CrossShardTxID)
    }
}
```

**Error Handling:**

| Scenario | Behavior |
|----------|----------|
| Debit fails (shouldn't happen - was pre-locked) | Revert snapshot, tx excluded, lock remains |
| Credit fails (shouldn't happen - no validation) | Revert snapshot, tx excluded, pending credit remains |
| WriteSet fails (shouldn't happen - direct write) | Revert snapshot, tx excluded, pending call remains |
| Abort fails (always succeeds - no-op) | N/A |

**Failed cross-shard operations** remain in pending state. They will be retried in the next block or eventually timeout (see simulation lock TTL).

### Benefits of This Approach

1. **All state changes through ProduceBlock:**
   - Consistent execution model
   - All mutations use snapshot/rollback
   - Proper error handling and audit trail

2. **Complete transaction history:**
   - Cross-shard debits/credits/writesets appear in TxOrdering
   - Full audit trail: can reconstruct state from blocks alone
   - Easy debugging: see exactly what operations occurred

3. **No race conditions:**
   - HTTP handler only queues transactions
   - ProduceBlock is the sole state mutator
   - Clear separation: queueing vs execution

4. **Timing consistency:**
   - All transactions executed on block boundaries
   - Deterministic ordering within blocks

### Implementation Summary

The following changes were made to implement the unified transaction model:

1. **protocol/types.go:**
   - Added `TxType` enum with 5 values (lines 150-164)
   - Extended `Transaction` struct with `TxType`, `CrossShardTxID`, `RwSet` fields (lines 180-183)
   - Updated `DeepCopy()` to include new fields (lines 261-273)

2. **evm.go:**
   - Modified `ExecuteTx` with TxType switch (lines 345-361)
   - Added `applyWriteSet` helper (lines 403-412)

3. **server.go:**
   - Modified `handleOrchestratorShardBlock` Phase 1 to queue typed transactions instead of direct execution (lines 692-751)
   - Phase 2 (CtToOrder) unchanged - only locks/votes, no AddTx calls

4. **chain.go:**
   - Added `cleanupAfterExecutionLocked` method (lines 297-320)
   - Added `clearPendingCreditForAddressLocked` method (lines 371-389)
   - ProduceBlock calls cleanup after each successful tx execution

**Backward Compatibility:**
- Empty `TxType` defaults to local execution
- Existing local transactions work unchanged
- No changes to prepare phase (CtToOrder processing)

---

## Design Principles

### 1. Lock-Only 2PC Approach

**Rationale:** Simplifies abort handling by never debiting during prepare phase.

**Implementation:**
- **Prepare:** Check balance, lock amount if sufficient
- **Commit:** Debit from balance, clear lock
- **Abort:** Just clear lock (balance unchanged, no refund needed)

**Alternative (Traditional 2PC):**
- Prepare: Debit immediately
- Commit: Keep debit
- Abort: Refund

**Why Lock-Only is Better:**
- Simpler code (no refund logic)
- Fewer state mutations (debit once vs debit + refund)
- Same safety guarantees (funds reserved during lock)

### 2. Multi-Shard Voting

**Rationale:** All involved shards must agree for commit.

**Involved Shards:**
```go
// internal/protocol/types.go (lines 271-279)
func (tx *CrossShardTx) InvolvedShards() []int {
    shards := tx.TargetShards()  // From RwSet
    // Add source shard if not already included
    for _, s := range shards {
        if s == tx.FromShard {
            return shards
        }
    }
    return append([]int{tx.FromShard}, shards...)
}
```

**Voting Logic:**
```go
// orchestrator/chain.go (lines 77-88)
if !canCommit {
    // First NO vote immediately aborts
    c.pendingResult[txID] = false
    delete(c.awaitingVotes, txID)
    delete(c.votes, txID)
    delete(c.expectedVoters, txID)
    return true
}
```

**Why Multi-Shard:**
- Source must confirm funds available
- Each destination must confirm ReadSet valid
- Any failure aborts (conservative, safe)
- Commit only if all vote YES (unanimous)

### 3. Atomic Check-and-Lock

**Rationale:** Prevent TOCTOU race in concurrent prepare requests.

**Implementation:**
```go
// server.go (lines 769-776)
s.evmState.mu.Lock()
lockedAmount := s.chain.GetLockedAmountForAddress(tx.From)
canCommit := s.evmState.CanDebit(tx.From, tx.Value.ToBigInt(), lockedAmount)
if canCommit {
    s.chain.LockFunds(tx.ID, tx.From, tx.Value.ToBigInt())
}
s.evmState.mu.Unlock()
```

**Race Without Atomicity:**
```
Time   Thread 1 (Prepare tx1)       Thread 2 (Prepare tx2)
----   ---------------------       ---------------------
T0     Check balance=100 ✓
T1                                 Check balance=100 ✓
T2     Lock 80
T3                                 Lock 80
T4     Balance=100, Locked=160 ❌  // Over-committed!
```

**Race With Atomicity:**
```
Time   Thread 1 (Prepare tx1)       Thread 2 (Prepare tx2)
----   ---------------------       ---------------------
T0     LOCK evmState.mu
T1     Check balance=100 ✓
T2     Lock 80
T3     UNLOCK evmState.mu
T4                                 LOCK evmState.mu
T5                                 Check: available=100-80=20
T6                                 Cannot lock 80 ✗
T7                                 UNLOCK evmState.mu
```

### 4. ReadSet Validation for Serializability

**Rationale:** Detect if state changed between simulation and commit.

**Implementation:**
```go
// server.go (lines 1244-1256)
for _, item := range rw.ReadSet {
    slot := common.Hash(item.Slot)
    expectedValue := common.BytesToHash(item.Value)
    actualValue := s.evmState.GetStorageAt(rw.Address, slot)

    if actualValue != expectedValue {
        log.Printf("ReadSet mismatch: expected %s, got %s",
            expectedValue.Hex(), actualValue.Hex())
        return false
    }
}
```

**Why ReadSet Matters:**
- Simulation runs at time T1
- Commit happens at time T2
- State may change between T1 and T2
- ReadSet validation detects conflicts
- Abort if reads are stale (serializability)

**Example Conflict:**
```
T1: Orchestrator simulates tx1, reads storage[slot]=100
T2: Local transaction modifies storage[slot]=200
T3: tx1 prepare arrives, validates ReadSet
    Expected: storage[slot]=100
    Actual:   storage[slot]=200
    Result:   Vote NO, abort tx1
```

### 5. Simulation Locks with TTL

**Rationale:** Prevent state changes during 2PC, but avoid deadlocks.

**Implementation:**
```go
// chain.go (lines 27-29)
const SimulationLockTTL = 2 * time.Minute

type SimulationLock struct {
    // ... fields ...
    CreatedAt time.Time
}

// chain.go (lines 454-484)
func (c *Chain) CleanupExpiredLocks() int {
    now := time.Now()
    for txID, locks := range c.simLocks {
        for _, lock := range locks {
            if now.Sub(lock.CreatedAt) >= SimulationLockTTL {
                // Remove expired lock
            }
        }
    }
}
```

**Why Locks:**
- Prevent local transactions from modifying state during 2PC
- Ensures ReadSet validation is meaningful
- Without locks, concurrent local tx could invalidate simulation

**Why TTL:**
- Orchestrator might crash/hang
- Locks shouldn't persist forever
- 2-minute TTL longer than normal 2PC cycle (~6-9 seconds)
- Expired locks cause abort (conservative, safe)

### 6. WriteSet Direct Application

**Rationale:** Avoid non-deterministic re-execution on commit.

**Current Approach (Direct Application):**
```go
// server.go (lines 733-739)
for _, write := range rw.WriteSet {
    slot := common.Hash(write.Slot)
    newValue := common.BytesToHash(write.NewValue)
    s.evmState.SetStorageAt(rw.Address, slot, newValue)
}
```

**Alternative (Re-Execution):**
```go
// NOT USED - would be non-deterministic
_, _, err := evmState.CallContract(tx.From, tx.To, tx.Data, tx.Value, tx.Gas)
```

**Why Direct Application:**
- Simulation already executed the transaction
- WriteSet contains deterministic results
- Re-execution could differ (timestamp, block number, etc.)
- Direct application ensures commit matches simulation

### 7. Conservative Error Handling

**Principle:** When in doubt, abort.

**Examples:**

1. **Missing Simulation Lock:**
```go
// server.go (lines 1226-1232)
lock, ok := s.chain.GetSimulationLockByAddr(rw.Address)
if !ok {
    log.Printf("No simulation lock - aborting")
    return false  // Vote NO
}
```

2. **ReadSet Mismatch:**
```go
// server.go (lines 1250-1255)
if actualValue != expectedValue {
    log.Printf("ReadSet mismatch - aborting")
    return false  // Vote NO
}
```

3. **First NO Vote:**
```go
// chain.go (lines 81-88)
if !canCommit {
    // Immediate abort on first NO
    c.pendingResult[txID] = false
    return true
}
```

**Rationale:**
- Safety over liveness
- Better to abort and retry than commit incorrectly
- Cross-shard transactions are rare (optimization for common case: local txs)
- User can retry if abort was spurious

---

## Summary

This document defines the complete transaction lifecycle for the sharding system:

**Transaction Types:**
1. Local (same-shard execution)
2. Cross-Shard Prepare (lock resources)
3. Cross-Shard Commit (finalize transaction)
4. Cross-Shard Abort (rollback transaction)

**TxType Values (Implemented):**
- `""` (TxTypeLocal): Normal local EVM execution
- `cross_debit`: Source shard debits locked funds
- `cross_credit`: Destination shard credits pending amount
- `cross_writeset`: Destination shard applies storage writes
- `cross_abort`: Any shard cleans up without state change

**Key Data Structures:**
- Transaction: User-facing transaction format (extended with TxType, CrossShardTxID, RwSet)
- CrossShardTx: Orchestrator-managed cross-shard transaction
- LockedFunds: Source shard lock state
- PendingCredit: Destination shard pending state
- SimulationLock: Prevents concurrent modifications

**Execution Model (Implemented):**
- All transactions (local and cross-shard operations) queued via `chain.AddTx()`
- All state mutations happen in `ProduceBlock` with snapshot/rollback
- Cross-shard operations appear in `TxOrdering` for complete audit trail
- Metadata cleanup happens after successful execution in ProduceBlock

**Design Principles:**
- Lock-only 2PC (no refund logic)
- Multi-shard voting (all must agree)
- Atomic check-and-lock (prevent races)
- ReadSet validation (serializability)
- Simulation locks with TTL (prevent conflicts)
- WriteSet direct application (deterministic)
- Conservative error handling (abort on uncertainty)

**Future Enhancements:**
1. Add transaction validation method for cross-shard operation fields
2. Integration tests for full cross-shard lifecycle with unified model
3. Performance benchmarking of queue-then-execute vs direct execution
4. Consider adding transaction receipts for cross-shard operations

---

**Document Version:** 1.1
**Last Updated:** 2025-12-18
**Authors:** System Architecture Team
**Related Documents:**
- `/docs/architecture.md` - System overview
- `/docs/2pc-protocol.md` - Block-based 2PC protocol
- `/internal/protocol/types.go` - Protocol data structures
- `/internal/shard/chain.go` - State shard chain implementation
- `/internal/shard/evm.go` - EVM execution engine
