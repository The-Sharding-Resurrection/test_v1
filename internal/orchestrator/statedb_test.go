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
