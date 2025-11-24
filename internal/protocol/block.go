package protocol

import (
	"crypto/sha256"
	"encoding/json"

	"github.com/ethereum/go-ethereum/common"
)

// Block types for state and transaction sharding

type BlockHash [32]byte

type ContractShardBlock struct {
	Height    uint64                   `json:"height"`
	PrevHash  BlockHash                `json:"prev_hash"`
	Timestamp uint64                   `json:"timestamp"`
	TpcResult map[string]bool          `json:"tpc_result"`    // txID -> committed
	CtToOrder []CrossShardTransaction  `json:"ct_to_order"`   // New cross-shard txs
}

type StateShardBlock struct {
	Height     uint64            `json:"height"`
	PrevHash   BlockHash         `json:"prev_hash"`
	Timestamp  uint64            `json:"timestamp"`
	StateRoot  common.Hash       `json:"state_root"`
	TxOrdering []TxRef           `json:"tx_ordering"`  // Local + cross-shard txs
	TpcPrepare map[string]bool   `json:"tpc_prepare"`  // txID -> can_commit
}

// TxRef references a transaction (local or cross-shard)
type TxRef struct {
	TxID        string `json:"tx_id"`
	IsCrossShard bool  `json:"is_cross_shard"`
}

func (b *ContractShardBlock) Hash() BlockHash {
	data, _ := json.Marshal(b)
	return sha256.Sum256(data)
}

func (b *StateShardBlock) Hash() BlockHash {
	data, _ := json.Marshal(b)
	return sha256.Sum256(data)
}

// CrossShardTransaction is defined in types.go with full design.md spec (RwSet, etc.)
