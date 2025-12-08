package evm

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
)

// Helper function to create a test StateDB in memory
func createTestStateDB(t *testing.T) *state.StateDB {
	db := rawdb.NewMemoryDatabase()
	trdb := triedb.NewDatabase(db, nil)
	sdb := state.NewDatabase(trdb, nil)
	stateDB, err := state.New(common.Hash{}, sdb)
	if err != nil {
		t.Fatalf("Failed to create StateDB: %v", err)
	}
	return stateDB
}

// Helper function to generate test addresses on specific shards
func addressOnShard(shardID, numShards int) common.Address {
	// Last byte determines shard: addr[19] % numShards == shardID (works for small numbers too)
	addr := common.Address{}
	addr[19] = byte((shardID) % numShards)
	return addr
}

func TestTrackingStateDBCreation(t *testing.T) {
	innerDB := createTestStateDB(t)
	localShardID := 0
	numShards := 6

	tracking := NewTrackingStateDB(innerDB, localShardID, numShards)

	if tracking.inner != innerDB {
		t.Errorf("Inner StateDB not set correctly")
	}
	if tracking.localShardID != localShardID {
		t.Errorf("Expected localShardID %d, got %d", localShardID, tracking.localShardID)
	}
	if tracking.numShards != numShards {
		t.Errorf("Expected numShards %d, got %d", numShards, tracking.numShards)
	}
	if len(tracking.accessedAddrs) != 0 {
		t.Errorf("Expected empty accessed addresses map, got %d", len(tracking.accessedAddrs))
	}
}

func TestGetAccessedAddresses(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr1 := addressOnShard(0, 6)
	addr2 := addressOnShard(1, 6)

	// Record access to addresses
	tracking.recordAccess(addr1)
	tracking.recordAccess(addr2)

	accessed := tracking.GetAccessedAddresses()
	if len(accessed) != 2 {
		t.Errorf("Expected 2 accessed addresses, got %d", len(accessed))
	}

	// Verify both addresses are in the result
	found := make(map[common.Address]bool)
	for _, addr := range accessed {
		found[addr] = true
	}

	if !found[addr1] || !found[addr2] {
		t.Errorf("Not all addresses found in GetAccessedAddresses")
	}
}

func TestNoAccessRecorded(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	accessed := tracking.GetAccessedAddresses()
	if len(accessed) != 0 {
		t.Errorf("Expected 0 accessed addresses, got %d", len(accessed))
	}
}

func TestHasCrossShardAccessLocal(t *testing.T) {
	innerDB := createTestStateDB(t)
	localShardID := 0
	tracking := NewTrackingStateDB(innerDB, localShardID, 6)

	// Address on local shard (0)
	localAddr := addressOnShard(0, 6)
	tracking.recordAccess(localAddr)

	if tracking.HasCrossShardAccess() {
		t.Errorf("Expected no cross-shard access for local address")
	}
}

func TestHasCrossShardAccessCrossShard(t *testing.T) {
	innerDB := createTestStateDB(t)
	localShardID := 0
	tracking := NewTrackingStateDB(innerDB, localShardID, 6)

	// Address on different shard (1)
	crossShardAddr := addressOnShard(1, 6)
	tracking.recordAccess(crossShardAddr)

	if !tracking.HasCrossShardAccess() {
		t.Errorf("Expected cross-shard access detected")
	}
}

func TestHasCrossShardAccessMixed(t *testing.T) {
	innerDB := createTestStateDB(t)
	localShardID := 0
	tracking := NewTrackingStateDB(innerDB, localShardID, 6)

	// Local address
	localAddr := addressOnShard(0, 6)
	tracking.recordAccess(localAddr)

	// Cross-shard address
	crossShardAddr := addressOnShard(1, 6)
	tracking.recordAccess(crossShardAddr)

	if !tracking.HasCrossShardAccess() {
		t.Errorf("Expected cross-shard access with mixed addresses")
	}
}

func TestGetCrossShardAddresses(t *testing.T) {
	innerDB := createTestStateDB(t)
	localShardID := 0
	tracking := NewTrackingStateDB(innerDB, localShardID, 6)

	localAddr := addressOnShard(0, 6)
	crossAddr1 := addressOnShard(1, 6)
	crossAddr2 := addressOnShard(2, 6)

	tracking.recordAccess(localAddr)
	tracking.recordAccess(crossAddr1)
	tracking.recordAccess(crossAddr2)

	crossShardAddrs := tracking.GetCrossShardAddresses()

	if len(crossShardAddrs) != 2 {
		t.Errorf("Expected 2 cross-shard addresses, got %d", len(crossShardAddrs))
	}

	if shardID, ok := crossShardAddrs[crossAddr1]; !ok || shardID != 1 {
		t.Errorf("Expected cross-shard address %s to map to shard 1", crossAddr1.Hex())
	}

	if shardID, ok := crossShardAddrs[crossAddr2]; !ok || shardID != 2 {
		t.Errorf("Expected cross-shard address %s to map to shard 2", crossAddr2.Hex())
	}

	if _, ok := crossShardAddrs[localAddr]; ok {
		t.Errorf("Local address should not be in cross-shard addresses")
	}
}

func TestGetBalance(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)

	// GetBalance should record access
	balance := tracking.GetBalance(addr)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("GetBalance should record address access")
	}

	if balance == nil {
		t.Errorf("GetBalance should return non-nil balance")
	}
}

func TestAddBalance(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)
	amount := uint256.NewInt(100)

	// AddBalance should record access
	tracking.AddBalance(addr, amount, tracing.BalanceChangeUnspecified)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("AddBalance should record address access")
	}

	// Verify balance was actually added
	balance := innerDB.GetBalance(addr)
	if balance.Cmp(amount) != 0 {
		t.Errorf("Expected balance %d, got %d", amount, balance)
	}
}

func TestSubBalance(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)
	initialAmount := uint256.NewInt(1000)
	subAmount := uint256.NewInt(100)

	// Set initial balance in inner DB
	innerDB.AddBalance(addr, initialAmount, tracing.BalanceChangeUnspecified)

	// SubBalance should record access
	tracking.SubBalance(addr, subAmount, tracing.BalanceChangeUnspecified)

	accessed := tracking.GetAccessedAddresses()
	if len(accessed) != 1 {
		t.Errorf("SubBalance should record address access")
	}

	// Verify balance was actually subtracted
	balance := innerDB.GetBalance(addr)
	expected := uint256.NewInt(900)
	if balance.Cmp(expected) != 0 {
		t.Errorf("Expected balance %d, got %d", expected, balance)
	}
}

func TestGetNonce(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)

	// GetNonce should record access
	nonce := tracking.GetNonce(addr)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("GetNonce should record address access")
	}

	if nonce != 0 {
		t.Errorf("Expected initial nonce 0, got %d", nonce)
	}
}

func TestSetNonce(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)
	newNonce := uint64(42)

	// SetNonce should record access
	tracking.SetNonce(addr, newNonce, tracing.NonceChangeUnspecified)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("SetNonce should record address access")
	}

	// Verify nonce was actually set
	nonce := innerDB.GetNonce(addr)
	if nonce != newNonce {
		t.Errorf("Expected nonce %d, got %d", newNonce, nonce)
	}
}

func TestGetCode(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)
	code := []byte{0x60, 0x01} // PUSH1 1

	// Set code in inner DB first
	innerDB.SetCode(addr, code, tracing.CodeChangeUnspecified)

	// GetCode should record access
	retrievedCode := tracking.GetCode(addr)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("GetCode should record address access")
	}

	if len(retrievedCode) != len(code) {
		t.Errorf("Expected code length %d, got %d", len(code), len(retrievedCode))
	}
}

func TestSetCode(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)
	code := []byte{0x60, 0x01}

	// SetCode should record access
	tracking.SetCode(addr, code, tracing.CodeChangeUnspecified)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("SetCode should record address access")
	}

	// Verify code was actually set
	storedCode := innerDB.GetCode(addr)
	if len(storedCode) != len(code) {
		t.Errorf("Expected code length %d, got %d", len(code), len(storedCode))
	}
}

func TestGetCodeHash(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)

	// GetCodeHash should record access
	codeHash := tracking.GetCodeHash(addr)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("GetCodeHash should record address access")
	}

	if codeHash == (common.Hash{}) {
		t.Errorf("GetCodeHash returned empty hash")
	}
}

func TestGetCodeSize(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)
	code := []byte{0x60, 0x01, 0x60, 0x02}

	// Set code first
	innerDB.SetCode(addr, code, tracing.CodeChangeUnspecified)

	// GetCodeSize should record access
	size := tracking.GetCodeSize(addr)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("GetCodeSize should record address access")
	}

	if size != len(code) {
		t.Errorf("Expected code size %d, got %d", len(code), size)
	}
}

func TestGetState(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)
	slot := common.HexToHash("0x01")
	value := common.HexToHash("0xdeadbeef")

	// Set state first
	innerDB.SetState(addr, slot, value)

	// GetState should record access
	retrieved := tracking.GetState(addr, slot)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("GetState should record address access")
	}

	if retrieved != value {
		t.Errorf("Expected value %s, got %s", value.Hex(), retrieved.Hex())
	}
}

func TestSetState(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)
	slot := common.HexToHash("0x01")
	value := common.HexToHash("0xdeadbeef")

	// SetState should record access
	tracking.SetState(addr, slot, value)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("SetState should record address access")
	}

	// Verify state was actually set
	stored := innerDB.GetState(addr, slot)
	if stored != value {
		t.Errorf("Expected stored value %s, got %s", value.Hex(), stored.Hex())
	}
}

func TestGetCommittedState(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)
	slot := common.HexToHash("0x01")

	// GetCommittedState should record access
	committed := tracking.GetCommittedState(addr, slot)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("GetCommittedState should record address access")
	}

	// Committed state for new address should be empty
	if committed != (common.Hash{}) {
		t.Errorf("Expected empty committed state, got %s", committed.Hex())
	}
}

func TestCreateAccount(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)

	// CreateAccount should record access
	tracking.CreateAccount(addr)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("CreateAccount should record address access")
	}

	// Verify account was actually created
	if !innerDB.Exist(addr) {
		t.Errorf("Expected account to exist after CreateAccount")
	}
}

func TestSelfDestruct(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)

	// Create account first
	innerDB.CreateAccount(addr)
	innerDB.AddBalance(addr, uint256.NewInt(100), tracing.BalanceChangeUnspecified)

	// SelfDestruct should record access
	tracking.SelfDestruct(addr)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("SelfDestruct should record address access")
	}
}

func TestHasSelfDestructed(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)

	// Create and self-destruct account
	innerDB.CreateAccount(addr)
	innerDB.SelfDestruct(addr)

	// HasSelfDestructed should record access
	hasSelfDestructed := tracking.HasSelfDestructed(addr)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("HasSelfDestructed should record address access")
	}

	if !hasSelfDestructed {
		t.Errorf("Expected account to be self-destructed")
	}
}

func TestExist(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)

	// Exist should record access
	exists := tracking.Exist(addr)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("Exist should record address access")
	}

	if exists {
		t.Errorf("Expected address to not exist initially")
	}

	// Create account and try again
	tracking = NewTrackingStateDB(innerDB, 0, 6)
	innerDB.CreateAccount(addr)

	exists = tracking.Exist(addr)

	if !exists {
		t.Errorf("Expected address to exist after CreateAccount")
	}
}

func TestEmpty(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)

	// Empty should record access
	isEmpty := tracking.Empty(addr)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("Empty should record address access")
	}

	if !isEmpty {
		t.Errorf("Expected address to be empty initially")
	}

	// Add balance and try again
	tracking = NewTrackingStateDB(innerDB, 0, 6)
	innerDB.CreateAccount(addr)
	innerDB.AddBalance(addr, uint256.NewInt(100), tracing.BalanceChangeUnspecified)

	isEmpty = tracking.Empty(addr)

	if isEmpty {
		t.Errorf("Expected address to not be empty after adding balance")
	}
}

func TestGetTransientState(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)
	slot := common.HexToHash("0x01")

	// GetTransientState should record access
	value := tracking.GetTransientState(addr, slot)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("GetTransientState should record address access")
	}

	if value != (common.Hash{}) {
		t.Errorf("Expected empty transient state, got %s", value.Hex())
	}
}

func TestSetTransientState(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)
	slot := common.HexToHash("0x01")
	value := common.HexToHash("0xabcdef")

	// SetTransientState should record access
	tracking.SetTransientState(addr, slot, value)

	if len(tracking.GetAccessedAddresses()) != 1 {
		t.Errorf("SetTransientState should record address access")
	}
}

func TestMultipleAddressAccess(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	// Access 5 different addresses
	for i := 0; i < 5; i++ {
		addr := addressOnShard(i%6, 6)
		tracking.GetBalance(addr)
	}

	accessed := tracking.GetAccessedAddresses()
	if len(accessed) != 5 {
		t.Errorf("Expected 5 unique addresses, got %d", len(accessed))
	}
}

func TestDuplicateAccessTracking(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)

	// Access same address multiple times
	tracking.GetBalance(addr)
	tracking.GetNonce(addr)
	tracking.GetCode(addr)

	accessed := tracking.GetAccessedAddresses()
	if len(accessed) != 1 {
		t.Errorf("Expected 1 unique address despite multiple accesses, got %d", len(accessed))
	}
}

func TestShardMapping(t *testing.T) {
	innerDB := createTestStateDB(t)
	numShards := 6

	for localShard := 0; localShard < numShards; localShard++ {
		tracking := NewTrackingStateDB(innerDB, localShard, numShards)

		// Test that shard mapping is correct
		for shardID := 0; shardID < numShards; shardID++ {
			addr := addressOnShard(shardID, numShards)
			tracking.recordAccess(addr)

			crossShardAddrs := tracking.GetCrossShardAddresses()

			if shardID == localShard {
				// Should not be in cross-shard addresses
				if _, ok := crossShardAddrs[addr]; ok {
					t.Errorf("Local address should not be in cross-shard addresses")
				}
			} else {
				// Should be in cross-shard addresses with correct shard ID
				if mappedShardID, ok := crossShardAddrs[addr]; !ok {
					t.Errorf("Cross-shard address should be in map")
				} else if mappedShardID != shardID {
					t.Errorf("Expected shard %d, got %d", shardID, mappedShardID)
				}
			}

			// Clear for next iteration
			tracking.accessedAddrs = make(map[common.Address]bool)
		}
	}
}

func TestRefundMethods(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	// Refund methods should not record access (they don't touch state)
	initialCount := len(tracking.GetAccessedAddresses())

	tracking.AddRefund(100)
	if len(tracking.GetAccessedAddresses()) != initialCount {
		t.Errorf("AddRefund should not record access")
	}

	tracking.SubRefund(50)
	if len(tracking.GetAccessedAddresses()) != initialCount {
		t.Errorf("SubRefund should not record access")
	}

	_ = tracking.GetRefund()
	if len(tracking.GetAccessedAddresses()) != initialCount {
		t.Errorf("GetRefund should not record access")
	}
}

func TestAccessListMethods(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)
	slot := common.HexToHash("0x01")

	// Access list methods may not record address access in inner DB
	// but they should work correctly
	tracking.AddAddressToAccessList(addr)
	tracking.AddSlotToAccessList(addr, slot)

	// These queries should work without error
	addrInList := tracking.AddressInAccessList(addr)
	if !addrInList {
		t.Errorf("Expected address to be in access list")
	}

	addrOk, slotOk := tracking.SlotInAccessList(addr, slot)
	if !addrOk || !slotOk {
		t.Errorf("Expected address and slot to be in access list")
	}
}

func TestSnapshotMethods(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	// Snapshot and RevertToSnapshot should work correctly
	snapshot := tracking.Snapshot()
	if snapshot < 0 {
		t.Errorf("Expected valid snapshot ID")
	}

	// This should not panic
	tracking.RevertToSnapshot(snapshot)
}

func TestPrepareMethod(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	sender := addressOnShard(0, 6)
	coinbase := addressOnShard(1, 6)

	// Prepare should work without error
	tracking.Prepare(
		params.MainnetChainConfig.Rules(big.NewInt(0), false, 0),
		sender,
		coinbase,
		nil,
		nil,
		nil,
	)
}

func TestAddLog(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	addr := addressOnShard(0, 6)
	log := &types.Log{
		Address: addr,
		Topics:  []common.Hash{common.HexToHash("0x01")},
		Data:    []byte{0x01, 0x02},
	}

	// AddLog should work without error
	tracking.AddLog(log)
}

func TestPreimageMethod(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	hash := common.HexToHash("0xabcdef")
	preimage := []byte{0x01, 0x02, 0x03}

	// AddPreimage should work without error
	tracking.AddPreimage(hash, preimage)
}

func TestFinaliseMethod(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	// Finalise should work without error
	tracking.Finalise(false)
}

func TestPassThroughMethods(t *testing.T) {
	innerDB := createTestStateDB(t)
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	// Methods that just pass through to inner DB
	_ = tracking.PointCache()
	_ = tracking.Witness()
	_ = tracking.AccessEvents()

	// These should not panic
}

func TestCrossShardDetectionWithDifferentNumShards(t *testing.T) {
	innerDB := createTestStateDB(t)

	// Test with 3 shards
	tracking := NewTrackingStateDB(innerDB, 0, 3)

	addr1 := addressOnShard(0, 3)
	addr2 := addressOnShard(1, 3)
	addr3 := addressOnShard(2, 3)

	tracking.recordAccess(addr1)
	tracking.recordAccess(addr2)
	tracking.recordAccess(addr3)

	if !tracking.HasCrossShardAccess() {
		t.Errorf("Expected cross-shard access with 3 addresses on different shards")
	}

	crossShardAddrs := tracking.GetCrossShardAddresses()
	if len(crossShardAddrs) != 2 {
		t.Errorf("Expected 2 cross-shard addresses, got %d", len(crossShardAddrs))
	}
}

func BenchmarkRecordAccess(b *testing.B) {
	innerDB := createTestStateDB(&testing.T{})
	tracking := NewTrackingStateDB(innerDB, 0, 6)
	addr := addressOnShard(0, 6)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracking.recordAccess(addr)
	}
}

func BenchmarkGetBalance(b *testing.B) {
	innerDB := createTestStateDB(&testing.T{})
	tracking := NewTrackingStateDB(innerDB, 0, 6)
	addr := addressOnShard(0, 6)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tracking.GetBalance(addr)
	}
}

func BenchmarkHasCrossShardAccess(b *testing.B) {
	innerDB := createTestStateDB(&testing.T{})
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	// Pre-populate with addresses
	for i := 0; i < 100; i++ {
		addr := addressOnShard(i%6, 6)
		tracking.recordAccess(addr)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tracking.HasCrossShardAccess()
	}
}

func BenchmarkGetCrossShardAddresses(b *testing.B) {
	innerDB := createTestStateDB(&testing.T{})
	tracking := NewTrackingStateDB(innerDB, 0, 6)

	// Pre-populate with addresses
	for i := 0; i < 100; i++ {
		addr := addressOnShard(i%6, 6)
		tracking.recordAccess(addr)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tracking.GetCrossShardAddresses()
	}
}
