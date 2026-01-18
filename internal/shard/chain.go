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
	pendingCalls   map[string]*protocol.CrossShardTx // txID -> tx data for contract execution on commit

	// V2 Optimistic Locking: Slot-level locks for fine-grained concurrency
	slotLocks map[common.Address]map[common.Hash]string // addr -> slot -> txID
	pendingRwSets  map[string][]protocol.RwVariable          // txID -> RwSet (for finalize)

	// Crash recovery: track orchestrator block processing
	lastOrchestratorHeight uint64            // Last processed orchestrator block height
	processedCommits       map[string]bool   // txID -> true if commit/abort already processed (idempotency)
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
		shardID:                shardID,
		blocks:                 []*protocol.StateShardBlock{genesis},
		height:                 0,
		currentTxs:             []protocol.Transaction{},
		prepares:               make(map[string]bool),
		locked:                 make(map[string]*LockedFunds),
		lockedByAddr:           make(map[common.Address][]*lockedEntry),
		pendingCredits:         make(map[string][]*PendingCredit),
		pendingCalls:           make(map[string]*protocol.CrossShardTx),
		slotLocks:              make(map[common.Address]map[common.Hash]string),
		pendingRwSets:          make(map[string][]protocol.RwVariable),
		lastOrchestratorHeight: 0,
		processedCommits:       make(map[string]bool),
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
	c.lockFundsLocked(txID, addr, amount)
}

// lockFundsLocked is the internal version that requires caller to hold c.mu
func (c *Chain) lockFundsLocked(txID string, addr common.Address, amount *big.Int) {
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
	return c.getLockedAmountForAddrLocked(addr)
}

// getLockedAmountForAddrLocked is internal version that requires caller to hold c.mu
func (c *Chain) getLockedAmountForAddrLocked(addr common.Address) *big.Int {
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
		// V2: Check lock conflicts for local transactions
		// Local txs fail when they try to access state with locked slots
		if err := c.checkLocalTxLockConflict(&tx); err != nil {
			log.Printf("Chain %d: %v", c.shardID, err)
			// Local tx blocked by lock - not included in block, no cleanup needed
			continue
		}

		// V2 Optimistic Locking: Lock transactions use atomic validate-and-lock
		// This ensures validation and lock acquisition happen atomically with rollback on failure
		if tx.TxType == protocol.TxTypeLock {
			// Step 1: For value transfers, validate balance and lock funds
			if tx.Value != nil && tx.Value.ToBigInt().Sign() > 0 {
				amount := tx.Value.ToBigInt()
				lockedAmount := c.getLockedAmountForAddrLocked(tx.From)
				if !evmState.CanDebit(tx.From, amount, lockedAmount) {
					log.Printf("Chain %d: Lock tx %s failed: insufficient balance for %s (need %s, locked %s)",
						c.shardID, tx.CrossShardTxID, tx.From.Hex(), amount.String(), lockedAmount.String())
					c.cleanupAfterFailureLocked(&tx)
					continue
				}
				// Store fund lock (doesn't actually debit - that happens on Finalize/commit)
				c.lockFundsLocked(tx.CrossShardTxID, tx.From, amount)
				log.Printf("Chain %d: Lock tx %s locked funds %s from %s",
					c.shardID, tx.CrossShardTxID, amount.String(), tx.From.Hex())
			}

			// Step 2: Validate ReadSet and acquire slot locks
			if err := c.validateAndLockReadSetLocked(tx.CrossShardTxID, tx.RwSet, evmState); err != nil {
				log.Printf("Chain %d: Lock tx %s failed: %v", c.shardID, tx.CrossShardTxID, err)
				// Rollback fund lock if we acquired it
				c.clearLockLocked(tx.CrossShardTxID)
				c.cleanupAfterFailureLocked(&tx)
				// Failed Lock tx is not included in block
			} else {
				successfulTxs = append(successfulTxs, tx)
				c.cleanupAfterExecutionLocked(&tx)
			}
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

	// Create defensive copies to avoid aliasing internal state
	// This prevents races if the block is serialized/broadcast while internal state changes
	prepareTxsCopy := make([]protocol.Transaction, len(c.prepareTxs))
	copy(prepareTxsCopy, c.prepareTxs)
	preparesCopy := make(map[string]bool, len(c.prepares))
	for k, v := range c.prepares {
		preparesCopy[k] = v
	}

	block := &protocol.StateShardBlock{
		ShardID:    c.shardID,
		Height:     c.height + 1,
		PrevHash:   c.blocks[c.height].Hash(),
		Timestamp:  uint64(time.Now().Unix()),
		StateRoot:  stateRoot,
		TxOrdering: successfulTxs,  // Only include successful transactions (already a local slice)
		PrepareTxs: prepareTxsCopy, // Defensive copy for crash recovery
		TpcPrepare: preparesCopy,   // Defensive copy of prepare votes
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
		// V2 Optimistic Locking: validateAndLockReadSetLocked already acquired slot locks
		// and stored RwSet in pendingRwSets. Just record YES vote here.
		c.prepares[tx.CrossShardTxID] = true
		log.Printf("Chain %d: Lock transaction succeeded for %s, voting YES", c.shardID, tx.CrossShardTxID)

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
// V2 Optimistic Locking: For Lock transactions, validateAndLockReadSetLocked already
// rolled back any partially acquired locks, so we just need to record the NO vote.
// MUST be called with c.mu held (used within ProduceBlock).
func (c *Chain) cleanupAfterFailureLocked(tx *protocol.Transaction) {
	switch tx.TxType {
	case protocol.TxTypeLock:
		// Lock validation/locking failed (e.g., ReadSet mismatch or slot already locked)
		// V2 Optimistic: validateAndLockReadSetLocked already rolled back any partial locks
		// Just record NO vote and clear any leftover metadata
		c.clearAllMetadataLocked(tx.CrossShardTxID)
		c.prepares[tx.CrossShardTxID] = false
		log.Printf("Chain %d: Lock failed for %s, voting NO", c.shardID, tx.CrossShardTxID)
	}
	// Other transaction types don't need special failure cleanup
}

// clearAllMetadataLocked clears all cross-shard transaction metadata (must be called with c.mu held)
// Used by both TxTypeCrossAbort and TxTypeUnlock to ensure consistent cleanup
func (c *Chain) clearAllMetadataLocked(txID string) {
	c.clearLockLocked(txID)
	c.clearPendingCreditLocked(txID)
	c.clearPendingCallLocked(txID)
	// V2 Optimistic: Clear slot locks and pending RwSet
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

// IsAddressLocked checks if any slot at this address is locked by any transaction.
// V2 Optimistic: Uses slot-level locks instead of address-level locks.
func (c *Chain) IsAddressLocked(addr common.Address) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// Check if any slot at this address is locked
	return len(c.slotLocks[addr]) > 0
}

// isAddressLockedLocked checks if address is locked (must be called with c.mu held)
// V2 Optimistic: Uses slot-level locks instead of address-level locks.
func (c *Chain) isAddressLockedLocked(addr common.Address) bool {
	return len(c.slotLocks[addr]) > 0
}

// checkLocalTxLockConflict checks if a local transaction conflicts with locked state.
// V2 Protocol: Local transactions are blocked when they directly access addresses with locked slots.
// Returns error if conflict detected, nil otherwise. MUST be called with c.mu held.
//
// Design Decision: SLOT-LEVEL LOCKING (V2 Optimistic)
// Cross-shard txs lock specific storage slots during Lock tx execution.
// Local txs are blocked if From or To address has ANY locked slots.
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

	// Check From address - if any slots are locked
	if c.isAddressLockedLocked(tx.From) {
		// Find one of the lock holders for error message
		for _, holder := range c.slotLocks[tx.From] {
			return fmt.Errorf("local tx %s blocked: From address %s has slots locked by %s", tx.ID, tx.From.Hex(), holder)
		}
	}

	// Check To address - if any slots are locked
	if c.isAddressLockedLocked(tx.To) {
		// Find one of the lock holders for error message
		for _, holder := range c.slotLocks[tx.To] {
			return fmt.Errorf("local tx %s blocked: To address %s has slots locked by %s", tx.ID, tx.To.Hex(), holder)
		}
	}

	return nil
}

// GetAddressLockHolder returns any txID holding a lock on a slot at this address, or empty string.
// V2 Optimistic: Returns first lock holder found at any slot.
func (c *Chain) GetAddressLockHolder(addr common.Address) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, holder := range c.slotLocks[addr] {
		return holder
	}
	return ""
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
	return c.validateAndLockReadSetLocked(txID, rwSet, evmState)
}

// validateAndLockReadSetLocked is the internal version called with c.mu already held.
// V2 Optimistic Locking: Atomic validate and lock - validates ReadSet values then acquires slot locks.
// Rolls back all acquired locks on any failure for clean error handling.
func (c *Chain) validateAndLockReadSetLocked(txID string, rwSet []protocol.RwVariable, evmState *EVMState) error {
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

// =============================================================================
// Crash Recovery Methods
// =============================================================================

// GetLastOrchestratorHeight returns the height of the last processed orchestrator block.
func (c *Chain) GetLastOrchestratorHeight() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastOrchestratorHeight
}

// SetLastOrchestratorHeight updates the last processed orchestrator block height.
func (c *Chain) SetLastOrchestratorHeight(height uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastOrchestratorHeight = height
}

// IsCommitProcessed checks if a commit/abort has already been processed for a transaction.
// Used for idempotency during crash recovery replay.
func (c *Chain) IsCommitProcessed(txID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.processedCommits[txID]
}

// MarkCommitProcessed marks a transaction's commit/abort as processed.
// Used for idempotency during crash recovery replay.
func (c *Chain) MarkCommitProcessed(txID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.processedCommits[txID] = true
}
