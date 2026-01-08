package orchestrator

import (
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// TestSubRefund_NoUnderflowPanic verifies fix #2:
// SubRefund should clamp to zero instead of panicking when gas > refund
func TestSubRefund_NoUnderflowPanic(t *testing.T) {
	fetcher, _ := NewStateFetcher(2, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	// Add some refund
	stateDB.AddRefund(100)
	if stateDB.GetRefund() != 100 {
		t.Fatalf("Expected refund 100, got %d", stateDB.GetRefund())
	}

	// This should NOT panic - it should clamp to zero
	// Before fix #2, this would panic with "refund counter below zero"
	stateDB.SubRefund(200) // Try to subtract more than available

	// Refund should be clamped to 0
	if stateDB.GetRefund() != 0 {
		t.Errorf("Expected refund 0 after underflow clamp, got %d", stateDB.GetRefund())
	}

	// Verify normal subtraction still works
	stateDB.AddRefund(50)
	stateDB.SubRefund(30)
	if stateDB.GetRefund() != 20 {
		t.Errorf("Expected refund 20, got %d", stateDB.GetRefund())
	}
}

// TestSubRefund_ExactMatch verifies SubRefund works when gas == refund
func TestSubRefund_ExactMatch(t *testing.T) {
	fetcher, _ := NewStateFetcher(2, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	stateDB.AddRefund(100)
	stateDB.SubRefund(100)

	if stateDB.GetRefund() != 0 {
		t.Errorf("Expected refund 0, got %d", stateDB.GetRefund())
	}
}

// TestSimulationStateDB_ConcurrentRefund verifies thread safety of refund operations
func TestSimulationStateDB_ConcurrentRefund(t *testing.T) {
	fetcher, _ := NewStateFetcher(2, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	var wg sync.WaitGroup
	iterations := 100

	// Concurrent AddRefund
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stateDB.AddRefund(1)
		}()
	}
	wg.Wait()

	// Should have exactly 100 refund
	if stateDB.GetRefund() != uint64(iterations) {
		t.Errorf("Expected refund %d, got %d", iterations, stateDB.GetRefund())
	}

	// Concurrent SubRefund - some may try to underflow
	for i := 0; i < iterations+50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stateDB.SubRefund(1)
		}()
	}
	wg.Wait()

	// Should be clamped to 0 (not panic)
	if stateDB.GetRefund() != 0 {
		t.Errorf("Expected refund 0 after concurrent underflow, got %d", stateDB.GetRefund())
	}
}

// TestSimulationStateDB_ConcurrentAccess verifies thread safety of SimulationStateDB
// This tests that concurrent access to the same addresses doesn't cause panics
func TestSimulationStateDB_ConcurrentAccess(t *testing.T) {
	fetcher, _ := NewStateFetcher(2, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")

	// First set initial balance
	stateDB.CreateAccount(addr)
	stateDB.AddBalance(addr, uint256FromBig(big.NewInt(10000)), 0)

	var wg sync.WaitGroup

	// Concurrent reads - this is the main concurrency pattern in simulation
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = stateDB.GetBalance(addr)
		}()
		go func() {
			defer wg.Done()
			_ = stateDB.GetNonce(addr)
		}()
	}
	wg.Wait()

	// Test passes if no race condition panic occurs
	// The SimulationStateDB has mutex protection for balance operations

	// Verify balance is still accessible
	bal := stateDB.GetBalance(addr)
	if bal == nil {
		t.Error("Expected non-nil balance")
	}
}

// TestSubRefund_ZeroSubtraction verifies SubRefund with zero gas works
func TestSubRefund_ZeroSubtraction(t *testing.T) {
	fetcher, _ := NewStateFetcher(2, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	stateDB.AddRefund(100)
	stateDB.SubRefund(0) // Zero subtraction should be no-op

	if stateDB.GetRefund() != 100 {
		t.Errorf("Expected refund 100 after zero subtraction, got %d", stateDB.GetRefund())
	}
}

// TestSubRefund_MultipleUnderflows verifies multiple underflows don't cause issues
func TestSubRefund_MultipleUnderflows(t *testing.T) {
	fetcher, _ := NewStateFetcher(2, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	// Multiple underflow attempts should all clamp to 0
	stateDB.SubRefund(100)
	stateDB.SubRefund(100)
	stateDB.SubRefund(100)

	if stateDB.GetRefund() != 0 {
		t.Errorf("Expected refund 0 after multiple underflows, got %d", stateDB.GetRefund())
	}
}

// TestSimulationStateDB_AccessedAddresses verifies address tracking for RwSet
func TestSimulationStateDB_AccessedAddresses(t *testing.T) {
	fetcher, _ := NewStateFetcher(2, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	addr1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	addr2 := common.HexToAddress("0x2222222222222222222222222222222222222222")

	// Modify balances - this adds addresses to RwSet
	stateDB.CreateAccount(addr1)
	stateDB.AddBalance(addr1, uint256FromBig(big.NewInt(1000)), 0)
	stateDB.CreateAccount(addr2)
	stateDB.AddBalance(addr2, uint256FromBig(big.NewInt(500)), 0)

	rwSet := stateDB.BuildRwSet()

	// Both addresses should be in RwSet due to balance modifications
	found := make(map[common.Address]bool)
	for _, rw := range rwSet {
		found[rw.Address] = true
	}

	if !found[addr1] {
		t.Error("addr1 should be in RwSet")
	}
	if !found[addr2] {
		t.Error("addr2 should be in RwSet")
	}
}

// TestSimulationStateDB_StorageTracking verifies storage read/write tracking
func TestSimulationStateDB_StorageTracking(t *testing.T) {
	fetcher, _ := NewStateFetcher(2, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	slot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	value := common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000000ff")

	stateDB.CreateAccount(addr)
	stateDB.SetState(addr, slot, value)

	// Verify storage was written
	storedValue := stateDB.GetState(addr, slot)
	if storedValue != value {
		t.Errorf("Expected storage value %s, got %s", value.Hex(), storedValue.Hex())
	}

	// Verify it appears in RwSet
	rwSet := stateDB.BuildRwSet()
	foundWrite := false
	for _, rw := range rwSet {
		if rw.Address == addr {
			for _, w := range rw.WriteSet {
				if common.Hash(w.Slot) == slot {
					foundWrite = true
					break
				}
			}
		}
	}

	if !foundWrite {
		t.Error("Storage write should appear in RwSet WriteSet")
	}
}

// ===== V2.2 Tests =====

// TestSimulationStateDB_PendingExternalCalls verifies V2.2 external call tracking
func TestSimulationStateDB_PendingExternalCalls(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	// Initially no pending calls
	if stateDB.HasPendingExternalCalls() {
		t.Error("Expected no pending external calls initially")
	}

	calls := stateDB.GetPendingExternalCalls()
	if len(calls) != 0 {
		t.Errorf("Expected 0 pending calls, got %d", len(calls))
	}

	// Record an external call
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	caller := common.HexToAddress("0xABCDabcdABcDabcDaBCDAbcdABcdAbCdABcDABCd")
	data := []byte{0x01, 0x02, 0x03, 0x04}
	value := uint256FromBig(big.NewInt(1000))

	stateDB.RecordPendingExternalCall(addr, caller, data, value, 3)

	// Should have pending calls now
	if !stateDB.HasPendingExternalCalls() {
		t.Error("Expected pending external calls after RecordPendingExternalCall")
	}

	calls = stateDB.GetPendingExternalCalls()
	if len(calls) != 1 {
		t.Errorf("Expected 1 pending call, got %d", len(calls))
	}

	// Verify call details
	call := calls[0]
	if call.Address != addr {
		t.Errorf("Address mismatch: got %s, want %s", call.Address.Hex(), addr.Hex())
	}
	if call.Caller != caller {
		t.Errorf("Caller mismatch: got %s, want %s", call.Caller.Hex(), caller.Hex())
	}
	if call.ShardID != 3 {
		t.Errorf("ShardID mismatch: got %d, want 3", call.ShardID)
	}

	// Clear pending calls
	stateDB.ClearPendingExternalCalls()
	if stateDB.HasPendingExternalCalls() {
		t.Error("Expected no pending external calls after clear")
	}
}

// TestSimulationStateDB_DuplicateExternalCall verifies duplicate calls are ignored
func TestSimulationStateDB_DuplicateExternalCall(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	addr := common.HexToAddress("0x1234")
	caller := common.HexToAddress("0x5678")
	data := []byte{0x01}
	value := uint256FromBig(big.NewInt(100))

	// Record same address twice
	stateDB.RecordPendingExternalCall(addr, caller, data, value, 1)
	stateDB.RecordPendingExternalCall(addr, caller, data, value, 1)

	calls := stateDB.GetPendingExternalCalls()
	if len(calls) != 1 {
		t.Errorf("Expected 1 pending call (deduplicated), got %d", len(calls))
	}
}

// TestSimulationStateDB_MultipleExternalCalls verifies multiple different calls are tracked
func TestSimulationStateDB_MultipleExternalCalls(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	addr1 := common.HexToAddress("0x1111")
	addr2 := common.HexToAddress("0x2222")
	addr3 := common.HexToAddress("0x3333")
	caller := common.HexToAddress("0x5678")
	data := []byte{0x01}
	value := uint256FromBig(big.NewInt(100))

	stateDB.RecordPendingExternalCall(addr1, caller, data, value, 1)
	stateDB.RecordPendingExternalCall(addr2, caller, data, value, 2)
	stateDB.RecordPendingExternalCall(addr3, caller, data, value, 3)

	calls := stateDB.GetPendingExternalCalls()
	if len(calls) != 3 {
		t.Errorf("Expected 3 pending calls, got %d", len(calls))
	}
}

// TestSimulationStateDBWithRwSet_PreloadedState verifies V2.2 preloaded RwSet
func TestSimulationStateDBWithRwSet_PreloadedState(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")

	// Create preloaded RwSet
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	slot := common.HexToHash("0x01")
	value := common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000000ff")

	preloadedRwSet := []protocol.RwVariable{
		{
			Address:        addr,
			ReferenceBlock: protocol.Reference{ShardNum: 0, BlockHeight: 100},
			ReadSet: []protocol.ReadSetItem{
				{Slot: protocol.Slot(slot), Value: value.Bytes()},
			},
		},
	}

	// Create stateDB with preloaded RwSet
	stateDB := NewSimulationStateDBWithRwSet("test-tx", fetcher, preloadedRwSet)

	// Verify preloaded RwSet is accessible
	preloaded := stateDB.GetPreloadedRwSet()
	if len(preloaded) != 1 {
		t.Errorf("Expected 1 preloaded RwVariable, got %d", len(preloaded))
	}

	// Verify address is preloaded
	if !stateDB.IsAddressPreloaded(addr) {
		t.Error("Expected address to be marked as preloaded")
	}

	unknownAddr := common.HexToAddress("0x9999")
	if stateDB.IsAddressPreloaded(unknownAddr) {
		t.Error("Expected unknown address to NOT be marked as preloaded")
	}
}

// TestCrossShardTracer_AddressToShard verifies shard ID calculation
func TestCrossShardTracer_AddressToShard(t *testing.T) {
	tests := []struct {
		name      string
		addr      common.Address
		numShards int
		expected  int
	}{
		{
			name:      "address ending in 0x00",
			addr:      common.HexToAddress("0x0000000000000000000000000000000000000000"),
			numShards: 8,
			expected:  0,
		},
		{
			name:      "address ending in 0x07",
			addr:      common.HexToAddress("0x0000000000000000000000000000000000000007"),
			numShards: 8,
			expected:  7,
		},
		{
			name:      "address ending in 0x10 (16)",
			addr:      common.HexToAddress("0x0000000000000000000000000000000000000010"),
			numShards: 8,
			expected:  0, // 16 % 8 = 0
		},
		{
			name:      "address ending in 0xff (255)",
			addr:      common.HexToAddress("0x00000000000000000000000000000000000000ff"),
			numShards: 8,
			expected:  7, // 255 % 8 = 7
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use the same formula as CrossShardTracer
			got := int(tt.addr[len(tt.addr)-1]) % tt.numShards
			if got != tt.expected {
				t.Errorf("AddressToShard(%s) = %d, want %d", tt.addr.Hex(), got, tt.expected)
			}
		})
	}
}

// TestCrossShardTracer_NewCrossShardTracer verifies tracer creation
func TestCrossShardTracer_NewCrossShardTracer(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	tracer := NewCrossShardTracer(stateDB, 8)

	if tracer == nil {
		t.Fatal("NewCrossShardTracer returned nil")
	}
}

// TestSimulationStateDB_FetchErrors verifies fetch error tracking
func TestSimulationStateDB_FetchErrors(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	// Initially no fetch errors
	if stateDB.HasFetchErrors() {
		t.Error("Expected no fetch errors initially")
	}

	errors := stateDB.GetFetchErrors()
	if len(errors) != 0 {
		t.Errorf("Expected 0 errors, got %d", len(errors))
	}
}

// TestSimulationStateDB_Snapshot_RestoresState verifies snapshot/revert
func TestSimulationStateDB_Snapshot_RestoresState(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	slot := common.HexToHash("0x01")

	// Create account and set initial state
	stateDB.CreateAccount(addr)
	stateDB.SetState(addr, slot, common.HexToHash("0x0a"))

	// Take snapshot
	snapID := stateDB.Snapshot()

	// Modify state
	stateDB.SetState(addr, slot, common.HexToHash("0x0b"))

	// Verify modified state
	val := stateDB.GetState(addr, slot)
	if val != common.HexToHash("0x0b") {
		t.Errorf("Expected 0x0b after modification, got %s", val.Hex())
	}

	// Revert to snapshot
	stateDB.RevertToSnapshot(snapID)

	// Verify state is reverted
	val = stateDB.GetState(addr, slot)
	if val != common.HexToHash("0x0a") {
		t.Errorf("Expected 0x0a after revert, got %s", val.Hex())
	}
}

// TestSimulationStateDB_RevertToInvalidSnapshot verifies handling of invalid snapshot IDs
func TestSimulationStateDB_RevertToInvalidSnapshot(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	addr := common.HexToAddress("0x1234")
	stateDB.CreateAccount(addr)
	stateDB.AddBalance(addr, uint256FromBig(big.NewInt(1000)), 0)

	// Revert to invalid snapshot ID - should be no-op
	stateDB.RevertToSnapshot(999)

	// Balance should still be 1000
	bal := stateDB.GetBalance(addr)
	if bal.ToBig().Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Expected balance 1000 after invalid revert, got %s", bal.String())
	}
}

// ===== V2.2 CrossShardTracer OnEnter Tests =====

// TestCrossShardTracer_OnEnter_SkipsDepthZero verifies depth 0 calls are ignored
func TestCrossShardTracer_OnEnter_SkipsDepthZero(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	// Set up an account with code to simulate a contract
	addr := common.HexToAddress("0x1234")
	stateDB.CreateAccount(addr)
	stateDB.SetCode(addr, []byte{0x60, 0x00}, tracing.CodeChangeUnspecified) // Some bytecode

	tracer := NewCrossShardTracer(stateDB, 8)

	// Call OnEnter at depth 0 - should be skipped
	// Note: typ=0xF1 is CALL opcode
	tracer.OnEnter(0, 0xF1, common.Address{}, addr, nil, 1000000, nil)

	// Should NOT have any pending external calls (depth 0 is the top-level call)
	if stateDB.HasPendingExternalCalls() {
		t.Error("Depth 0 calls should not be recorded as external calls")
	}
}

// TestCrossShardTracer_OnEnter_RecordsExternalContractCall verifies external calls are detected
func TestCrossShardTracer_OnEnter_RecordsExternalContractCall(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	// Set up an account with code (contract)
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	caller := common.HexToAddress("0xABCDabcdABcDabcDaBCDAbcdABcdAbCdABcDABCd")
	stateDB.CreateAccount(addr)
	stateDB.SetCode(addr, []byte{0x60, 0x00, 0x60, 0x00}, tracing.CodeChangeUnspecified) // Some bytecode

	tracer := NewCrossShardTracer(stateDB, 8)

	// Call OnEnter at depth > 0 for non-preloaded address with code
	// Note: typ=0xF1 is CALL opcode
	callData := []byte{0x12, 0x34, 0x56, 0x78}
	tracer.OnEnter(1, 0xF1, caller, addr, callData, 1000000, big.NewInt(100))

	// Should have a pending external call
	if !stateDB.HasPendingExternalCalls() {
		t.Fatal("Expected pending external call for non-preloaded contract")
	}

	calls := stateDB.GetPendingExternalCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 pending call, got %d", len(calls))
	}

	call := calls[0]
	if call.Address != addr {
		t.Errorf("Address mismatch: got %s, want %s", call.Address.Hex(), addr.Hex())
	}
	if call.Caller != caller {
		t.Errorf("Caller mismatch: got %s, want %s", call.Caller.Hex(), caller.Hex())
	}
}

// TestCrossShardTracer_OnEnter_SkipsPreloadedAddress verifies preloaded addresses are skipped
func TestCrossShardTracer_OnEnter_SkipsPreloadedAddress(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")

	// Preload an address via RwSet
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	preloadedRwSet := []protocol.RwVariable{
		{
			Address:        addr,
			ReferenceBlock: protocol.Reference{ShardNum: 0, BlockHeight: 100},
		},
	}
	stateDB := NewSimulationStateDBWithRwSet("test-tx", fetcher, preloadedRwSet)

	// Also set up code for the address
	stateDB.CreateAccount(addr)
	stateDB.SetCode(addr, []byte{0x60, 0x00}, tracing.CodeChangeUnspecified)

	tracer := NewCrossShardTracer(stateDB, 8)

	// Call OnEnter for preloaded address - should be skipped
	tracer.OnEnter(1, 0xF1, common.Address{}, addr, nil, 1000000, nil)

	// Should NOT have pending calls - address was preloaded
	if stateDB.HasPendingExternalCalls() {
		t.Error("Preloaded addresses should not be recorded as external calls")
	}
}

// TestCrossShardTracer_OnEnter_SkipsNonContract verifies EOA calls are skipped
func TestCrossShardTracer_OnEnter_SkipsNonContract(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	// Set up an account WITHOUT code (EOA)
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	stateDB.CreateAccount(addr)
	// No SetCode - this is an EOA

	tracer := NewCrossShardTracer(stateDB, 8)

	// Call OnEnter for EOA - should be skipped (no code)
	tracer.OnEnter(1, 0xF1, common.Address{}, addr, nil, 1000000, nil)

	// Should NOT have pending calls - address has no code
	if stateDB.HasPendingExternalCalls() {
		t.Error("EOA (no code) addresses should not be recorded as external calls")
	}
}

// TestCrossShardTracer_OnEnter_MultipleCallsSameAddress verifies deduplication
func TestCrossShardTracer_OnEnter_MultipleCallsSameAddress(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	addr := common.HexToAddress("0x1234")
	stateDB.CreateAccount(addr)
	stateDB.SetCode(addr, []byte{0x60, 0x00}, tracing.CodeChangeUnspecified)

	tracer := NewCrossShardTracer(stateDB, 8)

	// Call OnEnter multiple times for same address
	tracer.OnEnter(1, 0xF1, common.Address{}, addr, nil, 1000000, nil)
	tracer.OnEnter(2, 0xF1, common.Address{}, addr, nil, 1000000, nil)
	tracer.OnEnter(3, 0xF1, common.Address{}, addr, nil, 1000000, nil)

	// Should have only 1 pending call (deduplicated)
	calls := stateDB.GetPendingExternalCalls()
	if len(calls) != 1 {
		t.Errorf("Expected 1 pending call (deduplicated), got %d", len(calls))
	}
}

// TestCrossShardTracer_ShardIDCalculation verifies correct shard assignment
func TestCrossShardTracer_ShardIDCalculation(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	// Address ending in 0x05 → shard 5 (with 8 shards)
	addr := common.HexToAddress("0x0000000000000000000000000000000000000005")
	stateDB.CreateAccount(addr)
	stateDB.SetCode(addr, []byte{0x60, 0x00}, tracing.CodeChangeUnspecified)

	tracer := NewCrossShardTracer(stateDB, 8)
	tracer.OnEnter(1, 0xF1, common.Address{}, addr, nil, 1000000, nil)

	calls := stateDB.GetPendingExternalCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 pending call, got %d", len(calls))
	}

	if calls[0].ShardID != 5 {
		t.Errorf("Expected ShardID 5, got %d", calls[0].ShardID)
	}
}

// TestCrossShardTracer_OnExit_DoesNothing verifies OnExit is a no-op
func TestCrossShardTracer_OnExit_DoesNothing(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)
	tracer := NewCrossShardTracer(stateDB, 8)

	// OnExit should not panic and should be a no-op
	tracer.OnExit(1, nil, 1000, nil, false)
	tracer.OnExit(0, []byte{0x01}, 500, nil, true)

	// No assertions - just verifying it doesn't panic
}
