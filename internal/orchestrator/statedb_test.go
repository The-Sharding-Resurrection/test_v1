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
	fetcher := NewStateFetcher(2)
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
	fetcher := NewStateFetcher(2)
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	stateDB.AddRefund(100)
	stateDB.SubRefund(100)

	if stateDB.GetRefund() != 0 {
		t.Errorf("Expected refund 0, got %d", stateDB.GetRefund())
	}
}

// TestSimulationStateDB_ConcurrentRefund verifies thread safety of refund operations
func TestSimulationStateDB_ConcurrentRefund(t *testing.T) {
	fetcher := NewStateFetcher(2)
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
	fetcher := NewStateFetcher(2)
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
	fetcher := NewStateFetcher(2)
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	stateDB.AddRefund(100)
	stateDB.SubRefund(0) // Zero subtraction should be no-op

	if stateDB.GetRefund() != 100 {
		t.Errorf("Expected refund 100 after zero subtraction, got %d", stateDB.GetRefund())
	}
}

// TestSubRefund_MultipleUnderflows verifies multiple underflows don't cause issues
func TestSubRefund_MultipleUnderflows(t *testing.T) {
	fetcher := NewStateFetcher(2)
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
	fetcher := NewStateFetcher(2)
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
	fetcher := NewStateFetcher(2)
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
