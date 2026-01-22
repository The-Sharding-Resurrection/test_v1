# TODO: Design vs Implementation Gap Analysis

This document tracks discrepancies between `docs/design.md` and the current implementation.

## Critical Gaps (Architectural)

### 1. Orchestrator Shard Does Not Store Contract Bytecode

**Design Requirement:**
> "각 샤드에 디플로이 되어 있는 컨트랙트 코드를 자신의 상태로 유지"
> (Maintain contract code deployed to each shard as its own state)

**Current State:** ✅ **IMPLEMENTED** - Orchestrator fetches and caches bytecode on-demand via `StateFetcher`.

**Implementation:**
- [x] Add EVM state to orchestrator → `internal/orchestrator/statedb.go` (SimulationStateDB)
- [x] Fetch contract code from State Shards → `internal/orchestrator/statefetcher.go`
- [x] Cache bytecode (immutable after deploy) → `StateFetcher.codeCache`

**Files:** `internal/orchestrator/statedb.go`, `internal/orchestrator/statefetcher.go`

**Note:** Bytecode is fetched on-demand during simulation rather than synced proactively. This is sufficient for PoC.

---

### 2. Orchestrator Shard Does Not Run Light Nodes

**Design Requirement:**
> "각 샤드의 라이트 노드를 운영" (Operate light nodes for each shard)

**Current State:** Orchestrator Shard trusts State Shard blocks without verification.

**Impact:** No cryptographic verification of state values from State Shards.

**Required Changes:**
- [ ] Implement light client for each State Shard
- [ ] Track State Shard block headers
- [ ] Verify StateRoot against block signatures

**Files:** New file needed: `internal/orchestrator/lightclient.go`

---

### 3. Pre-Execution/Simulation Protocol Not Implemented

**Design Requirement:**
Orchestrator Shard Leader Node pre-executes cross-shard transactions using stored contract code to determine exact RwSet (read/write set).

**Current State:** ✅ **IMPLEMENTED** - Full EVM simulation with RwSet discovery.

**Implementation:**
- [x] Implement EVM simulation in Orchestrator Shard → `internal/orchestrator/simulator.go`
- [x] Execute transaction with fetched bytecode → `Simulator.runSimulation()`
- [x] Capture all SLOAD/SSTORE operations to build RwSet → `SimulationStateDB.BuildRwSet()`
- [x] Background worker processes simulation queue → `Simulator.worker()`

**Files:** `internal/orchestrator/simulator.go`, `internal/orchestrator/statedb.go`

**API:** `POST /cross-shard/call` submits tx for simulation, `GET /cross-shard/simulation/{txid}` checks status.

---

### 4. Request/Reply State Fetch Protocol Not Implemented

**Design Requirement:**
```
Request(ca, slot, referenceBlock) → Reply(val, wit)
```
During simulation, Orchestrator Shard requests state values from State Shards with Merkle proofs.

**Current State:** ✅ **IMPLEMENTED** (without Merkle proofs - deferred to Phase 3)

**Implementation:**
- [x] Add `/state/lock` endpoint to State Shards → locks address, returns account state
- [x] Add `/state/unlock` endpoint to State Shards → releases lock
- [x] Fetch storage slots on-demand → `GET /evm/storage/{addr}/{slot}`
- [x] Integrate with simulation → `StateFetcher` used by `SimulationStateDB`
- [ ] Merkle proof generation (deferred - see #11)
- [ ] Proof verification in Orchestrator (deferred - see #2)

**Files:**
- `internal/shard/server.go` - `/state/lock`, `/state/unlock` endpoints
- `internal/orchestrator/statefetcher.go` - `FetchAndLock()`, `GetStorageAt()`

---

## Major Protocol Differences

### 5. Transaction Structure Mismatch

**Design:**
```go
type Transaction struct {
    TxHash Hash
    From   Address
    To     Address      // <-- Missing in implementation
    Value  int
    Data   []byte
}
```

**Implementation:**
```go
type CrossShardTx struct {
    ID        string
    TxHash    common.Hash
    FromShard int
    From      common.Address
    Value     *big.Int
    Data      []byte
    RwSet     []RwVariable  // Destinations derived from here
    Status    TxStatus
}
```

**Decision:** Current approach (RwSet-derived destinations) is more flexible for multi-shard transactions. Keep as-is but document the deviation.

**Status:** Acceptable deviation - document in design.md

---

### 6. StateShardBlock.tx_ordering Type

**Design:** `tx_ordering []Transaction` (full transaction objects)

**Implementation:** `TxOrdering []TxRef` (just ID + isCrossShard flag)

**Location:** `internal/protocol/block.go:27`

**Decision:** TxRef is sufficient for current needs. Full Transaction would bloat blocks.

**Status:** Acceptable deviation

---

### 7. Destination Shards Do Not Vote

**Design:** Both source and destination shards validate ReadSet and vote.

**Current State:** ✅ **IMPLEMENTED** - All shards with RwSet entries now vote.

**Implementation:**
- [x] Implement destination shard voting → `handleOrchestratorShardBlock()` processes all RwSet entries
- [x] Add ReadSet validation before vote → `validateRwVariable()` checks ReadSet matches state
- [x] Aggregate votes from all involved shards → `OrchestratorChain.RecordVote()` collects votes

**Location:** `internal/shard/server.go:586-630`

---

### 8. ReadSet Validation Not Implemented

**Design:**
> "ct_to_order에 명시된 ReadSet 속 Value와 State Shard의 현재 상태 속 Value가 일치하는지 확인"
> (Check if ReadSet values match current State Shard state)

**Current State:** ✅ **IMPLEMENTED** - ReadSet populated during simulation and validated by State Shards.

**Implementation:**
- [x] Populate ReadSet during simulation → `SimulationStateDB.BuildRwSet()` captures all reads
- [x] Validate ReadSet values in State Shard before voting → `validateRwVariable()`
- [x] Reject (vote NO) if ReadSet values don't match → returns `false` on mismatch

**Location:** `internal/shard/server.go:700-730` (validateRwVariable)

---

### 9. Temporary State Application Not Implemented

**Design:**
> "tpc_prepare = true인 크로스-샤드 트랜잭션에 한해, 해당 트랜잭션의 ReadSet을 임시적으로 상태에 반영"
> (For txs with tpc_prepare=true, temporarily apply ReadSet to state)

**Current State:** Not implemented. External state is not cached.

**Impact:** Subsequent transactions cannot see uncommitted cross-shard state.

**Required Changes:**
- [ ] Add temporary state overlay for pending cross-shard txs
- [ ] Apply ReadSet values to overlay when tpc_prepare=true
- [ ] Clear overlay on commit/abort

---

## Minor Type Differences

### 10. Reference.BlockHeight Type

**Design:** `BlockHeight int`

**Implementation:** `BlockHeight uint64`

**Location:** `internal/protocol/types.go:17`

**Status:** Implementation is correct (block heights should be unsigned). Update design.md.

---

### 11. Merkle Proofs ✅ PARTIALLY IMPLEMENTED

**Design:** `ReadSetItem.Proof` contains Merkle proof for state verification.

**Implementation:** Proof generation and verification implemented, but `ReadSetItem.Proof` is still always `[][]byte{}` (empty) during simulation.

**Location:** `internal/protocol/types.go:132`

**Status:**
- ✅ Proof generation: `EVMState.GetStorageWithProof()` in `internal/shard/evm.go`
- ✅ Proof verification: `VerifyStorageProof()` in `internal/orchestrator/statefetcher.go`
- ✅ HTTP API: `GET /evm/storage/{address}/{slot}?proof=true`
- ⏳ RwSet integration: Deferred - requires modifying simulation to populate proofs
- ⏳ Light client: Blocked by #2 for canonical state root tracking

---

## Internal Inconsistencies

### 12. Deprecated HTTP 2PC Endpoints

**Issue:** Old HTTP-based 2PC endpoints still exist:
- `/cross-shard/prepare`
- `/cross-shard/commit`
- `/cross-shard/abort`
- `/cross-shard/credit`

**Location:** `internal/shard/server.go:95-98`

**Decision Options:**
- [ ] Remove endpoints (breaking change for manual testing)
- [ ] Keep for debugging (adds confusion)
- [ ] Mark as deprecated in code comments

---

### 13. RwSet Never Populated

**Issue:** `CrossShardTx.RwSet` exists but `ReadSet`/`WriteSet` arrays are never filled.

**Current State:** ✅ **IMPLEMENTED** - RwSet fully populated during simulation.

**Implementation:**
- [x] `SimulationStateDB` tracks all `GetState()` calls → populates `ReadSet`
- [x] `SimulationStateDB` tracks all `SetState()` calls → populates `WriteSet`
- [x] `BuildRwSet()` constructs complete `RwVariable` array with ReadSet/WriteSet

**Location:** `internal/orchestrator/statedb.go:554-607`

---

### 14. Multi-Recipient Value Distribution

**Issue:** Each `RwVariable` recipient gets the full `tx.Value` credited.

**Location:** `internal/shard/server.go:579`

**Example:** If tx.Value=100 and RwSet has 3 recipients, each gets 100 (not 33).

**Required Changes:**
- [ ] Add `Amount` field to `RwVariable`, OR
- [ ] Split `tx.Value` among recipients, OR
- [ ] Require single recipient for value transfers

---

## Acknowledged Simplifications

These are documented deviations, not implementation bugs:

| Feature | Design | Implementation | Reference |
|---------|--------|----------------|-----------|
| Validator rotation | Epoch-based | Single validator | README.md:94-99 |
| Proof validation | Merkle/Verkle | Trust without proof | README.md:83-87 |
| Light client | Full verification | Trust blocks | README.md:89-92 |
| Consensus | BFT per shard | Instant finality | README.md:94-99 |
| Block metadata | Synchronized | Per-shard values | See #15 below |

---

### 15. EVM Block/Chain Metadata Opcodes

**Issue:** EVM opcodes that return block/chain metadata may return inconsistent values across shards and between simulation and execution.

**Affected Opcodes:**

| Opcode | Name | State Shard | Orchestrator Sim | Consistency |
|--------|------|-------------|------------------|-------------|
| `0x46` | `CHAINID` | 1337 | 1337 | ✅ Same |
| `0x43` | `NUMBER` | `e.blockNum` (increments) | `1` (hardcoded) | ⚠️ Different |
| `0x42` | `TIMESTAMP` | `e.timestamp` (1700000000) | `1` | ⚠️ Different |
| `0x45` | `GASLIMIT` | 30,000,000 | 30,000,000 | ✅ Same |
| `0x41` | `COINBASE` | `0x0...0` | `0x0...0` | ✅ Same |
| `0x48` | `BASEFEE` | `0` | `1 gwei` | ⚠️ Different |
| `0x44` | `PREVRANDAO` | `0` / empty | `1` | ⚠️ Different |
| `0x40` | `BLOCKHASH` | empty hash | empty hash | ✅ Same (both wrong) |
| `0x32` | `ORIGIN` | caller | tx.From | ✅ Same |
| `0x3A` | `GASPRICE` | `0` | `1 gwei` | ⚠️ Different |

**Potential Issues:**

1. **Simulation vs Execution Mismatch**: If a contract checks `block.timestamp` or `block.number`, simulation result may differ from actual execution on the state shard.

2. **Cross-Shard Block Drift**: Each shard maintains its own `blockNum`. A contract relying on `block.number` could get different values on different shards during a cross-shard transaction.

3. **BLOCKHASH Always Zero**: Contracts using `blockhash(block.number - 1)` for randomness or verification will always get zero.

**Location:**
- State Shard EVM: `internal/shard/evm.go:164-188`
- Orchestrator Simulation: `internal/orchestrator/simulator.go:114-129`

**Production Fix (Deferred):**
- [ ] Orchestrator broadcasts canonical block metadata to all shards
- [ ] All shards use same `block.number` / `block.timestamp` for a 2PC round
- [ ] Or: flag contracts using non-deterministic opcodes

**Status:** Acceptable for PoC - contracts should avoid relying on block metadata for critical logic

---

## Implementation Priority

### ✅ Completed

| # | Task | Status |
|---|------|--------|
| P | State persistence | ✅ LevelDB-backed shard state with in-memory fallback when storage is unavailable |
| 1 | Contract bytecode storage | ✅ On-demand fetch via StateFetcher |
| 3 | Pre-execution simulation | ✅ Simulator with EVM |
| 4 | Request/Reply protocol | ✅ /state/lock, /state/unlock (no Merkle proofs) |
| 7 | Destination shard voting | ✅ validateRwVariable() |
| 8 | ReadSet validation | ✅ Check values match current state |
| 11 | Merkle proof generation/verification | ✅ Implemented (RwSet integration deferred) |
| 13 | RwSet population | ✅ BuildRwSet() from simulation |

---

## Full 2PC Cross-Shard Transaction Roadmap

### Phase A: Complete Lock Lifecycle (Critical) ✅ COMPLETED

**Goal:** Ensure simulation locks are properly released after 2PC completes.

| Task | Description | Status |
|------|-------------|--------|
| A.1 | Release simulation locks on commit | ✅ `server.go:587` |
| A.2 | Release simulation locks on abort | ✅ Same as A.1 |
| A.3 | Orchestrator notifies unlock | ✅ `service.go:102` |
| A.4 | Lock timeout mechanism | ✅ `chain.go:336-381` |

**Implementation:**
- `SimulationLock.CreatedAt` timestamp added
- `SimulationLockTTL = 2 * time.Minute` constant
- `CleanupExpiredLocks()` removes stale locks (TTL check: `>=` not `>`)
- `StartLockCleanup(30 * time.Second)` background goroutine

---

### Phase B: Multi-Shard Vote Aggregation ✅ COMPLETED

**Goal:** Orchestrator must collect votes from ALL involved shards before deciding commit/abort.

| Task | Description | Status |
|------|-------------|--------|
| B.1 | Track expected voters per tx | ✅ `expectedVoters map[string][]int` |
| B.2 | Record per-shard votes | ✅ `votes map[string]map[int]bool` |
| B.3 | Only commit when all vote YES | ✅ `RecordVote()` checks all expected shards |
| B.4 | Abort if any vote NO | ✅ Immediately sets result to false |

**Implementation:**
- `OrchestratorChain` now tracks `votes` and `expectedVoters` per tx
- `RecordVote(txID, shardID, canCommit)` aggregates votes properly
- `StateShardBlock` now includes `ShardID` field
- State shards send vote with their shardID
- First NO vote immediately aborts; all YES votes required to commit

**Files:** `internal/orchestrator/chain.go`, `internal/protocol/block.go`, `internal/orchestrator/service.go`

---

### Phase C: Multi-Recipient Credits ✅ COMPLETED

**Goal:** Support multiple credit recipients per cross-shard transaction.

| Task | Description | Status |
|------|-------------|--------|
| C.1 | Change pendingCredits to list | ✅ `map[string][]*PendingCredit` |
| C.2 | StorePendingCredit appends | ✅ Supports multiple credits per tx |
| C.3 | Apply all credits on commit | ✅ Loops over all credits |

**Implementation:**
- `Chain.pendingCredits` changed from `map[string]*PendingCredit` to `map[string][]*PendingCredit`
- `StorePendingCredit()` now appends to list
- `GetPendingCredits()` returns slice (renamed from `GetPendingCredit`)
- Commit handler applies all credits in list

**Note:** Each recipient still gets the full `tx.Value` (Phase C from original plan was about Amount field, which is deferred).

**Files:** `internal/shard/chain.go`, `internal/shard/server.go`

---

### Phase D: Vote Overwriting Fix ✅ COMPLETED

**Goal:** Prevent vote overwriting when tx has multiple RwSet entries on same shard.

**Implementation:**
- State shard collects all RwSet validations before adding prepare result
- Single combined vote (AND of all validations) per tx per shard

**Files:** `internal/shard/server.go:619-665`

---

### Phase E: Value Distribution Fix (#14)

**Goal:** Correctly distribute tx.Value among multiple recipients.

| Task | Description | Files |
|------|-------------|-------|
| E.1 | **Add Amount field to RwVariable** | `internal/protocol/types.go` |
| | `Amount *big.Int` - how much this recipient receives | |
| C.2 | **Populate Amount during simulation** | `internal/orchestrator/statedb.go` |
| | Track value transfers in BuildRwSet() | |
| C.3 | **Use RwVariable.Amount for credits** | `internal/shard/server.go` |
| | Replace `tx.Value` with `rw.Amount` in StorePendingCredit | |

**Test:** Transfer 100 to addresses on shards 1,2,3 with amounts 50,30,20.

---

### Phase F: Temporary State Overlay (#9)

**Goal:** Allow subsequent txs to read uncommitted cross-shard state.

| Task | Description | Files |
|------|-------------|-------|
| F.1 | **Add overlay state structure** | `internal/shard/evm.go` |
| | `pendingState map[txID]map[addr]map[slot]value` | |
| F.2 | **Apply ReadSet to overlay on prepare** | `internal/shard/server.go` |
| | When tpc_prepare=true, cache ReadSet values | |
| F.3 | **Query overlay before committed state** | `internal/shard/evm.go` |
| | GetStorageAt checks overlay first | |
| F.4 | **Clear overlay on commit/abort** | `internal/shard/server.go` |
| | Remove entries when TpcResult received | |

**Test:** Tx2 reads slot modified by uncommitted Tx1 → sees Tx1's value.

---

### Phase G: Error Handling & Recovery

**Goal:** Handle edge cases gracefully.

| Task | Description | Status |
|------|-------------|--------|
| G.1 | **Vote timeout** | ✅ Implemented (Issue #26, PR #47) |
| | Abort tx if no votes after N blocks | `voteStartBlock` + `voteTimeout` |
| G.2 | **Duplicate vote handling** | ✅ First vote wins |
| | `RecordVote()` ignores duplicate votes from same shard | |
| G.3 | **Simulation failure cleanup** | ✅ Implemented |
| | On EVM error or fetch error, unlock all, set status=failed | |
| G.4 | **Shard disconnect recovery** | Pending |
| | Retry block broadcast on connection failure | |
| G.5 | **Crash recovery** | ✅ Implemented (#21) |
| | Replay missed orchestrator blocks on startup | See note below |

**G.5 Note (Issue #21):** Full crash recovery implemented:
- State shards track `lastOrchestratorHeight` (last processed orchestrator block)
- State shards track `processedCommits` map for commit/abort idempotency
- On startup: `recoverFromOrchestrator()` fetches and replays missed blocks
- Orchestrator provides `GET /block/{height}` and `GET /block/latest` endpoints
- Both height-based and txID-based idempotency prevent duplicate processing

**⚠️ G.5 GAP - Simulation locks not blockchain-compliant:**

Current (broken): Locks acquired/released via HTTP API calls, TTL cleanup outside of blocks.

Correct design:
1. Lock acquisition: Orchestrator block contains lock request → State shard creates Lock tx in block
2. Lock release (success): TpcResult in orchestrator block → State shard creates Unlock tx in block
3. Lock release (failure): Orchestrator produces failure block → State shard creates Unlock tx in block

**Required refactoring:**
- Remove `/state/lock` and `/state/unlock` HTTP endpoints
- Add lock/unlock requests to orchestrator blocks
- State shards process via `TxTypeSimulationLock`/`TxTypeSimulationUnlock` transactions
- Remove TTL-based cleanup (all unlocks must come through blocks)

---

### Phase H: Testing & Documentation

| Task | Description | Status |
|------|-------------|--------|
| H.1 | **Unit tests for simulation components** | Partial |
| | Simulator, StateFetcher, SimulationStateDB | |
| H.2 | **Unit tests for /tx/submit endpoint** | ✅ Implemented |
| | `internal/shard/server_test.go` - local, cross-shard, wrong shard, insufficient balance | |
| H.3 | **Integration test: simple cross-shard transfer** | Pending |
| | scripts/test_simulation.py | |
| H.4 | **Integration test: contract call with storage** | Pending |
| | Deploy contract, call method, verify state on multiple shards | |
| H.5 | **Integration test: concurrent transactions** | Pending |
| | Multiple txs locking same addresses | |
| H.6 | **Update 2pc-protocol.md with simulation flow** | ✅ Completed |
| | Document simulation → prepare → commit lifecycle | |

---

### Phase I: Future Enhancements (Deferred)

| # | Task | Notes |
|---|------|-------|
| 2 | Light node implementation | Cryptographic verification |
| 11 | Merkle proof generation | Required for trustless verification |
| 12 | Deprecate old HTTP 2PC endpoints | After migration complete |

---

## Baseline Protocol Implementation

**Status:** ✅ **COMPLETED** (Experimental protocol for V2 comparison)

The baseline iterative cross-shard protocol has been implemented with all critical fixes applied.

| Task | Description | Status |
|------|-------------|--------|
| B.1 | Implement baseline chain management | ✅ Completed |
| B.2 | Add NoStateError detection in EVM | ✅ Completed |
| B.3 | Create overlay StateDB for re-execution | ✅ Completed |
| B.4 | Implement height tracking | ✅ Fixed - proper increment |
| B.5 | Add lock conflict detection | ✅ Fixed - logging added |
| B.6 | Fix panic-based control flow | ✅ Fixed - deferred recovery |
| B.7 | Fix overlay StateDB usage | ✅ Fixed - use wrapper not inner |
| B.8 | Add error logging | ✅ Fixed - execution failures logged |
| B.9 | Unit tests for baseline | ✅ Completed - comprehensive coverage |
| B.10 | Documentation | ✅ Completed - architecture.md, TODO.md, README.md updated |

**Files:**
- `internal/shard/baseline_chain.go` - Chain state with height tracking and lock management
- `internal/shard/baseline_evm.go` - EVM execution with NoStateError panic recovery
- `internal/shard/baseline_overlay.go` - Overlay StateDB implementation
- `internal/shard/baseline_handlers.go` - HTTP handlers
- `internal/shard/baseline_utils.go` - Utility functions
- `internal/orchestrator/baseline_service.go` - Orchestrator service
- `internal/shard/baseline_test.go` - Comprehensive unit tests

**Key Fixes (from code review):**
- Issue #1: Panic-based control flow fixed with deferred recovery
- Issue #3: Height tracking implemented (increments on each block)
- Issue #4: Lock conflict detection with warning logs
- Issue #5: Overlay StateDB usage corrected (use wrapper, not inner)
- Issue #7: Error logging added for failed executions
- Issue #13: Unit tests added for all major components
- Issue #14: Documentation updated

---

## Next Actions

**Completed:**
- ✅ Phase A - Lock lifecycle (release on commit/abort, TTL cleanup)
- ✅ Phase B - Multi-shard vote aggregation
- ✅ Phase C - Multi-recipient credits
- ✅ Phase D - Vote overwriting fix
- ✅ **Unified Transaction Submission** - Users submit to `/tx/submit`, system auto-detects cross-shard
- ✅ **Baseline Protocol** - Experimental iterative cross-shard protocol with all fixes

**Remaining (Current Implementation):**
1. **Phase E** - Value distribution with Amount field (optional)
2. **Phase F** - Temporary state overlay for sequential txs
3. **Phase G** - Error handling (vote timeout, shard disconnect recovery)
4. **Phase H** - Integration tests and documentation updates

**V2 Migration:** See Phase V section below for V2-specific implementation items.

---

## Phase V: V2 Protocol Migration

**Reference:** `docs/V2.md` contains the full V2 specification.

### V2.1: Transaction Entry Point Change - NOT NEEDED

**V2 Requirement:**
> All transactions are initially sent to the **State Shard** corresponding to the `To` address.
> State Shard performs local simulation to determine if Local Tx or Cross-shard Tx.

**Status:** ❌ **NOT NEEDED** - Current architecture handles routing automatically.

**Why Not Needed:**
The current `/tx/submit` endpoint already provides transparent cross-shard routing:
1. User submits tx to their From shard (simpler UX - no need to know To shard)
2. State Shard simulates with `TrackingStateDB` to detect cross-shard access
3. If cross-shard detected, tx is automatically forwarded to Orchestrator
4. Local txs execute immediately without Orchestrator involvement

This achieves the same outcome as V2.1 without requiring users to determine the correct shard.
The Orchestrator receives cross-shard transactions regardless of entry point.

| Task | Description | Status |
|------|-------------|--------|
| V2.1.1 | Route all txs to State Shard of `To` address first | ❌ Not needed |
| V2.1.2 | State Shard performs initial simulation | ✅ `TrackingStateDB` |
| V2.1.3 | Local txs processed immediately (no Orchestrator) | ✅ Already works |
| V2.1.4 | Cross-shard txs: build partial RwSet, forward to Orchestrator | ✅ Auto-forward |

**Impact:** No changes needed. Current architecture is sufficient.

---

### V2.2: Cross-Shard Simulation ✅ COMPLETED (via Lazy Fetching)

**V2 Requirement:**
> On `NoStateError`: verify RwSet consistency → send `RwSetRequest` to target shard → receive `RwSetReply` → merge and re-execute.

**Status:** ✅ **COMPLETED** - Implemented via **lazy state fetching** instead of iterative re-execution.

| Task | Description | Status |
|------|-------------|--------|
| V2.2.1 | Define `RwSetRequest` / `RwSetReply` message types | ✅ `protocol/types.go` (retained for State Shard endpoint) |
| V2.2.2 | State Shard: `/rw-set` endpoint for sub-call simulation | ✅ `shard/server.go` (available but not used by Orchestrator) |
| V2.2.3 | Orchestrator: handle cross-shard state access | ✅ Lazy fetching via `StateFetcher` |
| V2.2.4 | Orchestrator: build complete RwSet | ✅ `BuildRwSet()` from tracked state |
| V2.2.5 | Single-pass execution | ✅ No re-execution needed |

**Implementation (Lazy State Fetching):**

The original V2.2 design used iterative re-execution with tracer-based NoStateError detection.
This was **replaced** with a simpler lazy state fetching approach that achieves the same goal.

**How it works:**
- `SimulationStateDB.getOrFetchAccount()` lazily fetches bytecode/balance from target shards via HTTP
- `StateFetcher.GetStorageAt()` lazily fetches storage slots from target shards via HTTP
- All state accesses are tracked during single-pass EVM execution
- `BuildRwSet()` constructs the complete RwSet at the end from tracked reads/writes

**Benefits vs Iterative Re-execution:**
- **Simpler:** No iteration loop, no tracer, no merge logic
- **Fewer files:** Removed ~500 lines of code
- **Same result:** Complete RwSet built from all accessed state across all shards

**Trade-off:**
- Multiple HTTP requests per unique storage slot vs batched RwSetRequest
- Acceptable for PoC; can be optimized with batch fetching if needed

**Removed Components:**
- `CrossShardTracer` - No longer needed (state fetched lazily, not detected via tracer)
- `NewSimulationStateDBWithRwSet()` - No preloading needed
- `mergeRwSets()` / `removeStaleRwSet()` - No iteration to merge
- `VerifyRwSetConsistency()` - No preloaded values to verify
- `RequestRwSet()` / `RequestRwSetFromNoStateError()` - Not used (lazy fetch instead)

**Files:**
- `internal/orchestrator/simulator.go` - Single-pass execution
- `internal/orchestrator/statedb.go` - Lazy fetching StateDB, BuildRwSet()
- `internal/orchestrator/statefetcher.go` - HTTP fetching for state/storage

---

### V2.3: Merkle Proof Validation ✅ PARTIALLY IMPLEMENTED

**V2 Requirement:**
> Before simulation, the provided `RwSet` is validated using Merkle Proofs against the referenced State Root.

**Current State:** Proof generation and verification implemented, but not yet integrated into RwSet population.

| Task | Description | Status |
|------|-------------|--------|
| V2.3.1 | Generate Merkle proofs in State Shard | ✅ `EVMState.GetStorageWithProof()` |
| V2.3.2 | Include proofs in `ReadSetItem` | ⏳ Deferred - field exists but not populated |
| V2.3.3 | Validate proofs in Orchestrator before simulation | ✅ `VerifyStorageProof()` |
| V2.3.4 | Reject transactions with invalid proofs | ⏳ Deferred - verification opt-in only |

**Implementation:**
- ✅ Proof generation: `internal/shard/evm.go:GetStorageWithProof()`
- ✅ Proof verification: `internal/orchestrator/statefetcher.go:VerifyStorageProof()`
- ✅ HTTP endpoint: `GET /evm/storage/{address}/{slot}?proof=true`
- ✅ Opt-in verification: `StateFetcher.GetStorageAtWithProof(verifyProof=true)`

**Remaining Work:**
- Populate `ReadSetItem.Proof` during simulation (requires modifying `SimulationStateDB.BuildRwSet()`)
- Enable proof verification by default in cross-shard simulation
- Light client integration for canonical state root tracking

**Dependency:** Full integration requires #2 (light client) for state root validation.

---

### V2.4: Explicit Transaction Types & Ordering ✅ COMPLETED

**V2 Requirement:**
> Transactions must be arranged in `TxOrdering` following: `Finalize → Unlock → Lock → Local`

**Status:** ✅ **COMPLETED** - Optimistic locking with slot-level locks implemented.

| Task | Description | Status |
|------|-------------|--------|
| V2.4.1 | Add `TxTypeFinalize`, `TxTypeUnlock`, `TxTypeLock` types | ✅ `protocol/types.go` |
| V2.4.2 | State Shard: enforce ordering in block production | ✅ `chain.go:sortTransactionsByPriority()` |
| V2.4.3 | Lock transaction: verify ReadSet values, attempt lock | ✅ `evm.go:executeLock()` |
| V2.4.4 | Local transactions: fail if accessing locked state | ✅ `chain.go:checkLocalTxLockConflict()` |
| V2.4.5 | Finalize transaction: apply WriteSet on commit | ✅ `evm.go:applyWriteSet()` |

**Implementation Details:**
- `TxType.Priority()` returns: Finalize(1) > Unlock(2) > Lock(3) > Local(4) > SimError(5)
- `ProduceBlock()` sorts transactions by priority before execution
- Slot-level locking in `chain.go` with `slotLocks` map
- ReadSet validation in `executeLock()` with mismatch detection
- WriteSet application via `TxTypeFinalize` → `applyWriteSet()`

**Files:** `internal/shard/chain.go`, `internal/shard/evm.go`, `internal/protocol/types.go`

**See:** `docs/optimistic-locking.md` for full protocol documentation.

---

### V2.5: RwSet Consistency Verification - N/A (Lazy Fetching)

**V2 Requirement:**
> If `NoStateError` occurs, Orchestrator first verifies if state accessed so far matches declared `RwSet` to detect malicious behavior.

**Status:** ⚪ **NOT APPLICABLE** - No longer needed with lazy state fetching approach.

| Task | Description | Status |
|------|-------------|--------|
| V2.5.1 | Track accessed state during simulation | ✅ `SimulationStateDB` (retained) |
| V2.5.2 | Compare accessed state vs declared RwSet | ⚪ N/A - no preloaded RwSet |
| V2.5.3 | Handle inconsistency | ⚪ N/A - state always fetched fresh |

**Why Not Needed:**
- Lazy state fetching always fetches **current state** directly from shards
- No preloaded RwSet values that could become stale
- Each state access is fetched on-demand, ensuring consistency automatically
- Concurrent state changes are naturally handled by fetching fresh values

**Removed Components:**
- `VerifyRwSetConsistency()` - No preloaded values to verify
- `removeStaleRwSet()` - No stale entries to remove
- `IsAddressPreloaded()` - No preloading

**Note:** Consistency is still validated during 2PC prepare phase on State Shards via ReadSet validation.

---

### V2.6: Terminology Updates (DONE)

| Old Term | New Term | Status |
|----------|----------|--------|
| Worker Shard | State Shard | ✅ Already renamed |
| ContractShardBlock | OrchestratorShardBlock | ✅ Already renamed |

---

### V2 Implementation Priority

**Completed:**
- ✅ **V2.1** - Entry point (NOT NEEDED - current arch sufficient)
- ✅ **V2.2** - Cross-shard simulation (via lazy state fetching - simpler than iterative re-execution)
- ✅ **V2.3** - Merkle proof validation (proof generation and verification implemented, integration deferred)
- ✅ **V2.4** - Explicit transaction types & ordering (optimistic locking)
- ⚪ **V2.5** - RwSet consistency verification (N/A with lazy fetching - state always fresh)
- ✅ **V2.6** - Terminology updates

**Remaining (in priority order):**
1. **V2.3 Integration** - Populate ReadSetItem.Proof and enable proof verification by default
2. **Light Client** (#2) - Required for canonical state root validation

**Dependencies:**
- V2.3 full integration depends on light client (#2) for state root tracking

**Note on V2.2:** The original design specified iterative re-execution with `NoStateError` detection.
This was replaced with lazy state fetching which is simpler and achieves the same goal.
See `docs/V2.md` Implementation Notes for details.

---

## Security Fixes ✅ COMPLETED

### Phase S: Security Hardening

**Goal:** Fix critical and high priority security/correctness issues identified in code review.

| # | Issue | Severity | File(s) | Status |
|---|-------|----------|---------|--------|
| S.1 | SubRefund panic on underflow | CRITICAL | `orchestrator/statedb.go` | ✅ Fixed |
| S.2 | Lock expiration bypass (empty ReadSet) | CRITICAL | `shard/server.go` | ✅ Fixed |
| S.3 | Goroutine leak in block broadcast | CRITICAL | `orchestrator/service.go` | ✅ Fixed |
| S.4 | Pointer aliasing in pending tx map | HIGH | `orchestrator/service.go` | ✅ Fixed |
| S.5 | Missing JSON decode error handling | HIGH | `shard/server.go` | ✅ Fixed |
| S.6 | Non-atomic balance check and lock | HIGH | `shard/evm.go`, `server.go` | ✅ Fixed |
| S.7 | TrackingStateDB not thread-safe | HIGH | `shard/tracking_statedb.go` | ✅ Fixed |
| S.8 | Simulation queue blocking forever | HIGH | `orchestrator/simulator.go` | ✅ Fixed |

**Implementation Details:**

#### S.1: SubRefund Panic Fix
- **Problem:** `SubRefund(gas)` panicked if `gas > refund`, crashing orchestrator
- **Fix:** Clamp to zero instead of panic (matches geth simulation behavior)
- **Location:** `internal/orchestrator/statedb.go:295-301`

#### S.2: Lock Bypass Removed
- **Problem:** Transactions with empty ReadSet bypassed lock validation entirely
- **Fix:** Removed backwards-compatibility exemption - all txs require valid simulation lock
- **Location:** `internal/shard/server.go:1165-1169`

#### S.3: Goroutine Leak Fix
- **Problem:** Unbounded goroutines spawned for each block broadcast
- **Fix:** Added bounded concurrency with semaphore (max 3) and WaitGroup
- **Location:** `internal/orchestrator/service.go:280-320`

#### S.4: Pointer Aliasing Fix
- **Problem:** Stack-local tx stored in map without copy, causing use-after-free
- **Fix:** Use `tx.DeepCopy()` before storing in pending map
- **Location:** `internal/orchestrator/service.go:157-163`

#### S.5: JSON Error Handling
- **Problem:** JSON decode errors ignored, returned empty response
- **Fix:** Added explicit error checking and HTTP error responses
- **Location:** `internal/shard/server.go:368, 1075`

#### S.6: Atomic Balance Lock
- **Problem:** Gap between CanDebit check and LockFunds allowed race condition
- **Fix:** Added mutex to EVMState, wrap check-and-lock in single critical section
- **Location:** `internal/shard/evm.go:37`, `internal/shard/server.go:727-734`

#### S.7: TrackingStateDB Thread Safety
- **Problem:** Maps accessed without synchronization in concurrent simulation
- **Fix:** Added `sync.RWMutex`, protected all map access
- **Location:** `internal/shard/tracking_statedb.go`

#### S.8: Queue Timeout
- **Problem:** Blocking channel send could block forever if queue full
- **Fix:** Added 5-second timeout with select, returns error on timeout
- **Location:** `internal/orchestrator/simulator.go:79`

**Test Files Added:**
- `internal/orchestrator/statedb_test.go` - SubRefund underflow test
- `internal/orchestrator/service_test.go` - Pointer aliasing, broadcast concurrency tests
- `internal/orchestrator/simulator_test.go` - Queue timeout test
- `internal/shard/security_fixes_test.go` - Lock bypass, atomic balance, thread safety tests

**Design Decision - NOT Implemented:**
- **ReadSet re-validation at commit time** was considered but rejected
- Reason: Violates 2PC atomicity - once `TpcResult=true` is broadcast, ALL shards MUST commit
- ReadSet validation happens during PREPARE phase, not commit phase
