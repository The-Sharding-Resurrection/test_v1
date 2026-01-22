package shard

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sharding-experiment/sharding/config"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// =============================================================================
// Test Helpers
// =============================================================================

func setupTestServer(t *testing.T, shardID int, orchestratorURL string) *Server {
	t.Helper()
	return NewServerForTest(shardID, orchestratorURL, config.NetworkConfig{})
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

// =============================================================================
// Chain Core Tests
// =============================================================================

func TestChainBasics(t *testing.T) {
	tests := []struct {
		name string
		test func(*testing.T, *Chain)
	}{
		{"genesis block exists", func(t *testing.T, c *Chain) {
			if c.height != 0 || len(c.blocks) != 1 || c.blocks[0].Height != 0 {
				t.Error("Invalid genesis block")
			}
		}},
		{"add transactions", func(t *testing.T, c *Chain) {
			c.AddTx(protocol.Transaction{ID: "tx-1", IsCrossShard: true})
			c.AddTx(protocol.Transaction{ID: "tx-2", IsCrossShard: false})
			if len(c.currentTxs) != 2 {
				t.Errorf("Expected 2 txs, got %d", len(c.currentTxs))
			}
		}},
		{"add prepare results", func(t *testing.T, c *Chain) {
			c.AddPrepareResult("tx-1", true)
			c.AddPrepareResult("tx-2", false)
			if len(c.prepares) != 2 || !c.prepares["tx-1"] || c.prepares["tx-2"] {
				t.Error("Prepare results mismatch")
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewChain(0)
			tt.test(t, c)
		})
	}
}

func TestChainBlockProduction(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	chain.AddTx(protocol.Transaction{ID: "tx-1", IsCrossShard: true})
	chain.AddPrepareResult("tx-1", true)

	block, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	if block.Height != 1 || len(block.TxOrdering) != 1 || !block.TpcPrepare["tx-1"] {
		t.Error("Block production failed")
	}

	// Verify state cleared
	if len(chain.currentTxs) != 0 || len(chain.prepares) != 0 {
		t.Error("State should be cleared after block")
	}
}

func TestBlockChaining(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Genesis block should have zero PrevHash
	genesis := chain.blocks[0]
	if genesis.PrevHash != (protocol.BlockHash{}) {
		t.Errorf("Genesis PrevHash should be zero, got %x", genesis.PrevHash)
	}

	// Produce block 1
	chain.AddTx(protocol.Transaction{ID: "tx-1"})
	block1, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("Failed to produce block 1: %v", err)
	}
	if block1.PrevHash != genesis.Hash() {
		t.Errorf("Block 1 PrevHash should link to genesis: expected %x, got %x",
			genesis.Hash(), block1.PrevHash)
	}

	// Produce block 2
	chain.AddTx(protocol.Transaction{ID: "tx-2"})
	block2, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("Failed to produce block 2: %v", err)
	}
	if block2.PrevHash != block1.Hash() {
		t.Errorf("Block 2 PrevHash should link to block 1: expected %x, got %x",
			block1.Hash(), block2.PrevHash)
	}

	// Verify chain integrity
	if chain.height != 2 || len(chain.blocks) != 3 {
		t.Errorf("Chain should have height 2 and 3 blocks, got height=%d blocks=%d",
			chain.height, len(chain.blocks))
	}
}

// =============================================================================
// Lock Management Tests
// =============================================================================

func TestFundLocking(t *testing.T) {
	chain := NewChain(0)
	addr := common.HexToAddress("0x1234")
	amount := big.NewInt(1000)

	// Test lock/unlock cycle
	chain.LockFunds("tx-1", addr, amount)
	lock, ok := chain.GetLockedFunds("tx-1")
	if !ok || lock.Address != addr || lock.Amount.Cmp(amount) != 0 {
		t.Error("Lock not stored correctly")
	}

	// Verify amount is copied
	amount.SetInt64(2000)
	if lock.Amount.Cmp(big.NewInt(1000)) != 0 {
		t.Error("Lock amount should be independent copy")
	}

	// Clear lock
	chain.ClearLock("tx-1")
	if _, ok := chain.GetLockedFunds("tx-1"); ok {
		t.Error("Lock should be cleared")
	}
}

func TestSlotLocking(t *testing.T) {
	chain := NewChain(0)
	addr := common.HexToAddress("0x1234")
	slot1, slot2 := common.HexToHash("0x01"), common.HexToHash("0x02")

	// Lock slot1 for tx1
	if err := chain.LockSlot("tx1", addr, slot1); err != nil {
		t.Fatalf("Failed to lock slot1: %v", err)
	}

	// Same tx can lock same slot again (idempotent)
	if err := chain.LockSlot("tx1", addr, slot1); err != nil {
		t.Fatalf("Re-locking same slot should work: %v", err)
	}

	// Different tx cannot lock same slot
	if err := chain.LockSlot("tx2", addr, slot1); err == nil {
		t.Error("Different tx should not lock same slot")
	}

	// Different slot can be locked by different tx
	if err := chain.LockSlot("tx2", addr, slot2); err != nil {
		t.Fatalf("Different slot should be lockable: %v", err)
	}

	// Unlock and verify
	chain.UnlockSlot("tx1", addr, slot1)
	if chain.IsSlotLocked(addr, slot1) {
		t.Error("slot1 should be unlocked")
	}
}

func TestPendingCredits(t *testing.T) {
	chain := NewChain(0)
	addr1, addr2 := common.HexToAddress("0x5678"), common.HexToAddress("0x9999")
	amount1, amount2 := big.NewInt(500), big.NewInt(300)

	chain.StorePendingCredit("tx-1", addr1, amount1)
	chain.StorePendingCredit("tx-1", addr2, amount2)

	credits, ok := chain.GetPendingCredits("tx-1")
	if !ok || len(credits) != 2 {
		t.Error("Pending credits not stored correctly")
	}

	chain.ClearPendingCredit("tx-1")
	if _, ok := chain.GetPendingCredits("tx-1"); ok {
		t.Error("Pending credits should be cleared")
	}
}

// =============================================================================
// Transaction Priority Tests (V2.4)
// =============================================================================

func TestTransactionPriority(t *testing.T) {
	tests := []struct {
		txType   protocol.TxType
		priority int
	}{
		{protocol.TxTypeCrossDebit, 1},
		{protocol.TxTypeCrossCredit, 1},
		{protocol.TxTypeCrossWriteSet, 1},
		{protocol.TxTypeUnlock, 2},
		{protocol.TxTypeLock, 3},
		{protocol.TxTypeLocal, 4},
	}

	for _, tt := range tests {
		if priority := tt.txType.Priority(); priority != tt.priority {
			t.Errorf("%s: expected priority %d, got %d", tt.txType, tt.priority, priority)
		}
	}
}

func TestTransactionOrdering(t *testing.T) {
	chain := NewChain(0)
	evmState, _ := NewMemoryEVMState()

	// Add transactions in wrong order
	chain.AddTx(protocol.Transaction{ID: "local-1", TxType: protocol.TxTypeLocal})
	chain.AddTx(protocol.Transaction{ID: "lock-1", TxType: protocol.TxTypeLock, IsCrossShard: true, CrossShardTxID: "ctx-1"})
	chain.AddTx(protocol.Transaction{ID: "unlock-1", TxType: protocol.TxTypeUnlock, IsCrossShard: true, CrossShardTxID: "ctx-2"})
	chain.AddTx(protocol.Transaction{ID: "finalize-1", TxType: protocol.TxTypeCrossDebit, IsCrossShard: true, CrossShardTxID: "ctx-3"})

	block, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	// Verify ordering: finalize(1) > unlock(2) > lock(3) > local(4)
	expectedOrder := []string{"finalize-1", "unlock-1", "lock-1", "local-1"}
	for i, expected := range expectedOrder {
		if block.TxOrdering[i].ID != expected {
			t.Errorf("Position %d: expected %s, got %s", i, expected, block.TxOrdering[i].ID)
		}
	}
}

// =============================================================================
// HTTP Endpoint Tests
// =============================================================================

func TestShardAssignment(t *testing.T) {
	tests := []struct {
		address  string
		expected int
	}{
		{"0x0000000000000000000000000000000000000000", 0},
		{"0x0000000000000000000000000000000000000001", 1},
		{"0x0000000000000000000000000000000000000007", 7},
		{"0x00000000000000000000000000000000000000FF", 7},
	}

	for _, tc := range tests {
		addr := common.HexToAddress(tc.address)
		shard := int(addr[len(addr)-1]) % config.GetConfig().ShardNum
		if shard != tc.expected {
			t.Errorf("Address %s: expected shard %d, got %d", tc.address, tc.expected, shard)
		}
	}
}

func TestHandleTxSubmit_WrongShard(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")
	code, _ := submitTx(t, server, TxSubmitRequest{
		From:  "0x0000000000000000000000000000000000000001", // shard 1
		To:    "0x0000000000000000000000000000000000000000",
		Value: "100",
	})

	if code != http.StatusBadRequest {
		t.Errorf("Expected 400 for wrong shard, got %d", code)
	}
}

func TestHandleTxSubmit_LocalTransfers(t *testing.T) {
	tests := []struct {
		name          string
		senderFund    string
		transferValue string
		expectSuccess bool
	}{
		{"normal transfer", "1000", "100", true},
		{"zero value", "0", "0", true},
		{"exact balance", "1000", "1000", true},
		{"insufficient balance", "99", "100", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := setupTestServer(t, 0, "http://localhost:8080")
			sender := "0x0000000000000000000000000000000000000000"
			recipient := "0x0000000000000000000000000000000000000008"

			if tc.senderFund != "0" {
				fundAccount(t, server, sender, tc.senderFund)
			}

			code, result := submitTx(t, server, TxSubmitRequest{
				From:  sender,
				To:    recipient,
				Value: tc.transferValue,
			})

			if code != http.StatusOK {
				t.Fatalf("Expected 200, got %d", code)
			}
			if result["success"] != tc.expectSuccess {
				t.Errorf("Expected success=%v, got %v", tc.expectSuccess, result["success"])
			}
			if result["cross_shard"] != false {
				t.Error("Expected cross_shard=false")
			}
		})
	}
}

func TestHandleTxSubmit_CrossShardTransfer(t *testing.T) {
	var receivedTx protocol.CrossShardTx
	mockOrchestrator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	if code != http.StatusOK || result["cross_shard"] != true {
		t.Error("Cross-shard transfer failed")
	}
	if receivedTx.FromShard != 0 || receivedTx.Gas != 50000 {
		t.Error("Transaction data mismatch")
	}
}

// =============================================================================
// V2 Protocol Tests
// =============================================================================

func TestOrchestratorBlock_2PC_Flow(t *testing.T) {
	sourceServer := setupTestServer(t, 0, "http://localhost:8080")
	destServer := setupTestServer(t, 1, "http://localhost:8080")

	sender := "0x0000000000000000000000000000000000000000"
	receiver := "0x0000000000000000000000000000000000000001"
	fundAccount(t, sourceServer, sender, "1000")

	tx := protocol.CrossShardTx{
		ID:        "test-2pc-flow",
		FromShard: 0,
		From:      common.HexToAddress(sender),
		To:        common.HexToAddress(receiver),
		Value:     protocol.NewBigInt(big.NewInt(500)),
		RwSet: []protocol.RwVariable{{
			Address:        common.HexToAddress(receiver),
			ReferenceBlock: protocol.Reference{ShardNum: 1},
		}},
	}

	// Phase 1: CtToOrder - queues Lock transactions
	block1 := protocol.OrchestratorShardBlock{Height: 1, CtToOrder: []protocol.CrossShardTx{tx}}
	sendOrchestratorBlock(t, sourceServer, block1)
	sendOrchestratorBlock(t, destServer, block1)

	// Execute Phase 1
	sourceServer.chain.ProduceBlock(sourceServer.evmState)
	destServer.chain.ProduceBlock(destServer.evmState)

	// Phase 2: Commit
	block2 := protocol.OrchestratorShardBlock{Height: 2, TpcResult: map[string]bool{tx.ID: true}}
	sendOrchestratorBlock(t, sourceServer, block2)
	sendOrchestratorBlock(t, destServer, block2)

	// Execute Phase 2
	sourceServer.chain.ProduceBlock(sourceServer.evmState)
	destServer.chain.ProduceBlock(destServer.evmState)

	// Verify balances
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
	fundAccount(t, sourceServer, sender, "1000")

	tx := protocol.CrossShardTx{
		ID:        "test-abort",
		FromShard: 0,
		From:      common.HexToAddress(sender),
		Value:     protocol.NewBigInt(big.NewInt(500)),
		RwSet: []protocol.RwVariable{{
			Address:        common.HexToAddress("0x0000000000000000000000000000000000000001"),
			ReferenceBlock: protocol.Reference{ShardNum: 1},
		}},
	}

	// Phase 1: Lock
	block1 := protocol.OrchestratorShardBlock{Height: 1, CtToOrder: []protocol.CrossShardTx{tx}}
	sendOrchestratorBlock(t, sourceServer, block1)
	sourceServer.chain.ProduceBlock(sourceServer.evmState)

	// Phase 2: Abort
	block2 := protocol.OrchestratorShardBlock{Height: 2, TpcResult: map[string]bool{tx.ID: false}}
	sendOrchestratorBlock(t, sourceServer, block2)
	sourceServer.chain.ProduceBlock(sourceServer.evmState)

	// Verify sender NOT debited
	if bal := sourceServer.evmState.GetBalance(common.HexToAddress(sender)); bal.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Sender should still have 1000 after abort, got %s", bal)
	}
}

func TestReadSetValidation(t *testing.T) {
	chain := NewChain(0)
	evmState, _ := NewMemoryEVMState()

	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")
	value := common.HexToHash("0xABCD")

	evmState.SetStorageAt(addr, slot, value)

	// Matching ReadSet should succeed
	rwSet := []protocol.RwVariable{{
		Address: addr,
		ReadSet: []protocol.ReadSetItem{{Slot: protocol.Slot(slot), Value: value.Bytes()}},
	}}

	if err := chain.ValidateAndLockReadSet("tx1", rwSet, evmState); err != nil {
		t.Fatalf("Validation should succeed: %v", err)
	}
	if !chain.IsSlotLocked(addr, slot) {
		t.Error("Slot should be locked after validation")
	}

	// Mismatched ReadSet should fail
	wrongValue := common.HexToHash("0x1111")
	rwSet2 := []protocol.RwVariable{{
		Address: addr,
		ReadSet: []protocol.ReadSetItem{{Slot: protocol.Slot(slot), Value: wrongValue.Bytes()}},
	}}

	if err := chain.ValidateAndLockReadSet("tx2", rwSet2, evmState); err == nil {
		t.Error("Validation should fail for mismatched value")
	}
}

// =============================================================================
// Concurrency & Security Tests
// =============================================================================

func TestAtomicBalanceCheck(t *testing.T) {
	server := NewServerForTest(0, "http://localhost:8080", config.NetworkConfig{})
	sender := common.HexToAddress("0x0000000000000000000000000000000000000000")
	server.evmState.Credit(sender, big.NewInt(1000))

	// Create 10 concurrent transactions each trying to lock 200
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			tx := protocol.CrossShardTx{
				ID:        "concurrent-tx-" + string(rune('A'+id)),
				FromShard: 0,
				From:      sender,
				Value:     protocol.NewBigInt(big.NewInt(200)),
				RwSet: []protocol.RwVariable{{
					Address:        common.HexToAddress("0x0000000000000000000000000000000000000001"),
					ReferenceBlock: protocol.Reference{ShardNum: 1},
				}},
			}

			block := protocol.OrchestratorShardBlock{
				Height:    uint64(id + 1),
				CtToOrder: []protocol.CrossShardTx{tx},
				TpcResult: make(map[string]bool),
			}

			blockData, _ := json.Marshal(block)
			req := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			server.Router().ServeHTTP(w, req)

			if _, ok := server.chain.GetLockedFunds(tx.ID); ok {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	// Should have at most 5 successful locks (1000 / 200)
	if successCount > 5 {
		t.Errorf("Expected at most 5 locks, got %d (race condition!)", successCount)
	}

	totalLocked := server.chain.GetLockedAmountForAddress(sender)
	if totalLocked.Cmp(big.NewInt(1000)) > 0 {
		t.Errorf("Total locked %s exceeds balance", totalLocked)
	}
}

func TestTrackingStateDB_ThreadSafety(t *testing.T) {
	evmState, _ := NewMemoryEVMState()

	// Create accounts sequentially (StateDB not thread-safe)
	for i := 0; i < 10; i++ {
		addr := common.BigToAddress(big.NewInt(int64(i)))
		evmState.Credit(addr, big.NewInt(1000000))
	}

	trackingDB := NewTrackingStateDB(evmState.stateDB, 0, 6)

	// Populate tracking maps
	for i := 0; i < 10; i++ {
		addr := common.BigToAddress(big.NewInt(int64(i)))
		slot := common.BigToHash(big.NewInt(int64(i)))
		value := common.BigToHash(big.NewInt(int64(i * 100)))
		trackingDB.SetState(addr, slot, value)
	}

	// Test concurrent READ access
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = trackingDB.GetAccessedAddresses()
		}()
		go func() {
			defer wg.Done()
			_ = trackingDB.HasCrossShardAccess()
		}()
		go func() {
			defer wg.Done()
			_ = trackingDB.GetStorageWrites()
		}()
	}

	wg.Wait()

	// Verify tracking worked
	if len(trackingDB.GetAccessedAddresses()) == 0 {
		t.Error("Expected tracked addresses")
	}
}

// =============================================================================
// Validation & Error Handling Tests
// =============================================================================

func TestRequestValidation(t *testing.T) {
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

func TestIsDefiniteLocalError(t *testing.T) {
	localErrors := []string{"insufficient balance for transfer", "insufficient funds", "nonce too low"}
	for _, err := range localErrors {
		if !isDefiniteLocalError(err) {
			t.Errorf("Expected %q to be local error", err)
		}
	}

	nonLocalErrors := []string{"execution reverted", "out of gas"}
	for _, err := range nonLocalErrors {
		if isDefiniteLocalError(err) {
			t.Errorf("Expected %q to NOT be local error", err)
		}
	}
}

// =============================================================================
// Utility Endpoint Tests
// =============================================================================

func TestHealthAndInfo(t *testing.T) {
	server := setupTestServer(t, 3, "http://test-orchestrator:8080")

	// Health check
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 for health, got %d", rr.Code)
	}

	// Info endpoint
	req = httptest.NewRequest(http.MethodGet, "/info", nil)
	rr = httptest.NewRecorder()
	server.Router().ServeHTTP(rr, req)
	var result map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&result)
	if result["shard_id"] != float64(3) {
		t.Errorf("Expected shard_id=3, got %v", result["shard_id"])
	}
}

func TestBalanceAndFaucet(t *testing.T) {
	server := setupTestServer(t, 0, "http://localhost:8080")
	addr := "0x0000000000000000000000000000000000000000"

	if bal := getBalance(t, server, addr); bal.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("Expected initial balance 0, got %s", bal)
	}

	fundAccount(t, server, addr, "100")
	fundAccount(t, server, addr, "200")

	if bal := getBalance(t, server, addr); bal.Cmp(big.NewInt(300)) != 0 {
		t.Errorf("Expected balance 300, got %s", bal)
	}
}
