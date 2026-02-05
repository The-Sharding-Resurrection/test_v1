package shard

import (
	"github.com/ethereum/go-ethereum/common"
)

// AddressToShard maps an address to its home shard using the last byte modulo num shards
func AddressToShard(addr common.Address, numShards int) int {
	// Deterministic mapping used by benchmark/workload: high nibble of first byte.
	// This matches cmd/benchmark/main.go addressToShard (first hex digit after 0x).
	// Using the same rule avoids routing txs to the wrong shard and keeps block
	// production from skipping pending transactions.
	if numShards == 0 {
		return 0
	}
	return int(addr[0]>>4) % numShards
}
