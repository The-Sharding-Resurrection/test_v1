package shard

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

const NumShards = 8

// ExecuteBaselineTx executes a transaction in baseline mode with NoStateError detection
// Returns:
//   - success: whether execution completed without NoStateError
//   - rwSet: state accesses before NoStateError (if any)
//   - targetShard: shard ID that caused NoStateError (or -1 if success)
//   - err: execution error (revert, out of gas, etc.)
func (e *EVMState) ExecuteBaselineTx(
	tx *protocol.Transaction,
	shardID int,
	numShards int,
) (success bool, rwSet []protocol.RwVariable, targetShard int, err error) {
	// Create tracking StateDB to capture reads/writes
	tracking := NewTrackingStateDB(e.State, shardID, numShards)

	// Create EVM with baseline tracer
	tracer := &baselineTracer{
		localShardID: shardID,
		numShards:    numShards,
	}

	vmConfig := vm.Config{
		Tracer: tracer,
	}

	chainConfig := params.AllEthashProtocolChanges
	blockContext := vm.BlockContext{
		CanTransfer: func(db vm.StateDB, addr common.Address, amount *big.Int) bool {
			return db.GetBalance(addr).Cmp(amount.ToBig()) >= 0
		},
		Transfer: func(db vm.StateDB, sender, recipient common.Address, amount *big.Int) {},
		GetHash:  func(n uint64) common.Hash { return common.Hash{} },
		Coinbase: common.Address{},
		GasLimit: 30000000,
		BlockNumber: big.NewInt(1),
		Time: 0,
		Difficulty: big.NewInt(0),
	}

	txContext := vm.TxContext{
		Origin:   tx.From,
		GasPrice: big.NewInt(0),
	}

	evm := vm.NewEVM(blockContext, txContext, tracking, chainConfig, vmConfig)

	// Execute transaction
	value := tx.Value.ToBigInt()
	data := []byte(tx.Data)
	gas := tx.Gas
	if gas == 0 {
		gas = 10000000 // Default gas limit
	}

	snapshot := tracking.Snapshot()

	// Capture panics from tracer (used to halt execution on cross-shard calls)
	var ret []byte
	var gasLeft uint64
	var execErr error

	defer func() {
		if r := recover(); r != nil {
			// Check if it's a NoStateError panic from the tracer
			if nse, ok := r.(*protocol.NoStateError); ok {
				// Cross-shard call detected - capture RwSet and return
				rwSet = tracking.BuildRwSet(protocol.Reference{ShardNum: shardID})
				success = false
				targetShard = nse.ShardID
				err = nil
				return
			}
			// Re-panic if it's not a NoStateError
			panic(r)
		}
	}()

	ret, gasLeft, execErr = evm.Call(
		vm.AccountRef(tx.From),
		tx.To,
		data,
		gas,
		value,
	)
	_ = ret
	_ = gasLeft

	// Check for execution error
	if execErr != nil {
		tracking.RevertToSnapshot(snapshot)
		return false, nil, -1, execErr
	}

	// Success - build complete RwSet
	rwSet = tracking.BuildRwSet(protocol.Reference{ShardNum: shardID})
	tracking.Finalise(true)
	e.Commit() // Apply state changes

	return true, rwSet, -1, nil
}

// baselineTracer detects cross-shard calls by intercepting CALL/STATICCALL/DELEGATECALL opcodes
type baselineTracer struct {
	localShardID int
	numShards    int
	noStateErr   *protocol.NoStateError
}

// CaptureEnter is called when entering a new call frame
func (t *baselineTracer) CaptureEnter(typ vm.OpCode, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
	// Detect cross-shard calls
	if typ == vm.CALL || typ == vm.STATICCALL || typ == vm.DELEGATECALL {
		targetShard := AddressToShard(to, t.numShards)
		if targetShard != t.localShardID {
			// Cross-shard call detected - panic to halt execution immediately
			// This prevents accessing non-existent state and producing incorrect RwSets
			// The panic is caught by defer/recover in ExecuteBaselineTx
			t.noStateErr = &protocol.NoStateError{
				Address: to,
				Caller:  from,
				Data:    input,
				Value:   value,
				ShardID: targetShard,
			}
			panic(t.noStateErr)
		}
	}
}

// CaptureExit is called when exiting a call frame
func (t *baselineTracer) CaptureExit(output []byte, gasUsed uint64, err error) {}

// CaptureTxStart is called at the start of transaction execution
func (t *baselineTracer) CaptureTxStart(gasLimit uint64) {}

// CaptureTxEnd is called at the end of transaction execution
func (t *baselineTracer) CaptureTxEnd(restGas uint64) {}

// CaptureStart is called at the start of EVM execution
func (t *baselineTracer) CaptureStart(env *vm.EVM, from common.Address, to common.Address, create bool, input []byte, gas uint64, value *big.Int) {
}

// CaptureEnd is called at the end of EVM execution
func (t *baselineTracer) CaptureEnd(output []byte, gasUsed uint64, err error) {}

// CaptureState is called for each opcode
func (t *baselineTracer) CaptureState(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, rData []byte, depth int, err error) {
}

// CaptureFault is called when a fault occurs
func (t *baselineTracer) CaptureFault(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, depth int, err error) {
}

// OnOpcode is called for each opcode (legacy interface)
func (t *baselineTracer) OnOpcode(pc uint64, op byte, gas, cost uint64, scope *vm.ScopeContext, rData []byte, depth int, err error) error {
	return nil
}

// OnEnter is called when entering a call (legacy interface)
func (t *baselineTracer) OnEnter(depth int, typ byte, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
	opCode := vm.OpCode(typ)
	t.CaptureEnter(opCode, from, to, input, gas, value)
}

// OnExit is called when exiting a call (legacy interface)
func (t *baselineTracer) OnExit(depth int, output []byte, gasUsed uint64, err error, reverted bool) {
	t.CaptureExit(output, gasUsed, err)
}

// OnTxStart is called at transaction start (legacy interface)
func (t *baselineTracer) OnTxStart(env *vm.EVM, tx *vm.Transaction, from common.Address) {
	if tx != nil {
		t.CaptureTxStart(tx.Gas())
	}
}

// OnTxEnd is called at transaction end (legacy interface)
func (t *baselineTracer) OnTxEnd(receipt *vm.Receipt, err error) {
	if receipt != nil {
		t.CaptureTxEnd(receipt.GasUsed)
	}
}

// OnBlockStart is called at block start (legacy interface)
func (t *baselineTracer) OnBlockStart(env *vm.EVM, block *vm.Block, statedb vm.StateDB) {}

// OnBlockEnd is called at block end (legacy interface)
func (t *baselineTracer) OnBlockEnd(err error) {}

// OnSkippedBlock is called for skipped blocks (legacy interface)
func (t *baselineTracer) OnSkippedBlock(env *vm.EVM, block *vm.Block) error {
	return nil
}

// OnGenesisBlock is called for genesis block (legacy interface)
func (t *baselineTracer) OnGenesisBlock(b *vm.Block, alloc map[common.Address]vm.Account) error {
	return nil
}

// OnBalanceChange is called when balance changes (legacy interface)
func (t *baselineTracer) OnBalanceChange(a common.Address, prev, new *big.Int, reason vm.BalanceChangeReason) {
}

// OnNonceChange is called when nonce changes (legacy interface)
func (t *baselineTracer) OnNonceChange(a common.Address, prev, new uint64) {}

// OnCodeChange is called when code changes (legacy interface)
func (t *baselineTracer) OnCodeChange(a common.Address, prevCodeHash common.Hash, prev []byte, codeHash common.Hash, code []byte) {
}

// OnStorageChange is called when storage changes (legacy interface)
func (t *baselineTracer) OnStorageChange(a common.Address, k, prev, new common.Hash) {}

// OnLog is called for log events (legacy interface)
func (t *baselineTracer) OnLog(log *vm.Log) {}

// OnGasChange is called when gas changes (legacy interface)
func (t *baselineTracer) OnGasChange(old, new uint64, reason vm.GasChangeReason) {}

var _ vm.EVMLogger = (*baselineTracer)(nil)

// mergeRwSets combines two RwSets, with new values overwriting old ones
func mergeRwSets(base, new []protocol.RwVariable) []protocol.RwVariable {
	// Index base RwSet by address
	baseMap := make(map[common.Address]*protocol.RwVariable)
	for i := range base {
		baseMap[base[i].Address] = &base[i]
	}

	// Merge new RwSet
	for _, newRw := range new {
		if existing, ok := baseMap[newRw.Address]; ok {
			// Merge ReadSet (union)
			readMap := make(map[protocol.Slot]bool)
			for _, r := range existing.ReadSet {
				readMap[r.Slot] = true
			}
			for _, r := range newRw.ReadSet {
				if !readMap[r.Slot] {
					existing.ReadSet = append(existing.ReadSet, r)
					readMap[r.Slot] = true
				}
			}

			// Merge WriteSet (new overwrites old)
			writeMap := make(map[protocol.Slot]protocol.WriteSetItem)
			for _, w := range existing.WriteSet {
				writeMap[w.Slot] = w
			}
			for _, w := range newRw.WriteSet {
				writeMap[w.Slot] = w
			}
			existing.WriteSet = nil
			for _, w := range writeMap {
				existing.WriteSet = append(existing.WriteSet, w)
			}
		} else {
			// New address - add it
			baseMap[newRw.Address] = &newRw
		}
	}

	// Convert map back to slice
	result := make([]protocol.RwVariable, 0, len(baseMap))
	for _, rw := range baseMap {
		result = append(result, *rw)
	}
	return result
}

