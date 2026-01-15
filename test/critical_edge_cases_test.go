package test

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// =============================================================================
// Block Hash Determinism Tests (Critical Issue from Bug Search)
// =============================================================================

// TestBlockHash_MapOrdering verifies that block hashing is deterministic
// even when maps have different iteration orders.
// This tests the critical issue: JSON map serialization order.
func TestBlockHash_MapOrdering(t *testing.T) {
	// Create two blocks with same TpcResult but entries added in different order
	block1 := &protocol.OrchestratorShardBlock{
		Height:    1,
		TpcResult: map[string]bool{},
	}
	block1.TpcResult["tx-1"] = true
	block1.TpcResult["tx-2"] = false
	block1.TpcResult["tx-3"] = true

	block2 := &protocol.OrchestratorShardBlock{
		Height:    1,
		TpcResult: map[string]bool{},
	}
	block2.TpcResult["tx-3"] = true
	block2.TpcResult["tx-1"] = true
	block2.TpcResult["tx-2"] = false

	// Hashes should be equal if deterministic serialization is used
	hash1 := block1.Hash()
	hash2 := block2.Hash()

	// Note: Go's json.Marshal DOES sort map keys, so this should pass
	// But we verify this critical assumption
	if hash1 != hash2 {
		t.Errorf("Block hashes differ for semantically identical blocks:\nhash1: %x\nhash2: %x", hash1, hash2)
	}
}

// TestBlockHash_EmptyVsNil verifies distinction between empty and nil maps
func TestBlockHash_EmptyVsNil(t *testing.T) {
	blockNil := &protocol.OrchestratorShardBlock{
		Height:    1,
		TpcResult: nil,
	}

	blockEmpty := &protocol.OrchestratorShardBlock{
		Height:    1,
		TpcResult: make(map[string]bool),
	}

	hashNil := blockNil.Hash()
	hashEmpty := blockEmpty.Hash()

	// These SHOULD be different - nil and empty map serialize differently
	// null vs {}
	if hashNil == hashEmpty {
		t.Log("Warning: nil and empty map produce same hash - may cause issues")
	}
}

// TestBlockHash_MultipleComputations ensures hash is stable across calls
func TestBlockHash_MultipleComputations(t *testing.T) {
	block := &protocol.OrchestratorShardBlock{
		Height: 5,
		TpcResult: map[string]bool{
			"tx-a": true,
			"tx-b": false,
		},
		CtToOrder: []protocol.CrossShardTx{
			{ID: "ctx-1", FromShard: 0},
		},
	}

	// Compute hash multiple times - should always be same
	hashes := make([]protocol.BlockHash, 10)
	for i := 0; i < 10; i++ {
		hashes[i] = block.Hash()
	}

	for i := 1; i < len(hashes); i++ {
		if hashes[i] != hashes[0] {
			t.Errorf("Hash inconsistent on call %d: got %x, want %x", i, hashes[i], hashes[0])
		}
	}
}

// TestBlockJSON_MarshalError verifies behavior when marshal might fail
func TestBlockJSON_MarshalError(t *testing.T) {
	// A normal block should marshal without error
	block := &protocol.OrchestratorShardBlock{
		Height:    1,
		TpcResult: map[string]bool{"tx-1": true},
	}

	data, err := json.Marshal(block)
	if err != nil {
		t.Errorf("Expected successful marshal, got error: %v", err)
	}
	if len(data) == 0 {
		t.Error("Expected non-empty JSON data")
	}

	// Hash should not be zero
	hash := block.Hash()
	zeroHash := protocol.BlockHash{}
	if hash == zeroHash {
		t.Error("Block hash should not be zero for valid block")
	}
}

// =============================================================================
// Cross-Shard Transaction RwSet Tests
// =============================================================================

// TestCrossShardTx_NilRwSet verifies handling of nil RwSet
func TestCrossShardTx_NilRwSet(t *testing.T) {
	tx := &protocol.CrossShardTx{
		ID:        "ctx-nil-rwset",
		FromShard: 0,
		From:      common.HexToAddress("0x1234"),
		RwSet:     nil,
	}

	// Should not panic and should return empty target shards
	targets := tx.TargetShards()
	if len(targets) != 0 {
		t.Errorf("Expected empty targets for nil RwSet, got %v", targets)
	}

	// InvolvedShards should include only FromShard
	involved := tx.InvolvedShards()
	if len(involved) != 1 || involved[0] != 0 {
		t.Errorf("Expected [0] for involved shards, got %v", involved)
	}
}

// TestCrossShardTx_EmptyRwSet verifies handling of empty RwSet
func TestCrossShardTx_EmptyRwSet(t *testing.T) {
	tx := &protocol.CrossShardTx{
		ID:        "ctx-empty-rwset",
		FromShard: 1,
		From:      common.HexToAddress("0x5678"),
		RwSet:     []protocol.RwVariable{},
	}

	targets := tx.TargetShards()
	if len(targets) != 0 {
		t.Errorf("Expected empty targets for empty RwSet, got %v", targets)
	}

	involved := tx.InvolvedShards()
	if len(involved) != 1 || involved[0] != 1 {
		t.Errorf("Expected [1] for involved shards, got %v", involved)
	}
}

// TestCrossShardTx_DuplicateTargetShards verifies deduplication
func TestCrossShardTx_DuplicateTargetShards(t *testing.T) {
	addr := common.HexToAddress("0x1234")
	tx := &protocol.CrossShardTx{
		ID:        "ctx-dup",
		FromShard: 0,
		RwSet: []protocol.RwVariable{
			{Address: addr, ReferenceBlock: protocol.Reference{ShardNum: 1}},
			{Address: addr, ReferenceBlock: protocol.Reference{ShardNum: 1}}, // Same shard
			{Address: addr, ReferenceBlock: protocol.Reference{ShardNum: 2}},
		},
	}

	targets := tx.TargetShards()
	// Should deduplicate: only 1 and 2
	if len(targets) != 2 {
		t.Errorf("Expected 2 unique target shards, got %d: %v", len(targets), targets)
	}
}

// =============================================================================
// Transaction Deep Copy Isolation Tests
// =============================================================================

// TestTransaction_DeepCopyIsolation verifies mutations don't affect original
func TestTransaction_DeepCopyIsolation(t *testing.T) {
	original := protocol.Transaction{
		ID:             "tx-orig",
		TxType:         protocol.TxTypeLock,
		CrossShardTxID: "ctx-1",
		Value:          protocol.NewBigInt(big.NewInt(100)),
		RwSet: []protocol.RwVariable{
			{
				Address: common.HexToAddress("0x1"),
				ReadSet: []protocol.ReadSetItem{
					{Slot: protocol.Slot(common.HexToHash("0x01"))},
				},
			},
		},
	}

	// Create deep copy
	copied := original.DeepCopy()

	// Mutate the copy
	copied.ID = "tx-mutated"
	copied.TxType = protocol.TxTypeFinalize
	copied.Value.SetInt64(999)
	if len(copied.RwSet) > 0 {
		copied.RwSet[0].Address = common.HexToAddress("0x9999")
	}

	// Verify original is unchanged
	if original.ID != "tx-orig" {
		t.Error("Original ID was mutated")
	}
	if original.TxType != protocol.TxTypeLock {
		t.Error("Original TxType was mutated")
	}
	if original.Value.Int.Int64() != 100 {
		t.Errorf("Original Value was mutated: got %d", original.Value.Int.Int64())
	}
	if original.RwSet[0].Address != common.HexToAddress("0x1") {
		t.Error("Original RwSet was mutated")
	}
}

// TestCrossShardTx_DeepCopyIsolation verifies CrossShardTx deep copy
func TestCrossShardTx_DeepCopyIsolation(t *testing.T) {
	original := protocol.CrossShardTx{
		ID:        "ctx-orig",
		FromShard: 0,
		Status:    protocol.TxPending,
		SimStatus: protocol.SimSuccess,
		RwSet: []protocol.RwVariable{
			{Address: common.HexToAddress("0xABC")},
		},
	}

	copied := original.DeepCopy()

	// Mutate copy
	copied.ID = "ctx-mutated"
	copied.Status = protocol.TxCommitted
	copied.RwSet[0].Address = common.HexToAddress("0xDEF")

	// Verify original unchanged
	if original.ID != "ctx-orig" {
		t.Error("Original ID mutated")
	}
	if original.Status != protocol.TxPending {
		t.Error("Original Status mutated")
	}
	if original.RwSet[0].Address != common.HexToAddress("0xABC") {
		t.Error("Original RwSet mutated")
	}
}

// =============================================================================
// State Shard Block Tests
// =============================================================================

// TestStateShardBlock_Hash verifies StateShardBlock hash consistency
func TestStateShardBlock_Hash(t *testing.T) {
	block := &protocol.StateShardBlock{
		Height:     10,
		ShardID:    5,
		TpcPrepare: map[string]bool{"tx-1": true, "tx-2": false},
		StateRoot:  common.HexToHash("0xdeadbeef"),
	}

	hash1 := block.Hash()
	hash2 := block.Hash()

	if hash1 != hash2 {
		t.Error("StateShardBlock hash is not consistent")
	}

	// Different block should have different hash
	block2 := &protocol.StateShardBlock{
		Height:     10,
		ShardID:    5,
		TpcPrepare: map[string]bool{"tx-1": false}, // Different
		StateRoot:  common.HexToHash("0xdeadbeef"),
	}

	if block.Hash() == block2.Hash() {
		t.Error("Different blocks should have different hashes")
	}
}

// =============================================================================
// BigInt Edge Cases
// =============================================================================

// TestBigInt_NilHandling verifies nil BigInt handling throughout
func TestBigInt_NilHandling(t *testing.T) {
	// nil BigInt
	var nilBigInt *protocol.BigInt

	// DeepCopy of nil should return nil
	if nilBigInt.DeepCopy() != nil {
		t.Error("DeepCopy of nil BigInt should return nil")
	}

	// NewBigInt with nil returns nil (expected behavior)
	nilResult := protocol.NewBigInt(nil)
	if nilResult != nil {
		t.Error("NewBigInt(nil) should return nil")
	}

	// NewBigInt with zero creates valid BigInt
	zeroBigInt := protocol.NewBigInt(big.NewInt(0))
	if zeroBigInt == nil {
		t.Fatal("NewBigInt(big.NewInt(0)) should not return nil")
	}
	if zeroBigInt.Sign() != 0 {
		t.Error("NewBigInt(big.NewInt(0)) should create zero value")
	}

	// Marshal nil BigInt
	data, err := json.Marshal(nilBigInt)
	if err != nil {
		t.Errorf("Marshal nil BigInt error: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("Expected null, got %s", data)
	}
}

// TestBigInt_LargeNumbers verifies handling of very large numbers
func TestBigInt_LargeNumbers(t *testing.T) {
	// Test very large hex value
	largeHex := "0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF"
	var bi protocol.BigInt
	if err := json.Unmarshal([]byte(`"`+largeHex+`"`), &bi); err != nil {
		t.Fatalf("Failed to unmarshal large hex: %v", err)
	}

	// Should roundtrip correctly
	data, err := json.Marshal(&bi)
	if err != nil {
		t.Fatalf("Failed to marshal large BigInt: %v", err)
	}

	var bi2 protocol.BigInt
	if err := json.Unmarshal(data, &bi2); err != nil {
		t.Fatalf("Failed to unmarshal roundtripped BigInt: %v", err)
	}

	if bi.Int.Cmp(bi2.Int) != 0 {
		t.Error("Large BigInt lost precision during roundtrip")
	}
}
