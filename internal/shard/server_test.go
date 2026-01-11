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
	tests := []struct {
		address  string
		expected int
	}{
		{"0x0000000000000000000000000000000000000000", 0},
		{"0x0000000000000000000000000000000000000001", 1},
		{"0x0000000000000000000000000000000000000007", 7},
		{"0x00000000000000000000000000000000000000FF", 7}, // 255 % 8 = 7
	}

	for _, tc := range tests {
		addr := common.HexToAddress(tc.address)
		shard := int(addr[len(addr)-1]) % NumShards
		if shard != tc.expected {
			t.Errorf("Address %s: expected shard %d, got %d", tc.address, tc.expected, shard)
		}
	}
}

func TestHandleTxSubmit_WrongShard(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")

	// Sender on shard 1, submitting to shard 0
	code, _ := submitTx(t, server, TxSubmitRequest{
		From:  "0x0000000000000000000000000000000000000001",
		To:    "0x0000000000000000000000000000000000000000",
		Value: "100",
	})

	if code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for wrong shard sender, got %d", code)
	}
}

// =============================================================================
// Local Transfer Tests (Table-Driven)
// =============================================================================

func TestHandleTxSubmit_LocalTransfers(t *testing.T) {
	tests := []struct {
		name          string
		senderFund    string
		transferValue string
		expectSuccess bool
		expectQueued  bool
	}{
		{"normal transfer", "1000", "100", true, true},
		{"zero value", "0", "0", true, true},
		{"exact balance", "1000", "1000", true, true},
		{"insufficient balance", "99", "100", false, false},
		{"zero balance", "0", "100", false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := setupTestServer(t, 0, "http://localhost:8080")
			sender := "0x0000000000000000000000000000000000000000"
			recipient := "0x0000000000000000000000000000000000000008" // shard 0

			if tc.senderFund != "0" {
				fundAccount(t, server, sender, tc.senderFund)
			}

			code, result := submitTx(t, server, TxSubmitRequest{
				From:  sender,
				To:    recipient,
				Value: tc.transferValue,
			})

			if code != http.StatusOK {
				t.Fatalf("Expected 200 OK, got %d", code)
			}
			if result["success"] != tc.expectSuccess {
				t.Errorf("Expected success=%v, got %v (error: %v)", tc.expectSuccess, result["success"], result["error"])
			}
			if result["cross_shard"] != false {
				t.Errorf("Expected cross_shard=false, got %v", result["cross_shard"])
			}
		})
	}
}

func TestHandleTxSubmit_LocalTransfer_MultipleSequential(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")
	sender := "0x0000000000000000000000000000000000000000"
	recipient := "0x0000000000000000000000000000000000000008"

	fundAccount(t, server, sender, "1000")

	// Queue 3 transfers
	for i := 0; i < 3; i++ {
		code, result := submitTx(t, server, TxSubmitRequest{
			From:  sender,
			To:    recipient,
			Value: "100",
		})
		if code != http.StatusOK || result["success"] != true {
			t.Fatalf("Transfer %d failed", i+1)
		}
	}

	produceBlock(t, server)

	// Verify final balances
	if bal := getBalance(t, server, sender); bal.Cmp(big.NewInt(700)) != 0 {
		t.Errorf("Expected sender balance 700, got %s", bal)
	}
	if bal := getBalance(t, server, recipient); bal.Cmp(big.NewInt(300)) != 0 {
		t.Errorf("Expected recipient balance 300, got %s", bal)
	}
}

func TestHandleTxSubmit_LocalTransfer_SelfTransfer(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")
	addr := "0x0000000000000000000000000000000000000000"

	fundAccount(t, server, addr, "1000")
	submitTx(t, server, TxSubmitRequest{From: addr, To: addr, Value: "500"})

	if bal := getBalance(t, server, addr); bal.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Expected balance 1000 after self transfer, got %s", bal)
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
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "tx_id": receivedTx.ID})
	}))
	defer mockOrchestrator.Close()

	server := setupTestServer(t, 0, mockOrchestrator.URL)
	sender := "0x0000000000000000000000000000000000000000"
	recipient := "0x0000000000000000000000000000000000000001" // shard 1

	fundAccount(t, server, sender, "1000000000000000000")

	code, result := submitTx(t, server, TxSubmitRequest{
		From:  sender,
		To:    recipient,
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
	if receivedTx.FromShard != 0 {
		t.Errorf("Expected FromShard=0, got %d", receivedTx.FromShard)
	}
	if receivedTx.Gas != 50000 {
		t.Errorf("Expected Gas=50000, got %d", receivedTx.Gas)
	}
}

// Sample cross-shard pairs instead of testing all 56 combinations
func TestHandleTxSubmit_CrossShardTransfer_SamplePairs(t *testing.T) {
	samplePairs := [][2]int{{0, 1}, {0, 7}, {3, 5}, {7, 0}} // Representative sample

	for _, pair := range samplePairs {
		fromShard, toShard := pair[0], pair[1]
		t.Run(fmt.Sprintf("shard_%d_to_%d", fromShard, toShard), func(t *testing.T) {
			orchestratorCalled := false
			mockOrchestrator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				orchestratorCalled = true
				json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "tx_id": "test"})
			}))
			defer mockOrchestrator.Close()

			server := setupTestServer(t, fromShard, mockOrchestrator.URL)
			sender := common.Address{}
			sender[19] = byte(fromShard)
			recipient := common.Address{}
			recipient[19] = byte(toShard)

			fundAccount(t, server, sender.Hex(), "1000")

			code, result := submitTx(t, server, TxSubmitRequest{
				From:  sender.Hex(),
				To:    recipient.Hex(),
				Value: "100",
			})

			if code != http.StatusOK {
				t.Errorf("Expected 200 OK, got %d", code)
			}
			if !orchestratorCalled {
				t.Error("Expected orchestrator to be called")
			}
			if result["cross_shard"] != true {
				t.Error("Expected cross_shard=true")
			}
		})
	}
}

func TestHandleTxSubmit_CrossShardTransfer_OrchestratorDown(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:99999")
	sender := "0x0000000000000000000000000000000000000000"
	recipient := "0x0000000000000000000000000000000000000001"

	fundAccount(t, server, sender, "1000")

	code, _ := submitTx(t, server, TxSubmitRequest{
		From:  sender,
		To:    recipient,
		Value: "100",
	})

	if code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 Service Unavailable, got %d", code)
	}
}

// =============================================================================
// Contract Tests
// =============================================================================

func TestHandleDeploy(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")
	// Use sender 0x08 which produces contract on shard 0 (no forwarding needed)
	sender := "0x0000000000000000000000000000000000000008"
	fundAccount(t, server, sender, "10000000000000000000")

	// Simple contract: PUSH0 PUSH0 RETURN (0x5f5ff3)
	bytecode := "0x5f5ff3"
	deployReq := DeployRequest{From: sender, Bytecode: bytecode, Gas: 1000000}
	deployBody, _ := json.Marshal(deployReq)
	req := httptest.NewRequest(http.MethodPost, "/evm/deploy", bytes.NewReader(deployBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)

	var result map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&result)

	if rr.Code != http.StatusOK {
		// Skip if code forwarding fails (expected without Docker network)
		if strings.Contains(fmt.Sprintf("%v", result["error"]), "failed to forward") {
			t.Skip("Code forwarding not available in unit tests")
		}
		t.Fatalf("Deploy failed: %d - %v", rr.Code, result["error"])
	}

	if result["success"] != true {
		t.Errorf("Expected success=true, got %v", result["success"])
	}
	if result["address"] == nil {
		t.Error("Expected contract address in response")
	}
}

func TestHandleTxSubmit_ContractCall_LocalContract(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")
	sender := "0x0000000000000000000000000000000000000000"
	contract := "0x1111111111111111111111111111111111111100" // shard 0

	fundAccount(t, server, sender, "10000000000000000000")

	// Set simple contract code
	server.evmState.SetCode(common.HexToAddress(contract), common.FromHex("0x60426000526020600060f3"))

	code, result := submitTx(t, server, TxSubmitRequest{
		From:  sender,
		To:    contract,
		Value: "0",
		Data:  "0x",
		Gas:   100000,
	})

	if code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", code)
	}
	if result["cross_shard"] != false {
		t.Errorf("Expected cross_shard=false for local contract")
	}
	if result["success"] != true {
		t.Errorf("Expected success=true, got error: %v", result["error"])
	}
}

func TestHandleTxSubmit_ContractCall_CrossShardAddress(t *testing.T) {
	orchestratorCalled := false
	mockOrchestrator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orchestratorCalled = true
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "tx_id": "test"})
	}))
	defer mockOrchestrator.Close()

	server := setupTestServer(t, 0, mockOrchestrator.URL)
	sender := "0x0000000000000000000000000000000000000000"
	contract := "0x0000000000000000000000000000000000000001" // shard 1

	fundAccount(t, server, sender, "1000000000000000000")

	code, result := submitTx(t, server, TxSubmitRequest{
		From:  sender,
		To:    contract,
		Value: "0",
		Data:  "0xa9059cbb",
		Gas:   100000,
	})

	if code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", code)
	}
	if !orchestratorCalled {
		t.Error("Expected orchestrator to be called for cross-shard contract")
	}
	if result["cross_shard"] != true {
		t.Errorf("Expected cross_shard=true")
	}
}

// =============================================================================
// Gas Handling Tests
// =============================================================================

func TestHandleTxSubmit_GasLimits(t *testing.T) {
	tests := []struct {
		name        string
		gasProvided uint64
		expectedGas uint64
	}{
		{"default gas", 0, DefaultGasLimit},
		{"custom gas", 21000, 21000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var receivedTx protocol.CrossShardTx
			mockOrchestrator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				json.Unmarshal(body, &receivedTx)
				json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "tx_id": "test"})
			}))
			defer mockOrchestrator.Close()

			server := setupTestServer(t, 0, mockOrchestrator.URL)
			fundAccount(t, server, "0x0000000000000000000000000000000000000000", "1000")

			submitTx(t, server, TxSubmitRequest{
				From:  "0x0000000000000000000000000000000000000000",
				To:    "0x0000000000000000000000000000000000000001",
				Value: "100",
				Gas:   tc.gasProvided,
			})

			if receivedTx.Gas != tc.expectedGas {
				t.Errorf("Expected gas %d, got %d", tc.expectedGas, receivedTx.Gas)
			}
		})
	}
}

// =============================================================================
// Error Classification Tests
// =============================================================================

func TestIsDefiniteLocalError(t *testing.T) {
	localErrors := []string{
		"insufficient balance for transfer",
		"insufficient funds",
		"nonce too low",
		"INSUFFICIENT BALANCE", // case insensitive
	}
	for _, err := range localErrors {
		if !isDefiniteLocalError(err) {
			t.Errorf("Expected %q to be local error", err)
		}
	}

	nonLocalErrors := []string{
		"execution reverted",
		"out of gas",
		"some random error",
	}
	for _, err := range nonLocalErrors {
		if isDefiniteLocalError(err) {
			t.Errorf("Expected %q to NOT be local error", err)
		}
	}
}

// =============================================================================
// Request Validation Tests
// =============================================================================

func TestHandleTxSubmit_RequestValidation(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		expectedCode int
	}{
		{"invalid JSON", "not json", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := setupTestServer(t, 0, "http://localhost:8080")
			req := httptest.NewRequest(http.MethodPost, "/tx/submit", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			server.Router().ServeHTTP(rr, req)

			if rr.Code != tc.expectedCode {
				t.Errorf("Expected %d, got %d", tc.expectedCode, rr.Code)
			}
		})
	}
}

func TestHandleTxSubmit_InvalidValue(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")
	code, _ := submitTx(t, server, TxSubmitRequest{
		From:  "0x0000000000000000000000000000000000000000",
		To:    "0x0000000000000000000000000000000000000008",
		Value: "not a number",
	})
	if code != http.StatusBadRequest {
		t.Errorf("Expected 400 for invalid value, got %d", code)
	}
}

func TestHandleTxSubmit_EmptyValue(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")
	code, result := submitTx(t, server, TxSubmitRequest{
		From:  "0x0000000000000000000000000000000000000000",
		To:    "0x0000000000000000000000000000000000000008",
		Value: "",
	})
	if code != http.StatusOK || result["success"] != true {
		t.Errorf("Empty value should succeed with 0")
	}
}

// =============================================================================
// Health/Info Endpoints
// =============================================================================

func TestHealthCheck(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", rr.Code)
	}
}

func TestInfo(t *testing.T) {
	server := setupTestServer(t, 3, "http://test-orchestrator:8080")
	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)

	var result map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&result)

	if result["shard_id"] != float64(3) {
		t.Errorf("Expected shard_id=3, got %v", result["shard_id"])
	}
}

// =============================================================================
// Balance and Faucet Tests
// =============================================================================

func TestGetBalance(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")
	addr := "0x0000000000000000000000000000000000000000"

	if bal := getBalance(t, server, addr); bal.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("Expected initial balance 0, got %s", bal)
	}

	fundAccount(t, server, addr, "12345")
	if bal := getBalance(t, server, addr); bal.Cmp(big.NewInt(12345)) != 0 {
		t.Errorf("Expected balance 12345, got %s", bal)
	}
}

func TestFaucet(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")
	addr := "0x0000000000000000000000000000000000000000"

	fundAccount(t, server, addr, "100")
	fundAccount(t, server, addr, "200")
	fundAccount(t, server, addr, "300")

	if bal := getBalance(t, server, addr); bal.Cmp(big.NewInt(600)) != 0 {
		t.Errorf("Expected balance 600, got %s", bal)
	}
}

// =============================================================================
// Orchestrator Block Handling (core tests only - detailed tests in chain_test.go)
// =============================================================================

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
	sourceServer := setupTestServer(t, 0, "http://localhost:8080")
	destServer := setupTestServer(t, 1, "http://localhost:8080")

	sender := "0x0000000000000000000000000000000000000000"
	receiver := "0x0000000000000000000000000000000000000001"
	fundAccount(t, sourceServer, sender, "1000")

	tx := protocol.CrossShardTx{
		ID:        "test-transfer-1",
		FromShard: 0,
		From:      common.HexToAddress(sender),
		To:        common.HexToAddress(receiver),
		Value:     protocol.NewBigInt(big.NewInt(500)),
		RwSet: []protocol.RwVariable{{
			Address:        common.HexToAddress(receiver),
			ReferenceBlock: protocol.Reference{ShardNum: 1},
		}},
	}

	// V2 Optimistic: No pre-locking needed! Lock tx validates+locks atomically in ProduceBlock

	// Phase 1: CtToOrder - queues Lock transactions
	block1 := protocol.OrchestratorShardBlock{Height: 1, CtToOrder: []protocol.CrossShardTx{tx}}
	sendOrchestratorBlock(t, sourceServer, block1)
	sendOrchestratorBlock(t, destServer, block1)

	// Execute Phase 1: Lock tx validates balance and stores fund lock
	sourceServer.chain.ProduceBlock(sourceServer.evmState)
	destServer.chain.ProduceBlock(destServer.evmState)

	// Phase 2: Commit - queues Debit/Credit/Unlock based on stored lock info
	block2 := protocol.OrchestratorShardBlock{Height: 2, TpcResult: map[string]bool{tx.ID: true}}
	sendOrchestratorBlock(t, sourceServer, block2)
	sendOrchestratorBlock(t, destServer, block2)

	// Execute Phase 2: Debit, Credit, Unlock
	sourceServer.chain.ProduceBlock(sourceServer.evmState)
	destServer.chain.ProduceBlock(destServer.evmState)

	// Verify
	if bal := sourceServer.evmState.GetBalance(common.HexToAddress(sender)); bal.Cmp(big.NewInt(500)) != 0 {
		t.Errorf("Sender should have 500, got %s", bal)
	}
	if bal := destServer.evmState.GetBalance(common.HexToAddress(receiver)); bal.Cmp(big.NewInt(500)) != 0 {
		t.Errorf("Receiver should have 500, got %s", bal)
	}
}

func TestOrchestratorBlock_Abort(t *testing.T) {
	sourceServer := setupTestServer(t, 0, "http://localhost:8080")
	sender := "0x0000000000000000000000000000000000000000"
	receiver := "0x0000000000000000000000000000000000000001"
	fundAccount(t, sourceServer, sender, "1000")

	tx := protocol.CrossShardTx{
		ID:        "test-abort-1",
		FromShard: 0,
		From:      common.HexToAddress(sender),
		Value:     protocol.NewBigInt(big.NewInt(500)),
		RwSet: []protocol.RwVariable{{
			Address:        common.HexToAddress(receiver),
			ReferenceBlock: protocol.Reference{ShardNum: 1},
		}},
	}

	// Phase 1: CtToOrder - queues Lock transaction
	block1 := protocol.OrchestratorShardBlock{Height: 1, CtToOrder: []protocol.CrossShardTx{tx}}
	sendOrchestratorBlock(t, sourceServer, block1)

	// Execute Phase 1: Lock tx validates balance and stores fund lock
	sourceServer.chain.ProduceBlock(sourceServer.evmState)

	// Phase 2: Abort - releases funds lock
	block2 := protocol.OrchestratorShardBlock{Height: 2, TpcResult: map[string]bool{tx.ID: false}}
	sendOrchestratorBlock(t, sourceServer, block2)

	// Execute Phase 2: Abort clears metadata
	sourceServer.chain.ProduceBlock(sourceServer.evmState)

	// Verify sender NOT debited (funds were released)
	if bal := sourceServer.evmState.GetBalance(common.HexToAddress(sender)); bal.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Sender should still have 1000 after abort, got %s", bal)
	}
}
