package shard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/sharding-experiment/sharding/config"
	"github.com/sharding-experiment/sharding/internal/network"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

const (
	// Default block production interval (used if config not available)
	DefaultBlockProductionInterval = 3 * time.Second
)

// Server handles HTTP requests for a shard node
// MaxBlockBuffer is the maximum number of out-of-order blocks to buffer
const MaxBlockBuffer = 100

type Server struct {
	shardID                 int
	evmState                *EVMState
	chain                   *Chain
	orchestrator            string
	router                  *mux.Router
	receipts                *ReceiptStore
	httpClient              *http.Client
	blockBuffer             *BlockBuffer // Handles out-of-order orchestrator block delivery
	blockProductionInterval time.Duration
	done                    chan struct{} // Signal channel for graceful shutdown
	closeOnce               sync.Once     // Ensures Close() is idempotent
}

func NewServer(shardID int, orchestratorURL string, networkConfig config.NetworkConfig) *Server {
	evmState, err := NewEVMState(shardID)
	if err != nil {
		log.Printf("WARNING: Failed to create persistent EVM state for shard %d: %v. Falling back to in-memory state.", shardID, err)
		evmState, err = NewMemoryEVMState()
		if err != nil {
			log.Fatalf("Failed to create in-memory EVM state: %v", err)
		}
	}

	// Load block time from config, default to 3 seconds
	blockInterval := DefaultBlockProductionInterval
	if cfg, err := config.LoadDefault(); err == nil && cfg.BlockTimeMs > 0 {
		blockInterval = time.Duration(cfg.BlockTimeMs) * time.Millisecond
	}

	s := &Server{
		shardID:                 shardID,
		evmState:                evmState,
		chain:                   NewChain(shardID),
		orchestrator:            orchestratorURL,
		router:                  mux.NewRouter(),
		receipts:                NewReceiptStore(),
		httpClient:              network.NewHTTPClient(networkConfig, 10*time.Second),
		blockBuffer:             NewBlockBuffer(shardID, 1, MaxBlockBuffer), // Start expecting block 1 (after genesis)
		blockProductionInterval: blockInterval,
		done:                    make(chan struct{}),
	}
	s.setupRoutes()

	// Crash recovery: replay missed orchestrator blocks on startup
	s.recoverFromOrchestrator()

	go s.blockProducer() // Start block production
	// V2 Optimistic: No simulation lock cleanup needed - slot locks are only held during block production
	return s
}

// NewServerForTest creates a server without starting the block producer (for testing)
func NewServerForTest(shardID int, orchestratorURL string, networkConfig config.NetworkConfig) *Server {
	evmState, err := NewEVMState(shardID)
	if err != nil {
		log.Printf("WARNING: Failed to create persistent EVM state for shard %d (test mode): %v. Falling back to in-memory state.", shardID, err)
		evmState, err = NewMemoryEVMState()
		if err != nil {
			log.Fatalf("Failed to create in-memory EVM state (test mode): %v", err)
		}
	}

	// Load block time from config, default to 3 seconds
	blockInterval := DefaultBlockProductionInterval
	if cfg, err := config.LoadDefault(); err == nil && cfg.BlockTimeMs > 0 {
		blockInterval = time.Duration(cfg.BlockTimeMs) * time.Millisecond
	}

	s := &Server{
		shardID:                 shardID,
		evmState:                evmState,
		chain:                   NewChain(shardID),
		orchestrator:            orchestratorURL,
		router:                  mux.NewRouter(),
		receipts:                NewReceiptStore(),
		httpClient:              network.NewHTTPClient(networkConfig, 10*time.Second),
		blockBuffer:             NewBlockBuffer(shardID, 1, MaxBlockBuffer),
		blockProductionInterval: blockInterval,
	}
	s.setupRoutes()
	// Note: block producer not started for testing
	return s
}

// Router returns the HTTP router for testing
func (s *Server) Router() *mux.Router {
	return s.router
}

// ProduceBlock creates a new block with pending transactions (for testing)
func (s *Server) ProduceBlock() (*protocol.StateShardBlock, error) {
	return s.chain.ProduceBlock(s.evmState)
}

// =============================================================================
// Test helpers - expose internal state for integration testing
// =============================================================================

// SetStorageAt sets a storage slot value (for testing)
func (s *Server) SetStorageAt(addr common.Address, slot, value common.Hash) {
	s.evmState.SetStorageAt(addr, slot, value)
}

// GetStorageAt gets a storage slot value (for testing)
func (s *Server) GetStorageAt(addr common.Address, slot common.Hash) common.Hash {
	return s.evmState.GetStorageAt(addr, slot)
}

// GetBalance gets an address balance (for testing)
func (s *Server) GetBalance(addr common.Address) *big.Int {
	return s.evmState.GetBalance(addr)
}

// IsSlotLocked checks if a slot is locked (for testing)
func (s *Server) IsSlotLocked(addr common.Address, slot common.Hash) bool {
	return s.chain.IsSlotLocked(addr, slot)
}

// blockProducer creates State Shard blocks periodically
func (s *Server) blockProducer() {
	ticker := time.NewTicker(s.blockProductionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			log.Printf("Shard %d: Block producer stopping", s.shardID)
			return
		case <-ticker.C:
			block, err := s.chain.ProduceBlock(s.evmState)
			if err != nil {
				log.Printf("Shard %d: Failed to produce block: %v", s.shardID, err)
				continue
			}

			log.Printf("Shard %d: Produced block %d with %d txs",
				s.shardID, block.Height, len(block.TxOrdering))

			// Send block with TpcPrepare votes back to Orchestrator
			s.sendBlockToOrchestratorShard(block)
		}
	}
}

// Close gracefully shuts down the server, stopping the block producer goroutine.
// This method is idempotent and safe to call multiple times.
// Safe to call on servers created with NewServerForTest (done channel may be nil).
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		if s.done != nil {
			close(s.done)
		}
	})
}

// sendBlockToOrchestratorShard sends State Shard block back to Orchestrator.
// This delivers the shard's TpcPrepare votes for pending cross-shard transactions.
func (s *Server) sendBlockToOrchestratorShard(block *protocol.StateShardBlock) {
	blockData, err := json.Marshal(block)
	if err != nil {
		log.Printf("Shard %d: Failed to marshal State Shard block: %v", s.shardID, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", s.orchestrator+"/state-shard/block", bytes.NewBuffer(blockData))
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		log.Printf("Shard %d: Failed to send block to Orchestrator: %v", s.shardID, err)
		return
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Shard %d: Orchestrator returned %d", s.shardID, resp.StatusCode)
	}
}

// recoverFromOrchestrator replays missed orchestrator blocks on startup for crash recovery.
func (s *Server) recoverFromOrchestrator() {
	if s.orchestrator == "" {
		log.Printf("Shard %d: No orchestrator URL configured, skipping recovery", s.shardID)
		return
	}

	// Get our last processed height
	lastHeight := s.chain.GetLastOrchestratorHeight()
	log.Printf("Shard %d: Starting crash recovery from orchestrator (last processed height: %d)",
		s.shardID, lastHeight)

	// Query orchestrator for latest height
	latestURL := s.orchestrator + "/block/latest"
	resp, err := s.httpClient.Get(latestURL)
	if err != nil {
		log.Printf("Shard %d: Failed to query orchestrator for latest block: %v", s.shardID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Shard %d: Orchestrator returned status %d for latest block", s.shardID, resp.StatusCode)
		return
	}

	var latestResp struct {
		Height uint64                           `json:"height"`
		Block  *protocol.OrchestratorShardBlock `json:"block,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&latestResp); err != nil {
		log.Printf("Shard %d: Failed to decode latest block response: %v", s.shardID, err)
		return
	}

	orchestratorHeight := latestResp.Height
	log.Printf("Shard %d: Orchestrator latest height is %d, we have %d",
		s.shardID, orchestratorHeight, lastHeight)

	if lastHeight >= orchestratorHeight {
		log.Printf("Shard %d: No blocks to recover (already up to date)", s.shardID)
		return
	}

	// Fetch and process missed blocks
	// Start from lastHeight+1 (we already processed lastHeight)
	for height := lastHeight + 1; height <= orchestratorHeight; height++ {
		block, err := s.fetchOrchestratorBlock(height)
		if err != nil {
			log.Printf("Shard %d: Failed to fetch block %d during recovery: %v", s.shardID, height, err)
			// Continue trying to recover - blocks might be available later
			continue
		}
		if block == nil {
			log.Printf("Shard %d: Block %d not found during recovery", s.shardID, height)
			continue
		}

		log.Printf("Shard %d: Replaying block %d (%d txs, %d results)",
			s.shardID, height, len(block.CtToOrder), len(block.TpcResult))

		// Process the block (idempotency checks will skip already-processed commits)
		s.processOrchestratorBlock(block)
	}

	log.Printf("Shard %d: Recovery complete, processed up to height %d",
		s.shardID, s.chain.GetLastOrchestratorHeight())
}

// fetchOrchestratorBlock fetches a specific block from the orchestrator
func (s *Server) fetchOrchestratorBlock(height uint64) (*protocol.OrchestratorShardBlock, error) {
	url := fmt.Sprintf("%s/block/%d", s.orchestrator, height)
	resp, err := s.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var block protocol.OrchestratorShardBlock
	if err := json.NewDecoder(resp.Body).Decode(&block); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	return &block, nil
}

func (s *Server) setupRoutes() {
	// Account endpoints
	s.router.HandleFunc("/balance/{address}", s.handleGetBalance).Methods("GET")
	s.router.HandleFunc("/transfer", s.handleLocalTransfer).Methods("POST")
	s.router.HandleFunc("/faucet", s.handleFaucet).Methods("POST")

	// EVM endpoints
	s.router.HandleFunc("/evm/deploy", s.handleDeploy).Methods("POST")
	s.router.HandleFunc("/evm/call", s.handleCall).Methods("POST")
	s.router.HandleFunc("/evm/staticcall", s.handleStaticCall).Methods("POST")
	s.router.HandleFunc("/evm/code/{address}", s.handleGetCode).Methods("GET")
	s.router.HandleFunc("/evm/setcode", s.handleSetCode).Methods("POST") // Receive code from other shards
	s.router.HandleFunc("/evm/storage/{address}/{slot}", s.handleGetStorage).Methods("GET")
	s.router.HandleFunc("/evm/stateroot", s.handleGetStateRoot).Methods("GET")

	// Cross-shard endpoints (called by orchestrator)
	s.router.HandleFunc("/cross-shard/prepare", s.handlePrepare).Methods("POST")
	s.router.HandleFunc("/cross-shard/commit", s.handleCommit).Methods("POST")
	s.router.HandleFunc("/cross-shard/abort", s.handleAbort).Methods("POST")
	s.router.HandleFunc("/cross-shard/credit", s.handleCredit).Methods("POST")

	// State fetch endpoint (read-only for simulation)
	// V2 Optimistic Locking: No locks during simulation, just read state
	s.router.HandleFunc("/state/fetch", s.handleStateFetch).Methods("POST")

	// V2.2 Iterative re-execution endpoint
	s.router.HandleFunc("/rw-set", s.handleRwSet).Methods("POST")

	// Cross-shard transfer initiation (legacy - explicit cross-shard)
	s.router.HandleFunc("/cross-shard/transfer", s.handleCrossShardTransfer).Methods("POST")

	// Unified transaction submission - auto-detects cross-shard
	s.router.HandleFunc("/tx/submit", s.handleTxSubmit).Methods("POST")

	// Block propagation
	s.router.HandleFunc("/orchestrator-shard/block", s.handleOrchestratorShardBlock).Methods("POST")

	// Health check
	s.router.HandleFunc("/health", s.handleHealth).Methods("GET")
	s.router.HandleFunc("/info", s.handleInfo).Methods("GET")

	// JSON-RPC (for cast/forge compatibility)
	s.router.HandleFunc("/", s.handleJSONRPC).Methods("POST")
}

func (s *Server) Start(port int) error {
	addr := fmt.Sprintf(":%d", port)
	log.Printf("Shard %d starting on %s", s.shardID, addr)
	return http.ListenAndServe(addr, s.router)
}

// Handler implementations

func (s *Server) handleGetBalance(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	addr := common.HexToAddress(vars["address"])
	balance := s.evmState.GetBalance(addr)
	json.NewEncoder(w).Encode(map[string]string{
		"address": addr.Hex(),
		"balance": balance.String(),
	})
}

type TransferRequest struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"`
}

func (s *Server) handleLocalTransfer(w http.ResponseWriter, r *http.Request) {
	var req TransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	from := common.HexToAddress(req.From)
	to := common.HexToAddress(req.To)
	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok {
		http.Error(w, "invalid amount", http.StatusBadRequest)
		return
	}

	// Debit from sender
	if err := s.evmState.Debit(from, amount); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Credit to receiver
	s.evmState.Credit(to, amount)

	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

type FaucetRequest struct {
	Address string `json:"address"`
	Amount  string `json:"amount"`
}

func (s *Server) handleFaucet(w http.ResponseWriter, r *http.Request) {
	var req FaucetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	addr := common.HexToAddress(req.Address)
	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok {
		http.Error(w, "invalid amount", http.StatusBadRequest)
		return
	}
	s.evmState.Credit(addr, amount)

	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// Cross-shard handlers

func (s *Server) handlePrepare(w http.ResponseWriter, r *http.Request) {
	var req protocol.PrepareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Lock-only: check available balance (balance - locked) and lock funds
	// ATOMIC: Hold EVM mutex during check-and-lock to prevent race condition
	s.evmState.mu.Lock()
	lockedAmount := s.chain.GetLockedAmountForAddress(req.Address)
	canCommit := s.evmState.CanDebit(req.Address, req.Amount, lockedAmount)
	if canCommit {
		// Reserve funds (no debit yet - that happens on commit)
		s.chain.LockFunds(req.TxID, req.Address, req.Amount)
	}
	s.evmState.mu.Unlock()

	resp := protocol.PrepareResponse{
		TxID:    req.TxID,
		Success: canCommit,
	}
	if !canCommit {
		resp.Error = "insufficient available balance"
	}

	log.Printf("Shard %d: Prepare %s - success=%v", s.shardID, req.TxID, resp.Success)
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleCommit(w http.ResponseWriter, r *http.Request) {
	var req protocol.CommitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Lock-only: debit on commit, then clear lock
	lock, ok := s.chain.GetLockedFunds(req.TxID)
	if ok {
		// Now actually debit the funds
		s.evmState.Debit(lock.Address, lock.Amount)
		s.chain.ClearLock(req.TxID)
	}

	log.Printf("Shard %d: Commit %s - success=%v", s.shardID, req.TxID, ok)
	json.NewEncoder(w).Encode(map[string]bool{"success": ok})
}

func (s *Server) handleAbort(w http.ResponseWriter, r *http.Request) {
	var req protocol.CommitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Lock-only: just clear the lock (no refund needed - funds were never debited)
	_, ok := s.chain.GetLockedFunds(req.TxID)
	if ok {
		s.chain.ClearLock(req.TxID)
	}

	log.Printf("Shard %d: Abort %s - success=%v", s.shardID, req.TxID, ok)
	json.NewEncoder(w).Encode(map[string]bool{"success": ok})
}

type CreditRequest struct {
	TxID    string `json:"tx_id"`
	Address string `json:"address"`
	Amount  string `json:"amount"`
}

func (s *Server) handleCredit(w http.ResponseWriter, r *http.Request) {
	var req CreditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	addr := common.HexToAddress(req.Address)
	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok {
		http.Error(w, "invalid amount", http.StatusBadRequest)
		return
	}
	s.evmState.Credit(addr, amount)

	log.Printf("Shard %d: Credit %s to %s", s.shardID, req.Amount, addr.Hex())
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

type CrossShardTransferRequest struct {
	From    string `json:"from"`
	To      string `json:"to"`       // Recipient address
	ToShard int    `json:"to_shard"` // Recipient's shard
	Amount  string `json:"amount"`
}

// Note: The API still accepts to/to_shard for simple transfers.
// These get converted to RwSet entries in the CrossShardTx.

func (s *Server) handleCrossShardTransfer(w http.ResponseWriter, r *http.Request) {
	var req CrossShardTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Forward to orchestrator
	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok {
		http.Error(w, "invalid amount", http.StatusBadRequest)
		return
	}

	// Build RwSet for the destination - simple transfer is a write to recipient
	toAddr := common.HexToAddress(req.To)
	tx := protocol.CrossShardTx{
		ID:        uuid.New().String(),
		FromShard: s.shardID,
		From:      common.HexToAddress(req.From),
		To:        toAddr, // Must set To for proper credit routing
		Value:     protocol.NewBigInt(amount),
		RwSet: []protocol.RwVariable{
			{
				Address: toAddr,
				ReferenceBlock: protocol.Reference{
					ShardNum: req.ToShard,
				},
			},
		},
	}

	txData, _ := json.Marshal(tx)
	httpReq, _ := http.NewRequest("POST", s.orchestrator+"/cross-shard/call", bytes.NewBuffer(txData))
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		http.Error(w, "orchestrator unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("Shard %d: Failed to decode orchestrator response: %v", s.shardID, err)
		http.Error(w, "failed to decode orchestrator response: "+err.Error(), http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]interface{}{
		"shard_id":     s.shardID,
		"orchestrator": s.orchestrator,
	})
}

// EVM handlers

type DeployRequest struct {
	From     string `json:"from"`
	Bytecode string `json:"bytecode"` // hex encoded
	Value    string `json:"value"`
	Gas      uint64 `json:"gas"`
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	var req DeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	from := common.HexToAddress(req.From)
	bytecode := common.FromHex(req.Bytecode)
	value := big.NewInt(0)
	if req.Value != "" {
		var ok bool
		value, ok = new(big.Int).SetString(req.Value, 10)
		if !ok {
			http.Error(w, "invalid value", http.StatusBadRequest)
			return
		}
	}
	gas := req.Gas
	if gas == 0 {
		gas = 3_000_000
	}

	// Use tracked deployment to capture constructor storage writes
	contractAddr, returnData, gasUsed, _, storageWrites, err := s.evmState.DeployContractTracked(from, bytecode, value, gas, NumShards)
	if err != nil {
		log.Printf("Shard %d: Deploy failed: %v", s.shardID, err)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  false,
			"error":    err.Error(),
			"gas_used": gasUsed,
		})
		return
	}

	// Check which shard this contract address maps to (first hex digit)
	targetShard := AddressToShard(contractAddr)
	deployedCode := s.evmState.GetCode(contractAddr)

	if targetShard != s.shardID {
		// Contract address maps to different shard - forward code AND storage
		log.Printf("Shard %d: Contract %s maps to shard %d, forwarding code and %d storage slots",
			s.shardID, contractAddr.Hex(), targetShard, len(storageWrites))

		// Convert storage writes to hex strings for JSON
		storageHex := make(map[string]string)
		for slot, value := range storageWrites {
			storageHex[slot.Hex()] = value.Hex()
		}

		// Send code and storage to the target shard
		setCodeReq := SetCodeRequest{
			Address: contractAddr.Hex(),
			Code:    common.Bytes2Hex(deployedCode),
			Storage: storageHex,
		}
		setCodeData, _ := json.Marshal(setCodeReq)

		// Get target shard URL (assumes shards are at shard-{id}:8545)
		targetURL := fmt.Sprintf("http://shard-%d:8545/evm/setcode", targetShard)
		httpReq, _ := http.NewRequest("POST", targetURL, bytes.NewBuffer(setCodeData))
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := s.httpClient.Do(httpReq)
		if err != nil {
			log.Printf("Shard %d: Failed to forward code to shard %d: %v", s.shardID, targetShard, err)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":      false,
				"error":        fmt.Sprintf("failed to forward to shard %d: %v", targetShard, err),
				"address":      contractAddr.Hex(),
				"gas_used":     gasUsed,
				"target_shard": targetShard,
			})
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		log.Printf("Shard %d: Forwarded code and storage to shard %d", s.shardID, targetShard)
	}

	log.Printf("Shard %d: Contract deployed at %s (target shard: %d, storage slots: %d)",
		s.shardID, contractAddr.Hex(), targetShard, len(storageWrites))
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"address":       contractAddr.Hex(),
		"return":        common.Bytes2Hex(returnData),
		"gas_used":      gasUsed,
		"target_shard":  targetShard,
		"storage_slots": len(storageWrites),
	})
}

// SetCodeRequest for receiving contract code and storage from other shards
type SetCodeRequest struct {
	Address string            `json:"address"`
	Code    string            `json:"code"`    // hex encoded
	Storage map[string]string `json:"storage"` // slot -> value (hex encoded)
}

func (s *Server) handleSetCode(w http.ResponseWriter, r *http.Request) {
	var req SetCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	addr := common.HexToAddress(req.Address)
	code := common.FromHex(req.Code)

	// Verify this address belongs to this shard (first hex digit)
	targetShard := AddressToShard(addr)
	if targetShard != s.shardID {
		http.Error(w, fmt.Sprintf("address %s belongs to shard %d, not %d",
			addr.Hex(), targetShard, s.shardID), http.StatusBadRequest)
		return
	}

	// Set the code in our EVM state
	s.evmState.SetCode(addr, code)

	// Apply constructor storage if provided
	storageCount := 0
	if req.Storage != nil {
		for slotHex, valueHex := range req.Storage {
			slot := common.HexToHash(slotHex)
			value := common.HexToHash(valueHex)
			s.evmState.SetStorageAt(addr, slot, value)
			storageCount++
		}
	}

	log.Printf("Shard %d: Received and set code for %s (%d bytes, %d storage slots)",
		s.shardID, addr.Hex(), len(code), storageCount)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"address":       addr.Hex(),
		"storage_slots": storageCount,
	})
}

type CallRequest struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Data  string `json:"data"` // hex encoded calldata
	Value string `json:"value"`
	Gas   uint64 `json:"gas"`
}

func (s *Server) handleCall(w http.ResponseWriter, r *http.Request) {
	var req CallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	from := common.HexToAddress(req.From)
	to := common.HexToAddress(req.To)
	data := common.FromHex(req.Data)
	value := big.NewInt(0)
	if req.Value != "" {
		var ok bool
		value, ok = new(big.Int).SetString(req.Value, 10)
		if !ok {
			http.Error(w, "invalid value", http.StatusBadRequest)
			return
		}
	}
	gas := req.Gas
	if gas == 0 {
		gas = 1_000_000
	}

	ret, gasUsed, _, err := s.evmState.CallContract(from, to, data, value, gas)
	if err != nil {
		log.Printf("Shard %d: Call to %s failed: %v", s.shardID, to.Hex(), err)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  false,
			"error":    err.Error(),
			"gas_used": gasUsed,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"return":   common.Bytes2Hex(ret),
		"gas_used": gasUsed,
	})
}

func (s *Server) handleStaticCall(w http.ResponseWriter, r *http.Request) {
	var req CallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	from := common.HexToAddress(req.From)
	to := common.HexToAddress(req.To)
	data := common.FromHex(req.Data)
	gas := req.Gas
	if gas == 0 {
		gas = 1_000_000
	}

	ret, gasUsed, err := s.evmState.StaticCall(from, to, data, gas)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  false,
			"error":    err.Error(),
			"gas_used": gasUsed,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"return":   common.Bytes2Hex(ret),
		"gas_used": gasUsed,
	})
}

func (s *Server) handleGetCode(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	addr := common.HexToAddress(vars["address"])
	code := s.evmState.GetCode(addr)

	json.NewEncoder(w).Encode(map[string]string{
		"address": addr.Hex(),
		"code":    common.Bytes2Hex(code),
	})
}

func (s *Server) handleGetStorage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	addr := common.HexToAddress(vars["address"])
	slot := common.HexToHash(vars["slot"])

	// Check if proof is requested via query parameter
	includeProof := r.URL.Query().Get("proof") == "true"

	if includeProof {
		// V2.3: Return storage value with Merkle proof
		proofResp, err := s.evmState.GetStorageWithProof(addr, slot)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to generate proof: %v", err), http.StatusInternalServerError)
			return
		}

		// Convert proof response to JSON-friendly format
		response := map[string]interface{}{
			"address":       proofResp.Address.Hex(),
			"slot":          proofResp.Slot.Hex(),
			"value":         proofResp.Value.Hex(),
			"state_root":    proofResp.StateRoot.Hex(),
			"block_height":  proofResp.BlockHeight,
			"account_proof": formatProof(proofResp.AccountProof),
			"storage_proof": formatProof(proofResp.StorageProof),
		}
		json.NewEncoder(w).Encode(response)
	} else {
		// Legacy: Return just the value (backwards compatible)
		value := s.evmState.GetStorageAt(addr, slot)
		json.NewEncoder(w).Encode(map[string]string{
			"address": addr.Hex(),
			"slot":    slot.Hex(),
			"value":   value.Hex(),
		})
	}
}

// formatProof converts [][]byte proof to hex string array for JSON
func formatProof(proof [][]byte) []string {
	result := make([]string, len(proof))
	for i, p := range proof {
		result[i] = "0x" + common.Bytes2Hex(p)
	}
	return result
}

func (s *Server) handleGetStateRoot(w http.ResponseWriter, r *http.Request) {
	root := s.evmState.GetStateRoot()
	json.NewEncoder(w).Encode(map[string]string{
		"state_root": root.Hex(),
	})
}

// handleOrchestratorShardBlock receives blocks from Orchestrator Shard
func (s *Server) handleOrchestratorShardBlock(w http.ResponseWriter, r *http.Request) {
	var block protocol.OrchestratorShardBlock
	if err := json.NewDecoder(r.Body).Decode(&block); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Shard %d: Received Orchestrator Shard block %d with %d txs, %d results",
		s.shardID, block.Height, len(block.CtToOrder), len(block.TpcResult))

	// Use buffer to handle out-of-order delivery
	blocksToProcess := s.blockBuffer.ProcessBlock(&block)
	if blocksToProcess == nil {
		// Block was buffered or ignored - respond OK but don't process yet
		json.NewEncoder(w).Encode(map[string]string{"status": "buffered"})
		return
	}

	// Process all blocks returned by buffer (in order)
	for _, b := range blocksToProcess {
		s.processOrchestratorBlock(b)
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// processOrchestratorBlock handles the actual processing of an orchestrator block.
// This is called for blocks in the correct order after buffering.
func (s *Server) processOrchestratorBlock(block *protocol.OrchestratorShardBlock) {
	// Crash recovery: Check if we've already processed this block
	lastProcessed := s.chain.GetLastOrchestratorHeight()
	if block.Height <= lastProcessed {
		log.Printf("Shard %d: Skipping already processed orchestrator block %d (last=%d)",
			s.shardID, block.Height, lastProcessed)
		return
	}

	log.Printf("Shard %d: Processing Orchestrator Shard block %d", s.shardID, block.Height)

	// Phase 1: Process TpcResult (commit/abort from previous round)
	// Queue typed transactions instead of executing directly
	// This ensures all state changes go through ProduceBlock with snapshot/rollback
	for txID, committed := range block.TpcResult {
		// Idempotency: Atomically check and mark to prevent TOCTOU race during crash recovery
		if !s.chain.CheckAndMarkCommitProcessed(txID) {
			log.Printf("Shard %d: Skipping already processed commit/abort for %s", s.shardID, txID)
			continue
		}

		if committed {
			// COMMIT: Queue actual state mutations as typed transactions

			// Source shard: queue DEBIT for locked funds
			if lock, ok := s.chain.GetLockedFunds(txID); ok {
				s.chain.AddTx(protocol.Transaction{
					ID:             uuid.New().String(),
					TxType:         protocol.TxTypeCrossDebit,
					CrossShardTxID: txID,
					From:           lock.Address,
					Value:          protocol.NewBigInt(new(big.Int).Set(lock.Amount)),
					IsCrossShard:   true,
				})
				log.Printf("Shard %d: Queued debit for %s (%s from %s)",
					s.shardID, txID, lock.Amount.String(), lock.Address.Hex())
			}

			// Destination shard: queue CREDIT(s) for pending credits
			if credits, ok := s.chain.GetPendingCredits(txID); ok {
				for _, credit := range credits {
					s.chain.AddTx(protocol.Transaction{
						ID:             uuid.New().String(),
						TxType:         protocol.TxTypeCrossCredit,
						CrossShardTxID: txID,
						To:             credit.Address,
						Value:          protocol.NewBigInt(new(big.Int).Set(credit.Amount)),
						IsCrossShard:   true,
					})
					log.Printf("Shard %d: Queued credit for %s (%s to %s)",
						s.shardID, txID, credit.Amount.String(), credit.Address.Hex())
				}
			}

			// Destination shard: queue WRITESET for storage writes (legacy path)
			if pendingCall, ok := s.chain.GetPendingCall(txID); ok {
				// Filter RwSet to only include entries for this shard
				var localRwSet []protocol.RwVariable
				for _, rw := range pendingCall.RwSet {
					if rw.ReferenceBlock.ShardNum == s.shardID {
						localRwSet = append(localRwSet, rw.DeepCopy())
					}
				}
				if len(localRwSet) > 0 {
					s.chain.AddTx(protocol.Transaction{
						ID:             uuid.New().String(),
						TxType:         protocol.TxTypeCrossWriteSet,
						CrossShardTxID: txID,
						RwSet:          localRwSet,
						IsCrossShard:   true,
					})
					log.Printf("Shard %d: Queued writeset for %s (%d entries)",
						s.shardID, txID, len(localRwSet))
				}
			}

			// V2.4: Queue FINALIZE for pending RwSet (optimistic locking path)
			// This applies WriteSet that was validated during Lock phase
			if rwSet, ok := s.chain.GetPendingRwSet(txID); ok && len(rwSet) > 0 {
				s.chain.AddTx(protocol.Transaction{
					ID:             uuid.New().String(),
					TxType:         protocol.TxTypeFinalize,
					CrossShardTxID: txID,
					RwSet:          rwSet,
					IsCrossShard:   true,
				})
				log.Printf("Shard %d: Queued finalize for %s (%d RwSet entries)",
					s.shardID, txID, len(rwSet))
			}
		} else {
			// ABORT: Queue cleanup-only transaction (no state change)
			hasData := false
			if _, ok := s.chain.GetLockedFunds(txID); ok {
				hasData = true
			}
			if _, ok := s.chain.GetPendingCredits(txID); ok {
				hasData = true
			}
			if _, ok := s.chain.GetPendingCall(txID); ok {
				hasData = true
			}
			// V2.4: Also check for pending RwSet (optimistic locking)
			if _, ok := s.chain.GetPendingRwSet(txID); ok {
				hasData = true
			}
			if hasData {
				s.chain.AddTx(protocol.Transaction{
					ID:             uuid.New().String(),
					TxType:         protocol.TxTypeCrossAbort,
					CrossShardTxID: txID,
					IsCrossShard:   true,
				})
				log.Printf("Shard %d: Queued abort for %s", s.shardID, txID)
			}
		}

		// V2: Queue Unlock transaction to release locks during block production
		// This ensures proper ordering: Finalize(1) > Unlock(2) > Lock(3) > Local(4)
		s.chain.AddTx(protocol.Transaction{
			ID:             uuid.New().String(),
			TxType:         protocol.TxTypeUnlock,
			CrossShardTxID: txID,
			IsCrossShard:   true,
		})
		log.Printf("Shard %d: Queued unlock for %s", s.shardID, txID)
		// Note: Commit processing was atomically marked at the start of the loop
	}

	// Phase 2: Process CtToOrder (new cross-shard txs)
	// V2 Optimistic Locking: NO pre-locking! Just queue Lock transactions.
	// Lock tx execution validates ReadSet and acquires locks atomically.
	// Voting happens during block production based on Lock tx success/failure.
	for _, tx := range block.CtToOrder {
		// Collect RwSet entries for this shard
		var localRwSet []protocol.RwVariable
		for _, rw := range tx.RwSet {
			if rw.ReferenceBlock.ShardNum == s.shardID {
				localRwSet = append(localRwSet, rw.DeepCopy())
			}
		}

		// V2 Optimistic: RwSet stored by validateAndLockReadSetLocked when Lock succeeds
		// No need to pre-store here - Lock tx carries RwSet and it's stored atomically

		// Source shard: queue Lock tx for balance validation and fund locking
		if tx.FromShard == s.shardID {
			// V2 Optimistic: Don't lock funds now - Lock tx will validate and lock atomically
			// Store pending credit info for destination if this shard is also destination
			toShard := AddressToShard(tx.To)
			if tx.Value.ToBigInt().Sign() > 0 && toShard == s.shardID {
				s.chain.StorePendingCredit(tx.ID, tx.To, tx.Value.ToBigInt())
			}

			// Queue Lock tx - it will validate balance and lock funds atomically
			s.chain.AddTx(protocol.Transaction{
				ID:             uuid.New().String(),
				TxType:         protocol.TxTypeLock,
				CrossShardTxID: tx.ID,
				From:           tx.From,
				To:             tx.To,
				Value:          tx.Value,
				IsCrossShard:   true,
				RwSet:          localRwSet, // May be empty for source-only involvement
			})
			log.Printf("Shard %d: Queued Lock tx for source %s (value=%s)",
				s.shardID, tx.ID, tx.Value.ToBigInt().String())

		} else if len(localRwSet) > 0 {
			// Destination shard: queue Lock tx for ReadSet validation
			// V2 Optimistic: Don't lock addresses now - Lock tx validates+locks atomically

			// Store pending credit ONLY for tx.To address
			toShard := AddressToShard(tx.To)
			if tx.Value.ToBigInt().Sign() > 0 && toShard == s.shardID {
				s.chain.StorePendingCredit(tx.ID, tx.To, tx.Value.ToBigInt())
				log.Printf("Shard %d: Pending credit %s for %s (value=%s)",
					s.shardID, tx.ID, tx.To.Hex(), tx.Value.ToBigInt().String())
			}

			// If this is a contract call (has calldata), store for execution on commit
			if len(tx.Data) > 0 {
				s.chain.StorePendingCall(&tx)
				log.Printf("Shard %d: Stored pending call %s for execution on commit", s.shardID, tx.ID)
			}

			// Queue Lock tx - it will validate ReadSet and lock slots atomically
			s.chain.AddTx(protocol.Transaction{
				ID:             uuid.New().String(),
				TxType:         protocol.TxTypeLock,
				CrossShardTxID: tx.ID,
				From:           tx.From,
				To:             tx.To,
				IsCrossShard:   true,
				RwSet:          localRwSet,
			})
			log.Printf("Shard %d: Queued Lock tx for destination %s (%d RwSet entries)",
				s.shardID, tx.ID, len(localRwSet))
		}
	}

	// Update the last processed orchestrator height (for crash recovery)
	s.chain.SetLastOrchestratorHeight(block.Height)
}

// StateFetchRequest is the request format for /state/fetch
type StateFetchRequest struct {
	Address common.Address `json:"address"`
}

// StateFetchResponse is the response format for /state/fetch
type StateFetchResponse struct {
	Success  bool        `json:"success"`
	Error    string      `json:"error,omitempty"`
	Balance  *big.Int    `json:"balance"`
	Nonce    uint64      `json:"nonce"`
	Code     []byte      `json:"code,omitempty"`
	CodeHash common.Hash `json:"code_hash"`
}

// handleStateFetch returns account state WITHOUT acquiring locks.
// V2 Optimistic Locking: State is read speculatively for simulation.
// Validation and locking happens later during Lock tx execution.
func (s *Server) handleStateFetch(w http.ResponseWriter, r *http.Request) {
	var req StateFetchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Get current account state (read-only, no lock)
	acctState := s.evmState.GetAccountState(req.Address)

	log.Printf("Shard %d: Fetched state for %s (balance=%s, nonce=%d)",
		s.shardID, req.Address.Hex(), acctState.Balance.String(), acctState.Nonce)

	json.NewEncoder(w).Encode(StateFetchResponse{
		Success:  true,
		Balance:  acctState.Balance,
		Nonce:    acctState.Nonce,
		Code:     acctState.Code,
		CodeHash: acctState.CodeHash,
	})
}

// handleRwSet handles V2.2 RwSetRequest - simulates a sub-call and returns the RwSet
// This is called by the Orchestrator when it encounters a NoStateError during simulation
func (s *Server) handleRwSet(w http.ResponseWriter, r *http.Request) {
	var req protocol.RwSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Shard %d: RwSetRequest for tx %s, contract %s",
		s.shardID, req.TxID, req.Address.Hex())

	// Verify the target address belongs to this shard (first hex digit)
	targetShard := AddressToShard(req.Address)
	if targetShard != s.shardID {
		log.Printf("Shard %d: RwSetRequest for address %s belongs to shard %d",
			s.shardID, req.Address.Hex(), targetShard)
		json.NewEncoder(w).Encode(protocol.RwSetReply{
			Success: false,
			Error:   fmt.Sprintf("address %s belongs to shard %d, not %d", req.Address.Hex(), targetShard, s.shardID),
		})
		return
	}

	// Get value from request (default to 0)
	value := big.NewInt(0)
	if req.Value != nil && req.Value.Int != nil {
		value = req.Value.ToBigInt()
	}

	// Use default gas if not specified
	gas := uint64(1_000_000)

	// Update reference block to point to this shard
	refBlock := req.ReferenceBlock
	refBlock.ShardNum = s.shardID

	// Simulate the sub-call and build RwSet
	rwSet, gasUsed, err := s.evmState.SimulateCallForRwSet(
		req.Caller,
		req.Address,
		req.Data,
		value,
		gas,
		refBlock,
	)

	if err != nil {
		log.Printf("Shard %d: RwSetRequest simulation failed for tx %s: %v",
			s.shardID, req.TxID, err)
		json.NewEncoder(w).Encode(protocol.RwSetReply{
			Success: false,
			Error:   err.Error(),
			GasUsed: gasUsed,
		})
		return
	}

	log.Printf("Shard %d: RwSetRequest succeeded for tx %s, %d RwSet entries, gas=%d",
		s.shardID, req.TxID, len(rwSet), gasUsed)

	json.NewEncoder(w).Encode(protocol.RwSetReply{
		Success: true,
		RwSet:   rwSet,
		GasUsed: gasUsed,
	})
}

// TxSubmitRequest is the unified transaction submission format
// Users submit here without knowing if tx is cross-shard or not
type TxSubmitRequest struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Value string `json:"value"`
	Data  string `json:"data"` // hex encoded calldata (optional)
	Gas   uint64 `json:"gas"`
}

const (
	MinGasLimit        = 21_000     // Minimum gas for a basic transfer (Ethereum standard)
	DefaultGasLimit    = 1_000_000  // Default gas limit for transactions
	MaxGasLimit        = 30_000_000 // Maximum gas limit per transaction
	SimulationGasLimit = 3_000_000  // Higher gas limit for simulation
)

// NumShards is loaded from config at init time
var NumShards = 6 // Default value, overwritten by init()

func init() {
	// Load NumShards from config
	if cfg, err := config.LoadDefault(); err == nil && cfg.ShardNum > 0 {
		NumShards = cfg.ShardNum
	}
}

// AddressToShard determines which shard owns an address based on the first hex digit.
// The first character after '0x' directly encodes the shard number (0-7).
// This makes addresses human-readable: 0x0... = shard 0, 0x3... = shard 3, etc.
func AddressToShard(addr common.Address) int {
	// Get hex string and extract first character after 0x prefix
	hex := addr.Hex()[2:] // Remove "0x"
	firstChar := hex[0]

	// Parse hex digit: '0'-'9' -> 0-9, 'a'-'f' -> 10-15, 'A'-'F' -> 10-15
	var digit int
	if firstChar >= '0' && firstChar <= '9' {
		digit = int(firstChar - '0')
	} else if firstChar >= 'a' && firstChar <= 'f' {
		digit = int(firstChar - 'a' + 10)
	} else if firstChar >= 'A' && firstChar <= 'F' {
		digit = int(firstChar - 'A' + 10)
	}

	// For shards 0-7, the first digit directly indicates the shard
	// Addresses starting with 8-f are not used in our system
	return digit
}

// handleTxSubmit is the unified transaction endpoint
// It auto-detects whether a tx is cross-shard and routes accordingly
func (s *Server) handleTxSubmit(w http.ResponseWriter, r *http.Request) {
	var req TxSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	from := common.HexToAddress(req.From)
	to := common.HexToAddress(req.To)
	data := common.FromHex(req.Data)
	value := big.NewInt(0)
	if req.Value != "" {
		var ok bool
		value, ok = new(big.Int).SetString(req.Value, 10)
		if !ok {
			http.Error(w, "invalid value", http.StatusBadRequest)
			return
		}
	}
	gas := req.Gas
	if gas == 0 {
		gas = DefaultGasLimit
	}

	// Validate gas limits
	if gas < MinGasLimit {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("gas limit %d below minimum %d", gas, MinGasLimit),
		})
		return
	}
	if gas > MaxGasLimit {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("gas limit %d exceeds maximum %d", gas, MaxGasLimit),
		})
		return
	}

	// Check which shard the sender is on (first hex digit)
	fromShard := AddressToShard(from)
	if fromShard != s.shardID {
		// Sender is on a different shard - reject
		http.Error(w, fmt.Sprintf("sender %s belongs to shard %d, not shard %d", from.Hex(), fromShard, s.shardID), http.StatusBadRequest)
		return
	}

	// Quick check: is 'to' address on another shard? (first hex digit)
	toShard := AddressToShard(to)

	// Check if 'to' is a contract (has code)
	toCode := s.evmState.GetCode(to)
	isContract := len(toCode) > 0

	var isCrossShard bool
	var crossShardAddrs map[common.Address]int
	var simulationErr error

	if toShard != s.shardID {
		// Simple case: recipient is on another shard
		isCrossShard = true
		crossShardAddrs = map[common.Address]int{to: toShard}
		log.Printf("Shard %d: Tx to %s detected as cross-shard (recipient on shard %d)",
			s.shardID, to.Hex(), toShard)
	} else if isContract && len(data) > 0 {
		// Contract call on this shard - simulate to check for cross-shard access
		log.Printf("Shard %d: Simulating contract call to %s to detect cross-shard access",
			s.shardID, to.Hex())

		// Use higher gas limit for simulation to avoid false positives
		simGas := gas
		if simGas < SimulationGasLimit {
			simGas = SimulationGasLimit
		}

		_, accessedAddrs, hasCrossShard, simErr := s.evmState.SimulateCall(from, to, data, value, simGas, s.shardID, NumShards)

		if simErr != nil {
			// Classify the error to determine if it's cross-shard or a real error
			errStr := simErr.Error()

			// These errors indicate the tx would fail locally too - don't forward
			if isDefiniteLocalError(errStr) {
				log.Printf("Shard %d: Simulation failed with local error: %v", s.shardID, simErr)
				simulationErr = simErr
			} else {
				// Unknown error or potential cross-shard access issue - forward to orchestrator
				log.Printf("Shard %d: Simulation failed (%v), forwarding to orchestrator", s.shardID, simErr)
				isCrossShard = true
				crossShardAddrs = map[common.Address]int{to: toShard}
			}
		} else if hasCrossShard {
			isCrossShard = true
			// Build map of cross-shard addresses (using first hex digit)
			crossShardAddrs = make(map[common.Address]int)
			for _, addr := range accessedAddrs {
				addrShard := AddressToShard(addr)
				if addrShard != s.shardID {
					crossShardAddrs[addr] = addrShard
				}
			}
			log.Printf("Shard %d: Simulation detected %d cross-shard addresses",
				s.shardID, len(crossShardAddrs))
		}
	}

	// If simulation detected a definite local error, return it
	if simulationErr != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":     false,
			"error":       simulationErr.Error(),
			"cross_shard": false,
		})
		return
	}

	if isCrossShard {
		// Forward to orchestrator
		s.forwardToOrchestrator(w, from, to, value, data, gas, crossShardAddrs)
		return
	}

	// Local transaction - pre-validate balance for simple transfers
	// NOTE: This is a best-effort check. There's a TOCTOU race condition where
	// balance could change between this check and actual execution in ProduceBlock
	// (e.g., multiple concurrent transactions). Transactions that pass this check
	// but fail during execution will be reverted and excluded from the block.
	// This early check improves UX by rejecting obviously invalid transactions.
	if value.Sign() > 0 {
		lockedAmount := s.chain.GetLockedAmountForAddress(from)
		if !s.evmState.CanDebit(from, value, lockedAmount) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":     false,
				"error":       "insufficient balance",
				"cross_shard": false,
			})
			return
		}
	}

	// Queue for next block
	txID := uuid.New().String()
	log.Printf("Shard %d: Queued local tx %s (from=%s, to=%s)", s.shardID, txID, from.Hex(), to.Hex())
	s.chain.AddTx(protocol.Transaction{
		ID:           txID,
		From:         from,
		To:           to,
		Value:        protocol.NewBigInt(new(big.Int).Set(value)),
		Gas:          gas,
		Data:         data,
		IsCrossShard: false,
	})
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":     true,
		"tx_id":       txID,
		"status":      "queued",
		"cross_shard": false,
	})
}

// forwardToOrchestrator sends a cross-shard tx to the orchestrator
//
// Note on RwSet: The RwSet we construct here is minimal - it only contains the
// addresses detected during local simulation (or just the recipient for simple
// transfers). The Orchestrator will perform its own complete EVM simulation with
// proper state fetching to build the full RwSet with ReadSet/WriteSet populated.
// This is intentional: local simulation lacks cross-shard state, so we can only
// detect which addresses are accessed, not their actual read/write values.
func (s *Server) forwardToOrchestrator(w http.ResponseWriter, from, to common.Address, value *big.Int, data []byte, gas uint64, crossShardAddrs map[common.Address]int) {
	// Build RwSet from detected cross-shard addresses
	// Note: ReadSet/WriteSet are empty - Orchestrator will re-simulate with full state
	rwSet := make([]protocol.RwVariable, 0, len(crossShardAddrs))
	for addr, shardID := range crossShardAddrs {
		rwSet = append(rwSet, protocol.RwVariable{
			Address: addr,
			ReferenceBlock: protocol.Reference{
				ShardNum: shardID,
			},
		})
	}

	tx := protocol.CrossShardTx{
		ID:        uuid.New().String(),
		FromShard: s.shardID,
		From:      from,
		To:        to,
		Value:     protocol.NewBigInt(value),
		Gas:       gas,
		Data:      data,
		RwSet:     rwSet,
	}

	txData, _ := json.Marshal(tx)
	httpReq, _ := http.NewRequest("POST", s.orchestrator+"/cross-shard/call", bytes.NewBuffer(txData))
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		http.Error(w, "orchestrator unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("Shard %d: Failed to decode orchestrator response: %v", s.shardID, err)
		http.Error(w, "failed to decode orchestrator response: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Add cross_shard indicator
	result["cross_shard"] = true

	log.Printf("Shard %d: Forwarded cross-shard tx %s to orchestrator", s.shardID, tx.ID)
	json.NewEncoder(w).Encode(result)
}

// isDefiniteLocalError checks if an error is definitely a local execution error
// that would fail regardless of cross-shard state (vs errors that might be caused
// by missing cross-shard data like contracts on other shards)
//
// IMPORTANT: We are VERY conservative here. Most EVM errors during contract
// execution should be forwarded to the orchestrator because:
// - The target address might be a contract on another shard (no code locally)
// - A contract might call another contract on a different shard
// - "execution reverted" could be due to missing cross-shard state
//
// Only errors about the SENDER (who is always on the local shard) are definite.
func isDefiniteLocalError(errStr string) bool {
	// Only errors about the sender are definite local failures.
	// The sender's balance and nonce are always on their home shard.
	localErrors := []string{
		"insufficient balance", // Sender doesn't have enough funds
		"insufficient funds",   // Same as above
		"nonce too low",        // Sender's nonce is wrong
		"nonce too high",       // Sender's nonce is wrong
	}
	// NOTE: We intentionally do NOT include:
	// - "execution reverted" - contract might be on another shard
	// - "invalid opcode" - might be calling non-existent cross-shard contract
	// - "out of gas" - might succeed with proper cross-shard state
	// - Other EVM errors - could be caused by missing cross-shard state
	for _, e := range localErrors {
		if len(errStr) >= len(e) && containsIgnoreCase(errStr, e) {
			return true
		}
	}
	return false
}

// containsIgnoreCase checks if s contains substr (case-insensitive)
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// V2 Optimistic Locking: RwVariable validation is now done in chain.validateAndLockReadSetLocked
// which atomically validates ReadSet values and acquires slot locks during Lock tx execution.
