# EVM Execution Implementation for Cross-Shard Transactions

This document provides a comprehensive design for implementing EVM execution capabilities in the Orchestrator Shard, enabling automatic discovery of cross-shard state dependencies.

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Design Decisions](#design-decisions)
4. [Protocol Types](#protocol-types)
5. [State Shard Implementation](#state-shard-implementation)
6. [Orchestrator Implementation](#orchestrator-implementation)
7. [Execution Flow](#execution-flow)
8. [2PC Integration](#2pc-integration)
9. [API Reference](#api-reference)
10. [Error Handling](#error-handling)
11. [Known Limitations](#known-limitations)

---

## Overview

### Problem Statement

When a cross-shard transaction involves contract calls (e.g., Contract C1 on Shard 1 calling Contract C2 on Shard 2), the system needs to:

1. **Discover dependencies**: Determine which addresses/storage slots will be read/written
2. **Lock state**: Prevent concurrent modifications during transaction processing
3. **Execute atomically**: Ensure all-or-nothing semantics across shards

### Solution

The Orchestrator Shard **simulates** cross-shard transactions before including them in blocks:

1. **Simulation Phase**: Orchestrator executes the transaction locally, fetching and locking state from State Shards as needed
2. **RwSet Construction**: Build Read/Write set from tracked state accesses during simulation
3. **2PC Execution**: State Shards validate ReadSet and apply WriteSet atomically

### Key Insight

**Simulation locks ARE the prepare locks**. There are no separate phases. When the Orchestrator fetches state during simulation, the State Shard immediately locks that address. The lock remains until 2PC completes (commit or abort).

---

## Architecture

```
                                    Cross-Shard Contract Call
                                              |
                                              v
┌─────────────────────────────────────────────────────────────────────────┐
│                           ORCHESTRATOR SHARD                             │
│                                                                          │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────────────────┐   │
│  │   Service    │───>│  Simulator   │───>│   SimulationStateDB      │   │
│  │              │    │  (Worker)    │    │   (vm.StateDB impl)      │   │
│  │ /cross-shard │    │              │    │                          │   │
│  │ /call        │    │  - Queue     │    │  - Tracks reads/writes   │   │
│  └──────────────┘    │  - EVM exec  │    │  - Caches account state  │   │
│                      │  - RwSet     │    └────────────┬─────────────┘   │
│                      └──────────────┘                 │                  │
│                                                       v                  │
│                                          ┌──────────────────────────┐   │
│                                          │     StateFetcher         │   │
│                                          │                          │   │
│                                          │  - HTTP client           │   │
│                                          │  - Bytecode cache        │   │
│                                          │  - Lock tracking         │   │
│                                          └────────────┬─────────────┘   │
└───────────────────────────────────────────────────────┼─────────────────┘
                                                        │
                            ┌───────────────────────────┼───────────────────────────┐
                            │                           │                           │
                            v                           v                           v
                   ┌─────────────────┐         ┌─────────────────┐         ┌─────────────────┐
                   │   STATE SHARD 0  │         │   STATE SHARD 1  │         │   STATE SHARD N  │
                   │                  │         │                  │         │                  │
                   │  /state/lock    │         │  /state/lock    │         │  /state/lock    │
                   │  /state/unlock  │         │  /state/unlock  │         │  /state/unlock  │
                   │                  │         │                  │         │                  │
                   │  SimulationLock │         │  SimulationLock │         │  SimulationLock │
                   │  {txID, addr,   │         │  {txID, addr,   │         │  {txID, addr,   │
                   │   balance,      │         │   balance,      │         │   balance,      │
                   │   code,         │         │   code,         │         │   code,         │
                   │   storage}      │         │   storage}      │         │   storage}      │
                   └─────────────────┘         └─────────────────┘         └─────────────────┘
```

---

## Design Decisions

### PoC Simplifications

| Aspect | PoC Choice | Production Alternative |
|--------|------------|------------------------|
| Lock Granularity | Lock entire address | Lock individual storage slots |
| Merkle Proofs | Skip verification | Full Merkle proof validation |
| State Fetching | Sync HTTP per address | Batch requests, async fetching |
| Lock Timeout | No timeout | Configurable timeout with auto-abort |
| Conflict Resolution | First-come-first-served | Priority queuing, deadlock detection |

### Confirmed Design Choices

1. **Background Simulation**: Transactions are queued and simulated by a background worker. Submit returns immediately with a transaction ID.

2. **Bytecode Caching**: Contract bytecode is fetched once and cached (immutable after deployment).

3. **Full Address State**: On lock, State Shard returns complete account state (balance, nonce, code, all storage).

4. **Unified Lock**: Simulation lock IS the 2PC prepare lock. No separate phases.

5. **State Shard Authority**: State Shards are the source of truth. Orchestrator simulates but doesn't store state permanently.

6. **Immediate Rejection**: Simulation failures reject the transaction immediately and unlock all locked addresses.

---

## Protocol Types

### SimulationStatus

```go
type SimulationStatus string

const (
    SimPending    SimulationStatus = "sim_pending"    // Queued for simulation
    SimRunning    SimulationStatus = "sim_running"    // Currently simulating
    SimSuccess    SimulationStatus = "sim_success"    // Simulation completed
    SimFailed     SimulationStatus = "sim_failed"     // Simulation failed
)
```

### LockRequest / LockResponse

```go
// LockRequest requests exclusive lock on an address for simulation
type LockRequest struct {
    TxID    string         `json:"tx_id"`    // Transaction ID for lock ownership
    Address common.Address `json:"address"`  // Address to lock
}

// LockResponse returns full account state after successful lock
type LockResponse struct {
    Success  bool                        `json:"success"`
    Error    string                      `json:"error,omitempty"`
    Balance  *big.Int                    `json:"balance"`
    Nonce    uint64                      `json:"nonce"`
    Code     []byte                      `json:"code"`      // Contract bytecode
    CodeHash common.Hash                 `json:"code_hash"`
    Storage  map[common.Hash]common.Hash `json:"storage"`   // Full storage snapshot

    // NOTE: Merkle proofs skipped for PoC simplicity
    // In production, would include:
    // AccountProof  [][]byte `json:"account_proof"`
    // StorageProofs map[common.Hash][][]byte `json:"storage_proofs"`
}
```

### UnlockRequest

```go
// UnlockRequest releases a lock on simulation failure
type UnlockRequest struct {
    TxID    string         `json:"tx_id"`
    Address common.Address `json:"address"`
}
```

### Extended CrossShardTx

```go
type CrossShardTx struct {
    // Existing fields
    ID        string
    TxHash    common.Hash
    FromShard int
    From      common.Address
    Value     *big.Int
    Data      []byte
    RwSet     []RwVariable
    Status    TxStatus

    // New fields for contract calls
    To        common.Address   `json:"to,omitempty"`         // Target contract
    Gas       uint64           `json:"gas,omitempty"`        // Gas limit
    SimStatus SimulationStatus `json:"sim_status,omitempty"` // Simulation state
}
```

---

## State Shard Implementation

### SimulationLock

```go
// SimulationLock represents an address locked during simulation
// This lock is also used for 2PC (no separate prepare lock)
type SimulationLock struct {
    TxID      string
    Address   common.Address
    LockedAt  time.Time

    // Account state snapshot at lock time
    Balance   *big.Int
    Nonce     uint64
    Code      []byte
    CodeHash  common.Hash
    Storage   map[common.Hash]common.Hash
}
```

### Chain Extensions

```go
type Chain struct {
    // ... existing fields ...

    // Simulation locks (address-level)
    simLocks       map[string]*SimulationLock    // txID -> lock info
    simLocksByAddr map[common.Address]string     // address -> owning txID
}

// LockAddress acquires exclusive lock on an address
// Returns error if address is already locked by another tx
func (c *Chain) LockAddress(txID string, addr common.Address, state *EVMState) (*SimulationLock, error) {
    c.mu.Lock()
    defer c.mu.Unlock()

    // Check for existing lock
    if existingTxID, locked := c.simLocksByAddr[addr]; locked {
        if existingTxID != txID {
            return nil, fmt.Errorf("address %s already locked by tx %s", addr.Hex(), existingTxID)
        }
        // Same tx re-requesting lock - return existing
        return c.simLocks[txID], nil
    }

    // Capture full account state
    balance, nonce, code, codeHash, storage := state.GetFullAccountState(addr)

    lock := &SimulationLock{
        TxID:     txID,
        Address:  addr,
        LockedAt: time.Now(),
        Balance:  balance,
        Nonce:    nonce,
        Code:     code,
        CodeHash: codeHash,
        Storage:  storage,
    }

    c.simLocks[txID] = lock
    c.simLocksByAddr[addr] = txID

    return lock, nil
}

// UnlockAddress releases a simulation lock
func (c *Chain) UnlockAddress(txID string, addr common.Address) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if owningTx, ok := c.simLocksByAddr[addr]; ok && owningTx == txID {
        delete(c.simLocksByAddr, addr)
    }
    delete(c.simLocks, txID)
}

// IsAddressLocked checks if an address is currently locked
func (c *Chain) IsAddressLocked(addr common.Address) bool {
    c.mu.RLock()
    defer c.mu.RUnlock()
    _, locked := c.simLocksByAddr[addr]
    return locked
}

// GetSimulationLock returns lock info for an address
func (c *Chain) GetSimulationLock(addr common.Address) (*SimulationLock, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    txID, ok := c.simLocksByAddr[addr]
    if !ok {
        return nil, false
    }
    return c.simLocks[txID], true
}
```

### New HTTP Endpoints

#### POST /state/lock

Lock an address and return its full state.

**Request:**
```json
{
    "tx_id": "abc-123",
    "address": "0x1234..."
}
```

**Response (Success):**
```json
{
    "success": true,
    "balance": "1000000000000000000",
    "nonce": 5,
    "code": "0x6080...",
    "code_hash": "0xabc...",
    "storage": {
        "0x0000...0000": "0x0000...0001",
        "0x0000...0001": "0x0000...00ff"
    }
}
```

**Response (Conflict):**
```json
{
    "success": false,
    "error": "address 0x1234... already locked by tx xyz-789"
}
```

#### POST /state/unlock

Release a lock after simulation failure.

**Request:**
```json
{
    "tx_id": "abc-123",
    "address": "0x1234..."
}
```

**Response:**
```json
{
    "success": true
}
```

---

## Orchestrator Implementation

### StateFetcher

Handles HTTP communication with State Shards for state fetching and locking.

```go
type StateFetcher struct {
    numShards  int
    httpClient *http.Client

    // Bytecode cache (immutable after deploy)
    codeCacheMu sync.RWMutex
    codeCache   map[common.Address][]byte

    // Lock tracking per transaction for cleanup
    locksMu sync.RWMutex
    txLocks map[string][]lockedAddr
}

type lockedAddr struct {
    shardID int
    address common.Address
}

// AddressToShard determines which shard owns an address
// PoC: Simple modulo-based assignment
func (f *StateFetcher) AddressToShard(addr common.Address) int {
    return int(addr[0]) % f.numShards
}

// FetchAndLock locks an address and returns its state
func (f *StateFetcher) FetchAndLock(txID string, addr common.Address) (*protocol.LockResponse, error) {
    shardID := f.AddressToShard(addr)
    url := f.shardURL(shardID) + "/state/lock"

    req := protocol.LockRequest{TxID: txID, Address: addr}
    resp, err := f.post(url, req)
    if err != nil {
        return nil, fmt.Errorf("lock request failed: %w", err)
    }

    var lockResp protocol.LockResponse
    if err := json.NewDecoder(resp.Body).Decode(&lockResp); err != nil {
        return nil, err
    }

    if lockResp.Success {
        // Track lock for potential cleanup
        f.locksMu.Lock()
        f.txLocks[txID] = append(f.txLocks[txID], lockedAddr{shardID, addr})
        f.locksMu.Unlock()

        // Cache bytecode if present
        if len(lockResp.Code) > 0 {
            f.codeCacheMu.Lock()
            f.codeCache[addr] = lockResp.Code
            f.codeCacheMu.Unlock()
        }
    }

    return &lockResp, nil
}

// UnlockAll releases all locks held by a transaction (on failure)
func (f *StateFetcher) UnlockAll(txID string) {
    f.locksMu.Lock()
    locks := f.txLocks[txID]
    delete(f.txLocks, txID)
    f.locksMu.Unlock()

    for _, lock := range locks {
        url := f.shardURL(lock.shardID) + "/state/unlock"
        req := protocol.UnlockRequest{TxID: txID, Address: lock.address}
        f.post(url, req) // Best effort, ignore errors
    }
}

// GetCachedCode returns cached bytecode if available
func (f *StateFetcher) GetCachedCode(addr common.Address) ([]byte, bool) {
    f.codeCacheMu.RLock()
    defer f.codeCacheMu.RUnlock()
    code, ok := f.codeCache[addr]
    return code, ok
}
```

### SimulationStateDB

Custom implementation of `vm.StateDB` that fetches state from State Shards.

```go
// SimulationStateDB implements vm.StateDB for cross-shard simulation
type SimulationStateDB struct {
    mu sync.RWMutex

    txID    string
    fetcher *StateFetcher

    // Cached account state (from locked accounts)
    accounts map[common.Address]*AccountState

    // Write buffer (modifications during simulation)
    dirtyStorage map[common.Address]map[common.Hash]common.Hash
    dirtyBalance map[common.Address]*big.Int
    dirtyNonce   map[common.Address]uint64

    // Access tracking for RwSet construction
    readSlots  map[common.Address]map[common.Hash]common.Hash  // addr -> slot -> value
    writeSlots map[common.Address]map[common.Hash]common.Hash  // addr -> slot -> new value

    // Error during fetch
    fetchError error
}

type AccountState struct {
    Balance  *big.Int
    Nonce    uint64
    Code     []byte
    CodeHash common.Hash
    Storage  map[common.Hash]common.Hash
    ShardID  int
}

// ensureAccount fetches and locks account if not already cached
func (s *SimulationStateDB) ensureAccount(addr common.Address) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.fetchError != nil {
        return s.fetchError
    }

    if _, exists := s.accounts[addr]; exists {
        return nil
    }

    resp, err := s.fetcher.FetchAndLock(s.txID, addr)
    if err != nil {
        s.fetchError = err
        return err
    }

    if !resp.Success {
        s.fetchError = fmt.Errorf("lock failed: %s", resp.Error)
        return s.fetchError
    }

    s.accounts[addr] = &AccountState{
        Balance:  resp.Balance,
        Nonce:    resp.Nonce,
        Code:     resp.Code,
        CodeHash: resp.CodeHash,
        Storage:  resp.Storage,
        ShardID:  s.fetcher.AddressToShard(addr),
    }

    return nil
}

// GetState implements vm.StateDB - fetches storage slot
func (s *SimulationStateDB) GetState(addr common.Address, slot common.Hash) common.Hash {
    if err := s.ensureAccount(addr); err != nil {
        return common.Hash{}
    }

    s.mu.RLock()
    defer s.mu.RUnlock()

    // Check dirty storage first
    if dirty, ok := s.dirtyStorage[addr]; ok {
        if val, ok := dirty[slot]; ok {
            return val
        }
    }

    // Read from cached account state
    account := s.accounts[addr]
    value := account.Storage[slot]

    // Track read for RwSet
    if s.readSlots[addr] == nil {
        s.readSlots[addr] = make(map[common.Hash]common.Hash)
    }
    s.readSlots[addr][slot] = value

    return value
}

// SetState implements vm.StateDB - records storage write
func (s *SimulationStateDB) SetState(addr common.Address, slot common.Hash, value common.Hash) {
    if err := s.ensureAccount(addr); err != nil {
        return
    }

    s.mu.Lock()
    defer s.mu.Unlock()

    if s.dirtyStorage[addr] == nil {
        s.dirtyStorage[addr] = make(map[common.Hash]common.Hash)
    }
    s.dirtyStorage[addr][slot] = value

    // Track write for RwSet
    if s.writeSlots[addr] == nil {
        s.writeSlots[addr] = make(map[common.Hash]common.Hash)
    }
    s.writeSlots[addr][slot] = value
}

// BuildRwSet constructs RwSet from tracked reads/writes
func (s *SimulationStateDB) BuildRwSet() []protocol.RwVariable {
    s.mu.RLock()
    defer s.mu.RUnlock()

    var rwSet []protocol.RwVariable

    // Collect all accessed addresses
    allAddrs := make(map[common.Address]bool)
    for addr := range s.readSlots {
        allAddrs[addr] = true
    }
    for addr := range s.writeSlots {
        allAddrs[addr] = true
    }

    for addr := range allAddrs {
        account := s.accounts[addr]

        rw := protocol.RwVariable{
            Address: addr,
            ReferenceBlock: protocol.Reference{
                ShardNum: account.ShardID,
                // BlockHash/Height would be set from lock response in production
            },
        }

        // Add read slots
        if reads, ok := s.readSlots[addr]; ok {
            for slot, value := range reads {
                rw.ReadSet = append(rw.ReadSet, protocol.ReadSetItem{
                    Slot:  protocol.Slot(slot),
                    Value: value.Bytes(),
                    // Proof: nil - skipped for PoC
                })
            }
        }

        // Add write slots
        if writes, ok := s.writeSlots[addr]; ok {
            for slot := range writes {
                rw.WriteSet = append(rw.WriteSet, protocol.Slot(slot))
            }
        }

        rwSet = append(rwSet, rw)
    }

    return rwSet
}
```

### Simulator

Background worker processing simulation queue.

```go
type Simulator struct {
    fetcher  *StateFetcher
    chainCfg *params.ChainConfig

    queue    chan *SimulationRequest

    statusMu sync.RWMutex
    statuses map[string]protocol.SimulationStatus
    results  map[string]*SimulationResult

    onSuccess func(tx *protocol.CrossShardTx)
}

type SimulationRequest struct {
    Tx         *protocol.CrossShardTx
    ResultChan chan *SimulationResult
}

type SimulationResult struct {
    TxID    string
    Success bool
    Error   string
    RwSet   []protocol.RwVariable
    GasUsed uint64
}

// Submit queues a transaction for simulation
func (s *Simulator) Submit(tx *protocol.CrossShardTx) string {
    s.statusMu.Lock()
    tx.SimStatus = protocol.SimPending
    s.statuses[tx.ID] = protocol.SimPending
    s.statusMu.Unlock()

    s.queue <- &SimulationRequest{Tx: tx}
    return tx.ID
}

// GetStatus returns current simulation status
func (s *Simulator) GetStatus(txID string) (protocol.SimulationStatus, *SimulationResult) {
    s.statusMu.RLock()
    defer s.statusMu.RUnlock()
    return s.statuses[txID], s.results[txID]
}

// worker processes simulation queue
func (s *Simulator) worker() {
    for req := range s.queue {
        s.processSimulation(req)
    }
}

func (s *Simulator) processSimulation(req *SimulationRequest) {
    tx := req.Tx

    // Update status to running
    s.statusMu.Lock()
    s.statuses[tx.ID] = protocol.SimRunning
    tx.SimStatus = protocol.SimRunning
    s.statusMu.Unlock()

    // Create simulation StateDB
    stateDB := NewSimulationStateDB(tx.ID, s.fetcher)

    // Create EVM
    evm := s.createEVM(stateDB, tx)

    // Execute transaction
    var ret []byte
    var gasUsed uint64
    var err error

    if tx.To == (common.Address{}) {
        // Contract creation
        ret, _, gasUsed, err = evm.Create(tx.From, tx.Data, tx.Gas, uint256.MustFromBig(tx.Value))
    } else {
        // Contract call
        ret, gasUsed, err = evm.Call(tx.From, tx.To, tx.Data, tx.Gas, uint256.MustFromBig(tx.Value))
    }

    // Check for fetch errors during execution
    if stateDB.fetchError != nil {
        err = stateDB.fetchError
    }

    result := &SimulationResult{
        TxID:    tx.ID,
        GasUsed: gasUsed,
    }

    if err != nil {
        // Simulation failed - unlock all and record error
        s.fetcher.UnlockAll(tx.ID)

        result.Success = false
        result.Error = err.Error()

        s.statusMu.Lock()
        s.statuses[tx.ID] = protocol.SimFailed
        tx.SimStatus = protocol.SimFailed
        s.results[tx.ID] = result
        s.statusMu.Unlock()

        return
    }

    // Simulation succeeded - build RwSet
    rwSet := stateDB.BuildRwSet()
    tx.RwSet = rwSet

    result.Success = true
    result.RwSet = rwSet

    s.statusMu.Lock()
    s.statuses[tx.ID] = protocol.SimSuccess
    tx.SimStatus = protocol.SimSuccess
    s.results[tx.ID] = result
    s.statusMu.Unlock()

    // Callback to add tx to pending (locks remain held)
    if s.onSuccess != nil {
        s.onSuccess(tx)
    }

    _ = ret // Return data available in result if needed
}
```

---

## Execution Flow

### Complete Transaction Lifecycle

```
┌─────────────────────────────────────────────────────────────────────────┐
│ 1. USER SUBMISSION                                                       │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│   User ──POST /cross-shard/call──> Orchestrator                         │
│                                          │                               │
│                                    Queue to Simulator                    │
│                                          │                               │
│   User <──{tx_id, sim_status: pending}───┘                              │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    v
┌─────────────────────────────────────────────────────────────────────────┐
│ 2. SIMULATION (Background)                                               │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│   Simulator picks up tx from queue                                       │
│        │                                                                 │
│        v                                                                 │
│   Create SimulationStateDB                                               │
│        │                                                                 │
│        v                                                                 │
│   EVM.Call(from, to, data, gas, value)                                  │
│        │                                                                 │
│        │──SLOAD(addr, slot)──> SimulationStateDB.GetState()             │
│        │                              │                                  │
│        │                              v                                  │
│        │                       Not cached?                               │
│        │                              │                                  │
│        │                              v                                  │
│        │                       POST /state/lock to State Shard          │
│        │                              │                                  │
│        │                              v                                  │
│        │                       State Shard locks address                │
│        │                       Returns full account state               │
│        │                              │                                  │
│        │<─────────value───────────────┘                                 │
│        │                                                                 │
│        │──SSTORE(addr, slot, value)──> SimulationStateDB.SetState()    │
│        │                              │                                  │
│        │                              v                                  │
│        │                       Track in writeSlots                      │
│        │                                                                 │
│        v                                                                 │
│   EVM execution completes                                                │
│        │                                                                 │
│        v                                                                 │
│   BuildRwSet() from readSlots/writeSlots                                │
│        │                                                                 │
│        v                                                                 │
│   Add tx with RwSet to pendingTxs                                       │
│   (Locks remain held on State Shards)                                   │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    v
┌─────────────────────────────────────────────────────────────────────────┐
│ 3. BLOCK PRODUCTION (Every 3s)                                           │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│   Orchestrator produces OrchestratorShardBlock:                         │
│   {                                                                      │
│       Height: N,                                                         │
│       TpcResult: { ... },  // Previous round decisions                  │
│       CtToOrder: [tx]      // Simulated tx with RwSet                   │
│   }                                                                      │
│        │                                                                 │
│        v                                                                 │
│   Broadcast to all State Shards                                         │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    v
┌─────────────────────────────────────────────────────────────────────────┐
│ 4. STATE SHARD PROCESSING                                                │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│   For each tx in CtToOrder:                                             │
│        │                                                                 │
│        v                                                                 │
│   Check: Is address locked by this tx?                                  │
│        │                                                                 │
│        ├──NO──> Vote: false (TpcPrepare)                                │
│        │                                                                 │
│        v                                                                 │
│   Validate: Does ReadSet match locked state?                            │
│        │                                                                 │
│        ├──NO──> Vote: false (TpcPrepare)                                │
│        │                                                                 │
│        v                                                                 │
│   Vote: true (TpcPrepare)                                               │
│        │                                                                 │
│        v                                                                 │
│   Produce StateShardBlock with TpcPrepare                               │
│   Send to Orchestrator                                                   │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    v
┌─────────────────────────────────────────────────────────────────────────┐
│ 5. 2PC COMPLETION (Next Block)                                           │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│   Orchestrator collects votes, produces next block:                     │
│   {                                                                      │
│       TpcResult: { tx: true/false },  // Commit or abort                │
│       CtToOrder: [ ... ]                                                │
│   }                                                                      │
│        │                                                                 │
│        v                                                                 │
│   Broadcast to State Shards                                             │
│        │                                                                 │
│        v                                                                 │
│   State Shards process TpcResult:                                       │
│        │                                                                 │
│        ├──COMMIT──> Apply WriteSet from RwSet                           │
│        │            Clear lock                                           │
│        │                                                                 │
│        └──ABORT───> Discard changes                                     │
│                     Clear lock                                           │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 2PC Integration

### ReadSet Validation

State Shards validate that the values in ReadSet match the locked state:

```go
func (s *Server) validateReadSet(tx protocol.CrossShardTx) bool {
    for _, rw := range tx.RwSet {
        if rw.ReferenceBlock.ShardNum != s.shardID {
            continue
        }

        lock, ok := s.chain.GetSimulationLock(rw.Address)
        if !ok {
            // Address not locked - validation fails
            return false
        }

        for _, read := range rw.ReadSet {
            slot := common.Hash(read.Slot)
            expectedValue := common.BytesToHash(read.Value)
            actualValue := lock.Storage[slot]

            if expectedValue != actualValue {
                // State changed between lock and validation
                // This shouldn't happen since we hold the lock
                return false
            }
        }
    }
    return true
}
```

### WriteSet Application

On commit, State Shards apply the WriteSet:

```go
func (s *Server) applyWriteSet(tx protocol.CrossShardTx) {
    for _, rw := range tx.RwSet {
        if rw.ReferenceBlock.ShardNum != s.shardID {
            continue
        }

        // Apply each storage write
        for _, slot := range rw.WriteSet {
            // Get new value from simulation result
            // In full impl, WriteSet would include values
            // For PoC, we might need to re-execute or store values
        }
    }
}
```

---

## API Reference

### Orchestrator Endpoints

#### POST /cross-shard/call

Submit a cross-shard contract call for simulation and execution.

**Request:**
```json
{
    "from": "0x1111111111111111111111111111111111111111",
    "from_shard": 0,
    "to": "0x2222222222222222222222222222222222222222",
    "to_shard": 1,
    "value": "0",
    "data": "0xa9059cbb000000000000000000000000...",
    "gas": 100000
}
```

**Response:**
```json
{
    "tx_id": "550e8400-e29b-41d4-a716-446655440000",
    "sim_status": "sim_pending"
}
```

#### GET /cross-shard/simulation/{txid}

Get simulation status and result.

**Response (Pending):**
```json
{
    "tx_id": "550e8400-e29b-41d4-a716-446655440000",
    "sim_status": "sim_pending"
}
```

**Response (Success):**
```json
{
    "tx_id": "550e8400-e29b-41d4-a716-446655440000",
    "sim_status": "sim_success",
    "gas_used": 45000,
    "rw_set": [
        {
            "address": "0x2222...",
            "reference_block": {"shard_num": 1},
            "read_set": [{"slot": "0x00...", "value": "0x00...01"}],
            "write_set": ["0x00..."]
        }
    ]
}
```

**Response (Failed):**
```json
{
    "tx_id": "550e8400-e29b-41d4-a716-446655440000",
    "sim_status": "sim_failed",
    "error": "execution reverted"
}
```

### State Shard Endpoints

#### POST /state/lock

Lock an address and return full account state.

**Request:**
```json
{
    "tx_id": "550e8400-e29b-41d4-a716-446655440000",
    "address": "0x2222222222222222222222222222222222222222"
}
```

**Response:**
```json
{
    "success": true,
    "balance": "1000000000000000000",
    "nonce": 5,
    "code": "0x608060405234801561001057600080fd5b50...",
    "code_hash": "0xabc123...",
    "storage": {
        "0x0000000000000000000000000000000000000000000000000000000000000000": "0x0000000000000000000000000000000000000000000000000000000000000001"
    }
}
```

#### POST /state/unlock

Release a lock on simulation failure.

**Request:**
```json
{
    "tx_id": "550e8400-e29b-41d4-a716-446655440000",
    "address": "0x2222222222222222222222222222222222222222"
}
```

**Response:**
```json
{
    "success": true
}
```

---

## Error Handling

### Simulation Errors

| Error | Cause | Handling |
|-------|-------|----------|
| Lock Conflict | Address locked by another tx | Return error immediately, no cleanup needed |
| EVM Revert | Contract execution reverted | Unlock all, return SimFailed |
| Out of Gas | Insufficient gas for execution | Unlock all, return SimFailed |
| State Fetch Timeout | Network/shard unavailable | Unlock all, return SimFailed |

### 2PC Errors

| Error | Cause | Handling |
|-------|-------|----------|
| Missing Lock | Lock expired or never acquired | Vote NO |
| ReadSet Mismatch | State changed (shouldn't happen with locks) | Vote NO |
| Insufficient Balance | Balance changed | Vote NO |

---

## Known Limitations

### PoC Simplifications

1. **Address-Level Locking**: Entire address is locked, not individual storage slots. This reduces concurrency but simplifies implementation.

2. **No Merkle Proofs**: State is trusted from State Shards without cryptographic verification. In production, LockResponse would include Merkle proofs validated by the Orchestrator.

3. **No Lock Timeout**: Locks are held indefinitely until 2PC completes. In production, locks should timeout and auto-abort.

4. **Synchronous State Fetching**: Each address is fetched sequentially. Production could batch or parallelize.

5. **Full Storage Fetch**: Entire account storage is returned on lock. Production could fetch only accessed slots.

6. **No Deadlock Detection**: If two concurrent txs lock addresses in different orders, one will fail. Production needs deadlock detection or consistent lock ordering.

7. **Single Orchestrator**: No consensus on simulation results. Production needs Orchestrator consensus.

### Future Improvements

- Slot-level locking for higher concurrency
- Merkle proof validation for trustless verification
- Lock timeouts with automatic abort
- Batch state fetching
- Deadlock detection and prevention
- Orchestrator consensus for simulation results
