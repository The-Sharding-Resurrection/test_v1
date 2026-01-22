package shard

import "github.com/ethereum/go-ethereum/common"

// slotKey identifies a specific storage slot by address and slot hash.
// Used internally for tracking slot locks during two-phase deletion.
type slotKey struct {
	addr common.Address
	slot common.Hash
}
