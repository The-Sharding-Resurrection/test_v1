package evm

import (
	"fmt"
	"math"
	"math/big"
	"os"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/holiman/uint256"
	"github.com/sharding-experiment/sharding/evm/core"
	"github.com/sharding-experiment/sharding/evm/types"
	"github.com/sharding-experiment/sharding/evm/vm/runtime"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/triedb"
)

const SHANGHAI_BLOCK_COUNT int64 = 16830000

func NewMemoryState() (*state.StateDB, error) {
	return state.New(types.EmptyRootHash, state.NewDatabase(triedb.NewDatabase(rawdb.NewMemoryDatabase(), nil), nil))
}

func NewTestState(shardID int) (*state.StateDB, error) {
	shardStateRoot, err := os.ReadFile(fmt.Sprintf("/storage/test_statedb/shard%v_root.txt", shardID))
	if err != nil {
		return nil, err
	}

	ldbObject, err := leveldb.New("/storage/test_statedb/"+strconv.Itoa(shardID), 128, 1024, "", false)
	if err != nil {
		return nil, err
	}
	rdb := rawdb.NewDatabase(ldbObject)
	tdb := triedb.NewDatabase(rdb, nil)
	sdb := state.NewDatabase(tdb, nil)

	return state.New(common.HexToHash(string(shardStateRoot)), sdb)
}

func Transfer(statedb *state.StateDB, sender common.Address, recipient common.Address, amount *big.Int) error {
	// convert value to *uint256.Int
	value, _ := uint256.FromBig(amount)
	if core.CanTransfer(statedb, sender, value) {
		core.Transfer(statedb, sender, recipient, value)
		statedb.SetNonce(sender, statedb.GetNonce(sender)+1, tracing.NonceChangeUnspecified)
		return nil
	} else {
		return fmt.Errorf("not enough user balance")
	}
}

// Credit adds balance (used for cross-shard receives)
func Credit(statedb *state.StateDB, recipient common.Address, amount *big.Int) {
	value, _ := uint256.FromBig(amount)
	statedb.AddBalance(recipient, value, tracing.BalanceChangeUnspecified)
}

// CanDebit checks if an address has sufficient available balance
// Available = balance - lockedAmount
func CanDebit(statedb *state.StateDB, addr common.Address, amount *big.Int, lockedAmount *big.Int) bool {
	balance := statedb.GetBalance(addr).ToBig()
	available := new(big.Int).Sub(balance, lockedAmount)
	return available.Cmp(amount) >= 0
}

// LockFunds for cross-shard (deduct but track in separate map)
// This needs to be coordinated with the Server's lock tracking
func Debit(statedb *state.StateDB, sender common.Address, amount *big.Int) error {
	value, _ := uint256.FromBig(amount)
	if core.CanTransfer(statedb, sender, value) {
		statedb.SubBalance(sender, value, tracing.BalanceChangeUnspecified)
		statedb.SetNonce(sender, statedb.GetNonce(sender)+1, tracing.NonceChangeUnspecified)
	} else {
		return fmt.Errorf("not enough user balance")
	}

	return nil
}

func SetConfig(blockheight int64, stateDB *state.StateDB, random_value string) *runtime.Config {
	cfg := new(runtime.Config)
	if cfg.Difficulty == nil {
		cfg.Difficulty = big.NewInt(0)
	}
	if cfg.GasLimit == 0 {
		cfg.GasLimit = math.MaxUint64
	}
	if cfg.GasPrice == nil {
		cfg.GasPrice = new(big.Int)
	}
	if cfg.Value == nil {
		cfg.Value = new(big.Int)
	}
	if cfg.GetHashFn == nil {
		cfg.GetHashFn = func(n uint64) common.Hash {
			return common.BytesToHash(crypto.Keccak256([]byte(new(big.Int).SetUint64(n).String())))
		}
	}
	if cfg.BaseFee == nil {
		cfg.BaseFee = big.NewInt(params.InitialBaseFee)
	}
	if cfg.BlobBaseFee == nil {
		cfg.BlobBaseFee = big.NewInt(params.BlobTxMinBlobGasprice)
	}

	cfg.ChainConfig = params.SepoliaChainConfig

	// Decide evm version
	// EVM version is must more than shanghai because recent solidity compile has PUSH0 opcode
	// cfg.BlockNumber = big.NewInt(0)
	cfg.BlockNumber = big.NewInt(int64(SHANGHAI_BLOCK_COUNT) + int64(blockheight))

	cfg.Time = uint64(time.Now().Unix())
	random := common.BytesToHash([]byte(random_value))
	cfg.Random = &random
	cfg.State = stateDB

	return cfg
}

func DeployContract(blockheight uint64, statedb *state.StateDB, deployer common.Address, bytecode []byte, amount *big.Int, gas uint64) (common.Address, []byte, uint64, error) {
	value, _ := uint256.FromBig(amount)
	cfg := SetConfig(int64(blockheight), statedb, string(rune(time.Now().Unix())))
	evm := runtime.NewEnv(cfg)
	ret, address, leftOverGas, err := evm.Create(
		deployer,
		bytecode,
		gas,
		value,
	)
	if err != nil {
		return common.Address{0}, nil, 0, err
	}

	return address, ret, leftOverGas, nil
}

func CallContract(blockheight uint64, statedb *state.StateDB, caller common.Address, contract_address common.Address, input []byte, amount *big.Int, gas uint64) ([]byte, uint64, error) {
	value, _ := uint256.FromBig(amount)
	cfg := SetConfig(int64(blockheight), statedb, string(rune(time.Now().Unix())))
	evm := runtime.NewEnv(cfg)

	ret, leftOverGas, err := evm.Call(
		caller,
		contract_address,
		input,
		gas,
		value,
	)
	if err != nil {
		return nil, 0, err
	}

	return ret, leftOverGas, nil
}

func StaticCall(blockheight uint64, statedb *state.StateDB, caller common.Address, contract_address common.Address, input []byte, gas uint64) ([]byte, uint64, error) {
	cfg := SetConfig(int64(blockheight), statedb, string(rune(time.Now().Unix())))
	evm := runtime.NewEnv(cfg)

	ret, leftOverGas, err := evm.StaticCall(
		caller,
		contract_address,
		input,
		gas,
	)
	if err != nil {
		return nil, 0, err
	}

	return ret, leftOverGas, nil
}
