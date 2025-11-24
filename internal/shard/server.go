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
	"github.com/gorilla/mux"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

const (
	BlockProductionInterval = 3 * time.Second
)

// Server handles HTTP requests for a shard node
type Server struct {
	shardID      int
	locks        *LockManager
	evmState     *EVMState
	chain        *Chain
	orchestrator string
	router       *mux.Router
	receipts     *ReceiptStore
}

func NewServer(shardID int, orchestratorURL string) *Server {
	evmState, err := NewEVMState()
	if err != nil {
		log.Fatalf("Failed to create EVM state: %v", err)
	}

	s := &Server{
		shardID:      shardID,
		locks:        NewLockManager(),
		evmState:     evmState,
		chain:        NewChain(),
		orchestrator: orchestratorURL,
		router:       mux.NewRouter(),
		receipts:     NewReceiptStore(),
	}
	s.setupRoutes()
	go s.blockProducer() // Start block production
	return s
}

// blockProducer creates State Shard blocks periodically
func (s *Server) blockProducer() {
	ticker := time.NewTicker(BlockProductionInterval)
	defer ticker.Stop()

	for range ticker.C {
		root := s.evmState.GetStateRoot()
		block := s.chain.ProduceBlock(root)
		log.Printf("Shard %d: Produced block %d with %d txs",
			s.shardID, block.Height, len(block.TxOrdering))

		// Send block back to Contract Shard
		s.sendBlockToContractShard(block)
	}
}

// sendBlockToContractShard sends State Shard block to Contract Shard
func (s *Server) sendBlockToContractShard(block *protocol.StateShardBlock) {
	blockData, err := json.Marshal(block)
	if err != nil {
		log.Printf("Shard %d: Failed to marshal State Shard block: %v", s.shardID, err)
		return
	}
	_, err = http.Post(s.orchestrator+"/state-shard/block", "application/json", bytes.NewBuffer(blockData))
	if err != nil {
		log.Printf("Shard %d: Failed to send block to Contract Shard: %v", s.shardID, err)
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
	s.router.HandleFunc("/contract-shard/block", s.handleContractShardBlock).Methods("POST")

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
	amount, _ := new(big.Int).SetString(req.Amount, 10)

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
	amount, _ := new(big.Int).SetString(req.Amount, 10)
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

	// Debit from EVMState and track lock for potential abort
	err := s.evmState.Debit(req.Address, req.Amount)
	if err == nil {
		s.locks.Lock(req.TxID, req.Address, req.Amount)
	}

	resp := protocol.PrepareResponse{
		TxID:    req.TxID,
		Success: err == nil,
	}
	if err != nil {
		resp.Error = err.Error()
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

	// Clear the lock - funds already debited
	_, ok := s.locks.Get(req.TxID)
	if ok {
		s.locks.Clear(req.TxID)
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

	// Return funds to EVMState
	lock, ok := s.locks.Get(req.TxID)
	if ok {
		s.evmState.Credit(lock.Address, lock.Amount)
		s.locks.Clear(req.TxID)
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
	amount, _ := new(big.Int).SetString(req.Amount, 10)
	s.evmState.Credit(addr, amount)

	log.Printf("Shard %d: Credit %s to %s", s.shardID, req.Amount, addr.Hex())
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

type CrossShardTransferRequest struct {
	From    string `json:"from"`
	To      string `json:"to"`
	ToShard int    `json:"to_shard"`
	Amount  string `json:"amount"`
}

func (s *Server) handleCrossShardTransfer(w http.ResponseWriter, r *http.Request) {
	var req CrossShardTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Forward to orchestrator
	amount, _ := new(big.Int).SetString(req.Amount, 10)
	tx := protocol.CrossShardTx{
		FromShard: s.shardID,
		ToShard:   req.ToShard,
		From:      common.HexToAddress(req.From),
		To:        common.HexToAddress(req.To),
		Value:     amount,
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
		value, _ = new(big.Int).SetString(req.Value, 10)
	}
	gas := req.Gas
	if gas == 0 {
		gas = 3_000_000
	}

	contractAddr, returnData, gasUsed, _, err := s.evmState.DeployContract(from, bytecode, value, gas)
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
		value, _ = new(big.Int).SetString(req.Value, 10)
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

// handleContractShardBlock receives blocks from Contract Shard
func (s *Server) handleContractShardBlock(w http.ResponseWriter, r *http.Request) {
	var block protocol.ContractShardBlock
	if err := json.NewDecoder(r.Body).Decode(&block); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Shard %d: Received Contract Shard block %d with %d cross-shard txs",
		s.shardID, block.Height, len(block.CtToOrder))

	// Process cross-shard transactions from block
	for _, tx := range block.CtToOrder {
		// Check if this shard is involved
		if tx.FromShard == s.shardID || tx.ToShard == s.shardID {
			s.chain.AddTx(tx.TxID, true)

			// Validate if we're the source shard
			if tx.FromShard == s.shardID {
				// Check if sender has sufficient balance
				bal := s.evmState.GetBalance(tx.From)
				canCommit := bal.Cmp(tx.Value) >= 0
				s.chain.AddPrepareResult(tx.TxID, canCommit)
				log.Printf("Shard %d: Prepare %s = %v", s.shardID, tx.TxID, canCommit)
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
