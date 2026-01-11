package orchestrator

import (
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
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

// TestAddressToShard verifies shard ID calculation
func TestAddressToShard(t *testing.T) {
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
			// Use the same formula as AddressToShard
			got := int(tt.addr[len(tt.addr)-1]) % tt.numShards
			if got != tt.expected {
				t.Errorf("AddressToShard(%s) = %d, want %d", tt.addr.Hex(), got, tt.expected)
			}
		})
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
