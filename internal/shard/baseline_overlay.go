package shard

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// NewOverlayStateDB creates a new StateDB with RwSet data applied
// This allows baseline protocol to "mock" state from previous hops by injecting
// reads/writes from the RwSet into a fresh StateDB instance.
func NewOverlayStateDB(inner *state.StateDB, rwSet []protocol.RwVariable) (*state.StateDB, error) {
	// Create a new StateDB based on the current root of the inner StateDB
	// This allows us to have a fresh state that we can modify with the RwSet
	// without affecting the original StateDB (since we'll discard this one)
	newState, err := state.New(inner.IntermediateRoot(false), inner.Database())
	if err != nil {
		return nil, err
	}

	// Apply RwSet to the new StateDB
	for _, rw := range rwSet {
		// Apply ReadSet (mock values read from other shards)
		for _, read := range rw.ReadSet {
			newState.SetState(rw.Address, common.Hash(read.Slot), common.BytesToHash(read.Value))
		}

		// Apply WriteSet (mock values written by other shards in previous hops)
		for _, write := range rw.WriteSet {
			newState.SetState(rw.Address, common.Hash(write.Slot), common.BytesToHash(write.NewValue))
		}
	}

	return newState, nil
}