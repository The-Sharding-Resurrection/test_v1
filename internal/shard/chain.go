package shard

import (
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// Chain maintains the block chain for a shard
type Chain struct {
	mu         sync.RWMutex
	blocks     []*protocol.StateShardBlock
	height     uint64
	currentTxs []protocol.TxRef
	prepares   map[string]bool // txID -> prepare result
}

func NewChain() *Chain {
	genesis := &protocol.StateShardBlock{
		Height:     0,
		PrevHash:   protocol.BlockHash{},
		Timestamp:  uint64(time.Now().Unix()),
		StateRoot:  common.Hash{},
		TxOrdering: []protocol.TxRef{},
		TpcPrepare: map[string]bool{},
	}

	return &Chain{
		blocks:     []*protocol.StateShardBlock{genesis},
		height:     0,
		currentTxs: []protocol.TxRef{},
		prepares:   make(map[string]bool),
	}
}

// AddTx queues a transaction for next block
func (c *Chain) AddTx(txID string, isCrossShard bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.currentTxs = append(c.currentTxs, protocol.TxRef{
		TxID:         txID,
		IsCrossShard: isCrossShard,
	})
}

// AddPrepareResult records 2PC prepare result
func (c *Chain) AddPrepareResult(txID string, canCommit bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prepares[txID] = canCommit
}

// ProduceBlock creates next block
func (c *Chain) ProduceBlock(stateRoot common.Hash) *protocol.StateShardBlock {
	c.mu.Lock()
	defer c.mu.Unlock()

	block := &protocol.StateShardBlock{
		Height:     c.height + 1,
		PrevHash:   c.blocks[c.height].Hash(),
		Timestamp:  uint64(time.Now().Unix()),
		StateRoot:  stateRoot,
		TxOrdering: c.currentTxs,
		TpcPrepare: c.prepares,
	}

	c.blocks = append(c.blocks, block)
	c.height++
	c.currentTxs = nil
	c.prepares = make(map[string]bool)

	return block
}
