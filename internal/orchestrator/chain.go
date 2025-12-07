package orchestrator

import (
	"log"
	"sync"
	"time"

	"github.com/sharding-experiment/sharding/internal/protocol"
)

// OrchestratorChain maintains the Orchestrator Shard blockchain
type OrchestratorChain struct {
	mu            sync.RWMutex
	blocks        []*protocol.OrchestratorShardBlock
	height        uint64
	pendingTxs    []protocol.CrossShardTx
	awaitingVotes map[string]*protocol.CrossShardTx // txID -> tx (waiting for vote)
	pendingResult map[string]bool                   // txID -> commit decision (for next block)

	// Multi-shard vote aggregation
	votes          map[string]map[int]bool // txID -> shardID -> vote
	expectedVoters map[string][]int        // txID -> list of shard IDs that must vote
}

func NewOrchestratorChain() *OrchestratorChain {
	genesis := &protocol.OrchestratorShardBlock{
		Height:    0,
		PrevHash:  protocol.BlockHash{},
		Timestamp: uint64(time.Now().Unix()),
		TpcResult: map[string]bool{},
		CtToOrder: []protocol.CrossShardTx{},
	}

	return &OrchestratorChain{
		blocks:         []*protocol.OrchestratorShardBlock{genesis},
		height:         0,
		pendingTxs:     []protocol.CrossShardTx{},
		awaitingVotes:  make(map[string]*protocol.CrossShardTx),
		pendingResult:  make(map[string]bool),
		votes:          make(map[string]map[int]bool),
		expectedVoters: make(map[string][]int),
	}
}

// AddTransaction queues a cross-shard tx
func (c *OrchestratorChain) AddTransaction(tx protocol.CrossShardTx) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingTxs = append(c.pendingTxs, tx)
}

// RecordVote records a prepare vote from a State Shard
// Returns true if this vote is accepted (tx was awaiting vote from this shard)
func (c *OrchestratorChain) RecordVote(txID string, shardID int, canCommit bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if we're expecting this vote
	if _, ok := c.awaitingVotes[txID]; !ok {
		return false
	}

	// Initialize votes map for this tx if needed
	if c.votes[txID] == nil {
		c.votes[txID] = make(map[int]bool)
	}

	// Ignore duplicate votes from same shard (first vote wins)
	if _, alreadyVoted := c.votes[txID][shardID]; alreadyVoted {
		log.Printf("OrchestratorChain: Ignoring duplicate vote from shard %d for tx %s", shardID, txID)
		return false
	}

	// Record the vote
	c.votes[txID][shardID] = canCommit
	log.Printf("OrchestratorChain: Recorded vote from shard %d for tx %s: %v", shardID, txID, canCommit)

	// If any vote is NO, immediately abort
	if !canCommit {
		log.Printf("OrchestratorChain: Tx %s received NO vote from shard %d, aborting", txID, shardID)
		c.pendingResult[txID] = false
		delete(c.awaitingVotes, txID)
		delete(c.votes, txID)
		delete(c.expectedVoters, txID)
		return true
	}

	// Check if all expected voters have voted YES
	expected := c.expectedVoters[txID]
	if len(c.votes[txID]) >= len(expected) {
		// Verify all expected shards voted
		allVoted := true
		for _, expectedShardID := range expected {
			if _, voted := c.votes[txID][expectedShardID]; !voted {
				allVoted = false
				break
			}
		}
		if allVoted {
			log.Printf("OrchestratorChain: Tx %s received all %d YES votes, committing", txID, len(expected))
			c.pendingResult[txID] = true
			delete(c.awaitingVotes, txID)
			delete(c.votes, txID)
			delete(c.expectedVoters, txID)
			return true
		}
	}

	// Still waiting for more votes
	log.Printf("OrchestratorChain: Tx %s has %d/%d votes", txID, len(c.votes[txID]), len(expected))
	return true
}

// GetAwaitingTx retrieves a tx awaiting vote (for status updates)
func (c *OrchestratorChain) GetAwaitingTx(txID string) (*protocol.CrossShardTx, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	tx, ok := c.awaitingVotes[txID]
	return tx, ok
}

// ProduceBlock creates next Orchestrator Shard block
func (c *OrchestratorChain) ProduceBlock() *protocol.OrchestratorShardBlock {
	c.mu.Lock()
	defer c.mu.Unlock()

	block := &protocol.OrchestratorShardBlock{
		Height:    c.height + 1,
		PrevHash:  c.blocks[c.height].Hash(),
		Timestamp: uint64(time.Now().Unix()),
		TpcResult: c.pendingResult,
		CtToOrder: c.pendingTxs,
	}

	// Move pending txs to awaiting votes and compute expected voters
	for i := range c.pendingTxs {
		tx := c.pendingTxs[i]
		c.awaitingVotes[tx.ID] = &tx
		// Compute which shards must vote (all involved shards)
		involvedShards := tx.InvolvedShards()
		c.expectedVoters[tx.ID] = involvedShards
		log.Printf("OrchestratorChain: Tx %s expecting votes from shards %v", tx.ID, involvedShards)
	}

	c.blocks = append(c.blocks, block)
	c.height++
	c.pendingTxs = nil
	c.pendingResult = make(map[string]bool)

	return block
}
