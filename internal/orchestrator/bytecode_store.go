package orchestrator

import (
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
)

const (
	// BytecodeStoreCacheMB is the LevelDB block cache size in MB.
	// Small value (16 MB) since bytecode access is infrequent after initial fetch.
	BytecodeStoreCacheMB = 16

	// BytecodeStoreHandles is the maximum number of open file handles for LevelDB.
	// Small value (16) sufficient for low-frequency bytecode lookups.
	BytecodeStoreHandles = 16
)

// BytecodeStore provides persistent storage for contract bytecode.
// Since bytecode is immutable after deployment, it's safe to cache permanently.
type BytecodeStore struct {
	db     ethdb.Database
	mu     sync.RWMutex
	closed bool
}

// NewBytecodeStore creates a new persistent bytecode store.
// If path is empty or storage fails, falls back to in-memory storage.
func NewBytecodeStore(path string) (*BytecodeStore, error) {
	var db ethdb.Database

	if path != "" {
		// Ensure directory exists
		if mkErr := os.MkdirAll(path, 0755); mkErr != nil {
			log.Printf("[BytecodeStore] Failed to create directory %s: %v, using in-memory", path, mkErr)
			db = rawdb.NewMemoryDatabase()
		} else {
			// Try to open LevelDB
			ldb, ldbErr := leveldb.New(path, BytecodeStoreCacheMB, BytecodeStoreHandles, "", false)
			if ldbErr != nil {
				log.Printf("[BytecodeStore] Failed to open LevelDB at %s: %v, using in-memory", path, ldbErr)
				db = rawdb.NewMemoryDatabase()
			} else {
				db = rawdb.NewDatabase(ldb)
				log.Printf("[BytecodeStore] Opened persistent storage at %s", path)
			}
		}
	} else {
		db = rawdb.NewMemoryDatabase()
		log.Printf("[BytecodeStore] Using in-memory storage (no path specified)")
	}

	return &BytecodeStore{
		db:     db,
		closed: false,
	}, nil
}

// codeKey returns the database key for a contract address
func codeKey(addr common.Address) []byte {
	return append([]byte("code:"), addr.Bytes()...)
}

// Get retrieves bytecode for an address, returns nil if not found
func (bs *BytecodeStore) Get(addr common.Address) []byte {
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	if bs.closed {
		return nil
	}

	data, err := bs.db.Get(codeKey(addr))
	if err != nil {
		return nil // Not found or error
	}

	// Return a copy to avoid aliasing
	if len(data) == 0 {
		return nil
	}
	result := make([]byte, len(data))
	copy(result, data)
	return result
}

// Put stores bytecode for an address
func (bs *BytecodeStore) Put(addr common.Address, code []byte) error {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	if bs.closed {
		return fmt.Errorf("bytecode store is closed")
	}

	if len(code) == 0 {
		return nil // Don't store empty bytecode
	}

	return bs.db.Put(codeKey(addr), code)
}

// Has checks if bytecode exists for an address
func (bs *BytecodeStore) Has(addr common.Address) bool {
	bs.mu.RLock()
	defer bs.mu.RUnlock()

	if bs.closed {
		return false
	}

	ok, _ := bs.db.Has(codeKey(addr))
	return ok
}

// Close gracefully closes the underlying database
func (bs *BytecodeStore) Close() error {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	if bs.closed {
		return nil
	}

	bs.closed = true
	return bs.db.Close()
}
