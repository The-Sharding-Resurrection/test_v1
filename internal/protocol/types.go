package protocol

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// Slot represents a storage slot in a smart contract
type Slot common.Hash

// Reference to a specific block in a shard
type Reference struct {
	ShardNum    int         `json:"shard_num"`
	BlockHash   common.Hash `json:"block_hash"`
	BlockHeight uint64      `json:"block_height"`
}

// ReadSetItem represents a single state read with proof
type ReadSetItem struct {
	Slot  Slot     `json:"slot"`
	Value []byte   `json:"value"`
	Proof [][]byte `json:"proof"` // Merkle proof (empty for now, deferred)
}

// RwVariable represents read/write access to a contract's state
type RwVariable struct {
	Address        common.Address `json:"address"`
	ReferenceBlock Reference      `json:"reference_block"`
	ReadSet        []ReadSetItem  `json:"read_set"`
	WriteSet       []Slot         `json:"write_set"`
}

// CrossShardTransaction with ReadSet/WriteSet (design.md spec)
type CrossShardTransaction struct {
	TxHash common.Hash  `json:"tx_hash"`
	From   common.Address `json:"from"`
	To     common.Address `json:"to"`
	Value  *big.Int     `json:"value"`
	Data   []byte       `json:"data"`
	RwSet  []RwVariable `json:"rw_set"` // For cross-shard state access

	// Internal tracking fields
	TxID      string `json:"tx_id,omitempty"`
	FromShard int    `json:"from_shard"`
	ToShard   int    `json:"to_shard"`
}

// CrossShardTx (legacy HTTP API format - for compatibility)
type CrossShardTx struct {
	ID        string         `json:"id"`
	FromShard int            `json:"from_shard"`
	ToShard   int            `json:"to_shard"`
	From      common.Address `json:"from"`
	To        common.Address `json:"to"`
	Value     *big.Int       `json:"value"`
	Data      []byte         `json:"data"`
	Status    TxStatus       `json:"status"`
}

type TxStatus string

const (
	TxPending   TxStatus = "pending"
	TxPrepared  TxStatus = "prepared"
	TxCommitted TxStatus = "committed"
	TxAborted   TxStatus = "aborted"
)

// PrepareRequest sent by orchestrator to lock funds
type PrepareRequest struct {
	TxID    string         `json:"tx_id"`
	Address common.Address `json:"address"`
	Amount  *big.Int       `json:"amount"`
}

// PrepareResponse from shard confirming lock
type PrepareResponse struct {
	TxID    string `json:"tx_id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// CommitRequest to finalize the transaction
type CommitRequest struct {
	TxID string `json:"tx_id"`
}
