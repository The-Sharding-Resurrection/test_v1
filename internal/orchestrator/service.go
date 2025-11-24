package orchestrator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

const (
	HTTPClientTimeout    = 10 * time.Second
	BlockProductionInterval = 3 * time.Second
)

// Service coordinates cross-shard transactions
type Service struct {
	router     *mux.Router
	numShards  int
	pending    map[string]*protocol.CrossShardTx
	mu         sync.RWMutex
	httpClient *http.Client
	chain      *ContractChain
}

func NewService(numShards int) *Service {
	s := &Service{
		router:    mux.NewRouter(),
		numShards: numShards,
		pending:   make(map[string]*protocol.CrossShardTx),
		httpClient: &http.Client{
			Timeout: HTTPClientTimeout,
		},
		chain: NewContractChain(),
	}
	s.setupRoutes()
	go s.blockProducer() // Start block production
	return s
}

// blockProducer creates Contract Shard blocks periodically
func (s *Service) blockProducer() {
	ticker := time.NewTicker(BlockProductionInterval)
	defer ticker.Stop()

	for range ticker.C {
		block := s.chain.ProduceBlock()
		log.Printf("Contract Shard: Produced block %d with %d cross-shard txs",
			block.Height, len(block.CtToOrder))

		// Broadcast block to all State Shards
		s.broadcastBlock(block)

		// Execute 2PC for txs in block
		go s.execute2PCBatch(block.CtToOrder)
	}
}

func (s *Service) setupRoutes() {
	s.router.HandleFunc("/cross-shard/submit", s.handleSubmit).Methods("POST")
	s.router.HandleFunc("/cross-shard/status/{txid}", s.handleStatus).Methods("GET")
	s.router.HandleFunc("/state-shard/block", s.handleStateShardBlock).Methods("POST")
	s.router.HandleFunc("/health", s.handleHealth).Methods("GET")
	s.router.HandleFunc("/shards", s.handleShards).Methods("GET")
}

func (s *Service) Start(port int) error {
	addr := fmt.Sprintf(":%d", port)
	log.Printf("Orchestrator starting on %s (managing %d shards)", addr, s.numShards)
	return http.ListenAndServe(addr, s.router)
}

func (s *Service) shardURL(shardID int) string {
	return fmt.Sprintf("http://shard-%d:8545", shardID)
}

func (s *Service) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var tx protocol.CrossShardTx
	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Generate transaction ID
	tx.ID = uuid.New().String()
	tx.Status = protocol.TxPending

	// Validate shards
	if tx.FromShard < 0 || tx.FromShard >= s.numShards || tx.ToShard < 0 || tx.ToShard >= s.numShards {
		http.Error(w, "invalid shard ID", http.StatusBadRequest)
		return
	}

	// Store pending tx
	s.mu.Lock()
	s.pending[tx.ID] = &tx
	s.mu.Unlock()

	log.Printf("Received cross-shard tx %s: shard %d -> shard %d, amount %s",
		tx.ID, tx.FromShard, tx.ToShard, tx.Value.String())

	// Add to Contract Shard chain (will be included in next block)
	s.chain.AddTransaction(protocol.CrossShardTransaction{
		TxID:      tx.ID,
		FromShard: tx.FromShard,
		ToShard:   tx.ToShard,
		From:      tx.From,
		To:        tx.To,
		Value:     tx.Value,
	})

	json.NewEncoder(w).Encode(map[string]string{
		"tx_id":  tx.ID,
		"status": string(tx.Status),
	})
}

func (s *Service) execute2PC(tx *protocol.CrossShardTx) {
	// Phase 1: Prepare (lock funds on source shard)
	prepareReq := protocol.PrepareRequest{
		TxID:    tx.ID,
		Address: tx.From,
		Amount:  tx.Value,
	}

	prepData, _ := json.Marshal(prepareReq)
	resp, err := s.httpClient.Post(
		s.shardURL(tx.FromShard)+"/cross-shard/prepare",
		"application/json",
		bytes.NewBuffer(prepData),
	)

	if err != nil {
		log.Printf("Prepare failed for %s: %v", tx.ID, err)
		s.updateStatus(tx.ID, protocol.TxAborted)
		return
	}
	defer resp.Body.Close()

	var prepResp protocol.PrepareResponse
	json.NewDecoder(resp.Body).Decode(&prepResp)

	if !prepResp.Success {
		log.Printf("Prepare rejected for %s: %s", tx.ID, prepResp.Error)
		s.updateStatus(tx.ID, protocol.TxAborted)
		return
	}

	s.updateStatus(tx.ID, protocol.TxPrepared)
	log.Printf("Prepare succeeded for %s", tx.ID)

	// Phase 2: Commit
	// First, credit the destination shard
	creditReq := map[string]string{
		"tx_id":   tx.ID,
		"address": tx.To.Hex(),
		"amount":  tx.Value.String(),
	}
	creditData, _ := json.Marshal(creditReq)

	resp, err = s.httpClient.Post(
		s.shardURL(tx.ToShard)+"/cross-shard/credit",
		"application/json",
		bytes.NewBuffer(creditData),
	)

	if err != nil {
		log.Printf("Credit failed for %s: %v - aborting", tx.ID, err)
		s.abort(tx)
		return
	}
	resp.Body.Close()

	// Commit on source shard (release lock)
	commitReq := protocol.CommitRequest{TxID: tx.ID}
	commitData, _ := json.Marshal(commitReq)

	resp, err = s.httpClient.Post(
		s.shardURL(tx.FromShard)+"/cross-shard/commit",
		"application/json",
		bytes.NewBuffer(commitData),
	)

	if err != nil {
		log.Printf("Commit failed for %s: %v", tx.ID, err)
		// At this point destination already credited - log inconsistency
		return
	}
	resp.Body.Close()

	s.updateStatus(tx.ID, protocol.TxCommitted)
	log.Printf("Cross-shard tx %s committed successfully", tx.ID)
}

func (s *Service) abort(tx *protocol.CrossShardTx) {
	abortReq := protocol.CommitRequest{TxID: tx.ID}
	abortData, _ := json.Marshal(abortReq)

	resp, err := s.httpClient.Post(
		s.shardURL(tx.FromShard)+"/cross-shard/abort",
		"application/json",
		bytes.NewBuffer(abortData),
	)
	if err != nil {
		log.Printf("Abort failed for %s: %v", tx.ID, err)
		return
	}
	resp.Body.Close()

	s.updateStatus(tx.ID, protocol.TxAborted)
}

func (s *Service) updateStatus(txID string, status protocol.TxStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tx, ok := s.pending[txID]; ok {
		tx.Status = status
	}
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	txID := vars["txid"]

	s.mu.RLock()
	tx, ok := s.pending[txID]
	s.mu.RUnlock()

	if !ok {
		http.Error(w, "transaction not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"tx_id":  tx.ID,
		"status": string(tx.Status),
	})
}

func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (s *Service) handleShards(w http.ResponseWriter, r *http.Request) {
	shards := make([]map[string]interface{}, s.numShards)
	for i := 0; i < s.numShards; i++ {
		shards[i] = map[string]interface{}{
			"id":  i,
			"url": s.shardURL(i),
		}
	}
	json.NewEncoder(w).Encode(shards)
}

// broadcastBlock sends Contract Shard block to all State Shards
func (s *Service) broadcastBlock(block *protocol.ContractShardBlock) {
	blockData, _ := json.Marshal(block)
	for i := 0; i < s.numShards; i++ {
		url := s.shardURL(i) + "/contract-shard/block"
		go func(shardID int) {
			_, err := s.httpClient.Post(url, "application/json", bytes.NewBuffer(blockData))
			if err != nil {
				log.Printf("Failed to send block to shard %d: %v", shardID, err)
			}
		}(i)
	}
}

// execute2PCBatch runs 2PC for all txs in a block
func (s *Service) execute2PCBatch(txs []protocol.CrossShardTransaction) {
	for _, tx := range txs {
		s.execute2PC(&protocol.CrossShardTx{
			ID:        tx.TxID,
			FromShard: tx.FromShard,
			ToShard:   tx.ToShard,
			From:      tx.From,
			To:        tx.To,
			Value:     tx.Value,
		})
	}
}

// handleStateShardBlock receives blocks from State Shards
func (s *Service) handleStateShardBlock(w http.ResponseWriter, r *http.Request) {
	var block protocol.StateShardBlock
	if err := json.NewDecoder(r.Body).Decode(&block); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Contract Shard: Received State Shard block height=%d with %d prepare results",
		block.Height, len(block.TpcPrepare))

	// Collect 2PC prepare results and record for next Contract Shard block
	for txID, canCommit := range block.TpcPrepare {
		s.chain.RecordResult(txID, canCommit)
		s.updateStatus(txID, protocol.TxPrepared)
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
