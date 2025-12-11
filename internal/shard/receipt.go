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
	// Store a copy to avoid aliasing caller's data
	s.receipts[r.TxHash] = r.DeepCopy()
}

func (s *ReceiptStore) GetReceipt(hash common.Hash) *Receipt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.receipts[hash]
	if r == nil {
		return nil
	}
	// Return a copy to avoid aliasing internal data
	return r.DeepCopy()
}

// DeepCopy creates a deep copy of the Receipt
func (r *Receipt) DeepCopy() *Receipt {
	if r == nil {
		return nil
	}

	result := &Receipt{
		TxHash:            r.TxHash,
		TransactionIndex:  r.TransactionIndex,
		BlockHash:         r.BlockHash,
		From:              r.From,
		CumulativeGasUsed: r.CumulativeGasUsed,
		GasUsed:           r.GasUsed,
		LogsBloom:         r.LogsBloom,
		Status:            r.Status,
	}

	// Copy BlockNumber
	if r.BlockNumber != nil {
		bn := *r.BlockNumber
		result.BlockNumber = &bn
	}

	// Copy To
	if r.To != nil {
		to := *r.To
		result.To = &to
	}

	// Copy ContractAddress
	if r.ContractAddress != nil {
		ca := *r.ContractAddress
		result.ContractAddress = &ca
	}

	// Copy Logs
	if r.Logs != nil {
		result.Logs = make([]*types.Log, len(r.Logs))
		for i, log := range r.Logs {
			if log != nil {
				logCopy := *log
				// Copy Topics slice
				if log.Topics != nil {
					logCopy.Topics = make([]common.Hash, len(log.Topics))
					copy(logCopy.Topics, log.Topics)
				}
				// Copy Data slice
				if log.Data != nil {
					logCopy.Data = make([]byte, len(log.Data))
					copy(logCopy.Data, log.Data)
				}
				result.Logs[i] = &logCopy
			}
		}
	}

	// Copy ReturnData
	if r.ReturnData != nil {
		result.ReturnData = make(hexutil.Bytes, len(r.ReturnData))
		copy(result.ReturnData, r.ReturnData)
	}

	return result
}
