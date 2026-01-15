package shard

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"test_v1/internal/orchestrator"
)

// TestGetStorageWithProof_MemoryState tests proof generation on in-memory state
func TestGetStorageWithProof_MemoryState(t *testing.T) {
	// Create in-memory EVM state
	evm, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("failed to create EVM state: %v", err)
	}

	// Create a test account with some storage
	testAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	testSlot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	testValue := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000042")

	// Set storage value
	evm.SetStorageAt(testAddr, testSlot, testValue)

	// Commit to create a proper state root
	stateRoot, err := evm.Commit(1)
	if err != nil {
		t.Fatalf("failed to commit state: %v", err)
	}

	if stateRoot == (common.Hash{}) {
		t.Errorf("state root should not be empty after commit")
	}

	// Get storage with proof
	proofResp, err := evm.GetStorageWithProof(testAddr, testSlot)
	if err != nil {
		t.Fatalf("failed to get storage with proof: %v", err)
	}

	// Verify response fields
	if proofResp.Address != testAddr {
		t.Errorf("address mismatch: got %s, want %s", proofResp.Address.Hex(), testAddr.Hex())
	}

	if proofResp.Slot != testSlot {
		t.Errorf("slot mismatch: got %s, want %s", proofResp.Slot.Hex(), testSlot.Hex())
	}

	if proofResp.Value != testValue {
		t.Errorf("value mismatch: got %s, want %s", proofResp.Value.Hex(), testValue.Hex())
	}

	if proofResp.StateRoot == (common.Hash{}) {
		t.Errorf("state root should not be empty")
	}

	if len(proofResp.AccountProof) == 0 {
		t.Errorf("account proof should not be empty")
	}

	if len(proofResp.StorageProof) == 0 {
		t.Errorf("storage proof should not be empty")
	}

	t.Logf("Generated proof with %d account proof nodes and %d storage proof nodes",
		len(proofResp.AccountProof), len(proofResp.StorageProof))
}

// TestGetStorageWithProof_EmptySlot tests proof generation for non-existent storage
func TestGetStorageWithProof_EmptySlot(t *testing.T) {
	// Create in-memory EVM state
	evm, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("failed to create EVM state: %v", err)
	}

	// Test reading from a non-existent account
	testAddr := common.HexToAddress("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	testSlot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")

	// Get storage with proof (should return zero value and proof of non-existence)
	proofResp, err := evm.GetStorageWithProof(testAddr, testSlot)
	if err != nil {
		t.Fatalf("failed to get storage with proof: %v", err)
	}

	// Value should be zero for non-existent storage
	if proofResp.Value != (common.Hash{}) {
		t.Errorf("expected zero value for non-existent storage, got %s", proofResp.Value.Hex())
	}

	// Account proof may be empty for non-existent accounts (this is expected behavior)
	// The empty proof proves the account doesn't exist in the trie

	t.Logf("Non-existent storage proof: %d account nodes, %d storage nodes",
		len(proofResp.AccountProof), len(proofResp.StorageProof))
}

// TestGetStorageWithProof_AfterContractDeploy tests proof for contract storage
func TestGetStorageWithProof_AfterContractDeploy(t *testing.T) {
	// Create in-memory EVM state
	evm, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("failed to create EVM state: %v", err)
	}

	// Deploy a simple contract (just initialization code that stores a value)
	deployer := common.HexToAddress("0xdeployer1234567890abcdef1234567890abcdef")

	// Simple bytecode that stores 0x42 in slot 0 during construction
	// PUSH1 0x42, PUSH1 0x00, SSTORE, STOP
	const storeValueBytecode = "60426000550000"
	bytecode := common.Hex2Bytes(storeValueBytecode)

	// Deploy contract
	contractAddr, _, _, _, err := evm.DeployContract(deployer, bytecode, big.NewInt(0), 1_000_000)
	if err != nil {
		t.Fatalf("failed to deploy contract: %v", err)
	}

	// Commit state
	_, err = evm.Commit(1)
	if err != nil {
		t.Fatalf("failed to commit state: %v", err)
	}

	// Get storage proof for slot 0
	slot := common.Hash{}
	proofResp, err := evm.GetStorageWithProof(contractAddr, slot)
	if err != nil {
		t.Fatalf("failed to get storage with proof: %v", err)
	}

	expectedValue := common.HexToHash("0x42")
	if proofResp.Value != expectedValue {
		t.Errorf("value mismatch: got %s, want %s", proofResp.Value.Hex(), expectedValue.Hex())
	}

	if len(proofResp.AccountProof) == 0 {
		t.Errorf("account proof should not be empty for deployed contract")
	}

	if len(proofResp.StorageProof) == 0 {
		t.Errorf("storage proof should not be empty for contract with storage")
	}

	t.Logf("Contract storage proof: %d account nodes, %d storage nodes",
		len(proofResp.AccountProof), len(proofResp.StorageProof))
}

// TestGetStorageWithProof_EndToEnd tests full proof generation and verification flow
func TestGetStorageWithProof_EndToEnd(t *testing.T) {
	// Create in-memory EVM state
	evm, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("failed to create EVM state: %v", err)
	}

	// Create a test account with some storage
	testAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	testSlot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	testValue := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000042")

	// Set storage value
	evm.SetStorageAt(testAddr, testSlot, testValue)

	// Commit to create a proper state root
	stateRoot, err := evm.Commit(1)
	if err != nil {
		t.Fatalf("failed to commit state: %v", err)
	}

	// Generate proof
	proofResp, err := evm.GetStorageWithProof(testAddr, testSlot)
	if err != nil {
		t.Fatalf("failed to get storage with proof: %v", err)
	}

	// Convert [][]byte proofs to []string format expected by VerifyStorageProof
	accountProofStrs := make([]string, len(proofResp.AccountProof))
	for i, p := range proofResp.AccountProof {
		accountProofStrs[i] = common.Bytes2Hex(p)
	}

	storageProofStrs := make([]string, len(proofResp.StorageProof))
	for i, p := range proofResp.StorageProof {
		storageProofStrs[i] = common.Bytes2Hex(p)
	}

	// Verify the proof using orchestrator's verification function
	err = orchestrator.VerifyStorageProof(
		stateRoot,
		testAddr,
		testSlot,
		testValue,
		accountProofStrs,
		storageProofStrs,
	)
	if err != nil {
		t.Fatalf("end-to-end proof verification failed: %v", err)
	}

	t.Logf("✓ Successfully generated and verified proof for storage slot %s at %s",
		testSlot.Hex(), testAddr.Hex())
}

// TestGetStorageWithProof_EndToEnd_EmptySlot tests proof verification for non-existent storage
func TestGetStorageWithProof_EndToEnd_EmptySlot(t *testing.T) {
	// Create in-memory EVM state
	evm, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("failed to create EVM state: %v", err)
	}

	// Test reading from a non-existent account
	testAddr := common.HexToAddress("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	testSlot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")

	// Commit to create a proper state root
	stateRoot, err := evm.Commit(1)
	if err != nil {
		t.Fatalf("failed to commit state: %v", err)
	}

	// Generate proof for non-existent storage
	proofResp, err := evm.GetStorageWithProof(testAddr, testSlot)
	if err != nil {
		t.Fatalf("failed to get storage with proof: %v", err)
	}

	// Convert [][]byte proofs to []string format
	accountProofStrs := make([]string, len(proofResp.AccountProof))
	for i, p := range proofResp.AccountProof {
		accountProofStrs[i] = common.Bytes2Hex(p)
	}

	storageProofStrs := make([]string, len(proofResp.StorageProof))
	for i, p := range proofResp.StorageProof {
		storageProofStrs[i] = common.Bytes2Hex(p)
	}

	// Verify the proof (should verify zero value)
	expectedValue := common.Hash{} // Zero value for non-existent storage
	err = orchestrator.VerifyStorageProof(
		stateRoot,
		testAddr,
		testSlot,
		expectedValue,
		accountProofStrs,
		storageProofStrs,
	)
	if err != nil {
		t.Fatalf("end-to-end proof verification failed for empty slot: %v", err)
	}

	t.Logf("✓ Successfully verified proof of non-existence for slot %s at %s",
		testSlot.Hex(), testAddr.Hex())
}
