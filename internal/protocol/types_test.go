package protocol

import (
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
