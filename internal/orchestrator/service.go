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
		log.Printf("Contract Shard: Produced block %d with %d cross-shard txs, %d results",
			block.Height, len(block.CtToOrder), len(block.TpcResult))

		// Update status for txs with results
		for txID, committed := range block.TpcResult {
			if committed {
				s.updateStatus(txID, protocol.TxCommitted)
			} else {
				s.updateStatus(txID, protocol.TxAborted)
			}
		}

		// Broadcast block to all State Shards (they handle prepare and commit/abort)
		s.broadcastBlock(block)
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

	// Generate transaction ID if not provided
	if tx.ID == "" {
		tx.ID = uuid.New().String()
	}
	tx.Status = protocol.TxPending

	// Validate source shard
	if tx.FromShard < 0 || tx.FromShard >= s.numShards {
		http.Error(w, "invalid from_shard ID", http.StatusBadRequest)
		return
	}

	// Validate all target shards from RwSet
	for _, rw := range tx.RwSet {
		if rw.ReferenceBlock.ShardNum < 0 || rw.ReferenceBlock.ShardNum >= s.numShards {
			http.Error(w, "invalid target shard ID in RwSet", http.StatusBadRequest)
			return
		}
	}

	// Store pending tx
	s.mu.Lock()
	s.pending[tx.ID] = &tx
	s.mu.Unlock()

	targetShards := tx.TargetShards()
	log.Printf("Received cross-shard tx %s: shard %d -> shards %v, amount %s",
		tx.ID, tx.FromShard, targetShards, tx.Value.String())

	// Add to Contract Shard chain (will be included in next block)
	s.chain.AddTransaction(tx)

	json.NewEncoder(w).Encode(map[string]string{
		"tx_id":  tx.ID,
		"status": string(tx.Status),
	})
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
	blockData, err := json.Marshal(block)
	if err != nil {
		log.Printf("Failed to marshal Contract Shard block: %v", err)
		return
	}
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

// handleStateShardBlock receives blocks from State Shards
func (s *Service) handleStateShardBlock(w http.ResponseWriter, r *http.Request) {
	var block protocol.StateShardBlock
	if err := json.NewDecoder(r.Body).Decode(&block); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Contract Shard: Received State Shard block height=%d with %d prepare results",
		block.Height, len(block.TpcPrepare))

	// Collect 2PC prepare votes and record for next Contract Shard block
	for txID, canCommit := range block.TpcPrepare {
		if s.chain.RecordVote(txID, canCommit) {
			s.updateStatus(txID, protocol.TxPrepared)
			log.Printf("Contract Shard: Vote received for %s: canCommit=%v", txID, canCommit)
		}
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
