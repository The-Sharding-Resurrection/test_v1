package protocol

import (
	"bytes"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// =============================================================================
// V2 Verification Tests - JSON Serialization Edge Cases
// =============================================================================

func TestBigInt_JSONUnmarshal_AllBranches(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *big.Int
		wantErr  bool
	}{
		{
			name:     "decimal string",
			input:    `"1234567890"`,
			expected: big.NewInt(1234567890),
		},
		{
			name:     "hex string with 0x prefix",
			input:    `"0x1a2b3c"`,
			expected: big.NewInt(0x1a2b3c),
		},
		{
			name:     "zero as decimal",
			input:    `"0"`,
			expected: big.NewInt(0),
		},
		{
			name:     "zero as hex",
			input:    `"0x0"`,
			expected: big.NewInt(0),
		},
		{
			name:     "negative decimal",
			input:    `"-12345"`,
			expected: big.NewInt(-12345),
		},
		{
			name:     "raw number (int64)",
			input:    `42`,
			expected: big.NewInt(42),
		},
		{
			name:     "raw negative number",
			input:    `-100`,
			expected: big.NewInt(-100),
		},
		{
			name:     "large hex value (Wei)",
			input:    `"0xde0b6b3a7640000"`,
			expected: big.NewInt(1000000000000000000), // 1 ETH
		},
		{
			name:    "invalid hex string",
			input:   `"0xGGGG"`,
			wantErr: true,
		},
		{
			name:    "invalid decimal string",
			input:   `"not_a_number"`,
			wantErr: true,
		},
		{
			name:    "non-string non-number",
			input:   `[1,2,3]`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b BigInt
			err := json.Unmarshal([]byte(tt.input), &b)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for input %s", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if b.Int.Cmp(tt.expected) != 0 {
				t.Errorf("got %s, want %s", b.Int.String(), tt.expected.String())
			}
		})
	}
}

func TestBigInt_JSONMarshal_NilSafe(t *testing.T) {
	// Test nil inner value
	b := BigInt{Int: nil}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `"0"` {
		t.Errorf("expected \"0\", got %s", string(data))
	}

	// Test valid value
	b2 := BigInt{Int: big.NewInt(12345)}
	data, err = json.Marshal(b2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `"12345"` {
		t.Errorf("expected \"12345\", got %s", string(data))
	}
}

func TestBigInt_ToBigInt_NilSafe(t *testing.T) {
	// Nil BigInt pointer
	var b *BigInt = nil
	result := b.ToBigInt()
	if result.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("expected 0 for nil BigInt, got %s", result.String())
	}

	// Non-nil but nil Int
	b = &BigInt{Int: nil}
	result = b.ToBigInt()
	if result.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("expected 0 for nil Int, got %s", result.String())
	}

	// Valid value
	b = &BigInt{Int: big.NewInt(999)}
	result = b.ToBigInt()
	if result.Cmp(big.NewInt(999)) != 0 {
		t.Errorf("expected 999, got %s", result.String())
	}
}

func TestBigInt_DeepCopy(t *testing.T) {
	// Test nil DeepCopy
	var b *BigInt = nil
	cpy := b.DeepCopy()
	if cpy != nil {
		t.Error("expected nil for nil BigInt DeepCopy")
	}

	// Test copy isolation
	original := &BigInt{Int: big.NewInt(100)}
	copied := original.DeepCopy()

	// Modify original
	original.Int.SetInt64(999)

	// Copy should be unchanged
	if copied.Int.Cmp(big.NewInt(100)) != 0 {
		t.Errorf("DeepCopy not isolated: got %s, want 100", copied.Int.String())
	}
}

func TestHexBytes_JSONUnmarshal_AllBranches(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []byte
		wantErr  bool
	}{
		{
			name:     "empty string",
			input:    `""`,
			expected: []byte{},
		},
		{
			name:     "just 0x prefix",
			input:    `"0x"`,
			expected: []byte{},
		},
		{
			name:     "hex with 0x prefix",
			input:    `"0xaabbcc"`,
			expected: []byte{0xaa, 0xbb, 0xcc},
		},
		{
			name:     "hex without prefix",
			input:    `"aabbcc"`,
			expected: []byte{0xaa, 0xbb, 0xcc},
		},
		{
			name:     "uppercase hex",
			input:    `"0xAABBCC"`,
			expected: []byte{0xaa, 0xbb, 0xcc},
		},
		{
			name:     "mixed case hex",
			input:    `"0xAaBbCc"`,
			expected: []byte{0xaa, 0xbb, 0xcc},
		},
		{
			name:    "invalid hex characters",
			input:   `"0xGGHH"`,
			wantErr: true,
		},
		{
			name:    "odd length hex",
			input:   `"0xabc"`,
			wantErr: true,
		},
		{
			name:    "non-string input",
			input:   `123`,
			wantErr: true,
		},
		{
			name:    "array input",
			input:   `[1,2,3]`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h HexBytes
			err := json.Unmarshal([]byte(tt.input), &h)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for input %s", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(h, tt.expected) {
				t.Errorf("got %x, want %x", h, tt.expected)
			}
		})
	}
}

func TestHexBytes_JSONMarshal(t *testing.T) {
	tests := []struct {
		name     string
		input    HexBytes
		expected string
	}{
		{
			name:     "empty bytes",
			input:    HexBytes{},
			expected: `"0x"`,
		},
		{
			name:     "single byte",
			input:    HexBytes{0xff},
			expected: `"0xff"`,
		},
		{
			name:     "multiple bytes",
			input:    HexBytes{0xaa, 0xbb, 0xcc},
			expected: `"0xaabbcc"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(data) != tt.expected {
				t.Errorf("got %s, want %s", string(data), tt.expected)
			}
		})
	}
}

func TestHexBytes_DeepCopy(t *testing.T) {
	// Test nil
	var h HexBytes = nil
	cpy := h.DeepCopy()
	if cpy != nil {
		t.Error("expected nil for nil HexBytes DeepCopy")
	}

	// Test copy isolation
	original := HexBytes{0x01, 0x02, 0x03}
	copied := original.DeepCopy()

	// Modify original
	original[0] = 0xff

	// Copy should be unchanged
	if copied[0] != 0x01 {
		t.Errorf("DeepCopy not isolated: got %x, want 01", copied[0])
	}
}

// =============================================================================
// V2 Verification Tests - Transaction Priority Ordering
// =============================================================================

func TestTxType_Priority_V2Ordering(t *testing.T) {
	// V2 spec: Finalize(1) > Unlock(2) > Lock(3) > Local(4) > SimError(5)
	tests := []struct {
		txType   TxType
		expected int
		desc     string
	}{
		// Priority 1: Finalize types
		{TxTypeFinalize, 1, "Finalize (explicit)"},
		{TxTypeCrossDebit, 1, "CrossDebit (finalize)"},
		{TxTypeCrossCredit, 1, "CrossCredit (finalize)"},
		{TxTypeCrossWriteSet, 1, "CrossWriteSet (finalize)"},
		// Priority 2: Unlock
		{TxTypeUnlock, 2, "Unlock"},
		// Priority 3: Lock
		{TxTypeLock, 3, "Lock"},
		// Priority 4: Local (default)
		{TxTypeLocal, 4, "Local (empty)"},
		{"", 4, "Local (default empty string)"},
		{TxTypeCrossAbort, 4, "CrossAbort (local priority)"},
		{TxTypePrepareDebit, 4, "PrepareDebit (local priority)"},
		{TxTypePrepareCredit, 4, "PrepareCredit (local priority)"},
		{TxTypePrepareWriteSet, 4, "PrepareWriteSet (local priority)"},
		// Priority 5: SimError
		{TxTypeSimError, 5, "SimError"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := tt.txType.Priority()
			if got != tt.expected {
				t.Errorf("%s.Priority() = %d, want %d", tt.txType, got, tt.expected)
			}
		})
	}

	// Verify ordering relationships
	if TxTypeFinalize.Priority() >= TxTypeUnlock.Priority() {
		t.Error("Finalize should have higher priority (lower number) than Unlock")
	}
	if TxTypeUnlock.Priority() >= TxTypeLock.Priority() {
		t.Error("Unlock should have higher priority than Lock")
	}
	if TxTypeLock.Priority() >= TxTypeLocal.Priority() {
		t.Error("Lock should have higher priority than Local")
	}
	if TxTypeLocal.Priority() >= TxTypeSimError.Priority() {
		t.Error("Local should have higher priority than SimError")
	}
}

// =============================================================================
// V2 Verification Tests - DeepCopy Isolation
// =============================================================================

func TestTransaction_DeepCopy_Isolation(t *testing.T) {
	original := Transaction{
		ID:             "tx-1",
		From:           common.HexToAddress("0x1234"),
		To:             common.HexToAddress("0x5678"),
		Value:          NewBigInt(big.NewInt(1000)),
		Data:           HexBytes{0x01, 0x02},
		IsCrossShard:   true,
		TxType:         TxTypeLock,
		CrossShardTxID: "cross-1",
		RwSet: []RwVariable{
			{
				Address: common.HexToAddress("0xaaaa"),
				ReadSet: []ReadSetItem{
					{Slot: Slot(common.HexToHash("0x01")), Value: []byte{0x10}},
				},
				WriteSet: []WriteSetItem{
					{Slot: Slot(common.HexToHash("0x01")), OldValue: []byte{0x10}, NewValue: []byte{0x20}},
				},
			},
		},
	}

	copied := original.DeepCopy()

	// Modify original
	original.ID = "modified"
	original.Value.Int.SetInt64(9999)
	original.Data[0] = 0xff
	original.RwSet[0].Address = common.HexToAddress("0xbbbb")
	original.RwSet[0].ReadSet[0].Value[0] = 0xff
	original.RwSet[0].WriteSet[0].NewValue[0] = 0xff

	// Verify copy is unchanged
	if copied.ID != "tx-1" {
		t.Errorf("ID modified: got %s", copied.ID)
	}
	if copied.Value.Int.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Value modified: got %s", copied.Value.Int.String())
	}
	if copied.Data[0] != 0x01 {
		t.Errorf("Data modified: got %x", copied.Data[0])
	}
	if copied.RwSet[0].Address != common.HexToAddress("0xaaaa") {
		t.Errorf("RwSet.Address modified")
	}
	if copied.RwSet[0].ReadSet[0].Value[0] != 0x10 {
		t.Errorf("ReadSet.Value modified: got %x", copied.RwSet[0].ReadSet[0].Value[0])
	}
	if copied.RwSet[0].WriteSet[0].NewValue[0] != 0x20 {
		t.Errorf("WriteSet.NewValue modified: got %x", copied.RwSet[0].WriteSet[0].NewValue[0])
	}
}

func TestCrossShardTx_DeepCopy_Isolation(t *testing.T) {
	original := &CrossShardTx{
		ID:        "cstx-1",
		FromShard: 0,
		From:      common.HexToAddress("0x1234"),
		To:        common.HexToAddress("0x5678"),
		Value:     NewBigInt(big.NewInt(1000000)),
		Data:      HexBytes{0xaa, 0xbb},
		RwSet: []RwVariable{
			{
				Address:        common.HexToAddress("0xcccc"),
				ReferenceBlock: Reference{ShardNum: 2},
				ReadSet: []ReadSetItem{
					{Slot: Slot(common.HexToHash("0x05")), Value: []byte{0x55}},
				},
			},
		},
		Status:    TxPending,
		SimStatus: SimRunning,
		SimError:  "",
	}

	copied := original.DeepCopy()

	// Modify original
	original.ID = "modified"
	original.Value.Int.SetInt64(9999)
	original.Data[0] = 0xff
	original.RwSet[0].ReadSet[0].Value[0] = 0xff
	original.Status = TxCommitted
	original.SimStatus = SimFailed
	original.SimError = "error"

	// Verify copy unchanged
	if copied.ID != "cstx-1" {
		t.Errorf("ID modified")
	}
	if copied.Value.Int.Cmp(big.NewInt(1000000)) != 0 {
		t.Errorf("Value modified")
	}
	if copied.Data[0] != 0xaa {
		t.Errorf("Data modified")
	}
	if copied.RwSet[0].ReadSet[0].Value[0] != 0x55 {
		t.Errorf("RwSet modified")
	}
	if copied.Status != TxPending {
		t.Errorf("Status modified")
	}
	if copied.SimStatus != SimRunning {
		t.Errorf("SimStatus modified")
	}
}

func TestCrossShardTx_DeepCopy_Nil(t *testing.T) {
	var tx *CrossShardTx = nil
	copied := tx.DeepCopy()
	if copied != nil {
		t.Error("expected nil for nil CrossShardTx DeepCopy")
	}
}

func TestRwVariable_DeepCopy_Isolation(t *testing.T) {
	original := RwVariable{
		Address:        common.HexToAddress("0x1234"),
		ReferenceBlock: Reference{ShardNum: 3, BlockHeight: 100},
		ReadSet: []ReadSetItem{
			{Slot: Slot(common.HexToHash("0x01")), Value: []byte{0x11}, Proof: [][]byte{{0x01}, {0x02}}},
		},
		WriteSet: []WriteSetItem{
			{Slot: Slot(common.HexToHash("0x02")), OldValue: []byte{0x22}, NewValue: []byte{0x33}},
		},
	}

	copied := original.DeepCopy()

	// Modify original
	original.Address = common.HexToAddress("0xffff")
	original.ReadSet[0].Value[0] = 0xff
	original.ReadSet[0].Proof[0][0] = 0xff
	original.WriteSet[0].OldValue[0] = 0xff

	// Verify copy unchanged
	if copied.Address != common.HexToAddress("0x1234") {
		t.Error("Address modified")
	}
	if copied.ReadSet[0].Value[0] != 0x11 {
		t.Error("ReadSet.Value modified")
	}
	if copied.ReadSet[0].Proof[0][0] != 0x01 {
		t.Error("ReadSet.Proof modified")
	}
	if copied.WriteSet[0].OldValue[0] != 0x22 {
		t.Error("WriteSet.OldValue modified")
	}
}

func TestReadSetItem_DeepCopy_NilProof(t *testing.T) {
	original := ReadSetItem{
		Slot:  Slot(common.HexToHash("0x01")),
		Value: []byte{0x10},
		Proof: nil,
	}

	copied := original.DeepCopy()

	if copied.Proof != nil {
		t.Error("expected nil Proof in copy")
	}
	if copied.Value[0] != 0x10 {
		t.Error("Value not copied correctly")
	}
}

// =============================================================================
// V2 Verification Tests - RwSet Operations
// =============================================================================

func TestCrossShardTx_TargetShards_NilRwSet(t *testing.T) {
	tx := CrossShardTx{RwSet: nil}
	shards := tx.TargetShards()
	if shards != nil {
		t.Errorf("expected nil for nil RwSet, got %v", shards)
	}
}

func TestCrossShardTx_InvolvedShards_EmptyRwSet(t *testing.T) {
	tx := CrossShardTx{
		FromShard: 5,
		RwSet:     []RwVariable{},
	}
	shards := tx.InvolvedShards()
	if len(shards) != 1 || shards[0] != 5 {
		t.Errorf("expected [5] for empty RwSet with FromShard=5, got %v", shards)
	}
}

// =============================================================================
// V2 Verification Tests - Reference Block
// =============================================================================

func TestReference_Fields(t *testing.T) {
	ref := Reference{
		ShardNum:    4,
		BlockHash:   common.HexToHash("0x1234567890abcdef"),
		BlockHeight: 12345,
	}

	if ref.ShardNum != 4 {
		t.Error("ShardNum mismatch")
	}
	if ref.BlockHeight != 12345 {
		t.Error("BlockHeight mismatch")
	}
	if ref.BlockHash != common.HexToHash("0x1234567890abcdef") {
		t.Error("BlockHash mismatch")
	}
}

// =============================================================================
// V2 Verification Tests - NoStateError
// =============================================================================

func TestNoStateError_AllFields(t *testing.T) {
	err := &NoStateError{
		Address: common.HexToAddress("0x1111"),
		Caller:  common.HexToAddress("0x2222"),
		Data:    []byte{0x01, 0x02, 0x03, 0x04},
		Value:   big.NewInt(999999),
		ShardID: 7,
	}

	// Verify all fields accessible
	if err.Address != common.HexToAddress("0x1111") {
		t.Error("Address mismatch")
	}
	if err.Caller != common.HexToAddress("0x2222") {
		t.Error("Caller mismatch")
	}
	if len(err.Data) != 4 {
		t.Error("Data length mismatch")
	}
	if err.Value.Cmp(big.NewInt(999999)) != 0 {
		t.Error("Value mismatch")
	}
	if err.ShardID != 7 {
		t.Error("ShardID mismatch")
	}

	// Error message should contain key info
	msg := err.Error()
	if msg == "" {
		t.Error("Error message should not be empty")
	}
}

// =============================================================================
// V2 Verification Tests - Status Types
// =============================================================================

func TestTxStatus_Values(t *testing.T) {
	if TxPending != "pending" {
		t.Error("TxPending mismatch")
	}
	if TxPrepared != "prepared" {
		t.Error("TxPrepared mismatch")
	}
	if TxCommitted != "committed" {
		t.Error("TxCommitted mismatch")
	}
	if TxAborted != "aborted" {
		t.Error("TxAborted mismatch")
	}
}

func TestSimulationStatus_Values(t *testing.T) {
	if SimPending != "pending" {
		t.Error("SimPending mismatch")
	}
	if SimRunning != "running" {
		t.Error("SimRunning mismatch")
	}
	if SimSuccess != "success" {
		t.Error("SimSuccess mismatch")
	}
	if SimFailed != "failed" {
		t.Error("SimFailed mismatch")
	}
}

// =============================================================================
// V2 Verification Tests - Block Hash Consistency
// =============================================================================

func TestOrchestratorShardBlock_Hash_Deterministic(t *testing.T) {
	block := &OrchestratorShardBlock{
		Height:    100,
		Timestamp: 1700000000,
		TpcResult: map[string]bool{"tx1": true, "tx2": false},
		CtToOrder: []CrossShardTx{
			{ID: "new-tx-1"},
		},
	}

	// Hash should be deterministic
	hash1 := block.Hash()
	hash2 := block.Hash()

	if hash1 != hash2 {
		t.Error("Hash not deterministic")
	}

	// Different block should have different hash
	block2 := &OrchestratorShardBlock{
		Height:    100,
		Timestamp: 1700000001, // Different timestamp
		TpcResult: map[string]bool{"tx1": true, "tx2": false},
		CtToOrder: []CrossShardTx{
			{ID: "new-tx-1"},
		},
	}

	if block.Hash() == block2.Hash() {
		t.Error("Different blocks should have different hashes")
	}
}

func TestStateShardBlock_Hash_Deterministic(t *testing.T) {
	block := &StateShardBlock{
		ShardID:    3,
		Height:     50,
		Timestamp:  1700000000,
		StateRoot:  common.HexToHash("0xabcd"),
		TxOrdering: []Transaction{},
		TpcPrepare: map[string]bool{"tx1": true},
	}

	hash1 := block.Hash()
	hash2 := block.Hash()

	if hash1 != hash2 {
		t.Error("Hash not deterministic")
	}
}

// =============================================================================
// V2 Verification Tests - NewBigInt
// =============================================================================

func TestNewBigInt_NilInput(t *testing.T) {
	result := NewBigInt(nil)
	if result != nil {
		t.Error("expected nil for nil input")
	}
}

func TestNewBigInt_ValidInput(t *testing.T) {
	input := big.NewInt(12345)
	result := NewBigInt(input)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Int.Cmp(input) != 0 {
		t.Errorf("expected %s, got %s", input.String(), result.Int.String())
	}
}
