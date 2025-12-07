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

// Transaction represents a local transaction within a shard
type Transaction struct {
	ID           string         `json:"id,omitempty"`
	TxHash       common.Hash    `json:"tx_hash,omitempty"`
	From         common.Address `json:"from"`
	To           common.Address `json:"to"`
	Value        *big.Int       `json:"value"`
	Data         []byte         `json:"data,omitempty"`
	IsCrossShard bool           `json:"is_cross_shard"`
}

// CrossShardTx represents a cross-shard transaction
// Destinations are derived from RwSet - each RwVariable specifies an address and shard
type CrossShardTx struct {
	ID        string         `json:"id,omitempty"`
	TxHash    common.Hash    `json:"tx_hash,omitempty"`
	FromShard int            `json:"from_shard"`
	From      common.Address `json:"from"`
	To        common.Address `json:"to"`
	Value     *big.Int       `json:"value"`
	Gas       uint64         `json:"gas,omitempty"` // Gas limit for EVM execution
	Data      []byte         `json:"data,omitempty"`
	RwSet     []RwVariable   `json:"rw_set"`  // Target shards/addresses (discovered by simulation)
	Status    TxStatus       `json:"status,omitempty"`
	SimStatus SimulationStatus `json:"sim_status,omitempty"` // Simulation state
	SimError  string         `json:"sim_error,omitempty"`    // Error message if simulation failed
}

// TargetShards returns all unique shard IDs referenced in RwSet
func (tx *CrossShardTx) TargetShards() []int {
	seen := make(map[int]bool)
	var shards []int
	for _, rw := range tx.RwSet {
		if !seen[rw.ReferenceBlock.ShardNum] {
			seen[rw.ReferenceBlock.ShardNum] = true
			shards = append(shards, rw.ReferenceBlock.ShardNum)
		}
	}
	return shards
}

// InvolvedShards returns FromShard + all target shards
func (tx *CrossShardTx) InvolvedShards() []int {
	shards := tx.TargetShards()
	for _, s := range shards {
		if s == tx.FromShard {
			return shards
		}
	}
	return append([]int{tx.FromShard}, shards...)
}

type TxStatus string

const (
	TxPending   TxStatus = "pending"
	TxPrepared  TxStatus = "prepared"
	TxCommitted TxStatus = "committed"
	TxAborted   TxStatus = "aborted"
)

// SimulationStatus represents the state of a cross-shard tx simulation
type SimulationStatus string

const (
	SimPending  SimulationStatus = "pending"  // Queued for simulation
	SimRunning  SimulationStatus = "running"  // Currently simulating
	SimSuccess  SimulationStatus = "success"  // Simulation complete, RwSet discovered
	SimFailed   SimulationStatus = "failed"   // Simulation failed (revert, out of gas, etc.)
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

// LockRequest requests a lock on an address for simulation/2PC
type LockRequest struct {
	TxID    string         `json:"tx_id"`
	Address common.Address `json:"address"`
}

// LockResponse returns locked account state
type LockResponse struct {
	Success  bool                       `json:"success"`
	Error    string                     `json:"error,omitempty"`
	Balance  *big.Int                   `json:"balance,omitempty"`
	Nonce    uint64                     `json:"nonce,omitempty"`
	Code     []byte                     `json:"code,omitempty"`
	CodeHash common.Hash                `json:"code_hash,omitempty"`
	Storage  map[common.Hash]common.Hash `json:"storage,omitempty"` // Full storage dump
}

// UnlockRequest releases a lock on failure
type UnlockRequest struct {
	TxID    string         `json:"tx_id"`
	Address common.Address `json:"address"`
}

// UnlockResponse confirms unlock
type UnlockResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}
