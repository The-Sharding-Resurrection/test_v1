package shard

import (
	"fmt"
	"log"
	"math/big"
	"sort"
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
	shardID        int // This shard's ID
	blocks         []*protocol.StateShardBlock
	height         uint64
	currentTxs     []protocol.Transaction
	prepareTxs     []protocol.Transaction                        // Prepare ops to include in next block (for recovery)
	prepares       map[string]bool                               // txID -> prepare result (for block)
	locked         map[string]*LockedFunds                       // txID -> reserved funds
	lockedByAddr   map[common.Address][]*lockedEntry             // address -> list of locks (for available balance)
	pendingCredits map[string][]*PendingCredit                   // txID -> list of credits to apply on commit
	pendingCalls   map[string]*protocol.CrossShardTx             // txID -> tx data for contract execution on commit
	simLocks       map[string]map[common.Address]*SimulationLock // txID -> addr -> lock (for simulation)
	simLocksByAddr map[common.Address]string                     // addr -> txID (for lock checking)

	// V2 Optimistic Locking: Slot-level locks for fine-grained concurrency
	slotLocks      map[common.Address]map[common.Hash]string // addr -> slot -> txID
	pendingRwSets  map[string][]protocol.RwVariable          // txID -> RwSet (for finalize)
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
		slotLocks:      make(map[common.Address]map[common.Hash]string),
		pendingRwSets:  make(map[string][]protocol.RwVariable),
	}
}

// AddTx queues a transaction for next block
func (c *Chain) AddTx(tx protocol.Transaction) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Deep copy to avoid aliasing caller's data
	c.currentTxs = append(c.currentTxs, tx.DeepCopy())
}

// RecordPrepareTx records a prepare operation for inclusion in the next block.
// This is for audit trail and crash recovery - the actual execution already happened.
// The transaction will be included in PrepareTxs field of the produced block.
//
// Valid TxTypes: TxTypePrepareDebit, TxTypePrepareCredit, TxTypePrepareWriteSet
func (c *Chain) RecordPrepareTx(tx protocol.Transaction) {
	// Validate TxType
	validTypes := map[protocol.TxType]bool{
		protocol.TxTypePrepareDebit:    true,
		protocol.TxTypePrepareCredit:   true,
		protocol.TxTypePrepareWriteSet: true,
	}
	if !validTypes[tx.TxType] {
		log.Printf("WARNING: Chain %d: Invalid prepare TxType '%s' for tx %s", c.shardID, tx.TxType, tx.CrossShardTxID)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Deep copy to avoid aliasing caller's data
	c.prepareTxs = append(c.prepareTxs, tx.DeepCopy())
}

// AddPrepareResult records 2PC prepare result
func (c *Chain) AddPrepareResult(txID string, canCommit bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prepares[txID] = canCommit
}

// sortTransactionsByPriority orders transactions for V2 block production.
// V2 ordering: Finalize(1) > Unlock(2) > Lock(3) > Local(4)
// Uses stable sort to preserve FIFO order within same priority.
func (c *Chain) sortTransactionsByPriority(txs []protocol.Transaction) []protocol.Transaction {
	sorted := make([]protocol.Transaction, len(txs))
	copy(sorted, txs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].TxType.Priority() < sorted[j].TxType.Priority()
	})
	return sorted
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
	if !ok || lock == nil {
		return nil, ok
	}
	// Return a copy to avoid aliasing internal data
	return &LockedFunds{
		Address: lock.Address,
		Amount:  new(big.Int).Set(lock.Amount),
	}, true
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
	if !ok || credits == nil {
		return nil, ok
	}
	// Return a copy to avoid aliasing internal slice
	result := make([]*PendingCredit, len(credits))
	for i, credit := range credits {
		result[i] = &PendingCredit{
			Address: credit.Address,
			Amount:  new(big.Int).Set(credit.Amount),
		}
	}
	return result, ok
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
	// Store a copy to avoid aliasing caller's data
	c.pendingCalls[tx.ID] = tx.DeepCopy()
}

// GetPendingCall retrieves pending call data for a transaction
func (c *Chain) GetPendingCall(txID string) (*protocol.CrossShardTx, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	tx, ok := c.pendingCalls[txID]
	if !ok || tx == nil {
		return nil, ok
	}
	// Return a deep copy to avoid aliasing internal data
	return tx.DeepCopy(), true
}

// ClearPendingCall removes a pending call record
func (c *Chain) ClearPendingCall(txID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pendingCalls, txID)
}

// ProduceBlock executes pending transactions, commits state, and creates the next block.
// This is atomic - holds the lock for the entire operation to prevent race conditions.
// Failed transactions are reverted and excluded from the block.
// Cross-shard operations (debit/credit/writeset/abort) are cleaned up after successful execution.
//
// V2: Transactions are sorted by priority before execution:
// Finalize(1) > Unlock(2) > Lock(3) > Local(4)
func (c *Chain) ProduceBlock(evmState *EVMState) (*protocol.StateShardBlock, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// V2: Sort transactions by priority before execution
	sortedTxs := c.sortTransactionsByPriority(c.currentTxs)

	// Execute transactions in priority order, reverting failed ones
	successfulTxs := make([]protocol.Transaction, 0, len(sortedTxs))
	for _, tx := range sortedTxs {
		// V2: Check lock conflicts for local transactions (pessimistic locking)
		// Local txs fail when they try to access locked state variables
		if err := c.checkLocalTxLockConflict(&tx); err != nil {
			log.Printf("Chain %d: %v", c.shardID, err)
			// Local tx blocked by lock - not included in block, no cleanup needed
			continue
		}

		// Snapshot before execution so we can revert on failure
		snapshot := evmState.Snapshot()
		if err := evmState.ExecuteTx(&tx); err != nil {
			log.Printf("Chain %d: Failed to execute tx %s: %v (reverted)", c.shardID, tx.ID, err)
			evmState.RevertToSnapshot(snapshot)
			// Handle cleanup for failed cross-shard transactions
			c.cleanupAfterFailureLocked(&tx)
			// Failed tx is not included in block
		} else {
			successfulTxs = append(successfulTxs, tx)
			// Cleanup metadata for cross-shard operations after successful execution
			c.cleanupAfterExecutionLocked(&tx)
		}
	}

	// Commit state and get new root
	stateRoot, err := evmState.Commit(c.height + 1)
	if err != nil {
		return nil, err
	}

	block := &protocol.StateShardBlock{
		ShardID:    c.shardID,
		Height:     c.height + 1,
		PrevHash:   c.blocks[c.height].Hash(),
		Timestamp:  uint64(time.Now().Unix()),
		StateRoot:  stateRoot,
		TxOrdering: successfulTxs,  // Only include successful transactions
		PrepareTxs: c.prepareTxs,   // Include prepare operations for crash recovery
		TpcPrepare: c.prepares,
	}

	c.blocks = append(c.blocks, block)
	c.height++
	c.currentTxs = nil
	c.prepareTxs = nil // Clear prepare txs for next block
	c.prepares = make(map[string]bool)

	return block, nil
}

// cleanupAfterExecutionLocked removes metadata for completed cross-shard operations.
// MUST be called with c.mu held (used within ProduceBlock).
func (c *Chain) cleanupAfterExecutionLocked(tx *protocol.Transaction) {
	if tx.CrossShardTxID == "" {
		return // Local tx, no cleanup needed
	}

	switch tx.TxType {
	case protocol.TxTypeCrossDebit:
		// Source shard: clear the fund lock
		c.clearLockLocked(tx.CrossShardTxID)
		log.Printf("Chain %d: Cleared lock for %s", c.shardID, tx.CrossShardTxID)

	case protocol.TxTypeCrossCredit:
		// Dest shard: clear this specific pending credit
		// Note: Multiple credits may exist for same CrossShardTxID
		c.clearPendingCreditForAddressLocked(tx.CrossShardTxID, tx.To)
		log.Printf("Chain %d: Cleared pending credit for %s to %s",
			c.shardID, tx.CrossShardTxID, tx.To.Hex())

	case protocol.TxTypeCrossWriteSet:
		// Dest shard: clear the pending call
		c.clearPendingCallLocked(tx.CrossShardTxID)
		log.Printf("Chain %d: Cleared pending call for %s", c.shardID, tx.CrossShardTxID)

	case protocol.TxTypeCrossAbort:
		// Abort: clear ALL metadata for this cross-shard tx
		c.clearAllMetadataLocked(tx.CrossShardTxID)
		log.Printf("Chain %d: Aborted and cleared all metadata for %s",
			c.shardID, tx.CrossShardTxID)

	// V2 transaction types
	case protocol.TxTypeLock:
		// Lock validation succeeded - record YES vote
		// Metadata (simLocks, locked funds) is already set during prepare phase
		c.prepares[tx.CrossShardTxID] = true

		// V2: Acquire slot-level locks and store RwSet for finalization
		for _, rw := range tx.RwSet {
			for _, item := range rw.ReadSet {
				slot := common.Hash(item.Slot)
				c.lockSlotLocked(tx.CrossShardTxID, rw.Address, slot)
			}
		}
		c.pendingRwSets[tx.CrossShardTxID] = tx.RwSet
		log.Printf("Chain %d: Lock transaction succeeded for %s, voting YES (slot locks acquired)", c.shardID, tx.CrossShardTxID)

	case protocol.TxTypeFinalize:
		// V2: WriteSet was already applied in ExecuteTx
		// Clear slot locks but keep other metadata until Unlock
		c.unlockAllSlotsForTxLocked(tx.CrossShardTxID)
		c.clearPendingRwSetLocked(tx.CrossShardTxID)
		log.Printf("Chain %d: Finalized WriteSet for %s", c.shardID, tx.CrossShardTxID)

	case protocol.TxTypeUnlock:
		// Release all locks and metadata for this cross-shard tx
		// V2: Also release slot locks and clear pending RwSet
		c.unlockAllSlotsForTxLocked(tx.CrossShardTxID)
		c.clearPendingRwSetLocked(tx.CrossShardTxID)
		c.clearAllMetadataLocked(tx.CrossShardTxID)
		log.Printf("Chain %d: Unlocked all for %s (including slot locks)", c.shardID, tx.CrossShardTxID)
	}
}

// cleanupAfterFailureLocked handles cleanup when a transaction fails execution.
// For Lock transactions, this releases locks acquired during prepare phase.
// MUST be called with c.mu held (used within ProduceBlock).
func (c *Chain) cleanupAfterFailureLocked(tx *protocol.Transaction) {
	switch tx.TxType {
	case protocol.TxTypeLock:
		// Lock validation failed (e.g., ReadSet mismatch)
		// Release the locks that were acquired during prepare phase
		// V2: Also release any slot-level locks and pending RwSet
		c.unlockAllSlotsForTxLocked(tx.CrossShardTxID)
		c.clearPendingRwSetLocked(tx.CrossShardTxID)
		c.clearAllMetadataLocked(tx.CrossShardTxID)
		log.Printf("Chain %d: Lock validation failed for %s, released all locks (including slot locks)",
			c.shardID, tx.CrossShardTxID)

		// Note: The orchestrator needs to be notified of this failure.
		// This happens through the TpcPrepare vote - the shard will vote NO
		// because the transaction wasn't successfully executed.
		c.prepares[tx.CrossShardTxID] = false
	}
	// Other transaction types don't need special failure cleanup
}

// clearAllMetadataLocked clears all cross-shard transaction metadata (must be called with c.mu held)
// Used by both TxTypeCrossAbort and TxTypeUnlock to ensure consistent cleanup
func (c *Chain) clearAllMetadataLocked(txID string) {
	c.clearLockLocked(txID)
	c.clearPendingCreditLocked(txID)
	c.clearPendingCallLocked(txID)
	c.clearSimulationLocksLocked(txID)
	// V2.4: Also clear slot locks and pending RwSet to prevent memory leaks
	c.unlockAllSlotsForTxLocked(txID)
	c.clearPendingRwSetLocked(txID)
}

// clearLockLocked clears the fund lock (must be called with c.mu held)
func (c *Chain) clearLockLocked(txID string) {
	lock, ok := c.locked[txID]
	if !ok {
		return
	}

	// Remove from lockedByAddr
	entries := c.lockedByAddr[lock.Address]
	for i, e := range entries {
		if e.txID == txID {
			c.lockedByAddr[lock.Address] = append(entries[:i], entries[i+1:]...)
			break
		}
	}
	if len(c.lockedByAddr[lock.Address]) == 0 {
		delete(c.lockedByAddr, lock.Address)
	}

	delete(c.locked, txID)
}

// clearPendingCreditLocked clears all pending credits for a txID (must be called with c.mu held)
func (c *Chain) clearPendingCreditLocked(txID string) {
	delete(c.pendingCredits, txID)
}

// clearPendingCreditForAddressLocked clears a specific credit by address (for multiple credits per tx)
func (c *Chain) clearPendingCreditForAddressLocked(txID string, addr common.Address) {
	credits, ok := c.pendingCredits[txID]
	if !ok {
		return
	}

	// Filter out the credit for this address
	remaining := make([]*PendingCredit, 0, len(credits))
	for _, credit := range credits {
		if credit.Address != addr {
			remaining = append(remaining, credit)
		}
	}

	if len(remaining) == 0 {
		delete(c.pendingCredits, txID)
	} else {
		c.pendingCredits[txID] = remaining
	}
}

// clearPendingCallLocked clears the pending call (must be called with c.mu held)
func (c *Chain) clearPendingCallLocked(txID string) {
	delete(c.pendingCalls, txID)
}

// clearSimulationLocksLocked clears all simulation locks for a txID (must be called with c.mu held)
func (c *Chain) clearSimulationLocksLocked(txID string) {
	// Get all addresses locked by this txID
	addrLocks, ok := c.simLocks[txID]
	if !ok {
		return
	}

	// Remove from simLocksByAddr index
	for addr := range addrLocks {
		if c.simLocksByAddr[addr] == txID {
			delete(c.simLocksByAddr, addr)
		}
	}

	// Remove the lock entry
	delete(c.simLocks, txID)
}

// IsAddressLocked checks if an address is locked by any transaction.
// V2: Local transactions should fail if they try to access locked state.
func (c *Chain) IsAddressLocked(addr common.Address) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, locked := c.simLocksByAddr[addr]
	return locked
}

// isAddressLockedLocked checks if address is locked (must be called with c.mu held)
func (c *Chain) isAddressLockedLocked(addr common.Address) bool {
	_, locked := c.simLocksByAddr[addr]
	return locked
}

// checkLocalTxLockConflict checks if a local transaction conflicts with locked state.
// V2 Protocol: Local transactions are blocked when they directly access locked addresses.
// Returns error if conflict detected, nil otherwise. MUST be called with c.mu held.
//
// Design Decision: ADDRESS-LEVEL LOCKING
// We use address-level granularity (not storage slot level) for simplicity:
// - Cross-shard txs lock entire addresses via LockAddress()
// - Local txs are blocked if From or To address is locked
//
// KNOWN LIMITATION (see GitHub issue #25):
// This only checks direct From/To addresses, not nested contract calls.
// If local tx calls contract B, and B internally calls locked contract A,
// the conflict won't be detected here, potentially causing state corruption.
// Fix: TX-ID based lock tagging to detect nested calls during EVM execution.
func (c *Chain) checkLocalTxLockConflict(tx *protocol.Transaction) error {
	// Only check local transactions (empty TxType or explicit TxTypeLocal)
	if tx.TxType != protocol.TxTypeLocal && tx.TxType != "" {
		return nil
	}

	// Check From address
	if c.isAddressLockedLocked(tx.From) {
		holder := c.simLocksByAddr[tx.From]
		return fmt.Errorf("local tx %s blocked: From address %s locked by %s", tx.ID, tx.From.Hex(), holder)
	}

	// Check To address
	if c.isAddressLockedLocked(tx.To) {
		holder := c.simLocksByAddr[tx.To]
		return fmt.Errorf("local tx %s blocked: To address %s locked by %s", tx.ID, tx.To.Hex(), holder)
	}

	return nil
}

// GetAddressLockHolder returns the txID holding the lock on an address, or empty string if unlocked.
func (c *Chain) GetAddressLockHolder(addr common.Address) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.simLocksByAddr[addr]
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

// GetSimulationLocks retrieves all simulation locks for a transaction
func (c *Chain) GetSimulationLocks(txID string) (map[common.Address]*SimulationLock, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	locks, ok := c.simLocks[txID]
	if !ok || locks == nil {
		return nil, ok
	}
	// Return a deep copy to avoid aliasing internal map
	result := make(map[common.Address]*SimulationLock, len(locks))
	for addr, lock := range locks {
		storageCopy := make(map[common.Hash]common.Hash, len(lock.Storage))
		for k, v := range lock.Storage {
			storageCopy[k] = v
		}
		codeCopy := make([]byte, len(lock.Code))
		copy(codeCopy, lock.Code)
		result[addr] = &SimulationLock{
			TxID:      lock.TxID,
			Address:   lock.Address,
			Balance:   new(big.Int).Set(lock.Balance),
			Nonce:     lock.Nonce,
			Code:      codeCopy,
			CodeHash:  lock.CodeHash,
			Storage:   storageCopy,
			CreatedAt: lock.CreatedAt,
		}
	}
	return result, ok
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
	if !ok {
		return nil, false
	}
	// Return a copy to avoid aliasing internal data
	storageCopy := make(map[common.Hash]common.Hash, len(lock.Storage))
	for k, v := range lock.Storage {
		storageCopy[k] = v
	}
	codeCopy := make([]byte, len(lock.Code))
	copy(codeCopy, lock.Code)
	return &SimulationLock{
		TxID:      lock.TxID,
		Address:   lock.Address,
		Balance:   new(big.Int).Set(lock.Balance),
		Nonce:     lock.Nonce,
		Code:      codeCopy,
		CodeHash:  lock.CodeHash,
		Storage:   storageCopy,
		CreatedAt: lock.CreatedAt,
	}, true
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

// =============================================================================
// V2 Optimistic Locking: Slot-level locking and ReadSet/WriteSet operations
// =============================================================================

// SlotLockError indicates a storage slot is already locked by another transaction
type SlotLockError struct {
	Address  common.Address
	Slot     common.Hash
	LockedBy string
}

func (e *SlotLockError) Error() string {
	return fmt.Sprintf("slot %s at %s is locked by transaction %s",
		e.Slot.Hex(), e.Address.Hex(), e.LockedBy)
}

// ReadSetMismatchError indicates a ReadSet value doesn't match current state
type ReadSetMismatchError struct {
	Address  common.Address
	Slot     common.Hash
	Expected []byte
	Actual   common.Hash
}

func (e *ReadSetMismatchError) Error() string {
	return fmt.Sprintf("ReadSet mismatch at %s slot %s: expected %x, got %s",
		e.Address.Hex(), e.Slot.Hex(), e.Expected, e.Actual.Hex())
}

// LockSlot acquires a slot-level lock for a transaction
func (c *Chain) LockSlot(txID string, addr common.Address, slot common.Hash) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lockSlotLocked(txID, addr, slot)
}

// lockSlotLocked acquires a slot lock (must be called with c.mu held)
func (c *Chain) lockSlotLocked(txID string, addr common.Address, slot common.Hash) error {
	// Initialize address map if needed
	if c.slotLocks[addr] == nil {
		c.slotLocks[addr] = make(map[common.Hash]string)
	}

	// Check if already locked
	if existingTxID, ok := c.slotLocks[addr][slot]; ok {
		if existingTxID != txID {
			return &SlotLockError{Address: addr, Slot: slot, LockedBy: existingTxID}
		}
		// Same transaction already holds the lock
		return nil
	}

	c.slotLocks[addr][slot] = txID
	return nil
}

// UnlockSlot releases a slot-level lock
func (c *Chain) UnlockSlot(txID string, addr common.Address, slot common.Hash) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unlockSlotLocked(txID, addr, slot)
}

// unlockSlotLocked releases a slot lock (must be called with c.mu held)
func (c *Chain) unlockSlotLocked(txID string, addr common.Address, slot common.Hash) {
	if c.slotLocks[addr] == nil {
		return
	}

	// Only unlock if this transaction holds the lock
	if c.slotLocks[addr][slot] == txID {
		delete(c.slotLocks[addr], slot)
		// Clean up empty address map
		if len(c.slotLocks[addr]) == 0 {
			delete(c.slotLocks, addr)
		}
	}
}

// UnlockAllSlotsForTx releases all slot locks held by a transaction
func (c *Chain) UnlockAllSlotsForTx(txID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unlockAllSlotsForTxLocked(txID)
}

// unlockAllSlotsForTxLocked releases all slot locks for a tx (must be called with c.mu held)
func (c *Chain) unlockAllSlotsForTxLocked(txID string) {
	for addr, slots := range c.slotLocks {
		for slot, holder := range slots {
			if holder == txID {
				delete(slots, slot)
			}
		}
		if len(slots) == 0 {
			delete(c.slotLocks, addr)
		}
	}
}

// IsSlotLocked checks if a specific slot is locked
func (c *Chain) IsSlotLocked(addr common.Address, slot common.Hash) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.slotLocks[addr] == nil {
		return false
	}
	_, locked := c.slotLocks[addr][slot]
	return locked
}

// GetSlotLockHolder returns the txID holding the lock on a slot, or empty string if unlocked
func (c *Chain) GetSlotLockHolder(addr common.Address, slot common.Hash) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.slotLocks[addr] == nil {
		return ""
	}
	return c.slotLocks[addr][slot]
}

// ValidateAndLockReadSet validates that ReadSet values match current state and acquires locks.
// This is the core optimistic concurrency control validation.
// Returns nil if validation passes and locks are acquired, error otherwise.
func (c *Chain) ValidateAndLockReadSet(txID string, rwSet []protocol.RwVariable, evmState *EVMState) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Track all slots we lock so we can rollback on failure
	type lockEntry struct {
		addr common.Address
		slot common.Hash
	}
	var lockedSlots []lockEntry

	// Validate and lock each slot in ReadSet
	for _, rw := range rwSet {
		for _, item := range rw.ReadSet {
			slot := common.Hash(item.Slot)

			// Get current value from EVM state
			currentValue := evmState.GetStorageAt(rw.Address, slot)

			// Compare with expected value from ReadSet
			expectedValue := common.BytesToHash(item.Value)
			if currentValue != expectedValue {
				// Rollback any locks we acquired
				for _, entry := range lockedSlots {
					c.unlockSlotLocked(txID, entry.addr, entry.slot)
				}
				return &ReadSetMismatchError{
					Address:  rw.Address,
					Slot:     slot,
					Expected: item.Value,
					Actual:   currentValue,
				}
			}

			// Try to acquire lock on this slot
			if err := c.lockSlotLocked(txID, rw.Address, slot); err != nil {
				// Rollback any locks we acquired
				for _, entry := range lockedSlots {
					c.unlockSlotLocked(txID, entry.addr, entry.slot)
				}
				return err
			}
			lockedSlots = append(lockedSlots, lockEntry{addr: rw.Address, slot: slot})
		}
	}

	// Store RwSet for later finalization
	c.pendingRwSets[txID] = rwSet

	return nil
}

// ApplyWriteSet applies the WriteSet from a committed cross-shard transaction.
// This is called during the Finalize phase after all shards have voted YES.
func (c *Chain) ApplyWriteSet(txID string, evmState *EVMState) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	rwSet, ok := c.pendingRwSets[txID]
	if !ok {
		return fmt.Errorf("no pending RwSet found for transaction %s", txID)
	}

	// Apply each write in the WriteSet
	for _, rw := range rwSet {
		for _, item := range rw.WriteSet {
			slot := common.Hash(item.Slot)
			newValue := common.BytesToHash(item.NewValue)
			evmState.SetStorageAt(rw.Address, slot, newValue)
			log.Printf("Chain %d: Applied WriteSet for %s: slot %s = %s",
				c.shardID, txID, slot.Hex(), newValue.Hex())
		}
	}

	return nil
}

// ClearPendingRwSet removes the stored RwSet for a transaction (after commit or abort)
func (c *Chain) ClearPendingRwSet(txID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clearPendingRwSetLocked(txID)
}

// clearPendingRwSetLocked removes pending RwSet (must be called with c.mu held)
func (c *Chain) clearPendingRwSetLocked(txID string) {
	delete(c.pendingRwSets, txID)
}

// GetPendingRwSet retrieves the stored RwSet for a transaction
func (c *Chain) GetPendingRwSet(txID string) ([]protocol.RwVariable, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rwSet, ok := c.pendingRwSets[txID]
	if !ok {
		return nil, false
	}
	// Return a deep copy to avoid aliasing
	result := make([]protocol.RwVariable, len(rwSet))
	for i, rw := range rwSet {
		result[i] = rw.DeepCopy()
	}
	return result, true
}
