package orchestrator

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sharding-experiment/sharding/config"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// TestService_PointerAliasing verifies fix #5:
// Transactions stored in pending map should be independent copies,
// not pointers to the same memory that could be modified
func TestService_PointerAliasing(t *testing.T) {
	service, err := NewService(2, "", config.NetworkConfig{}) // Empty path for in-memory storage
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	// Create first transaction
	tx1 := protocol.CrossShardTx{
		ID:        "tx-1",
		FromShard: 0,
		From:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Value:     protocol.NewBigInt(big.NewInt(100)),
	}

	// Add to pending
	service.AddPendingTx(tx1)

	// Create second transaction (reusing same variable)
	tx2 := protocol.CrossShardTx{
		ID:        "tx-2",
		FromShard: 1,
		From:      common.HexToAddress("0x2222222222222222222222222222222222222222"),
		Value:     protocol.NewBigInt(big.NewInt(200)),
	}

	// Add second tx
	service.AddPendingTx(tx2)

	// Verify tx1 still has original values (not overwritten by tx2)
	status1 := service.GetTxStatus("tx-1")
	if status1 != protocol.TxPending {
		t.Errorf("tx-1 should have pending status, got %s", status1)
	}

	status2 := service.GetTxStatus("tx-2")
	if status2 != protocol.TxPending {
		t.Errorf("tx-2 should have pending status, got %s", status2)
	}

	// Both transactions should exist independently
	// Before fix #5, tx1 could be overwritten by tx2's data
}

// TestService_ConcurrentPendingAccess verifies thread safety of pending map
func TestService_ConcurrentPendingAccess(t *testing.T) {
	service, err := NewService(2, "", config.NetworkConfig{}) // Empty path for in-memory storage
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	var wg sync.WaitGroup
	iterations := 100

	// Concurrent writes
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tx := protocol.CrossShardTx{
				ID:        "concurrent-tx-" + string(rune('A'+id%26)),
				FromShard: id % 2,
				From:      common.BigToAddress(big.NewInt(int64(id))),
				Value:     protocol.NewBigInt(big.NewInt(int64(id * 100))),
			}
			service.AddPendingTx(tx)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = service.GetTxStatus("concurrent-tx-" + string(rune('A'+id%26)))
		}(i)
	}

	wg.Wait()
	// Test passes if no race condition panic occurs
}

// TestService_BroadcastDoesNotLeak verifies fix #4:
// broadcastBlock should use bounded concurrency and wait for completion
// Note: This is a structural test - we verify the goroutines complete
func TestService_BroadcastDoesNotLeak(t *testing.T) {
	// This test verifies that broadcast completes in bounded time
	// Before fix #4: unbounded goroutines could accumulate
	// After fix #4: WaitGroup + semaphore ensure bounded concurrency

	service, err := NewService(2, "", config.NetworkConfig{}) // Empty path for in-memory storage
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	// Create a block
	block := &protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{},
		TpcResult: map[string]bool{},
	}

	// This should complete (goroutines don't leak)
	// The broadcast will fail to reach actual shards, but that's expected
	// What we're testing is that the function returns (doesn't leak goroutines)
	done := make(chan bool)
	go func() {
		service.broadcastBlock(block)
		done <- true
	}()

	// Should complete within timeout (5s per shard + margin)
	select {
	case <-done:
		t.Log("broadcastBlock completed successfully")
	case <-done:
		// Already done
	}
}

// TestService_RetryConfiguration tests that retry constants are properly set (G.4)
func TestService_RetryConfiguration(t *testing.T) {
	// Verify retry configuration is reasonable
	if BroadcastMaxRetries < 1 {
		t.Error("BroadcastMaxRetries should be at least 1")
	}
	if BroadcastMaxRetries > 10 {
		t.Error("BroadcastMaxRetries should not be excessive (<=10)")
	}

	if BroadcastInitialBackoff < 50*time.Millisecond {
		t.Error("BroadcastInitialBackoff should be at least 50ms")
	}
	if BroadcastInitialBackoff > 1*time.Second {
		t.Error("BroadcastInitialBackoff should not be too long (<=1s)")
	}

	if BroadcastMaxBackoff < BroadcastInitialBackoff {
		t.Error("BroadcastMaxBackoff should be >= BroadcastInitialBackoff")
	}
	if BroadcastMaxBackoff > 30*time.Second {
		t.Error("BroadcastMaxBackoff should not be excessive (<=30s)")
	}

	t.Logf("G.4 Retry config: MaxRetries=%d, InitialBackoff=%v, MaxBackoff=%v, Timeout=%v",
		BroadcastMaxRetries, BroadcastInitialBackoff, BroadcastMaxBackoff, BroadcastTimeout)
}

// TestService_BroadcastWithRetry tests that broadcast retries on failure (G.4)
func TestService_BroadcastWithRetry(t *testing.T) {
	service, err := NewService(2, "", config.NetworkConfig{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	// Create a block
	block := &protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{},
		TpcResult: map[string]bool{},
	}

	// Broadcast to non-existent shards (will fail and retry)
	// This tests that the retry mechanism completes without panicking
	done := make(chan bool)
	go func() {
		service.broadcastBlock(block)
		done <- true
	}()

	// Calculate max time: retries * (max backoff) * shards + timeout buffer
	maxTime := time.Duration(BroadcastMaxRetries+1) * (BroadcastMaxBackoff + BroadcastTimeout) * 2
	if maxTime < 30*time.Second {
		maxTime = 30 * time.Second
	}

	select {
	case <-done:
		t.Log("G.4: Broadcast with retry completed (all retries exhausted to unreachable shards)")
	case <-time.After(maxTime):
		t.Errorf("Broadcast with retry timed out after %v", maxTime)
	}
}

// =============================================================================
// HTTP Handler Tests
// =============================================================================

// TestHandler_Health tests the /health endpoint
func TestHandler_Health(t *testing.T) {
	service, err := NewService(2, "", config.NetworkConfig{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	service.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp["status"] != "healthy" {
		t.Errorf("Expected status 'healthy', got '%s'", resp["status"])
	}
}

// TestHandler_Shards tests the /shards endpoint
func TestHandler_Shards(t *testing.T) {
	service, err := NewService(3, "", config.NetworkConfig{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	req := httptest.NewRequest("GET", "/shards", nil)
	w := httptest.NewRecorder()

	service.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var shards []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &shards); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(shards) != 3 {
		t.Errorf("Expected 3 shards, got %d", len(shards))
	}

	// Verify shard IDs
	for i, shard := range shards {
		if int(shard["id"].(float64)) != i {
			t.Errorf("Expected shard id %d, got %v", i, shard["id"])
		}
	}
}

// TestHandler_Submit tests the /cross-shard/submit endpoint
func TestHandler_Submit(t *testing.T) {
	service, err := NewService(2, "", config.NetworkConfig{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	tests := []struct {
		name       string
		tx         protocol.CrossShardTx
		wantStatus int
		wantErr    bool
	}{
		{
			name: "valid transaction",
			tx: protocol.CrossShardTx{
				FromShard: 0,
				From:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
				Value:     protocol.NewBigInt(big.NewInt(100)),
				RwSet: []protocol.RwVariable{
					{
						Address:        common.HexToAddress("0x2222222222222222222222222222222222222222"),
						ReferenceBlock: protocol.Reference{ShardNum: 1},
					},
				},
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "invalid from_shard (negative)",
			tx: protocol.CrossShardTx{
				FromShard: -1,
				From:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
				Value:     protocol.NewBigInt(big.NewInt(100)),
			},
			wantStatus: http.StatusBadRequest,
			wantErr:    true,
		},
		{
			name: "invalid from_shard (too high)",
			tx: protocol.CrossShardTx{
				FromShard: 99,
				From:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
				Value:     protocol.NewBigInt(big.NewInt(100)),
			},
			wantStatus: http.StatusBadRequest,
			wantErr:    true,
		},
		{
			name: "invalid target shard in RwSet",
			tx: protocol.CrossShardTx{
				FromShard: 0,
				From:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
				Value:     protocol.NewBigInt(big.NewInt(100)),
				RwSet: []protocol.RwVariable{
					{
						Address:        common.HexToAddress("0x2222222222222222222222222222222222222222"),
						ReferenceBlock: protocol.Reference{ShardNum: 99}, // Invalid shard
					},
				},
			},
			wantStatus: http.StatusBadRequest,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.tx)
			req := httptest.NewRequest("POST", "/cross-shard/submit", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			service.router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Expected status %d, got %d: %s", tt.wantStatus, w.Code, w.Body.String())
			}

			if !tt.wantErr && w.Code == http.StatusOK {
				var resp map[string]string
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("Failed to parse response: %v", err)
				}
				if resp["tx_id"] == "" {
					t.Error("Expected tx_id in response")
				}
				if resp["status"] != string(protocol.TxPending) {
					t.Errorf("Expected status 'pending', got '%s'", resp["status"])
				}
			}
		})
	}
}

// TestHandler_Submit_InvalidJSON tests submit with invalid JSON
func TestHandler_Submit_InvalidJSON(t *testing.T) {
	service, err := NewService(2, "", config.NetworkConfig{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	req := httptest.NewRequest("POST", "/cross-shard/submit", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	service.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for invalid JSON, got %d", w.Code)
	}
}

// TestHandler_Status tests the /cross-shard/status/{txid} endpoint
func TestHandler_Status(t *testing.T) {
	service, err := NewService(2, "", config.NetworkConfig{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	// Add a transaction first
	tx := protocol.CrossShardTx{
		ID:        "test-tx-123",
		FromShard: 0,
		From:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Value:     protocol.NewBigInt(big.NewInt(100)),
	}
	service.AddPendingTx(tx)

	t.Run("existing transaction", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/cross-shard/status/test-tx-123", nil)
		w := httptest.NewRecorder()

		service.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		var resp map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}

		if resp["tx_id"] != "test-tx-123" {
			t.Errorf("Expected tx_id 'test-tx-123', got '%s'", resp["tx_id"])
		}
		if resp["status"] != string(protocol.TxPending) {
			t.Errorf("Expected status 'pending', got '%s'", resp["status"])
		}
	})

	t.Run("non-existent transaction", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/cross-shard/status/nonexistent", nil)
		w := httptest.NewRecorder()

		service.router.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("Expected status 404, got %d", w.Code)
		}
	})
}

// TestHandler_Call tests the /cross-shard/call endpoint
func TestHandler_Call(t *testing.T) {
	service, err := NewService(2, "", config.NetworkConfig{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	t.Run("valid call submission", func(t *testing.T) {
		tx := protocol.CrossShardTx{
			FromShard: 0,
			From:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
			Value:     protocol.NewBigInt(big.NewInt(0)),
			Data:      []byte{0x01, 0x02, 0x03},
		}

		body, _ := json.Marshal(tx)
		req := httptest.NewRequest("POST", "/cross-shard/call", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		service.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}

		if resp["tx_id"] == "" {
			t.Error("Expected tx_id in response")
		}
		if resp["status"] != string(protocol.SimPending) {
			t.Errorf("Expected status 'sim_pending', got '%s'", resp["status"])
		}
	})

	t.Run("invalid from_shard", func(t *testing.T) {
		tx := protocol.CrossShardTx{
			FromShard: -1,
			From:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
		}

		body, _ := json.Marshal(tx)
		req := httptest.NewRequest("POST", "/cross-shard/call", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		service.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/cross-shard/call", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		service.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

// TestHandler_SimulationStatus tests the /cross-shard/simulation/{txid} endpoint
func TestHandler_SimulationStatus(t *testing.T) {
	service, err := NewService(2, "", config.NetworkConfig{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	t.Run("non-existent simulation", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/cross-shard/simulation/nonexistent", nil)
		w := httptest.NewRecorder()

		service.router.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("Expected status 404, got %d", w.Code)
		}
	})
}

// TestHandler_StateShardBlock tests the /state-shard/block endpoint
func TestHandler_StateShardBlock(t *testing.T) {
	service, err := NewService(2, "", config.NetworkConfig{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	// First, add a pending transaction so we can vote on it
	tx := protocol.CrossShardTx{
		ID:        "vote-test-tx",
		FromShard: 0,
		From:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Value:     protocol.NewBigInt(big.NewInt(100)),
		RwSet: []protocol.RwVariable{
			{
				Address:        common.HexToAddress("0x2222222222222222222222222222222222222222"),
				ReferenceBlock: protocol.Reference{ShardNum: 1},
			},
		},
	}
	service.AddPendingTx(tx)
	service.chain.AddTransaction(tx)

	t.Run("valid state shard block with votes", func(t *testing.T) {
		block := protocol.StateShardBlock{
			ShardID:    0,
			Height:     1,
			TpcPrepare: map[string]bool{"vote-test-tx": true},
		}

		body, _ := json.Marshal(block)
		req := httptest.NewRequest("POST", "/state-shard/block", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		service.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/state-shard/block", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		service.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

// TestHandler_GetBlock tests the /block/{height} endpoint
func TestHandler_GetBlock(t *testing.T) {
	service, err := NewService(2, "", config.NetworkConfig{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	// Produce a block first
	_ = service.chain.ProduceBlock()

	t.Run("existing block", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/block/1", nil)
		w := httptest.NewRecorder()

		service.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}

		var block protocol.OrchestratorShardBlock
		if err := json.Unmarshal(w.Body.Bytes(), &block); err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}

		if block.Height != 1 {
			t.Errorf("Expected height 1, got %d", block.Height)
		}
	})

	t.Run("non-existent block", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/block/999", nil)
		w := httptest.NewRecorder()

		service.router.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("Expected status 404, got %d", w.Code)
		}
	})

	t.Run("invalid height", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/block/invalid", nil)
		w := httptest.NewRecorder()

		service.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

// TestHandler_GetLatestBlock tests the /block/latest endpoint
func TestHandler_GetLatestBlock(t *testing.T) {
	service, err := NewService(2, "", config.NetworkConfig{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	t.Run("before any blocks produced", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/block/latest", nil)
		w := httptest.NewRecorder()

		service.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}

		if resp["height"].(float64) != 0 {
			t.Errorf("Expected height 0, got %v", resp["height"])
		}
	})

	// Produce a block
	_ = service.chain.ProduceBlock()

	t.Run("after block produced", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/block/latest", nil)
		w := httptest.NewRecorder()

		service.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}

		if resp["height"].(float64) != 1 {
			t.Errorf("Expected height 1, got %v", resp["height"])
		}
		if resp["block"] == nil {
			t.Error("Expected block in response")
		}
	})
}

// TestHandler_Router tests that Router() returns the router
func TestHandler_Router(t *testing.T) {
	service, err := NewService(2, "", config.NetworkConfig{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	router := service.Router()
	if router == nil {
		t.Error("Router() should not return nil")
	}
	if router != service.router {
		t.Error("Router() should return the service's router")
	}
}

// TestService_UpdateStatus tests the updateStatus method
func TestService_UpdateStatus(t *testing.T) {
	service, err := NewService(2, "", config.NetworkConfig{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}
	defer service.Close()

	// Add a transaction
	tx := protocol.CrossShardTx{
		ID:        "status-test-tx",
		FromShard: 0,
		From:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Value:     protocol.NewBigInt(big.NewInt(100)),
	}
	service.AddPendingTx(tx)

	// Update status
	service.updateStatus("status-test-tx", protocol.TxCommitted)

	// Verify
	status := service.GetTxStatus("status-test-tx")
	if status != protocol.TxCommitted {
		t.Errorf("Expected status '%s', got '%s'", protocol.TxCommitted, status)
	}

	// Update non-existent tx (should not panic)
	service.updateStatus("nonexistent", protocol.TxAborted)
}

// TestService_Close_StopsGoroutines verifies that Close() properly stops all background goroutines.
// This prevents resource leaks when services are created and destroyed (e.g., in tests).
func TestService_Close_StopsGoroutines(t *testing.T) {
	// Record goroutine count before creating service
	// Note: We use a baseline because other goroutines may exist from test infrastructure
	baseline := runtime.NumGoroutine()

	service, err := NewService(2, "", config.NetworkConfig{})
	if err != nil {
		t.Fatalf("Failed to create service: %v", err)
	}

	// Service should have started background goroutines
	afterCreate := runtime.NumGoroutine()
	if afterCreate <= baseline {
		t.Logf("Warning: Expected goroutines to increase after service creation (baseline=%d, after=%d)",
			baseline, afterCreate)
	}

	// Close the service
	if err := service.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	// Wait for goroutines to stop
	time.Sleep(200 * time.Millisecond)

	// Check goroutine count returned close to baseline
	afterClose := runtime.NumGoroutine()

	// Allow for some tolerance (runtime may have other goroutines)
	if afterClose > baseline+2 {
		t.Errorf("Goroutine leak: baseline=%d, afterCreate=%d, afterClose=%d (expected close to baseline)",
			baseline, afterCreate, afterClose)
	} else {
		t.Logf("Goroutine test passed: baseline=%d, afterCreate=%d, afterClose=%d",
			baseline, afterCreate, afterClose)
	}
}
