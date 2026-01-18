package shard

import (
	"testing"

	"github.com/sharding-experiment/sharding/internal/protocol"
)

func makeTestOrchestratorBlock(height uint64) *protocol.OrchestratorShardBlock {
	return &protocol.OrchestratorShardBlock{
		Height:    height,
		TpcResult: map[string]bool{},
		CtToOrder: []protocol.CrossShardTx{},
	}
}

func TestBlockBuffer_InOrderDelivery(t *testing.T) {
	buf := NewBlockBuffer(0, 1, 100)

	// Process blocks in order
	for i := uint64(1); i <= 5; i++ {
		block := makeTestOrchestratorBlock(i)
		result := buf.ProcessBlock(block)

		if len(result) != 1 {
			t.Errorf("Block %d: expected 1 block, got %d", i, len(result))
		}
		if result[0].Height != i {
			t.Errorf("Block %d: expected height %d, got %d", i, i, result[0].Height)
		}
	}

	if buf.GetExpected() != 6 {
		t.Errorf("Expected next height 6, got %d", buf.GetExpected())
	}
	if buf.GetBufferedCount() != 0 {
		t.Errorf("Expected 0 buffered blocks, got %d", buf.GetBufferedCount())
	}
}

func TestBlockBuffer_OutOfOrderDelivery(t *testing.T) {
	buf := NewBlockBuffer(0, 1, 100)

	// Receive block 3 first (out of order)
	block3 := makeTestOrchestratorBlock(3)
	result := buf.ProcessBlock(block3)
	if result != nil {
		t.Errorf("Block 3 should be buffered, got %d blocks", len(result))
	}
	if buf.GetBufferedCount() != 1 {
		t.Errorf("Expected 1 buffered block, got %d", buf.GetBufferedCount())
	}

	// Receive block 2 (still out of order)
	block2 := makeTestOrchestratorBlock(2)
	result = buf.ProcessBlock(block2)
	if result != nil {
		t.Errorf("Block 2 should be buffered, got %d blocks", len(result))
	}
	if buf.GetBufferedCount() != 2 {
		t.Errorf("Expected 2 buffered blocks, got %d", buf.GetBufferedCount())
	}

	// Receive block 1 (fills the gap)
	block1 := makeTestOrchestratorBlock(1)
	result = buf.ProcessBlock(block1)
	if len(result) != 3 {
		t.Errorf("Expected 3 blocks (1, 2, 3), got %d", len(result))
	}

	// Verify order
	for i, b := range result {
		expectedHeight := uint64(i + 1)
		if b.Height != expectedHeight {
			t.Errorf("Block %d: expected height %d, got %d", i, expectedHeight, b.Height)
		}
	}

	if buf.GetExpected() != 4 {
		t.Errorf("Expected next height 4, got %d", buf.GetExpected())
	}
	if buf.GetBufferedCount() != 0 {
		t.Errorf("Expected 0 buffered blocks after drain, got %d", buf.GetBufferedCount())
	}
}

func TestBlockBuffer_DuplicateBlock(t *testing.T) {
	buf := NewBlockBuffer(0, 1, 100)

	// Process block 1
	block1 := makeTestOrchestratorBlock(1)
	result := buf.ProcessBlock(block1)
	if len(result) != 1 {
		t.Errorf("Expected 1 block, got %d", len(result))
	}

	// Try to process block 1 again (duplicate)
	result = buf.ProcessBlock(block1)
	if result != nil {
		t.Errorf("Duplicate block should be ignored, got %d blocks", len(result))
	}

	if buf.GetExpected() != 2 {
		t.Errorf("Expected next height 2, got %d", buf.GetExpected())
	}
}

func TestBlockBuffer_OldBlock(t *testing.T) {
	buf := NewBlockBuffer(0, 5, 100) // Start expecting block 5

	// Receive old block 3
	block3 := makeTestOrchestratorBlock(3)
	result := buf.ProcessBlock(block3)
	if result != nil {
		t.Errorf("Old block should be ignored, got %d blocks", len(result))
	}

	if buf.GetExpected() != 5 {
		t.Errorf("Expected should still be 5, got %d", buf.GetExpected())
	}
}

func TestBlockBuffer_BufferLimit(t *testing.T) {
	buf := NewBlockBuffer(0, 1, 3) // Only buffer 3 blocks

	// Buffer blocks 2, 3, 4
	buf.ProcessBlock(makeTestOrchestratorBlock(2))
	buf.ProcessBlock(makeTestOrchestratorBlock(3))
	buf.ProcessBlock(makeTestOrchestratorBlock(4))

	if buf.GetBufferedCount() != 3 {
		t.Errorf("Expected 3 buffered blocks, got %d", buf.GetBufferedCount())
	}

	// Try to buffer block 5 (should be dropped due to limit)
	result := buf.ProcessBlock(makeTestOrchestratorBlock(5))
	if result != nil {
		t.Errorf("Block should be dropped due to buffer limit, got %d blocks", len(result))
	}
	if buf.GetBufferedCount() != 3 {
		t.Errorf("Buffer count should still be 3, got %d", buf.GetBufferedCount())
	}
}

func TestBlockBuffer_DuplicateBufferedBlock(t *testing.T) {
	buf := NewBlockBuffer(0, 1, 100)

	// Buffer block 3
	buf.ProcessBlock(makeTestOrchestratorBlock(3))
	if buf.GetBufferedCount() != 1 {
		t.Errorf("Expected 1 buffered block, got %d", buf.GetBufferedCount())
	}

	// Try to buffer block 3 again
	result := buf.ProcessBlock(makeTestOrchestratorBlock(3))
	if result != nil {
		t.Errorf("Duplicate buffered block should be ignored")
	}
	if buf.GetBufferedCount() != 1 {
		t.Errorf("Buffer count should still be 1, got %d", buf.GetBufferedCount())
	}
}

func TestBlockBuffer_PartialDrain(t *testing.T) {
	buf := NewBlockBuffer(0, 1, 100)

	// Buffer blocks 2, 3, 5 (gap at 4)
	buf.ProcessBlock(makeTestOrchestratorBlock(2))
	buf.ProcessBlock(makeTestOrchestratorBlock(3))
	buf.ProcessBlock(makeTestOrchestratorBlock(5))

	// Receive block 1 - should drain 1, 2, 3 but not 5 (gap at 4)
	result := buf.ProcessBlock(makeTestOrchestratorBlock(1))
	if len(result) != 3 {
		t.Errorf("Expected 3 blocks (1, 2, 3), got %d", len(result))
	}

	if buf.GetExpected() != 4 {
		t.Errorf("Expected next height 4, got %d", buf.GetExpected())
	}
	if buf.GetBufferedCount() != 1 {
		t.Errorf("Expected 1 buffered block (block 5), got %d", buf.GetBufferedCount())
	}

	// Now receive block 4 - should drain 4, 5
	result = buf.ProcessBlock(makeTestOrchestratorBlock(4))
	if len(result) != 2 {
		t.Errorf("Expected 2 blocks (4, 5), got %d", len(result))
	}

	if buf.GetExpected() != 6 {
		t.Errorf("Expected next height 6, got %d", buf.GetExpected())
	}
	if buf.GetBufferedCount() != 0 {
		t.Errorf("Expected 0 buffered blocks, got %d", buf.GetBufferedCount())
	}
}

func TestBlockBuffer_SetExpected(t *testing.T) {
	buf := NewBlockBuffer(0, 1, 100)

	buf.SetExpected(10)
	if buf.GetExpected() != 10 {
		t.Errorf("Expected 10, got %d", buf.GetExpected())
	}

	// Block 9 should now be ignored (old)
	result := buf.ProcessBlock(makeTestOrchestratorBlock(9))
	if result != nil {
		t.Errorf("Old block should be ignored")
	}

	// Block 10 should be processed
	result = buf.ProcessBlock(makeTestOrchestratorBlock(10))
	if len(result) != 1 {
		t.Errorf("Expected 1 block, got %d", len(result))
	}
}

func TestBlockBuffer_GetBufferedHeights(t *testing.T) {
	buf := NewBlockBuffer(0, 1, 100)

	buf.ProcessBlock(makeTestOrchestratorBlock(3))
	buf.ProcessBlock(makeTestOrchestratorBlock(5))
	buf.ProcessBlock(makeTestOrchestratorBlock(7))

	heights := buf.GetBufferedHeights()
	if len(heights) != 3 {
		t.Errorf("Expected 3 heights, got %d", len(heights))
	}

	// Check all heights are present (order may vary)
	heightMap := make(map[uint64]bool)
	for _, h := range heights {
		heightMap[h] = true
	}
	for _, expected := range []uint64{3, 5, 7} {
		if !heightMap[expected] {
			t.Errorf("Expected height %d in buffered heights", expected)
		}
	}
}
