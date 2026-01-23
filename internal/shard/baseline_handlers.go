package shard

import (
	"encoding/json"
	"log"
	"math/big"
	"net/http"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/sharding-experiment/sharding/config"
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

	tx := protocol.Transaction{
		ID:    uuid.New().String(),
		From:  from,
		To:    to,
		Value: protocol.NewBigInt(value),
		Gas:   gas,
		Data:  data,
	}

	// Simulate Cross-shard transaction
	_, _, targetShard, err := s.evmState.ExecuteBaselineTx(&tx, s.shardID, config.GetConfig().ShardNum, true)
	log.Printf("target shard: %d", targetShard)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		// return
	}
	if targetShard != -1 || err != nil {
		// Cross-shard transaction - mark as PENDING
		tx.IsCrossShard = true
		tx.CtStatus = protocol.CtStatusPending
		tx.TargetShard = -1
		log.Printf("Shard %d (Baseline): Received cross-shard tx %s (target shard: %d)",
			s.shardID, tx.ID, targetShard)
	} else {
		// Local transaction
		tx.IsCrossShard = false
		log.Printf("Shard %d (Baseline): Received local tx %s", s.shardID, tx.ID)
	}

	// Add to baseline chain mempool
	s.baselineChain.AddTransaction(&tx)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"tx_id":          tx.ID,
		"is_cross_shard": tx.IsCrossShard,
		"status":         "queued",
	})
}
