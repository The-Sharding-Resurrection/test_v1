package orchestrator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// StateFetcher handles HTTP communication with State Shards for state fetching
type StateFetcher struct {
	numShards  int
	httpClient *http.Client

	// Code cache (bytecode is immutable after deploy)
	codeCacheMu sync.RWMutex
	codeCache   map[common.Address][]byte

	// Cached locked state per transaction
	stateCacheMu sync.RWMutex
	stateCache   map[string]map[common.Address]*cachedState // txID -> addr -> state

	// Track locked addresses per tx for cleanup
	locksMu sync.RWMutex
	locks   map[string][]lockedAddr // txID -> list of locked addresses
}

// cachedState holds the locked account state
type cachedState struct {
	ShardID  int
	Balance  *big.Int
	Nonce    uint64
	Code     []byte
	CodeHash common.Hash
}

type lockedAddr struct {
	ShardID int
	Address common.Address
}

// NewStateFetcher creates a new state fetcher
func NewStateFetcher(numShards int) *StateFetcher {
	return &StateFetcher{
		numShards: numShards,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		codeCache:  make(map[common.Address][]byte),
		stateCache: make(map[string]map[common.Address]*cachedState),
		locks:      make(map[string][]lockedAddr),
	}
}

func (sf *StateFetcher) shardURL(shardID int) string {
	return fmt.Sprintf("http://shard-%d:8545", shardID)
}

// FetchAndLock locks an address on a shard and returns the account state
func (sf *StateFetcher) FetchAndLock(txID string, shardID int, addr common.Address) (*protocol.LockResponse, error) {
	if shardID < 0 || shardID >= sf.numShards {
		return nil, fmt.Errorf("invalid shard ID: %d", shardID)
	}

	req := protocol.LockRequest{
		TxID:    txID,
		Address: addr,
	}

	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal lock request: %w", err)
	}

	url := sf.shardURL(shardID) + "/state/lock"
	resp, err := sf.httpClient.Post(url, "application/json", bytes.NewBuffer(reqData))
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var lockResp protocol.LockResponse
	if err := json.Unmarshal(body, &lockResp); err != nil {
		return nil, fmt.Errorf("unmarshal lock response: %w", err)
	}

	if !lockResp.Success {
		return &lockResp, fmt.Errorf("lock failed: %s", lockResp.Error)
	}

	// Track this lock for cleanup
	sf.locksMu.Lock()
	sf.locks[txID] = append(sf.locks[txID], lockedAddr{ShardID: shardID, Address: addr})
	sf.locksMu.Unlock()

	// Cache the state for this tx
	sf.stateCacheMu.Lock()
	if sf.stateCache[txID] == nil {
		sf.stateCache[txID] = make(map[common.Address]*cachedState)
	}
	sf.stateCache[txID][addr] = &cachedState{
		ShardID:  shardID,
		Balance:  lockResp.Balance,
		Nonce:    lockResp.Nonce,
		Code:     lockResp.Code,
		CodeHash: lockResp.CodeHash,
	}
	sf.stateCacheMu.Unlock()

	// Cache the code globally (bytecode is immutable)
	if len(lockResp.Code) > 0 {
		sf.codeCacheMu.Lock()
		sf.codeCache[addr] = lockResp.Code
		sf.codeCacheMu.Unlock()
	}

	return &lockResp, nil
}

// getState returns cached state or fetches and locks if not cached
func (sf *StateFetcher) getState(txID string, shardID int, addr common.Address) (*cachedState, error) {
	// Check cache first
	sf.stateCacheMu.RLock()
	if txCache, ok := sf.stateCache[txID]; ok {
		if cached, ok := txCache[addr]; ok {
			sf.stateCacheMu.RUnlock()
			return cached, nil
		}
	}
	sf.stateCacheMu.RUnlock()

	// Not cached - fetch and lock
	_, err := sf.FetchAndLock(txID, shardID, addr)
	if err != nil {
		return nil, err
	}

	// Return from cache (FetchAndLock populated it)
	sf.stateCacheMu.RLock()
	defer sf.stateCacheMu.RUnlock()
	return sf.stateCache[txID][addr], nil
}

// UnlockAddress unlocks a specific address on a shard
func (sf *StateFetcher) UnlockAddress(txID string, shardID int, addr common.Address) error {
	req := protocol.UnlockRequest{
		TxID:    txID,
		Address: addr,
	}

	reqData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal unlock request: %w", err)
	}

	url := sf.shardURL(shardID) + "/state/unlock"
	resp, err := sf.httpClient.Post(url, "application/json", bytes.NewBuffer(reqData))
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	var unlockResp protocol.UnlockResponse
	if err := json.NewDecoder(resp.Body).Decode(&unlockResp); err != nil {
		return fmt.Errorf("decode unlock response: %w", err)
	}

	if !unlockResp.Success {
		return fmt.Errorf("unlock failed: %s", unlockResp.Error)
	}

	return nil
}

// UnlockAll releases all locks held by a transaction
func (sf *StateFetcher) UnlockAll(txID string) {
	sf.locksMu.Lock()
	lockedAddrs := sf.locks[txID]
	delete(sf.locks, txID)
	sf.locksMu.Unlock()

	// Clear state cache for this tx
	sf.stateCacheMu.Lock()
	delete(sf.stateCache, txID)
	sf.stateCacheMu.Unlock()

	// Unlock all addresses (best effort, don't block on errors)
	for _, la := range lockedAddrs {
		go func(shardID int, addr common.Address) {
			sf.UnlockAddress(txID, shardID, addr)
		}(la.ShardID, la.Address)
	}
}

// GetStorageAt fetches a storage slot - locks address if not already locked
// Note: Storage slots are fetched on-demand via HTTP, not included in lock response
func (sf *StateFetcher) GetStorageAt(txID string, shardID int, addr common.Address, slot common.Hash) (common.Hash, error) {
	// Ensure address is locked first (for consistency during simulation)
	_, err := sf.getState(txID, shardID, addr)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to lock address: %w", err)
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

// GetBalance returns balance - locks address if not already locked
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

// GetNonce returns nonce - locks address if not already locked
func (sf *StateFetcher) GetNonce(txID string, shardID int, addr common.Address) (uint64, error) {
	state, err := sf.getState(txID, shardID, addr)
	if err != nil {
		return 0, err
	}
	return state.Nonce, nil
}

// GetCode returns code - locks address if not already locked
func (sf *StateFetcher) GetCode(txID string, shardID int, addr common.Address) ([]byte, error) {
	// Check global code cache first (bytecode is immutable)
	sf.codeCacheMu.RLock()
	if code, ok := sf.codeCache[addr]; ok {
		sf.codeCacheMu.RUnlock()
		// Return a copy to avoid aliasing cached data
		if code == nil {
			return nil, nil
		}
		result := make([]byte, len(code))
		copy(result, code)
		return result, nil
	}
	sf.codeCacheMu.RUnlock()

	// Not in global cache - get state (will lock if needed)
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

// GetLockedAddresses returns all addresses locked by a transaction
func (sf *StateFetcher) GetLockedAddresses(txID string) []lockedAddr {
	sf.locksMu.RLock()
	defer sf.locksMu.RUnlock()
	// Return a copy to avoid aliasing internal slice
	locked := sf.locks[txID]
	if locked == nil {
		return nil
	}
	result := make([]lockedAddr, len(locked))
	copy(result, locked)
	return result
}

// AddressToShard returns which shard owns an address (simple modulo assignment)
func (sf *StateFetcher) AddressToShard(addr common.Address) int {
	// Use last byte of address for shard assignment
	return int(addr[len(addr)-1]) % sf.numShards
}
