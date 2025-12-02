package shard

import (
	"encoding/json"
	"log"
	"math/big"
	"net/http"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"
)

type jsonRPCRequest struct {
	JSONRPC string            `json:"jsonrpc"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
	ID      interface{}       `json:"id"`
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type txArgs struct {
	From  common.Address  `json:"from"`
	To    *common.Address `json:"to"`
	Data  hexutil.Bytes   `json:"data"`
	Value *hexutil.Big    `json:"value"`
	Gas   *hexutil.Uint64 `json:"gas"`
}

const (
	DefaultDeployGas   = 3_000_000
	DefaultCallGas     = 1_000_000
	DefaultEstimateGas = 100_000
	ChainID            = 1337
)

func (s *Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var result interface{}
	var rpcErr *rpcError

	switch req.Method {
	case "eth_chainId":
		result = hexutil.Uint64(ChainID)

	case "eth_blockNumber":
		result = hexutil.Uint64(s.stateDB.blockNum)

	case "eth_getBalance":
		var addr common.Address
		if err := json.Unmarshal(req.Params[0], &addr); err != nil {
			rpcErr = &rpcError{-32602, "invalid address parameter"}
			break
		}
		bal := s.stateDB.GetBalance(addr)
		result = (*hexutil.Big)(bal)

	case "eth_getCode":
		var addr common.Address
		if err := json.Unmarshal(req.Params[0], &addr); err != nil {
			rpcErr = &rpcError{-32602, "invalid address parameter"}
			break
		}
		code := s.stateDB.GetCode(addr)
		result = hexutil.Bytes(code)

	case "eth_getTransactionCount":
		var addr common.Address
		if err := json.Unmarshal(req.Params[0], &addr); err != nil {
			rpcErr = &rpcError{-32602, "invalid address parameter"}
			break
		}
		nonce := s.stateDB.GetNonce(addr)
		result = hexutil.Uint64(nonce)

	case "eth_sendTransaction":
		var args txArgs
		if err := json.Unmarshal(req.Params[0], &args); err != nil {
			rpcErr = &rpcError{-32602, "invalid transaction parameters"}
			break
		}

		gas := uint64(DefaultDeployGas)
		if args.Gas != nil {
			gas = uint64(*args.Gas)
		}
		value := big.NewInt(0)
		if args.Value != nil {
			value = (*big.Int)(args.Value)
		}

		txID := uuid.New().String()
		txHash := common.BytesToHash([]byte(txID))

		var contractAddr *common.Address
		var returnData []byte
		var gasUsed uint64
		var logs []*types.Log
		var err error
		var status uint64

		if args.To == nil {
			// Deploy
			var addr common.Address
			addr, returnData, gasUsed, logs, err = s.stateDB.DeployContract(args.From, args.Data, value, gas)
			if err == nil {
				contractAddr = &addr
				status = 1
				log.Printf("Shard %d: Deployed %s (Tx: %s)", s.shardID, addr.Hex(), txHash.Hex())
			}
		} else {
			// Call
			returnData, gasUsed, logs, err = s.stateDB.CallContract(args.From, *args.To, args.Data, value, gas)
			if err == nil {
				status = 1
			}
		}

		if err != nil {
			rpcErr = &rpcError{-32000, err.Error()}
		} else {
			receipt := &Receipt{
				TxHash:            txHash,
				TransactionIndex:  0,
				BlockHash:         common.Hash{},
				BlockNumber:       (*hexutil.Big)(big.NewInt(int64(s.stateDB.blockNum))),
				From:              args.From,
				To:                args.To,
				GasUsed:           hexutil.Uint64(gasUsed),
				CumulativeGasUsed: hexutil.Uint64(gasUsed),
				ContractAddress:   contractAddr,
				Logs:              logs,
				Status:            hexutil.Uint64(status),
				ReturnData:        hexutil.Bytes(returnData),
			}
			s.receipts.AddReceipt(receipt)
			result = txHash.Hex()
		}

	case "eth_getTransactionReceipt":
		var hash common.Hash
		if err := json.Unmarshal(req.Params[0], &hash); err != nil {
			rpcErr = &rpcError{-32602, "invalid transaction hash"}
			break
		}
		receipt := s.receipts.GetReceipt(hash)
		if receipt != nil {
			result = receipt
		} else {
			result = nil
		}

	case "eth_getBlockByNumber":
		result = map[string]interface{}{
			"number":           hexutil.Uint64(s.stateDB.blockNum),
			"hash":             common.Hash{}.Hex(),
			"parentHash":       common.Hash{}.Hex(),
			"nonce":            "0x0000000000000000",
			"sha3Uncles":       common.Hash{}.Hex(),
			"logsBloom":        hexutil.Bytes(types.Bloom{}.Bytes()).String(),
			"transactionsRoot": common.Hash{}.Hex(),
			"stateRoot":        s.stateDB.GetStateRoot().Hex(),
			"receiptsRoot":     common.Hash{}.Hex(),
			"miner":            common.Address{}.Hex(),
			"difficulty":       "0x0",
			"totalDifficulty":  "0x0",
			"extraData":        "0x",
			"size":             "0x0",
			"gasLimit":         "0x1c9c380",
			"gasUsed":          "0x0",
			"timestamp":        hexutil.Uint64(s.stateDB.timestamp),
			"transactions":     []string{},
			"uncles":           []string{},
		}

	case "eth_call":
		var args txArgs
		if err := json.Unmarshal(req.Params[0], &args); err != nil {
			rpcErr = &rpcError{-32602, "invalid call parameters"}
			break
		}

		gas := uint64(DefaultCallGas)
		if args.Gas != nil {
			gas = uint64(*args.Gas)
		}

		if args.To == nil {
			rpcErr = &rpcError{-32000, "to address required"}
		} else {
			ret, _, err := s.stateDB.StaticCall(args.From, *args.To, args.Data, gas)
			if err != nil {
				rpcErr = &rpcError{-32000, err.Error()}
			} else {
				result = hexutil.Bytes(ret)
			}
		}

	case "eth_gasPrice":
		result = hexutil.Uint64(0)

	case "eth_estimateGas":
		result = hexutil.Uint64(DefaultEstimateGas)

	default:
		rpcErr = &rpcError{-32601, "method not found: " + req.Method}
	}

	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		Result:  result,
		Error:   rpcErr,
		ID:      req.ID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
