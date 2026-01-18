package shard

import (
	"log"
	"sync"

	"github.com/sharding-experiment/sharding/internal/protocol"
)

// BlockBuffer handles out-of-order orchestrator block delivery by buffering
// blocks that arrive ahead of sequence and releasing them when gaps are filled.
type BlockBuffer struct {
	mu        sync.Mutex
	expected  uint64                                    // Next expected block height
	buffered  map[uint64]*protocol.OrchestratorShardBlock // Out-of-order blocks waiting to be processed
	maxBuffer int                                       // Maximum number of blocks to buffer
	shardID   int                                       // For logging
}

// NewBlockBuffer creates a new BlockBuffer.
// startHeight is the first block height expected (typically 1, after genesis).
// maxBuffer limits memory usage by capping buffered blocks.
func NewBlockBuffer(shardID int, startHeight uint64, maxBuffer int) *BlockBuffer {
	return &BlockBuffer{
		expected:  startHeight,
		buffered:  make(map[uint64]*protocol.OrchestratorShardBlock),
		maxBuffer: maxBuffer,
		shardID:   shardID,
	}
}

// ProcessBlock handles an incoming orchestrator block and returns blocks ready
// to be processed in order. Returns nil if the block was buffered or ignored.
//
// Behavior:
// - If block.Height == expected: process immediately, then drain any buffered successors
// - If block.Height > expected: buffer for later (if within maxBuffer limit)
// - If block.Height < expected: ignore (duplicate or already processed)
func (b *BlockBuffer) ProcessBlock(block *protocol.OrchestratorShardBlock) []*protocol.OrchestratorShardBlock {
	b.mu.Lock()
	defer b.mu.Unlock()

	if block.Height == b.expected {
		// Block is next in sequence - process it and any buffered successors
		result := []*protocol.OrchestratorShardBlock{block}
		b.expected++

		// Drain buffered blocks that are now in sequence
		for {
			next, ok := b.buffered[b.expected]
			if !ok {
				break
			}
			result = append(result, next)
			delete(b.buffered, b.expected)
			b.expected++
			log.Printf("Shard %d: BlockBuffer released buffered block %d", b.shardID, b.expected-1)
		}

		return result
	}

	if block.Height > b.expected {
		// Block arrived out of order - buffer it
		gap := block.Height - b.expected

		// Check buffer limit to prevent memory exhaustion
		if len(b.buffered) >= b.maxBuffer {
			log.Printf("Shard %d: BlockBuffer full (%d blocks), dropping block %d (expected %d)",
				b.shardID, len(b.buffered), block.Height, b.expected)
			return nil
		}

		// Don't buffer if already have this block
		if _, exists := b.buffered[block.Height]; exists {
			log.Printf("Shard %d: BlockBuffer already has block %d buffered", b.shardID, block.Height)
			return nil
		}

		b.buffered[block.Height] = block
		log.Printf("Shard %d: BlockBuffer buffered block %d (expected %d, gap=%d, buffered=%d)",
			b.shardID, block.Height, b.expected, gap, len(b.buffered))
		return nil
	}

	// block.Height < b.expected - old/duplicate block
	log.Printf("Shard %d: BlockBuffer ignoring old block %d (expected %d)",
		b.shardID, block.Height, b.expected)
	return nil
}

// GetExpected returns the next expected block height.
func (b *BlockBuffer) GetExpected() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.expected
}

// GetBufferedCount returns the number of currently buffered blocks.
func (b *BlockBuffer) GetBufferedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buffered)
}

// SetExpected sets the expected block height (used during recovery).
func (b *BlockBuffer) SetExpected(height uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.expected = height
}

// GetBufferedHeights returns the heights of all currently buffered blocks (for debugging).
func (b *BlockBuffer) GetBufferedHeights() []uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	heights := make([]uint64, 0, len(b.buffered))
	for h := range b.buffered {
		heights = append(heights, h)
	}
	return heights
}
