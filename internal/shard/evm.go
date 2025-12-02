package shard

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
)

// EVMState wraps geth's StateDB with standalone EVM execution
type EVMState struct {
	db        state.Database
	stateDB   *state.StateDB
	chainCfg  *params.ChainConfig
	blockNum  uint64
	timestamp uint64
}

// NewEVMState creates a new in-memory EVM state
func NewEVMState() (*EVMState, error) {
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

// Errors
var ErrInsufficientFunds = &EVMError{"insufficient funds"}

type EVMError struct {
	msg string
}

func (e *EVMError) Error() string {
	return e.msg
}
