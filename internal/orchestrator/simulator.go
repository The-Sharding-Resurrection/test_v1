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

func (s *Simulator) runSimulation(job *simulationJob) {
	tx := job.tx

	// Update status to running
	s.mu.Lock()
	if result, ok := s.results[tx.ID]; ok {
		result.Status = protocol.SimRunning
	}
	s.mu.Unlock()

	log.Printf("Simulator: Running simulation for tx %s", tx.ID)

	// Create simulation state
	stateDB := NewSimulationStateDB(tx.ID, s.fetcher)

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

	// Create EVM
	evm := vm.NewEVM(blockCtx, stateDB, s.chainConfig, s.vmConfig)
	evm.SetTxContext(txCtx)

	// Prepare access list
	rules := s.chainConfig.Rules(blockCtx.BlockNumber, false, blockCtx.Time)
	stateDB.Prepare(rules, tx.From, common.Address{}, nil, nil, nil)

	// Execute transaction via EVM
	// Both contract calls and simple transfers go through evm.Call()
	// This ensures consistent behavior and proper gas accounting
	var gasUsed uint64
	var execErr error

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

	// Use EVM for ALL transactions (including simple transfers)
	// This ensures proper execution semantics and gas accounting
	_, gasLeft, err := evm.Call(
		tx.From,
		toAddr,
		tx.Data,
		gas,
		uint256FromBig(tx.Value.ToBigInt()),
	)
	gasUsed = gas - gasLeft
	execErr = err

	// Check for fetch errors that occurred during execution
	if stateDB.HasFetchErrors() {
		fetchErrs := stateDB.GetFetchErrors()
		log.Printf("Simulator: Tx %s had %d fetch errors during simulation", tx.ID, len(fetchErrs))
		execErr = fetchErrs[0] // Report first error
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
		result.RwSet = stateDB.BuildRwSet()

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
