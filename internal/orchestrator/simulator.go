package orchestrator

import (
	"fmt"
	"log"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// ResultTTL is the time after which simulation results are cleaned up
const ResultTTL = 10 * time.Minute

// ResultCleanupInterval is how often we check for expired results
const ResultCleanupInterval = 1 * time.Minute

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
	stopCleanup chan struct{}
	stopped     sync.Once // Ensures Stop() is idempotent
}

type simulationJob struct {
	tx protocol.CrossShardTx
}

// SimulationResult holds the outcome of a simulation
type SimulationResult struct {
	TxID      string
	Status    protocol.SimulationStatus
	RwSet     []protocol.RwVariable
	GasUsed   uint64
	Error     string
	CreatedAt time.Time // For TTL-based cleanup
}

// NewSimulator creates a new simulation worker
func NewSimulator(fetcher *StateFetcher, onSuccess func(tx protocol.CrossShardTx)) *Simulator {
	s := &Simulator{
		fetcher:     fetcher,
		queue:       make(chan *simulationJob, 100),
		results:     make(map[string]*SimulationResult),
		onSuccess:   onSuccess,
		numShards:   NumShards, // V2.2: Use constant from statedb.go
		stopCleanup: make(chan struct{}),
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

	// Start background workers
	go s.worker()
	go s.cleanupWorker()

	return s
}

// Submit queues a transaction for simulation
// Returns error if queue is full (timeout after 5 seconds)
func (s *Simulator) Submit(tx protocol.CrossShardTx) error {
	// Set initial status
	s.mu.Lock()
	s.results[tx.ID] = &SimulationResult{
		TxID:      tx.ID,
		Status:    protocol.SimPending,
		CreatedAt: time.Now(),
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
		TxID:      result.TxID,
		Status:    result.Status,
		RwSet:     rwSetCopy,
		GasUsed:   result.GasUsed,
		Error:     result.Error,
		CreatedAt: result.CreatedAt,
	}, true
}

// worker processes simulation jobs from the queue
func (s *Simulator) worker() {
	for job := range s.queue {
		s.runSimulation(job)
	}
}

// cleanupWorker periodically removes expired results to prevent memory leaks
func (s *Simulator) cleanupWorker() {
	ticker := time.NewTicker(ResultCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.cleanupExpiredResults()
		case <-s.stopCleanup:
			return
		}
	}
}

// Stop gracefully stops the simulator's background workers.
// This method is idempotent and safe to call multiple times.
func (s *Simulator) Stop() {
	s.stopped.Do(func() {
		// Stop cleanup worker
		close(s.stopCleanup)
		// Close queue to stop worker (will exit when range loop ends)
		close(s.queue)
		log.Printf("Simulator: Stopped")
	})
}

// cleanupExpiredResults removes results older than ResultTTL
func (s *Simulator) cleanupExpiredResults() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	expiredCount := 0

	for txID, result := range s.results {
		if now.Sub(result.CreatedAt) >= ResultTTL {
			delete(s.results, txID)
			expiredCount++
		}
	}

	if expiredCount > 0 {
		log.Printf("Simulator: Cleaned up %d expired results (remaining: %d)", expiredCount, len(s.results))
	}
}

// runSimulation executes single-pass simulation for cross-shard transactions.
//
// Lazy State Fetching Architecture:
// The StateDB lazily fetches state from any shard on-demand during EVM execution:
//   - GetCode(addr) fetches bytecode from the owning shard if not cached
//   - GetState(addr, slot) fetches storage slot from the owning shard if not cached
//   - GetBalance(addr) fetches account balance from the owning shard if not cached
//
// This eliminates the need for iterative re-execution. When the EVM executes a CALL
// to an external contract, state is fetched transparently via HTTP to the target shard.
// The RwSet is built from tracked reads/writes at the end of the single execution pass.
func (s *Simulator) runSimulation(job *simulationJob) {
	tx := job.tx

	// Update status to running
	s.mu.Lock()
	if result, ok := s.results[tx.ID]; ok {
		result.Status = protocol.SimRunning
	}
	s.mu.Unlock()

	log.Printf("Simulator: Running simulation for tx %s", tx.ID)

	// Create simulation StateDB with lazy fetching
	stateDB := NewSimulationStateDB(tx.ID, s.fetcher)

	// Build EVM context (no tracer needed - state is fetched lazily)
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

	// Create EVM (no tracer)
	evm := vm.NewEVM(blockCtx, stateDB, s.chainConfig, vm.Config{})
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

	// Execute transaction via EVM (state is fetched lazily as needed)
	_, gasLeft, execErr := evm.Call(
		tx.From,
		toAddr,
		tx.Data,
		gas,
		uint256FromBig(tx.Value.ToBigInt()),
	)
	gasUsed := gas - gasLeft

	// Check for fetch errors that occurred during execution
	if stateDB.HasFetchErrors() {
		fetchErrs := stateDB.GetFetchErrors()
		log.Printf("Simulator: Tx %s had %d fetch errors during simulation", tx.ID, len(fetchErrs))
		execErr = fetchErrs[0] // Report first error
	}

	// Build result
	result := &SimulationResult{
		TxID:      tx.ID,
		GasUsed:   gasUsed,
		CreatedAt: time.Now(),
	}

	if execErr != nil {
		// Simulation failed - clear cache and record error
		log.Printf("Simulator: Tx %s failed: %v", tx.ID, execErr)
		s.fetcher.ClearCache(tx.ID)
		result.Status = protocol.SimFailed
		result.Error = execErr.Error()

		// V2: Call error callback to record SimError transaction
		if s.onError != nil {
			tx.SimStatus = protocol.SimFailed
			tx.SimError = execErr.Error()
			s.onError(tx)
		}
	} else {
		// Simulation succeeded - build RwSet from tracked state accesses
		log.Printf("Simulator: Tx %s succeeded, gas used: %d", tx.ID, gasUsed)
		result.Status = protocol.SimSuccess
		result.RwSet = stateDB.BuildRwSet()

		// Update tx with RwSet and call success callback
		tx.RwSet = result.RwSet
		tx.Gas = gasUsed
		if s.onSuccess != nil {
			s.onSuccess(tx)
		}

		// Clear cache after simulation completes
		s.fetcher.ClearCache(tx.ID)
	}

	s.mu.Lock()
	s.results[tx.ID] = result
	s.mu.Unlock()
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
