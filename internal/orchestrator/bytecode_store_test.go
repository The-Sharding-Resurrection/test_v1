package orchestrator

import (
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestBytecodeStore_InMemory(t *testing.T) {
	// Create in-memory store (empty path)
	store, err := NewBytecodeStore("")
	if err != nil {
		t.Fatalf("Failed to create in-memory store: %v", err)
	}
	defer store.Close()

	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	code := []byte{0x60, 0x80, 0x60, 0x40} // Sample bytecode

	// Initially should not exist
	if store.Has(addr) {
		t.Error("Expected Has() to return false for non-existent address")
	}
	if got := store.Get(addr); got != nil {
		t.Errorf("Expected Get() to return nil, got %v", got)
	}

	// Store bytecode
	if err := store.Put(addr, code); err != nil {
		t.Fatalf("Put() failed: %v", err)
	}

	// Now should exist
	if !store.Has(addr) {
		t.Error("Expected Has() to return true after Put()")
	}

	// Get should return the bytecode
	got := store.Get(addr)
	if got == nil {
		t.Fatal("Get() returned nil after Put()")
	}
	if len(got) != len(code) {
		t.Errorf("Expected bytecode length %d, got %d", len(code), len(got))
	}
	for i := range code {
		if got[i] != code[i] {
			t.Errorf("Bytecode mismatch at index %d: expected %x, got %x", i, code[i], got[i])
		}
	}
}

func TestBytecodeStore_Persistent(t *testing.T) {
	// Create temporary directory for test
	tmpDir, err := os.MkdirTemp("", "bytecode_store_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "bytecode_db")
	addr := common.HexToAddress("0xabcdef0123456789abcdef0123456789abcdef01")
	code := []byte{0x60, 0x80, 0x60, 0x40, 0x52, 0x34}

	// Create store, put bytecode, close
	{
		store, err := NewBytecodeStore(dbPath)
		if err != nil {
			t.Fatalf("Failed to create persistent store: %v", err)
		}

		if err := store.Put(addr, code); err != nil {
			t.Fatalf("Put() failed: %v", err)
		}

		if err := store.Close(); err != nil {
			t.Fatalf("Close() failed: %v", err)
		}
	}

	// Reopen and verify data persisted
	{
		store, err := NewBytecodeStore(dbPath)
		if err != nil {
			t.Fatalf("Failed to reopen store: %v", err)
		}
		defer store.Close()

		if !store.Has(addr) {
			t.Error("Data should persist after reopening")
		}

		got := store.Get(addr)
		if got == nil {
			t.Fatal("Get() returned nil after reopen")
		}
		if len(got) != len(code) {
			t.Errorf("Expected bytecode length %d, got %d", len(code), len(got))
		}
	}
}

func TestBytecodeStore_EmptyCode(t *testing.T) {
	store, err := NewBytecodeStore("")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")

	// Putting empty code should be a no-op
	if err := store.Put(addr, nil); err != nil {
		t.Fatalf("Put(nil) should not fail: %v", err)
	}
	if err := store.Put(addr, []byte{}); err != nil {
		t.Fatalf("Put([]) should not fail: %v", err)
	}

	// Should not be stored
	if store.Has(addr) {
		t.Error("Empty bytecode should not be stored")
	}
}

func TestBytecodeStore_GetReturnsCopy(t *testing.T) {
	store, err := NewBytecodeStore("")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	addr := common.HexToAddress("0x2222222222222222222222222222222222222222")
	code := []byte{0x01, 0x02, 0x03}

	if err := store.Put(addr, code); err != nil {
		t.Fatalf("Put() failed: %v", err)
	}

	// Get a copy and modify it
	got := store.Get(addr)
	got[0] = 0xFF

	// Original should be unchanged
	got2 := store.Get(addr)
	if got2[0] == 0xFF {
		t.Error("Modifying returned slice affected stored data - Get() should return a copy")
	}
}

func TestBytecodeStore_ConcurrentAccess(t *testing.T) {
	store, err := NewBytecodeStore("")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	var wg sync.WaitGroup
	numWorkers := 10
	numOps := 100

	// Concurrent writes and reads
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				addr := common.BigToAddress(big.NewInt(int64(workerID*numOps + j)))
				code := []byte{byte(workerID), byte(j)}

				_ = store.Put(addr, code)
				_ = store.Has(addr)
				_ = store.Get(addr)
			}
		}(i)
	}

	wg.Wait()
	// Test passes if no race condition panic occurs
}

func TestBytecodeStore_ClosedStore(t *testing.T) {
	store, err := NewBytecodeStore("")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	addr := common.HexToAddress("0x3333333333333333333333333333333333333333")
	code := []byte{0x01, 0x02}

	// Store data before closing
	if err := store.Put(addr, code); err != nil {
		t.Fatalf("Put() failed: %v", err)
	}

	// Close the store
	if err := store.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	// Operations on closed store should be safe (return nil/false/error)
	if store.Get(addr) != nil {
		t.Error("Get() on closed store should return nil")
	}
	if store.Has(addr) {
		t.Error("Has() on closed store should return false")
	}
	if err := store.Put(addr, code); err == nil {
		t.Error("Put() on closed store should return error")
	}
}
