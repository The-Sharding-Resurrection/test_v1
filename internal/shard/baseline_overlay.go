package shard

import (
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

// OverlayStateDB wraps a StateDB and overlays RwSet data for re-execution
// This allows baseline protocol to "mock" state from previous hops
type OverlayStateDB struct {
	inner   *state.StateDB
	overlay map[common.Address]map[common.Hash]common.Hash // addr -> slot -> value
}

// NewOverlayStateDB creates a new overlay StateDB with RwSet data
func NewOverlayStateDB(inner *state.StateDB, rwSet []protocol.RwVariable) *OverlayStateDB {
	overlay := make(map[common.Address]map[common.Hash]common.Hash)

	// Build overlay from RwSet
	for _, rw := range rwSet {
		overlay[rw.Address] = make(map[common.Hash]common.Hash)
		for _, read := range rw.ReadSet {
			overlay[rw.Address][common.Hash(read.Slot)] = common.BytesToHash(read.Value)
		}
	}

	return &OverlayStateDB{
		inner:   inner,
		overlay: overlay,
	}
}

// GetState checks overlay first, then falls back to inner StateDB
func (o *OverlayStateDB) GetState(addr common.Address, slot common.Hash) common.Hash {
	if addrOverlay, ok := o.overlay[addr]; ok {
		if value, ok := addrOverlay[slot]; ok {
			return value
		}
	}
	return o.inner.GetState(addr, slot)
}

// GetStateAndCommittedState checks overlay for current, uses inner for committed
func (o *OverlayStateDB) GetStateAndCommittedState(addr common.Address, slot common.Hash) (common.Hash, common.Hash) {
	current := o.GetState(addr, slot)
	committed := o.inner.GetCommittedState(addr, slot)
	return current, committed
}

// SetState updates inner StateDB (not overlay - overlay is read-only)
func (o *OverlayStateDB) SetState(addr common.Address, key common.Hash, value common.Hash) common.Hash {
	return o.inner.SetState(addr, key, value)
}

// All other methods delegate to inner StateDB

func (o *OverlayStateDB) CreateAccount(addr common.Address) {
	o.inner.CreateAccount(addr)
}

func (o *OverlayStateDB) CreateContract(addr common.Address) {
	o.inner.CreateContract(addr)
}

func (o *OverlayStateDB) SubBalance(addr common.Address, amount *uint256.Int, reason tracing.BalanceChangeReason) uint256.Int {
	return o.inner.SubBalance(addr, amount, reason)
}

func (o *OverlayStateDB) AddBalance(addr common.Address, amount *uint256.Int, reason tracing.BalanceChangeReason) uint256.Int {
	return o.inner.AddBalance(addr, amount, reason)
}

func (o *OverlayStateDB) GetBalance(addr common.Address) *uint256.Int {
	return o.inner.GetBalance(addr)
}

func (o *OverlayStateDB) GetNonce(addr common.Address) uint64 {
	return o.inner.GetNonce(addr)
}

func (o *OverlayStateDB) SetNonce(addr common.Address, nonce uint64, reason tracing.NonceChangeReason) {
	o.inner.SetNonce(addr, nonce, reason)
}

func (o *OverlayStateDB) GetCodeHash(addr common.Address) common.Hash {
	return o.inner.GetCodeHash(addr)
}

func (o *OverlayStateDB) GetCode(addr common.Address) []byte {
	return o.inner.GetCode(addr)
}

func (o *OverlayStateDB) SetCode(addr common.Address, code []byte, reason tracing.CodeChangeReason) []byte {
	return o.inner.SetCode(addr, code, reason)
}

func (o *OverlayStateDB) GetCodeSize(addr common.Address) int {
	return o.inner.GetCodeSize(addr)
}

func (o *OverlayStateDB) AddRefund(gas uint64) {
	o.inner.AddRefund(gas)
}

func (o *OverlayStateDB) SubRefund(gas uint64) {
	o.inner.SubRefund(gas)
}

func (o *OverlayStateDB) GetRefund() uint64 {
	return o.inner.GetRefund()
}

func (o *OverlayStateDB) GetCommittedState(addr common.Address, hash common.Hash) common.Hash {
	return o.inner.GetCommittedState(addr, hash)
}

func (o *OverlayStateDB) GetStorageRoot(addr common.Address) common.Hash {
	return o.inner.GetStorageRoot(addr)
}

func (o *OverlayStateDB) GetTransientState(addr common.Address, key common.Hash) common.Hash {
	return o.inner.GetTransientState(addr, key)
}

func (o *OverlayStateDB) SetTransientState(addr common.Address, key common.Hash, value common.Hash) {
	o.inner.SetTransientState(addr, key, value)
}

func (o *OverlayStateDB) SelfDestruct(addr common.Address) uint256.Int {
	return o.inner.SelfDestruct(addr)
}

func (o *OverlayStateDB) HasSelfDestructed(addr common.Address) bool {
	return o.inner.HasSelfDestructed(addr)
}

func (o *OverlayStateDB) SelfDestruct6780(addr common.Address) (uint256.Int, bool) {
	return o.inner.SelfDestruct6780(addr)
}

func (o *OverlayStateDB) Exist(addr common.Address) bool {
	return o.inner.Exist(addr)
}

func (o *OverlayStateDB) Empty(addr common.Address) bool {
	return o.inner.Empty(addr)
}

func (o *OverlayStateDB) AddressInAccessList(addr common.Address) bool {
	return o.inner.AddressInAccessList(addr)
}

func (o *OverlayStateDB) SlotInAccessList(addr common.Address, slot common.Hash) (bool, bool) {
	return o.inner.SlotInAccessList(addr, slot)
}

func (o *OverlayStateDB) AddAddressToAccessList(addr common.Address) {
	o.inner.AddAddressToAccessList(addr)
}

func (o *OverlayStateDB) AddSlotToAccessList(addr common.Address, slot common.Hash) {
	o.inner.AddSlotToAccessList(addr, slot)
}

func (o *OverlayStateDB) Prepare(rules params.Rules, sender, coinbase common.Address, dest *common.Address, precompiles []common.Address, txAccesses types.AccessList) {
	o.inner.Prepare(rules, sender, coinbase, dest, precompiles, txAccesses)
}

func (o *OverlayStateDB) RevertToSnapshot(revid int) {
	o.inner.RevertToSnapshot(revid)
}

func (o *OverlayStateDB) Snapshot() int {
	return o.inner.Snapshot()
}

func (o *OverlayStateDB) AddLog(log *types.Log) {
	o.inner.AddLog(log)
}

func (o *OverlayStateDB) AddPreimage(hash common.Hash, preimage []byte) {
	o.inner.AddPreimage(hash, preimage)
}

func (o *OverlayStateDB) PointCache() *utils.PointCache {
	return o.inner.PointCache()
}

func (o *OverlayStateDB) Witness() *stateless.Witness {
	return o.inner.Witness()
}

func (o *OverlayStateDB) AccessEvents() *state.AccessEvents {
	return o.inner.AccessEvents()
}

func (o *OverlayStateDB) Finalise(deleteEmptyObjects bool) {
	o.inner.Finalise(deleteEmptyObjects)
}
