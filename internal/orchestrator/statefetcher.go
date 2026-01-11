package orchestrator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// StateFetcher handles HTTP communication with State Shards for state fetching.
//
// V2 Optimistic Locking: This fetcher performs READ-ONLY state access during simulation.
// No locks are acquired during simulation. Locking only happens during Lock tx execution
// on State Shards after the Orchestrator broadcasts CtToOrder.
type StateFetcher struct {
	numShards  int
	httpClient *http.Client

	// Persistent bytecode storage (bytecode is immutable after deploy)
	bytecodeStore *BytecodeStore

	// Cached state per transaction (read-only, no locks)
	stateCacheMu sync.RWMutex
	stateCache   map[string]map[common.Address]*cachedState // txID -> addr -> state
}

// cachedState holds fetched account state (read-only snapshot)
type cachedState struct {
	ShardID  int
	Balance  *big.Int
	Nonce    uint64
	Code     []byte
	CodeHash common.Hash
}

// fetchedAddr tracks which addresses were fetched for a tx
type fetchedAddr struct {
	ShardID int
	Address common.Address
}

// NewStateFetcher creates a new state fetcher with persistent bytecode storage.
// If bytecodePath is empty, bytecode is stored in-memory only.
func NewStateFetcher(numShards int, bytecodePath string) (*StateFetcher, error) {
	bytecodeStore, err := NewBytecodeStore(bytecodePath)
	if err != nil {
		return nil, fmt.Errorf("create bytecode store: %w", err)
	}

	return &StateFetcher{
		numShards: numShards,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		bytecodeStore: bytecodeStore,
		stateCache:    make(map[string]map[common.Address]*cachedState),
	}, nil
}

// Close gracefully shuts down the StateFetcher, closing the bytecode store
func (sf *StateFetcher) Close() error {
	if sf.bytecodeStore != nil {
		return sf.bytecodeStore.Close()
	}
	return nil
}

func (sf *StateFetcher) shardURL(shardID int) string {
	return fmt.Sprintf("http://shard-%d:8545", shardID)
}

// FetchStateResponse mirrors the response from /state/fetch endpoint
type FetchStateResponse struct {
	Success  bool            `json:"success"`
	Error    string          `json:"error,omitempty"`
	Balance  *big.Int        `json:"balance"`
	Nonce    uint64          `json:"nonce"`
	Code     []byte          `json:"code,omitempty"`
	CodeHash common.Hash     `json:"code_hash"`
}

// FetchState reads account state from a shard WITHOUT acquiring locks.
// V2 Optimistic Locking: State is read speculatively for simulation.
// Validation and locking happens later during Lock tx execution.
func (sf *StateFetcher) FetchState(txID string, shardID int, addr common.Address) (*FetchStateResponse, error) {
	if shardID < 0 || shardID >= sf.numShards {
		return nil, fmt.Errorf("invalid shard ID: %d", shardID)
	}

	// Request format - just address, no lock
	req := struct {
		Address common.Address `json:"address"`
	}{
		Address: addr,
	}

	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal fetch request: %w", err)
	}

	url := sf.shardURL(shardID) + "/state/fetch"
	resp, err := sf.httpClient.Post(url, "application/json", bytes.NewBuffer(reqData))
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var fetchResp FetchStateResponse
	if err := json.Unmarshal(body, &fetchResp); err != nil {
		return nil, fmt.Errorf("unmarshal fetch response: %w", err)
	}

	if !fetchResp.Success {
		return &fetchResp, fmt.Errorf("fetch failed: %s", fetchResp.Error)
	}

	// Cache the state for this tx (read-only)
	sf.stateCacheMu.Lock()
	if sf.stateCache[txID] == nil {
		sf.stateCache[txID] = make(map[common.Address]*cachedState)
	}
	sf.stateCache[txID][addr] = &cachedState{
		ShardID:  shardID,
		Balance:  fetchResp.Balance,
		Nonce:    fetchResp.Nonce,
		Code:     fetchResp.Code,
		CodeHash: fetchResp.CodeHash,
	}
	sf.stateCacheMu.Unlock()

	// Persist bytecode to storage (bytecode is immutable after deploy)
	if len(fetchResp.Code) > 0 {
		if err := sf.bytecodeStore.Put(addr, fetchResp.Code); err != nil {
			// Log but don't fail - caching is optional optimization
			log.Printf("[StateFetcher] Warning: failed to persist bytecode for %s: %v", addr.Hex(), err)
		}
	}

	return &fetchResp, nil
}

// getState returns cached state or fetches if not cached.
// Uses double-checked locking to reduce redundant HTTP requests under concurrent access.
// Note: This pattern reduces but doesn't fully prevent duplicate fetches. If multiple
// goroutines pass the second check before any completes FetchState, duplicates occur.
// This is acceptable since state is immutable during simulation - duplicates waste
// bandwidth but don't cause correctness issues.
func (sf *StateFetcher) getState(txID string, shardID int, addr common.Address) (*cachedState, error) {
	// First check: read lock (fast path for cached data)
	sf.stateCacheMu.RLock()
	if txCache, ok := sf.stateCache[txID]; ok {
		if cached, ok := txCache[addr]; ok {
			sf.stateCacheMu.RUnlock()
			return cached, nil
		}
	}
	sf.stateCacheMu.RUnlock()

	// Cache miss - acquire write lock for double-checked locking
	sf.stateCacheMu.Lock()

	// Second check: catches cases where another goroutine populated cache while we waited
	if txCache, ok := sf.stateCache[txID]; ok {
		if cached, ok := txCache[addr]; ok {
			sf.stateCacheMu.Unlock()
			return cached, nil
		}
	}
	sf.stateCacheMu.Unlock()

	// Not cached - fetch (releases lock during HTTP to allow parallel fetches for different addresses)
	_, err := sf.FetchState(txID, shardID, addr)
	if err != nil {
		return nil, err
	}

	// Return from cache (FetchState populated it)
	sf.stateCacheMu.RLock()
	defer sf.stateCacheMu.RUnlock()
	return sf.stateCache[txID][addr], nil
}

// ClearCache clears the state cache for a transaction.
// Called after simulation completes (success or failure).
func (sf *StateFetcher) ClearCache(txID string) {
	sf.stateCacheMu.Lock()
	delete(sf.stateCache, txID)
	sf.stateCacheMu.Unlock()
}

// GetStorageAt fetches a storage slot (read-only, no locking)
func (sf *StateFetcher) GetStorageAt(txID string, shardID int, addr common.Address, slot common.Hash) (common.Hash, error) {
	// Ensure we have basic state cached first
	_, err := sf.getState(txID, shardID, addr)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to fetch address state: %w", err)
	}

	// Fetch the specific storage slot via HTTP
	url := fmt.Sprintf("%s/evm/storage/%s/%s", sf.shardURL(shardID), addr.Hex(), slot.Hex())
	resp, err := sf.httpClient.Get(url)
	if err != nil {
		return common.Hash{}, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	var result struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return common.Hash{}, fmt.Errorf("decode storage response: %w", err)
	}

	return common.HexToHash(result.Value), nil
}

// GetBalance returns balance (read-only, no locking)
func (sf *StateFetcher) GetBalance(txID string, shardID int, addr common.Address) (*big.Int, error) {
	state, err := sf.getState(txID, shardID, addr)
	if err != nil {
		return nil, err
	}
	// Return a copy to avoid aliasing cached data
	if state.Balance == nil {
		return nil, nil
	}
	return new(big.Int).Set(state.Balance), nil
}

// GetNonce returns nonce (read-only, no locking)
func (sf *StateFetcher) GetNonce(txID string, shardID int, addr common.Address) (uint64, error) {
	state, err := sf.getState(txID, shardID, addr)
	if err != nil {
		return 0, err
	}
	return state.Nonce, nil
}

// GetCode returns code - checks persistent store first, then fetches if needed (no locking)
func (sf *StateFetcher) GetCode(txID string, shardID int, addr common.Address) ([]byte, error) {
	// Check persistent bytecode store first (bytecode is immutable)
	if code := sf.bytecodeStore.Get(addr); code != nil {
		return code, nil // BytecodeStore.Get already returns a copy
	}

	// Not in persistent store - get state (will fetch if needed, no lock)
	state, err := sf.getState(txID, shardID, addr)
	if err != nil {
		return nil, err
	}
	// Return a copy to avoid aliasing cached data
	if state.Code == nil {
		return nil, nil
	}
	result := make([]byte, len(state.Code))
	copy(result, state.Code)
	return result, nil
}

// AddressToShard returns which shard owns an address (simple modulo assignment)
func (sf *StateFetcher) AddressToShard(addr common.Address) int {
	// Use last byte of address for shard assignment
	return int(addr[len(addr)-1]) % sf.numShards
}
