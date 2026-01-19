package protocol

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/ethereum/go-ethereum/common"
)

// Block types for state and transaction sharding

type BlockHash [32]byte

type OrchestratorShardBlock struct {
	Height    uint64          `json:"height"`
	PrevHash  BlockHash       `json:"prev_hash"`
	Timestamp uint64          `json:"timestamp"`
	TpcResult map[string]bool `json:"tpc_result,omitempty"`  // V2: txID -> committed
	CtToOrder []CrossShardTx  `json:"ct_to_order,omitempty"` // V2: New cross-shard txs
	CtToProcess []Transaction `json:"ct_to_process,omitempty"` // Baseline: Txs to route
}

type StateShardBlock struct {
	ShardID    int             `json:"shard_id"`    // Which shard produced this block
	Height     uint64          `json:"height"`
	PrevHash   BlockHash       `json:"prev_hash"`
	Timestamp  uint64          `json:"timestamp"`
	StateRoot  common.Hash     `json:"state_root"`
	TxOrdering []Transaction   `json:"tx_ordering"` // Local + cross-shard txs
	PrepareTxs []Transaction   `json:"prepare_txs,omitempty"` // Prepare ops (for crash recovery)
	TpcPrepare map[string]bool `json:"tpc_prepare"` // txID -> can_commit
}

func (b *OrchestratorShardBlock) Hash() BlockHash {
	data, _ := json.Marshal(b)
	return sha256.Sum256(data)
}

func (b *StateShardBlock) Hash() BlockHash {
	data, _ := json.Marshal(b)
	return sha256.Sum256(data)
}

// CrossShardTx is defined in types.go
