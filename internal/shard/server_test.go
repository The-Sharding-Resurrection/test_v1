package shard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleTxSubmit_LocalTransfer(t *testing.T) {
	// Create server for shard 0 (without block producer for testing)
	server := NewServerForTest(0, "http://localhost:8080")

	// Fund the sender (address ending in 0x00 belongs to shard 0)
	senderAddr := "0x0000000000000000000000000000000000000000"
	recipientAddr := "0x0000000000000000000000000000000000000006" // ends in 06, shard 0 (6 % 6 = 0)

	// Credit sender with funds via faucet
	faucetReq := FaucetRequest{Address: senderAddr, Amount: "1000000000000000000"}
	faucetBody, _ := json.Marshal(faucetReq)
	req := httptest.NewRequest(http.MethodPost, "/faucet", bytes.NewReader(faucetBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Faucet failed: %s", rr.Body.String())
	}

	// Submit local transfer (both addresses on shard 0)
	txReq := TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "100000000000000000", // 0.1 ETH
	}
	txBody, _ := json.Marshal(txReq)
	req = httptest.NewRequest(http.MethodPost, "/tx/submit", bytes.NewReader(txBody))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	var result map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Check it was executed locally
	if result["cross_shard"] != false {
		t.Errorf("Expected cross_shard=false, got %v", result["cross_shard"])
	}
	if result["success"] != true {
		t.Errorf("Expected success=true, got %v (error: %v)", result["success"], result["error"])
	}
}

func TestHandleTxSubmit_CrossShardTransfer(t *testing.T) {
	// Create a mock orchestrator to receive the forwarded tx
	orchestratorCalled := false
	mockOrchestrator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orchestratorCalled = true
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"tx_id":   "test-tx-id",
		})
	}))
	defer mockOrchestrator.Close()

	// Create server for shard 0
	server := NewServerForTest(0, mockOrchestrator.URL)

	// Fund the sender
	senderAddr := "0x0000000000000000000000000000000000000000" // shard 0
	recipientAddr := "0x0000000000000000000000000000000000000001" // shard 1 (1 % 6 = 1)

	// Credit sender with funds
	faucetReq := FaucetRequest{Address: senderAddr, Amount: "1000000000000000000"}
	faucetBody, _ := json.Marshal(faucetReq)
	req := httptest.NewRequest(http.MethodPost, "/faucet", bytes.NewReader(faucetBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Faucet failed: %s", rr.Body.String())
	}

	// Submit cross-shard transfer
	txReq := TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "100000000000000000",
	}
	txBody, _ := json.Marshal(txReq)
	req = httptest.NewRequest(http.MethodPost, "/tx/submit", bytes.NewReader(txBody))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	// Check that orchestrator was called
	if !orchestratorCalled {
		t.Error("Expected orchestrator to be called for cross-shard tx")
	}

	var result map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Check it was detected as cross-shard
	if result["cross_shard"] != true {
		t.Errorf("Expected cross_shard=true, got %v", result["cross_shard"])
	}
}

func TestHandleTxSubmit_WrongShard(t *testing.T) {
	// Create server for shard 0
	server := NewServerForTest(0, "http://localhost:8080")

	// Try to submit from an address on shard 1
	senderAddr := "0x0000000000000000000000000000000000000001" // shard 1
	recipientAddr := "0x0000000000000000000000000000000000000000"

	txReq := TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "100",
	}
	txBody, _ := json.Marshal(txReq)
	req := httptest.NewRequest(http.MethodPost, "/tx/submit", bytes.NewReader(txBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)

	// Should be rejected because sender is on wrong shard
	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleTxSubmit_InsufficientBalance(t *testing.T) {
	// Create server for shard 0
	server := NewServerForTest(0, "http://localhost:8080")

	// Don't fund the sender - they have 0 balance
	senderAddr := "0x0000000000000000000000000000000000000000"
	recipientAddr := "0x0000000000000000000000000000000000000006"

	txReq := TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "100", // Try to send 100 with 0 balance
	}
	txBody, _ := json.Marshal(txReq)
	req := httptest.NewRequest(http.MethodPost, "/tx/submit", bytes.NewReader(txBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", rr.Code)
	}

	var result map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&result)

	// Should fail with insufficient balance
	if result["success"] != false {
		t.Errorf("Expected success=false for insufficient balance, got %v", result["success"])
	}
}

func TestIsDefiniteLocalError(t *testing.T) {
	tests := []struct {
		err      string
		expected bool
	}{
		// Sender-related errors are definite local failures
		{"insufficient balance for transfer", true},
		{"insufficient funds", true},
		{"nonce too low", true},
		{"nonce too high", true},
		{"INSUFFICIENT BALANCE", true}, // case insensitive

		// These should be forwarded to orchestrator (could be cross-shard)
		{"execution reverted", false},  // Contract might be on another shard
		{"out of gas", false},          // Might succeed with proper cross-shard state
		{"invalid opcode", false},      // Might be calling non-existent cross-shard contract
		{"some random error", false},
		{"cross-shard access detected", false},
		{"", false},
	}

	for _, tc := range tests {
		result := isDefiniteLocalError(tc.err)
		if result != tc.expected {
			t.Errorf("isDefiniteLocalError(%q) = %v, expected %v", tc.err, result, tc.expected)
		}
	}
}
