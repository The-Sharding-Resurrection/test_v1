package orchestrator

import (
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/stateless"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/trie/utils"
	"github.com/holiman/uint256"
	"github.com/sharding-experiment/sharding/internal/protocol"
)

// NumShards is the total number of shards in the system
// Used for address-to-shard mapping
const NumShards = 8

// SimulationStateDB implements vm.StateDB for cross-shard transaction simulation.
// It fetches state on-demand from State Shards and tracks all reads/writes for RwSet construction.
//
// Error Handling Strategy:
// The vm.StateDB interface methods (GetBalance, GetNonce, GetState, etc.) do not return errors.
// When a fetch fails, errors are collected in fetchErrors and the method returns a zero/empty value.
// After EVM execution completes, the caller MUST check HasFetchErrors() to detect failures.
// This design allows the EVM to continue executing (collecting partial RwSet data for debugging)
// while ensuring the simulation ultimately fails if any required state couldn't be fetched.
type SimulationStateDB struct {
	mu           sync.RWMutex
	txID         string
	fetcher      *StateFetcher

	// Cached account state (fetched on first access)
	accounts     map[common.Address]*accountState

	// Track reads and writes for RwSet construction
	reads        map[common.Address]map[common.Hash]common.Hash // addr -> slot -> value read
	writes       map[common.Address]map[common.Hash]common.Hash // addr -> slot -> new value written
	writeOlds    map[common.Address]map[common.Hash]common.Hash // addr -> slot -> old value before write

	// Access list for EIP-2929
	accessList   *accessList

	// Transaction logs
	logs         []*types.Log

	// Refund counter
	refund       uint64

	// Snapshots for revert
	snapshots    []snapshot

	// Transient storage (EIP-1153)
	transient    map[common.Address]map[common.Hash]common.Hash

	// Track fetch errors - if any fetch fails, simulation should abort
	fetchErrors []error
}

type accountState struct {
	Balance         *uint256.Int
	OriginalBalance *uint256.Int // Balance when first fetched (for tracking changes)
	Nonce           uint64
	Code            []byte
	CodeHash        common.Hash
	ShardID         int
	Destructed      bool
	Created         bool
}

type snapshot struct {
	accounts  map[common.Address]*accountState
	reads     map[common.Address]map[common.Hash]common.Hash
	writes    map[common.Address]map[common.Hash]common.Hash
	refund    uint64
	logsLen   int
}

type accessList struct {
	addresses map[common.Address]int
	slots     []map[common.Hash]struct{}
}

func newAccessList() *accessList {
	return &accessList{
		addresses: make(map[common.Address]int),
	}
}

func (al *accessList) AddAddress(addr common.Address) bool {
	if _, ok := al.addresses[addr]; ok {
		return false
	}
	al.addresses[addr] = -1
	return true
}

func (al *accessList) AddSlot(addr common.Address, slot common.Hash) (bool, bool) {
	idx, addrPresent := al.addresses[addr]
	if !addrPresent {
		al.addresses[addr] = len(al.slots)
		slotMap := make(map[common.Hash]struct{})
		slotMap[slot] = struct{}{}
		al.slots = append(al.slots, slotMap)
		return true, true
	}
	if idx == -1 {
		al.addresses[addr] = len(al.slots)
		slotMap := make(map[common.Hash]struct{})
		slotMap[slot] = struct{}{}
		al.slots = append(al.slots, slotMap)
		return false, true
	}
	if _, ok := al.slots[idx][slot]; ok {
		return false, false
	}
	al.slots[idx][slot] = struct{}{}
	return false, true
}

func (al *accessList) ContainsAddress(addr common.Address) bool {
	_, ok := al.addresses[addr]
	return ok
}

func (al *accessList) Contains(addr common.Address, slot common.Hash) (bool, bool) {
	idx, addrOk := al.addresses[addr]
	if !addrOk || idx == -1 {
		return addrOk, false
	}
	_, slotOk := al.slots[idx][slot]
	return addrOk, slotOk
}

// NewSimulationStateDB creates a new StateDB for simulation
func NewSimulationStateDB(txID string, fetcher *StateFetcher) *SimulationStateDB {
	return &SimulationStateDB{
		txID:       txID,
		fetcher:    fetcher,
		accounts:   make(map[common.Address]*accountState),
		reads:      make(map[common.Address]map[common.Hash]common.Hash),
		writes:     make(map[common.Address]map[common.Hash]common.Hash),
		writeOlds:  make(map[common.Address]map[common.Hash]common.Hash),
		accessList: newAccessList(),
		logs:       nil,
		transient:  make(map[common.Address]map[common.Hash]common.Hash),
	}
}

// getOrFetchAccount gets account from cache or fetches from shard.
// Uses double-checked locking to reduce lock contention on cache hits.
// The TOCTOU gap between check and fetch is safe because EVM execution is single-threaded,
// meaning only one goroutine accesses this StateDB at a time per simulation.
func (s *SimulationStateDB) getOrFetchAccount(addr common.Address) (*accountState, error) {
	s.mu.RLock()
	if acct, ok := s.accounts[addr]; ok {
		s.mu.RUnlock()
		return acct, nil
	}
	s.mu.RUnlock()

	// Fetch state without holding lock (read-only HTTP call for simulation)
	shardID := s.fetcher.AddressToShard(addr)
	fetchResp, err := s.fetcher.FetchState(s.txID, shardID, addr)
	if err != nil {
		// Record the error - simulation should fail
		s.mu.Lock()
		s.fetchErrors = append(s.fetchErrors, err)
		// Return empty account but DON'T cache it (so we don't mask the error)
		acct := &accountState{
			Balance:         uint256.NewInt(0),
			OriginalBalance: uint256.NewInt(0),
			Nonce:           0,
			Code:            nil,
			CodeHash:        common.Hash{},
			ShardID:         shardID,
		}
		s.mu.Unlock()
		return acct, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check again after acquiring write lock
	if acct, ok := s.accounts[addr]; ok {
		return acct, nil
	}

	balance := uint256.NewInt(0)
	if fetchResp.Balance != nil {
		balance = uint256.MustFromBig(fetchResp.Balance)
	}

	codeHash := fetchResp.CodeHash
	if len(fetchResp.Code) == 0 {
		codeHash = common.Hash{}
	} else if codeHash == (common.Hash{}) {
		codeHash = crypto.Keccak256Hash(fetchResp.Code)
	}

	acct := &accountState{
		Balance:         balance,
		OriginalBalance: new(uint256.Int).Set(balance), // Store original for change detection
		Nonce:           fetchResp.Nonce,
		Code:            fetchResp.Code,
		CodeHash:        codeHash,
		ShardID:         shardID,
	}
	s.accounts[addr] = acct
	return acct, nil
}

// vm.StateDB implementation

func (s *SimulationStateDB) CreateAccount(addr common.Address) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[addr] = &accountState{
		Balance:         uint256.NewInt(0),
		OriginalBalance: uint256.NewInt(0),
		Nonce:           0,
		Code:            nil,
		CodeHash:        common.Hash{},
		ShardID:         s.fetcher.AddressToShard(addr),
		Created:         true,
	}
}

func (s *SimulationStateDB) CreateContract(addr common.Address) {
	s.CreateAccount(addr)
}

func (s *SimulationStateDB) GetBalance(addr common.Address) *uint256.Int {
	acct, _ := s.getOrFetchAccount(addr)
	return new(uint256.Int).Set(acct.Balance)
}

func (s *SimulationStateDB) SubBalance(addr common.Address, amount *uint256.Int, reason tracing.BalanceChangeReason) uint256.Int {
	acct, _ := s.getOrFetchAccount(addr)
	prev := *acct.Balance
	s.mu.Lock()
	acct.Balance.Sub(acct.Balance, amount)
	s.mu.Unlock()
	return prev
}

func (s *SimulationStateDB) AddBalance(addr common.Address, amount *uint256.Int, reason tracing.BalanceChangeReason) uint256.Int {
	acct, _ := s.getOrFetchAccount(addr)
	prev := *acct.Balance
	s.mu.Lock()
	acct.Balance.Add(acct.Balance, amount)
	s.mu.Unlock()
	return prev
}

func (s *SimulationStateDB) GetNonce(addr common.Address) uint64 {
	acct, _ := s.getOrFetchAccount(addr)
	return acct.Nonce
}

func (s *SimulationStateDB) SetNonce(addr common.Address, nonce uint64, reason tracing.NonceChangeReason) {
	acct, _ := s.getOrFetchAccount(addr)
	s.mu.Lock()
	acct.Nonce = nonce
	s.mu.Unlock()
}

func (s *SimulationStateDB) GetCodeHash(addr common.Address) common.Hash {
	acct, _ := s.getOrFetchAccount(addr)
	if len(acct.Code) == 0 {
		return common.Hash{}
	}
	return acct.CodeHash
}

func (s *SimulationStateDB) GetCode(addr common.Address) []byte {
	acct, _ := s.getOrFetchAccount(addr)
	if acct.Code == nil {
		return nil
	}
	// Return a copy to avoid aliasing internal data
	result := make([]byte, len(acct.Code))
	copy(result, acct.Code)
	return result
}

func (s *SimulationStateDB) SetCode(addr common.Address, code []byte, reason tracing.CodeChangeReason) []byte {
	acct, _ := s.getOrFetchAccount(addr)
	s.mu.Lock()
	prev := acct.Code
	// Copy the new code to avoid aliasing caller's data
	if code != nil {
		acct.Code = make([]byte, len(code))
		copy(acct.Code, code)
	} else {
		acct.Code = nil
	}
	if len(code) > 0 {
		acct.CodeHash = crypto.Keccak256Hash(code)
	} else {
		acct.CodeHash = common.Hash{}
	}
	s.mu.Unlock()
	// Return a copy of prev to avoid aliasing
	if prev == nil {
		return nil
	}
	result := make([]byte, len(prev))
	copy(result, prev)
	return result
}

func (s *SimulationStateDB) GetCodeSize(addr common.Address) int {
	return len(s.GetCode(addr))
}

func (s *SimulationStateDB) AddRefund(gas uint64) {
	s.mu.Lock()
	s.refund += gas
	s.mu.Unlock()
}

func (s *SimulationStateDB) SubRefund(gas uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gas > s.refund {
		// Clamp to zero instead of panicking - prevents crashes during simulation
		s.refund = 0
	} else {
		s.refund -= gas
	}
}

func (s *SimulationStateDB) GetRefund() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.refund
}

func (s *SimulationStateDB) GetState(addr common.Address, slot common.Hash) common.Hash {
	// First check writes
	s.mu.RLock()
	if writes, ok := s.writes[addr]; ok {
		if val, ok := writes[slot]; ok {
			s.mu.RUnlock()
			return val
		}
	}
	// Then check reads cache
	if reads, ok := s.reads[addr]; ok {
		if val, ok := reads[slot]; ok {
			s.mu.RUnlock()
			return val
		}
	}
	s.mu.RUnlock()

	// Fetch from shard
	acct, _ := s.getOrFetchAccount(addr)
	val, err := s.fetcher.GetStorageAt(s.txID, acct.ShardID, addr, slot)
	if err != nil {
		val = common.Hash{}
	}

	// Cache the read
	s.mu.Lock()
	if s.reads[addr] == nil {
		s.reads[addr] = make(map[common.Hash]common.Hash)
	}
	s.reads[addr][slot] = val
	s.mu.Unlock()

	return val
}

func (s *SimulationStateDB) GetStateAndCommittedState(addr common.Address, slot common.Hash) (common.Hash, common.Hash) {
	// For simulation, committed state is the original read value
	current := s.GetState(addr, slot)

	s.mu.RLock()
	if reads, ok := s.reads[addr]; ok {
		if committed, ok := reads[slot]; ok {
			s.mu.RUnlock()
			return current, committed
		}
	}
	s.mu.RUnlock()

	return current, current
}

func (s *SimulationStateDB) SetState(addr common.Address, slot common.Hash, value common.Hash) common.Hash {
	prev := s.GetState(addr, slot)

	s.mu.Lock()
	// Record old value on first write to this slot
	if s.writeOlds[addr] == nil {
		s.writeOlds[addr] = make(map[common.Hash]common.Hash)
	}
	if _, alreadyWritten := s.writeOlds[addr][slot]; !alreadyWritten {
		s.writeOlds[addr][slot] = prev
	}

	// Record new value
	if s.writes[addr] == nil {
		s.writes[addr] = make(map[common.Hash]common.Hash)
	}
	s.writes[addr][slot] = value
	s.mu.Unlock()

	return prev
}

func (s *SimulationStateDB) GetStorageRoot(addr common.Address) common.Hash {
	// Not implemented for simulation
	return common.Hash{}
}

func (s *SimulationStateDB) GetTransientState(addr common.Address, key common.Hash) common.Hash {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if storage, ok := s.transient[addr]; ok {
		return storage[key]
	}
	return common.Hash{}
}

func (s *SimulationStateDB) SetTransientState(addr common.Address, key, value common.Hash) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transient[addr] == nil {
		s.transient[addr] = make(map[common.Hash]common.Hash)
	}
	s.transient[addr][key] = value
}

func (s *SimulationStateDB) SelfDestruct(addr common.Address) uint256.Int {
	acct, _ := s.getOrFetchAccount(addr)
	s.mu.Lock()
	prev := *acct.Balance
	acct.Destructed = true
	acct.Balance = uint256.NewInt(0)
	s.mu.Unlock()
	return prev
}

func (s *SimulationStateDB) HasSelfDestructed(addr common.Address) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if acct, ok := s.accounts[addr]; ok {
		return acct.Destructed
	}
	return false
}

func (s *SimulationStateDB) SelfDestruct6780(addr common.Address) (uint256.Int, bool) {
	acct, _ := s.getOrFetchAccount(addr)
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := *acct.Balance
	if acct.Created {
		acct.Destructed = true
		acct.Balance = uint256.NewInt(0)
		return prev, true
	}
	return prev, false
}

func (s *SimulationStateDB) Exist(addr common.Address) bool {
	acct, _ := s.getOrFetchAccount(addr)
	return acct.Nonce > 0 || acct.Balance.Sign() > 0 || len(acct.Code) > 0
}

func (s *SimulationStateDB) Empty(addr common.Address) bool {
	acct, _ := s.getOrFetchAccount(addr)
	return acct.Nonce == 0 && acct.Balance.Sign() == 0 && len(acct.Code) == 0
}

func (s *SimulationStateDB) AddressInAccessList(addr common.Address) bool {
	return s.accessList.ContainsAddress(addr)
}

func (s *SimulationStateDB) SlotInAccessList(addr common.Address, slot common.Hash) (bool, bool) {
	return s.accessList.Contains(addr, slot)
}

func (s *SimulationStateDB) AddAddressToAccessList(addr common.Address) {
	s.accessList.AddAddress(addr)
}

func (s *SimulationStateDB) AddSlotToAccessList(addr common.Address, slot common.Hash) {
	s.accessList.AddSlot(addr, slot)
}

func (s *SimulationStateDB) PointCache() *utils.PointCache {
	return nil
}

func (s *SimulationStateDB) Prepare(rules params.Rules, sender, coinbase common.Address, dest *common.Address, precompiles []common.Address, txAccesses types.AccessList) {
	s.accessList = newAccessList()
	s.accessList.AddAddress(sender)
	if dest != nil {
		s.accessList.AddAddress(*dest)
	}
	s.accessList.AddAddress(coinbase)
	for _, addr := range precompiles {
		s.accessList.AddAddress(addr)
	}
	for _, el := range txAccesses {
		s.accessList.AddAddress(el.Address)
		for _, key := range el.StorageKeys {
			s.accessList.AddSlot(el.Address, key)
		}
	}
}

func (s *SimulationStateDB) RevertToSnapshot(revid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if revid >= len(s.snapshots) {
		return
	}
	snap := s.snapshots[revid]
	s.accounts = snap.accounts
	s.reads = snap.reads
	s.writes = snap.writes
	s.refund = snap.refund
	s.logs = s.logs[:snap.logsLen]
	s.snapshots = s.snapshots[:revid]
}

func (s *SimulationStateDB) Snapshot() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Deep copy accounts
	accountsCopy := make(map[common.Address]*accountState)
	for addr, acct := range s.accounts {
		acctCopy := *acct
		if acct.Balance != nil {
			acctCopy.Balance = new(uint256.Int).Set(acct.Balance)
		}
		if acct.OriginalBalance != nil {
			acctCopy.OriginalBalance = new(uint256.Int).Set(acct.OriginalBalance)
		}
		if acct.Code != nil {
			acctCopy.Code = make([]byte, len(acct.Code))
			copy(acctCopy.Code, acct.Code)
		}
		accountsCopy[addr] = &acctCopy
	}

	// Deep copy reads
	readsCopy := make(map[common.Address]map[common.Hash]common.Hash)
	for addr, slots := range s.reads {
		slotsCopy := make(map[common.Hash]common.Hash)
		for k, v := range slots {
			slotsCopy[k] = v
		}
		readsCopy[addr] = slotsCopy
	}

	// Deep copy writes
	writesCopy := make(map[common.Address]map[common.Hash]common.Hash)
	for addr, slots := range s.writes {
		slotsCopy := make(map[common.Hash]common.Hash)
		for k, v := range slots {
			slotsCopy[k] = v
		}
		writesCopy[addr] = slotsCopy
	}

	s.snapshots = append(s.snapshots, snapshot{
		accounts: accountsCopy,
		reads:    readsCopy,
		writes:   writesCopy,
		refund:   s.refund,
		logsLen:  len(s.logs),
	})

	return len(s.snapshots) - 1
}

func (s *SimulationStateDB) AddLog(log *types.Log) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, log)
}

func (s *SimulationStateDB) AddPreimage(hash common.Hash, preimage []byte) {
	// Not needed for simulation
}

func (s *SimulationStateDB) Witness() *stateless.Witness {
	return nil
}

func (s *SimulationStateDB) AccessEvents() *state.AccessEvents {
	return nil
}

func (s *SimulationStateDB) Finalise(deleteEmptyObjects bool) {
	// No finalization needed for simulation
}

// BuildRwSet constructs the RwSet from tracked reads, writes, and balance changes
func (s *SimulationStateDB) BuildRwSet() []protocol.RwVariable {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Collect all addresses that were read, written, or had balance changes
	addrSet := make(map[common.Address]bool)
	for addr := range s.reads {
		addrSet[addr] = true
	}
	for addr := range s.writes {
		addrSet[addr] = true
	}
	// Include accounts with balance changes (for simple transfers)
	for addr, acct := range s.accounts {
		if acct.OriginalBalance != nil && acct.Balance.Cmp(acct.OriginalBalance) != 0 {
			addrSet[addr] = true
		}
	}

	var rwSet []protocol.RwVariable
	for addr := range addrSet {
		acct := s.accounts[addr]
		shardID := 0
		if acct != nil {
			shardID = acct.ShardID
		} else {
			shardID = s.fetcher.AddressToShard(addr)
		}

		var readSet []protocol.ReadSetItem
		if reads, ok := s.reads[addr]; ok {
			for slot, value := range reads {
				readSet = append(readSet, protocol.ReadSetItem{
					Slot:  protocol.Slot(slot),
					Value: value.Bytes(),
					Proof: nil, // PoC: skip Merkle proofs
				})
			}
		}

		var writeSet []protocol.WriteSetItem
		if writes, ok := s.writes[addr]; ok {
			for slot, newVal := range writes {
				oldVal := common.Hash{}
				if olds, ok := s.writeOlds[addr]; ok {
					if v, ok := olds[slot]; ok {
						oldVal = v
					}
				}
				writeSet = append(writeSet, protocol.WriteSetItem{
					Slot:     protocol.Slot(slot),
					OldValue: oldVal.Bytes(),
					NewValue: newVal.Bytes(),
				})
			}
		}

		rwSet = append(rwSet, protocol.RwVariable{
			Address: addr,
			ReferenceBlock: protocol.Reference{
				ShardNum: shardID,
			},
			ReadSet:  readSet,
			WriteSet: writeSet,
		})
	}

	return rwSet
}

// GetLogs returns the transaction logs
func (s *SimulationStateDB) GetLogs() []*types.Log {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.logs == nil {
		return nil
	}
	// Return a copy to avoid aliasing internal slice
	result := make([]*types.Log, len(s.logs))
	copy(result, s.logs)
	return result
}

// HasFetchErrors returns true if any state fetch failed during simulation
func (s *SimulationStateDB) HasFetchErrors() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.fetchErrors) > 0
}

// GetFetchErrors returns all fetch errors that occurred during simulation
func (s *SimulationStateDB) GetFetchErrors() []error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.fetchErrors == nil {
		return nil
	}
	// Return a copy to avoid aliasing internal slice
	result := make([]error, len(s.fetchErrors))
	copy(result, s.fetchErrors)
	return result
}

