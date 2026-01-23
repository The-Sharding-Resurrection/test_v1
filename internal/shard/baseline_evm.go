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
	simulate bool,
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

	// Check if the destination address is on a different shard
	destShard := AddressToShard(tx.To, numShards)
	if destShard != shardID {
		// This is a cross-shard transaction starting explicitly with a remote address.
		// We process the sender's side (nonce bump, balance deduction) and return pending status.

		// 1. Check balance
		amount := uint256.MustFromBig(value)
		if tracking.GetBalance(tx.From).Cmp(amount) < 0 {
			return false, nil, -1, vm.ErrInsufficientBalance
		}

		// 2. Apply changes to Sender
		// Increment Nonce
		nonce := tracking.GetNonce(tx.From)
		tracking.SetNonce(tx.From, nonce+1, tracing.NonceChangeUnspecified)

		// Deduct Value
		tracking.SubBalance(tx.From, amount, tracing.BalanceChangeTransfer)

		// 3. Build RwSet
		rwSet = tracking.BuildRwSet(protocol.Reference{ShardNum: shardID})

		// V2.4 Fix: Ensure tx.From has the correct balance (Original Balance)
		// The local tracking DB has the deducted balance (correct for local locking),
		// but the RwSet sent to the target must have the original balance so the
		// EVM execution on the target shard (which will also try to deduct) can succeed.
		// We add the value back to the balance in the RwSet.
		foundSender := false
		for i := range rwSet {
			if rwSet[i].Address == tx.From {
				if rwSet[i].Balance != nil {
					rwSet[i].Balance.Int.Add(rwSet[i].Balance.Int, value)
				}
				foundSender = true
				break
			}
		}

		if !foundSender {
			// If tx.From was not in rwSet (unlikely as we modified it), we must add it.
			// Retrieve current (deducted) balance and add value back.
			currentBal := tracking.GetBalance(tx.From).ToBig()
			originalBal := new(big.Int).Add(currentBal, value)
			nonce := tracking.GetNonce(tx.From)

			rwSet = append(rwSet, protocol.RwVariable{
				Address:        tx.From,
				ReferenceBlock: protocol.Reference{ShardNum: shardID},
				Balance:        protocol.NewBigInt(originalBal),
				Nonce:          &nonce,
			})
		}

		// 4. Return as PENDING (success=false simulates NoStateError behavior for flow control)
		return false, rwSet, destShard, nil
	}

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
	if !simulate {
		tracking.Finalise(true)
	}
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

			// Merge Balance and Nonce (new overwrites old)
			if newRw.Balance != nil {
				existing.Balance = newRw.Balance
			}
			if newRw.Nonce != nil {
				existing.Nonce = newRw.Nonce
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
