package shard

import (
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/stateless"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie/utils"
	"github.com/holiman/uint256"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// TrackingStateDB wraps a StateDB and tracks all accessed addresses and storage writes
// Used for simulation to detect cross-shard transactions and for deployment storage tracking
type TrackingStateDB struct {
	mu            sync.RWMutex
	inner         *state.StateDB
	accessedAddrs map[common.Address]bool
	storageReads  map[common.Address]map[common.Hash]common.Hash // addr -> slot -> value (V2.2)
	storageWrites map[common.Address]map[common.Hash]common.Hash // addr -> slot -> value
	numShards     int
	localShardID  int
}

// NewTrackingStateDB creates a new tracking wrapper
func NewTrackingStateDB(inner *state.StateDB, localShardID, numShards int) *TrackingStateDB {
	return &TrackingStateDB{
		inner:         inner,
		accessedAddrs: make(map[common.Address]bool),
		storageReads:  make(map[common.Address]map[common.Hash]common.Hash),
		storageWrites: make(map[common.Address]map[common.Hash]common.Hash),
		numShards:     numShards,
		localShardID:  localShardID,
	}
}

// recordAccess tracks an address access
func (t *TrackingStateDB) recordAccess(addr common.Address) {
	t.mu.Lock()
	t.accessedAddrs[addr] = true
	t.mu.Unlock()
}

// GetAccessedAddresses returns all addresses accessed during execution
func (t *TrackingStateDB) GetAccessedAddresses() []common.Address {
	t.mu.RLock()
	defer t.mu.RUnlock()
	addrs := make([]common.Address, 0, len(t.accessedAddrs))
	for addr := range t.accessedAddrs {
		addrs = append(addrs, addr)
	}
	return addrs
}

// HasCrossShardAccess returns true if any accessed address is on another shard
func (t *TrackingStateDB) HasCrossShardAccess() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for addr := range t.accessedAddrs {
		shardID := int(addr[len(addr)-1]) % t.numShards
		if shardID != t.localShardID {
			return true
		}
	}
	return false
}

// GetCrossShardAddresses returns addresses that are on other shards
func (t *TrackingStateDB) GetCrossShardAddresses() map[common.Address]int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make(map[common.Address]int)
	for addr := range t.accessedAddrs {
		shardID := int(addr[len(addr)-1]) % t.numShards
		if shardID != t.localShardID {
			result[addr] = shardID
		}
	}
	return result
}

// === StateDB interface implementation ===

func (t *TrackingStateDB) CreateAccount(addr common.Address) {
	t.recordAccess(addr)
	t.inner.CreateAccount(addr)
}

func (t *TrackingStateDB) CreateContract(addr common.Address) {
	t.recordAccess(addr)
	t.inner.CreateContract(addr)
}

func (t *TrackingStateDB) SubBalance(addr common.Address, amount *uint256.Int, reason tracing.BalanceChangeReason) uint256.Int {
	t.recordAccess(addr)
	return t.inner.SubBalance(addr, amount, reason)
}

func (t *TrackingStateDB) AddBalance(addr common.Address, amount *uint256.Int, reason tracing.BalanceChangeReason) uint256.Int {
	t.recordAccess(addr)
	return t.inner.AddBalance(addr, amount, reason)
}

func (t *TrackingStateDB) GetBalance(addr common.Address) *uint256.Int {
	t.recordAccess(addr)
	return t.inner.GetBalance(addr)
}

func (t *TrackingStateDB) GetNonce(addr common.Address) uint64 {
	t.recordAccess(addr)
	return t.inner.GetNonce(addr)
}

func (t *TrackingStateDB) SetNonce(addr common.Address, nonce uint64, reason tracing.NonceChangeReason) {
	t.recordAccess(addr)
	t.inner.SetNonce(addr, nonce, reason)
}

func (t *TrackingStateDB) GetCodeHash(addr common.Address) common.Hash {
	t.recordAccess(addr)
	return t.inner.GetCodeHash(addr)
}

func (t *TrackingStateDB) GetCode(addr common.Address) []byte {
	t.recordAccess(addr)
	return t.inner.GetCode(addr)
}

func (t *TrackingStateDB) SetCode(addr common.Address, code []byte, reason tracing.CodeChangeReason) []byte {
	t.recordAccess(addr)
	return t.inner.SetCode(addr, code, reason)
}

func (t *TrackingStateDB) GetCodeSize(addr common.Address) int {
	t.recordAccess(addr)
	return t.inner.GetCodeSize(addr)
}

func (t *TrackingStateDB) AddRefund(gas uint64) {
	t.inner.AddRefund(gas)
}

func (t *TrackingStateDB) SubRefund(gas uint64) {
	t.inner.SubRefund(gas)
}

func (t *TrackingStateDB) GetRefund() uint64 {
	return t.inner.GetRefund()
}

func (t *TrackingStateDB) GetCommittedState(addr common.Address, hash common.Hash) common.Hash {
	t.recordAccess(addr)
	return t.inner.GetCommittedState(addr, hash)
}

func (t *TrackingStateDB) GetState(addr common.Address, hash common.Hash) common.Hash {
	t.recordAccess(addr)
	value := t.inner.GetState(addr, hash)
	// V2.2: Track storage read for RwSet
	t.mu.Lock()
	if t.storageReads[addr] == nil {
		t.storageReads[addr] = make(map[common.Hash]common.Hash)
	}
	// Only record first read (original value)
	if _, exists := t.storageReads[addr][hash]; !exists {
		t.storageReads[addr][hash] = value
	}
	t.mu.Unlock()
	return value
}

func (t *TrackingStateDB) GetStateAndCommittedState(addr common.Address, slot common.Hash) (common.Hash, common.Hash) {
	t.recordAccess(addr)
	value := t.inner.GetState(addr, slot)
	committed := t.inner.GetCommittedState(addr, slot)
	// V2.2: Track storage read for RwSet
	t.mu.Lock()
	if t.storageReads[addr] == nil {
		t.storageReads[addr] = make(map[common.Hash]common.Hash)
	}
	if _, exists := t.storageReads[addr][slot]; !exists {
		t.storageReads[addr][slot] = value
	}
	t.mu.Unlock()
	return value, committed
}

func (t *TrackingStateDB) SetState(addr common.Address, key common.Hash, value common.Hash) common.Hash {
	t.mu.Lock()
	t.accessedAddrs[addr] = true
	// V2.2: Track read of old value (if not already tracked)
	if t.storageReads[addr] == nil {
		t.storageReads[addr] = make(map[common.Hash]common.Hash)
	}
	if _, exists := t.storageReads[addr][key]; !exists {
		// Record the original value before write
		t.storageReads[addr][key] = t.inner.GetState(addr, key)
	}
	// Track storage write (new value)
	if t.storageWrites[addr] == nil {
		t.storageWrites[addr] = make(map[common.Hash]common.Hash)
	}
	t.storageWrites[addr][key] = value
	t.mu.Unlock()
	return t.inner.SetState(addr, key, value)
}

// GetStorageWrites returns a copy of all storage writes tracked during execution
func (t *TrackingStateDB) GetStorageWrites() map[common.Address]map[common.Hash]common.Hash {
	t.mu.RLock()
	defer t.mu.RUnlock()
	// Return a deep copy to avoid race conditions after unlock
	result := make(map[common.Address]map[common.Hash]common.Hash, len(t.storageWrites))
	for addr, slots := range t.storageWrites {
		slotCopy := make(map[common.Hash]common.Hash, len(slots))
		for k, v := range slots {
			slotCopy[k] = v
		}
		result[addr] = slotCopy
	}
	return result
}

// GetStorageWritesForAddress returns a copy of storage writes for a specific address
func (t *TrackingStateDB) GetStorageWritesForAddress(addr common.Address) map[common.Hash]common.Hash {
	t.mu.RLock()
	defer t.mu.RUnlock()
	slots := t.storageWrites[addr]
	if slots == nil {
		return nil
	}
	// Return a copy to avoid race conditions after unlock
	result := make(map[common.Hash]common.Hash, len(slots))
	for k, v := range slots {
		result[k] = v
	}
	return result
}

func (t *TrackingStateDB) GetStorageRoot(addr common.Address) common.Hash {
	t.recordAccess(addr)
	return t.inner.GetStorageRoot(addr)
}

func (t *TrackingStateDB) GetTransientState(addr common.Address, key common.Hash) common.Hash {
	t.recordAccess(addr)
	return t.inner.GetTransientState(addr, key)
}

func (t *TrackingStateDB) SetTransientState(addr common.Address, key common.Hash, value common.Hash) {
	t.recordAccess(addr)
	t.inner.SetTransientState(addr, key, value)
}

func (t *TrackingStateDB) SelfDestruct(addr common.Address) uint256.Int {
	t.recordAccess(addr)
	return t.inner.SelfDestruct(addr)
}

func (t *TrackingStateDB) HasSelfDestructed(addr common.Address) bool {
	t.recordAccess(addr)
	return t.inner.HasSelfDestructed(addr)
}

func (t *TrackingStateDB) SelfDestruct6780(addr common.Address) (uint256.Int, bool) {
	t.recordAccess(addr)
	return t.inner.SelfDestruct6780(addr)
}

func (t *TrackingStateDB) Exist(addr common.Address) bool {
	t.recordAccess(addr)
	return t.inner.Exist(addr)
}

func (t *TrackingStateDB) Empty(addr common.Address) bool {
	t.recordAccess(addr)
	return t.inner.Empty(addr)
}

func (t *TrackingStateDB) AddressInAccessList(addr common.Address) bool {
	return t.inner.AddressInAccessList(addr)
}

func (t *TrackingStateDB) SlotInAccessList(addr common.Address, slot common.Hash) (addressOk bool, slotOk bool) {
	return t.inner.SlotInAccessList(addr, slot)
}

func (t *TrackingStateDB) AddAddressToAccessList(addr common.Address) {
	t.inner.AddAddressToAccessList(addr)
}

func (t *TrackingStateDB) AddSlotToAccessList(addr common.Address, slot common.Hash) {
	t.inner.AddSlotToAccessList(addr, slot)
}

func (t *TrackingStateDB) Prepare(rules params.Rules, sender, coinbase common.Address, dest *common.Address, precompiles []common.Address, txAccesses types.AccessList) {
	t.inner.Prepare(rules, sender, coinbase, dest, precompiles, txAccesses)
}

func (t *TrackingStateDB) RevertToSnapshot(revid int) {
	t.inner.RevertToSnapshot(revid)
}

func (t *TrackingStateDB) Snapshot() int {
	return t.inner.Snapshot()
}

func (t *TrackingStateDB) AddLog(log *types.Log) {
	t.inner.AddLog(log)
}

func (t *TrackingStateDB) AddPreimage(hash common.Hash, preimage []byte) {
	t.inner.AddPreimage(hash, preimage)
}

func (t *TrackingStateDB) PointCache() *utils.PointCache {
	return t.inner.PointCache()
}

func (t *TrackingStateDB) Witness() *stateless.Witness {
	return t.inner.Witness()
}

func (t *TrackingStateDB) AccessEvents() *state.AccessEvents {
	return t.inner.AccessEvents()
}

func (t *TrackingStateDB) Finalise(deleteEmptyObjects bool) {
	t.inner.Finalise(deleteEmptyObjects)
}

// GetStorageReads returns a copy of all storage reads tracked during execution
// V2.2: Used to build ReadSet for RwSetReply
func (t *TrackingStateDB) GetStorageReads() map[common.Address]map[common.Hash]common.Hash {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make(map[common.Address]map[common.Hash]common.Hash, len(t.storageReads))
	for addr, slots := range t.storageReads {
		slotCopy := make(map[common.Hash]common.Hash, len(slots))
		for k, v := range slots {
			slotCopy[k] = v
		}
		result[addr] = slotCopy
	}
	return result
}

// BuildRwSet constructs a protocol.RwVariable slice from tracked reads and writes
// V2.2: Used to return RwSet for sub-call simulation
func (t *TrackingStateDB) BuildRwSet(refBlock protocol.Reference) []protocol.RwVariable {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Collect all addresses that had reads or writes
	addrSet := make(map[common.Address]bool)
	for addr := range t.storageReads {
		addrSet[addr] = true
	}
	for addr := range t.storageWrites {
		addrSet[addr] = true
	}

	result := make([]protocol.RwVariable, 0, len(addrSet))
	for addr := range addrSet {
		rw := protocol.RwVariable{
			Address:        addr,
			ReferenceBlock: refBlock,
		}

		// Build ReadSet from tracked reads
		if reads, ok := t.storageReads[addr]; ok {
			rw.ReadSet = make([]protocol.ReadSetItem, 0, len(reads))
			for slot, value := range reads {
				rw.ReadSet = append(rw.ReadSet, protocol.ReadSetItem{
					Slot:  protocol.Slot(slot),
					Value: value.Bytes(),
				})
			}
		}

		// Build WriteSet from tracked writes
		if writes, ok := t.storageWrites[addr]; ok {
			rw.WriteSet = make([]protocol.WriteSetItem, 0, len(writes))
			for slot, newValue := range writes {
				// Get original value from reads (we track it in SetState)
				oldValue := common.Hash{}
				if reads, hasReads := t.storageReads[addr]; hasReads {
					if v, hasSlot := reads[slot]; hasSlot {
						oldValue = v
					}
				}
				rw.WriteSet = append(rw.WriteSet, protocol.WriteSetItem{
					Slot:     protocol.Slot(slot),
					OldValue: oldValue.Bytes(),
					NewValue: newValue.Bytes(),
				})
			}
		}

		result = append(result, rw)
	}

	return result
}
