package shard

import (
	"fmt"
	"log"
	"math/big"
	"os"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
	"github.com/sharding-experiment/sharding/config"
)

// EVMState wraps geth's StateDB with standalone EVM execution
type EVMState struct {
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

	shardStateRoot, err := os.ReadFile(fmt.Sprintf("%s/shard%v_root.txt", config.StorageDir, shardID))
	if err != nil {
		return nil, err
	}

	rootStr := strings.TrimSpace(string(shardStateRoot))
	if !(len(rootStr) == 66 && (rootStr[:2] == "0x" || rootStr[:2] == "0X")) {
		return nil, fmt.Errorf("invalid state root format: %q", rootStr)
	}

	ldbObject, err := leveldb.New(config.StorageDir+strconv.Itoa(shardID), 128, 1024, "", false)
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

// Credit adds balance (used for cross-shard receives)
func (e *EVMState) Credit(addr common.Address, amount *big.Int) {
	e.stateDB.AddBalance(addr, uint256.MustFromBig(amount), tracing.BalanceChangeUnspecified)
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

// Errors
var ErrInsufficientFunds = &EVMError{"insufficient funds"}

type EVMError struct {
	msg string
}

func (e *EVMError) Error() string {
	return e.msg
}
