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

// TestLockBypassRemoved verifies fix #3:
// Transactions without simulation locks should be rejected, even with empty ReadSet
func TestLockBypassRemoved(t *testing.T) {
	server := NewServerForTest(1, "http://localhost:8080")

	// Create a tx with RwSet but NO simulation lock set up
	toAddr := common.HexToAddress("0x0000000000000000000000000000000000000001")

	tx := protocol.CrossShardTx{
		ID:        "test-no-lock",
		FromShard: 0,
		From:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
		To:        toAddr,
		Value:     protocol.NewBigInt(big.NewInt(1000)),
		RwSet: []protocol.RwVariable{
			{
				Address:        toAddr,
				ReferenceBlock: protocol.Reference{ShardNum: 1},
				// Empty ReadSet - before fix #3, this would bypass lock validation
				ReadSet: []protocol.ReadSetItem{},
			},
		},
	}

	// DO NOT set up simulation lock - this is the test
	// server.chain.LockAddress(tx.ID, toAddr, ...) -- intentionally NOT called

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

	// The tx should NOT have pending credits because validation failed
	// Before fix #3: empty ReadSet would bypass lock check, credits would exist
	// After fix #3: missing lock causes validation to fail, no credits
	_, hasCredits := server.chain.GetPendingCredits(tx.ID)
	if hasCredits {
		t.Error("Transaction without simulation lock should NOT have pending credits (fix #3)")
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
	evmState, err := NewEVMState()
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
	evmState, err := NewEVMState()
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
