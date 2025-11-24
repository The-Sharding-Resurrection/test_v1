package shard

import (
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

// Receipt represents the result of a transaction execution
type Receipt struct {
	TxHash            common.Hash     `json:"transactionHash"`
	TransactionIndex  hexutil.Uint64  `json:"transactionIndex"`
	BlockHash         common.Hash     `json:"blockHash"`
	BlockNumber       *hexutil.Big    `json:"blockNumber"`
	From              common.Address  `json:"from"`
	To                *common.Address `json:"to"`
	CumulativeGasUsed hexutil.Uint64  `json:"cumulativeGasUsed"`
	GasUsed           hexutil.Uint64  `json:"gasUsed"`
	ContractAddress   *common.Address `json:"contractAddress"`
	Logs              []*types.Log    `json:"logs"`
	LogsBloom         types.Bloom     `json:"logsBloom"`
	Status            hexutil.Uint64  `json:"status"`
	ReturnData        hexutil.Bytes   `json:"returnData"` // Custom field for debugging
}

// ReceiptStore manages transaction receipts in memory
type ReceiptStore struct {
	receipts map[common.Hash]*Receipt
	mu       sync.RWMutex
}

func NewReceiptStore() *ReceiptStore {
	return &ReceiptStore{
		receipts: make(map[common.Hash]*Receipt),
	}
}

func (s *ReceiptStore) AddReceipt(r *Receipt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receipts[r.TxHash] = r
}

func (s *ReceiptStore) GetReceipt(hash common.Hash) *Receipt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.receipts[hash]
}
