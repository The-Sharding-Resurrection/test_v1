package shard

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

// ===== Receipt DeepCopy Tests =====

// TestReceipt_DeepCopy_Basic verifies basic deep copy functionality
func TestReceipt_DeepCopy_Basic(t *testing.T) {
	original := &Receipt{
		TxHash:            common.HexToHash("0x1234567890"),
		TransactionIndex:  hexutil.Uint64(5),
		BlockHash:         common.HexToHash("0xabcdef"),
		From:              common.HexToAddress("0x1111"),
		CumulativeGasUsed: hexutil.Uint64(21000),
		GasUsed:           hexutil.Uint64(21000),
		Status:            hexutil.Uint64(1),
	}

	copy := original.DeepCopy()

	// Verify values are equal
	if copy.TxHash != original.TxHash {
		t.Errorf("TxHash mismatch: got %s, want %s", copy.TxHash.Hex(), original.TxHash.Hex())
	}
	if copy.TransactionIndex != original.TransactionIndex {
		t.Errorf("TransactionIndex mismatch: got %d, want %d", copy.TransactionIndex, original.TransactionIndex)
	}
	if copy.BlockHash != original.BlockHash {
		t.Errorf("BlockHash mismatch: got %s, want %s", copy.BlockHash.Hex(), original.BlockHash.Hex())
	}
	if copy.From != original.From {
		t.Errorf("From mismatch: got %s, want %s", copy.From.Hex(), original.From.Hex())
	}
	if copy.Status != original.Status {
		t.Errorf("Status mismatch: got %d, want %d", copy.Status, original.Status)
	}

	// Verify modifying copy doesn't affect original
	copy.Status = 0
	if original.Status != 1 {
		t.Error("Modifying copy should not affect original")
	}
}

// TestReceipt_DeepCopy_NilReceipt verifies DeepCopy handles nil receiver
func TestReceipt_DeepCopy_NilReceipt(t *testing.T) {
	var r *Receipt
	copy := r.DeepCopy()
	if copy != nil {
		t.Error("DeepCopy of nil should return nil")
	}
}

// TestReceipt_DeepCopy_WithBlockNumber verifies BlockNumber pointer is properly copied
func TestReceipt_DeepCopy_WithBlockNumber(t *testing.T) {
	blockNum := hexutil.Big(*big.NewInt(12345))
	original := &Receipt{
		TxHash:      common.HexToHash("0x1234"),
		BlockNumber: &blockNum,
	}

	copy := original.DeepCopy()

	// Verify BlockNumber is copied
	if copy.BlockNumber == nil {
		t.Fatal("BlockNumber should be copied")
	}
	if copy.BlockNumber.ToInt().Cmp(original.BlockNumber.ToInt()) != 0 {
		t.Errorf("BlockNumber mismatch: got %s, want %s",
			copy.BlockNumber.ToInt().String(), original.BlockNumber.ToInt().String())
	}

	// Verify it's a separate pointer
	if copy.BlockNumber == original.BlockNumber {
		t.Error("BlockNumber pointer should be different (deep copy)")
	}
}

// TestReceipt_DeepCopy_WithTo verifies To pointer is properly copied
func TestReceipt_DeepCopy_WithTo(t *testing.T) {
	to := common.HexToAddress("0x5678")
	original := &Receipt{
		TxHash: common.HexToHash("0x1234"),
		To:     &to,
	}

	copy := original.DeepCopy()

	// Verify To is copied
	if copy.To == nil {
		t.Fatal("To should be copied")
	}
	if *copy.To != *original.To {
		t.Errorf("To mismatch: got %s, want %s", copy.To.Hex(), original.To.Hex())
	}

	// Verify it's a separate pointer
	if copy.To == original.To {
		t.Error("To pointer should be different (deep copy)")
	}
}

// TestReceipt_DeepCopy_WithContractAddress verifies ContractAddress is properly copied
func TestReceipt_DeepCopy_WithContractAddress(t *testing.T) {
	contractAddr := common.HexToAddress("0xcontract")
	original := &Receipt{
		TxHash:          common.HexToHash("0x1234"),
		ContractAddress: &contractAddr,
	}

	copy := original.DeepCopy()

	// Verify ContractAddress is copied
	if copy.ContractAddress == nil {
		t.Fatal("ContractAddress should be copied")
	}
	if *copy.ContractAddress != *original.ContractAddress {
		t.Errorf("ContractAddress mismatch: got %s, want %s",
			copy.ContractAddress.Hex(), original.ContractAddress.Hex())
	}

	// Verify it's a separate pointer
	if copy.ContractAddress == original.ContractAddress {
		t.Error("ContractAddress pointer should be different (deep copy)")
	}
}

// TestReceipt_DeepCopy_WithLogs verifies Logs slice is properly copied
func TestReceipt_DeepCopy_WithLogs(t *testing.T) {
	original := &Receipt{
		TxHash: common.HexToHash("0x1234"),
		Logs: []*types.Log{
			{
				Address: common.HexToAddress("0xlog1"),
				Topics:  []common.Hash{common.HexToHash("0xtopic1"), common.HexToHash("0xtopic2")},
				Data:    []byte{0x01, 0x02, 0x03},
			},
			{
				Address: common.HexToAddress("0xlog2"),
				Topics:  []common.Hash{common.HexToHash("0xtopic3")},
				Data:    []byte{0x04, 0x05},
			},
		},
	}

	copyReceipt := original.DeepCopy()

	// Verify Logs slice is copied
	if len(copyReceipt.Logs) != len(original.Logs) {
		t.Fatalf("Logs length mismatch: got %d, want %d", len(copyReceipt.Logs), len(original.Logs))
	}

	// Verify each log is a deep copy
	for i := range original.Logs {
		if copyReceipt.Logs[i] == original.Logs[i] {
			t.Errorf("Log %d pointer should be different (deep copy)", i)
		}
		if copyReceipt.Logs[i].Address != original.Logs[i].Address {
			t.Errorf("Log %d Address mismatch", i)
		}
		if len(copyReceipt.Logs[i].Topics) != len(original.Logs[i].Topics) {
			t.Errorf("Log %d Topics length mismatch", i)
		}
		if len(copyReceipt.Logs[i].Data) != len(original.Logs[i].Data) {
			t.Errorf("Log %d Data length mismatch", i)
		}
	}

	// Verify Topics slice is independent
	// Note: common.Hash is a [32]byte array (value type), so assigning to Topics[0]
	// replaces the value in the slice. We verify the slices themselves are different.
	if &copyReceipt.Logs[0].Topics[0] == &original.Logs[0].Topics[0] {
		t.Error("Topics slice should be a separate allocation")
	}

	// Verify Data slice is independent by modifying copy
	copyReceipt.Logs[0].Data[0] = 0xff
	if original.Logs[0].Data[0] == 0xff {
		t.Error("Modifying copy's Data should not affect original")
	}
}

// TestReceipt_DeepCopy_WithReturnData verifies ReturnData is properly copied
func TestReceipt_DeepCopy_WithReturnData(t *testing.T) {
	original := &Receipt{
		TxHash:     common.HexToHash("0x1234"),
		ReturnData: hexutil.Bytes{0xaa, 0xbb, 0xcc, 0xdd},
	}

	copy := original.DeepCopy()

	// Verify ReturnData is copied
	if len(copy.ReturnData) != len(original.ReturnData) {
		t.Fatalf("ReturnData length mismatch: got %d, want %d",
			len(copy.ReturnData), len(original.ReturnData))
	}

	// Modify copy and verify original unchanged
	copy.ReturnData[0] = 0xff
	if original.ReturnData[0] == 0xff {
		t.Error("Modifying copy's ReturnData should not affect original")
	}
}

// TestReceipt_DeepCopy_NilLogs verifies nil Logs is handled
func TestReceipt_DeepCopy_NilLogs(t *testing.T) {
	original := &Receipt{
		TxHash: common.HexToHash("0x1234"),
		Logs:   nil,
	}

	copy := original.DeepCopy()

	if copy.Logs != nil {
		t.Error("Nil Logs should remain nil after copy")
	}
}

// TestReceipt_DeepCopy_EmptyLogs verifies empty Logs slice is handled
func TestReceipt_DeepCopy_EmptyLogs(t *testing.T) {
	original := &Receipt{
		TxHash: common.HexToHash("0x1234"),
		Logs:   []*types.Log{},
	}

	copy := original.DeepCopy()

	if copy.Logs == nil {
		t.Error("Empty Logs should not become nil")
	}
	if len(copy.Logs) != 0 {
		t.Errorf("Empty Logs should remain empty, got %d", len(copy.Logs))
	}
}

// ===== ReceiptStore Tests =====

// TestReceiptStore_AddAndGet verifies basic add/get operations
func TestReceiptStore_AddAndGet(t *testing.T) {
	store := NewReceiptStore()

	txHash := common.HexToHash("0x1234567890abcdef")
	receipt := &Receipt{
		TxHash:  txHash,
		Status:  hexutil.Uint64(1),
		GasUsed: hexutil.Uint64(21000),
	}

	// Add receipt
	store.AddReceipt(receipt)

	// Get receipt
	got := store.GetReceipt(txHash)
	if got == nil {
		t.Fatal("GetReceipt returned nil for existing receipt")
	}

	// Verify fields
	if got.TxHash != txHash {
		t.Errorf("TxHash mismatch: got %s, want %s", got.TxHash.Hex(), txHash.Hex())
	}
	if got.Status != 1 {
		t.Errorf("Status mismatch: got %d, want 1", got.Status)
	}
	if got.GasUsed != 21000 {
		t.Errorf("GasUsed mismatch: got %d, want 21000", got.GasUsed)
	}
}

// TestReceiptStore_GetNonExistent verifies getting non-existent receipt
func TestReceiptStore_GetNonExistent(t *testing.T) {
	store := NewReceiptStore()

	got := store.GetReceipt(common.HexToHash("0xnonexistent"))
	if got != nil {
		t.Errorf("Expected nil for non-existent receipt, got %+v", got)
	}
}

// TestReceiptStore_Isolation verifies returned receipts are isolated (deep copies)
func TestReceiptStore_Isolation(t *testing.T) {
	store := NewReceiptStore()

	txHash := common.HexToHash("0x1234")
	original := &Receipt{
		TxHash:     txHash,
		Status:     hexutil.Uint64(1),
		ReturnData: hexutil.Bytes{0xaa, 0xbb},
	}

	store.AddReceipt(original)

	// Modify original after adding
	original.Status = 999
	original.ReturnData[0] = 0xff

	// Get receipt - should have original values
	got := store.GetReceipt(txHash)
	if got.Status == 999 {
		t.Error("Modifying original after add should not affect stored receipt")
	}
	if got.ReturnData[0] == 0xff {
		t.Error("Modifying original's ReturnData after add should not affect stored receipt")
	}

	// Modify retrieved receipt
	got.Status = 888

	// Get again - should still have original values
	got2 := store.GetReceipt(txHash)
	if got2.Status == 888 {
		t.Error("Modifying retrieved receipt should not affect stored receipt")
	}
}

// TestReceiptStore_Overwrite verifies adding receipt with same hash overwrites
func TestReceiptStore_Overwrite(t *testing.T) {
	store := NewReceiptStore()

	txHash := common.HexToHash("0x1234")

	// Add first receipt
	store.AddReceipt(&Receipt{
		TxHash: txHash,
		Status: hexutil.Uint64(0), // Failure
	})

	// Add second receipt with same hash
	store.AddReceipt(&Receipt{
		TxHash: txHash,
		Status: hexutil.Uint64(1), // Success
	})

	// Get should return second receipt
	got := store.GetReceipt(txHash)
	if got.Status != 1 {
		t.Errorf("Expected Status 1 after overwrite, got %d", got.Status)
	}
}

// TestReceiptStore_MultipleReceipts verifies multiple receipts can be stored
func TestReceiptStore_MultipleReceipts(t *testing.T) {
	store := NewReceiptStore()

	hashes := []common.Hash{
		common.HexToHash("0x1111"),
		common.HexToHash("0x2222"),
		common.HexToHash("0x3333"),
	}

	// Add multiple receipts
	for i, hash := range hashes {
		store.AddReceipt(&Receipt{
			TxHash: hash,
			Status: hexutil.Uint64(i + 1),
		})
	}

	// Verify all can be retrieved
	for i, hash := range hashes {
		got := store.GetReceipt(hash)
		if got == nil {
			t.Fatalf("Receipt %d not found", i)
		}
		if got.Status != hexutil.Uint64(i+1) {
			t.Errorf("Receipt %d Status mismatch: got %d, want %d", i, got.Status, i+1)
		}
	}
}

// TestReceiptStore_ConcurrentAccess verifies thread-safety of receipt store
func TestReceiptStore_ConcurrentAccess(t *testing.T) {
	store := NewReceiptStore()
	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			hash := common.BigToHash(big.NewInt(int64(i)))
			store.AddReceipt(&Receipt{
				TxHash: hash,
				Status: hexutil.Uint64(i),
			})
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			hash := common.BigToHash(big.NewInt(int64(i)))
			_ = store.GetReceipt(hash) // May be nil if not yet added
		}
		done <- true
	}()

	// Wait for both to complete without panic
	<-done
	<-done
}
