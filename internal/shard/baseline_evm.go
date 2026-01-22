package shard

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

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
	tracking := NewTrackingStateDB(e.stateDB, shardID, numShards)

	// Define hooks for cross-shard call detection
	hooks := &tracing.Hooks{
		OnEnter: func(depth int, typ byte, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
			opCode := vm.OpCode(typ)
			// Detect cross-shard calls
			if opCode == vm.CALL || opCode == vm.STATICCALL || opCode == vm.DELEGATECALL {
				targetShard := AddressToShard(to, numShards)
				if targetShard != shardID {
					// Cross-shard call detected - panic to halt execution immediately
					// This prevents accessing non-existent state and producing incorrect RwSets
					// The panic is caught by defer/recover in ExecuteBaselineTx
					nse := &protocol.NoStateError{
						Address: to,
						Caller:  from,
						Data:    input,
						Value:   value,
						ShardID: targetShard,
					}
					panic(nse)
				}
			}
		},
	}

	vmConfig := vm.Config{
		Tracer: hooks,
	}

	chainConfig := params.AllEthashProtocolChanges
	blockContext := vm.BlockContext{
		CanTransfer: func(db vm.StateDB, addr common.Address, amount *uint256.Int) bool {
			return db.GetBalance(addr).Cmp(amount) >= 0
		},
		Transfer: func(db vm.StateDB, sender, recipient common.Address, amount *uint256.Int) {
			db.SubBalance(sender, amount, tracing.BalanceChangeTransfer)
			db.AddBalance(recipient, amount, tracing.BalanceChangeTransfer)
		},
		GetHash:     func(n uint64) common.Hash { return common.Hash{} },
		Coinbase:    common.Address{},
		GasLimit:    30000000,
		BlockNumber: big.NewInt(1),
		Time:        0,
		Difficulty:  big.NewInt(0),
		BaseFee:     big.NewInt(0),
		Random:      &common.Hash{},
	}

	txContext := vm.TxContext{
		Origin:   tx.From,
		GasPrice: big.NewInt(0),
	}

	evm := vm.NewEVM(blockContext, tracking, chainConfig, vmConfig)
	evm.TxContext = txContext

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
		tx.From,
		tx.To,
		data,
		gas,
		uint256.MustFromBig(value),
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
	return true, rwSet, -1, nil
}

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
