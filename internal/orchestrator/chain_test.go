package orchestrator

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

func TestNewOrchestratorChain(t *testing.T) {
	chain := NewOrchestratorChain()

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

func TestOrchestratorChain_AddTransaction(t *testing.T) {
	chain := NewOrchestratorChain()

	tx := protocol.CrossShardTx{
		ID:        "tx-1",
		FromShard: 0,
		From:      common.HexToAddress("0x1234"),
		Value:     protocol.NewBigInt(big.NewInt(100)),
		RwSet: []protocol.RwVariable{
			{Address: common.HexToAddress("0x5678"), ReferenceBlock: protocol.Reference{ShardNum: 1}},
		},
	}

	chain.AddTransaction(tx)

	if len(chain.pendingTxs) != 1 {
		t.Errorf("Expected 1 pending tx, got %d", len(chain.pendingTxs))
	}
	if chain.pendingTxs[0].ID != "tx-1" {
		t.Errorf("Expected tx ID 'tx-1', got %s", chain.pendingTxs[0].ID)
	}
}

func TestOrchestratorChain_ProduceBlock(t *testing.T) {
	chain := NewOrchestratorChain()

	// Add a transaction
	tx := protocol.CrossShardTx{
		ID:        "tx-1",
		FromShard: 0,
		From:      common.HexToAddress("0x1234"),
		Value:     protocol.NewBigInt(big.NewInt(100)),
	}
	chain.AddTransaction(tx)

	// Produce block
	block := chain.ProduceBlock()

	// Verify block
	if block.Height != 1 {
		t.Errorf("Expected block height 1, got %d", block.Height)
	}
	if len(block.CtToOrder) != 1 {
		t.Errorf("Expected 1 tx in CtToOrder, got %d", len(block.CtToOrder))
	}
	if block.CtToOrder[0].ID != "tx-1" {
		t.Errorf("Expected tx ID 'tx-1' in block, got %s", block.CtToOrder[0].ID)
	}

	// Verify chain state
	if chain.height != 1 {
		t.Errorf("Expected chain height 1, got %d", chain.height)
	}
	if len(chain.pendingTxs) != 0 {
		t.Errorf("Expected 0 pending txs after block, got %d", len(chain.pendingTxs))
	}
	if len(chain.awaitingVotes) != 1 {
		t.Errorf("Expected 1 tx awaiting votes, got %d", len(chain.awaitingVotes))
	}
}

func TestOrchestratorChain_RecordVote(t *testing.T) {
	chain := NewOrchestratorChain()

	// Add tx and produce block (FromShard: 0, only involved shard is 0)
	tx := protocol.CrossShardTx{ID: "tx-1", FromShard: 0, Value: protocol.NewBigInt(big.NewInt(100))}
	chain.AddTransaction(tx)
	chain.ProduceBlock()

	// Record vote for non-existent tx
	if chain.RecordVote("tx-999", 0, true) {
		t.Error("Expected false for non-existent tx")
	}

	// Record vote for existing tx from shard 0
	if !chain.RecordVote("tx-1", 0, true) {
		t.Error("Expected true for existing tx")
	}

	// Verify state
	if len(chain.awaitingVotes) != 0 {
		t.Errorf("Expected 0 awaiting votes after vote, got %d", len(chain.awaitingVotes))
	}
	if len(chain.pendingResult) != 1 {
		t.Errorf("Expected 1 pending result, got %d", len(chain.pendingResult))
	}
	if !chain.pendingResult["tx-1"] {
		t.Error("Expected pending result to be true")
	}
}

func TestOrchestratorChain_VoteInNextBlock(t *testing.T) {
	chain := NewOrchestratorChain()

	// Add tx and produce block 1 (FromShard: 0)
	tx := protocol.CrossShardTx{ID: "tx-1", FromShard: 0, Value: protocol.NewBigInt(big.NewInt(100))}
	chain.AddTransaction(tx)
	chain.ProduceBlock()

	// Record vote from shard 0
	chain.RecordVote("tx-1", 0, true)

	// Produce block 2 - should contain TpcResult
	block2 := chain.ProduceBlock()

	if block2.Height != 2 {
		t.Errorf("Expected block height 2, got %d", block2.Height)
	}
	if len(block2.TpcResult) != 1 {
		t.Errorf("Expected 1 TpcResult entry, got %d", len(block2.TpcResult))
	}
	if !block2.TpcResult["tx-1"] {
		t.Error("Expected TpcResult[tx-1] to be true")
	}

	// pendingResult should be cleared
	if len(chain.pendingResult) != 0 {
		t.Errorf("Expected 0 pending results after block, got %d", len(chain.pendingResult))
	}
}

func TestOrchestratorChain_AbortVote(t *testing.T) {
	chain := NewOrchestratorChain()

	// Add tx and produce block (FromShard: 0)
	tx := protocol.CrossShardTx{ID: "tx-1", FromShard: 0, Value: protocol.NewBigInt(big.NewInt(100))}
	chain.AddTransaction(tx)
	chain.ProduceBlock()

	// Record abort vote from shard 0
	chain.RecordVote("tx-1", 0, false)

	// Produce next block
	block := chain.ProduceBlock()

	if block.TpcResult["tx-1"] {
		t.Error("Expected TpcResult[tx-1] to be false (aborted)")
	}
}

func TestOrchestratorChain_GetAwaitingTx(t *testing.T) {
	chain := NewOrchestratorChain()

	// Initially no awaiting tx
	_, ok := chain.GetAwaitingTx("tx-1")
	if ok {
		t.Error("Expected no awaiting tx initially")
	}

	// Add tx and produce block
	tx := protocol.CrossShardTx{ID: "tx-1", FromShard: 0, Value: protocol.NewBigInt(big.NewInt(100))}
	chain.AddTransaction(tx)
	chain.ProduceBlock()

	// Now should find awaiting tx
	awaitingTx, ok := chain.GetAwaitingTx("tx-1")
	if !ok {
		t.Error("Expected to find awaiting tx")
	}
	if awaitingTx.ID != "tx-1" {
		t.Errorf("Expected tx ID 'tx-1', got %s", awaitingTx.ID)
	}
}

func TestOrchestratorChain_MultipleTransactions(t *testing.T) {
	chain := NewOrchestratorChain()

	// Add multiple transactions
	for i := 0; i < 5; i++ {
		tx := protocol.CrossShardTx{
			ID:        "tx-" + string(rune('A'+i)),
			FromShard: i % 3,
			Value:     protocol.NewBigInt(big.NewInt(int64(100 * (i + 1)))),
		}
		chain.AddTransaction(tx)
	}

	// Produce block
	block := chain.ProduceBlock()

	if len(block.CtToOrder) != 5 {
		t.Errorf("Expected 5 txs in block, got %d", len(block.CtToOrder))
	}
	if len(chain.awaitingVotes) != 5 {
		t.Errorf("Expected 5 awaiting votes, got %d", len(chain.awaitingVotes))
	}
}

func TestOrchestratorChain_BlockLinking(t *testing.T) {
	chain := NewOrchestratorChain()

	// Get genesis hash
	genesisHash := chain.blocks[0].Hash()

	// Produce block 1
	block1 := chain.ProduceBlock()
	if block1.PrevHash != genesisHash {
		t.Error("Block 1 should link to genesis")
	}

	// Produce block 2
	block1Hash := block1.Hash()
	block2 := chain.ProduceBlock()
	if block2.PrevHash != block1Hash {
		t.Error("Block 2 should link to block 1")
	}
}

// ===== Crash Recovery Endpoint Tests =====

func TestOrchestratorChain_GetHeight(t *testing.T) {
	chain := NewOrchestratorChain()

	// Initial height should be 0 (genesis)
	if height := chain.GetHeight(); height != 0 {
		t.Errorf("Expected initial height 0, got %d", height)
	}

	// Produce blocks and verify height increases
	chain.ProduceBlock()
	if height := chain.GetHeight(); height != 1 {
		t.Errorf("Expected height 1, got %d", height)
	}

	chain.ProduceBlock()
	if height := chain.GetHeight(); height != 2 {
		t.Errorf("Expected height 2, got %d", height)
	}

	chain.ProduceBlock()
	if height := chain.GetHeight(); height != 3 {
		t.Errorf("Expected height 3, got %d", height)
	}
}

func TestOrchestratorChain_GetBlock(t *testing.T) {
	chain := NewOrchestratorChain()

	// Genesis block should be at height 0
	genesis := chain.GetBlock(0)
	if genesis == nil {
		t.Fatal("Genesis block should exist at height 0")
	}
	if genesis.Height != 0 {
		t.Errorf("Genesis block should have height 0, got %d", genesis.Height)
	}

	// Produce blocks
	block1 := chain.ProduceBlock()
	block2 := chain.ProduceBlock()

	// Verify GetBlock returns correct blocks
	fetched1 := chain.GetBlock(1)
	if fetched1 == nil {
		t.Fatal("Block 1 should exist")
	}
	if fetched1.Height != block1.Height {
		t.Errorf("Fetched block 1 height mismatch: %d vs %d", fetched1.Height, block1.Height)
	}

	fetched2 := chain.GetBlock(2)
	if fetched2 == nil {
		t.Fatal("Block 2 should exist")
	}
	if fetched2.Height != block2.Height {
		t.Errorf("Fetched block 2 height mismatch: %d vs %d", fetched2.Height, block2.Height)
	}
}

func TestOrchestratorChain_GetBlock_NotFound(t *testing.T) {
	chain := NewOrchestratorChain()

	// Block at height 1 shouldn't exist yet
	block := chain.GetBlock(1)
	if block != nil {
		t.Error("Block 1 should not exist before producing")
	}

	// Block at very high height shouldn't exist
	block = chain.GetBlock(1000)
	if block != nil {
		t.Error("Block 1000 should not exist")
	}

	// Produce a block and verify height 1 now exists but 2 doesn't
	chain.ProduceBlock()

	if chain.GetBlock(1) == nil {
		t.Error("Block 1 should exist after producing")
	}
	if chain.GetBlock(2) != nil {
		t.Error("Block 2 should not exist yet")
	}
}

func TestOrchestratorChain_GetBlock_WithContent(t *testing.T) {
	chain := NewOrchestratorChain()

	// Add a transaction before producing block
	tx := protocol.CrossShardTx{
		ID:        "ctx-fetch-test",
		FromShard: 0,
		From:      common.HexToAddress("0x1234"),
		Value:     protocol.NewBigInt(big.NewInt(100)),
		RwSet: []protocol.RwVariable{
			{Address: common.HexToAddress("0x5678"), ReferenceBlock: protocol.Reference{ShardNum: 1}},
		},
	}
	chain.AddTransaction(tx)

	// Produce block
	block := chain.ProduceBlock()

	// Fetch and verify content
	fetched := chain.GetBlock(1)
	if fetched == nil {
		t.Fatal("Block should exist")
	}
	if len(fetched.CtToOrder) != 1 {
		t.Errorf("Expected 1 tx in block, got %d", len(fetched.CtToOrder))
	}
	if fetched.CtToOrder[0].ID != "ctx-fetch-test" {
		t.Errorf("Expected tx ID 'ctx-fetch-test', got %s", fetched.CtToOrder[0].ID)
	}

	// Verify we got the same block
	if fetched.Height != block.Height {
		t.Error("Fetched block should match produced block")
	}
}
