package shard

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// TestLockBypassRemoved verifies V2 protocol:
// In V2, locks are acquired during CtToOrder processing, not pre-existing.
// Pending credits are set during CtToOrder, validated during block production.
// This test verifies V2's security model where validation happens via Lock tx.
func TestLockBypassRemoved(t *testing.T) {
	server := NewServerForTest(1, "http://localhost:8080")

	// Create a tx with RwSet - in V2, locks are acquired during CtToOrder processing
	toAddr := common.HexToAddress("0x0000000000000000000000000000000000000001")

	tx := protocol.CrossShardTx{
		ID:        "test-v2-model",
		FromShard: 0,
		From:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
		To:        toAddr,
		Value:     protocol.NewBigInt(big.NewInt(1000)),
		RwSet: []protocol.RwVariable{
			{
				Address:        toAddr,
				ReferenceBlock: protocol.Reference{ShardNum: 1},
				// Empty ReadSet - validation happens during block production
				ReadSet: []protocol.ReadSetItem{},
			},
		},
	}

	// In V2, we do NOT pre-set simulation locks.
	// Locks are acquired during CtToOrder processing.

	// Create orchestrator block with this tx
	block := protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{tx},
		TpcResult: make(map[string]bool),
	}

	// Send the block
	blockData, _ := json.Marshal(block)
	req := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// V2 Model: Pending credits ARE set during CtToOrder processing.
	// Validation happens during block production via Lock tx.
	_, hasCredits := server.chain.GetPendingCredits(tx.ID)
	if !hasCredits {
		t.Error("V2: Pending credits should be set during CtToOrder (validation happens during block production)")
	}

	// V2: Address should be locked during CtToOrder processing
	if !server.chain.IsAddressLocked(toAddr) {
		t.Error("V2: Address should be locked during CtToOrder processing")
	}

	// V2: Produce a block and verify Lock tx is executed
	block2, _ := server.chain.ProduceBlock(server.evmState)
	hasLockTx := false
	for _, executedTx := range block2.TxOrdering {
		if executedTx.TxType == protocol.TxTypeLock && executedTx.CrossShardTxID == tx.ID {
			hasLockTx = true
			break
		}
	}
	if !hasLockTx {
		t.Error("V2: Lock transaction should be executed in block")
	}
}

// TestLockBypassRemoved_WithLock verifies that transactions WITH locks still work
func TestLockBypassRemoved_WithLock(t *testing.T) {
	server := NewServerForTest(1, "http://localhost:8080")

	toAddr := common.HexToAddress("0x0000000000000000000000000000000000000001")

	tx := protocol.CrossShardTx{
		ID:        "test-with-lock",
		FromShard: 0,
		From:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
		To:        toAddr,
		Value:     protocol.NewBigInt(big.NewInt(1000)),
		RwSet: []protocol.RwVariable{
			{
				Address:        toAddr,
				ReferenceBlock: protocol.Reference{ShardNum: 1},
			},
		},
	}

	// Set up simulation lock (this is required after fix #3)
	server.chain.LockAddress(tx.ID, toAddr, big.NewInt(0), 0, nil, common.Hash{}, nil)

	// Create orchestrator block
	block := protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{tx},
		TpcResult: make(map[string]bool),
	}

	blockData, _ := json.Marshal(block)
	req := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// With proper lock, pending credits should exist
	_, hasCredits := server.chain.GetPendingCredits(tx.ID)
	if !hasCredits {
		t.Error("Transaction WITH simulation lock should have pending credits")
	}
}

// TestAtomicBalanceCheck verifies fix #7:
// Concurrent prepare requests should not cause race condition in balance check
func TestAtomicBalanceCheck(t *testing.T) {
	server := NewServerForTest(0, "http://localhost:8080")

	// Fund account with exactly 1000
	sender := common.HexToAddress("0x0000000000000000000000000000000000000000")
	server.evmState.Credit(sender, big.NewInt(1000))

	// Create 10 concurrent transactions each trying to lock 200
	// Only 5 should succeed (1000 / 200 = 5)
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			tx := protocol.CrossShardTx{
				ID:        "concurrent-tx-" + string(rune('A'+id)),
				FromShard: 0,
				From:      sender,
				Value:     protocol.NewBigInt(big.NewInt(200)),
				RwSet: []protocol.RwVariable{
					{
						Address:        common.HexToAddress("0x0000000000000000000000000000000000000001"),
						ReferenceBlock: protocol.Reference{ShardNum: 1},
					},
				},
			}

			block := protocol.OrchestratorShardBlock{
				Height:    uint64(id + 1),
				CtToOrder: []protocol.CrossShardTx{tx},
				TpcResult: make(map[string]bool),
			}

			blockData, _ := json.Marshal(block)
			req := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			server.Router().ServeHTTP(w, req)

			// Check if lock was acquired
			if _, ok := server.chain.GetLockedFunds(tx.ID); ok {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	// Should have exactly 5 successful locks (1000 / 200)
	// Before fix #7: race condition could allow more than 5
	// After fix #7: atomic check-and-lock prevents over-locking
	if successCount > 5 {
		t.Errorf("Expected at most 5 successful locks (balance=1000, each=200), got %d (race condition!)", successCount)
	}

	// Verify total locked doesn't exceed balance
	totalLocked := server.chain.GetLockedAmountForAddress(sender)
	if totalLocked.Cmp(big.NewInt(1000)) > 0 {
		t.Errorf("Total locked %s exceeds balance 1000 (race condition!)", totalLocked.String())
	}
}

// TestTrackingStateDB_ThreadSafety verifies fix #8:
// Concurrent access to TrackingStateDB's tracking maps should not cause race conditions
// Note: The underlying geth StateDB is not thread-safe, but our tracking maps are protected
func TestTrackingStateDB_ThreadSafety(t *testing.T) {
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Create some accounts (sequentially - StateDB not thread-safe)
	for i := 0; i < 10; i++ {
		addr := common.BigToAddress(big.NewInt(int64(i)))
		evmState.Credit(addr, big.NewInt(1000000))
	}

	trackingDB := NewTrackingStateDB(evmState.stateDB, 0, 6)

	// First, do some sequential writes to populate the tracking maps
	for i := 0; i < 10; i++ {
		addr := common.BigToAddress(big.NewInt(int64(i)))
		slot := common.BigToHash(big.NewInt(int64(i)))
		value := common.BigToHash(big.NewInt(int64(i * 100)))
		trackingDB.SetState(addr, slot, value)
	}

	var wg sync.WaitGroup

	// Now test concurrent READ access to our tracking maps
	// This is what fix #8 protects - concurrent map reads
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = trackingDB.GetAccessedAddresses()
		}()
		go func() {
			defer wg.Done()
			_ = trackingDB.HasCrossShardAccess()
		}()
		go func() {
			defer wg.Done()
			_ = trackingDB.GetStorageWrites()
		}()
	}

	wg.Wait()

	// Test passes if no race condition panic occurs
	// Before fix #8, concurrent map access would cause "concurrent map read and write"

	// Verify tracking worked correctly
	accessed := trackingDB.GetAccessedAddresses()
	if len(accessed) == 0 {
		t.Error("Expected some accessed addresses to be tracked")
	}

	storageWrites := trackingDB.GetStorageWrites()
	if len(storageWrites) == 0 {
		t.Error("Expected some storage writes to be tracked")
	}
}

// TestTrackingStateDB_StorageWriteTracking verifies storage writes are correctly tracked
func TestTrackingStateDB_StorageWriteTracking(t *testing.T) {
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	contractAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	evmState.Credit(contractAddr, big.NewInt(1000000))

	trackingDB := NewTrackingStateDB(evmState.stateDB, 0, 6)

	// Write to multiple slots
	slot1 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	slot2 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000002")
	value1 := common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000000ff")
	value2 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000100")

	trackingDB.SetState(contractAddr, slot1, value1)
	trackingDB.SetState(contractAddr, slot2, value2)

	// Verify writes are tracked
	writes := trackingDB.GetStorageWritesForAddress(contractAddr)
	if writes == nil {
		t.Fatal("Expected storage writes to be tracked")
	}

	if writes[slot1] != value1 {
		t.Errorf("Expected slot1 value %s, got %s", value1.Hex(), writes[slot1].Hex())
	}

	if writes[slot2] != value2 {
		t.Errorf("Expected slot2 value %s, got %s", value2.Hex(), writes[slot2].Hex())
	}
}

// TestAtomicBalanceCheck_ExactBalance verifies locking exactly the available balance
func TestAtomicBalanceCheck_ExactBalance(t *testing.T) {
	server := NewServerForTest(0, "http://localhost:8080")

	sender := common.HexToAddress("0x0000000000000000000000000000000000000000")
	server.evmState.Credit(sender, big.NewInt(1000))

	// Try to lock exactly the balance
	tx := protocol.CrossShardTx{
		ID:        "exact-balance-tx",
		FromShard: 0,
		From:      sender,
		Value:     protocol.NewBigInt(big.NewInt(1000)),
		RwSet: []protocol.RwVariable{
			{
				Address:        common.HexToAddress("0x0000000000000000000000000000000000000001"),
				ReferenceBlock: protocol.Reference{ShardNum: 1},
			},
		},
	}

	block := protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{tx},
		TpcResult: make(map[string]bool),
	}

	blockData, _ := json.Marshal(block)
	req := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	// Should succeed - exact balance
	if _, ok := server.chain.GetLockedFunds(tx.ID); !ok {
		t.Error("Expected lock to succeed with exact balance")
	}
}

// TestAtomicBalanceCheck_InsufficientBalance verifies rejection when balance is insufficient
func TestAtomicBalanceCheck_InsufficientBalance(t *testing.T) {
	server := NewServerForTest(0, "http://localhost:8080")

	sender := common.HexToAddress("0x0000000000000000000000000000000000000000")
	server.evmState.Credit(sender, big.NewInt(500))

	// Try to lock more than balance
	tx := protocol.CrossShardTx{
		ID:        "insufficient-balance-tx",
		FromShard: 0,
		From:      sender,
		Value:     protocol.NewBigInt(big.NewInt(1000)),
		RwSet: []protocol.RwVariable{
			{
				Address:        common.HexToAddress("0x0000000000000000000000000000000000000001"),
				ReferenceBlock: protocol.Reference{ShardNum: 1},
			},
		},
	}

	block := protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{tx},
		TpcResult: make(map[string]bool),
	}

	blockData, _ := json.Marshal(block)
	req := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	// Should fail - insufficient balance
	if _, ok := server.chain.GetLockedFunds(tx.ID); ok {
		t.Error("Expected lock to fail with insufficient balance")
	}
}

// TestLockBypassRemoved_MultipleRwVariables verifies V2 locks all RwSet addresses
func TestLockBypassRemoved_MultipleRwVariables(t *testing.T) {
	server := NewServerForTest(1, "http://localhost:8080")

	addr1 := common.HexToAddress("0x0000000000000000000000000000000000000001")
	addr2 := common.HexToAddress("0x0000000000000000000000000000000000000002")

	// Create tx with multiple RwSet entries
	tx := protocol.CrossShardTx{
		ID:        "multi-rw-test",
		FromShard: 0,
		From:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
		To:        addr1,
		Value:     protocol.NewBigInt(big.NewInt(1000)),
		RwSet: []protocol.RwVariable{
			{
				Address:        addr1,
				ReferenceBlock: protocol.Reference{ShardNum: 1},
			},
			{
				Address:        addr2,
				ReferenceBlock: protocol.Reference{ShardNum: 1},
			},
		},
	}

	// V2: Locks are acquired during CtToOrder processing, not pre-set
	// Pre-locking one address should be a no-op when block is processed
	server.chain.LockAddress(tx.ID, addr1, big.NewInt(0), 0, nil, common.Hash{}, nil)

	block := protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{tx},
		TpcResult: make(map[string]bool),
	}

	blockData, _ := json.Marshal(block)
	req := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	// V2: ALL addresses are locked during CtToOrder processing
	if !server.chain.IsAddressLocked(addr1) {
		t.Error("V2: addr1 should be locked after CtToOrder processing")
	}
	if !server.chain.IsAddressLocked(addr2) {
		t.Error("V2: addr2 should be locked after CtToOrder processing")
	}

	// V2: Produce a block to verify Lock tx with both RwSet entries
	block2, _ := server.chain.ProduceBlock(server.evmState)
	var lockTx *protocol.Transaction
	for i := range block2.TxOrdering {
		if block2.TxOrdering[i].TxType == protocol.TxTypeLock && block2.TxOrdering[i].CrossShardTxID == tx.ID {
			lockTx = &block2.TxOrdering[i]
			break
		}
	}
	if lockTx == nil {
		t.Fatal("V2: Lock transaction should be executed")
	}
	if len(lockTx.RwSet) != 2 {
		t.Errorf("V2: Lock tx should have 2 RwSet entries, got %d", len(lockTx.RwSet))
	}
}

// TestChain_SimulationLockLifecycle verifies simulation lock acquire/release
func TestChain_SimulationLockLifecycle(t *testing.T) {
	chain := NewChain(0)

	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	txID := "test-lock-lifecycle"

	// Initially unlocked
	if chain.IsAddressLocked(addr) {
		t.Error("Address should not be locked initially")
	}

	// Lock address
	err := chain.LockAddress(txID, addr, big.NewInt(1000), 0, nil, common.Hash{}, nil)
	if err != nil {
		t.Fatalf("Failed to lock address: %v", err)
	}

	// Should be locked now
	if !chain.IsAddressLocked(addr) {
		t.Error("Address should be locked after LockAddress")
	}

	// Get lock info
	lock, ok := chain.GetSimulationLockByAddr(addr)
	if !ok {
		t.Error("Should be able to get simulation lock")
	}
	if lock.TxID != txID {
		t.Errorf("Expected txID %s, got %s", txID, lock.TxID)
	}

	// Unlock
	chain.UnlockAddress(txID, addr)

	// Should be unlocked
	if chain.IsAddressLocked(addr) {
		t.Error("Address should be unlocked after UnlockAddress")
	}
}

// TestChain_SimulationLockConflict verifies locks conflict correctly
func TestChain_SimulationLockConflict(t *testing.T) {
	chain := NewChain(0)

	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")

	// First tx locks the address
	err := chain.LockAddress("tx-1", addr, big.NewInt(1000), 0, nil, common.Hash{}, nil)
	if err != nil {
		t.Fatalf("First lock should succeed: %v", err)
	}

	// Second tx tries to lock same address - should fail
	err = chain.LockAddress("tx-2", addr, big.NewInt(500), 0, nil, common.Hash{}, nil)
	if err == nil {
		t.Error("Second lock on same address should fail")
	}

	// Verify error type
	if _, ok := err.(*AddressLockedError); !ok {
		t.Errorf("Expected AddressLockedError, got %T", err)
	}

	// Same tx can "lock" again (no-op)
	err = chain.LockAddress("tx-1", addr, big.NewInt(1000), 0, nil, common.Hash{}, nil)
	if err != nil {
		t.Errorf("Same tx locking again should succeed (no-op): %v", err)
	}
}

// TestEVMState_AtomicCheckAndLock verifies the atomic check-and-lock pattern works
// Note: The mutex in EVMState protects the CanDebit+LockFunds atomic pattern,
// NOT arbitrary concurrent StateDB access (geth's StateDB isn't thread-safe).
func TestEVMState_AtomicCheckAndLock(t *testing.T) {
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	addr := common.HexToAddress("0x0000000000000000000000000000000000000000")
	evmState.Credit(addr, big.NewInt(1000))

	chain := NewChain(0)

	// Test the atomic check-and-lock pattern
	// This is what we protect with the mutex in server.go
	evmState.mu.Lock()
	lockedAmount := chain.GetLockedAmountForAddress(addr)
	canDebit := evmState.CanDebit(addr, big.NewInt(500), lockedAmount)
	if canDebit {
		chain.LockFunds("tx-1", addr, big.NewInt(500))
	}
	evmState.mu.Unlock()

	if !canDebit {
		t.Error("First lock should succeed")
	}

	// Second atomic check-and-lock
	evmState.mu.Lock()
	lockedAmount = chain.GetLockedAmountForAddress(addr)
	canDebit = evmState.CanDebit(addr, big.NewInt(500), lockedAmount)
	if canDebit {
		chain.LockFunds("tx-2", addr, big.NewInt(500))
	}
	evmState.mu.Unlock()

	if !canDebit {
		t.Error("Second lock should succeed (exactly uses remaining balance)")
	}

	// Third attempt should fail
	evmState.mu.Lock()
	lockedAmount = chain.GetLockedAmountForAddress(addr)
	canDebit = evmState.CanDebit(addr, big.NewInt(100), lockedAmount)
	evmState.mu.Unlock()

	if canDebit {
		t.Error("Third lock should fail (insufficient balance)")
	}

	// Verify total locked = 1000
	totalLocked := chain.GetLockedAmountForAddress(addr)
	if totalLocked.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Expected total locked 1000, got %s", totalLocked.String())
	}
}
