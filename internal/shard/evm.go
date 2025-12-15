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
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/params"
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

// ExecuteCrossShardTx executes a committed cross-shard transaction within a block.
// This method handles both source and destination shard execution:
//
// For the SOURCE shard (tx.FromShard == localShardID):
// - Debit the sender's balance
//
// For DESTINATION shards (addresses in RwSet on this shard):
// - For simple value transfers: Credit the receiver (if tx.To is on this shard)
// - For contract calls: Set up temporary state from ReadSet, execute, and clean up
//
// Parameters:
// - tx: The committed cross-shard transaction with RwSet containing ReadSet values
// - localShardID: This shard's ID to identify which addresses are local
// - numShards: Total number of shards for address-to-shard mapping
//
// Returns gas used and error (if any)
func (e *EVMState) ExecuteCrossShardTx(tx *protocol.CrossShardTx, localShardID, numShards int) (uint64, error) {
	var gasUsed uint64

	// Check if this is the source shard
	isSourceShard := tx.FromShard == localShardID

	// Check if tx.To is on this shard
	toShard := int(tx.To[len(tx.To)-1]) % numShards
	isToOnThisShard := toShard == localShardID

	// Case 1: Source shard - debit the sender
	if isSourceShard {
		if err := e.Debit(tx.From, tx.Value.ToBigInt()); err != nil {
			return 0, err
		}
		gasUsed = 21000
	}

	// Case 2: Simple value transfer to this shard - credit the receiver
	if len(tx.Data) == 0 && isToOnThisShard && !isSourceShard {
		// Simple transfer: just credit the receiver
		e.Credit(tx.To, tx.Value.ToBigInt())
		gasUsed = 21000
	}

	// Case 3: Contract call with RwSet entries on this shard
	// Set up temporary state, re-execute to verify WriteSet, and apply results
	if len(tx.Data) > 0 {
		// Check if any RwSet entries are for this shard
		hasLocalRwSet := false
		for _, rw := range tx.RwSet {
			rwShard := int(rw.Address[len(rw.Address)-1]) % numShards
			if rwShard == localShardID {
				hasLocalRwSet = true
				break
			}
		}

		if hasLocalRwSet {
			// Track which addresses we set up temporarily
			tempAddresses := make(map[common.Address]bool)

			// Step 1: Set up temporary state for cross-shard accounts from ReadSet
			for _, rw := range tx.RwSet {
				addrShard := int(rw.Address[len(rw.Address)-1]) % numShards
				if addrShard == localShardID {
					continue // Skip local addresses - they already have state
				}

				// Set up temporary account with ReadSet values
				tempAddresses[rw.Address] = true

				// Apply ReadSet values to storage
				for _, item := range rw.ReadSet {
					slot := common.Hash(item.Slot)
					value := common.BytesToHash(item.Value)
					e.stateDB.SetState(rw.Address, slot, value)
				}
			}

			// Step 2: Execute the transaction through EVM
			gas := tx.Gas
			if gas == 0 {
				gas = 1_000_000 // Default gas for contract calls
			}

			// Note: For source shard executing a contract call, the sender's balance
			// was already debited above. We use the EVM for execution.
			_, gasUsed, _, err := e.CallContract(tx.From, tx.To, tx.Data, tx.Value.ToBigInt(), gas)
			if err != nil {
				// Note: Even on error, we continue because 2PC has already committed
				// In production, this would need proper error handling
				log.Printf("Warning: Contract call execution failed during cross-shard commit: %v", err)
			}

			// Step 3: Clean up temporary state for non-local addresses
			for _, rw := range tx.RwSet {
				if !tempAddresses[rw.Address] {
					continue
				}

				// Clear the temporary ReadSet values we set up
				for _, item := range rw.ReadSet {
					slot := common.Hash(item.Slot)
					// Check if this slot was written during execution
					wasWritten := false
					for _, write := range rw.WriteSet {
						if write.Slot == item.Slot {
							wasWritten = true
							break
						}
					}
					// Only clear if it wasn't written during execution
					if !wasWritten {
						e.stateDB.SetState(rw.Address, slot, common.Hash{})
					}
				}
			}

			return gasUsed, nil
		}
	}

	return gasUsed, nil
}

// Errors
var ErrInsufficientFunds = &EVMError{"insufficient funds"}

type EVMError struct {
	msg string
}

func (e *EVMError) Error() string {
	return e.msg
}
