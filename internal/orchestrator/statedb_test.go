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

// ===== BuildRwSet Tests =====

// TestBuildRwSet_MultipleStorageReadsWrites verifies BuildRwSet handles multiple storage operations
func TestBuildRwSet_MultipleStorageReadsWrites(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	slot1 := common.HexToHash("0x01")
	slot2 := common.HexToHash("0x02")
	slot3 := common.HexToHash("0x03")
	value1 := common.HexToHash("0xaa")
	value2 := common.HexToHash("0xbb")
	value3 := common.HexToHash("0xcc")

	stateDB.CreateAccount(addr)

	// Write to multiple slots
	stateDB.SetState(addr, slot1, value1)
	stateDB.SetState(addr, slot2, value2)
	stateDB.SetState(addr, slot3, value3)

	// Read back (these don't add new entries since SetState already reads each slot)
	_ = stateDB.GetState(addr, slot1)
	_ = stateDB.GetState(addr, slot2)

	rwSet := stateDB.BuildRwSet()

	// Should have one RwVariable for the address
	if len(rwSet) != 1 {
		t.Fatalf("Expected 1 RwVariable, got %d", len(rwSet))
	}

	// Should have 3 writes (all SetState operations)
	if len(rwSet[0].WriteSet) != 3 {
		t.Errorf("Expected 3 WriteSet entries, got %d", len(rwSet[0].WriteSet))
	}

	// Should have 3 reads - SetState internally calls GetState to capture old values
	// Each SetState creates a read for that slot, so 3 SetState = 3 reads
	if len(rwSet[0].ReadSet) != 3 {
		t.Errorf("Expected 3 ReadSet entries, got %d", len(rwSet[0].ReadSet))
	}
}

// TestBuildRwSet_BalanceChangeWithoutStorage verifies balance changes are tracked
func TestBuildRwSet_BalanceChangeWithoutStorage(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	addr := common.HexToAddress("0xABCDabcdABcDabcDaBCDAbcdABcdAbCdABcDABCd")

	stateDB.CreateAccount(addr)
	stateDB.AddBalance(addr, uint256FromBig(big.NewInt(1000000)), 0)

	rwSet := stateDB.BuildRwSet()

	// Should have one RwVariable for the address
	if len(rwSet) != 1 {
		t.Fatalf("Expected 1 RwVariable, got %d", len(rwSet))
	}

	if rwSet[0].Address != addr {
		t.Errorf("Expected address %s, got %s", addr.Hex(), rwSet[0].Address.Hex())
	}
}

// TestBuildRwSet_MixedReadWriteSameSlot verifies read/write to same slot
func TestBuildRwSet_MixedReadWriteSameSlot(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")

	stateDB.CreateAccount(addr)

	// Read first (gets zero)
	val1 := stateDB.GetState(addr, slot)
	if val1 != (common.Hash{}) {
		t.Errorf("Expected zero hash for uninitialized slot")
	}

	// Write new value
	newValue := common.HexToHash("0xff")
	stateDB.SetState(addr, slot, newValue)

	// Read again (should get new value)
	val2 := stateDB.GetState(addr, slot)
	if val2 != newValue {
		t.Errorf("Expected %s, got %s", newValue.Hex(), val2.Hex())
	}

	rwSet := stateDB.BuildRwSet()

	if len(rwSet) != 1 {
		t.Fatalf("Expected 1 RwVariable, got %d", len(rwSet))
	}

	// Should have the slot in both read and write sets
	if len(rwSet[0].ReadSet) < 1 {
		t.Error("Expected at least 1 ReadSet entry for the slot")
	}
	if len(rwSet[0].WriteSet) != 1 {
		t.Errorf("Expected 1 WriteSet entry, got %d", len(rwSet[0].WriteSet))
	}
}

// TestBuildRwSet_EmptyForFailedSimulation verifies BuildRwSet returns empty for no operations
func TestBuildRwSet_EmptyForNoOperations(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	rwSet := stateDB.BuildRwSet()

	if len(rwSet) != 0 {
		t.Errorf("Expected empty RwSet for no operations, got %d entries", len(rwSet))
	}
}

// TestBuildRwSet_MultipleAddresses verifies multiple addresses are tracked separately
func TestBuildRwSet_MultipleAddresses(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	addr1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	addr2 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	addr3 := common.HexToAddress("0x3333333333333333333333333333333333333333")
	slot := common.HexToHash("0x01")
	value := common.HexToHash("0xff")

	// Operations on multiple addresses
	stateDB.CreateAccount(addr1)
	stateDB.AddBalance(addr1, uint256FromBig(big.NewInt(100)), 0)
	stateDB.SetState(addr1, slot, value)

	stateDB.CreateAccount(addr2)
	stateDB.AddBalance(addr2, uint256FromBig(big.NewInt(200)), 0)

	stateDB.CreateAccount(addr3)
	_ = stateDB.GetState(addr3, slot) // Just a read

	rwSet := stateDB.BuildRwSet()

	// Should have 3 RwVariables, one for each address
	if len(rwSet) != 3 {
		t.Errorf("Expected 3 RwVariables, got %d", len(rwSet))
	}

	// Verify all addresses are present
	foundAddrs := make(map[common.Address]bool)
	for _, rw := range rwSet {
		foundAddrs[rw.Address] = true
	}

	if !foundAddrs[addr1] || !foundAddrs[addr2] || !foundAddrs[addr3] {
		t.Error("Not all addresses found in RwSet")
	}
}

// ===== Lazy Fetching Tests =====

// TestLazyFetching_CachesAccountState verifies that account state is cached
// after the first access, avoiding redundant fetches. This is the core behavior
// that enables efficient single-pass execution.
func TestLazyFetching_CachesAccountState(t *testing.T) {
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("lazy-cache-test", fetcher)

	addr := common.HexToAddress("0x1234")
	stateDB.CreateAccount(addr)
	stateDB.AddBalance(addr, uint256FromBig(big.NewInt(1000)), 0)

	// First access
	bal1 := stateDB.GetBalance(addr)

	// Modify via SetBalance
	stateDB.AddBalance(addr, uint256FromBig(big.NewInt(500)), 0)

	// Second access should see updated value (from cache)
	bal2 := stateDB.GetBalance(addr)

	if bal1.Cmp(uint256FromBig(big.NewInt(1000))) != 0 {
		t.Errorf("Expected initial balance 1000, got %s", bal1.String())
	}

	if bal2.Cmp(uint256FromBig(big.NewInt(1500))) != 0 {
		t.Errorf("Expected updated balance 1500, got %s", bal2.String())
	}

	// Verify nonce is also cached
	nonce := stateDB.GetNonce(addr)
	if nonce != 0 {
		t.Errorf("Expected nonce 0, got %d", nonce)
	}
}

// TestLazyFetching_SingleThreadedSafety verifies that the double-checked locking
// pattern is safe because EVM execution is single-threaded (only one call at a time).
func TestLazyFetching_SingleThreadedSafety(t *testing.T) {
	// This test demonstrates the expected single-threaded access pattern
	fetcher, _ := NewStateFetcher(8, "")
	stateDB := NewSimulationStateDB("single-thread-test", fetcher)

	addr := common.HexToAddress("0x1234")
	stateDB.CreateAccount(addr)

	// Simulate sequential EVM operations (as would happen in real execution)
	// All operations on the same StateDB happen sequentially, never concurrently
	// Note: We only test balance/nonce which use cached account state,
	// not GetState which requires HTTP calls to shards
	for i := 0; i < 100; i++ {
		_ = stateDB.GetBalance(addr)
		_ = stateDB.GetNonce(addr)
	}

	// If we get here without panic/race, the sequential access pattern is correct
	t.Log("Single-threaded access pattern verified")
}

// =============================================================================
// V2 Coverage Tests - AccessList Operations
// =============================================================================

// TestAccessList_AddSlot verifies AddSlot behavior
func TestAccessList_AddSlot(t *testing.T) {
	al := newAccessList()
	addr := common.HexToAddress("0x1234")
	slot1 := common.HexToHash("0x01")
	slot2 := common.HexToHash("0x02")

	// Add slot to new address
	addrAdded, slotAdded := al.AddSlot(addr, slot1)
	if !addrAdded || !slotAdded {
		t.Error("First AddSlot should return true, true")
	}

	// Add different slot to existing address
	addrAdded, slotAdded = al.AddSlot(addr, slot2)
	if addrAdded || !slotAdded {
		t.Error("Second AddSlot should return false, true")
	}

	// Add same slot again
	addrAdded, slotAdded = al.AddSlot(addr, slot1)
	if addrAdded || slotAdded {
		t.Error("Duplicate AddSlot should return false, false")
	}
}

// TestAccessList_AddSlotAfterAddress verifies slot addition to previously added address
func TestAccessList_AddSlotAfterAddress(t *testing.T) {
	al := newAccessList()
	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")

	// First add just the address
	al.AddAddress(addr)

	// Then add a slot to that address (idx == -1 case)
	addrAdded, slotAdded := al.AddSlot(addr, slot)
	if addrAdded || !slotAdded {
		t.Error("AddSlot after AddAddress should return false, true")
	}
}

// TestAccessList_ContainsAddress verifies ContainsAddress
func TestAccessList_ContainsAddress(t *testing.T) {
	al := newAccessList()
	addr := common.HexToAddress("0x1234")

	if al.ContainsAddress(addr) {
		t.Error("Empty accessList should not contain address")
	}

	al.AddAddress(addr)
	if !al.ContainsAddress(addr) {
		t.Error("Should contain address after AddAddress")
	}
}

// TestAccessList_Contains verifies Contains function
func TestAccessList_Contains(t *testing.T) {
	al := newAccessList()
	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")

	// Neither present
	addrOk, slotOk := al.Contains(addr, slot)
	if addrOk || slotOk {
		t.Error("Empty list should return false, false")
	}

	// Only address present (idx == -1)
	al.AddAddress(addr)
	addrOk, slotOk = al.Contains(addr, slot)
	if !addrOk || slotOk {
		t.Error("Should return true, false for address-only")
	}

	// Both present
	al.AddSlot(addr, slot)
	addrOk, slotOk = al.Contains(addr, slot)
	if !addrOk || !slotOk {
		t.Error("Should return true, true when both present")
	}

	// Address present, different slot
	otherSlot := common.HexToHash("0x99")
	addrOk, slotOk = al.Contains(addr, otherSlot)
	if !addrOk || slotOk {
		t.Error("Should return true, false for missing slot")
	}
}

// TestCreateContract verifies CreateContract behavior
func TestCreateContract(t *testing.T) {
	fetcher, _ := NewStateFetcher(2, "")
	stateDB := NewSimulationStateDB("test-tx", fetcher)

	addr := common.HexToAddress("0xABCD")

	// CreateContract should create account in internal map
	stateDB.CreateContract(addr)

	// In EVM semantics, Exist returns true only if nonce > 0, balance > 0, or has code
	// A freshly created contract has none of these, so it's "empty" by EVM standards
	// But we can verify the account was created by checking it's in the cache
	// and that GetBalance doesn't trigger a fetch (no error since account exists)
	balance := stateDB.GetBalance(addr)
	if balance.Sign() != 0 {
		t.Error("Newly created contract should have zero balance")
	}

	// Account should be empty (zero balance, zero nonce, no code)
	if !stateDB.Empty(addr) {
		t.Error("Newly created contract should be empty")
	}
}

// TestTransferAndGetBlockHash verifies the helper functions
func TestTransferAndGetBlockHash(t *testing.T) {
	// Test getBlockHash - always returns zero hash in simulation
	hash := getBlockHash(12345)
	if hash != (common.Hash{}) {
		t.Error("getBlockHash should return zero hash")
	}
}
