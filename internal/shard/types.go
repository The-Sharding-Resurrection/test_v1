package shard

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// slotKey identifies a specific storage slot by address and slot hash.
// Used internally for tracking slot locks during two-phase deletion.
type slotKey struct {
	addr common.Address
	slot common.Hash
}

// lockedEntry links a txID to its lock amount for address-based lookup.
// Used internally for tracking pending fund locks per address.
type lockedEntry struct {
	txID   string
	amount *big.Int
}
