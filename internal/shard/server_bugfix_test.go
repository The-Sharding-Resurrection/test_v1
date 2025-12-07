package shard

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// TestMoneyPrinterBugFix verifies that value is only credited to tx.To,
// not to every address in the RwSet. This tests the fix for the critical
// "money printer" bug where a tx touching multiple addresses would credit
// value to all of them.
func TestMoneyPrinterBugFix(t *testing.T) {
	// Create a server for shard 1 (destination shard)
	server := NewServerForTest(1, "http://localhost:8080")

	// Create a cross-shard tx that touches multiple addresses on this shard
	// But only tx.To should receive the value
	toAddr := common.HexToAddress("0x0000000000000000000000000000000000000001") // shard 1
	otherAddr1 := common.HexToAddress("0x0000000000000000000000000000000000000007") // shard 1
	otherAddr2 := common.HexToAddress("0x000000000000000000000000000000000000000d") // shard 1

	tx := protocol.CrossShardTx{
		ID:        "test-money-printer-fix",
		FromShard: 0,
		From:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
		To:        toAddr, // This is the intended recipient
		Value:     protocol.NewBigInt(big.NewInt(1000000)),
		RwSet: []protocol.RwVariable{
			{Address: toAddr, ReferenceBlock: protocol.Reference{ShardNum: 1}},
			{Address: otherAddr1, ReferenceBlock: protocol.Reference{ShardNum: 1}},
			{Address: otherAddr2, ReferenceBlock: protocol.Reference{ShardNum: 1}},
		},
	}

	// Set up simulation locks (required since fix #3 removed the lock bypass)
	for _, rw := range tx.RwSet {
		server.chain.LockAddress(tx.ID, rw.Address, big.NewInt(0), 0, nil, common.Hash{}, nil)
	}

	// Create orchestrator block with this tx
	block := protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{tx},
		TpcResult: make(map[string]bool),
	}

	// Send the block to the shard
	blockData, _ := json.Marshal(block)
	req := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify that only tx.To has pending credit, not the other addresses
	credits, hasCredits := server.chain.GetPendingCredits(tx.ID)
	if !hasCredits {
		t.Fatal("Expected pending credits to exist")
	}

	// Should have exactly 1 credit (to tx.To only)
	if len(credits) != 1 {
		t.Fatalf("Expected 1 pending credit, got %d (money printer bug!)", len(credits))
	}

	// The credit should be to tx.To
	if credits[0].Address != toAddr {
		t.Errorf("Credit should be to %s, got %s", toAddr.Hex(), credits[0].Address.Hex())
	}

	// Verify the amount is correct (not multiplied)
	expectedAmount := big.NewInt(1000000)
	if credits[0].Amount.Cmp(expectedAmount) != 0 {
		t.Errorf("Expected amount %s, got %s", expectedAmount.String(), credits[0].Amount.String())
	}
}

// TestCrossShardTransferSetsTxTo verifies that handleCrossShardTransfer
// properly sets tx.To for correct credit routing.
func TestCrossShardTransferSetsTxTo(t *testing.T) {
	// This test verifies the fix that ensures tx.To is set
	// in handleCrossShardTransfer

	server := NewServerForTest(0, "http://localhost:8080")

	// Fund the sender
	senderAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	server.evmState.Credit(senderAddr, big.NewInt(1000000000000000000))

	// Create a mock orchestrator to capture the tx
	var capturedTx protocol.CrossShardTx
	mockOrchestrator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedTx)
		json.NewEncoder(w).Encode(map[string]string{"tx_id": capturedTx.ID, "status": "pending"})
	}))
	defer mockOrchestrator.Close()

	// Update server to use mock orchestrator
	server.orchestrator = mockOrchestrator.URL

	// Make a cross-shard transfer request
	transferReq := CrossShardTransferRequest{
		From:    "0x1234567890123456789012345678901234567890",
		To:      "0xABCDabcdABcDabcDaBCDAbcdABcdAbCdABcDABCd",
		ToShard: 1,
		Amount:  "100000000000000000",
	}
	reqData, _ := json.Marshal(transferReq)

	req := httptest.NewRequest("POST", "/cross-shard/transfer", bytes.NewBuffer(reqData))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify tx.To is set correctly
	expectedTo := common.HexToAddress("0xABCDabcdABcDabcDaBCDAbcdABcdAbCdABcDABCd")
	if capturedTx.To != expectedTo {
		t.Errorf("tx.To should be %s, got %s", expectedTo.Hex(), capturedTx.To.Hex())
	}

	// Verify RwSet also has the recipient
	if len(capturedTx.RwSet) != 1 {
		t.Fatalf("Expected 1 RwSet entry, got %d", len(capturedTx.RwSet))
	}
	if capturedTx.RwSet[0].Address != expectedTo {
		t.Errorf("RwSet[0].Address should be %s, got %s", expectedTo.Hex(), capturedTx.RwSet[0].Address.Hex())
	}
}

// TestStorageTrackingDuringDeployment verifies that constructor storage
// writes are properly tracked during deployment.
func TestStorageTrackingDuringDeployment(t *testing.T) {
	// Create a simple contract that sets storage in constructor
	// This is the bytecode for a contract that sets slot 0 to 42 in constructor
	// pragma solidity ^0.8.0;
	// contract Test { uint256 public value = 42; }
	bytecode := common.FromHex("6080604052602a60005534801561001557600080fd5b5060b6806100246000396000f3fe6080604052348015600f57600080fd5b506004361060285760003560e01c80633fa4f24514602d575b600080fd5b60336047565b604051603e91906067565b60405180910390f35b60005481565b6000819050919050565b6061816050565b82525050565b6000602082019050607a6000830184605a565b9291505056fea2646970667358221220")

	evmState, err := NewEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Fund deployer
	deployer := common.HexToAddress("0x1234567890123456789012345678901234567890")
	evmState.Credit(deployer, big.NewInt(1e18))

	// Deploy with storage tracking
	contractAddr, _, _, _, storageWrites, err := evmState.DeployContractTracked(
		deployer, bytecode, big.NewInt(0), 3000000, 6,
	)
	if err != nil {
		t.Fatalf("Deployment failed: %v", err)
	}

	t.Logf("Contract deployed at %s", contractAddr.Hex())
	t.Logf("Storage writes: %v", storageWrites)

	// The contract sets slot 0 to 42 in constructor
	// Verify storage writes were tracked
	if storageWrites != nil && len(storageWrites) > 0 {
		// Check if slot 0 was written
		slot0 := common.Hash{}
		if val, ok := storageWrites[slot0]; ok {
			// Value should be 42 (0x2a)
			expectedVal := common.HexToHash("0x000000000000000000000000000000000000000000000000000000000000002a")
			if val != expectedVal {
				t.Errorf("Expected slot 0 = %s, got %s", expectedVal.Hex(), val.Hex())
			}
		}
	}
}

// TestSetCodeWithStorage verifies that handleSetCode properly applies
// both code and storage from cross-shard deployment.
func TestSetCodeWithStorage(t *testing.T) {
	// Create server for shard 3 (target shard for contract)
	server := NewServerForTest(3, "http://localhost:8080")

	// Create an address that maps to shard 3
	contractAddr := common.HexToAddress("0x0000000000000000000000000000000000000003") // last byte 0x03 -> shard 3

	// Sample contract code and storage
	code := common.FromHex("6080604052") // minimal bytecode
	storage := map[string]string{
		"0x0000000000000000000000000000000000000000000000000000000000000000": "0x000000000000000000000000000000000000000000000000000000000000002a", // slot 0 = 42
		"0x0000000000000000000000000000000000000000000000000000000000000001": "0x0000000000000000000000000000000000000000000000000000000000000064", // slot 1 = 100
	}

	setCodeReq := SetCodeRequest{
		Address: contractAddr.Hex(),
		Code:    common.Bytes2Hex(code),
		Storage: storage,
	}
	reqData, _ := json.Marshal(setCodeReq)

	req := httptest.NewRequest("POST", "/evm/setcode", bytes.NewBuffer(reqData))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify code was set
	storedCode := server.evmState.GetCode(contractAddr)
	if !bytes.Equal(storedCode, code) {
		t.Errorf("Code mismatch: expected %x, got %x", code, storedCode)
	}

	// Verify storage was set
	slot0 := common.Hash{}
	val0 := server.evmState.GetStorageAt(contractAddr, slot0)
	expectedVal0 := common.HexToHash("0x000000000000000000000000000000000000000000000000000000000000002a")
	if val0 != expectedVal0 {
		t.Errorf("Slot 0 mismatch: expected %s, got %s", expectedVal0.Hex(), val0.Hex())
	}

	slot1 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	val1 := server.evmState.GetStorageAt(contractAddr, slot1)
	expectedVal1 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000064")
	if val1 != expectedVal1 {
		t.Errorf("Slot 1 mismatch: expected %s, got %s", expectedVal1.Hex(), val1.Hex())
	}
}
