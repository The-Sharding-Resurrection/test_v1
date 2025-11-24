package orchestrator

import (
	"sync"
	"time"

	"github.com/sharding-experiment/sharding/internal/protocol"
)

// ContractChain maintains the Contract Shard blockchain
type ContractChain struct {
	mu            sync.RWMutex
	blocks        []*protocol.ContractShardBlock
	height        uint64
	pendingTxs    []protocol.CrossShardTransaction
	pendingResult map[string]bool // From previous round
}

func NewContractChain() *ContractChain {
	genesis := &protocol.ContractShardBlock{
		Height:    0,
		PrevHash:  protocol.BlockHash{},
		Timestamp: uint64(time.Now().Unix()),
		TpcResult: map[string]bool{},
		CtToOrder: []protocol.CrossShardTransaction{},
	}

	return &ContractChain{
		blocks:        []*protocol.ContractShardBlock{genesis},
		height:        0,
		pendingTxs:    []protocol.CrossShardTransaction{},
		pendingResult: make(map[string]bool),
	}
}

// AddTransaction queues a cross-shard tx
func (c *ContractChain) AddTransaction(tx protocol.CrossShardTransaction) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingTxs = append(c.pendingTxs, tx)
}

// RecordResult stores 2PC result from previous round
func (c *ContractChain) RecordResult(txID string, committed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingResult[txID] = committed
}

// ProduceBlock creates next Contract Shard block
func (c *ContractChain) ProduceBlock() *protocol.ContractShardBlock {
	c.mu.Lock()
	defer c.mu.Unlock()

	block := &protocol.ContractShardBlock{
		Height:    c.height + 1,
		PrevHash:  c.blocks[c.height].Hash(),
		Timestamp: uint64(time.Now().Unix()),
		TpcResult: c.pendingResult,
		CtToOrder: c.pendingTxs,
	}

	c.blocks = append(c.blocks, block)
	c.height++
	c.pendingTxs = nil
	c.pendingResult = make(map[string]bool)

	return block
}
