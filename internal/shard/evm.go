package shard

import (
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
	"github.com/sharding-experiment/sharding/config"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// EVMState wraps geth's StateDB with standalone EVM execution
type EVMState struct {
	mu        sync.Mutex // Protects balance operations for atomic check-and-lock
	db        state.Database
	stateDB   *state.StateDB
	chainCfg  *params.ChainConfig
	blockNum  uint64
	timestamp uint64
}

// NewMemoryEVMState creates a new in-memory EVM state (for testing)
// NewEVMState creates a new in-memory EVM state
func NewMemoryEVMState() (*EVMState, error) {
	// In-memory database
	memDB := rawdb.NewMemoryDatabase()
	trieDB := triedb.NewDatabase(memDB, nil)
	db := state.NewDatabase(trieDB, nil)

	// Create state from empty root
	stateDB, err := state.New(types.EmptyRootHash, db)
	if err != nil {
		return nil, err
	}

	// Minimal chain config (Shanghai fork for latest EVM features)
	chainCfg := &params.ChainConfig{
		ChainID:             big.NewInt(1337),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		ShanghaiTime:        new(uint64),
	}

	return &EVMState{
		db:        db,
		stateDB:   stateDB,
		chainCfg:  chainCfg,
		blockNum:  1,
		timestamp: 1700000000,
	}, nil
}

// NewEVMState creates a new in-memory EVM state
func NewEVMState(shardID int) (*EVMState, error) {
	config, err := config.LoadDefault()
	if err != nil {
		return nil, err
	}

	rootPath := filepath.Join(config.StorageDir, fmt.Sprintf("shard%v_root.txt", shardID))
	shardStateRoot, err := os.ReadFile(rootPath)
	if err != nil {
		return nil, err
	}

	rootStr := strings.TrimSpace(string(shardStateRoot))
	if !(len(rootStr) == 66 && (rootStr[:2] == "0x" || rootStr[:2] == "0X")) {
		return nil, fmt.Errorf("invalid state root format: %q", rootStr)
	}

	sdbPath := filepath.Join(config.StorageDir, strconv.Itoa(shardID))
	ldbObject, err := leveldb.New(sdbPath, 128, 1024, "", false)
	if err != nil {
		return nil, err
	}

	rdb := rawdb.NewDatabase(ldbObject)
	tdb := triedb.NewDatabase(rdb, nil)
	sdb := state.NewDatabase(tdb, nil)

	stateDB, err := state.New(common.HexToHash(rootStr), sdb)

	if err != nil {
		return nil, err
	}

	// Minimal chain config (Shanghai fork for latest EVM features)
	chainCfg := &params.ChainConfig{
		ChainID:             big.NewInt(1337),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		ShanghaiTime:        new(uint64),
	}

	return &EVMState{
		db:        sdb,
		stateDB:   stateDB,
		chainCfg:  chainCfg,
		blockNum:  1,
		timestamp: 1700000000,
	}, nil
}

// Commit commits the current state and returns the new root
func (e *EVMState) Commit(blockNum uint64) (common.Hash, error) {
	newStateRoot, err := e.stateDB.Commit(blockNum, false, false)
	if err != nil {
		return common.Hash{}, err
	}

	// Recreate StateDB at the new root so cached tries aren't reused after commit
	newStateDB, err := state.New(newStateRoot, e.stateDB.Database())
	if err != nil {
		log.Printf("Failed to reload StateDB at root %s: %v", newStateRoot.Hex(), err)
		return common.Hash{}, err
	}
	e.stateDB = newStateDB
	return newStateRoot, nil
}

// GetBalance returns account balance
func (e *EVMState) GetBalance(addr common.Address) *big.Int {
	return e.stateDB.GetBalance(addr).ToBig()
}

// GetNonce returns account nonce
func (e *EVMState) GetNonce(addr common.Address) uint64 {
	return e.stateDB.GetNonce(addr)
}

// GetCode returns contract code
func (e *EVMState) GetCode(addr common.Address) []byte {
	return e.stateDB.GetCode(addr)
}

// SetCode sets contract code at an address (for cross-shard deployment)
func (e *EVMState) SetCode(addr common.Address, code []byte) {
	e.stateDB.SetCode(addr, code, 0)
}

// GetCodeHash returns the hash of an account's code
func (e *EVMState) GetCodeHash(addr common.Address) common.Hash {
	return e.stateDB.GetCodeHash(addr)
}

// AccountState represents the full state of an account for locking
type AccountState struct {
	Balance  *big.Int
	Nonce    uint64
	Code     []byte
	CodeHash common.Hash
	// Storage is fetched on-demand via GetStorageAt during simulation
	// For PoC, we don't dump all storage slots here
}

// GetAccountState returns full account state for locking
func (e *EVMState) GetAccountState(addr common.Address) *AccountState {
	return &AccountState{
		Balance:  e.GetBalance(addr),
		Nonce:    e.GetNonce(addr),
		Code:     e.GetCode(addr),
		CodeHash: e.GetCodeHash(addr),
	}
}

// DeployContract deploys a contract and returns its address
func (e *EVMState) DeployContract(deployer common.Address, bytecode []byte, value *big.Int, gas uint64) (common.Address, []byte, uint64, []*types.Log, error) {
	evm := e.newEVM(deployer, value)

	// Create contract - EVM handles nonce and address
	ret, contractAddr, leftOverGas, err := evm.Create(
		deployer,
		bytecode,
		gas,
		uint256.MustFromBig(value),
	)

	if err != nil {
		return common.Address{}, nil, gas - leftOverGas, nil, err
	}

	return contractAddr, ret, gas - leftOverGas, e.stateDB.Logs(), nil
}

// DeployContractTracked deploys a contract and tracks storage writes during constructor
// Returns contract address, return data, gas used, logs, storage writes, and error
func (e *EVMState) DeployContractTracked(deployer common.Address, bytecode []byte, value *big.Int, gas uint64, numShards int) (common.Address, []byte, uint64, []*types.Log, map[common.Hash]common.Hash, error) {
	// Create tracking wrapper to capture storage writes
	trackingDB := NewTrackingStateDB(e.stateDB, 0, numShards) // localShardID doesn't matter for deployment

	// Create EVM with tracking state
	blockCtx := vm.BlockContext{
		CanTransfer: func(db vm.StateDB, addr common.Address, amount *uint256.Int) bool {
			return db.GetBalance(addr).Cmp(amount) >= 0
		},
		Transfer: func(db vm.StateDB, from, to common.Address, amount *uint256.Int) {
			db.SubBalance(from, amount, tracing.BalanceChangeTransfer)
			db.AddBalance(to, amount, tracing.BalanceChangeTransfer)
		},
		GetHash: func(n uint64) common.Hash {
			return common.Hash{}
		},
		Coinbase:    common.Address{},
		GasLimit:    30_000_000,
		BlockNumber: new(big.Int).SetUint64(e.blockNum),
		Time:        e.timestamp,
		Difficulty:  big.NewInt(0),
		BaseFee:     big.NewInt(0),
		Random:      &common.Hash{},
	}

	evm := vm.NewEVM(blockCtx, trackingDB, e.chainCfg, vm.Config{})
	evm.TxContext = vm.TxContext{
		Origin:   deployer,
		GasPrice: big.NewInt(0),
	}

	// Create contract
	ret, contractAddr, leftOverGas, err := evm.Create(
		deployer,
		bytecode,
		gas,
		uint256.MustFromBig(value),
	)

	if err != nil {
		return common.Address{}, nil, gas - leftOverGas, nil, nil, err
	}

	// Get storage writes for the deployed contract
	storageWrites := trackingDB.GetStorageWritesForAddress(contractAddr)

	return contractAddr, ret, gas - leftOverGas, e.stateDB.Logs(), storageWrites, nil
}

// CallContract executes a contract call
func (e *EVMState) CallContract(caller, contract common.Address, input []byte, value *big.Int, gas uint64) ([]byte, uint64, []*types.Log, error) {
	evm := e.newEVM(caller, value)

	ret, leftOverGas, err := evm.Call(
		caller,
		contract,
		input,
		gas,
		uint256.MustFromBig(value),
	)

	return ret, gas - leftOverGas, e.stateDB.Logs(), err
}

// StaticCall executes a read-only call (no state changes)
func (e *EVMState) StaticCall(caller, contract common.Address, input []byte, gas uint64) ([]byte, uint64, error) {
	evm := e.newEVM(caller, big.NewInt(0))

	ret, leftOverGas, err := evm.StaticCall(
		caller,
		contract,
		input,
		gas,
	)

	return ret, gas - leftOverGas, err
}

// GetStateRoot returns current state root (without committing)
func (e *EVMState) GetStateRoot() common.Hash {
	return e.stateDB.IntermediateRoot(false)
}

// GetStorageAt returns storage value at a given slot
func (e *EVMState) GetStorageAt(addr common.Address, slot common.Hash) common.Hash {
	return e.stateDB.GetState(addr, slot)
}

// GetStorageWithProof returns storage value with Merkle proof (V2.3)
// Returns the storage value, state root, account proof, and storage proof
func (e *EVMState) GetStorageWithProof(addr common.Address, slot common.Hash) (*protocol.StorageProofResponse, error) {
	// Get current state root (intermediate root without commit)
	stateRoot := e.GetStateRoot()

	// Get storage value
	value := e.GetStorageAt(addr, slot)

	// Generate account proof (path from state root to account)
	// The account proof proves that the account exists at the state root
	// and includes the account's storage root
	accountProof, storageRoot, err := e.getAccountProof(stateRoot, addr)
	if err != nil {
		return nil, fmt.Errorf("failed to generate account proof: %w", err)
	}

	// Generate storage proof (path from storage root to slot)
	// The storage proof proves that the slot has the given value
	storageProof, err := e.getStorageProof(addr, storageRoot, slot)
	if err != nil {
		return nil, fmt.Errorf("failed to generate storage proof: %w", err)
	}

	return &protocol.StorageProofResponse{
		Address:      addr,
		Slot:         slot,
		Value:        value,
		StateRoot:    stateRoot,
		BlockHeight:  e.blockNum,
		AccountProof: accountProof,
		StorageProof: storageProof,
	}, nil
}

// getAccountProof generates a Merkle proof for an account in the state trie
// Returns the proof and the account's storage root
func (e *EVMState) getAccountProof(stateRoot common.Hash, addr common.Address) ([][]byte, common.Hash, error) {
	// Create a trie at the state root
	tr, err := trie.New(trie.StateTrieID(stateRoot), e.db.TrieDB())
	if err != nil {
		return nil, common.Hash{}, fmt.Errorf("failed to open state trie: %w", err)
	}

	// Generate proof for the account address
	var proof trie.Proofs
	accountKey := crypto.Keccak256(addr.Bytes())
	err = tr.Prove(accountKey, &proof)
	if err != nil {
		return nil, common.Hash{}, fmt.Errorf("failed to prove account: %w", err)
	}

	// Get the account to extract storage root
	// If account doesn't exist, storage root is empty
	storageRoot := common.Hash{}
	account := e.stateDB.GetAccount(addr)
	if account != nil {
		storageRoot = account.Root
	}

	// Convert proof to [][]byte format
	proofBytes := make([][]byte, len(proof))
	for i, p := range proof {
		proofBytes[i] = p
	}

	return proofBytes, storageRoot, nil
}

// getStorageProof generates a Merkle proof for a storage slot in the account's storage trie
func (e *EVMState) getStorageProof(addr common.Address, storageRoot common.Hash, slot common.Hash) ([][]byte, error) {
	// If storage root is empty, the account has no storage
	if storageRoot == (common.Hash{}) || storageRoot == types.EmptyRootHash {
		return [][]byte{}, nil
	}

	// Create a trie at the storage root
	tr, err := trie.New(trie.StorageTrieID(e.GetStateRoot(), crypto.Keccak256Hash(addr.Bytes()), storageRoot), e.db.TrieDB())
	if err != nil {
		return nil, fmt.Errorf("failed to open storage trie: %w", err)
	}

	// Generate proof for the storage slot
	var proof trie.Proofs
	slotKey := crypto.Keccak256(slot.Bytes())
	err = tr.Prove(slotKey, &proof)
	if err != nil {
		return nil, fmt.Errorf("failed to prove storage slot: %w", err)
	}

	// Convert proof to [][]byte format
	proofBytes := make([][]byte, len(proof))
	for i, p := range proof {
		proofBytes[i] = p
	}

	return proofBytes, nil
}

// SetStorageAt sets storage value at a given slot (for applying write sets)
func (e *EVMState) SetStorageAt(addr common.Address, slot common.Hash, value common.Hash) {
	e.stateDB.SetState(addr, slot, value)
}

// newEVM creates a new EVM instance for execution
func (e *EVMState) newEVM(caller common.Address, value *big.Int) *vm.EVM {
	blockCtx := vm.BlockContext{
		CanTransfer: func(db vm.StateDB, addr common.Address, amount *uint256.Int) bool {
			return db.GetBalance(addr).Cmp(amount) >= 0
		},
		Transfer: func(db vm.StateDB, from, to common.Address, amount *uint256.Int) {
			db.SubBalance(from, amount, tracing.BalanceChangeTransfer)
			db.AddBalance(to, amount, tracing.BalanceChangeTransfer)
		},
		GetHash: func(n uint64) common.Hash {
			return common.Hash{} // Simplified
		},
		Coinbase:    common.Address{},
		GasLimit:    30_000_000,
		BlockNumber: new(big.Int).SetUint64(e.blockNum),
		Time:        e.timestamp,
		Difficulty:  big.NewInt(0),
		BaseFee:     big.NewInt(0),
		Random:      &common.Hash{},
	}

	evm := vm.NewEVM(blockCtx, e.stateDB, e.chainCfg, vm.Config{})
	evm.TxContext = vm.TxContext{
		Origin:   caller,
		GasPrice: big.NewInt(0),
	}

	return evm
}

// ExecuteTx executes a transaction based on its TxType.
// For local transactions, runs EVM execution.
// For cross-shard operations, applies the specific state change.
func (e *EVMState) ExecuteTx(tx *protocol.Transaction) error {
	switch tx.TxType {
	case protocol.TxTypeLocal: // TxTypeLocal == "", handles both empty and explicit local
		return e.executeLocalTx(tx)
	case protocol.TxTypeCrossDebit:
		return e.Debit(tx.From, tx.Value.ToBigInt())
	case protocol.TxTypeCrossCredit:
		e.Credit(tx.To, tx.Value.ToBigInt())
		return nil
	case protocol.TxTypeCrossWriteSet:
		return e.applyWriteSet(tx.RwSet)
	case protocol.TxTypeCrossAbort:
		// No-op for state; cleanup happens in chain.cleanupAfterExecution
		return nil
	// V2 transaction types
	case protocol.TxTypeLock:
		return e.executeLock(tx)
	case protocol.TxTypeUnlock:
		// No-op for state; cleanup happens in chain.cleanupAfterExecution
		return nil
	// V2 optimistic locking types
	case protocol.TxTypeFinalize:
		// Apply committed WriteSet from cross-shard transaction
		return e.applyWriteSet(tx.RwSet)
	case protocol.TxTypeSimError:
		// No-op for state; just records simulation failure in block
		return nil
	default:
		return fmt.Errorf("unknown transaction type: %s", tx.TxType)
	}
}

// executeLocalTx handles normal EVM transaction execution.
// Gas should be validated by the caller before reaching this function.
// A fallback gas limit is used only for internal/legacy calls.
func (e *EVMState) executeLocalTx(tx *protocol.Transaction) error {
	value := tx.Value.ToBigInt()
	gas := tx.Gas
	if gas == 0 {
		// Fallback for internal calls - should not happen for user transactions
		// as handleTxSubmit validates and sets gas before queueing
		gas = 3_000_000
	}

	evm := e.newEVM(tx.From, value)

	if len(tx.Data) > 0 {
		// Contract call
		_, gasLeft, err := evm.Call(tx.From, tx.To, tx.Data, gas, uint256.MustFromBig(value))
		if err != nil {
			return fmt.Errorf("contract call failed (gas used: %d): %w", gas-gasLeft, err)
		}
	} else {
		// Simple transfer
		if !evm.Context.CanTransfer(e.stateDB, tx.From, uint256.MustFromBig(value)) {
			return ErrInsufficientFunds
		}
		evm.Context.Transfer(e.stateDB, tx.From, tx.To, uint256.MustFromBig(value))
	}
	return nil
}

// applyWriteSet applies storage writes from a cross-shard transaction's RwSet
func (e *EVMState) applyWriteSet(rwSet []protocol.RwVariable) error {
	for _, rw := range rwSet {
		for _, write := range rw.WriteSet {
			slot := common.Hash(write.Slot)
			newValue := common.BytesToHash(write.NewValue)
			e.SetStorageAt(rw.Address, slot, newValue)
		}
	}
	return nil
}

// executeLock is a no-op for V2 Optimistic Locking.
//
// V2 Optimistic Locking Flow:
// Lock transactions are handled directly in Chain.ProduceBlock using
// validateAndLockReadSetLocked(), which atomically:
// 1. Validates ReadSet values match current state
// 2. Acquires slot-level locks
// 3. Rolls back all locks on any failure
//
// This method exists only for backwards compatibility with ExecuteTx dispatch.
// The actual Lock logic is in chain.go:validateAndLockReadSetLocked.
func (e *EVMState) executeLock(tx *protocol.Transaction) error {
	// V2 Optimistic: Lock handling is done in Chain.ProduceBlock directly
	// This should not be called, but return success if it is
	return nil
}

// Credit adds balance (used for cross-shard receives)
func (e *EVMState) Credit(addr common.Address, amount *big.Int) {
	e.stateDB.AddBalance(addr, uint256.MustFromBig(amount), tracing.BalanceChangeUnspecified)
}

// Snapshot creates a state snapshot for potential rollback
func (e *EVMState) Snapshot() int {
	return e.stateDB.Snapshot()
}

// RevertToSnapshot rolls back state to a previous snapshot
func (e *EVMState) RevertToSnapshot(snapshot int) {
	e.stateDB.RevertToSnapshot(snapshot)
}

// CanDebit checks if an address has sufficient available balance
// Available = balance - lockedAmount
func (e *EVMState) CanDebit(addr common.Address, amount *big.Int, lockedAmount *big.Int) bool {
	balance := e.stateDB.GetBalance(addr).ToBig()
	available := new(big.Int).Sub(balance, lockedAmount)
	return available.Cmp(amount) >= 0
}

// LockFunds for cross-shard (deduct but track in separate map)
// This needs to be coordinated with the Server's lock tracking
func (e *EVMState) Debit(addr common.Address, amount *big.Int) error {
	bal := e.stateDB.GetBalance(addr)
	amtU256 := uint256.MustFromBig(amount)

	if bal.Cmp(amtU256) < 0 {
		return ErrInsufficientFunds
	}

	e.stateDB.SubBalance(addr, amtU256, tracing.BalanceChangeUnspecified)
	return nil
}

// SimulateCall runs a transaction simulation and returns accessed addresses
// This is used to detect if a tx is cross-shard before execution
func (e *EVMState) SimulateCall(caller, contract common.Address, input []byte, value *big.Int, gas uint64, localShardID, numShards int) ([]byte, []common.Address, bool, error) {
	// Create a snapshot to revert after simulation
	snapshot := e.stateDB.Snapshot()

	// Create tracking wrapper
	trackingDB := NewTrackingStateDB(e.stateDB, localShardID, numShards)

	// Create EVM with tracking state
	blockCtx := vm.BlockContext{
		CanTransfer: func(db vm.StateDB, addr common.Address, amount *uint256.Int) bool {
			return db.GetBalance(addr).Cmp(amount) >= 0
		},
		Transfer: func(db vm.StateDB, from, to common.Address, amount *uint256.Int) {
			db.SubBalance(from, amount, tracing.BalanceChangeTransfer)
			db.AddBalance(to, amount, tracing.BalanceChangeTransfer)
		},
		GetHash: func(n uint64) common.Hash {
			return common.Hash{}
		},
		Coinbase:    common.Address{},
		GasLimit:    30_000_000,
		BlockNumber: new(big.Int).SetUint64(e.blockNum),
		Time:        e.timestamp,
		Difficulty:  big.NewInt(0),
		BaseFee:     big.NewInt(0),
		Random:      &common.Hash{},
	}

	evm := vm.NewEVM(blockCtx, trackingDB, e.chainCfg, vm.Config{})
	evm.TxContext = vm.TxContext{
		Origin:   caller,
		GasPrice: big.NewInt(0),
	}

	// Execute call
	ret, _, err := evm.Call(
		caller,
		contract,
		input,
		gas,
		uint256.MustFromBig(value),
	)

	// Revert state changes (simulation only)
	e.stateDB.RevertToSnapshot(snapshot)

	// Return accessed addresses and cross-shard status
	accessedAddrs := trackingDB.GetAccessedAddresses()
	hasCrossShard := trackingDB.HasCrossShardAccess()

	return ret, accessedAddrs, hasCrossShard, err
}

// SimulateCallForRwSet runs a sub-call simulation and returns the RwSet
// V2.2: Used by State Shards to handle RwSetRequest from Orchestrator
func (e *EVMState) SimulateCallForRwSet(caller, contract common.Address, input []byte, value *big.Int, gas uint64, refBlock protocol.Reference) ([]protocol.RwVariable, uint64, error) {
	// Create a snapshot to revert after simulation
	snapshot := e.stateDB.Snapshot()

	// Create tracking wrapper - for local simulation, all addresses are "local"
	// We track reads/writes regardless of shard assignment
	trackingDB := NewTrackingStateDB(e.stateDB, refBlock.ShardNum, NumShards)

	// Create EVM with tracking state
	blockCtx := vm.BlockContext{
		CanTransfer: func(db vm.StateDB, addr common.Address, amount *uint256.Int) bool {
			return db.GetBalance(addr).Cmp(amount) >= 0
		},
		Transfer: func(db vm.StateDB, from, to common.Address, amount *uint256.Int) {
			db.SubBalance(from, amount, tracing.BalanceChangeTransfer)
			db.AddBalance(to, amount, tracing.BalanceChangeTransfer)
		},
		GetHash: func(n uint64) common.Hash {
			return common.Hash{}
		},
		Coinbase:    common.Address{},
		GasLimit:    30_000_000,
		BlockNumber: new(big.Int).SetUint64(refBlock.BlockHeight),
		Time:        e.timestamp,
		Difficulty:  big.NewInt(0),
		BaseFee:     big.NewInt(0),
		Random:      &common.Hash{},
	}

	evm := vm.NewEVM(blockCtx, trackingDB, e.chainCfg, vm.Config{})
	evm.TxContext = vm.TxContext{
		Origin:   caller,
		GasPrice: big.NewInt(0),
	}

	// Execute call
	_, gasLeft, err := evm.Call(
		caller,
		contract,
		input,
		gas,
		uint256.MustFromBig(value),
	)

	gasUsed := gas - gasLeft

	// Revert state changes (simulation only)
	e.stateDB.RevertToSnapshot(snapshot)

	if err != nil {
		return nil, gasUsed, err
	}

	// Build RwSet from tracked reads/writes
	rwSet := trackingDB.BuildRwSet(refBlock)

	return rwSet, gasUsed, nil
}

// Errors
var ErrInsufficientFunds = &EVMError{"insufficient funds"}

type EVMError struct {
	msg string
}

func (e *EVMError) Error() string {
	return e.msg
}
