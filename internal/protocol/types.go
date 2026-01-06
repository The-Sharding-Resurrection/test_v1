package protocol

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// BigInt wraps big.Int with JSON string support
type BigInt struct {
	*big.Int
}

func NewBigInt(i *big.Int) *BigInt {
	if i == nil {
		return nil
	}
	return &BigInt{i}
}

func (b *BigInt) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		// Try as number
		var n int64
		if err := json.Unmarshal(data, &n); err != nil {
			return fmt.Errorf("BigInt: cannot unmarshal %s", string(data))
		}
		b.Int = big.NewInt(n)
		return nil
	}

	// Handle "0x" prefix for hex
	if len(s) >= 2 && s[:2] == "0x" {
		b.Int = new(big.Int)
		_, ok := b.Int.SetString(s[2:], 16)
		if !ok {
			return fmt.Errorf("BigInt: invalid hex string %s", s)
		}
		return nil
	}

	// Decimal string
	b.Int = new(big.Int)
	_, ok := b.Int.SetString(s, 10)
	if !ok {
		return fmt.Errorf("BigInt: invalid decimal string %s", s)
	}
	return nil
}

func (b BigInt) MarshalJSON() ([]byte, error) {
	if b.Int == nil {
		return []byte("\"0\""), nil
	}
	return json.Marshal(b.Int.String())
}

// ToBigInt returns the underlying *big.Int (nil-safe)
func (b *BigInt) ToBigInt() *big.Int {
	if b == nil || b.Int == nil {
		return big.NewInt(0)
	}
	return b.Int
}

// DeepCopy creates a deep copy of BigInt
func (b *BigInt) DeepCopy() *BigInt {
	if b == nil || b.Int == nil {
		return nil
	}
	return &BigInt{Int: new(big.Int).Set(b.Int)}
}

// HexBytes wraps []byte with hex string JSON support
type HexBytes []byte

func (h *HexBytes) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("HexBytes: expected string, got %s", string(data))
	}
	// Handle empty string
	if s == "" || s == "0x" {
		*h = []byte{}
		return nil
	}
	// Remove 0x prefix if present
	if len(s) >= 2 && s[:2] == "0x" {
		s = s[2:]
	}
	// Decode hex
	decoded, err := hex.DecodeString(s)
	if err != nil {
		return fmt.Errorf("HexBytes: invalid hex %s: %v", s, err)
	}
	*h = decoded
	return nil
}

func (h HexBytes) MarshalJSON() ([]byte, error) {
	return json.Marshal("0x" + hex.EncodeToString(h))
}

// DeepCopy creates a deep copy of HexBytes
func (h HexBytes) DeepCopy() HexBytes {
	if h == nil {
		return nil
	}
	cpy := make(HexBytes, len(h))
	copy(cpy, h)
	return cpy
}

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

// WriteSetItem represents a storage write with old and new values
type WriteSetItem struct {
	Slot     Slot   `json:"slot"`
	OldValue []byte `json:"old_value"` // Value before simulation
	NewValue []byte `json:"new_value"` // Value after simulation
}

// RwVariable represents read/write access to a contract's state
type RwVariable struct {
	Address        common.Address `json:"address"`
	ReferenceBlock Reference      `json:"reference_block"`
	ReadSet        []ReadSetItem  `json:"read_set"`
	WriteSet       []WriteSetItem `json:"write_set"` // Now includes values
}

// TxType identifies the type of transaction operation
type TxType string

const (
	// TxTypeLocal is a normal local EVM execution (default)
	TxTypeLocal TxType = ""
	// TxTypeCrossDebit debits locked funds on source shard (commit phase)
	TxTypeCrossDebit TxType = "cross_debit"
	// TxTypeCrossCredit credits pending amount on destination shard (commit phase)
	TxTypeCrossCredit TxType = "cross_credit"
	// TxTypeCrossWriteSet applies storage writes on destination shard (commit phase)
	TxTypeCrossWriteSet TxType = "cross_writeset"
	// TxTypeCrossAbort cleans up without state change (abort phase)
	TxTypeCrossAbort TxType = "cross_abort"

	// Prepare-phase transaction types (2PC Phase 1) - recorded for crash recovery
	// TxTypePrepareDebit records fund lock on source shard
	TxTypePrepareDebit TxType = "prepare_debit"
	// TxTypePrepareCredit records pending credit on destination shard
	TxTypePrepareCredit TxType = "prepare_credit"
	// TxTypePrepareWriteSet records pending contract call on destination shard
	TxTypePrepareWriteSet TxType = "prepare_writeset"

	// V2 explicit transaction types for strict ordering
	// TxTypeLock acquires state locks for new cross-shard transactions
	TxTypeLock TxType = "lock"
	// TxTypeUnlock releases locks after 2PC commit/abort
	TxTypeUnlock TxType = "unlock"

	// V2 optimistic locking types
	// TxTypeSimError records a failed simulation (for consensus history)
	TxTypeSimError TxType = "sim_error"
	// TxTypeFinalize applies committed WriteSet (explicit finalize)
	TxTypeFinalize TxType = "finalize"
)

// Priority returns execution order for transaction types.
// Lower numbers execute first in block production.
// V2 ordering: Finalize(1) > Unlock(2) > Lock(3) > Local(4) > SimError(5)
func (t TxType) Priority() int {
	switch t {
	// Finalize transactions - apply committed cross-shard state (highest priority)
	case TxTypeCrossDebit, TxTypeCrossCredit, TxTypeCrossWriteSet, TxTypeFinalize:
		return 1
	// Unlock transactions - release locks after commit/abort
	case TxTypeUnlock:
		return 2
	// Lock transactions - acquire locks for new cross-shard txs
	case TxTypeLock:
		return 3
	// Simulation errors - recorded at end of block
	case TxTypeSimError:
		return 5
	// Local and all others (including abort, prepare records) - standard priority
	default:
		return 4
	}
}

// Transaction represents a transaction within a shard
// For local transactions, only the base fields are used
// For cross-shard commit/abort operations, TxType and CrossShardTxID are set
type Transaction struct {
	// Base fields (used by all transaction types)
	ID           string         `json:"id,omitempty"`
	TxHash       common.Hash    `json:"tx_hash,omitempty"`
	From         common.Address `json:"from"`
	To           common.Address `json:"to"`
	Value        *BigInt        `json:"value"`
	Gas          uint64         `json:"gas,omitempty"`
	Data         HexBytes       `json:"data,omitempty"`
	IsCrossShard bool           `json:"is_cross_shard"`

	// Cross-shard operation fields (used when TxType != TxTypeLocal)
	TxType         TxType       `json:"tx_type,omitempty"`            // Operation type
	CrossShardTxID string       `json:"cross_shard_tx_id,omitempty"`  // Links to original CrossShardTx
	RwSet          []RwVariable `json:"rw_set,omitempty"`             // ReadSet/WriteSet for cross-shard ops
	Error          string       `json:"error,omitempty"`              // Error message for TxTypeSimError
}

// DeepCopy creates a deep copy of ReadSetItem
func (r *ReadSetItem) DeepCopy() ReadSetItem {
	// Copy value
	valCopy := make([]byte, len(r.Value))
	copy(valCopy, r.Value)

	// Copy proof
	var proofCopy [][]byte
	if r.Proof != nil {
		proofCopy = make([][]byte, len(r.Proof))
		for k, p := range r.Proof {
			pCopy := make([]byte, len(p))
			copy(pCopy, p)
			proofCopy[k] = pCopy
		}
	}

	return ReadSetItem{
		Slot:  r.Slot,
		Value: valCopy,
		Proof: proofCopy,
	}
}

// DeepCopy creates a deep copy of WriteSetItem
func (w *WriteSetItem) DeepCopy() WriteSetItem {
	oldCopy := make([]byte, len(w.OldValue))
	copy(oldCopy, w.OldValue)
	newCopy := make([]byte, len(w.NewValue))
	copy(newCopy, w.NewValue)

	return WriteSetItem{
		Slot:     w.Slot,
		OldValue: oldCopy,
		NewValue: newCopy,
	}
}

// DeepCopy creates a deep copy of RwVariable
func (rw *RwVariable) DeepCopy() RwVariable {
	var readSetCopy []ReadSetItem
	if rw.ReadSet != nil {
		readSetCopy = make([]ReadSetItem, len(rw.ReadSet))
		for i, item := range rw.ReadSet {
			readSetCopy[i] = item.DeepCopy()
		}
	}

	var writeSetCopy []WriteSetItem
	if rw.WriteSet != nil {
		writeSetCopy = make([]WriteSetItem, len(rw.WriteSet))
		for i, item := range rw.WriteSet {
			writeSetCopy[i] = item.DeepCopy()
		}
	}

	return RwVariable{
		Address:        rw.Address,
		ReferenceBlock: rw.ReferenceBlock,
		ReadSet:        readSetCopy,
		WriteSet:       writeSetCopy,
	}
}

// DeepCopy creates a deep copy of the Transaction
func (tx *Transaction) DeepCopy() Transaction {
	// Deep copy RwSet if present
	var rwSetCopy []RwVariable
	if tx.RwSet != nil {
		rwSetCopy = make([]RwVariable, len(tx.RwSet))
		for i, rw := range tx.RwSet {
			rwSetCopy[i] = rw.DeepCopy()
		}
	}

	return Transaction{
		ID:             tx.ID,
		TxHash:         tx.TxHash,
		From:           tx.From,
		To:             tx.To,
		Value:          tx.Value.DeepCopy(),
		Gas:            tx.Gas,
		Data:           tx.Data.DeepCopy(),
		IsCrossShard:   tx.IsCrossShard,
		TxType:         tx.TxType,
		CrossShardTxID: tx.CrossShardTxID,
		RwSet:          rwSetCopy,
		Error:          tx.Error,
	}
}

// CrossShardTx represents a cross-shard transaction
// Destinations are derived from RwSet - each RwVariable specifies an address and shard
type CrossShardTx struct {
	ID        string           `json:"id,omitempty"`
	TxHash    common.Hash      `json:"tx_hash,omitempty"`
	FromShard int              `json:"from_shard"`
	From      common.Address   `json:"from"`
	To        common.Address   `json:"to"`
	Value     *BigInt          `json:"value"`
	Gas       uint64           `json:"gas,omitempty"` // Gas limit for EVM execution
	Data      HexBytes         `json:"data,omitempty"`
	RwSet     []RwVariable     `json:"rw_set"` // Target shards/addresses (discovered by simulation)
	Status    TxStatus         `json:"status,omitempty"`
	SimStatus SimulationStatus `json:"sim_status,omitempty"` // Simulation state
	SimError  string           `json:"sim_error,omitempty"`  // Error message if simulation failed
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

// DeepCopy creates a deep copy of the CrossShardTx to avoid aliasing
func (tx *CrossShardTx) DeepCopy() *CrossShardTx {
	if tx == nil {
		return nil
	}

	// Deep copy RwSet
	var rwSetCopy []RwVariable
	if tx.RwSet != nil {
		rwSetCopy = make([]RwVariable, len(tx.RwSet))
		for i, rw := range tx.RwSet {
			rwSetCopy[i] = rw.DeepCopy()
		}
	}

	return &CrossShardTx{
		ID:        tx.ID,
		TxHash:    tx.TxHash,
		FromShard: tx.FromShard,
		From:      tx.From,
		To:        tx.To,
		Value:     tx.Value.DeepCopy(),
		Gas:       tx.Gas,
		Data:      tx.Data.DeepCopy(),
		RwSet:     rwSetCopy,
		Status:    tx.Status,
		SimStatus: tx.SimStatus,
		SimError:  tx.SimError,
	}
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
	SimPending SimulationStatus = "pending" // Queued for simulation
	SimRunning SimulationStatus = "running" // Currently simulating
	SimSuccess SimulationStatus = "success" // Simulation complete, RwSet discovered
	SimFailed  SimulationStatus = "failed"  // Simulation failed (revert, out of gas, etc.)
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
	Success  bool                        `json:"success"`
	Error    string                      `json:"error,omitempty"`
	Balance  *big.Int                    `json:"balance,omitempty"`
	Nonce    uint64                      `json:"nonce,omitempty"`
	Code     []byte                      `json:"code,omitempty"`
	CodeHash common.Hash                 `json:"code_hash,omitempty"`
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
