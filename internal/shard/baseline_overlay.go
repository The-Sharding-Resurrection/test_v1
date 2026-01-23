package shard

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/holiman/uint256"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// ApplyRwSetOverlay applies RwSet data to an existing StateDB
// This allows baseline protocol to "mock" state from previous hops by injecting
// reads/writes from the RwSet into the StateDB instance.
// Caller should likely use Snapshot/Revert to undo these changes after execution.
func ApplyRwSetOverlay(stateDB *state.StateDB, rwSet []protocol.RwVariable) {
	// Apply RwSet to the StateDB
	for _, rw := range rwSet {
		// V2.4: Apply Balance and Nonce (mock account state from other shards)
		if rw.Balance != nil {
			// Reset balance to 0 then add target balance (safest way if SetBalance unavailable)
			current := stateDB.GetBalance(rw.Address)
			if !current.IsZero() {
				stateDB.SubBalance(rw.Address, current, tracing.BalanceChangeUnspecified)
			}
			stateDB.AddBalance(rw.Address, uint256.MustFromBig(rw.Balance.ToBigInt()), tracing.BalanceChangeUnspecified)
		}
		if rw.Nonce != nil {
			stateDB.SetNonce(rw.Address, *rw.Nonce, tracing.NonceChangeUnspecified)
		}

		// Apply ReadSet (mock values read from other shards)
		for _, read := range rw.ReadSet {
			stateDB.SetState(rw.Address, common.Hash(read.Slot), common.BytesToHash(read.Value))
		}

		// Apply WriteSet (mock values written by other shards in previous hops)
		for _, write := range rw.WriteSet {
			stateDB.SetState(rw.Address, common.Hash(write.Slot), common.BytesToHash(write.NewValue))
		}
	}
}