package shard

import (
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// ===== V2 Test Coverage Gaps =====
// These tests cover functions that had 0% or low coverage in the original test suite.

// TestClearPendingCall verifies ClearPendingCall function
func TestClearPendingCall(t *testing.T) {
	chain := NewChain(0)

	txID := "ctx-1"
	chain.StorePendingCall(&protocol.CrossShardTx{
		ID:   txID,
		From: common.HexToAddress("0x1234"),
		To:   common.HexToAddress("0x5678"),
	})

	// Verify call exists
	if _, ok := chain.GetPendingCall(txID); !ok {
		t.Fatal("Pending call should exist")
	}

	// Clear and verify
	chain.ClearPendingCall(txID)

	if _, ok := chain.GetPendingCall(txID); ok {
		t.Error("Pending call should be cleared")
	}
}

// TestClearPendingCall_NonExistent verifies clearing non-existent call is safe
func TestClearPendingCall_NonExistent(t *testing.T) {
	chain := NewChain(0)

	// Should not panic when clearing non-existent call
	chain.ClearPendingCall("non-existent-tx")

	// Should still return not found
	if _, ok := chain.GetPendingCall("non-existent-tx"); ok {
		t.Error("Non-existent call should return false")
	}
}

// TestSlotLockError_ErrorMethod verifies SlotLockError.Error() format
func TestSlotLockError_ErrorMethod(t *testing.T) {
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	slot := common.HexToHash("0x01")
	txID := "holding-tx"

	err := &SlotLockError{
		Address:  addr,
		Slot:     slot,
		LockedBy: txID,
	}

	errMsg := err.Error()

	// Verify error message contains key information
	if errMsg == "" {
		t.Error("Error message should not be empty")
	}

	// The error message should contain the address, slot, and holder info
	if len(errMsg) < 10 {
		t.Errorf("Error message seems incomplete: %s", errMsg)
	}
}

// TestClearPendingRwSet verifies ClearPendingRwSet function
func TestClearPendingRwSet(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")
	txID := "ctx-1"

	// Create RwSet and validate+lock it
	rwSet := []protocol.RwVariable{{
		Address: addr,
		ReadSet: []protocol.ReadSetItem{
			{Slot: protocol.Slot(slot), Value: common.Hash{}.Bytes()},
		},
	}}

	err = chain.ValidateAndLockReadSet(txID, rwSet, evmState)
	if err != nil {
		t.Fatalf("ValidateAndLockReadSet failed: %v", err)
	}

	// Verify RwSet exists
	if _, ok := chain.GetPendingRwSet(txID); !ok {
		t.Fatal("Pending RwSet should exist")
	}

	// Clear and verify
	chain.ClearPendingRwSet(txID)

	if _, ok := chain.GetPendingRwSet(txID); ok {
		t.Error("Pending RwSet should be cleared")
	}
}

// TestGetPendingRwSet_NonExistent verifies GetPendingRwSet for non-existent tx
func TestGetPendingRwSet_NonExistent(t *testing.T) {
	chain := NewChain(0)

	rwSet, ok := chain.GetPendingRwSet("non-existent-tx")
	if ok {
		t.Error("Non-existent RwSet should return false")
	}
	if rwSet != nil {
		t.Error("Non-existent RwSet should return nil")
	}
}

// TestGetSlotLockHolder_NoLocks verifies GetSlotLockHolder with no locks
func TestGetSlotLockHolder_NoLocks(t *testing.T) {
	chain := NewChain(0)

	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")

	holder := chain.GetSlotLockHolder(addr, slot)
	if holder != "" {
		t.Errorf("Expected empty holder for unlocked slot, got %s", holder)
	}
}

// TestGetSlotLockHolder_AfterUnlock verifies holder is empty after unlock
func TestGetSlotLockHolder_AfterUnlock(t *testing.T) {
	chain := NewChain(0)

	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")
	txID := "tx-1"

	// Lock
	err := chain.LockSlot(txID, addr, slot)
	if err != nil {
		t.Fatalf("LockSlot failed: %v", err)
	}

	// Verify locked
	if holder := chain.GetSlotLockHolder(addr, slot); holder != txID {
		t.Errorf("Expected holder %s, got %s", txID, holder)
	}

	// Unlock
	chain.UnlockSlot(txID, addr, slot)

	// Verify unlocked
	if holder := chain.GetSlotLockHolder(addr, slot); holder != "" {
		t.Errorf("Expected empty holder after unlock, got %s", holder)
	}
}

// TestUnlockSlot_WrongTransaction verifies unlocking with wrong tx is no-op
func TestUnlockSlot_WrongTransaction(t *testing.T) {
	chain := NewChain(0)

	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")

	// Lock with tx-1
	err := chain.LockSlot("tx-1", addr, slot)
	if err != nil {
		t.Fatalf("LockSlot failed: %v", err)
	}

	// Try to unlock with tx-2 (should be no-op)
	chain.UnlockSlot("tx-2", addr, slot)

	// Should still be locked by tx-1
	if holder := chain.GetSlotLockHolder(addr, slot); holder != "tx-1" {
		t.Errorf("Slot should still be locked by tx-1, got %s", holder)
	}
}

// TestValidateAndLockReadSet_RollbackOnSecondMismatch verifies partial lock rollback
func TestValidateAndLockReadSet_RollbackOnSecondMismatch(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	addr := common.HexToAddress("0x1234")
	slot1 := common.HexToHash("0x01")
	slot2 := common.HexToHash("0x02")

	// Set state: slot1 = 0x10, slot2 = 0x20
	evmState.SetStorageAt(addr, slot1, common.HexToHash("0x10"))
	evmState.SetStorageAt(addr, slot2, common.HexToHash("0x20"))

	// Create RwSet: slot1 matches (0x10), slot2 mismatches (expect 0x99)
	rwSet := []protocol.RwVariable{{
		Address: addr,
		ReadSet: []protocol.ReadSetItem{
			{Slot: protocol.Slot(slot1), Value: common.HexToHash("0x10").Bytes()}, // Match
			{Slot: protocol.Slot(slot2), Value: common.HexToHash("0x99").Bytes()}, // Mismatch!
		},
	}}

	// Validation should fail
	err = chain.ValidateAndLockReadSet("tx-1", rwSet, evmState)
	if err == nil {
		t.Fatal("Expected validation to fail due to mismatch")
	}

	// Both slots should be unlocked (rollback)
	if chain.IsSlotLocked(addr, slot1) {
		t.Error("slot1 should be unlocked after rollback")
	}
	if chain.IsSlotLocked(addr, slot2) {
		t.Error("slot2 should be unlocked after rollback")
	}
}

// TestValidateAndLockReadSet_SlotCollision verifies validation fails on slot collision
func TestValidateAndLockReadSet_SlotCollision(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")

	// Pre-lock the slot with another transaction
	err = chain.LockSlot("tx-existing", addr, slot)
	if err != nil {
		t.Fatalf("LockSlot failed: %v", err)
	}

	// Try to validate+lock with new transaction
	rwSet := []protocol.RwVariable{{
		Address: addr,
		ReadSet: []protocol.ReadSetItem{
			{Slot: protocol.Slot(slot), Value: common.Hash{}.Bytes()},
		},
	}}

	err = chain.ValidateAndLockReadSet("tx-new", rwSet, evmState)
	if err == nil {
		t.Fatal("Expected validation to fail due to slot collision")
	}

	// Should be SlotLockError
	slotErr, ok := err.(*SlotLockError)
	if !ok {
		t.Errorf("Expected SlotLockError, got %T: %v", err, err)
	} else if slotErr.LockedBy != "tx-existing" {
		t.Errorf("Expected LockedBy tx-existing, got %s", slotErr.LockedBy)
	}
}

// TestApplyWriteSet_NonExistentTx verifies ApplyWriteSet for non-existent tx
func TestApplyWriteSet_NonExistentTx(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Apply WriteSet for non-existent transaction
	err = chain.ApplyWriteSet("non-existent-tx", evmState)
	if err == nil {
		t.Error("Expected error for non-existent tx")
	}
}

// TestClearLock_OnlyClearsLock verifies ClearLock only clears the lock,
// not pending credits (those are cleared separately via TxTypeCrossCredit execution)
func TestClearLock_OnlyClearsLock(t *testing.T) {
	chain := NewChain(0)

	txID := "tx-1"
	addr := common.HexToAddress("0x1234")

	// Set up lock and pending credit for same address
	chain.LockFunds(txID, addr, big.NewInt(100))
	chain.StorePendingCredit(txID, addr, big.NewInt(50))

	// Clear lock - should NOT clear pending credits
	// Pending credits are cleared separately via TxTypeCrossCredit execution
	chain.ClearLock(txID)

	// Lock should be cleared
	if _, ok := chain.GetLockedFunds(txID); ok {
		t.Error("Lock should be cleared")
	}

	// Pending credits should STILL exist (not cleared by ClearLock)
	credits, ok := chain.GetPendingCredits(txID)
	if !ok {
		t.Error("Pending credits should still exist after ClearLock")
	}
	if len(credits) != 1 || credits[0].Address != addr {
		t.Error("Pending credit should be preserved")
	}
}

// TestRecordPrepareTx_NilValue verifies RecordPrepareTx handles nil Value
func TestRecordPrepareTx_NilValue(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Record prepare tx with nil value
	chain.RecordPrepareTx(protocol.Transaction{
		TxType:         protocol.TxTypePrepareDebit,
		CrossShardTxID: "tx-nil-value",
		From:           common.HexToAddress("0x1234"),
		Value:          nil, // Nil value
	})

	// Should not panic
	block, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	if len(block.PrepareTxs) != 1 {
		t.Errorf("Expected 1 prepare tx, got %d", len(block.PrepareTxs))
	}
}

// TestClearPendingCreditForAddress_ViaCrossCredit verifies that executing
// TxTypeCrossCredit clears only the specific pending credit for that address.
// This tests the internal clearPendingCreditForAddressLocked function via ProduceBlock.
func TestClearPendingCreditForAddress_ViaCrossCredit(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVMState: %v", err)
	}

	txID := "tx-1"
	addr1 := common.HexToAddress("0x1111")
	addr2 := common.HexToAddress("0x2222")

	// Store credits for two addresses under same tx
	chain.StorePendingCredit(txID, addr1, big.NewInt(100))
	chain.StorePendingCredit(txID, addr2, big.NewInt(200))

	// Verify both exist
	credits, ok := chain.GetPendingCredits(txID)
	if !ok || len(credits) != 2 {
		t.Fatalf("Expected 2 credits, got %d", len(credits))
	}

	// Add a CrossCredit transaction targeting addr1
	// This will call clearPendingCreditForAddressLocked internally during ProduceBlock
	creditTx := protocol.Transaction{
		TxType:         protocol.TxTypeCrossCredit,
		CrossShardTxID: txID,
		To:             addr1,
		Value:          protocol.NewBigInt(big.NewInt(100)),
	}
	chain.AddTx(creditTx)

	// Produce block to execute the transaction
	_, err = chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	// Verify addr2's credit still exists (addr1's was cleared)
	credits, ok = chain.GetPendingCredits(txID)
	if !ok {
		t.Fatal("Credits should still exist for addr2")
	}
	if len(credits) != 1 {
		t.Errorf("Expected 1 credit remaining, got %d", len(credits))
	}
	if credits[0].Address != addr2 {
		t.Errorf("Expected addr2's credit to remain, got %s", credits[0].Address.Hex())
	}
}

// TestMultipleLockTypes_SameTransaction verifies funds lock + slot lock coexist
func TestMultipleLockTypes_SameTransaction(t *testing.T) {
	chain := NewChain(0)

	txID := "ctx-1"
	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")

	// Lock funds (for balance)
	chain.LockFunds(txID, addr, big.NewInt(100))

	// Lock slot (for storage)
	err := chain.LockSlot(txID, addr, slot)
	if err != nil {
		t.Fatalf("LockSlot failed: %v", err)
	}

	// Both should be locked
	if _, ok := chain.GetLockedFunds(txID); !ok {
		t.Error("Funds should be locked")
	}
	if !chain.IsSlotLocked(addr, slot) {
		t.Error("Slot should be locked")
	}

	// Unlock all slots for tx
	chain.UnlockAllSlotsForTx(txID)

	// Slot unlocked, but funds still locked
	if chain.IsSlotLocked(addr, slot) {
		t.Error("Slot should be unlocked")
	}
	if _, ok := chain.GetLockedFunds(txID); !ok {
		t.Error("Funds should still be locked (different lock type)")
	}
}

// TestProduceBlock_SimErrorTxType verifies SimError transaction handling
func TestProduceBlock_SimErrorTxType(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Add SimError transaction
	chain.AddTx(protocol.Transaction{
		ID:             "simerror-1",
		TxType:         protocol.TxTypeSimError,
		CrossShardTxID: "ctx-failed",
		IsCrossShard:   true,
		Error:          "simulation failed: out of gas",
	})

	block, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	// SimError tx should be in block
	if len(block.TxOrdering) != 1 {
		t.Fatalf("Expected 1 tx, got %d", len(block.TxOrdering))
	}
	if block.TxOrdering[0].TxType != protocol.TxTypeSimError {
		t.Errorf("Expected SimError type, got %s", block.TxOrdering[0].TxType)
	}
}

// TestProduceBlock_EmptyBlock verifies empty block production
func TestProduceBlock_EmptyBlock(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Don't add any transactions
	block, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	// Block should still be produced (empty block is valid)
	if block.Height != 1 {
		t.Errorf("Expected height 1, got %d", block.Height)
	}
	if len(block.TxOrdering) != 0 {
		t.Errorf("Expected 0 txs, got %d", len(block.TxOrdering))
	}
	if len(block.TpcPrepare) != 0 {
		t.Errorf("Expected 0 prepares, got %d", len(block.TpcPrepare))
	}
}

// TestReadSetMismatchError_Format verifies ReadSetMismatchError format
func TestReadSetMismatchError_Format(t *testing.T) {
	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")
	expected := []byte{0xaa, 0xbb, 0xcc}
	actual := common.HexToHash("0xbb")

	err := &ReadSetMismatchError{
		Address:  addr,
		Slot:     slot,
		Expected: expected,
		Actual:   actual,
	}

	errMsg := err.Error()
	if errMsg == "" {
		t.Error("Error message should not be empty")
	}

	// Verify it contains useful information
	if len(errMsg) < 20 {
		t.Errorf("Error message seems incomplete: %s", errMsg)
	}
}

// TestChain_UnlockTx_ClearsAllRelatedState verifies Unlock clears everything
func TestChain_UnlockTx_ClearsAllRelatedState(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	txID := "ctx-complete"
	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")

	// Set up all types of state for the transaction
	chain.LockFunds(txID, addr, big.NewInt(100))
	chain.StorePendingCredit(txID, addr, big.NewInt(50))
	chain.StorePendingCall(&protocol.CrossShardTx{ID: txID})
	chain.LockSlot(txID, addr, slot)

	// Store pending RwSet
	chain.mu.Lock()
	chain.pendingRwSets[txID] = []protocol.RwVariable{{Address: addr}}
	chain.mu.Unlock()

	// Add unlock transaction
	chain.AddTx(protocol.Transaction{
		ID:             "unlock-1",
		TxType:         protocol.TxTypeUnlock,
		CrossShardTxID: txID,
		IsCrossShard:   true,
	})

	// Produce block (executes unlock)
	_, err = chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	// Verify ALL state is cleared
	if _, ok := chain.GetLockedFunds(txID); ok {
		t.Error("Locked funds should be cleared")
	}
	if _, ok := chain.GetPendingCredits(txID); ok {
		t.Error("Pending credits should be cleared")
	}
	if _, ok := chain.GetPendingCall(txID); ok {
		t.Error("Pending call should be cleared")
	}
	if chain.IsSlotLocked(addr, slot) {
		t.Error("Slot lock should be cleared")
	}
	if _, ok := chain.GetPendingRwSet(txID); ok {
		t.Error("Pending RwSet should be cleared")
	}
}

// ===== Crash Recovery Tests =====

// TestOrchestratorHeightTracking verifies lastOrchestratorHeight tracking
func TestOrchestratorHeightTracking(t *testing.T) {
	chain := NewChain(0)

	// Initial height should be 0
	if height := chain.GetLastOrchestratorHeight(); height != 0 {
		t.Errorf("Expected initial height 0, got %d", height)
	}

	// Set and get height
	chain.SetLastOrchestratorHeight(5)
	if height := chain.GetLastOrchestratorHeight(); height != 5 {
		t.Errorf("Expected height 5, got %d", height)
	}

	// Update height
	chain.SetLastOrchestratorHeight(10)
	if height := chain.GetLastOrchestratorHeight(); height != 10 {
		t.Errorf("Expected height 10, got %d", height)
	}
}

// TestCommitProcessedIdempotency verifies idempotency tracking for commits
func TestCommitProcessedIdempotency(t *testing.T) {
	chain := NewChain(0)

	txID := "ctx-idem-1"

	// Should not be processed initially
	if chain.IsCommitProcessed(txID) {
		t.Error("Transaction should not be marked as processed initially")
	}

	// Mark as processed
	chain.MarkCommitProcessed(txID)

	// Should now be processed
	if !chain.IsCommitProcessed(txID) {
		t.Error("Transaction should be marked as processed after MarkCommitProcessed")
	}

	// Multiple marks should be safe
	chain.MarkCommitProcessed(txID)
	if !chain.IsCommitProcessed(txID) {
		t.Error("Transaction should remain marked as processed")
	}
}

// TestMultipleCommitsIdempotency verifies independent tracking of multiple txs
func TestMultipleCommitsIdempotency(t *testing.T) {
	chain := NewChain(0)

	tx1 := "ctx-1"
	tx2 := "ctx-2"
	tx3 := "ctx-3"

	// Mark only tx1 and tx3
	chain.MarkCommitProcessed(tx1)
	chain.MarkCommitProcessed(tx3)

	// Check individual states
	if !chain.IsCommitProcessed(tx1) {
		t.Error("tx1 should be processed")
	}
	if chain.IsCommitProcessed(tx2) {
		t.Error("tx2 should NOT be processed")
	}
	if !chain.IsCommitProcessed(tx3) {
		t.Error("tx3 should be processed")
	}
}

// TestHeightTrackingIndependentOfBlockProduction verifies orchestrator height
// is tracked independently of local block production
func TestHeightTrackingIndependentOfBlockProduction(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Set orchestrator height
	chain.SetLastOrchestratorHeight(100)

	// Produce a local block
	block, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	// Local block height is 1, orchestrator height remains 100
	if block.Height != 1 {
		t.Errorf("Expected local block height 1, got %d", block.Height)
	}
	if height := chain.GetLastOrchestratorHeight(); height != 100 {
		t.Errorf("Expected orchestrator height 100, got %d", height)
	}
}

// ===== CheckAndMarkCommitProcessed Tests =====

// TestCheckAndMarkCommitProcessed_Basic verifies the atomic check-and-mark operation
func TestCheckAndMarkCommitProcessed_Basic(t *testing.T) {
	chain := NewChain(0)

	// First call should return true (newly marked)
	if !chain.CheckAndMarkCommitProcessed("tx-1") {
		t.Error("First call should return true (newly marked)")
	}

	// Second call should return false (already processed)
	if chain.CheckAndMarkCommitProcessed("tx-1") {
		t.Error("Second call should return false (already processed)")
	}

	// Third call should still return false
	if chain.CheckAndMarkCommitProcessed("tx-1") {
		t.Error("Third call should return false (already processed)")
	}

	// Different tx should return true
	if !chain.CheckAndMarkCommitProcessed("tx-2") {
		t.Error("Different tx should return true (newly marked)")
	}
}

// TestCheckAndMarkCommitProcessed_Concurrent verifies atomicity under concurrent access.
// This tests the TOCTOU fix - only one goroutine should succeed in marking a tx.
func TestCheckAndMarkCommitProcessed_Concurrent(t *testing.T) {
	chain := NewChain(0)
	const goroutines = 100
	var wg sync.WaitGroup
	successCount := int32(0)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if chain.CheckAndMarkCommitProcessed("race-tx") {
				atomic.AddInt32(&successCount, 1)
			}
		}()
	}

	wg.Wait()

	// Exactly one goroutine should succeed
	if successCount != 1 {
		t.Errorf("TOCTOU race detected: Expected exactly 1 success, got %d", successCount)
	}
}

// TestCheckAndMarkCommitProcessed_MultipleTransactions verifies independent txs don't interfere
func TestCheckAndMarkCommitProcessed_MultipleTransactions(t *testing.T) {
	chain := NewChain(0)

	// Mark multiple transactions
	txIDs := []string{"tx-a", "tx-b", "tx-c", "tx-d"}
	for _, txID := range txIDs {
		if !chain.CheckAndMarkCommitProcessed(txID) {
			t.Errorf("First mark of %s should succeed", txID)
		}
	}

	// All should now return false
	for _, txID := range txIDs {
		if chain.CheckAndMarkCommitProcessed(txID) {
			t.Errorf("Second mark of %s should fail", txID)
		}
	}
}

// ===== unlockAllSlotsForTx Tests (Two-Phase Deletion) =====

// TestUnlockAllSlotsForTx_MultipleSlots verifies two-phase deletion works correctly
func TestUnlockAllSlotsForTx_MultipleSlots(t *testing.T) {
	chain := NewChain(0)
	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")

	// Lock multiple slots for same tx
	for i := 0; i < 10; i++ {
		slot := common.HexToHash(fmt.Sprintf("0x%064d", i))
		err := chain.LockSlot("tx-1", addr, slot)
		if err != nil {
			t.Fatalf("Failed to lock slot %d: %v", i, err)
		}
	}

	// Verify all locked
	for i := 0; i < 10; i++ {
		slot := common.HexToHash(fmt.Sprintf("0x%064d", i))
		if !chain.IsSlotLocked(addr, slot) {
			t.Errorf("Slot %d should be locked", i)
		}
	}

	// Unlock all for tx
	chain.UnlockAllSlotsForTx("tx-1")

	// Verify all unlocked
	for i := 0; i < 10; i++ {
		slot := common.HexToHash(fmt.Sprintf("0x%064d", i))
		if chain.IsSlotLocked(addr, slot) {
			t.Errorf("Slot %d should be unlocked", i)
		}
	}
}

// TestUnlockAllSlotsForTx_MultipleAddresses verifies cleanup across multiple addresses
func TestUnlockAllSlotsForTx_MultipleAddresses(t *testing.T) {
	chain := NewChain(0)

	// Lock slots across multiple addresses
	addresses := []common.Address{
		common.HexToAddress("0x1111111111111111111111111111111111111111"),
		common.HexToAddress("0x2222222222222222222222222222222222222222"),
		common.HexToAddress("0x3333333333333333333333333333333333333333"),
	}

	slot := common.HexToHash("0x01")
	for _, addr := range addresses {
		err := chain.LockSlot("tx-1", addr, slot)
		if err != nil {
			t.Fatalf("Failed to lock slot for %s: %v", addr.Hex(), err)
		}
	}

	// Unlock all for tx
	chain.UnlockAllSlotsForTx("tx-1")

	// Verify all unlocked
	for _, addr := range addresses {
		if chain.IsSlotLocked(addr, slot) {
			t.Errorf("Slot for %s should be unlocked", addr.Hex())
		}
	}
}

// TestUnlockAllSlotsForTx_MixedTxs verifies only target tx slots are unlocked
func TestUnlockAllSlotsForTx_MixedTxs(t *testing.T) {
	chain := NewChain(0)
	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")

	slot1 := common.HexToHash("0x01")
	slot2 := common.HexToHash("0x02")

	// Lock slot1 with tx-1, slot2 with tx-2
	chain.LockSlot("tx-1", addr, slot1)
	chain.LockSlot("tx-2", addr, slot2)

	// Unlock only tx-1
	chain.UnlockAllSlotsForTx("tx-1")

	// slot1 should be unlocked, slot2 should still be locked
	if chain.IsSlotLocked(addr, slot1) {
		t.Error("slot1 should be unlocked (tx-1 unlocked)")
	}
	if !chain.IsSlotLocked(addr, slot2) {
		t.Error("slot2 should still be locked (tx-2 not unlocked)")
	}
}
