package shard

import (
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/sharding-experiment/sharding/internal/protocol"
)

// LockedFunds represents reserved funds for a pending 2PC transaction
// In lock-only 2PC, funds are locked (reserved) but not debited until commit
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
	currentTxs     []protocol.Transaction
	prepares       map[string]bool                   // txID -> prepare result (for block)
	locked         map[string]*LockedFunds           // txID -> reserved funds
	lockedByAddr   map[common.Address][]*lockedEntry // address -> list of locks (for available balance)
	pendingCredits map[string]*PendingCredit         // txID -> credit to apply on commit
}

// lockedEntry links a txID to its lock for address-based lookup
type lockedEntry struct {
	txID   string
	amount *big.Int
}

func NewChain() *Chain {
	genesis := &protocol.StateShardBlock{
		Height:     0,
		PrevHash:   protocol.BlockHash{},
		Timestamp:  uint64(time.Now().Unix()),
		StateRoot:  common.Hash{},
		TxOrdering: []protocol.Transaction{},
		TpcPrepare: map[string]bool{},
	}

	return &Chain{
		blocks:         []*protocol.StateShardBlock{genesis},
		height:         0,
		currentTxs:     []protocol.Transaction{},
		prepares:       make(map[string]bool),
		locked:         make(map[string]*LockedFunds),
		lockedByAddr:   make(map[common.Address][]*lockedEntry),
		pendingCredits: make(map[string]*PendingCredit),
	}
}

// AddTx queues a transaction for next block
func (c *Chain) AddTx(tx protocol.Transaction) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.currentTxs = append(c.currentTxs, tx)
}

// AddPrepareResult records 2PC prepare result
func (c *Chain) AddPrepareResult(txID string, canCommit bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prepares[txID] = canCommit
}

// LockFunds reserves funds for a 2PC transaction (lock-only, no debit)
func (c *Chain) LockFunds(txID string, addr common.Address, amount *big.Int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	amountCopy := new(big.Int).Set(amount)
	c.locked[txID] = &LockedFunds{
		Address: addr,
		Amount:  amountCopy,
	}

	// Add to address-based index for available balance calculation
	c.lockedByAddr[addr] = append(c.lockedByAddr[addr], &lockedEntry{
		txID:   txID,
		amount: amountCopy,
	})
}

// GetLockedFunds retrieves locked funds for a transaction
func (c *Chain) GetLockedFunds(txID string) (*LockedFunds, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	lock, ok := c.locked[txID]
	return lock, ok
}

// GetLockedAmountForAddress returns total locked amount for an address
func (c *Chain) GetLockedAmountForAddress(addr common.Address) *big.Int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	total := big.NewInt(0)
	for _, entry := range c.lockedByAddr[addr] {
		total.Add(total, entry.amount)
	}
	return total
}

// ClearLock removes locked funds record (on commit or abort)
func (c *Chain) ClearLock(txID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Get the lock to find the address
	lock, ok := c.locked[txID]
	if !ok {
		return
	}

	// Remove from address-based index
	entries := c.lockedByAddr[lock.Address]
	for i, entry := range entries {
		if entry.txID == txID {
			// Remove by swapping with last and truncating
			entries[i] = entries[len(entries)-1]
			c.lockedByAddr[lock.Address] = entries[:len(entries)-1]
			break
		}
	}

	// Clean up empty address entry
	if len(c.lockedByAddr[lock.Address]) == 0 {
		delete(c.lockedByAddr, lock.Address)
	}

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
