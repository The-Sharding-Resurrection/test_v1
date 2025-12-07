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

### 11. Merkle Proofs Always Empty

**Design:** `ReadSetItem.Proof` contains Merkle proof for state verification.

**Implementation:** `Proof` is always `[][]byte{}` (empty).

**Location:** `internal/protocol/types.go:23`

**Status:** Documented as deferred in README.md. Blocked by #2 and #4.

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

---

## Implementation Priority

### Phase 1: Core Protocol Completion
1. [x] #7 - Destination shard voting ✅
2. [x] #8 - ReadSet validation ✅
3. [ ] #14 - Multi-recipient value fix

### Phase 2: Simulation Protocol
4. [x] #1 - Contract bytecode storage ✅
5. [x] #3 - Pre-execution simulation ✅
6. [x] #13 - RwSet population ✅

### Phase 3: Verification
7. [ ] #2 - Light node implementation
8. [x] #4 - Request/Reply protocol ✅ (without Merkle proofs)
9. [ ] #11 - Merkle proof generation

### Phase 4: Cleanup
10. [ ] #9 - Temporary state overlay
11. [ ] #12 - Deprecate/remove old endpoints
12. [ ] Update design.md with implementation decisions

### New Tasks (from orchestrator-evm branch)
13. [ ] Release simulation locks on 2PC commit/abort
14. [ ] Add unit tests for Simulator, StateFetcher, SimulationStateDB
15. [ ] Integration test for `/cross-shard/call` simulation flow
