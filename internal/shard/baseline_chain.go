package shard

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// BaselineChain manages baseline protocol state for a shard
type BaselineChain struct {
	mu          sync.RWMutex
	shardID     int
	numShards   int
	height      uint64                                    // Current block height
	mempool     []*protocol.Transaction                   // Local transactions + PENDING cross-shard txs
	lockedSlots map[common.Address]map[common.Hash]string // addr -> slot -> txID
	pendingTxs  map[string]*protocol.Transaction          // txID -> tx (for re-execution)
}

// NewBaselineChain creates a new baseline chain
func NewBaselineChain(shardID int, numShards int) *BaselineChain {
	return &BaselineChain{
		shardID:     shardID,
		numShards:   numShards,
		height:      0, // Start at height 0, first block will be 1
		mempool:     nil,
		lockedSlots: make(map[common.Address]map[common.Hash]string),
		pendingTxs:  make(map[string]*protocol.Transaction),
	}
}

// AddTransaction adds a transaction to the mempool
func (c *BaselineChain) AddTransaction(tx *protocol.Transaction) {
	c.mu.Lock()
	defer c.mu.Unlock()
	txCopy := tx.DeepCopy()
	c.mempool = append(c.mempool, &txCopy)
}

// ProduceBlock produces a new block with baseline ordering: Unlock → Finalize → Normal → Lock
func (c *BaselineChain) ProduceBlock(evmState *EVMState) (*protocol.StateShardBlock, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var txOrdering []protocol.Transaction
	var lockTxs []protocol.Transaction

	// Process mempool transactions
	for _, tx := range c.mempool {
		if tx.IsCrossShard && tx.CtStatus == protocol.CtStatusPending {
			// Re-execute with RwSet overlay
			success, rwSet, targetShard, err := c.reExecuteWithOverlayLocked(tx, evmState)

			if err != nil {
				// Execution error - mark as FAIL
				log.Printf("Shard %d: Tx %s failed during re-execution: %v", c.shardID, tx.ID, err)
				tx.CtStatus = protocol.CtStatusFail
				tx.Error = err.Error()
				txOrdering = append(txOrdering, tx.DeepCopy())
				continue
			}

			if !success {
				// NoStateError - generate Lock tx and route to target shard
				tx.RwSet = mergeRwSets(tx.RwSet, rwSet)
				tx.TargetShard = targetShard
				tx.CtStatus = protocol.CtStatusPending

				// Generate Lock tx
				lockTx := c.generateLockTxLocked(tx)
				lockTxs = append(lockTxs, lockTx)

				// Store for next round
				txCopy := tx.DeepCopy()
				c.pendingTxs[tx.ID] = &txCopy
			} else {
				// Execution complete - mark as SUCCESS
				tx.RwSet = mergeRwSets(tx.RwSet, rwSet)
				tx.CtStatus = protocol.CtStatusSuccess
				txOrdering = append(txOrdering, tx.DeepCopy())

				// Clean up locks and pending state
				c.unlockSlotsLocked(tx.ID)
				delete(c.pendingTxs, tx.ID)
			}
		} else {
			// Local transaction or finalized cross-shard tx
			txOrdering = append(txOrdering, tx.DeepCopy())
		}
	}

	// Clear mempool
	c.mempool = nil

	// Append Lock txs at the end (baseline ordering: Normal → Lock)
	txOrdering = append(txOrdering, lockTxs...)

	// Increment height for new block
	c.height++

	block := &protocol.StateShardBlock{
		ShardID:    c.shardID,
		Height:     c.height,
		Timestamp:  uint64(time.Now().Unix()),
		TxOrdering: txOrdering,
	}

	return block, nil
}

// reExecuteWithOverlayLocked re-executes a PENDING transaction with RwSet overlay
// Must be called with lock held
func (c *BaselineChain) reExecuteWithOverlayLocked(tx *protocol.Transaction, evmState *EVMState) (success bool, rwSet []protocol.RwVariable, targetShard int, err error) {
	// Create overlay StateDB with RwSet data
	overlayDB := NewOverlayStateDB(evmState.stateDB, tx.RwSet)

	// Temporarily swap StateDB for overlay execution
	originalState := evmState.stateDB
	evmState.stateDB = overlayDB // Use the overlay wrapper (not inner)
	defer func() {
		evmState.stateDB = originalState
	}()

	// Execute with baseline tracer
	success, rwSet, targetShard, err = evmState.ExecuteBaselineTx(tx, c.shardID, c.numShards)
	return
}

// generateLockTxLocked creates a Lock transaction for accessed slots
// Must be called with lock held
func (c *BaselineChain) generateLockTxLocked(tx *protocol.Transaction) protocol.Transaction {
	// Acquire locks for all accessed slots in RwSet
	for _, rw := range tx.RwSet {
		if AddressToShard(rw.Address, c.numShards) != c.shardID {
			continue // Only lock local state
		}

		if c.lockedSlots[rw.Address] == nil {
			c.lockedSlots[rw.Address] = make(map[common.Hash]string)
		}

		// Lock all read slots
		for _, read := range rw.ReadSet {
			slot := common.Hash(read.Slot)
			// Check for lock conflict
			if existingTxID, exists := c.lockedSlots[rw.Address][slot]; exists && existingTxID != tx.ID {
				log.Printf("Shard %d: Lock conflict on address %s slot %s - already locked by %s (requested by %s)",
					c.shardID, rw.Address.Hex(), slot.Hex(), existingTxID, tx.ID)
				// For now, we overwrite (simple baseline - no conflict resolution)
				// Production system would need conflict resolution strategy
			}
			c.lockedSlots[rw.Address][slot] = tx.ID
		}

		// Lock all write slots
		for _, write := range rw.WriteSet {
			slot := common.Hash(write.Slot)
			// Check for lock conflict
			if existingTxID, exists := c.lockedSlots[rw.Address][slot]; exists && existingTxID != tx.ID {
				log.Printf("Shard %d: Lock conflict on address %s slot %s - already locked by %s (requested by %s)",
					c.shardID, rw.Address.Hex(), slot.Hex(), existingTxID, tx.ID)
				// For now, we overwrite (simple baseline - no conflict resolution)
				// Production system would need conflict resolution strategy
			}
			c.lockedSlots[rw.Address][slot] = tx.ID
		}
	}

	return protocol.Transaction{
		ID:             uuid.New().String(),
		TxType:         protocol.TxTypeLock,
		CrossShardTxID: tx.ID,
		IsCrossShard:   true,
	}
}

// unlockSlotsLocked releases locks held by a transaction
// Must be called with lock held
func (c *BaselineChain) unlockSlotsLocked(txID string) {
	for addr, slots := range c.lockedSlots {
		for slot, lockTxID := range slots {
			if lockTxID == txID {
				delete(slots, slot)
			}
		}
		if len(slots) == 0 {
			delete(c.lockedSlots, addr)
		}
	}
}

// ProcessOrchestratorBlock processes an orchestrator block (Phase 3: Feedback)
func (c *BaselineChain) ProcessOrchestratorBlock(orchBlock *protocol.OrchestratorShardBlock) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, tx := range orchBlock.CtToProcess {
		switch tx.CtStatus {
		case protocol.CtStatusSuccess:
			// Generate Finalize + Unlock transactions
			finalizeTx := c.generateFinalizeTxLocked(&tx)
			unlockTx := c.generateUnlockTxLocked(&tx)

			// Add to mempool with priority
			c.mempool = append([]*protocol.Transaction{&unlockTx, &finalizeTx}, c.mempool...)

		case protocol.CtStatusFail:
			// Generate Unlock transaction only
			unlockTx := c.generateUnlockTxLocked(&tx)
			c.mempool = append([]*protocol.Transaction{&unlockTx}, c.mempool...)

		case protocol.CtStatusPending:
			// Route to target shard
			if tx.TargetShard == c.shardID {
				// This shard should process the transaction
				txCopy := tx.DeepCopy()
				c.mempool = append(c.mempool, &txCopy)
			}
		}
	}
}

// generateFinalizeTxLocked creates a Finalize transaction to apply WriteSet
// Must be called with lock held
func (c *BaselineChain) generateFinalizeTxLocked(tx *protocol.Transaction) protocol.Transaction {
	return protocol.Transaction{
		ID:             uuid.New().String(),
		TxType:         protocol.TxTypeFinalize,
		CrossShardTxID: tx.ID,
		RwSet:          tx.RwSet,
		IsCrossShard:   true,
	}
}

// generateUnlockTxLocked creates an Unlock transaction to release locks
// Must be called with lock held
func (c *BaselineChain) generateUnlockTxLocked(tx *protocol.Transaction) protocol.Transaction {
	return protocol.Transaction{
		ID:             uuid.New().String(),
		TxType:         protocol.TxTypeUnlock,
		CrossShardTxID: tx.ID,
		IsCrossShard:   true,
	}
}

// ExecuteTransaction executes a single transaction (called during block production)
func (c *BaselineChain) ExecuteTransaction(evmState *EVMState, tx *protocol.Transaction) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch tx.TxType {
	case protocol.TxTypeLock:
		// Lock transaction - already handled in generateLockTxLocked
		log.Printf("Shard %d: Executed Lock tx for %s", c.shardID, tx.CrossShardTxID)
		return nil

	case protocol.TxTypeUnlock:
		// Unlock transaction
		c.unlockSlotsLocked(tx.CrossShardTxID)
		delete(c.pendingTxs, tx.CrossShardTxID)
		log.Printf("Shard %d: Executed Unlock tx for %s", c.shardID, tx.CrossShardTxID)
		return nil

	case protocol.TxTypeFinalize:
		// Finalize transaction - apply WriteSet
		for _, rw := range tx.RwSet {
			if AddressToShard(rw.Address, c.numShards) != c.shardID {
				continue // Only apply local state
			}

			for _, write := range rw.WriteSet {
				slot := common.Hash(write.Slot)
				value := common.BytesToHash(write.NewValue)
				evmState.stateDB.SetState(rw.Address, slot, value)
			}
		}
		log.Printf("Shard %d: Executed Finalize tx for %s", c.shardID, tx.CrossShardTxID)
		return nil

	default:
		// Local transaction
		if !tx.IsCrossShard {
			// Execute normally
			success, _, _, err := evmState.ExecuteBaselineTx(tx, c.shardID, c.numShards)
			if err != nil {
				return fmt.Errorf("local tx execution failed: %w", err)
			}
			if !success {
				// This shouldn't happen for local txs
				return fmt.Errorf("unexpected NoStateError for local tx")
			}
			log.Printf("Shard %d: Executed local tx %s", c.shardID, tx.ID)
			return nil
		}

		// Cross-shard transaction (handled in ProduceBlock)
		return nil
	}
}
