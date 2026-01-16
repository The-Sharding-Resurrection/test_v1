package orchestrator

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// TestParseProof tests the parseProof helper function
func TestParseProof(t *testing.T) {
	tests := []struct {
		name      string
		hexProof  []string
		wantLen   int
		wantError bool
	}{
		{
			name:      "empty proof",
			hexProof:  []string{},
			wantLen:   0,
			wantError: false,
		},
		{
			name: "valid hex with 0x prefix",
			hexProof: []string{
				"0x0102030405",
				"0xaabbccdd",
			},
			wantLen:   2,
			wantError: false,
		},
		{
			name: "valid hex without 0x prefix",
			hexProof: []string{
				"0102030405",
				"aabbccdd",
			},
			wantLen:   2,
			wantError: false,
		},
		{
			name: "invalid hex",
			hexProof: []string{
				"0xZZZZ",
			},
			wantLen:   0,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proof, err := parseProof(tt.hexProof)
			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if len(proof) != tt.wantLen {
				t.Errorf("proof length = %d, want %d", len(proof), tt.wantLen)
			}
		})
	}
}

// TestVerifyStorageProof_EmptyStorage tests proof verification for non-existent storage
func TestVerifyStorageProof_EmptyStorage(t *testing.T) {
	// Empty state root (all zeros means empty storage)
	stateRoot := common.Hash{}
	addr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	slot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	value := common.Hash{} // Zero value

	// Empty proofs for empty storage
	accountProof := []string{}
	storageProof := []string{}

	// This should succeed - empty state root proves value is zero
	err := VerifyStorageProof(stateRoot, addr, slot, value, accountProof, storageProof)
	if err != nil {
		t.Errorf("expected nil for empty state root with zero value, got: %v", err)
	}
}

// TestVerifyStorageProof_NonZeroValueWithEmptyRoot tests validation logic
func TestVerifyStorageProof_NonZeroValueWithEmptyRoot(t *testing.T) {
	// This test verifies that the function correctly rejects
	// a non-zero value claim when the storage root is empty

	// Note: We can't create a real valid proof without a full trie,
	// but we can test the validation logic in the verifier
	stateRoot := common.HexToHash("0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421") // Empty trie root
	addr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	slot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	nonZeroValue := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000042")

	accountProof := []string{}
	storageProof := []string{}

	// Should fail because value is non-zero but storage root is empty
	err := VerifyStorageProof(stateRoot, addr, slot, nonZeroValue, accountProof, storageProof)
	if err == nil {
		t.Errorf("expected error for non-zero value with empty storage root")
	}
}
