package shard

import (
	"log"
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

// SimulationLockTTL is the maximum duration a simulation lock can be held
// If a lock expires before 2PC completes, the transaction will be aborted
const SimulationLockTTL = 2 * time.Minute

// SimulationLock holds locked account state for simulation/2PC
// This is the unified lock used for both simulation and 2PC prepare
type SimulationLock struct {
	TxID      string
	Address   common.Address
	Balance   *big.Int
	Nonce     uint64
	Code      []byte
	CodeHash  common.Hash
	Storage   map[common.Hash]common.Hash
	CreatedAt time.Time
}

// Chain maintains the block chain for a shard and 2PC state
type Chain struct {
	mu             sync.RWMutex
	shardID        int                               // This shard's ID
	blocks         []*protocol.StateShardBlock
	height         uint64
	currentTxs     []protocol.Transaction
	prepares       map[string]bool                   // txID -> prepare result (for block)
	locked         map[string]*LockedFunds           // txID -> reserved funds
	lockedByAddr   map[common.Address][]*lockedEntry // address -> list of locks (for available balance)
	pendingCredits map[string][]*PendingCredit        // txID -> list of credits to apply on commit
	pendingCalls   map[string]*protocol.CrossShardTx // txID -> tx data for contract execution on commit
	simLocks       map[string]map[common.Address]*SimulationLock // txID -> addr -> lock (for simulation)
	simLocksByAddr map[common.Address]string                     // addr -> txID (for lock checking)
}

// lockedEntry links a txID to its lock for address-based lookup
type lockedEntry struct {
	txID   string
	amount *big.Int
}

func NewChain(shardID int) *Chain {
	genesis := &protocol.StateShardBlock{
		ShardID:    shardID,
		Height:     0,
		PrevHash:   protocol.BlockHash{},
		Timestamp:  uint64(time.Now().Unix()),
		StateRoot:  common.Hash{},
		TxOrdering: []protocol.Transaction{},
		TpcPrepare: map[string]bool{},
	}

	return &Chain{
		shardID:        shardID,
		blocks:         []*protocol.StateShardBlock{genesis},
		height:         0,
		currentTxs:     []protocol.Transaction{},
		prepares:       make(map[string]bool),
		locked:         make(map[string]*LockedFunds),
		lockedByAddr:   make(map[common.Address][]*lockedEntry),
		pendingCredits: make(map[string][]*PendingCredit),
		pendingCalls:   make(map[string]*protocol.CrossShardTx),
		simLocks:       make(map[string]map[common.Address]*SimulationLock),
		simLocksByAddr: make(map[common.Address]string),
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
// Multiple calls for the same txID will append to the list of credits
func (c *Chain) StorePendingCredit(txID string, addr common.Address, amount *big.Int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingCredits[txID] = append(c.pendingCredits[txID], &PendingCredit{
		Address: addr,
		Amount:  new(big.Int).Set(amount),
	})
}

// GetPendingCredits retrieves all pending credits for a transaction
func (c *Chain) GetPendingCredits(txID string) ([]*PendingCredit, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	credits, ok := c.pendingCredits[txID]
	return credits, ok
}

// ClearPendingCredit removes a pending credit record
func (c *Chain) ClearPendingCredit(txID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pendingCredits, txID)
}

// StorePendingCall stores transaction data for contract execution on commit
func (c *Chain) StorePendingCall(tx *protocol.CrossShardTx) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pendingCalls[tx.ID] = tx
}

// GetPendingCall retrieves pending call data for a transaction
func (c *Chain) GetPendingCall(txID string) (*protocol.CrossShardTx, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	tx, ok := c.pendingCalls[txID]
	return tx, ok
}

// ClearPendingCall removes a pending call record
func (c *Chain) ClearPendingCall(txID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pendingCalls, txID)
}

// ProduceBlock creates next block
func (c *Chain) ProduceBlock() *protocol.StateShardBlock {
	c.mu.Lock()
	defer c.mu.Unlock()

	block := &protocol.StateShardBlock{
		ShardID:    c.shardID,
		Height:     c.height + 1,
		PrevHash:   c.blocks[c.height].Hash(),
		Timestamp:  uint64(time.Now().Unix()),
		TxOrdering: c.currentTxs,
		TpcPrepare: c.prepares,
	}

	c.blocks = append(c.blocks, block)
	c.height++
	c.currentTxs = nil
	c.prepares = make(map[string]bool)

	return block
}

// LockAddress acquires a simulation lock on an address for a transaction
// Returns error if address is already locked by another transaction
// The lock contains full account state at lock time
func (c *Chain) LockAddress(txID string, addr common.Address, balance *big.Int, nonce uint64, code []byte, codeHash common.Hash, storage map[common.Hash]common.Hash) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if address is already locked
	if existingTxID, ok := c.simLocksByAddr[addr]; ok {
		if existingTxID != txID {
			return &AddressLockedError{Address: addr, LockedBy: existingTxID}
		}
		// Same transaction already holds the lock - no-op
		return nil
	}

	// Create new lock with copied data
	storageCopy := make(map[common.Hash]common.Hash)
	for k, v := range storage {
		storageCopy[k] = v
	}
	codeCopy := make([]byte, len(code))
	copy(codeCopy, code)

	lock := &SimulationLock{
		TxID:      txID,
		Address:   addr,
		Balance:   new(big.Int).Set(balance),
		Nonce:     nonce,
		Code:      codeCopy,
		CodeHash:  codeHash,
		Storage:   storageCopy,
		CreatedAt: time.Now(),
	}

	// Initialize tx locks map if needed
	if c.simLocks[txID] == nil {
		c.simLocks[txID] = make(map[common.Address]*SimulationLock)
	}
	c.simLocks[txID][addr] = lock
	c.simLocksByAddr[addr] = txID

	return nil
}

// AddressLockedError indicates an address is already locked
type AddressLockedError struct {
	Address  common.Address
	LockedBy string
}

func (e *AddressLockedError) Error() string {
	return "address " + e.Address.Hex() + " is locked by transaction " + e.LockedBy
}

// UnlockAddress releases a simulation lock on an address
func (c *Chain) UnlockAddress(txID string, addr common.Address) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Verify the lock belongs to this transaction
	if existingTxID, ok := c.simLocksByAddr[addr]; ok && existingTxID == txID {
		delete(c.simLocksByAddr, addr)
		// Remove from tx's lock map
		if c.simLocks[txID] != nil {
			delete(c.simLocks[txID], addr)
			// Clean up if no more locks for this tx
			if len(c.simLocks[txID]) == 0 {
				delete(c.simLocks, txID)
			}
		}
	}
}

// UnlockAllForTx releases all simulation locks held by a transaction
func (c *Chain) UnlockAllForTx(txID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Find and remove all addresses locked by this tx
	for addr, lockedTxID := range c.simLocksByAddr {
		if lockedTxID == txID {
			delete(c.simLocksByAddr, addr)
		}
	}
	delete(c.simLocks, txID)
}

// IsAddressLocked checks if an address is currently locked
func (c *Chain) IsAddressLocked(addr common.Address) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.simLocksByAddr[addr]
	return ok
}

// GetSimulationLocks retrieves all simulation locks for a transaction
func (c *Chain) GetSimulationLocks(txID string) (map[common.Address]*SimulationLock, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	locks, ok := c.simLocks[txID]
	return locks, ok
}

// GetSimulationLockByAddr retrieves the simulation lock on an address
func (c *Chain) GetSimulationLockByAddr(addr common.Address) (*SimulationLock, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	txID, ok := c.simLocksByAddr[addr]
	if !ok {
		return nil, false
	}
	locks, ok := c.simLocks[txID]
	if !ok {
		return nil, false
	}
	lock, ok := locks[addr]
	return lock, ok
}

// CleanupExpiredLocks removes simulation locks that have exceeded their TTL
func (c *Chain) CleanupExpiredLocks() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	expiredTxs := make([]string, 0)

	// Find expired transactions
	for txID, locks := range c.simLocks {
		for _, lock := range locks {
			if now.Sub(lock.CreatedAt) >= SimulationLockTTL {
				expiredTxs = append(expiredTxs, txID)
				break // Only need to find one expired lock per tx
			}
		}
	}

	// Clean up expired transactions
	for _, txID := range expiredTxs {
		// Remove from simLocksByAddr
		if locks, ok := c.simLocks[txID]; ok {
			for addr := range locks {
				delete(c.simLocksByAddr, addr)
			}
		}
		// Remove from simLocks
		delete(c.simLocks, txID)
	}

	return len(expiredTxs)
}

// StartLockCleanup starts a background goroutine that periodically cleans up expired locks
func (c *Chain) StartLockCleanup(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			if count := c.CleanupExpiredLocks(); count > 0 {
				log.Printf("Chain: Cleaned up %d expired simulation locks", count)
			}
		}
	}()
}
