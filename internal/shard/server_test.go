package shard

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sharding-experiment/sharding/config"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// Helper to create test server
func newTestServer(t *testing.T, shardID int) *Server {
	server := NewServerForTest(shardID, "http://localhost:8080", config.NetworkConfig{})
	return server
}

// =============================================================================
// Basic Handler Tests
// =============================================================================

// TestHandler_GetBalance tests GET /balance/{address}
func TestHandler_GetBalance(t *testing.T) {
	server := newTestServer(t, 0)

	// Set up an account with balance
	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	server.evmState.Credit(addr, big.NewInt(1000000))

	t.Run("existing account", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/balance/"+addr.Hex(), nil)
		w := httptest.NewRecorder()
		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}

		if resp["balance"].(string) != "1000000" {
			t.Errorf("Expected balance '1000000', got '%s'", resp["balance"])
		}
	})

	t.Run("non-existent account", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/balance/0x2222222222222222222222222222222222222222", nil)
		w := httptest.NewRecorder()
		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["balance"].(string) != "0" {
			t.Errorf("Expected balance '0' for new account, got '%s'", resp["balance"])
		}
	})
}

// TestHandler_LocalTransfer tests POST /transfer (local transfer)
func TestHandler_LocalTransfer(t *testing.T) {
	server := newTestServer(t, 0)

	// Set up sender with balance
	sender := common.HexToAddress("0x0000000000000000000000000000000000000001")
	receiver := common.HexToAddress("0x0000000000000000000000000000000000000002")
	server.evmState.Credit(sender, big.NewInt(1000))

	t.Run("valid transfer", func(t *testing.T) {
		body := map[string]interface{}{
			"from":   sender.Hex(),
			"to":     receiver.Hex(),
			"amount": "100",
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/transfer", bytes.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}

		// Verify balances
		senderBal := server.evmState.GetBalance(sender)
		receiverBal := server.evmState.GetBalance(receiver)

		if senderBal.Cmp(big.NewInt(900)) != 0 {
			t.Errorf("Expected sender balance 900, got %s", senderBal.String())
		}
		if receiverBal.Cmp(big.NewInt(100)) != 0 {
			t.Errorf("Expected receiver balance 100, got %s", receiverBal.String())
		}
	})

	t.Run("insufficient balance", func(t *testing.T) {
		body := map[string]interface{}{
			"from":   sender.Hex(),
			"to":     receiver.Hex(),
			"amount": "10000", // More than balance
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/transfer", bytes.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400 for insufficient balance, got %d", w.Code)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/transfer", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400 for invalid JSON, got %d", w.Code)
		}
	})
}

// TestHandler_Faucet tests POST /faucet
func TestHandler_Faucet(t *testing.T) {
	server := newTestServer(t, 0)
	addr := common.HexToAddress("0x0000000000000000000000000000000000000001")

	t.Run("faucet request", func(t *testing.T) {
		body := map[string]interface{}{
			"address": addr.Hex(),
			"amount":  "1000000000000000000", // 1 ETH
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/faucet", bytes.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}

		// Verify balance increased
		balance := server.evmState.GetBalance(addr)
		if balance.Cmp(big.NewInt(0)) <= 0 {
			t.Error("Expected balance to be positive after faucet")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/faucet", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

// =============================================================================
// EVM Handler Tests
// =============================================================================

// TestHandler_Deploy tests POST /evm/deploy
func TestHandler_Deploy(t *testing.T) {
	server := newTestServer(t, 0)

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/evm/deploy", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

// TestHandler_SetCode tests POST /evm/setcode
func TestHandler_SetCode(t *testing.T) {
	server := newTestServer(t, 0)

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/evm/setcode", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

// TestHandler_GetCode tests GET /evm/code/{address}
func TestHandler_GetCode(t *testing.T) {
	server := newTestServer(t, 0)
	// Use address that belongs to shard 0
	addr := common.HexToAddress("0x0000000000000000000000000000000000000008")

	// Set some code
	bytecode, _ := hex.DecodeString("60006000f3")
	server.evmState.SetCode(addr, bytecode)

	t.Run("existing code", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/evm/code/"+addr.Hex(), nil)
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		// Check if code contains the expected bytes (may or may not have 0x prefix)
		codeStr := resp["code"].(string)
		if codeStr != "0x60006000f3" && codeStr != "60006000f3" {
			t.Errorf("Expected code containing '60006000f3', got '%s'", codeStr)
		}
	})

	t.Run("no code", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/evm/code/0x0000000000000000000000000000000000000010", nil)
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		codeStr := resp["code"].(string)
		if codeStr != "0x" && codeStr != "" {
			t.Errorf("Expected empty code, got '%s'", codeStr)
		}
	})
}

// TestHandler_GetStorage tests GET /evm/storage/{address}/{slot}
func TestHandler_GetStorage(t *testing.T) {
	server := newTestServer(t, 0)
	// Use address that belongs to shard 0
	addr := common.HexToAddress("0x0000000000000000000000000000000000000008")
	slot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	value := common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000000ff")

	// Set storage value
	server.evmState.SetStorageAt(addr, slot, value)

	t.Run("existing storage", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/evm/storage/"+addr.Hex()+"/"+slot.Hex(), nil)
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["value"].(string) != value.Hex() {
			t.Errorf("Expected value '%s', got '%s'", value.Hex(), resp["value"])
		}
	})

	t.Run("empty slot", func(t *testing.T) {
		emptySlot := common.HexToHash("0x9999999999999999999999999999999999999999999999999999999999999999")
		req := httptest.NewRequest("GET", "/evm/storage/"+addr.Hex()+"/"+emptySlot.Hex(), nil)
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}
	})
}

// TestHandler_GetStateRoot tests GET /evm/stateroot
func TestHandler_GetStateRoot(t *testing.T) {
	server := newTestServer(t, 0)

	req := httptest.NewRequest("GET", "/evm/stateroot", nil)
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["state_root"] == nil {
		t.Error("Expected state_root in response")
	}
}

// TestHandler_Call tests POST /evm/call
func TestHandler_Call(t *testing.T) {
	server := newTestServer(t, 0)

	// Deploy a simple contract that returns a value
	contractAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	// This bytecode returns 42 when called
	// PUSH1 0x2a PUSH1 0x00 MSTORE PUSH1 0x20 PUSH1 0x00 RETURN
	bytecode, _ := hex.DecodeString("602a60005260206000f3")
	server.evmState.SetCode(contractAddr, bytecode)

	caller := common.HexToAddress("0x0000000000000000000000000000000000000001")
	server.evmState.Credit(caller, big.NewInt(1e18))

	t.Run("call contract", func(t *testing.T) {
		body := map[string]interface{}{
			"from": caller.Hex(),
			"to":   contractAddr.Hex(),
			"data": "",
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/evm/call", bytes.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/evm/call", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

// TestHandler_StaticCall tests POST /evm/staticcall
func TestHandler_StaticCall(t *testing.T) {
	server := newTestServer(t, 0)

	contractAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	bytecode, _ := hex.DecodeString("602a60005260206000f3")
	server.evmState.SetCode(contractAddr, bytecode)

	t.Run("static call", func(t *testing.T) {
		body := map[string]interface{}{
			"to":   contractAddr.Hex(),
			"data": "",
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/evm/staticcall", bytes.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/evm/staticcall", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

// =============================================================================
// 2PC Handler Tests
// =============================================================================

// TestHandler_Prepare tests POST /cross-shard/prepare
func TestHandler_Prepare(t *testing.T) {
	server := newTestServer(t, 0)

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/cross-shard/prepare", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

// TestHandler_Credit tests POST /cross-shard/credit
func TestHandler_Credit(t *testing.T) {
	server := newTestServer(t, 0)

	receiver := common.HexToAddress("0x0000000000000000000000000000000000000001")

	t.Run("credit account", func(t *testing.T) {
		// CreditRequest expects: tx_id, address, amount (not to/value)
		body := map[string]interface{}{
			"tx_id":   "test-credit-1",
			"address": receiver.Hex(),
			"amount":  "500",
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/cross-shard/credit", bytes.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}

		// Note: handleCredit directly credits the account (doesn't store as pending)
		// Verify balance was credited
		balance := server.evmState.GetBalance(receiver)
		if balance.Cmp(big.NewInt(500)) != 0 {
			t.Errorf("Expected balance 500, got %s", balance.String())
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/cross-shard/credit", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

// TestHandler_Commit tests POST /cross-shard/commit
func TestHandler_Commit(t *testing.T) {
	server := newTestServer(t, 0)

	// Set up locked funds (commit debits locked funds, not pending credits)
	sender := common.HexToAddress("0x0000000000000000000000000000000000000001")
	server.evmState.Credit(sender, big.NewInt(1000)) // Fund the sender
	server.chain.LockFunds("commit-test-tx", sender, big.NewInt(100))

	t.Run("commit existing tx", func(t *testing.T) {
		body := map[string]interface{}{
			"tx_id": "commit-test-tx",
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/cross-shard/commit", bytes.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}

		// Verify funds were debited from sender (1000 - 100 = 900)
		balance := server.evmState.GetBalance(sender)
		if balance.Cmp(big.NewInt(900)) != 0 {
			t.Errorf("Expected balance 900, got %s", balance.String())
		}

		// Verify lock was cleared
		if _, ok := server.chain.GetLockedFunds("commit-test-tx"); ok {
			t.Error("Lock should have been cleared")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/cross-shard/commit", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

// TestHandler_Abort tests POST /cross-shard/abort
func TestHandler_Abort(t *testing.T) {
	server := newTestServer(t, 0)

	t.Run("abort tx", func(t *testing.T) {
		body := map[string]interface{}{
			"tx_id": "abort-test-tx",
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/cross-shard/abort", bytes.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/cross-shard/abort", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

// =============================================================================
// Cross-Shard Handler Tests
// =============================================================================

// TestHandler_CrossShardTransfer tests POST /cross-shard/transfer
func TestHandler_CrossShardTransfer(t *testing.T) {
	server := newTestServer(t, 0)

	sender := common.HexToAddress("0x0000000000000000000000000000000000000001")
	receiver := common.HexToAddress("0x0000000000000000000000000000000000000002")
	server.evmState.Credit(sender, big.NewInt(1000))

	t.Run("cross shard transfer invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/cross-shard/transfer", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})

	// Note: Full cross-shard transfer test would need a real orchestrator connection
	// Just test the request parsing and validation here
	t.Run("cross shard transfer with correct format", func(t *testing.T) {
		// CrossShardTransferRequest: from, to, to_shard, amount
		body := map[string]interface{}{
			"to_shard": 1,
			"from":     sender.Hex(),
			"to":       receiver.Hex(),
			"amount":   "100",
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/cross-shard/transfer", bytes.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		// Will fail to connect to orchestrator, but should not get "invalid amount" error
		// Accept either OK (if somehow connects) or 500 (can't connect to orchestrator)
		if w.Code == http.StatusBadRequest && w.Body.String() == "invalid amount\n" {
			t.Errorf("Got 'invalid amount' error - API format is wrong")
		}
	})
}

// =============================================================================
// State Fetch Handler Tests
// =============================================================================

// TestHandler_StateFetch tests POST /state/fetch
func TestHandler_StateFetch(t *testing.T) {
	server := newTestServer(t, 0)

	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	server.evmState.Credit(addr, big.NewInt(1000))

	t.Run("fetch account state", func(t *testing.T) {
		body := map[string]interface{}{
			"tx_id":   "fetch-test-1",
			"address": addr.Hex(),
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/state/fetch", bytes.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/state/fetch", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

// TestHandler_RwSet tests POST /rw-set
func TestHandler_RwSet(t *testing.T) {
	server := newTestServer(t, 0)

	contractAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	bytecode, _ := hex.DecodeString("602a60005260206000f3")
	server.evmState.SetCode(contractAddr, bytecode)

	caller := common.HexToAddress("0x0000000000000000000000000000000000000001")
	server.evmState.Credit(caller, big.NewInt(1e18))

	t.Run("rw set request", func(t *testing.T) {
		body := map[string]interface{}{
			"address": contractAddr.Hex(),
			"caller":  caller.Hex(),
			"data":    "",
			"value":   "0",
		}
		jsonBody, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/rw-set", bytes.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/rw-set", bytes.NewReader([]byte("invalid")))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		server.router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

// =============================================================================
// Server Method Tests
// =============================================================================

// TestServer_ProduceBlock tests the ProduceBlock method
func TestServer_ProduceBlock(t *testing.T) {
	server := newTestServer(t, 0)

	// Add a transaction using the Transaction type
	sender := common.HexToAddress("0x0000000000000000000000000000000000000001")
	receiver := common.HexToAddress("0x0000000000000000000000000000000000000002")
	server.evmState.Credit(sender, big.NewInt(1000))

	tx := protocol.Transaction{
		ID:       "produce-test-tx",
		TxType:   protocol.TxTypeLocal,
		From:     sender,
		To:       receiver,
		Value:    protocol.NewBigInt(big.NewInt(100)),
	}
	server.chain.AddTx(tx)

	// Produce block
	block, _ := server.ProduceBlock()

	if block == nil {
		t.Fatal("Expected block to be produced")
	}
	if block.Height != 1 {
		t.Errorf("Expected height 1, got %d", block.Height)
	}
}

// TestServer_SetStorageAt tests the SetStorageAt method
func TestServer_SetStorageAt(t *testing.T) {
	server := newTestServer(t, 0)

	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	slot := common.HexToHash("0x01")
	value := common.HexToHash("0xff")

	server.SetStorageAt(addr, slot, value)

	got := server.GetStorageAt(addr, slot)
	if got != value {
		t.Errorf("Expected %s, got %s", value.Hex(), got.Hex())
	}
}

// TestServer_GetBalance tests the GetBalance method
func TestServer_GetBalance(t *testing.T) {
	server := newTestServer(t, 0)

	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	server.evmState.Credit(addr, big.NewInt(12345))

	balance := server.GetBalance(addr)
	if balance.Cmp(big.NewInt(12345)) != 0 {
		t.Errorf("Expected 12345, got %s", balance.String())
	}
}

// TestServer_IsSlotLocked tests the IsSlotLocked method
func TestServer_IsSlotLocked(t *testing.T) {
	server := newTestServer(t, 0)

	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	slot := common.HexToHash("0x01")

	// Initially not locked
	if server.IsSlotLocked(addr, slot) {
		t.Error("Slot should not be locked initially")
	}

	// Lock the slot
	server.chain.LockSlot("test-tx", addr, slot)

	// Now should be locked
	if !server.IsSlotLocked(addr, slot) {
		t.Error("Slot should be locked after LockSlot")
	}
}

// =============================================================================
// Server Lifecycle Tests
// =============================================================================

// TestServer_Close_Idempotent verifies that Close() can be called multiple times safely.
// This tests the sync.Once protection against double-close panic.
func TestServer_Close_Idempotent(t *testing.T) {
	// Create server with block producer (not test server)
	server := NewServer(99, "", config.NetworkConfig{})

	// First close should succeed
	server.Close()

	// Second close should not panic (tests sync.Once)
	server.Close()

	// Third close for good measure
	server.Close()
}

// TestServer_Close_NilDoneChannel verifies Close() handles nil done channel gracefully.
// This is important for servers created with NewServerForTest.
func TestServer_Close_NilDoneChannel(t *testing.T) {
	server := newTestServer(t, 0)

	// Test server has nil done channel - Close should not panic
	server.Close()
	server.Close() // Multiple calls should be safe
}

// TestServer_Close_StopsBlockProducer verifies that Close() stops the block producer goroutine.
func TestServer_Close_StopsBlockProducer(t *testing.T) {
	baseline := runtime.NumGoroutine()

	// Create a real server (starts block producer goroutine)
	server := NewServer(99, "", config.NetworkConfig{})

	// Server should have started block producer goroutine
	afterCreate := runtime.NumGoroutine()
	if afterCreate <= baseline {
		t.Logf("Warning: Expected goroutines to increase (baseline=%d, after=%d)", baseline, afterCreate)
	}

	// Close the server
	server.Close()

	// Poll for goroutines to stop
	deadline := time.Now().Add(2 * time.Second)
	var afterClose int
	for time.Now().Before(deadline) {
		afterClose = runtime.NumGoroutine()
		if afterClose <= baseline+2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Allow for some tolerance (runtime may have other goroutines)
	if afterClose > baseline+2 {
		t.Errorf("Goroutine leak: baseline=%d, afterCreate=%d, afterClose=%d",
			baseline, afterCreate, afterClose)
	} else {
		t.Logf("Server goroutine test passed: baseline=%d, afterCreate=%d, afterClose=%d",
			baseline, afterCreate, afterClose)
	}
}

// =============================================================================
// Additional Coverage Tests
// =============================================================================

// TestHandler_Prepare_Success tests successful prepare request
func TestHandler_Prepare_Success(t *testing.T) {
	server := newTestServer(t, 0)

	// Fund the account
	sender := common.HexToAddress("0x0000000000000000000000000000000000000001")
	server.evmState.Credit(sender, big.NewInt(1000))

	// Use protocol.PrepareRequest for correct JSON serialization
	prepReq := protocol.PrepareRequest{
		TxID:    "prepare-test-1",
		Address: sender,
		Amount:  big.NewInt(500),
	}
	jsonBody, _ := json.Marshal(prepReq)
	req := httptest.NewRequest("POST", "/cross-shard/prepare", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["success"] != true {
		t.Errorf("Expected success=true, got %v", resp["success"])
	}

	// Verify funds are locked
	lock, ok := server.chain.GetLockedFunds("prepare-test-1")
	if !ok {
		t.Error("Expected funds to be locked")
	} else if lock.Amount.Cmp(big.NewInt(500)) != 0 {
		t.Errorf("Expected locked amount 500, got %s", lock.Amount.String())
	}
}

// TestHandler_Prepare_InsufficientBalance tests prepare with insufficient funds
func TestHandler_Prepare_InsufficientBalance(t *testing.T) {
	server := newTestServer(t, 0)

	// Fund with less than requested
	sender := common.HexToAddress("0x0000000000000000000000000000000000000001")
	server.evmState.Credit(sender, big.NewInt(100))

	prepReq := protocol.PrepareRequest{
		TxID:    "prepare-test-fail",
		Address: sender,
		Amount:  big.NewInt(500), // More than balance
	}
	jsonBody, _ := json.Marshal(prepReq)
	req := httptest.NewRequest("POST", "/cross-shard/prepare", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 (with success=false), got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["success"] != false {
		t.Errorf("Expected success=false, got %v", resp["success"])
	}
	if resp["error"] == nil || resp["error"] == "" {
		t.Error("Expected error message for insufficient balance")
	}
}

// TestHandler_SetCode_Success tests successful code deployment
func TestHandler_SetCode_Success(t *testing.T) {
	server := newTestServer(t, 0)

	// Use address that belongs to shard 0 (last byte % 8 == 0)
	addr := common.HexToAddress("0x1111111111111111111111111111111111111110") // ends in 0

	body := map[string]interface{}{
		"address": addr.Hex(),
		"code":    "0x60006000f3", // Simple bytecode
		"storage": map[string]string{
			"0x0000000000000000000000000000000000000000000000000000000000000001": "0x0000000000000000000000000000000000000000000000000000000000000042",
		},
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/evm/setcode", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["success"] != true {
		t.Errorf("Expected success=true, got %v", resp["success"])
	}
	if resp["storage_slots"].(float64) != 1 {
		t.Errorf("Expected storage_slots=1, got %v", resp["storage_slots"])
	}

	// Verify code was set
	code := server.evmState.GetCode(addr)
	if len(code) == 0 {
		t.Error("Expected code to be set")
	}

	// Verify storage was set
	slot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	value := server.evmState.GetStorageAt(addr, slot)
	if value != common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000042") {
		t.Errorf("Expected storage value 0x42, got %s", value.Hex())
	}
}

// TestHandler_SetCode_WrongShard tests code deployment to wrong shard
func TestHandler_SetCode_WrongShard(t *testing.T) {
	server := newTestServer(t, 0)

	// Use address that belongs to shard 1 (last byte % 8 == 1)
	addr := common.HexToAddress("0x1111111111111111111111111111111111111111") // ends in 1

	body := map[string]interface{}{
		"address": addr.Hex(),
		"code":    "0x60006000f3",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/evm/setcode", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for wrong shard, got %d", w.Code)
	}
}

// TestHandler_RwSet_WithStorageAccess tests RwSet request with storage access
func TestHandler_RwSet_WithStorageAccess(t *testing.T) {
	server := newTestServer(t, 0)

	// Deploy a contract that reads/writes storage
	// Bytecode: PUSH1 0x42, PUSH1 0x00, SSTORE (store 0x42 at slot 0)
	contractAddr := common.HexToAddress("0x0000000000000000000000000000000000000008")
	bytecode, _ := hex.DecodeString("604260005560206000f3")
	server.evmState.SetCode(contractAddr, bytecode)

	// Set initial storage
	slot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000")
	server.evmState.SetStorageAt(contractAddr, slot, common.HexToHash("0x01"))

	caller := common.HexToAddress("0x0000000000000000000000000000000000000001")
	server.evmState.Credit(caller, big.NewInt(1e18))

	body := map[string]interface{}{
		"address": contractAddr.Hex(),
		"caller":  caller.Hex(),
		"data":    "",
		"value":   "0",
		"tx_id":   "rwset-test-1",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/rw-set", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["success"] != true {
		t.Errorf("Expected success=true, got %v (error: %v)", resp["success"], resp["error"])
	}
}

// TestHandler_RwSet_NoContract tests RwSet request to address without contract
func TestHandler_RwSet_NoContract(t *testing.T) {
	server := newTestServer(t, 0)

	// Address with no contract code
	contractAddr := common.HexToAddress("0x0000000000000000000000000000000000000020")

	caller := common.HexToAddress("0x0000000000000000000000000000000000000001")
	server.evmState.Credit(caller, big.NewInt(1e18))

	body := map[string]interface{}{
		"address": contractAddr.Hex(),
		"caller":  caller.Hex(),
		"data":    "0x12345678",
		"value":   "0",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/rw-set", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	// Should return OK with success=true (no code = no-op call)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandler_Abort tests POST /cross-shard/abort
func TestHandler_Abort_WithLockedFunds(t *testing.T) {
	server := newTestServer(t, 0)

	// Set up locked funds
	sender := common.HexToAddress("0x0000000000000000000000000000000000000001")
	server.evmState.Credit(sender, big.NewInt(1000))
	server.chain.LockFunds("abort-test-tx", sender, big.NewInt(100))

	// Verify funds are locked
	if _, ok := server.chain.GetLockedFunds("abort-test-tx"); !ok {
		t.Fatal("Funds should be locked before abort")
	}

	body := map[string]interface{}{
		"tx_id": "abort-test-tx",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/cross-shard/abort", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify funds are unlocked (not debited, just released)
	if _, ok := server.chain.GetLockedFunds("abort-test-tx"); ok {
		t.Error("Locked funds should be cleared after abort")
	}

	// Balance should remain unchanged (lock released, no debit)
	balance := server.evmState.GetBalance(sender)
	if balance.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Expected balance 1000 after abort, got %s", balance.String())
	}
}
