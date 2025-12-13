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
