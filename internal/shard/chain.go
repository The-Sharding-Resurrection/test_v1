package shard

import (
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/sharding-experiment/sharding/internal/protocol"
)

// LockedFunds represents escrowed funds for a pending 2PC transaction
type LockedFunds struct {
	Address common.Address
	Amount  *big.Int
}

// PendingCredit represents a credit waiting for commit
type PendingCredit struct {
	Address common.Address
	Amount  *big.Int
}

// Chain maintains the block chain for a shard and 2PC state
type Chain struct {
	mu             sync.RWMutex
	blocks         []*protocol.StateShardBlock
	height         uint64
	currentTxs     []protocol.TxRef
	prepares       map[string]bool           // txID -> prepare result (for block)
	locked         map[string]*LockedFunds   // txID -> escrowed funds (for abort)
	pendingCredits map[string]*PendingCredit // txID -> credit to apply on commit
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
		blocks:         []*protocol.StateShardBlock{genesis},
		height:         0,
		currentTxs:     []protocol.TxRef{},
		prepares:       make(map[string]bool),
		locked:         make(map[string]*LockedFunds),
		pendingCredits: make(map[string]*PendingCredit),
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

// LockFunds records escrowed funds for a 2PC transaction
func (c *Chain) LockFunds(txID string, addr common.Address, amount *big.Int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.locked[txID] = &LockedFunds{
		Address: addr,
		Amount:  new(big.Int).Set(amount),
	}
}

// GetLockedFunds retrieves locked funds for a transaction
func (c *Chain) GetLockedFunds(txID string) (*LockedFunds, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	lock, ok := c.locked[txID]
	return lock, ok
}

// ClearLock removes locked funds record (on commit or abort)
func (c *Chain) ClearLock(txID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.locked, txID)
}

// StorePendingCredit stores a pending credit for destination shard
func (c *Chain) StorePendingCredit(txID string, addr common.Address, amount *big.Int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingCredits[txID] = &PendingCredit{
		Address: addr,
		Amount:  new(big.Int).Set(amount),
	}
}

// GetPendingCredit retrieves a pending credit
func (c *Chain) GetPendingCredit(txID string) (*PendingCredit, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	credit, ok := c.pendingCredits[txID]
	return credit, ok
}

// ClearPendingCredit removes a pending credit record
func (c *Chain) ClearPendingCredit(txID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pendingCredits, txID)
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
