package shard

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

func TestNewChain(t *testing.T) {
	chain := NewChain(0)

	if chain.height != 0 {
		t.Errorf("Expected initial height 0, got %d", chain.height)
	}
	if len(chain.blocks) != 1 {
		t.Errorf("Expected 1 genesis block, got %d blocks", len(chain.blocks))
	}
	if chain.blocks[0].Height != 0 {
		t.Errorf("Expected genesis block height 0, got %d", chain.blocks[0].Height)
	}
}

func TestChain_AddTx(t *testing.T) {
	chain := NewChain(0)

	chain.AddTx(protocol.Transaction{ID: "tx-1", IsCrossShard: true})
	chain.AddTx(protocol.Transaction{ID: "tx-2", IsCrossShard: false})

	if len(chain.currentTxs) != 2 {
		t.Errorf("Expected 2 txs, got %d", len(chain.currentTxs))
	}
	if chain.currentTxs[0].ID != "tx-1" || !chain.currentTxs[0].IsCrossShard {
		t.Error("First tx mismatch")
	}
	if chain.currentTxs[1].ID != "tx-2" || chain.currentTxs[1].IsCrossShard {
		t.Error("Second tx mismatch")
	}
}

func TestChain_AddPrepareResult(t *testing.T) {
	chain := NewChain(0)

	chain.AddPrepareResult("tx-1", true)
	chain.AddPrepareResult("tx-2", false)

	if len(chain.prepares) != 2 {
		t.Errorf("Expected 2 prepare results, got %d", len(chain.prepares))
	}
	if !chain.prepares["tx-1"] {
		t.Error("Expected tx-1 prepare to be true")
	}
	if chain.prepares["tx-2"] {
		t.Error("Expected tx-2 prepare to be false")
	}
}

func TestChain_LockFunds(t *testing.T) {
	chain := NewChain(0)
	addr := common.HexToAddress("0x1234")
	amount := big.NewInt(1000)

	chain.LockFunds("tx-1", addr, amount)

	lock, ok := chain.GetLockedFunds("tx-1")
	if !ok {
		t.Error("Expected to find locked funds")
	}
	if lock.Address != addr {
		t.Error("Address mismatch")
	}
	if lock.Amount.Cmp(amount) != 0 {
		t.Error("Amount mismatch")
	}

	// Verify amount is copied, not referenced
	amount.SetInt64(2000)
	if lock.Amount.Cmp(big.NewInt(1000)) != 0 {
		t.Error("Lock amount should be independent copy")
	}
}

func TestChain_ClearLock(t *testing.T) {
	chain := NewChain(0)
	addr := common.HexToAddress("0x1234")

	chain.LockFunds("tx-1", addr, big.NewInt(1000))
	chain.ClearLock("tx-1")

	_, ok := chain.GetLockedFunds("tx-1")
	if ok {
		t.Error("Lock should be cleared")
	}
}

func TestChain_PendingCredits(t *testing.T) {
	chain := NewChain(0)
	addr1 := common.HexToAddress("0x5678")
	addr2 := common.HexToAddress("0x9999")
	amount1 := big.NewInt(500)
	amount2 := big.NewInt(300)

	// Store multiple credits for same tx
	chain.StorePendingCredit("tx-1", addr1, amount1)
	chain.StorePendingCredit("tx-1", addr2, amount2)

	credits, ok := chain.GetPendingCredits("tx-1")
	if !ok {
		t.Error("Expected to find pending credits")
	}
	if len(credits) != 2 {
		t.Errorf("Expected 2 credits, got %d", len(credits))
	}
	if credits[0].Address != addr1 || credits[0].Amount.Cmp(amount1) != 0 {
		t.Error("First credit mismatch")
	}
	if credits[1].Address != addr2 || credits[1].Amount.Cmp(amount2) != 0 {
		t.Error("Second credit mismatch")
	}

	// Clear and verify
	chain.ClearPendingCredit("tx-1")
	_, ok = chain.GetPendingCredits("tx-1")
	if ok {
		t.Error("Pending credits should be cleared")
	}
}

func TestChain_ProduceBlock(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Add some state
	chain.AddTx(protocol.Transaction{ID: "tx-1", IsCrossShard: true})
	chain.AddPrepareResult("tx-1", true)

	block, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	if block.Height != 1 {
		t.Errorf("Expected block height 1, got %d", block.Height)
	}
	if len(block.TxOrdering) != 1 {
		t.Errorf("Expected 1 tx in ordering, got %d", len(block.TxOrdering))
	}
	if len(block.TpcPrepare) != 1 {
		t.Errorf("Expected 1 prepare result, got %d", len(block.TpcPrepare))
	}
	if !block.TpcPrepare["tx-1"] {
		t.Error("Expected tx-1 prepare to be true in block")
	}

	// Verify state is cleared
	if len(chain.currentTxs) != 0 {
		t.Error("currentTxs should be cleared after block")
	}
	if len(chain.prepares) != 0 {
		t.Error("prepares should be cleared after block")
	}
}

func TestChain_BlockLinking(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	genesisHash := chain.blocks[0].Hash()
	block1, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	if block1.PrevHash != genesisHash {
		t.Error("Block 1 should link to genesis")
	}

	block1Hash := block1.Hash()
	block2, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	if block2.PrevHash != block1Hash {
		t.Error("Block 2 should link to block 1")
	}
}

func TestChain_MultipleLocks(t *testing.T) {
	chain := NewChain(0)

	// Add multiple locks
	for i := 0; i < 5; i++ {
		addr := common.BigToAddress(big.NewInt(int64(i + 1)))
		chain.LockFunds("tx-"+string(rune('A'+i)), addr, big.NewInt(int64(100*(i+1))))
	}

	// Verify all locks exist
	for i := 0; i < 5; i++ {
		txID := "tx-" + string(rune('A'+i))
		lock, ok := chain.GetLockedFunds(txID)
		if !ok {
			t.Errorf("Expected lock for %s", txID)
		}
		expectedAmount := big.NewInt(int64(100 * (i + 1)))
		if lock.Amount.Cmp(expectedAmount) != 0 {
			t.Errorf("Amount mismatch for %s", txID)
		}
	}

	// Clear some locks
	chain.ClearLock("tx-A")
	chain.ClearLock("tx-C")

	// Verify cleared and remaining
	_, ok := chain.GetLockedFunds("tx-A")
	if ok {
		t.Error("tx-A should be cleared")
	}
	_, ok = chain.GetLockedFunds("tx-B")
	if !ok {
		t.Error("tx-B should still exist")
	}
}

func TestChain_ConcurrentProduction(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Simulate multiple rounds
	for round := 0; round < 3; round++ {
		// Add txs for this round
		for i := 0; i < 3; i++ {
			chain.AddTx(protocol.Transaction{ID: "tx-" + string(rune('0'+round)) + "-" + string(rune('A'+i)), IsCrossShard: true})
			chain.AddPrepareResult("tx-"+string(rune('0'+round))+"-"+string(rune('A'+i)), i%2 == 0)
		}

		block, err := chain.ProduceBlock(evmState)
		if err != nil {
			t.Fatalf("ProduceBlock failed: %v", err)
		}
		if int(block.Height) != round+1 {
			t.Errorf("Expected height %d, got %d", round+1, block.Height)
		}
		if len(block.TxOrdering) != 3 {
			t.Errorf("Round %d: expected 3 txs, got %d", round, len(block.TxOrdering))
		}
	}

	if chain.height != 3 {
		t.Errorf("Expected final height 3, got %d", chain.height)
	}
}

func TestChain_SimulationLock(t *testing.T) {
	chain := NewChain(0)
	addr := common.HexToAddress("0x1234")
	txID := "tx-sim-1"
	balance := big.NewInt(1000)
	nonce := uint64(5)
	code := []byte{0x60, 0x80, 0x60, 0x40}
	codeHash := common.HexToHash("0xabcd")
	storage := map[common.Hash]common.Hash{
		common.HexToHash("0x01"): common.HexToHash("0x100"),
		common.HexToHash("0x02"): common.HexToHash("0x200"),
	}

	// Lock address
	err := chain.LockAddress(txID, addr, balance, nonce, code, codeHash, storage)
	if err != nil {
		t.Fatalf("LockAddress failed: %v", err)
	}

	// Verify lock exists
	if !chain.IsAddressLocked(addr) {
		t.Error("Address should be locked")
	}

	// Retrieve lock
	lock, ok := chain.GetSimulationLockByAddr(addr)
	if !ok {
		t.Fatal("Expected to find lock by address")
	}
	if lock.Balance.Cmp(balance) != 0 {
		t.Error("Balance mismatch")
	}
	if lock.Nonce != nonce {
		t.Error("Nonce mismatch")
	}
	if len(lock.Code) != len(code) {
		t.Error("Code length mismatch")
	}
	if len(lock.Storage) != len(storage) {
		t.Error("Storage length mismatch")
	}

	// Verify data is copied
	balance.SetInt64(9999)
	if lock.Balance.Cmp(big.NewInt(1000)) != 0 {
		t.Error("Lock balance should be independent copy")
	}
}

func TestChain_SimulationLock_AlreadyLocked(t *testing.T) {
	chain := NewChain(0)
	addr := common.HexToAddress("0x1234")
	storage := map[common.Hash]common.Hash{}

	// First lock succeeds
	err := chain.LockAddress("tx-1", addr, big.NewInt(100), 1, nil, common.Hash{}, storage)
	if err != nil {
		t.Fatalf("First lock should succeed: %v", err)
	}

	// Second lock by different tx fails
	err = chain.LockAddress("tx-2", addr, big.NewInt(200), 2, nil, common.Hash{}, storage)
	if err == nil {
		t.Fatal("Second lock should fail")
	}
	lockedErr, ok := err.(*AddressLockedError)
	if !ok {
		t.Fatalf("Expected AddressLockedError, got %T", err)
	}
	if lockedErr.LockedBy != "tx-1" {
		t.Errorf("Expected LockedBy tx-1, got %s", lockedErr.LockedBy)
	}

	// Same tx can re-lock (no-op)
	err = chain.LockAddress("tx-1", addr, big.NewInt(300), 3, nil, common.Hash{}, storage)
	if err != nil {
		t.Fatalf("Same tx should be able to re-lock: %v", err)
	}
}

func TestChain_SimulationLock_MultipleAddresses(t *testing.T) {
	chain := NewChain(0)
	txID := "tx-multi"
	addr1 := common.HexToAddress("0x1111")
	addr2 := common.HexToAddress("0x2222")
	storage := map[common.Hash]common.Hash{}

	// Lock first address
	err := chain.LockAddress(txID, addr1, big.NewInt(100), 1, nil, common.Hash{}, storage)
	if err != nil {
		t.Fatalf("Lock addr1 failed: %v", err)
	}

	// Lock second address with same tx
	err = chain.LockAddress(txID, addr2, big.NewInt(200), 2, nil, common.Hash{}, storage)
	if err != nil {
		t.Fatalf("Lock addr2 failed: %v", err)
	}

	// Both should be locked
	if !chain.IsAddressLocked(addr1) || !chain.IsAddressLocked(addr2) {
		t.Error("Both addresses should be locked")
	}

	// Get all locks for tx
	locks, ok := chain.GetSimulationLocks(txID)
	if !ok {
		t.Fatal("Expected to find locks for tx")
	}
	if len(locks) != 2 {
		t.Errorf("Expected 2 locks, got %d", len(locks))
	}

	// Unlock one address
	chain.UnlockAddress(txID, addr1)
	if chain.IsAddressLocked(addr1) {
		t.Error("addr1 should be unlocked")
	}
	if !chain.IsAddressLocked(addr2) {
		t.Error("addr2 should still be locked")
	}

	// Locks map should still have addr2
	locks, ok = chain.GetSimulationLocks(txID)
	if !ok {
		t.Fatal("Should still have locks for tx")
	}
	if len(locks) != 1 {
		t.Errorf("Expected 1 lock remaining, got %d", len(locks))
	}
}

func TestChain_UnlockAllForTx(t *testing.T) {
	chain := NewChain(0)
	txID := "tx-unlock-all"
	addr1 := common.HexToAddress("0x1111")
	addr2 := common.HexToAddress("0x2222")
	storage := map[common.Hash]common.Hash{}

	chain.LockAddress(txID, addr1, big.NewInt(100), 1, nil, common.Hash{}, storage)
	chain.LockAddress(txID, addr2, big.NewInt(200), 2, nil, common.Hash{}, storage)

	// Unlock all
	chain.UnlockAllForTx(txID)

	// Both should be unlocked
	if chain.IsAddressLocked(addr1) || chain.IsAddressLocked(addr2) {
		t.Error("Both addresses should be unlocked")
	}

	// Locks map should be empty for tx
	_, ok := chain.GetSimulationLocks(txID)
	if ok {
		t.Error("Should not have locks for tx after unlock all")
	}
}

func TestChain_GetLockedAmountForAddress(t *testing.T) {
	chain := NewChain(0)
	addr := common.HexToAddress("0x1234")

	// Initially no locks
	locked := chain.GetLockedAmountForAddress(addr)
	if locked.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("Expected 0 locked, got %s", locked.String())
	}

	// Add first lock
	chain.LockFunds("tx-1", addr, big.NewInt(100))
	locked = chain.GetLockedAmountForAddress(addr)
	if locked.Cmp(big.NewInt(100)) != 0 {
		t.Errorf("Expected 100 locked, got %s", locked.String())
	}

	// Add second lock for same address
	chain.LockFunds("tx-2", addr, big.NewInt(200))
	locked = chain.GetLockedAmountForAddress(addr)
	if locked.Cmp(big.NewInt(300)) != 0 {
		t.Errorf("Expected 300 locked, got %s", locked.String())
	}

	// Add lock for different address (should not affect)
	otherAddr := common.HexToAddress("0x5678")
	chain.LockFunds("tx-3", otherAddr, big.NewInt(500))
	locked = chain.GetLockedAmountForAddress(addr)
	if locked.Cmp(big.NewInt(300)) != 0 {
		t.Errorf("Expected 300 locked for addr, got %s", locked.String())
	}

	// Clear one lock
	chain.ClearLock("tx-1")
	locked = chain.GetLockedAmountForAddress(addr)
	if locked.Cmp(big.NewInt(200)) != 0 {
		t.Errorf("Expected 200 locked after clear, got %s", locked.String())
	}

	// Clear remaining lock
	chain.ClearLock("tx-2")
	locked = chain.GetLockedAmountForAddress(addr)
	if locked.Cmp(big.NewInt(0)) != 0 {
		t.Errorf("Expected 0 locked after all cleared, got %s", locked.String())
	}
}

// =============================================================================
// RecordPrepareTx Tests (for crash recovery)
// =============================================================================

func TestChain_RecordPrepareTx(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Record prepare tx
	prepareTx := protocol.Transaction{
		TxType:         protocol.TxTypePrepareDebit,
		CrossShardTxID: "tx-1",
		From:           common.HexToAddress("0x1234"),
		Value:          protocol.NewBigInt(big.NewInt(100)),
	}
	chain.RecordPrepareTx(prepareTx)

	// Verify it's queued
	if len(chain.prepareTxs) != 1 {
		t.Errorf("Expected 1 prepare tx, got %d", len(chain.prepareTxs))
	}

	// Produce block and verify prepare tx is included
	block, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	if len(block.PrepareTxs) != 1 {
		t.Fatalf("Expected 1 prepare tx in block, got %d", len(block.PrepareTxs))
	}
	if block.PrepareTxs[0].TxType != protocol.TxTypePrepareDebit {
		t.Errorf("Wrong TxType in block: got %s", block.PrepareTxs[0].TxType)
	}
	if block.PrepareTxs[0].CrossShardTxID != "tx-1" {
		t.Errorf("Wrong CrossShardTxID in block: got %s", block.PrepareTxs[0].CrossShardTxID)
	}

	// Verify prepareTxs cleared after block production
	if len(chain.prepareTxs) != 0 {
		t.Error("prepareTxs should be cleared after block")
	}
}

func TestChain_RecordPrepareTx_DeepCopy(t *testing.T) {
	chain := NewChain(0)

	originalValue := big.NewInt(100)
	prepareTx := protocol.Transaction{
		TxType:         protocol.TxTypePrepareDebit,
		CrossShardTxID: "tx-deep-copy",
		Value:          protocol.NewBigInt(originalValue),
	}
	chain.RecordPrepareTx(prepareTx)

	// Modify original value
	originalValue.SetInt64(999)

	// Verify stored value is independent (deep copy worked)
	if chain.prepareTxs[0].Value.ToBigInt().Cmp(big.NewInt(100)) != 0 {
		t.Errorf("PrepareTx should be deep copied, got value %s", chain.prepareTxs[0].Value.ToBigInt().String())
	}
}

func TestChain_RecordPrepareTx_AllTypes(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Record all three prepare tx types
	chain.RecordPrepareTx(protocol.Transaction{
		TxType:         protocol.TxTypePrepareDebit,
		CrossShardTxID: "tx-1",
		From:           common.HexToAddress("0x1111"),
		Value:          protocol.NewBigInt(big.NewInt(100)),
	})
	chain.RecordPrepareTx(protocol.Transaction{
		TxType:         protocol.TxTypePrepareCredit,
		CrossShardTxID: "tx-1",
		To:             common.HexToAddress("0x2222"),
		Value:          protocol.NewBigInt(big.NewInt(100)),
	})
	chain.RecordPrepareTx(protocol.Transaction{
		TxType:         protocol.TxTypePrepareWriteSet,
		CrossShardTxID: "tx-1",
		From:           common.HexToAddress("0x1111"),
		RwSet: []protocol.RwVariable{{
			Address: common.HexToAddress("0x3333"),
		}},
	})

	// Produce block
	block, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	// Verify all three are in block
	if len(block.PrepareTxs) != 3 {
		t.Fatalf("Expected 3 prepare txs in block, got %d", len(block.PrepareTxs))
	}

	// Check types in order
	expectedTypes := []protocol.TxType{
		protocol.TxTypePrepareDebit,
		protocol.TxTypePrepareCredit,
		protocol.TxTypePrepareWriteSet,
	}
	for i, expected := range expectedTypes {
		if block.PrepareTxs[i].TxType != expected {
			t.Errorf("PrepareTx[%d]: expected type %s, got %s", i, expected, block.PrepareTxs[i].TxType)
		}
	}
}

func TestChain_RecordPrepareTx_MultipleBlocks(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// First block with 2 prepare txs
	chain.RecordPrepareTx(protocol.Transaction{
		TxType:         protocol.TxTypePrepareDebit,
		CrossShardTxID: "tx-block1-a",
	})
	chain.RecordPrepareTx(protocol.Transaction{
		TxType:         protocol.TxTypePrepareCredit,
		CrossShardTxID: "tx-block1-b",
	})

	block1, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock 1 failed: %v", err)
	}
	if len(block1.PrepareTxs) != 2 {
		t.Errorf("Block 1: expected 2 prepare txs, got %d", len(block1.PrepareTxs))
	}

	// Second block with 1 prepare tx
	chain.RecordPrepareTx(protocol.Transaction{
		TxType:         protocol.TxTypePrepareDebit,
		CrossShardTxID: "tx-block2",
	})

	block2, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock 2 failed: %v", err)
	}
	if len(block2.PrepareTxs) != 1 {
		t.Errorf("Block 2: expected 1 prepare tx, got %d", len(block2.PrepareTxs))
	}

	// Verify block1 remains unchanged after block2 (no slice aliasing)
	if len(block1.PrepareTxs) != 2 {
		t.Errorf("Block 1 corrupted: expected 2 prepare txs, got %d", len(block1.PrepareTxs))
	}

	// Third block with no prepare txs
	block3, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock 3 failed: %v", err)
	}
	if len(block3.PrepareTxs) != 0 {
		t.Errorf("Block 3: expected 0 prepare txs, got %d", len(block3.PrepareTxs))
	}

	// Verify previous blocks remain immutable
	if len(block1.PrepareTxs) != 2 {
		t.Errorf("Block 1 corrupted after block 3: expected 2, got %d", len(block1.PrepareTxs))
	}
	if len(block2.PrepareTxs) != 1 {
		t.Errorf("Block 2 corrupted after block 3: expected 1, got %d", len(block2.PrepareTxs))
	}
}

// =============================================================================
// V2.4: Transaction Priority Ordering Tests
// =============================================================================

func TestTxTypePriority(t *testing.T) {
	// Test priority values
	tests := []struct {
		txType         protocol.TxType
		expectedPriority int
		description    string
	}{
		{protocol.TxTypeCrossDebit, 1, "Finalize (cross_debit)"},
		{protocol.TxTypeCrossCredit, 1, "Finalize (cross_credit)"},
		{protocol.TxTypeCrossWriteSet, 1, "Finalize (cross_writeset)"},
		{protocol.TxTypeUnlock, 2, "Unlock"},
		{protocol.TxTypeLock, 3, "Lock"},
		{protocol.TxTypeLocal, 4, "Local (default)"},
		{protocol.TxTypePrepareDebit, 4, "PrepareDebit (treated as local priority)"},
		{protocol.TxTypeCrossAbort, 4, "CrossAbort (treated as local priority)"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			priority := tt.txType.Priority()
			if priority != tt.expectedPriority {
				t.Errorf("%s: expected priority %d, got %d", tt.description, tt.expectedPriority, priority)
			}
		})
	}
}

func TestChain_SortTransactionsByPriority(t *testing.T) {
	chain := NewChain(0)

	// Create transactions in random order
	txs := []protocol.Transaction{
		{ID: "local-1", TxType: protocol.TxTypeLocal},
		{ID: "lock-1", TxType: protocol.TxTypeLock},
		{ID: "finalize-1", TxType: protocol.TxTypeCrossDebit},
		{ID: "unlock-1", TxType: protocol.TxTypeUnlock},
		{ID: "local-2", TxType: protocol.TxTypeLocal},
		{ID: "finalize-2", TxType: protocol.TxTypeCrossCredit},
	}

	sorted := chain.sortTransactionsByPriority(txs)

	// Verify sorted order: Finalize(1) > Unlock(2) > Lock(3) > Local(4)
	expectedOrder := []string{"finalize-1", "finalize-2", "unlock-1", "lock-1", "local-1", "local-2"}

	if len(sorted) != len(expectedOrder) {
		t.Fatalf("Expected %d transactions, got %d", len(expectedOrder), len(sorted))
	}

	for i, expected := range expectedOrder {
		if sorted[i].ID != expected {
			t.Errorf("Position %d: expected %s, got %s", i, expected, sorted[i].ID)
		}
	}

	// Verify original slice is unchanged
	if txs[0].ID != "local-1" {
		t.Error("Original slice should not be modified")
	}
}

func TestChain_SortTransactionsByPriority_StableSort(t *testing.T) {
	chain := NewChain(0)

	// Create multiple transactions of the same type
	txs := []protocol.Transaction{
		{ID: "local-A", TxType: protocol.TxTypeLocal},
		{ID: "local-B", TxType: protocol.TxTypeLocal},
		{ID: "local-C", TxType: protocol.TxTypeLocal},
		{ID: "unlock-1", TxType: protocol.TxTypeUnlock},
	}

	sorted := chain.sortTransactionsByPriority(txs)

	// Unlock should come first, but local txs should maintain relative order
	if sorted[0].ID != "unlock-1" {
		t.Errorf("First should be unlock, got %s", sorted[0].ID)
	}
	if sorted[1].ID != "local-A" {
		t.Errorf("Second should be local-A, got %s", sorted[1].ID)
	}
	if sorted[2].ID != "local-B" {
		t.Errorf("Third should be local-B, got %s", sorted[2].ID)
	}
	if sorted[3].ID != "local-C" {
		t.Errorf("Fourth should be local-C, got %s", sorted[3].ID)
	}
}

func TestChain_ProduceBlock_TransactionOrdering(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Add transactions in wrong order (they should be sorted during block production)
	chain.AddTx(protocol.Transaction{ID: "local-1", TxType: protocol.TxTypeLocal})
	chain.AddTx(protocol.Transaction{ID: "finalize-1", TxType: protocol.TxTypeCrossDebit, IsCrossShard: true, CrossShardTxID: "ctx-1"})
	chain.AddTx(protocol.Transaction{ID: "unlock-1", TxType: protocol.TxTypeUnlock, IsCrossShard: true, CrossShardTxID: "ctx-2"})
	chain.AddTx(protocol.Transaction{ID: "lock-1", TxType: protocol.TxTypeLock, IsCrossShard: true, CrossShardTxID: "ctx-3"})

	// Lock funds for finalize tx to execute properly
	chain.LockFunds("ctx-1", common.HexToAddress("0x1234"), big.NewInt(100))

	block, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	// Verify ordering in produced block
	if len(block.TxOrdering) != 4 {
		t.Fatalf("Expected 4 transactions, got %d", len(block.TxOrdering))
	}

	// Should be: finalize-1, unlock-1, lock-1, local-1
	expectedOrder := []string{"finalize-1", "unlock-1", "lock-1", "local-1"}
	for i, expected := range expectedOrder {
		if block.TxOrdering[i].ID != expected {
			t.Errorf("Position %d: expected %s, got %s", i, expected, block.TxOrdering[i].ID)
		}
	}
}

func TestChain_UnlockTransactionCleanup(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	txID := "tx-unlock-test"
	addr := common.HexToAddress("0x1234")

	// Set up all the state that unlock should clear
	chain.LockFunds(txID, addr, big.NewInt(100))
	chain.StorePendingCredit(txID, addr, big.NewInt(50))
	chain.StorePendingCall(&protocol.CrossShardTx{ID: txID, RwSet: []protocol.RwVariable{{Address: addr}}})
	chain.LockAddress(txID, addr, big.NewInt(100), 1, nil, common.Hash{}, nil)

	// Queue unlock transaction
	chain.AddTx(protocol.Transaction{
		ID:             "unlock-tx-1",
		TxType:         protocol.TxTypeUnlock,
		CrossShardTxID: txID,
		IsCrossShard:   true,
	})

	// Produce block (executes unlock)
	_, err = chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	// Verify all state is cleared
	if _, ok := chain.GetLockedFunds(txID); ok {
		t.Error("Locked funds should be cleared")
	}
	if _, ok := chain.GetPendingCredits(txID); ok {
		t.Error("Pending credits should be cleared")
	}
	if _, ok := chain.GetPendingCall(txID); ok {
		t.Error("Pending call should be cleared")
	}
	if _, ok := chain.GetSimulationLocks(txID); ok {
		t.Error("Simulation locks should be cleared")
	}
	if chain.IsAddressLocked(addr) {
		t.Error("Address should be unlocked")
	}
}

func TestChain_GetAddressLockHolder(t *testing.T) {
	chain := NewChain(0)
	addr := common.HexToAddress("0x1234")
	txID := "tx-holder-test"

	// Initially no holder
	holder := chain.GetAddressLockHolder(addr)
	if holder != "" {
		t.Errorf("Expected empty holder, got %s", holder)
	}

	// Lock address
	err := chain.LockAddress(txID, addr, big.NewInt(100), 1, nil, common.Hash{}, nil)
	if err != nil {
		t.Fatalf("LockAddress failed: %v", err)
	}

	// Verify holder
	holder = chain.GetAddressLockHolder(addr)
	if holder != txID {
		t.Errorf("Expected holder %s, got %s", txID, holder)
	}

	// Unlock
	chain.UnlockAllForTx(txID)

	// Holder should be empty again
	holder = chain.GetAddressLockHolder(addr)
	if holder != "" {
		t.Errorf("Expected empty holder after unlock, got %s", holder)
	}
}

func TestChain_LockTransaction_WithRwSet(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Set up some state in EVM
	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")
	value := common.HexToHash("0x100")
	evmState.stateDB.SetState(addr, slot, value)

	// Create lock transaction with matching RwSet
	chain.AddTx(protocol.Transaction{
		ID:             "lock-tx-1",
		TxType:         protocol.TxTypeLock,
		CrossShardTxID: "ctx-1",
		IsCrossShard:   true,
		RwSet: []protocol.RwVariable{{
			Address: addr,
			ReadSet: []protocol.ReadSetItem{{
				Slot:  protocol.Slot(slot),
				Value: value.Bytes(),
			}},
		}},
	})

	// Should succeed - ReadSet matches current state
	_, err = chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock should succeed with matching RwSet: %v", err)
	}
}

func TestChain_LockTransaction_ReadSetMismatch(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// Set up some state in EVM
	addr := common.HexToAddress("0x1234")
	slot := common.HexToHash("0x01")
	actualValue := common.HexToHash("0x100")
	evmState.stateDB.SetState(addr, slot, actualValue)

	// Simulate locks acquired during prepare phase (before TxTypeLock is executed)
	txID := "ctx-1"
	chain.LockFunds(txID, addr, big.NewInt(100))
	chain.LockAddress(txID, addr, big.NewInt(100), 1, nil, common.Hash{}, nil)

	// Verify locks are held before block production
	if !chain.IsAddressLocked(addr) {
		t.Fatal("Address should be locked before block production")
	}

	// Create lock transaction with mismatched RwSet (expects different value)
	expectedValue := common.HexToHash("0x999") // Different from actual
	chain.AddTx(protocol.Transaction{
		ID:             "lock-tx-1",
		TxType:         protocol.TxTypeLock,
		CrossShardTxID: txID,
		IsCrossShard:   true,
		RwSet: []protocol.RwVariable{{
			Address: addr,
			ReadSet: []protocol.ReadSetItem{{
				Slot:  protocol.Slot(slot),
				Value: expectedValue.Bytes(),
			}},
		}},
	})

	// ProduceBlock should still succeed even though lock tx fails validation
	block, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock should not fail: %v", err)
	}

	// Failed transactions are NOT included in TxOrdering
	if len(block.TxOrdering) != 0 {
		t.Fatalf("Expected 0 txs in ordering (failed tx excluded), got %d", len(block.TxOrdering))
	}

	// Verify locks are RELEASED after failure (bug fix)
	if chain.IsAddressLocked(addr) {
		t.Error("Address should be unlocked after lock validation failure")
	}
	if _, ok := chain.GetLockedFunds(txID); ok {
		t.Error("Locked funds should be cleared after lock validation failure")
	}

	// Verify a NO vote is recorded for the failed transaction
	if vote, exists := block.TpcPrepare[txID]; !exists {
		t.Error("Expected a vote for the failed transaction")
	} else if vote {
		t.Error("Expected NO vote for failed lock transaction, got YES")
	}
}

func TestChain_MixedTransactionTypes_FullFlow(t *testing.T) {
	chain := NewChain(0)
	evmState, err := NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	// This test focuses on transaction ORDERING within a block.
	// We use transactions that will succeed to verify the ordering.

	// Add transactions that will succeed:
	// - Unlock: metadata-only, always succeeds
	// - Lock with empty RwSet: always succeeds
	chain.AddTx(protocol.Transaction{
		ID:             "unlock-1",
		TxType:         protocol.TxTypeUnlock,
		CrossShardTxID: "completed-tx",
		IsCrossShard:   true,
	})
	chain.AddTx(protocol.Transaction{
		ID:             "lock-1",
		TxType:         protocol.TxTypeLock,
		CrossShardTxID: "new-tx",
		IsCrossShard:   true,
		RwSet:          []protocol.RwVariable{}, // Empty = no validation needed
	})

	block, err := chain.ProduceBlock(evmState)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}

	// Verify correct ordering: unlock(2) before lock(3)
	if len(block.TxOrdering) != 2 {
		t.Fatalf("Expected 2 txs, got %d", len(block.TxOrdering))
	}

	// Priority order: unlock(2) > lock(3)
	if block.TxOrdering[0].TxType != protocol.TxTypeUnlock {
		t.Errorf("First tx should be unlock, got %s", block.TxOrdering[0].TxType)
	}
	if block.TxOrdering[1].TxType != protocol.TxTypeLock {
		t.Errorf("Second tx should be lock, got %s", block.TxOrdering[1].TxType)
	}
}

func TestChain_EmptyTransactionList_Sorting(t *testing.T) {
	chain := NewChain(0)

	sorted := chain.sortTransactionsByPriority([]protocol.Transaction{})
	if len(sorted) != 0 {
		t.Error("Sorting empty list should return empty list")
	}
}

func TestChain_SingleTransaction_Sorting(t *testing.T) {
	chain := NewChain(0)

	txs := []protocol.Transaction{{ID: "single", TxType: protocol.TxTypeLock}}
	sorted := chain.sortTransactionsByPriority(txs)

	if len(sorted) != 1 || sorted[0].ID != "single" {
		t.Error("Single transaction should remain unchanged")
	}
}
