package orchestrator

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/sharding-experiment/sharding/config"
	"github.com/sharding-experiment/sharding/internal/network"
)

// proofDB implements ethdb.KeyValueReader for Merkle proof verification.
// It stores proof nodes keyed by their Keccak256 hash.
type proofDB struct {
	nodes map[common.Hash][]byte
}

// newProofDB creates a proof database from a list of RLP-encoded trie nodes.
func newProofDB(proof [][]byte) *proofDB {
	db := &proofDB{nodes: make(map[common.Hash][]byte)}
	for _, node := range proof {
		db.nodes[crypto.Keccak256Hash(node)] = node
	}
	return db
}

// Has checks if a key exists in the proof database.
func (db *proofDB) Has(key []byte) (bool, error) {
	if len(key) != common.HashLength {
		return false, nil
	}
	_, ok := db.nodes[common.BytesToHash(key)]
	return ok, nil
}

// Get retrieves a node from the proof database.
func (db *proofDB) Get(key []byte) ([]byte, error) {
	if len(key) != common.HashLength {
		return nil, errors.New("invalid key length")
	}
	if node, ok := db.nodes[common.BytesToHash(key)]; ok {
		return node, nil
	}
	return nil, errors.New("node not found")
}

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
// networkConfig specifies network simulation parameters (delays, etc.).
func NewStateFetcher(numShards int, bytecodePath string, networkConfig config.NetworkConfig) (*StateFetcher, error) {
	bytecodeStore, err := NewBytecodeStore(bytecodePath)
	if err != nil {
		return nil, fmt.Errorf("create bytecode store: %w", err)
	}

	return &StateFetcher{
		numShards:     numShards,
		httpClient:    network.NewHTTPClient(networkConfig, 10*time.Second),
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
// V2.3: Optionally requests and verifies Merkle proofs when verifyProof is true
func (sf *StateFetcher) GetStorageAt(txID string, shardID int, addr common.Address, slot common.Hash) (common.Hash, error) {
	return sf.getStorageAtWithProof(txID, shardID, addr, slot, false)
}

// GetStorageAtWithProof fetches a storage slot and optionally verifies Merkle proof
func (sf *StateFetcher) GetStorageAtWithProof(txID string, shardID int, addr common.Address, slot common.Hash, verifyProof bool) (common.Hash, error) {
	return sf.getStorageAtWithProof(txID, shardID, addr, slot, verifyProof)
}

// getStorageAtWithProof is the internal implementation
func (sf *StateFetcher) getStorageAtWithProof(txID string, shardID int, addr common.Address, slot common.Hash, verifyProof bool) (common.Hash, error) {
	// Ensure we have basic state cached first
	_, err := sf.getState(txID, shardID, addr)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to fetch address state: %w", err)
	}

	// Build URL with proof parameter if needed
	url := fmt.Sprintf("%s/evm/storage/%s/%s", sf.shardURL(shardID), addr.Hex(), slot.Hex())
	if verifyProof {
		url += "?proof=true"
	}

	resp, err := sf.httpClient.Get(url)
	if err != nil {
		return common.Hash{}, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return common.Hash{}, fmt.Errorf("GET %s returned status %d", url, resp.StatusCode)
	}

	if verifyProof {
		// Parse response with proof
		var proofResp struct {
			Address      string   `json:"address"`
			Slot         string   `json:"slot"`
			Value        string   `json:"value"`
			StateRoot    string   `json:"state_root"`
			BlockHeight  uint64   `json:"block_height"`
			AccountProof []string `json:"account_proof"`
			StorageProof []string `json:"storage_proof"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&proofResp); err != nil {
			return common.Hash{}, fmt.Errorf("decode storage proof response: %w", err)
		}

		// Verify the proof
		stateRoot := common.HexToHash(proofResp.StateRoot)
		value := common.HexToHash(proofResp.Value)

		// TODO(V2.3): Validate stateRoot against canonical chain
		// Currently we trust the State Shard's reported state root. To achieve trustless
		// verification, we need to validate that the stateRoot corresponds to a canonical
		// State Shard block header. This requires light client infrastructure to track
		// State Shard block headers (see issue #2).

		if err := VerifyStorageProof(stateRoot, addr, slot, value, proofResp.AccountProof, proofResp.StorageProof); err != nil {
			return common.Hash{}, fmt.Errorf("proof verification failed: %w", err)
		}

		log.Printf("[StateFetcher] Verified proof for storage slot %s at %s (state root %s)",
			slot.Hex(), addr.Hex(), stateRoot.Hex())

		return value, nil
	} else {
		// Legacy: just parse value
		var result struct {
			Value string `json:"value"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return common.Hash{}, fmt.Errorf("decode storage response: %w", err)
		}

		return common.HexToHash(result.Value), nil
	}
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

// VerifyStorageProof verifies a Merkle proof for a storage slot value (V2.3)
// Returns nil if proof is valid, error otherwise
func VerifyStorageProof(
	stateRoot common.Hash,
	addr common.Address,
	slot common.Hash,
	value common.Hash,
	accountProof []string,
	storageProof []string,
) error {
	// Early validation: prevent DOS attacks via excessively large proofs
	// Typical Ethereum trie depth is ~64 nodes, so this is a generous upper bound
	const maxProofNodes = 128
	if len(accountProof) > maxProofNodes {
		return fmt.Errorf("account proof too large: %d nodes (max %d)", len(accountProof), maxProofNodes)
	}
	if len(storageProof) > maxProofNodes {
		return fmt.Errorf("storage proof too large: %d nodes (max %d)", len(storageProof), maxProofNodes)
	}

	// Convert hex string proofs to [][]byte
	accountProofBytes, err := parseProof(accountProof)
	if err != nil {
		return fmt.Errorf("invalid account proof format: %w", err)
	}

	storageProofBytes, err := parseProof(storageProof)
	if err != nil {
		return fmt.Errorf("invalid storage proof format: %w", err)
	}

	// Step 1: Verify account proof against state root
	accountKey := crypto.Keccak256(addr.Bytes())
	accountRLP, err := trie.VerifyProof(stateRoot, accountKey, newProofDB(accountProofBytes))
	if err != nil {
		return fmt.Errorf("account proof verification failed: %w", err)
	}

	// Step 2: Extract storage root from verified account
	// Account RLP encoding: [nonce, balance, storageRoot, codeHash]
	var account struct {
		Nonce       uint64
		Balance     *big.Int
		StorageRoot common.Hash
		CodeHash    common.Hash
	}

	if len(accountRLP) > 0 {
		if err := rlp.DecodeBytes(accountRLP, &account); err != nil {
			return fmt.Errorf("failed to decode account: %w", err)
		}
	} else {
		// Account doesn't exist - storage root is empty
		account.StorageRoot = types.EmptyRootHash
	}

	// Step 3: Verify storage proof against storage root
	// If storage root is empty, value must be zero
	if account.StorageRoot == types.EmptyRootHash || account.StorageRoot == (common.Hash{}) {
		if value != (common.Hash{}) {
			return fmt.Errorf("storage proof invalid: non-zero value for empty storage root")
		}
		return nil
	}

	slotKey := crypto.Keccak256(slot.Bytes())
	valueRLP, err := trie.VerifyProof(account.StorageRoot, slotKey, newProofDB(storageProofBytes))
	if err != nil {
		return fmt.Errorf("storage proof verification failed: %w", err)
	}

	// Step 4: Verify that the retrieved value matches the claimed value
	var storedValue common.Hash
	if len(valueRLP) > 0 {
		// Storage values are RLP-encoded
		if err := rlp.DecodeBytes(valueRLP, &storedValue); err != nil {
			return fmt.Errorf("failed to decode storage value: %w", err)
		}
	}

	if storedValue != value {
		return fmt.Errorf("storage value mismatch: proof shows %s, claimed %s",
			storedValue.Hex(), value.Hex())
	}

	return nil
}

// parseProof converts hex string array to [][]byte
func parseProof(hexProof []string) ([][]byte, error) {
	result := make([][]byte, len(hexProof))
	for i, hexStr := range hexProof {
		// Remove 0x prefix if present
		if len(hexStr) >= 2 && hexStr[:2] == "0x" {
			hexStr = hexStr[2:]
		}
		bytes, err := hex.DecodeString(hexStr)
		if err != nil {
			return nil, fmt.Errorf("invalid hex at index %d: %w", i, err)
		}
		result[i] = bytes
	}
	return result, nil
}
