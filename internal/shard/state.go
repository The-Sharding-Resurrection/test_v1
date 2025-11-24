package shard

import (
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common"
)

// LockManager tracks locked funds for 2PC cross-shard transactions
type LockManager struct {
	mu     sync.RWMutex
	locked map[string]*LockedFunds // txID -> locked funds
}

type LockedFunds struct {
	Address common.Address
	Amount  *big.Int
}

func NewLockManager() *LockManager {
	return &LockManager{
		locked: make(map[string]*LockedFunds),
	}
}

// Lock records locked funds for a transaction
func (l *LockManager) Lock(txID string, addr common.Address, amount *big.Int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.locked[txID] = &LockedFunds{
		Address: addr,
		Amount:  new(big.Int).Set(amount),
	}
}

// Get retrieves locked funds for a transaction
func (l *LockManager) Get(txID string) (*LockedFunds, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	lock, ok := l.locked[txID]
	return lock, ok
}

// Clear removes locked funds record
func (l *LockManager) Clear(txID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.locked, txID)
}
