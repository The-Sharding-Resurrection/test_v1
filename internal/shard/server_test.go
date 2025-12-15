package shard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// =============================================================================
// Test Helpers
// =============================================================================

func setupTestServer(t *testing.T, shardID int, orchestratorURL string) *Server {
	t.Helper()
	return NewServerForTest(shardID, orchestratorURL)
}

func fundAccount(t *testing.T, server *Server, address string, amount string) {
	t.Helper()
	faucetReq := FaucetRequest{Address: address, Amount: amount}
	faucetBody, _ := json.Marshal(faucetReq)
	req := httptest.NewRequest(http.MethodPost, "/faucet", bytes.NewReader(faucetBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Faucet failed: %s", rr.Body.String())
	}
}

func submitTx(t *testing.T, server *Server, req TxSubmitRequest) (int, map[string]interface{}) {
	t.Helper()
	txBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/tx/submit", bytes.NewReader(txBody))
	httpReq.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, httpReq)

	var result map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&result)
	return rr.Code, result
}

func getBalance(t *testing.T, server *Server, address string) *big.Int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/balance/"+address, nil)
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)

	var result map[string]string
	json.NewDecoder(rr.Body).Decode(&result)
	balance, _ := new(big.Int).SetString(result["balance"], 10)
	return balance
}

func produceBlock(t *testing.T, server *Server) {
	t.Helper()
	_, err := server.ProduceBlock()
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}
}

// =============================================================================
// Shard Assignment Tests
// =============================================================================

func TestShardAssignment(t *testing.T) {
	// Verify shard assignment logic: address % NumShards
	tests := []struct {
		address  string
		expected int
	}{
		{"0x0000000000000000000000000000000000000000", 0}, // 0 % 6 = 0
		{"0x0000000000000000000000000000000000000001", 1}, // 1 % 6 = 1
		{"0x0000000000000000000000000000000000000002", 2}, // 2 % 6 = 2
		{"0x0000000000000000000000000000000000000003", 3}, // 3 % 6 = 3
		{"0x0000000000000000000000000000000000000004", 4}, // 4 % 6 = 4
		{"0x0000000000000000000000000000000000000005", 5}, // 5 % 6 = 5
		{"0x0000000000000000000000000000000000000006", 0}, // 6 % 6 = 0
		{"0x0000000000000000000000000000000000000007", 1}, // 7 % 6 = 1
		{"0x00000000000000000000000000000000000000FF", 3}, // 255 % 6 = 3
	}

	for _, tc := range tests {
		addr := common.HexToAddress(tc.address)
		shard := int(addr[len(addr)-1]) % NumShards
		if shard != tc.expected {
			t.Errorf("Address %s: expected shard %d, got %d", tc.address, tc.expected, shard)
		}
	}
}

func TestHandleTxSubmit_AllShardsWrongSender(t *testing.T) {
	// Test that each shard rejects transactions from addresses on other shards
	for shardID := 0; shardID < NumShards; shardID++ {
		server := setupTestServer(t, shardID, "http://localhost:8080")

		for wrongShard := 0; wrongShard < NumShards; wrongShard++ {
			if wrongShard == shardID {
				continue // Skip the correct shard
			}

			// Create address that belongs to wrongShard
			senderAddr := common.Address{}
			senderAddr[19] = byte(wrongShard)

			code, _ := submitTx(t, server, TxSubmitRequest{
				From:  senderAddr.Hex(),
				To:    "0x0000000000000000000000000000000000000000",
				Value: "100",
			})

			if code != http.StatusBadRequest {
				t.Errorf("Shard %d: expected 400 for sender on shard %d, got %d",
					shardID, wrongShard, code)
			}
		}
	}
}

// =============================================================================
// Local Transfer Tests
// =============================================================================

func TestHandleTxSubmit_LocalTransfer(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	senderAddr := "0x0000000000000000000000000000000000000000"
	recipientAddr := "0x0000000000000000000000000000000000000006" // 6 % 6 = 0

	fundAccount(t, server, senderAddr, "1000000000000000000") // 1 ETH

	code, result := submitTx(t, server, TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "100000000000000000", // 0.1 ETH
	})

	if code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", code)
	}
	if result["cross_shard"] != false {
		t.Errorf("Expected cross_shard=false, got %v", result["cross_shard"])
	}
	if result["success"] != true {
		t.Errorf("Expected success=true, got %v (error: %v)", result["success"], result["error"])
	}
}

func TestHandleTxSubmit_LocalTransfer_ZeroValue(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	senderAddr := "0x0000000000000000000000000000000000000000"
	recipientAddr := "0x0000000000000000000000000000000000000006"

	// Don't need to fund for zero value transfer
	code, result := submitTx(t, server, TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "0",
	})

	if code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", code)
	}
	if result["success"] != true {
		t.Errorf("Expected success=true for zero value transfer, got %v", result["success"])
	}
	if result["cross_shard"] != false {
		t.Errorf("Expected cross_shard=false, got %v", result["cross_shard"])
	}
}

func TestHandleTxSubmit_LocalTransfer_ExactBalance(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	senderAddr := "0x0000000000000000000000000000000000000000"
	recipientAddr := "0x0000000000000000000000000000000000000006"

	fundAccount(t, server, senderAddr, "1000")

	// Transfer exact balance
	code, result := submitTx(t, server, TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "1000",
	})

	if code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", code)
	}
	if result["success"] != true {
		t.Errorf("Expected success=true for exact balance transfer, got %v (error: %v)",
			result["success"], result["error"])
	}
	if result["status"] != "queued" {
		t.Errorf("Expected status=queued, got %v", result["status"])
	}

	// Produce block to execute queued transactions
	produceBlock(t, server)

	// Verify balances after block production
	senderBalance := getBalance(t, server, senderAddr)
	recipientBalance := getBalance(t, server, recipientAddr)

	if senderBalance.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("Expected sender balance 0, got %s", senderBalance.String())
	}
	if recipientBalance.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Expected recipient balance 1000, got %s", recipientBalance.String())
	}
}

func TestHandleTxSubmit_LocalTransfer_MultipleSequential(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	senderAddr := "0x0000000000000000000000000000000000000000"
	recipientAddr := "0x0000000000000000000000000000000000000006"

	fundAccount(t, server, senderAddr, "1000")

	// Queue 3 transfers
	for i := 0; i < 3; i++ {
		code, result := submitTx(t, server, TxSubmitRequest{
			From:  senderAddr,
			To:    recipientAddr,
			Value: "100",
		})

		if code != http.StatusOK {
			t.Fatalf("Transfer %d: expected 200 OK, got %d", i+1, code)
		}
		if result["success"] != true {
			t.Fatalf("Transfer %d: expected success=true, got %v", i+1, result["success"])
		}
	}

	// Produce block to execute all queued transactions
	produceBlock(t, server)

	// Verify final balances after block production
	senderBalance := getBalance(t, server, senderAddr)
	recipientBalance := getBalance(t, server, recipientAddr)

	if senderBalance.Cmp(big.NewInt(700)) != 0 {
		t.Errorf("Expected sender balance 700, got %s", senderBalance.String())
	}
	if recipientBalance.Cmp(big.NewInt(300)) != 0 {
		t.Errorf("Expected recipient balance 300, got %s", recipientBalance.String())
	}
}

func TestHandleTxSubmit_LocalTransfer_SelfTransfer(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	addr := "0x0000000000000000000000000000000000000000"
	fundAccount(t, server, addr, "1000")

	// Transfer to self
	code, result := submitTx(t, server, TxSubmitRequest{
		From:  addr,
		To:    addr,
		Value: "500",
	})

	if code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", code)
	}
	if result["success"] != true {
		t.Errorf("Expected success=true for self transfer, got %v", result["success"])
	}

	// Balance should remain 1000
	balance := getBalance(t, server, addr)
	if balance.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Expected balance 1000 after self transfer, got %s", balance.String())
	}
}

// =============================================================================
// Cross-Shard Transfer Tests
// =============================================================================

func TestHandleTxSubmit_CrossShardTransfer(t *testing.T) {
	var receivedTx protocol.CrossShardTx
	orchestratorCalled := false

	mockOrchestrator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orchestratorCalled = true
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedTx)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"tx_id":   receivedTx.ID,
		})
	}))
	defer mockOrchestrator.Close()

	server := setupTestServer(t, 0, mockOrchestrator.URL)

	senderAddr := "0x0000000000000000000000000000000000000000"    // shard 0
	recipientAddr := "0x0000000000000000000000000000000000000001" // shard 1

	fundAccount(t, server, senderAddr, "1000000000000000000")

	code, result := submitTx(t, server, TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "100000000000000000",
		Gas:   50000,
	})

	if code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", code)
	}
	if !orchestratorCalled {
		t.Error("Expected orchestrator to be called")
	}
	if result["cross_shard"] != true {
		t.Errorf("Expected cross_shard=true, got %v", result["cross_shard"])
	}

	// Verify the forwarded tx has correct fields
	if receivedTx.FromShard != 0 {
		t.Errorf("Expected FromShard=0, got %d", receivedTx.FromShard)
	}
	if receivedTx.From != common.HexToAddress(senderAddr) {
		t.Errorf("Expected From=%s, got %s", senderAddr, receivedTx.From.Hex())
	}
	if receivedTx.Gas != 50000 {
		t.Errorf("Expected Gas=50000, got %d", receivedTx.Gas)
	}
	if len(receivedTx.RwSet) != 1 {
		t.Errorf("Expected 1 RwSet entry, got %d", len(receivedTx.RwSet))
	}
	if receivedTx.RwSet[0].ReferenceBlock.ShardNum != 1 {
		t.Errorf("Expected RwSet shard=1, got %d", receivedTx.RwSet[0].ReferenceBlock.ShardNum)
	}
}

func TestHandleTxSubmit_CrossShardTransfer_AllShardPairs(t *testing.T) {
	// Test cross-shard detection for all shard pairs
	for fromShard := 0; fromShard < NumShards; fromShard++ {
		for toShard := 0; toShard < NumShards; toShard++ {
			if fromShard == toShard {
				continue // Skip same-shard
			}

			orchestratorCalled := false
			mockOrchestrator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				orchestratorCalled = true
				json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "tx_id": "test"})
			}))

			server := setupTestServer(t, fromShard, mockOrchestrator.URL)

			senderAddr := common.Address{}
			senderAddr[19] = byte(fromShard)

			recipientAddr := common.Address{}
			recipientAddr[19] = byte(toShard)

			fundAccount(t, server, senderAddr.Hex(), "1000")

			code, result := submitTx(t, server, TxSubmitRequest{
				From:  senderAddr.Hex(),
				To:    recipientAddr.Hex(),
				Value: "100",
			})

			mockOrchestrator.Close()

			if code != http.StatusOK {
				t.Errorf("Shard %d->%d: expected 200 OK, got %d", fromShard, toShard, code)
				continue
			}
			if !orchestratorCalled {
				t.Errorf("Shard %d->%d: expected orchestrator to be called", fromShard, toShard)
			}
			if result["cross_shard"] != true {
				t.Errorf("Shard %d->%d: expected cross_shard=true", fromShard, toShard)
			}
		}
	}
}

func TestHandleTxSubmit_CrossShardTransfer_OrchestratorDown(t *testing.T) {
	// Use invalid URL to simulate orchestrator being down
	server := setupTestServer(t, 0, "http://localhost:99999")

	senderAddr := "0x0000000000000000000000000000000000000000"
	recipientAddr := "0x0000000000000000000000000000000000000001"

	fundAccount(t, server, senderAddr, "1000")

	code, _ := submitTx(t, server, TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "100",
	})

	// Should return 503 Service Unavailable
	if code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 Service Unavailable, got %d", code)
	}
}

// =============================================================================
// Insufficient Balance Tests
// =============================================================================

func TestHandleTxSubmit_InsufficientBalance(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	senderAddr := "0x0000000000000000000000000000000000000000"
	recipientAddr := "0x0000000000000000000000000000000000000006"

	// Don't fund - 0 balance
	code, result := submitTx(t, server, TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "100",
	})

	if code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", code)
	}
	if result["success"] != false {
		t.Errorf("Expected success=false, got %v", result["success"])
	}
	if result["cross_shard"] != false {
		t.Errorf("Expected cross_shard=false, got %v", result["cross_shard"])
	}
}

func TestHandleTxSubmit_InsufficientBalance_ByOne(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	senderAddr := "0x0000000000000000000000000000000000000000"
	recipientAddr := "0x0000000000000000000000000000000000000006"

	fundAccount(t, server, senderAddr, "99")

	// Try to send 100 with only 99
	code, result := submitTx(t, server, TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "100",
	})

	if code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", code)
	}
	if result["success"] != false {
		t.Errorf("Expected success=false for insufficient balance, got %v", result["success"])
	}

	// Verify balance unchanged
	balance := getBalance(t, server, senderAddr)
	if balance.Cmp(big.NewInt(99)) != 0 {
		t.Errorf("Expected balance 99 after failed transfer, got %s", balance.String())
	}
}

// =============================================================================
// Wrong Shard Tests
// =============================================================================

func TestHandleTxSubmit_WrongShard(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	// Sender on shard 1, submitting to shard 0
	senderAddr := "0x0000000000000000000000000000000000000001"
	recipientAddr := "0x0000000000000000000000000000000000000000"

	code, _ := submitTx(t, server, TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "100",
	})

	if code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request, got %d", code)
	}
}

// =============================================================================
// Contract Tests
// =============================================================================

func TestHandleTxSubmit_ContractCall_LocalContract(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	senderAddr := "0x0000000000000000000000000000000000000000"
	fundAccount(t, server, senderAddr, "10000000000000000000") // 10 ETH

	// Use a contract address that's known to be on shard 0 (ends with 00, 06, 0C, 12, etc.)
	// We'll use address ending in 0x00 for shard 0
	contractAddr := "0x1111111111111111111111111111111111111100" // ends in 00, shard 0

	// Set code directly on the contract address
	// Simple contract: PUSH1 0x42 PUSH1 0x00 MSTORE PUSH1 0x20 PUSH1 0x00 RETURN
	// Returns 0x42 padded to 32 bytes
	contractCode := common.FromHex("0x60426000526020600060f3")
	server.evmState.SetCode(common.HexToAddress(contractAddr), contractCode)

	// Now call the contract via /tx/submit
	code, result := submitTx(t, server, TxSubmitRequest{
		From:  senderAddr,
		To:    contractAddr,
		Value: "0",
		Data:  "0x",
		Gas:   100000,
	})

	if code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", code)
	}
	if result["cross_shard"] != false {
		t.Errorf("Expected cross_shard=false for local contract, got %v", result["cross_shard"])
	}
	if result["success"] != true {
		t.Errorf("Expected success=true, got %v (error: %v)", result["success"], result["error"])
	}
}

func TestHandleTxSubmit_ContractDeploy_CrossShardAddress(t *testing.T) {
	// This test verifies that when a deployed contract lands on a different shard,
	// subsequent calls are correctly detected as cross-shard

	orchestratorCalled := false
	mockOrchestrator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orchestratorCalled = true
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "tx_id": "test"})
	}))
	defer mockOrchestrator.Close()

	server := setupTestServer(t, 0, mockOrchestrator.URL)

	senderAddr := "0x0000000000000000000000000000000000000000"
	fundAccount(t, server, senderAddr, "10000000000000000000")

	// Deploy contract
	bytecode := "0x60006000f3"
	deployReq := DeployRequest{From: senderAddr, Bytecode: bytecode, Gas: 1000000}
	deployBody, _ := json.Marshal(deployReq)
	req := httptest.NewRequest(http.MethodPost, "/evm/deploy", bytes.NewReader(deployBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)

	var deployResult map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&deployResult)
	if deployResult["success"] != true {
		// Skip if failure is due to code forwarding (expected in unit test without Docker network)
		errStr := fmt.Sprintf("%v", deployResult["error"])
		if strings.Contains(errStr, "failed to forward") {
			t.Skip("Code forwarding to remote shard not available in unit tests")
		}
		t.Fatalf("Deploy failed: %v", deployResult["error"])
	}

	contractAddr := deployResult["address"].(string)
	contractAddress := common.HexToAddress(contractAddr)
	contractShard := int(contractAddress[len(contractAddress)-1]) % NumShards

	t.Logf("Contract deployed at %s (shard %d)", contractAddr, contractShard)

	if contractShard == 0 {
		t.Skip("Contract landed on shard 0, can't test cross-shard detection")
	}

	// Call the contract - should be detected as cross-shard
	code, result := submitTx(t, server, TxSubmitRequest{
		From: senderAddr,
		To:   contractAddr,
		Data: "0x",
		Gas:  100000,
	})

	if code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", code)
	}
	if !orchestratorCalled {
		t.Error("Expected orchestrator to be called for cross-shard contract")
	}
	if result["cross_shard"] != true {
		t.Errorf("Expected cross_shard=true for contract on shard %d, got %v", contractShard, result["cross_shard"])
	}
}

func TestHandleTxSubmit_ContractCall_NonExistentContract_CrossShard(t *testing.T) {
	// When calling a non-existent contract that's on another shard,
	// should forward to orchestrator (the contract might exist there)

	orchestratorCalled := false
	mockOrchestrator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orchestratorCalled = true
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "tx_id": "test"})
	}))
	defer mockOrchestrator.Close()

	server := setupTestServer(t, 0, mockOrchestrator.URL)

	senderAddr := "0x0000000000000000000000000000000000000000"
	// Address on shard 1 - might be a contract there
	contractAddr := "0x0000000000000000000000000000000000000001"

	fundAccount(t, server, senderAddr, "1000000000000000000")

	code, result := submitTx(t, server, TxSubmitRequest{
		From:  senderAddr,
		To:    contractAddr,
		Value: "0",
		Data:  "0xa9059cbb", // Some function selector
		Gas:   100000,
	})

	if code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", code)
	}
	if !orchestratorCalled {
		t.Error("Expected orchestrator to be called for potential cross-shard contract")
	}
	if result["cross_shard"] != true {
		t.Errorf("Expected cross_shard=true, got %v", result["cross_shard"])
	}
}

// =============================================================================
// Gas Handling Tests
// =============================================================================

func TestHandleTxSubmit_DefaultGasLimit(t *testing.T) {
	var receivedTx protocol.CrossShardTx

	mockOrchestrator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedTx)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "tx_id": "test"})
	}))
	defer mockOrchestrator.Close()

	server := setupTestServer(t, 0, mockOrchestrator.URL)

	senderAddr := "0x0000000000000000000000000000000000000000"
	recipientAddr := "0x0000000000000000000000000000000000000001"

	fundAccount(t, server, senderAddr, "1000")

	// Submit without specifying gas
	submitTx(t, server, TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "100",
		// Gas not specified - should use default
	})

	// Should use DefaultGasLimit
	if receivedTx.Gas != DefaultGasLimit {
		t.Errorf("Expected default gas %d, got %d", DefaultGasLimit, receivedTx.Gas)
	}
}

func TestHandleTxSubmit_CustomGasLimit(t *testing.T) {
	var receivedTx protocol.CrossShardTx

	mockOrchestrator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedTx)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "tx_id": "test"})
	}))
	defer mockOrchestrator.Close()

	server := setupTestServer(t, 0, mockOrchestrator.URL)

	senderAddr := "0x0000000000000000000000000000000000000000"
	recipientAddr := "0x0000000000000000000000000000000000000001"

	fundAccount(t, server, senderAddr, "1000")

	// Submit with custom gas
	submitTx(t, server, TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "100",
		Gas:   21000,
	})

	if receivedTx.Gas != 21000 {
		t.Errorf("Expected gas 21000, got %d", receivedTx.Gas)
	}
}

// =============================================================================
// Error Classification Tests
// =============================================================================

func TestIsDefiniteLocalError(t *testing.T) {
	tests := []struct {
		name     string
		err      string
		expected bool
	}{
		// Sender-related errors are definite local failures
		{"insufficient balance", "insufficient balance for transfer", true},
		{"insufficient funds", "insufficient funds", true},
		{"nonce too low", "nonce too low", true},
		{"nonce too high", "nonce too high", true},
		{"case insensitive", "INSUFFICIENT BALANCE", true},
		{"mixed case", "Insufficient Balance For Transfer", true},
		{"partial match", "error: insufficient balance", true},
		{"with context", "transaction failed: nonce too low (expected 5)", true},

		// Execution errors should NOT be local (might be cross-shard)
		{"execution reverted", "execution reverted", false},
		{"out of gas", "out of gas", false},
		{"invalid opcode", "invalid opcode: INVALID", false},
		{"stack underflow", "stack underflow", false},
		{"stack overflow", "stack overflow", false},
		{"invalid jump", "invalid jump destination", false},
		{"write protection", "write protection", false},

		// Random errors should be forwarded
		{"random error", "some random error", false},
		{"empty string", "", false},
		{"nil-like", "null", false},
		{"contract error", "contract call failed", false},
		{"revert reason", "revert: unauthorized", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isDefiniteLocalError(tc.err)
			if result != tc.expected {
				t.Errorf("isDefiniteLocalError(%q) = %v, expected %v", tc.err, result, tc.expected)
			}
		})
	}
}

func TestContainsIgnoreCase(t *testing.T) {
	tests := []struct {
		s        string
		substr   string
		expected bool
	}{
		{"hello world", "world", true},
		{"Hello World", "world", true},
		{"HELLO WORLD", "hello", true},
		{"hello", "hello world", false},
		{"", "test", false},
		{"test", "", true},
		{"ABC123", "bc12", true},
		{"abc", "ABC", true},
	}

	for _, tc := range tests {
		result := containsIgnoreCase(tc.s, tc.substr)
		if result != tc.expected {
			t.Errorf("containsIgnoreCase(%q, %q) = %v, expected %v",
				tc.s, tc.substr, result, tc.expected)
		}
	}
}

// =============================================================================
// Request Validation Tests
// =============================================================================

func TestHandleTxSubmit_InvalidJSON(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	req := httptest.NewRequest(http.MethodPost, "/tx/submit", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for invalid JSON, got %d", rr.Code)
	}
}

func TestHandleTxSubmit_InvalidValue(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	code, result := submitTx(t, server, TxSubmitRequest{
		From:  "0x0000000000000000000000000000000000000000",
		To:    "0x0000000000000000000000000000000000000006",
		Value: "not a number",
	})

	if code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for invalid value, got %d (result: %v)", code, result)
	}
}

func TestHandleTxSubmit_EmptyValue(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	// Empty value should default to 0
	code, result := submitTx(t, server, TxSubmitRequest{
		From:  "0x0000000000000000000000000000000000000000",
		To:    "0x0000000000000000000000000000000000000006",
		Value: "",
	})

	if code != http.StatusOK {
		t.Fatalf("Expected 200 OK for empty value, got %d", code)
	}
	if result["success"] != true {
		t.Errorf("Expected success=true for empty value (0), got %v", result["success"])
	}
}

func TestHandleTxSubmit_LargeValue(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	senderAddr := "0x0000000000000000000000000000000000000000"
	recipientAddr := "0x0000000000000000000000000000000000000006"

	// Fund with a large amount
	largeAmount := "1000000000000000000000000000" // 1 billion ETH
	fundAccount(t, server, senderAddr, largeAmount)

	code, result := submitTx(t, server, TxSubmitRequest{
		From:  senderAddr,
		To:    recipientAddr,
		Value: "999999999999999999999999999",
	})

	if code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", code)
	}
	if result["success"] != true {
		t.Errorf("Expected success=true for large value transfer, got %v", result["success"])
	}
}

// =============================================================================
// Balance Query Tests
// =============================================================================

func TestGetBalance(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	addr := "0x0000000000000000000000000000000000000000"

	// Initial balance should be 0
	balance := getBalance(t, server, addr)
	if balance.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("Expected initial balance 0, got %s", balance.String())
	}

	// Fund and check
	fundAccount(t, server, addr, "12345")
	balance = getBalance(t, server, addr)
	if balance.Cmp(big.NewInt(12345)) != 0 {
		t.Errorf("Expected balance 12345, got %s", balance.String())
	}
}

// =============================================================================
// Faucet Tests
// =============================================================================

func TestFaucet(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	addr := "0x0000000000000000000000000000000000000000"

	// Multiple faucet calls should accumulate
	fundAccount(t, server, addr, "100")
	fundAccount(t, server, addr, "200")
	fundAccount(t, server, addr, "300")

	balance := getBalance(t, server, addr)
	if balance.Cmp(big.NewInt(600)) != 0 {
		t.Errorf("Expected balance 600 after multiple faucet calls, got %s", balance.String())
	}
}

// =============================================================================
// Health Check Tests
// =============================================================================

func TestHealthCheck(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", rr.Code)
	}

	var result map[string]string
	json.NewDecoder(rr.Body).Decode(&result)

	if result["status"] != "healthy" {
		t.Errorf("Expected status=healthy, got %s", result["status"])
	}
}

func TestInfo(t *testing.T) {
	server := setupTestServer(t, 3, "http://test-orchestrator:8080")

	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", rr.Code)
	}

	var result map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&result)

	if result["shard_id"] != float64(3) {
		t.Errorf("Expected shard_id=3, got %v", result["shard_id"])
	}
	if result["orchestrator"] != "http://test-orchestrator:8080" {
		t.Errorf("Expected orchestrator URL, got %v", result["orchestrator"])
	}
}

// =============================================================================
// Orchestrator Block Handling Tests
// =============================================================================

// Helper to send orchestrator block to server
func sendOrchestratorBlock(t *testing.T, server *Server, block protocol.OrchestratorShardBlock) (int, map[string]string) {
	t.Helper()
	blockBody, _ := json.Marshal(block)
	req := httptest.NewRequest(http.MethodPost, "/orchestrator-shard/block", bytes.NewReader(blockBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)

	var result map[string]string
	json.NewDecoder(rr.Body).Decode(&result)
	return rr.Code, result
}

func TestOrchestratorBlock_SimpleValueTransfer(t *testing.T) {
	// Test that simple value transfers (no WriteSet) properly credit recipients
	// This tests the fix for the bug where WriteSet was required for credits

	sourceServer := setupTestServer(t, 0, "http://localhost:8080")
	destServer := setupTestServer(t, 1, "http://localhost:8080")

	// Fund sender on source shard
	sender := "0x0000000000000000000000000000000000000000"
	receiver := "0x0000000000000000000000000000000000000001" // on shard 1
	fundAccount(t, sourceServer, sender, "1000")

	// Create cross-shard tx (simple value transfer - no WriteSet)
	tx := protocol.CrossShardTx{
		ID:        "test-transfer-1",
		FromShard: 0,
		From:      common.HexToAddress(sender),
		To:        common.HexToAddress(receiver), // Must set To for proper credit routing
		Value:     protocol.NewBigInt(big.NewInt(500)),
		RwSet: []protocol.RwVariable{
			{
				Address:        common.HexToAddress(receiver),
				ReferenceBlock: protocol.Reference{ShardNum: 1},
				// No WriteSet - this is a simple value transfer
			},
		},
	}

	// Set up simulation lock on destination shard (required for RwSet validation)
	destServer.chain.LockAddress(tx.ID, common.HexToAddress(receiver), big.NewInt(0), 0, nil, common.Hash{}, nil)

	// Phase 1: Send CtToOrder block to both shards
	block1 := protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{tx},
		TpcResult: map[string]bool{},
	}

	code, _ := sendOrchestratorBlock(t, sourceServer, block1)
	if code != http.StatusOK {
		t.Fatalf("Source shard failed to process block: %d", code)
	}

	code, _ = sendOrchestratorBlock(t, destServer, block1)
	if code != http.StatusOK {
		t.Fatalf("Dest shard failed to process block: %d", code)
	}

	// Verify source shard locked funds
	lock, ok := sourceServer.chain.GetLockedFunds(tx.ID)
	if !ok {
		t.Error("Source shard should have locked funds")
	}
	if lock.Amount.Cmp(big.NewInt(500)) != 0 {
		t.Errorf("Lock amount should be 500, got %s", lock.Amount.String())
	}

	// Verify dest shard stored pending credit (this is what the bug prevented)
	credits, ok := destServer.chain.GetPendingCredits(tx.ID)
	if !ok {
		t.Error("Dest shard should have pending credit (bug fix verification)")
	}
	if len(credits) != 1 {
		t.Errorf("Expected 1 pending credit, got %d", len(credits))
	}
	if credits[0].Amount.Cmp(big.NewInt(500)) != 0 {
		t.Errorf("Credit amount should be 500, got %s", credits[0].Amount.String())
	}

	// Phase 2: Send TpcResult=true (commit)
	block2 := protocol.OrchestratorShardBlock{
		Height:    2,
		CtToOrder: []protocol.CrossShardTx{},
		TpcResult: map[string]bool{tx.ID: true},
	}

	code, _ = sendOrchestratorBlock(t, sourceServer, block2)
	if code != http.StatusOK {
		t.Fatalf("Source shard failed to process commit: %d", code)
	}

	code, _ = sendOrchestratorBlock(t, destServer, block2)
	if code != http.StatusOK {
		t.Fatalf("Dest shard failed to process commit: %d", code)
	}

	// Verify sender was debited
	senderBalance := sourceServer.evmState.GetBalance(common.HexToAddress(sender))
	if senderBalance.Cmp(big.NewInt(500)) != 0 {
		t.Errorf("Sender balance should be 500 after debit, got %s", senderBalance.String())
	}

	// Verify receiver was credited
	receiverBalance := destServer.evmState.GetBalance(common.HexToAddress(receiver))
	if receiverBalance.Cmp(big.NewInt(500)) != 0 {
		t.Errorf("Receiver balance should be 500 after credit, got %s", receiverBalance.String())
	}

	// Verify locks/credits are cleared
	_, ok = sourceServer.chain.GetLockedFunds(tx.ID)
	if ok {
		t.Error("Lock should be cleared after commit")
	}
	_, ok = destServer.chain.GetPendingCredits(tx.ID)
	if ok {
		t.Error("Pending credit should be cleared after commit")
	}
}

func TestOrchestratorBlock_AbortClearsLock(t *testing.T) {
	// Test that abort properly clears locks without debiting

	sourceServer := setupTestServer(t, 0, "http://localhost:8080")
	destServer := setupTestServer(t, 1, "http://localhost:8080")

	sender := "0x0000000000000000000000000000000000000000"
	receiver := "0x0000000000000000000000000000000000000001"
	fundAccount(t, sourceServer, sender, "1000")

	tx := protocol.CrossShardTx{
		ID:        "test-abort-1",
		FromShard: 0,
		From:      common.HexToAddress(sender),
		Value:     protocol.NewBigInt(big.NewInt(500)),
		RwSet: []protocol.RwVariable{
			{
				Address:        common.HexToAddress(receiver),
				ReferenceBlock: protocol.Reference{ShardNum: 1},
			},
		},
	}

	// Set up simulation lock on destination shard (required for RwSet validation)
	destServer.chain.LockAddress(tx.ID, common.HexToAddress(receiver), big.NewInt(0), 0, nil, common.Hash{}, nil)

	// Phase 1: CtToOrder
	block1 := protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{tx},
		TpcResult: map[string]bool{},
	}
	sendOrchestratorBlock(t, sourceServer, block1)
	sendOrchestratorBlock(t, destServer, block1)

	// Verify lock exists
	_, ok := sourceServer.chain.GetLockedFunds(tx.ID)
	if !ok {
		t.Error("Lock should exist after CtToOrder")
	}

	// Phase 2: TpcResult=false (abort)
	block2 := protocol.OrchestratorShardBlock{
		Height:    2,
		CtToOrder: []protocol.CrossShardTx{},
		TpcResult: map[string]bool{tx.ID: false},
	}
	sendOrchestratorBlock(t, sourceServer, block2)
	sendOrchestratorBlock(t, destServer, block2)

	// Verify sender NOT debited (lock-only approach: no refund needed)
	senderBalance := sourceServer.evmState.GetBalance(common.HexToAddress(sender))
	if senderBalance.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Sender balance should still be 1000 after abort, got %s", senderBalance.String())
	}

	// Verify receiver NOT credited
	receiverBalance := destServer.evmState.GetBalance(common.HexToAddress(receiver))
	if receiverBalance.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("Receiver balance should be 0 after abort, got %s", receiverBalance.String())
	}

	// Verify locks/credits are cleared
	_, ok = sourceServer.chain.GetLockedFunds(tx.ID)
	if ok {
		t.Error("Lock should be cleared after abort")
	}
}

func TestOrchestratorBlock_MultipleAddressesInRwSet(t *testing.T) {
	// Test that only tx.To receives the value credit, NOT all addresses in RwSet
	// This verifies the "money printer" bug fix - a tx with value=100 and multiple
	// RwSet addresses should only credit tx.To, not all touched addresses.

	sourceServer := setupTestServer(t, 0, "http://localhost:8080")
	dest1Server := setupTestServer(t, 1, "http://localhost:8080")
	dest2Server := setupTestServer(t, 2, "http://localhost:8080")

	sender := "0x0000000000000000000000000000000000000000"
	receiver1 := "0x0000000000000000000000000000000000000001" // shard 1 - tx.To
	receiver2 := "0x0000000000000000000000000000000000000002" // shard 2 - just in RwSet
	fundAccount(t, sourceServer, sender, "1000")

	tx := protocol.CrossShardTx{
		ID:        "test-multi-1",
		FromShard: 0,
		From:      common.HexToAddress(sender),
		To:        common.HexToAddress(receiver1), // Only this address should get value
		Value:     protocol.NewBigInt(big.NewInt(100)),
		RwSet: []protocol.RwVariable{
			{
				Address:        common.HexToAddress(receiver1),
				ReferenceBlock: protocol.Reference{ShardNum: 1},
			},
			{
				Address:        common.HexToAddress(receiver2),
				ReferenceBlock: protocol.Reference{ShardNum: 2},
			},
		},
	}

	// Set up simulation locks on destination shards (required for RwSet validation)
	dest1Server.chain.LockAddress(tx.ID, common.HexToAddress(receiver1), big.NewInt(0), 0, nil, common.Hash{}, nil)
	dest2Server.chain.LockAddress(tx.ID, common.HexToAddress(receiver2), big.NewInt(0), 0, nil, common.Hash{}, nil)

	// Phase 1: CtToOrder
	block1 := protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{tx},
		TpcResult: map[string]bool{},
	}
	sendOrchestratorBlock(t, sourceServer, block1)
	sendOrchestratorBlock(t, dest1Server, block1)
	sendOrchestratorBlock(t, dest2Server, block1)

	// Verify ONLY tx.To shard has pending credit (shard 1), NOT shard 2
	credits1, ok1 := dest1Server.chain.GetPendingCredits(tx.ID)
	_, ok2 := dest2Server.chain.GetPendingCredits(tx.ID)
	if !ok1 {
		t.Error("Dest shard for tx.To should have pending credit")
	}
	if ok2 {
		t.Error("Other RwSet address should NOT have pending credit (money printer bug!)")
	}
	if len(credits1) != 1 {
		t.Errorf("Expected 1 credit for tx.To, got %d", len(credits1))
	}

	// Phase 2: Commit
	block2 := protocol.OrchestratorShardBlock{
		Height:    2,
		CtToOrder: []protocol.CrossShardTx{},
		TpcResult: map[string]bool{tx.ID: true},
	}
	sendOrchestratorBlock(t, sourceServer, block2)
	sendOrchestratorBlock(t, dest1Server, block2)
	sendOrchestratorBlock(t, dest2Server, block2)

	// Verify ONLY receiver1 (tx.To) credited, receiver2 should have 0
	r1Balance := dest1Server.evmState.GetBalance(common.HexToAddress(receiver1))
	r2Balance := dest2Server.evmState.GetBalance(common.HexToAddress(receiver2))
	if r1Balance.Cmp(big.NewInt(100)) != 0 {
		t.Errorf("Receiver1 (tx.To) balance should be 100, got %s", r1Balance.String())
	}
	if r2Balance.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("Receiver2 (not tx.To) should NOT be credited, got %s (money printer!)", r2Balance.String())
	}
}

func TestOrchestratorBlock_SourceShardVotesNo(t *testing.T) {
	// Test that insufficient balance causes a NO vote

	sourceServer := setupTestServer(t, 0, "http://localhost:8080")

	sender := "0x0000000000000000000000000000000000000000"
	receiver := "0x0000000000000000000000000000000000000001"

	// Don't fund sender - should vote NO due to insufficient balance

	tx := protocol.CrossShardTx{
		ID:        "test-no-vote-1",
		FromShard: 0,
		From:      common.HexToAddress(sender),
		Value:     protocol.NewBigInt(big.NewInt(500)),
		RwSet: []protocol.RwVariable{
			{
				Address:        common.HexToAddress(receiver),
				ReferenceBlock: protocol.Reference{ShardNum: 1},
			},
		},
	}

	block1 := protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{tx},
		TpcResult: map[string]bool{},
	}
	sendOrchestratorBlock(t, sourceServer, block1)

	// Verify NO vote was recorded (because sender has no funds)
	stateBlock, err := sourceServer.chain.ProduceBlock(sourceServer.evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}
	vote, ok := stateBlock.TpcPrepare[tx.ID]
	if !ok {
		t.Error("Vote should be recorded")
	}
	if vote {
		t.Error("Vote should be NO (false) due to insufficient balance")
	}

	// Verify no lock was created (since we can't commit anyway)
	_, ok = sourceServer.chain.GetLockedFunds(tx.ID)
	if ok {
		t.Error("Lock should not be created when voting NO")
	}
}
