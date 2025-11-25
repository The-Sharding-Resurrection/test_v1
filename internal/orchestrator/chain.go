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
	pendingTxs    []protocol.CrossShardTx
	awaitingVotes map[string]*protocol.CrossShardTx // txID -> tx (waiting for vote)
	pendingResult map[string]bool                   // txID -> commit decision (for next block)
}

func NewContractChain() *ContractChain {
	genesis := &protocol.ContractShardBlock{
		Height:    0,
		PrevHash:  protocol.BlockHash{},
		Timestamp: uint64(time.Now().Unix()),
		TpcResult: map[string]bool{},
		CtToOrder: []protocol.CrossShardTx{},
	}

	return &ContractChain{
		blocks:        []*protocol.ContractShardBlock{genesis},
		height:        0,
		pendingTxs:    []protocol.CrossShardTx{},
		awaitingVotes: make(map[string]*protocol.CrossShardTx),
		pendingResult: make(map[string]bool),
	}
}

// AddTransaction queues a cross-shard tx
func (c *ContractChain) AddTransaction(tx protocol.CrossShardTx) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingTxs = append(c.pendingTxs, tx)
}

// RecordVote records a prepare vote from a State Shard
// Returns true if this completes the voting (tx was awaiting vote)
func (c *ContractChain) RecordVote(txID string, canCommit bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if we're expecting this vote
	if _, ok := c.awaitingVotes[txID]; !ok {
		return false
	}

	// Vote received - move to pending result for next block
	c.pendingResult[txID] = canCommit
	delete(c.awaitingVotes, txID)
	return true
}

// GetAwaitingTx retrieves a tx awaiting vote (for status updates)
func (c *ContractChain) GetAwaitingTx(txID string) (*protocol.CrossShardTx, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	tx, ok := c.awaitingVotes[txID]
	return tx, ok
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

	// Move pending txs to awaiting votes
	for i := range c.pendingTxs {
		tx := c.pendingTxs[i]
		c.awaitingVotes[tx.ID] = &tx
	}

	c.blocks = append(c.blocks, block)
	c.height++
	c.pendingTxs = nil
	c.pendingResult = make(map[string]bool)

	return block
}
