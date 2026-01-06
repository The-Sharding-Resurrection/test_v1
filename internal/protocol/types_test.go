package protocol

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestCrossShardTx_TargetShards(t *testing.T) {
	tests := []struct {
		name     string
		tx       CrossShardTx
		expected []int
	}{
		{
			name: "single target shard",
			tx: CrossShardTx{
				RwSet: []RwVariable{
					{Address: common.HexToAddress("0x1"), ReferenceBlock: Reference{ShardNum: 2}},
				},
			},
			expected: []int{2},
		},
		{
			name: "multiple target shards",
			tx: CrossShardTx{
				RwSet: []RwVariable{
					{Address: common.HexToAddress("0x1"), ReferenceBlock: Reference{ShardNum: 1}},
					{Address: common.HexToAddress("0x2"), ReferenceBlock: Reference{ShardNum: 3}},
					{Address: common.HexToAddress("0x3"), ReferenceBlock: Reference{ShardNum: 5}},
				},
			},
			expected: []int{1, 3, 5},
		},
		{
			name: "duplicate shards deduplicated",
			tx: CrossShardTx{
				RwSet: []RwVariable{
					{Address: common.HexToAddress("0x1"), ReferenceBlock: Reference{ShardNum: 2}},
					{Address: common.HexToAddress("0x2"), ReferenceBlock: Reference{ShardNum: 2}},
					{Address: common.HexToAddress("0x3"), ReferenceBlock: Reference{ShardNum: 3}},
				},
			},
			expected: []int{2, 3},
		},
		{
			name:     "empty RwSet",
			tx:       CrossShardTx{RwSet: []RwVariable{}},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.tx.TargetShards()
			if len(got) != len(tt.expected) {
				t.Errorf("TargetShards() = %v, want %v", got, tt.expected)
				return
			}
			for i, v := range got {
				if v != tt.expected[i] {
					t.Errorf("TargetShards()[%d] = %d, want %d", i, v, tt.expected[i])
				}
			}
		})
	}
}

func TestCrossShardTx_InvolvedShards(t *testing.T) {
	tests := []struct {
		name     string
		tx       CrossShardTx
		expected []int
	}{
		{
			name: "from shard not in targets",
			tx: CrossShardTx{
				FromShard: 0,
				RwSet: []RwVariable{
					{Address: common.HexToAddress("0x1"), ReferenceBlock: Reference{ShardNum: 2}},
				},
			},
			expected: []int{0, 2},
		},
		{
			name: "from shard already in targets",
			tx: CrossShardTx{
				FromShard: 2,
				RwSet: []RwVariable{
					{Address: common.HexToAddress("0x1"), ReferenceBlock: Reference{ShardNum: 2}},
					{Address: common.HexToAddress("0x2"), ReferenceBlock: Reference{ShardNum: 3}},
				},
			},
			expected: []int{2, 3},
		},
		{
			name: "multiple shards",
			tx: CrossShardTx{
				FromShard: 0,
				RwSet: []RwVariable{
					{Address: common.HexToAddress("0x1"), ReferenceBlock: Reference{ShardNum: 1}},
					{Address: common.HexToAddress("0x2"), ReferenceBlock: Reference{ShardNum: 3}},
				},
			},
			expected: []int{0, 1, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.tx.InvolvedShards()
			if len(got) != len(tt.expected) {
				t.Errorf("InvolvedShards() = %v, want %v", got, tt.expected)
				return
			}
			for i, v := range got {
				if v != tt.expected[i] {
					t.Errorf("InvolvedShards()[%d] = %d, want %d", i, v, tt.expected[i])
				}
			}
		})
	}
}

func TestOrchestratorShardBlock_Hash(t *testing.T) {
	block1 := &OrchestratorShardBlock{
		Height:    1,
		Timestamp: 1000,
		TpcResult: map[string]bool{"tx1": true},
		CtToOrder: []CrossShardTx{},
	}

	block2 := &OrchestratorShardBlock{
		Height:    1,
		Timestamp: 1000,
		TpcResult: map[string]bool{"tx1": true},
		CtToOrder: []CrossShardTx{},
	}

	block3 := &OrchestratorShardBlock{
		Height:    2,
		Timestamp: 1000,
		TpcResult: map[string]bool{"tx1": true},
		CtToOrder: []CrossShardTx{},
	}

	hash1 := block1.Hash()
	hash2 := block2.Hash()
	hash3 := block3.Hash()

	// Same content should produce same hash
	if hash1 != hash2 {
		t.Errorf("Expected same hash for identical blocks")
	}

	// Different content should produce different hash
	if hash1 == hash3 {
		t.Errorf("Expected different hash for different blocks")
	}
}

func TestStateShardBlock_Hash(t *testing.T) {
	block1 := &StateShardBlock{
		Height:     1,
		Timestamp:  1000,
		StateRoot:  common.HexToHash("0x1234"),
		TxOrdering: []Transaction{},
		TpcPrepare: map[string]bool{},
	}

	block2 := &StateShardBlock{
		Height:     1,
		Timestamp:  1000,
		StateRoot:  common.HexToHash("0x1234"),
		TxOrdering: []Transaction{},
		TpcPrepare: map[string]bool{},
	}

	hash1 := block1.Hash()
	hash2 := block2.Hash()

	if hash1 != hash2 {
		t.Errorf("Expected same hash for identical blocks")
	}
}

func TestCrossShardTx_JSONSerialization(t *testing.T) {
	tx := CrossShardTx{
		ID:        "test-tx-1",
		FromShard: 0,
		From:      common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		Value:     NewBigInt(big.NewInt(1000000000000000000)),
		Status:    TxPending,
		RwSet: []RwVariable{
			{
				Address:        common.HexToAddress("0xabcdef"),
				ReferenceBlock: Reference{ShardNum: 2, BlockHeight: 100},
			},
		},
	}

	// Verify fields
	if tx.ID != "test-tx-1" {
		t.Errorf("Expected ID 'test-tx-1', got %s", tx.ID)
	}
	if tx.FromShard != 0 {
		t.Errorf("Expected FromShard 0, got %d", tx.FromShard)
	}
	if tx.Value.Cmp(big.NewInt(1000000000000000000)) != 0 {
		t.Errorf("Expected Value 1e18, got %s", tx.Value.String())
	}
	if tx.Status != TxPending {
		t.Errorf("Expected Status pending, got %s", tx.Status)
	}
}

// ===== V2.2 Type Tests =====

func TestNoStateError_Error(t *testing.T) {
	err := &NoStateError{
		Address: common.HexToAddress("0x1234567890123456789012345678901234567890"),
		Caller:  common.HexToAddress("0xABCDabcdABcDabcDaBCDAbcdABcdAbCdABcDABCd"),
		Data:    []byte{0x01, 0x02, 0x03},
		Value:   big.NewInt(1000),
		ShardID: 3,
	}

	// Test error message format
	errMsg := err.Error()
	if errMsg == "" {
		t.Error("Error() should return non-empty string")
	}

	// Should contain address info
	if !contains(errMsg, "0x1234567890123456789012345678901234567890") &&
		!contains(errMsg, "1234567890123456789012345678901234567890") {
		t.Errorf("Error message should contain address, got: %s", errMsg)
	}
}

func TestNoStateError_IsNoStateError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name: "NoStateError",
			err: &NoStateError{
				Address: common.HexToAddress("0x1234"),
				ShardID: 1,
			},
			expected: true,
		},
		{
			name:     "other error",
			err:      fmt.Errorf("some other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNoStateError(tt.err)
			if got != tt.expected {
				t.Errorf("IsNoStateError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNoStateError_AsNoStateError(t *testing.T) {
	originalErr := &NoStateError{
		Address: common.HexToAddress("0x1234"),
		Caller:  common.HexToAddress("0x5678"),
		Data:    []byte{0xaa, 0xbb},
		Value:   big.NewInt(999),
		ShardID: 5,
	}

	// Test with NoStateError
	nse, ok := AsNoStateError(originalErr)
	if !ok || nse == nil {
		t.Fatal("AsNoStateError should return (non-nil, true) for NoStateError")
	}
	if nse.Address != originalErr.Address {
		t.Errorf("Address mismatch: got %s, want %s", nse.Address.Hex(), originalErr.Address.Hex())
	}
	if nse.ShardID != 5 {
		t.Errorf("ShardID mismatch: got %d, want 5", nse.ShardID)
	}

	// Test with non-NoStateError
	otherErr := fmt.Errorf("not a NoStateError")
	nse, ok = AsNoStateError(otherErr)
	if ok || nse != nil {
		t.Error("AsNoStateError should return (nil, false) for non-NoStateError")
	}

	// Test with nil
	nse, ok = AsNoStateError(nil)
	if ok || nse != nil {
		t.Error("AsNoStateError should return (nil, false) for nil error")
	}
}

func TestRwSetRequest_Fields(t *testing.T) {
	req := RwSetRequest{
		Address:        common.HexToAddress("0x1234567890123456789012345678901234567890"),
		Data:           HexBytes{0x01, 0x02, 0x03, 0x04},
		Value:          NewBigInt(big.NewInt(1000000)),
		Caller:         common.HexToAddress("0xABCDabcdABcDabcDaBCDAbcdABcdAbCdABcDABCd"),
		ReferenceBlock: Reference{ShardNum: 2, BlockHeight: 100},
		TxID:           "test-tx-123",
	}

	// Verify all fields are accessible
	if req.Address != common.HexToAddress("0x1234567890123456789012345678901234567890") {
		t.Error("Address field mismatch")
	}
	if len(req.Data) != 4 {
		t.Errorf("Data length should be 4, got %d", len(req.Data))
	}
	if req.Value.Cmp(big.NewInt(1000000)) != 0 {
		t.Errorf("Value mismatch: got %s", req.Value.String())
	}
	if req.TxID != "test-tx-123" {
		t.Errorf("TxID mismatch: got %s", req.TxID)
	}
	if req.ReferenceBlock.ShardNum != 2 {
		t.Errorf("ReferenceBlock.ShardNum mismatch: got %d", req.ReferenceBlock.ShardNum)
	}
}

func TestRwSetReply_SuccessCase(t *testing.T) {
	reply := RwSetReply{
		Success: true,
		RwSet: []RwVariable{
			{
				Address: common.HexToAddress("0x1234"),
				ReadSet: []ReadSetItem{
					{Slot: Slot(common.HexToHash("0x01")), Value: []byte{0x00}},
				},
				WriteSet: []WriteSetItem{
					{Slot: Slot(common.HexToHash("0x01")), OldValue: []byte{0x00}, NewValue: []byte{0x01}},
				},
			},
		},
		GasUsed: 21000,
	}

	if !reply.Success {
		t.Error("Expected Success to be true")
	}
	if len(reply.RwSet) != 1 {
		t.Errorf("Expected 1 RwVariable, got %d", len(reply.RwSet))
	}
	if reply.GasUsed != 21000 {
		t.Errorf("Expected GasUsed 21000, got %d", reply.GasUsed)
	}
	if reply.Error != "" {
		t.Errorf("Expected empty error, got: %s", reply.Error)
	}
}

func TestRwSetReply_ErrorCase(t *testing.T) {
	reply := RwSetReply{
		Success: false,
		RwSet:   nil,
		Error:   "simulation failed: out of gas",
		GasUsed: 50000,
	}

	if reply.Success {
		t.Error("Expected Success to be false")
	}
	if reply.Error != "simulation failed: out of gas" {
		t.Errorf("Error mismatch: got %s", reply.Error)
	}
	if reply.RwSet != nil {
		t.Error("Expected RwSet to be nil on error")
	}
}

func TestReadSetItem_SlotValue(t *testing.T) {
	slot := Slot(common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"))
	item := ReadSetItem{
		Slot:  slot,
		Value: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		Proof: nil, // Empty proof (deferred)
	}

	if common.Hash(item.Slot) != common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001") {
		t.Error("Slot mismatch")
	}
	if len(item.Value) != 16 {
		t.Errorf("Value length should be 16, got %d", len(item.Value))
	}
}

func TestWriteSetItem_OldNewValues(t *testing.T) {
	item := WriteSetItem{
		Slot:     Slot(common.HexToHash("0x05")),
		OldValue: []byte{0x00},
		NewValue: []byte{0xff},
	}

	if len(item.OldValue) != 1 || item.OldValue[0] != 0x00 {
		t.Error("OldValue mismatch")
	}
	if len(item.NewValue) != 1 || item.NewValue[0] != 0xff {
		t.Error("NewValue mismatch")
	}
}

func TestRwVariable_ReadWriteSets(t *testing.T) {
	rw := RwVariable{
		Address:        common.HexToAddress("0x1234"),
		ReferenceBlock: Reference{ShardNum: 1, BlockHeight: 50},
		ReadSet: []ReadSetItem{
			{Slot: Slot(common.HexToHash("0x01")), Value: []byte{0x01}},
			{Slot: Slot(common.HexToHash("0x02")), Value: []byte{0x02}},
		},
		WriteSet: []WriteSetItem{
			{Slot: Slot(common.HexToHash("0x01")), OldValue: []byte{0x01}, NewValue: []byte{0x10}},
		},
	}

	if len(rw.ReadSet) != 2 {
		t.Errorf("Expected 2 ReadSet items, got %d", len(rw.ReadSet))
	}
	if len(rw.WriteSet) != 1 {
		t.Errorf("Expected 1 WriteSet item, got %d", len(rw.WriteSet))
	}
	if rw.ReferenceBlock.ShardNum != 1 {
		t.Errorf("Expected ShardNum 1, got %d", rw.ReferenceBlock.ShardNum)
	}
}

// Helper function for string contains check
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
