package shard

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// ApplyRwSetOverlay applies RwSet data to an existing StateDB
// This allows baseline protocol to "mock" state from previous hops by injecting
// reads/writes from the RwSet into the StateDB instance.
// Caller should likely use Snapshot/Revert to undo these changes after execution.
func ApplyRwSetOverlay(stateDB *state.StateDB, rwSet []protocol.RwVariable) {
	// Apply RwSet to the StateDB
	for _, rw := range rwSet {
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