package shard

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestNewChain(t *testing.T) {
	chain := NewChain()

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
	chain := NewChain()

	chain.AddTx("tx-1", true)
	chain.AddTx("tx-2", false)

	if len(chain.currentTxs) != 2 {
		t.Errorf("Expected 2 txs, got %d", len(chain.currentTxs))
	}
	if chain.currentTxs[0].TxID != "tx-1" || !chain.currentTxs[0].IsCrossShard {
		t.Error("First tx mismatch")
	}
	if chain.currentTxs[1].TxID != "tx-2" || chain.currentTxs[1].IsCrossShard {
		t.Error("Second tx mismatch")
	}
}

func TestChain_AddPrepareResult(t *testing.T) {
	chain := NewChain()

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
	chain := NewChain()
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
	chain := NewChain()
	addr := common.HexToAddress("0x1234")

	chain.LockFunds("tx-1", addr, big.NewInt(1000))
	chain.ClearLock("tx-1")

	_, ok := chain.GetLockedFunds("tx-1")
	if ok {
		t.Error("Lock should be cleared")
	}
}

func TestChain_PendingCredits(t *testing.T) {
	chain := NewChain()
	addr := common.HexToAddress("0x5678")
	amount := big.NewInt(500)

	chain.StorePendingCredit("tx-1", addr, amount)

	credit, ok := chain.GetPendingCredit("tx-1")
	if !ok {
		t.Error("Expected to find pending credit")
	}
	if credit.Address != addr {
		t.Error("Address mismatch")
	}
	if credit.Amount.Cmp(amount) != 0 {
		t.Error("Amount mismatch")
	}

	// Clear and verify
	chain.ClearPendingCredit("tx-1")
	_, ok = chain.GetPendingCredit("tx-1")
	if ok {
		t.Error("Pending credit should be cleared")
	}
}

func TestChain_ProduceBlock(t *testing.T) {
	chain := NewChain()
	stateRoot := common.HexToHash("0xabcd1234")

	// Add some state
	chain.AddTx("tx-1", true)
	chain.AddPrepareResult("tx-1", true)

	block := chain.ProduceBlock(stateRoot)

	if block.Height != 1 {
		t.Errorf("Expected block height 1, got %d", block.Height)
	}
	if block.StateRoot != stateRoot {
		t.Error("StateRoot mismatch")
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
	chain := NewChain()

	genesisHash := chain.blocks[0].Hash()
	block1 := chain.ProduceBlock(common.Hash{})

	if block1.PrevHash != genesisHash {
		t.Error("Block 1 should link to genesis")
	}

	block1Hash := block1.Hash()
	block2 := chain.ProduceBlock(common.Hash{})

	if block2.PrevHash != block1Hash {
		t.Error("Block 2 should link to block 1")
	}
}

func TestChain_MultipleLocks(t *testing.T) {
	chain := NewChain()

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
	chain := NewChain()

	// Simulate multiple rounds
	for round := 0; round < 3; round++ {
		// Add txs for this round
		for i := 0; i < 3; i++ {
			chain.AddTx("tx-"+string(rune('0'+round))+"-"+string(rune('A'+i)), true)
			chain.AddPrepareResult("tx-"+string(rune('0'+round))+"-"+string(rune('A'+i)), i%2 == 0)
		}

		block := chain.ProduceBlock(common.Hash{})
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

func TestChain_GetLockedAmountForAddress(t *testing.T) {
	chain := NewChain()
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
