package orchestrator

import (
	"fmt"
	"log"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// Simulator runs cross-shard transaction simulations in a background worker
type Simulator struct {
	mu          sync.RWMutex
	fetcher     *StateFetcher
	queue       chan *simulationJob
	results     map[string]*SimulationResult
	onSuccess   func(tx protocol.CrossShardTx)    // Callback when simulation succeeds
	onError     func(tx protocol.CrossShardTx)    // Callback when simulation fails (V2)
	chainConfig *params.ChainConfig
	vmConfig    vm.Config
	numShards   int // V2.2: Total number of shards for address mapping
}

type simulationJob struct {
	tx protocol.CrossShardTx
}

// SimulationResult holds the outcome of a simulation
type SimulationResult struct {
	TxID    string
	Status  protocol.SimulationStatus
	RwSet   []protocol.RwVariable
	GasUsed uint64
	Error   string
}

// NewSimulator creates a new simulation worker
func NewSimulator(fetcher *StateFetcher, onSuccess func(tx protocol.CrossShardTx)) *Simulator {
	s := &Simulator{
		fetcher:   fetcher,
		queue:     make(chan *simulationJob, 100),
		results:   make(map[string]*SimulationResult),
		onSuccess: onSuccess,
		numShards: NumShards, // V2.2: Use constant from statedb.go
		chainConfig: &params.ChainConfig{
			ChainID:             big.NewInt(1337),
			HomesteadBlock:      big.NewInt(0),
			EIP150Block:         big.NewInt(0),
			EIP155Block:         big.NewInt(0),
			EIP158Block:         big.NewInt(0),
			ByzantiumBlock:      big.NewInt(0),
			ConstantinopleBlock: big.NewInt(0),
			PetersburgBlock:     big.NewInt(0),
			IstanbulBlock:       big.NewInt(0),
			MuirGlacierBlock:    big.NewInt(0),
			BerlinBlock:         big.NewInt(0),
			LondonBlock:         big.NewInt(0),
		},
		vmConfig: vm.Config{},
	}

	// Start background worker
	go s.worker()

	return s
}

// Submit queues a transaction for simulation
// Returns error if queue is full (timeout after 5 seconds)
func (s *Simulator) Submit(tx protocol.CrossShardTx) error {
	// Set initial status
	s.mu.Lock()
	s.results[tx.ID] = &SimulationResult{
		TxID:   tx.ID,
		Status: protocol.SimPending,
	}
	s.mu.Unlock()

	// Non-blocking send with timeout to prevent indefinite blocking
	select {
	case s.queue <- &simulationJob{tx: tx}:
		log.Printf("Simulator: Queued tx %s for simulation", tx.ID)
		return nil
	case <-time.After(5 * time.Second):
		s.mu.Lock()
		s.results[tx.ID].Status = protocol.SimFailed
		s.results[tx.ID].Error = "simulation queue full"
		s.mu.Unlock()
		log.Printf("Simulator: Queue full, tx %s rejected", tx.ID)
		return fmt.Errorf("simulation queue full (timeout)")
	}
}

// SetOnError sets the callback for simulation failures (V2 optimistic locking)
func (s *Simulator) SetOnError(callback func(tx protocol.CrossShardTx)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onError = callback
}

// GetResult returns the simulation result for a transaction
// Returns a copy to avoid race conditions with the worker goroutine
func (s *Simulator) GetResult(txID string) (*SimulationResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result, ok := s.results[txID]
	if !ok || result == nil {
		return nil, ok
	}
	// Return a copy to avoid race with worker modifying Status
	rwSetCopy := make([]protocol.RwVariable, len(result.RwSet))
	copy(rwSetCopy, result.RwSet)
	return &SimulationResult{
		TxID:    result.TxID,
		Status:  result.Status,
		RwSet:   rwSetCopy,
		GasUsed: result.GasUsed,
		Error:   result.Error,
	}, true
}

// worker processes simulation jobs from the queue
func (s *Simulator) worker() {
	for job := range s.queue {
		s.runSimulation(job)
	}
}

// V2.2: Maximum iterations for iterative re-execution to prevent infinite loops
const maxIterations = 10

func (s *Simulator) runSimulation(job *simulationJob) {
	tx := job.tx

	// Update status to running
	s.mu.Lock()
	if result, ok := s.results[tx.ID]; ok {
		result.Status = protocol.SimRunning
	}
	s.mu.Unlock()

	log.Printf("Simulator: Running simulation for tx %s", tx.ID)

	// V2.2: Iterative re-execution with RwSet accumulation
	// Start with empty preloaded RwSet
	var accumulatedRwSet []protocol.RwVariable
	var gasUsed uint64
	var execErr error
	var finalStateDB *SimulationStateDB

	for iteration := 0; iteration < maxIterations; iteration++ {
		log.Printf("Simulator: Tx %s iteration %d (preloaded RwSet: %d entries)", tx.ID, iteration, len(accumulatedRwSet))

		// Create simulation state with any accumulated RwSet from previous iterations
		var stateDB *SimulationStateDB
		if len(accumulatedRwSet) > 0 {
			stateDB = NewSimulationStateDBWithRwSet(tx.ID, s.fetcher, accumulatedRwSet)

			// V2.2: Verify RwSet consistency before re-execution
			// This ensures the state hasn't changed since we fetched the RwSet
			staleAddrs := stateDB.VerifyRwSetConsistency()
			if len(staleAddrs) > 0 {
				log.Printf("Simulator: Tx %s has stale RwSet for %d addresses, re-fetching", tx.ID, len(staleAddrs))
				// Remove stale addresses from accumulated RwSet and retry
				accumulatedRwSet = removeStaleRwSet(accumulatedRwSet, staleAddrs)
				// Create fresh stateDB without stale entries
				if len(accumulatedRwSet) > 0 {
					stateDB = NewSimulationStateDBWithRwSet(tx.ID, s.fetcher, accumulatedRwSet)
				} else {
					stateDB = NewSimulationStateDB(tx.ID, s.fetcher)
				}
			}
		} else {
			stateDB = NewSimulationStateDB(tx.ID, s.fetcher)
		}

		// V2.2: Create tracer to detect cross-shard calls
		tracer := NewCrossShardTracer(stateDB, s.numShards)
		vmConfigWithTracer := vm.Config{
			Tracer: &tracing.Hooks{
				OnEnter: tracer.OnEnter,
				OnExit:  tracer.OnExit,
			},
		}

		// Build EVM context
		blockCtx := vm.BlockContext{
			CanTransfer: canTransfer,
			Transfer:    transfer,
			GetHash:     getBlockHash,
			Coinbase:    common.Address{},
			BlockNumber: big.NewInt(1),
			Time:        1,
			Difficulty:  big.NewInt(1),
			GasLimit:    30000000,
			BaseFee:     big.NewInt(1000000000),
		}

		txCtx := vm.TxContext{
			Origin:   tx.From,
			GasPrice: big.NewInt(1000000000),
		}

		// Create EVM with tracer
		evm := vm.NewEVM(blockCtx, stateDB, s.chainConfig, vmConfigWithTracer)
		evm.SetTxContext(txCtx)

		// Prepare access list
		rules := s.chainConfig.Rules(blockCtx.BlockNumber, false, blockCtx.Time)
		stateDB.Prepare(rules, tx.From, common.Address{}, nil, nil, nil)

		// Determine target address: tx.To for direct calls, or first RwSet entry
		toAddr := tx.To
		if toAddr == (common.Address{}) && len(tx.RwSet) > 0 {
			toAddr = tx.RwSet[0].Address
		}

		gas := tx.Gas
		if gas == 0 {
			if len(tx.Data) > 0 {
				gas = 1000000 // Default gas for contract calls
			} else {
				gas = 21000 // Base gas for simple transfers
			}
		}

		// Execute transaction via EVM
		_, gasLeft, err := evm.Call(
			tx.From,
			toAddr,
			tx.Data,
			gas,
			uint256FromBig(tx.Value.ToBigInt()),
		)
		gasUsed = gas - gasLeft
		execErr = err
		finalStateDB = stateDB

		// Check for fetch errors that occurred during execution
		if stateDB.HasFetchErrors() {
			fetchErrs := stateDB.GetFetchErrors()
			log.Printf("Simulator: Tx %s had %d fetch errors during simulation", tx.ID, len(fetchErrs))
			execErr = fetchErrs[0] // Report first error
			break // Stop iteration on fetch errors
		}

		// V2.2: Check for pending external calls that need RwSetRequest
		if stateDB.HasPendingExternalCalls() {
			pendingCalls := stateDB.GetPendingExternalCalls()
			log.Printf("Simulator: Tx %s iteration %d has %d pending external calls", tx.ID, iteration, len(pendingCalls))

			// Fetch RwSet from each external shard
			newRwEntries := 0
			for _, nse := range pendingCalls {
				log.Printf("  - Requesting RwSet from shard %d for %s", nse.ShardID, nse.Address.Hex())

				// Send RwSetRequest to the external shard
				refBlock := protocol.Reference{ShardNum: nse.ShardID, BlockHeight: 0} // PoC: use block 0
				reply, err := s.fetcher.RequestRwSetFromNoStateError(nse, tx.ID, refBlock)
				if err != nil {
					log.Printf("Simulator: Failed to get RwSet from shard %d: %v", nse.ShardID, err)
					// Continue - we'll retry without this RwSet
					continue
				}

				// Merge the returned RwSet into accumulated set
				log.Printf("  - Received RwSet with %d entries from shard %d", len(reply.RwSet), nse.ShardID)
				accumulatedRwSet = mergeRwSets(accumulatedRwSet, reply.RwSet)
				newRwEntries += len(reply.RwSet)
			}

			if newRwEntries > 0 {
				// We got new RwSet entries - re-execute from scratch
				log.Printf("Simulator: Tx %s re-executing with %d total RwSet entries", tx.ID, len(accumulatedRwSet))
				continue // Next iteration
			}

			// No new entries - we're stuck (external calls but can't get their state)
			log.Printf("Simulator: Tx %s has external calls but failed to get their RwSet", tx.ID)
			execErr = fmt.Errorf("failed to resolve external state dependencies")
			break
		}

		// No pending external calls - simulation complete
		log.Printf("Simulator: Tx %s completed after %d iterations", tx.ID, iteration+1)
		break
	}

	// Build result
	result := &SimulationResult{
		TxID:    tx.ID,
		GasUsed: gasUsed,
	}

	if execErr != nil {
		// Simulation failed - unlock all and record error
		log.Printf("Simulator: Tx %s failed: %v", tx.ID, execErr)
		s.fetcher.UnlockAll(tx.ID)
		result.Status = protocol.SimFailed
		result.Error = execErr.Error()

		// V2: Call error callback to record SimError transaction
		if s.onError != nil {
			tx.SimStatus = protocol.SimFailed
			tx.SimError = execErr.Error()
			s.onError(tx)
		}
	} else {
		// Simulation succeeded - build RwSet and keep locks
		log.Printf("Simulator: Tx %s succeeded, gas used: %d", tx.ID, gasUsed)
		result.Status = protocol.SimSuccess
		if finalStateDB != nil {
			result.RwSet = finalStateDB.BuildRwSet()
		}

		// Update tx with RwSet and call success callback
		tx.RwSet = result.RwSet
		tx.Gas = gasUsed
		if s.onSuccess != nil {
			s.onSuccess(tx)
		}
	}

	s.mu.Lock()
	s.results[tx.ID] = result
	s.mu.Unlock()
}

// mergeRwSets combines two RwSet slices, deduplicating by address
// V2.2: Used to accumulate RwSet entries across iterations
func mergeRwSets(existing, new []protocol.RwVariable) []protocol.RwVariable {
	// Build map for deduplication
	byAddr := make(map[common.Address]*protocol.RwVariable)
	for i := range existing {
		byAddr[existing[i].Address] = &existing[i]
	}

	// Merge new entries
	for _, rw := range new {
		if existing, ok := byAddr[rw.Address]; ok {
			// Merge ReadSet and WriteSet
			existing.ReadSet = mergeReadSets(existing.ReadSet, rw.ReadSet)
			existing.WriteSet = mergeWriteSets(existing.WriteSet, rw.WriteSet)
		} else {
			// Add new address
			rwCopy := rw
			byAddr[rw.Address] = &rwCopy
		}
	}

	// Convert back to slice
	result := make([]protocol.RwVariable, 0, len(byAddr))
	for _, rw := range byAddr {
		result = append(result, *rw)
	}
	return result
}

// mergeReadSets combines ReadSet entries, keeping the first value for each slot
func mergeReadSets(existing, new []protocol.ReadSetItem) []protocol.ReadSetItem {
	bySlot := make(map[protocol.Slot]protocol.ReadSetItem)
	for _, item := range existing {
		bySlot[item.Slot] = item
	}
	for _, item := range new {
		if _, exists := bySlot[item.Slot]; !exists {
			bySlot[item.Slot] = item
		}
	}
	result := make([]protocol.ReadSetItem, 0, len(bySlot))
	for _, item := range bySlot {
		result = append(result, item)
	}
	return result
}

// mergeWriteSets combines WriteSet entries, keeping the latest value for each slot
func mergeWriteSets(existing, new []protocol.WriteSetItem) []protocol.WriteSetItem {
	bySlot := make(map[protocol.Slot]protocol.WriteSetItem)
	for _, item := range existing {
		bySlot[item.Slot] = item
	}
	for _, item := range new {
		bySlot[item.Slot] = item // New overwrites existing
	}
	result := make([]protocol.WriteSetItem, 0, len(bySlot))
	for _, item := range bySlot {
		result = append(result, item)
	}
	return result
}

// removeStaleRwSet filters out RwSet entries for stale addresses
// V2.2: Used when consistency verification detects changed state
func removeStaleRwSet(rwSet []protocol.RwVariable, staleAddrs []common.Address) []protocol.RwVariable {
	// Build set of stale addresses for O(1) lookup
	stale := make(map[common.Address]bool)
	for _, addr := range staleAddrs {
		stale[addr] = true
	}

	// Filter out stale entries
	result := make([]protocol.RwVariable, 0, len(rwSet))
	for _, rw := range rwSet {
		if !stale[rw.Address] {
			result = append(result, rw)
		}
	}
	return result
}

// Helper functions for EVM

func canTransfer(db vm.StateDB, addr common.Address, amount *uint256.Int) bool {
	return db.GetBalance(addr).Cmp(amount) >= 0
}

func transfer(db vm.StateDB, sender, recipient common.Address, amount *uint256.Int) {
	db.SubBalance(sender, amount, 0)
	db.AddBalance(recipient, amount, 0)
}

func getBlockHash(n uint64) common.Hash {
	return common.Hash{}
}

func uint256FromBig(b *big.Int) *uint256.Int {
	if b == nil {
		return uint256.NewInt(0)
	}
	u, _ := uint256.FromBig(b)
	return u
}
