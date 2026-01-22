package shard

import (
	"github.com/ethereum/go-ethereum/common"
)

// AddressToShard maps an address to its home shard using the last byte modulo num shards
func AddressToShard(addr common.Address, numShards int) int {
	return int(addr[len(addr)-1]) % numShards
}
