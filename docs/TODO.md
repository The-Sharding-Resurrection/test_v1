# TODO: Design vs Implementation Gap Analysis

This document tracks discrepancies between `docs/design.md` and the current implementation.

## Critical Gaps (Architectural)

### 1. Contract Shard Does Not Store Contract Bytecode

**Design Requirement:**
> "각 샤드에 디플로이 되어 있는 컨트랙트 코드를 자신의 상태로 유지"
> (Maintain contract code deployed to each shard as its own state)

**Current State:** Orchestrator (`internal/orchestrator/`) is completely stateless. No EVM state, no contract storage.

**Impact:** Cannot pre-execute cross-shard transactions to determine RwSet.

**Required Changes:**
- [ ] Add EVM state to orchestrator (similar to `internal/shard/evm.go`)
- [ ] Sync contract deployments from State Shards to Contract Shard
- [ ] Consider blob-like expiration for stored bytecode

**Files:** `internal/orchestrator/service.go`

---

### 2. Contract Shard Does Not Run Light Nodes

**Design Requirement:**
> "각 샤드의 라이트 노드를 운영" (Operate light nodes for each shard)

**Current State:** Contract Shard trusts State Shard blocks without verification.

**Impact:** No cryptographic verification of state values from State Shards.

**Required Changes:**
- [ ] Implement light client for each State Shard
- [ ] Track State Shard block headers
- [ ] Verify StateRoot against block signatures

**Files:** New file needed: `internal/orchestrator/lightclient.go`

---

### 3. Pre-Execution/Simulation Protocol Not Implemented

**Design Requirement:**
Contract Shard Leader Node pre-executes cross-shard transactions using stored contract code to determine exact RwSet (read/write set).

**Current State:** No simulation. Orchestrator just queues transactions with user-provided RwSet.

**Impact:**
- RwSet must be manually specified by caller
- Cannot automatically detect cross-shard state dependencies
- Cannot validate RwSet correctness

**Required Changes:**
- [ ] Implement EVM simulation in Contract Shard
- [ ] Execute transaction with stored bytecode
- [ ] Capture all SLOAD/SSTORE operations to build RwSet
- [ ] Validate user-provided RwSet against simulation result

**Files:** New file needed: `internal/orchestrator/simulation.go`

---

### 4. Request/Reply State Fetch Protocol Not Implemented

**Design Requirement:**
```
Request(ca, slot, referenceBlock) → Reply(val, wit)
```
During simulation, Contract Shard requests state values from State Shards with Merkle proofs.

**Current State:** No such protocol exists.

**Impact:** Cannot fetch external state during simulation.

**Required Changes:**
- [ ] Add `/state/request` endpoint to State Shards
- [ ] Implement Merkle proof generation in State Shards
- [ ] Add proof verification in Contract Shard
- [ ] Integrate with simulation protocol

**Files:**
- `internal/shard/server.go` - new endpoint
- `internal/orchestrator/simulation.go` - state fetcher

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

**Implementation:** Only source shard votes (balance check). Destination shards just store pending credits.

**Location:** `internal/shard/server.go:552-584`

**Impact:** Destination shards cannot reject transactions based on state conflicts.

**Required Changes:**
- [ ] Implement destination shard voting
- [ ] Add ReadSet validation before vote
- [ ] Aggregate votes from all involved shards in Contract Shard

---

### 8. ReadSet Validation Not Implemented

**Design:**
> "ct_to_order에 명시된 ReadSet 속 Value와 State Shard의 현재 상태 속 Value가 일치하는지 확인"
> (Check if ReadSet values match current State Shard state)

**Current State:** `ReadSet`/`WriteSet` fields exist but are never populated or validated.

**Location:** `internal/protocol/types.go:20-32`

**Required Changes:**
- [ ] Populate ReadSet during simulation (blocked by #3)
- [ ] Validate ReadSet values in State Shard before voting
- [ ] Reject (vote NO) if ReadSet values don't match current state

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

**Current Usage:** Only `RwVariable.Address` and `RwVariable.ReferenceBlock.ShardNum` are used.

**Blocked By:** #3 (simulation protocol)

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
1. [ ] #7 - Destination shard voting
2. [ ] #8 - ReadSet validation
3. [ ] #14 - Multi-recipient value fix

### Phase 2: Simulation Protocol
4. [ ] #1 - Contract bytecode storage
5. [ ] #3 - Pre-execution simulation
6. [ ] #13 - RwSet population

### Phase 3: Verification
7. [ ] #2 - Light node implementation
8. [ ] #4 - Request/Reply protocol
9. [ ] #11 - Merkle proof generation

### Phase 4: Cleanup
10. [ ] #9 - Temporary state overlay
11. [ ] #12 - Deprecate/remove old endpoints
12. [ ] Update design.md with implementation decisions
