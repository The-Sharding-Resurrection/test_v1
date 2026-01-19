package shard

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/sharding-experiment/sharding/internal/protocol"
)

// Baseline-specific handlers for State Shard server

// handleOrchestratorShardBlockBaseline processes orchestrator blocks in baseline mode
func (s *Server) handleOrchestratorShardBlockBaseline(w http.ResponseWriter, r *http.Request) {
	var block protocol.OrchestratorShardBlock
	if err := json.NewDecoder(r.Body).Decode(&block); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Shard %d (Baseline): Received Orchestrator block %d with %d txs to process",
		s.shardID, block.Height, len(block.CtToProcess))

	// Process baseline orchestrator block (Phase 3: Feedback)
	if s.baselineChain != nil {
		s.baselineChain.ProcessOrchestratorBlock(&block)
	} else {
		log.Printf("Shard %d: Baseline chain not initialized", s.shardID)
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleTxSubmitBaseline handles transaction submission in baseline mode
func (s *Server) handleTxSubmitBaseline(w http.ResponseWriter, r *http.Request) {
	var tx protocol.Transaction
	if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Auto-detect cross-shard by checking target address
	targetShard := AddressToShard(tx.To, NumShards)
	if targetShard != s.shardID {
		// Cross-shard transaction - mark as PENDING
		tx.IsCrossShard = true
		tx.CtStatus = protocol.CtStatusPending
		tx.TargetShard = targetShard
		log.Printf("Shard %d (Baseline): Received cross-shard tx %s (target shard: %d)",
			s.shardID, tx.ID, targetShard)
	} else {
		// Local transaction
		tx.IsCrossShard = false
		log.Printf("Shard %d (Baseline): Received local tx %s", s.shardID, tx.ID)
	}

	// Add to baseline chain mempool
	if s.baselineChain != nil {
		s.baselineChain.AddTransaction(&tx)
	} else {
		log.Printf("Shard %d: Baseline chain not initialized", s.shardID)
		http.Error(w, "baseline chain not initialized", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"tx_id":        tx.ID,
		"is_cross_shard": tx.IsCrossShard,
		"status":       "queued",
	})
}
