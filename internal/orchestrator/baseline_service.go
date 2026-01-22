package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/sharding-experiment/sharding/config"
	"github.com/sharding-experiment/sharding/internal/network"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

const (
	BaselineBlockInterval = 3 * time.Second
)

// BaselineService implements a stateless orchestrator router for the baseline protocol
// It simply aggregates transactions from State Shards and broadcasts them
type BaselineService struct {
	mu          sync.RWMutex
	router      *mux.Router
	numShards   int
	httpClient  *http.Client
	pendingTxs  map[string]*protocol.Transaction // txID -> tx
	blockHeight uint64
}

// NewBaselineService creates a new baseline orchestrator service
func NewBaselineService(numShards int, networkConfig config.NetworkConfig) (*BaselineService, error) {
	s := &BaselineService{
		router:      mux.NewRouter(),
		numShards:   numShards,
		httpClient:  network.NewHTTPClient(networkConfig, 10*time.Second),
		pendingTxs:  make(map[string]*protocol.Transaction),
		blockHeight: 0,
	}

	s.setupRoutes()
	go s.blockProducer() // Start block production
	return s, nil
}

// setupRoutes configures HTTP endpoints
func (s *BaselineService) setupRoutes() {
	// State Shard block submission
	s.router.HandleFunc("/state-shard/block", s.handleStateShardBlock).Methods("POST")

	// Health check
	s.router.HandleFunc("/health", s.handleHealth).Methods("GET")
	s.router.HandleFunc("/info", s.handleInfo).Methods("GET")
}

// Router returns the HTTP router
func (s *BaselineService) Router() *mux.Router {
	return s.router
}

// handleStateShardBlock receives blocks from State Shards
func (s *BaselineService) handleStateShardBlock(w http.ResponseWriter, r *http.Request) {
	var block protocol.StateShardBlock
	if err := json.NewDecoder(r.Body).Decode(&block); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Orchestrator (Baseline): Received block from shard %d with %d txs",
		block.ShardID, len(block.TxOrdering))

	// Aggregate transactions (Phase 2: Data Availability)
	s.mu.Lock()
	for _, tx := range block.TxOrdering {
		if tx.IsCrossShard {
			// Store or update cross-shard transaction
			s.pendingTxs[tx.ID] = &tx
			log.Printf("Orchestrator (Baseline): Aggregated tx %s (status=%d, target=%d)",
				tx.ID, tx.CtStatus, tx.TargetShard)
		}
	}
	s.mu.Unlock()

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleHealth returns health status
func (s *BaselineService) handleHealth(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

// handleInfo returns orchestrator info
func (s *BaselineService) handleInfo(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"mode":        "baseline",
		"num_shards":  s.numShards,
		"pending_txs": len(s.pendingTxs),
		"height":      s.blockHeight,
	})
}

// blockProducer creates and broadcasts Orchestrator blocks periodically
func (s *BaselineService) blockProducer() {
	ticker := time.NewTicker(BaselineBlockInterval)
	defer ticker.Stop()

	for range ticker.C {
		block := s.produceBlock()
		if block != nil {
			s.broadcastBlock(block)
		}
	}
}

// produceBlock creates an Orchestrator block with aggregated transactions
func (s *BaselineService) produceBlock() *protocol.OrchestratorShardBlock {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.pendingTxs) == 0 {
		// No pending transactions - skip block production
		return nil
	}

	// Collect all pending transactions
	var ctToProcess []protocol.Transaction
	for _, tx := range s.pendingTxs {
		ctToProcess = append(ctToProcess, tx.DeepCopy())
	}

	// Clear pending transactions (they'll be resubmitted if still pending)
	s.pendingTxs = make(map[string]*protocol.Transaction)

	s.blockHeight++
	block := &protocol.OrchestratorShardBlock{
		Height:      s.blockHeight,
		Timestamp:   uint64(time.Now().Unix()),
		CtToProcess: ctToProcess,
	}

	log.Printf("Orchestrator (Baseline): Produced block %d with %d txs to process",
		block.Height, len(ctToProcess))

	return block
}

// broadcastBlock sends the Orchestrator block to all State Shards
func (s *BaselineService) broadcastBlock(block *protocol.OrchestratorShardBlock) {
	blockData, err := json.Marshal(block)
	if err != nil {
		log.Printf("Orchestrator (Baseline): Failed to marshal block: %v", err)
		return
	}

	// Broadcast to all shards in parallel
	var wg sync.WaitGroup
	for shardID := 0; shardID < s.numShards; shardID++ {
		wg.Add(1)
		go func(sid int) {
			defer wg.Done()

			url := getShardURL(sid) + "/orchestrator-shard/block"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(blockData))
			req.Header.Set("Content-Type", "application/json")

			resp, err := s.httpClient.Do(req)
			if err != nil {
				log.Printf("Orchestrator (Baseline): Failed to send block to shard %d: %v", sid, err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				log.Printf("Orchestrator (Baseline): Shard %d returned %d", sid, resp.StatusCode)
			}
		}(shardID)
	}

	wg.Wait()
	log.Printf("Orchestrator (Baseline): Broadcasted block %d to all shards", block.Height)
}

// getShardURL returns the URL for a given shard ID
func getShardURL(shardID int) string {
	// Docker network DNS names: shard-0, shard-1, ..., shard-7
	return fmt.Sprintf("http://shard-%d:8545", shardID)
}

// Close gracefully shuts down the service
func (s *BaselineService) Close() error {
	return nil
}

// Start starts the HTTP server
func (s *BaselineService) Start(port int) error {
	addr := ":8080" // Orchestrator always listens on 8080
	log.Printf("Orchestrator (Baseline) starting on %s", addr)
	return http.ListenAndServe(addr, s.router)
}
