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
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/sharding-experiment/sharding/evm"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

const (
	BlockProductionInterval = 3 * time.Second
)

// Server handles HTTP requests for a shard node
type Server struct {
	shardID      int
	stateDB      *state.StateDB
	chain        *Chain
	orchestrator string
	router       *mux.Router
	receipts     *ReceiptStore
}

func NewServer(shardID int, orchestratorURL string) *Server {
	stateDB, err := evm.NewTestState(shardID)
	if err != nil {
		log.Fatalf("Failed to create EVM state: %v", err)
	}

	s := &Server{
		shardID:      shardID,
		stateDB:      stateDB,
		chain:        NewChain(),
		orchestrator: orchestratorURL,
		router:       mux.NewRouter(),
		receipts:     NewReceiptStore(),
	}
	s.setupRoutes()
	go s.blockProducer() // Start block production
	return s
}

// NewServerForTest creates a server without starting the block producer (for testing)
func NewServerForTest(shardID int, orchestratorURL string) *Server {
	stateDB, err := evm.NewTestState(shardID)
	if err != nil {
		log.Fatalf("Failed to create EVM state: %v", err)
	}

	s := &Server{
		shardID:      shardID,
		stateDB:      stateDB,
		chain:        NewChain(),
		orchestrator: orchestratorURL,
		router:       mux.NewRouter(),
		receipts:     NewReceiptStore(),
	}
	s.setupRoutes()
	// Note: block producer not started for testing
	return s
}

// blockProducer creates State Shard blocks periodically
func (s *Server) blockProducer() {
	ticker := time.NewTicker(BlockProductionInterval)
	defer ticker.Stop()

	for range ticker.C {
		root := s.stateDB.IntermediateRoot(false)
		block := s.chain.ProduceBlock(root)
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
	s.router.HandleFunc("/evm/storage/{address}/{slot}", s.handleGetStorage).Methods("GET")
	s.router.HandleFunc("/evm/stateroot", s.handleGetStateRoot).Methods("GET")

	// Cross-shard endpoints (called by orchestrator)
	s.router.HandleFunc("/cross-shard/prepare", s.handlePrepare).Methods("POST")
	s.router.HandleFunc("/cross-shard/commit", s.handleCommit).Methods("POST")
	s.router.HandleFunc("/cross-shard/abort", s.handleAbort).Methods("POST")
	s.router.HandleFunc("/cross-shard/credit", s.handleCredit).Methods("POST")

	// Cross-shard transfer initiation
	s.router.HandleFunc("/cross-shard/transfer", s.handleCrossShardTransfer).Methods("POST")

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
	balance := s.stateDB.GetBalance(addr)
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
	if err := evm.Debit(s.stateDB, from, amount); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Credit to receiver
	evm.Credit(s.stateDB, to, amount)

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
	evm.Credit(s.stateDB, addr, amount)

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
	lockedAmount := s.chain.GetLockedAmountForAddress(req.Address)
	canCommit := s.evmState.CanDebit(req.Address, req.Amount, lockedAmount)

	if canCommit {
		// Reserve funds (no debit yet - that happens on commit)
		s.chain.LockFunds(req.TxID, req.Address, req.Amount)
	}

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
	evm.Credit(s.stateDB, addr, amount)

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
		Value:     amount,
		RwSet: []protocol.RwVariable{
			{
				Address: toAddr,
				ReferenceBlock: protocol.Reference{
					ShardNum: req.ToShard,
				},
				// WriteSet indicates this address receives funds
			},
		},
	}

	txData, _ := json.Marshal(tx)
	resp, err := http.Post(
		s.orchestrator+"/cross-shard/submit",
		"application/json",
		bytes.NewBuffer(txData),
	)
	if err != nil {
		http.Error(w, "orchestrator unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
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

	contractAddr, returnData, gasUsed, err := evm.DeployContract(s.chain.height, s.stateDB, from, bytecode, value, gas)
	if err != nil {
		log.Printf("Shard %d: Deploy failed: %v", s.shardID, err)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  false,
			"error":    err.Error(),
			"gas_used": gasUsed,
		})
		return
	}

	log.Printf("Shard %d: Contract deployed at %s", s.shardID, contractAddr.Hex())
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"address":  contractAddr.Hex(),
		"return":   common.Bytes2Hex(returnData),
		"gas_used": gasUsed,
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

	ret, gasUsed, err := evm.CallContract(s.chain.height, s.stateDB, from, to, data, value, gas)
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

	ret, gasUsed, err := evm.StaticCall(s.chain.height, s.stateDB, from, to, data, gas)
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
	code := s.stateDB.GetCode(addr)

	json.NewEncoder(w).Encode(map[string]string{
		"address": addr.Hex(),
		"code":    common.Bytes2Hex(code),
	})
}

func (s *Server) handleGetStorage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	addr := common.HexToAddress(vars["address"])
	slot := common.HexToHash(vars["slot"])
	value := s.stateDB.GetState(addr, slot)

	json.NewEncoder(w).Encode(map[string]string{
		"address": addr.Hex(),
		"slot":    slot.Hex(),
		"value":   value.Hex(),
	})
}

func (s *Server) handleGetStateRoot(w http.ResponseWriter, r *http.Request) {
	root := s.chain.blocks[s.chain.height].StateRoot
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

		// Destination shard: handle pending credits
		if credit, ok := s.chain.GetPendingCredit(txID); ok {
			if committed {
				// Commit: apply the credit
				evm.Credit(s.stateDB, credit.Address, credit.Amount)
				log.Printf("Shard %d: Committed %s - credited %s to %s",
					s.shardID, txID, credit.Amount.String(), credit.Address.Hex())
			} else {
				log.Printf("Shard %d: Aborted %s (destination - no credit)", s.shardID, txID)
			}
			s.chain.ClearPendingCredit(txID)
		}
	}

	// Phase 2: Process CtToOrder (new cross-shard txs)
	for _, tx := range block.CtToOrder {
		// Source shard: validate available balance, lock, and vote
		if tx.FromShard == s.shardID {
			s.chain.AddTx(tx.ID, true)

			// Lock-only: check available balance (balance - already locked)
			lockedAmount := s.chain.GetLockedAmountForAddress(tx.From)
			canCommit := s.evmState.CanDebit(tx.From, tx.Value, lockedAmount)

			if canCommit {
				// Reserve funds (no debit yet - that happens on commit)
				s.chain.LockFunds(tx.ID, tx.From, tx.Value)
			}

			s.chain.AddPrepareResult(tx.ID, canCommit)
			log.Printf("Shard %d: Prepare %s = %v (lock %s from %s)",
				s.shardID, tx.ID, canCommit, tx.Value.String(), tx.From.Hex())
		}

		// Destination shard(s): store pending credits from RwSet
		// Each RwVariable with our shard in ReferenceBlock gets a pending credit
		for _, rw := range tx.RwSet {
			if rw.ReferenceBlock.ShardNum == s.shardID {
				s.chain.AddTx(tx.ID, true)
				// For simple transfers, Value is split among recipients
				// For now, assume single recipient gets full Value
				s.chain.StorePendingCredit(tx.ID, rw.Address, tx.Value)
				log.Printf("Shard %d: Pending credit %s for %s",
					s.shardID, tx.ID, rw.Address.Hex())
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
