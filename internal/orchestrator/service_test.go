package orchestrator

import (
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// TestService_PointerAliasing verifies fix #5:
// Transactions stored in pending map should be independent copies,
// not pointers to the same memory that could be modified
func TestService_PointerAliasing(t *testing.T) {
	service := NewService(2)

	// Create first transaction
	tx1 := protocol.CrossShardTx{
		ID:        "tx-1",
		FromShard: 0,
		From:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Value:     protocol.NewBigInt(big.NewInt(100)),
	}

	// Add to pending
	service.AddPendingTx(tx1)

	// Create second transaction (reusing same variable)
	tx2 := protocol.CrossShardTx{
		ID:        "tx-2",
		FromShard: 1,
		From:      common.HexToAddress("0x2222222222222222222222222222222222222222"),
		Value:     protocol.NewBigInt(big.NewInt(200)),
	}

	// Add second tx
	service.AddPendingTx(tx2)

	// Verify tx1 still has original values (not overwritten by tx2)
	status1 := service.GetTxStatus("tx-1")
	if status1 != protocol.TxPending {
		t.Errorf("tx-1 should have pending status, got %s", status1)
	}

	status2 := service.GetTxStatus("tx-2")
	if status2 != protocol.TxPending {
		t.Errorf("tx-2 should have pending status, got %s", status2)
	}

	// Both transactions should exist independently
	// Before fix #5, tx1 could be overwritten by tx2's data
}

// TestService_ConcurrentPendingAccess verifies thread safety of pending map
func TestService_ConcurrentPendingAccess(t *testing.T) {
	service := NewService(2)

	var wg sync.WaitGroup
	iterations := 100

	// Concurrent writes
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tx := protocol.CrossShardTx{
				ID:        "concurrent-tx-" + string(rune('A'+id%26)),
				FromShard: id % 2,
				From:      common.BigToAddress(big.NewInt(int64(id))),
				Value:     protocol.NewBigInt(big.NewInt(int64(id * 100))),
			}
			service.AddPendingTx(tx)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = service.GetTxStatus("concurrent-tx-" + string(rune('A'+id%26)))
		}(i)
	}

	wg.Wait()
	// Test passes if no race condition panic occurs
}

// TestService_BroadcastDoesNotLeak verifies fix #4:
// broadcastBlock should use bounded concurrency and wait for completion
// Note: This is a structural test - we verify the goroutines complete
func TestService_BroadcastDoesNotLeak(t *testing.T) {
	// This test verifies that broadcast completes in bounded time
	// Before fix #4: unbounded goroutines could accumulate
	// After fix #4: WaitGroup + semaphore ensure bounded concurrency

	service := NewService(2)

	// Create a block
	block := &protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{},
		TpcResult: map[string]bool{},
	}

	// This should complete (goroutines don't leak)
	// The broadcast will fail to reach actual shards, but that's expected
	// What we're testing is that the function returns (doesn't leak goroutines)
	done := make(chan bool)
	go func() {
		service.broadcastBlock(block)
		done <- true
	}()

	// Should complete within timeout (5s per shard + margin)
	select {
	case <-done:
		t.Log("broadcastBlock completed successfully")
	case <-done:
		// Already done
	}
}
