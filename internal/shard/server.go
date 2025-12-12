package shard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

const (
	BlockProductionInterval = 3 * time.Second
)

// Server handles HTTP requests for a shard node
type Server struct {
	shardID      int
	evmState     *EVMState
	chain        *Chain
	orchestrator string
	router       *mux.Router
	receipts     *ReceiptStore
}

func NewServer(shardID int, orchestratorURL string) *Server {
	evmState, err := NewEVMState(shardID)
	if err != nil {
		log.Printf("WARNING: Failed to create persistent EVM state for shard %d: %v. Falling back to in-memory state.", shardID, err)
		evmState, err = NewMemoryEVMState()
		if err != nil {
			log.Fatalf("Failed to create in-memory EVM state: %v", err)
		}
	}

	s := &Server{
		shardID:      shardID,
		evmState:     evmState,
		chain:        NewChain(shardID),
		orchestrator: orchestratorURL,
		router:       mux.NewRouter(),
		receipts:     NewReceiptStore(),
	}
	s.setupRoutes()
	go s.blockProducer()                       // Start block production
	s.chain.StartLockCleanup(30 * time.Second) // Start lock cleanup every 30s
	return s
}

// NewServerForTest creates a server without starting the block producer (for testing)
func NewServerForTest(shardID int, orchestratorURL string) *Server {
	evmState, err := NewEVMState(shardID)
	if err != nil {
		log.Printf("WARNING: Failed to create persistent EVM state for shard %d (test mode): %v. Falling back to in-memory state.", shardID, err)
		evmState, err = NewMemoryEVMState()
		if err != nil {
			log.Fatalf("Failed to create in-memory EVM state (test mode): %v", err)
		}
	}

	s := &Server{
		shardID:      shardID,
		evmState:     evmState,
		chain:        NewChain(shardID),
		orchestrator: orchestratorURL,
		router:       mux.NewRouter(),
		receipts:     NewReceiptStore(),
	}
	s.setupRoutes()
	// Note: block producer not started for testing
	return s
}

// Router returns the HTTP router for testing
func (s *Server) Router() *mux.Router {
	return s.router
}

// blockProducer creates State Shard blocks periodically
func (s *Server) blockProducer() {
	ticker := time.NewTicker(BlockProductionInterval)
	defer ticker.Stop()

	for range ticker.C {
		s.chain.ExecutePendingTxs(s.evmState)
		newRoot := s.evmState.Commit(s.chain.height + 1)

		block := s.chain.ProduceBlock(newRoot)
		log.Printf("Shard %d: Produced block %d with %d txs",
			s.shardID, block.Height, len(block.TxOrdering))

		// Send block back to Orchestrator Shard
		s.sendBlockToOrchestratorShard(block)
	}
}

// sendBlockToOrchestratorShard sends State Shard block to Orchestrator Shard
func (s *Server) sendBlockToOrchestratorShard(block *protocol.StateShardBlock) {
	blockData, err := json.Marshal(block)
	if err != nil {
		log.Printf("Shard %d: Failed to marshal State Shard block: %v", s.shardID, err)
		return
	}
	_, err = http.Post(s.orchestrator+"/state-shard/block", "application/json", bytes.NewBuffer(blockData))
	if err != nil {
		log.Printf("Shard %d: Failed to send block to Orchestrator Shard: %v", s.shardID, err)
	}
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

	// State lock endpoints (for simulation/2PC)
	s.router.HandleFunc("/state/lock", s.handleStateLock).Methods("POST")
	s.router.HandleFunc("/state/unlock", s.handleStateUnlock).Methods("POST")

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
	resp, err := http.Post(
		s.orchestrator+"/cross-shard/call",
		"application/json",
		bytes.NewBuffer(txData),
	)
	if err != nil {
		http.Error(w, "orchestrator unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

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

	// Check which shard this contract address maps to
	targetShard := int(contractAddr[len(contractAddr)-1]) % NumShards
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
		resp, err := http.Post(targetURL, "application/json", bytes.NewBuffer(setCodeData))
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

	// Verify this address belongs to this shard
	targetShard := int(addr[len(addr)-1]) % NumShards
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
	value := s.evmState.GetStorageAt(addr, slot)

	json.NewEncoder(w).Encode(map[string]string{
		"address": addr.Hex(),
		"slot":    slot.Hex(),
		"value":   value.Hex(),
	})
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

	// Phase 1: Process TpcResult (commit/abort from previous round)
	for txID, committed := range block.TpcResult {
		// Source shard: handle locked funds
		if lock, ok := s.chain.GetLockedFunds(txID); ok {
			if committed {
				// Lock-only: debit on commit, then clear lock
				s.evmState.Debit(lock.Address, lock.Amount)
				s.chain.ClearLock(txID)
				log.Printf("Shard %d: Committed %s - debited %s from %s",
					s.shardID, txID, lock.Amount.String(), lock.Address.Hex())
			} else {
				// Lock-only: just clear lock (no refund needed)
				s.chain.ClearLock(txID)
				log.Printf("Shard %d: Aborted %s - released lock", s.shardID, txID)
			}
		}

		// Destination shard: handle pending credits (may have multiple recipients)
		if credits, ok := s.chain.GetPendingCredits(txID); ok {
			if committed {
				// Commit: apply all credits
				for _, credit := range credits {
					s.evmState.Credit(credit.Address, credit.Amount)
					log.Printf("Shard %d: Committed %s - credited %s to %s",
						s.shardID, txID, credit.Amount.String(), credit.Address.Hex())
				}
			} else {
				log.Printf("Shard %d: Aborted %s (destination - %d credits discarded)", s.shardID, txID, len(credits))
			}
			s.chain.ClearPendingCredit(txID)
		}

		// Destination shard: apply write sets from simulation
		if pendingCall, ok := s.chain.GetPendingCall(txID); ok {
			if committed {
				// Apply the write set from simulation directly (don't re-execute)
				// Note: ReadSet was validated during prepare phase. Simulation locks
				// prevent conflicting modifications between prepare and commit.
				writeCount := 0
				for _, rw := range pendingCall.RwSet {
					if rw.ReferenceBlock.ShardNum != s.shardID {
						continue
					}
					for _, write := range rw.WriteSet {
						slot := common.Hash(write.Slot)
						newValue := common.BytesToHash(write.NewValue)
						s.evmState.SetStorageAt(rw.Address, slot, newValue)
						writeCount++
					}
				}
				log.Printf("Shard %d: Committed %s - applied %d storage writes",
					s.shardID, txID, writeCount)
			} else {
				log.Printf("Shard %d: Aborted %s (write set discarded)", s.shardID, txID)
			}
			s.chain.ClearPendingCall(txID)
		}

		// Release simulation locks for this transaction (both commit and abort)
		s.chain.UnlockAllForTx(txID)
	}

	// Phase 2: Process CtToOrder (new cross-shard txs)
	for _, tx := range block.CtToOrder {
		// Source shard: validate available balance, lock, and vote
		if tx.FromShard == s.shardID {
			s.chain.AddTx(protocol.Transaction{
				ID:           tx.ID,
				TxHash:       tx.TxHash,
				From:         tx.From,
				To:           tx.To,
				Value:        protocol.NewBigInt(new(big.Int).Set(tx.Value.ToBigInt())),
				Data:         tx.Data,
				IsCrossShard: true,
			})

			// Lock-only: check available balance (balance - already locked)
			// ATOMIC: Hold EVM mutex during check-and-lock to prevent race condition
			s.evmState.mu.Lock()
			lockedAmount := s.chain.GetLockedAmountForAddress(tx.From)
			canCommit := s.evmState.CanDebit(tx.From, tx.Value.ToBigInt(), lockedAmount)
			if canCommit {
				// Reserve funds (no debit yet - that happens on commit)
				s.chain.LockFunds(tx.ID, tx.From, tx.Value.ToBigInt())
			}
			s.evmState.mu.Unlock()

			s.chain.AddPrepareResult(tx.ID, canCommit)
			log.Printf("Shard %d: Prepare %s = %v (lock %s from %s)",
				s.shardID, tx.ID, canCommit, tx.Value.ToBigInt().String(), tx.From.Hex())
		}

		// Process RwSet entries for this shard (destination shard voting)
		// Collect all RwSet validations first, then add a single vote
		rwEntriesForThisShard := 0
		allRwValid := true
		for _, rw := range tx.RwSet {
			if rw.ReferenceBlock.ShardNum != s.shardID {
				continue
			}
			rwEntriesForThisShard++

			// Only add tx once when we first encounter an RwSet entry for this shard
			if rwEntriesForThisShard == 1 {
				s.chain.AddTx(protocol.Transaction{
					ID:           tx.ID,
					TxHash:       tx.TxHash,
					From:         tx.From,
					To:           tx.To,
					Value:        protocol.NewBigInt(new(big.Int).Set(tx.Value.ToBigInt())),
					Data:         tx.Data,
					IsCrossShard: true,
				})
			}

			// Validate simulation lock exists and ReadSet matches
			rwCanCommit := s.validateRwVariable(tx.ID, rw)
			log.Printf("Shard %d: Validate RwSet for %s on %s = %v",
				s.shardID, tx.ID, rw.Address.Hex(), rwCanCommit)

			// Track combined result (AND of all validations)
			if !rwCanCommit {
				allRwValid = false
			}
		}

		// Add single prepare vote for all RwSet entries on this shard
		if rwEntriesForThisShard > 0 {
			s.chain.AddPrepareResult(tx.ID, allRwValid)
			log.Printf("Shard %d: Combined RwSet vote for %s = %v (%d entries)",
				s.shardID, tx.ID, allRwValid, rwEntriesForThisShard)

			// Store pending credit ONLY for tx.To address (not every RwSet entry!)
			// This prevents the "money printer" bug where value gets credited to all touched addresses
			toShard := int(tx.To[len(tx.To)-1]) % NumShards
			if allRwValid && tx.Value.ToBigInt().Sign() > 0 && toShard == s.shardID {
				s.chain.StorePendingCredit(tx.ID, tx.To, tx.Value.ToBigInt())
				log.Printf("Shard %d: Pending credit %s for %s (value=%s)",
					s.shardID, tx.ID, tx.To.Hex(), tx.Value.ToBigInt().String())
			}

			// If this is a contract call (has calldata), store for execution on commit
			if allRwValid && len(tx.Data) > 0 {
				s.chain.StorePendingCall(&tx)
				log.Printf("Shard %d: Stored pending call %s for execution on commit", s.shardID, tx.ID)
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleStateLock locks an address and returns full account state
func (s *Server) handleStateLock(w http.ResponseWriter, r *http.Request) {
	var req protocol.LockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Get current account state
	acctState := s.evmState.GetAccountState(req.Address)

	// Try to lock the address
	// For PoC, we pass empty storage - Orchestrator will fetch slots on-demand
	emptyStorage := make(map[common.Hash]common.Hash)
	err := s.chain.LockAddress(
		req.TxID,
		req.Address,
		acctState.Balance,
		acctState.Nonce,
		acctState.Code,
		acctState.CodeHash,
		emptyStorage,
	)

	if err != nil {
		log.Printf("Shard %d: Lock failed for %s on tx %s: %v",
			s.shardID, req.Address.Hex(), req.TxID, err)
		json.NewEncoder(w).Encode(protocol.LockResponse{
			Success: false,
			Error:   err.Error(),
		})
		return
	}

	log.Printf("Shard %d: Locked address %s for tx %s",
		s.shardID, req.Address.Hex(), req.TxID)

	json.NewEncoder(w).Encode(protocol.LockResponse{
		Success:  true,
		Balance:  acctState.Balance,
		Nonce:    acctState.Nonce,
		Code:     acctState.Code,
		CodeHash: acctState.CodeHash,
		Storage:  emptyStorage, // Fetched on-demand during simulation
	})
}

// handleStateUnlock releases a lock on an address
func (s *Server) handleStateUnlock(w http.ResponseWriter, r *http.Request) {
	var req protocol.UnlockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.chain.UnlockAddress(req.TxID, req.Address)

	log.Printf("Shard %d: Unlocked address %s for tx %s",
		s.shardID, req.Address.Hex(), req.TxID)

	json.NewEncoder(w).Encode(protocol.UnlockResponse{
		Success: true,
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
	NumShards          = 6         // TODO: make configurable
	DefaultGasLimit    = 1_000_000 // Default gas limit for transactions
	SimulationGasLimit = 3_000_000 // Higher gas limit for simulation
)

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

	// Check which shard the sender is on
	fromShard := int(from[len(from)-1]) % NumShards
	if fromShard != s.shardID {
		// Sender is on a different shard - reject
		http.Error(w, fmt.Sprintf("sender %s belongs to shard %d, not shard %d", from.Hex(), fromShard, s.shardID), http.StatusBadRequest)
		return
	}

	// Quick check: is 'to' address on another shard?
	toShard := int(to[len(to)-1]) % NumShards

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
			// Build map of cross-shard addresses
			crossShardAddrs = make(map[common.Address]int)
			for _, addr := range accessedAddrs {
				addrShard := int(addr[len(addr)-1]) % NumShards
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

	// Local execution
	log.Printf("Shard %d: Add local tx (from=%s, to=%s)", s.shardID, from.Hex(), to.Hex())
	s.chain.AddTx(protocol.Transaction{
		ID:           uuid.New().String(),
		From:         from,
		To:           to,
		Value:        protocol.NewBigInt(new(big.Int).Set(value)),
		Data:         data,
		IsCrossShard: false,
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
	resp, err := http.Post(
		s.orchestrator+"/cross-shard/call",
		"application/json",
		bytes.NewBuffer(txData),
	)
	if err != nil {
		http.Error(w, "orchestrator unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

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
	sLower := make([]byte, len(s))
	substrLower := make([]byte, len(substr))
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			sLower[i] = s[i] + 32
		} else {
			sLower[i] = s[i]
		}
	}
	for i := 0; i < len(substr); i++ {
		if substr[i] >= 'A' && substr[i] <= 'Z' {
			substrLower[i] = substr[i] + 32
		} else {
			substrLower[i] = substr[i]
		}
	}
	return bytesContains(sLower, substrLower)
}

// bytesContains checks if haystack contains needle
func bytesContains(haystack, needle []byte) bool {
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// validateRwVariable validates a RwVariable against the locked/current state
// Returns true if the ReadSet matches and the address is properly locked
// If the simulation lock has expired (TTL exceeded), returns false to abort the tx
func (s *Server) validateRwVariable(txID string, rw protocol.RwVariable) bool {
	// Check if simulation lock exists for this address and tx
	lock, ok := s.chain.GetSimulationLockByAddr(rw.Address)
	if !ok {
		// Lock is missing (possibly expired or never acquired) - abort the transaction
		// SECURITY: Do NOT allow bypassing lock validation for any transaction
		log.Printf("Shard %d: No simulation lock for %s (tx %s) - aborting",
			s.shardID, rw.Address.Hex(), txID)
		return false
	}

	// Verify lock belongs to this tx
	if lock.TxID != txID {
		log.Printf("Shard %d: Lock for %s belongs to %s, not %s",
			s.shardID, rw.Address.Hex(), lock.TxID, txID)
		return false
	}

	// Validate ReadSet - each read value must match current state
	for _, item := range rw.ReadSet {
		slot := common.Hash(item.Slot)
		expectedValue := common.BytesToHash(item.Value)

		// Get current storage value
		actualValue := s.evmState.GetStorageAt(rw.Address, slot)

		if actualValue != expectedValue {
			log.Printf("Shard %d: ReadSet mismatch for %s slot %s: expected %s, got %s",
				s.shardID, rw.Address.Hex(), slot.Hex(),
				expectedValue.Hex(), actualValue.Hex())
			return false
		}
	}

	return true
}
