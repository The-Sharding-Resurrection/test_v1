package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
	"github.com/sharding-experiment/sharding/config"
)

const SHARD_NUM = 6

func main() {
	// Create test_statedb directory
	err := os.MkdirAll("./storage/test_statedb/", 0755)
	if err != nil {
		panic(err)
	}

	// Generate address.txt file with deterministic addresses
	err = GenerateAddresses()
	if err != nil {
		panic(err)
	}

	// Create storage for each shard
	for i := 0; i < SHARD_NUM; i++ {
		CreateStorage(i)
	}
}

func GetAddresses() []*common.Address {
	addresses, err := os.ReadFile("./storage/address.txt")
	if err != nil {
		panic(err)
	}
	addressesInString := strings.Split(string(addresses), "\n")
	addressArray := make([]*common.Address, 0)
	for i := 0; i < len(addressesInString); i++ {
		addrStr := strings.TrimSpace(addressesInString[i])
		if addrStr == "" {
			continue
		}
		stringtoaddress := common.HexToAddress(addrStr)
		addressArray = append(addressArray, &stringtoaddress)
	}
	return addressArray
}

func GenerateAddresses() error {
	file, err := os.Create("./storage/address.txt")
	if err != nil {
		return err
	}
	defer file.Close()

	cfg, err := config.LoadDefault()
	if err != nil {
		return err
	}

	for i := 0; i < cfg.TestAccountNum; i++ {
		seed := fmt.Sprintf("shard-test-account-%d", i)
		hash := sha256.Sum256([]byte(seed))
		addr := common.BytesToAddress(hash[:])
		if _, err := fmt.Fprintln(file, addr.Hex()); err != nil {
			return err
		}
	}
	return nil
}

func CreateStorage(shardID int) {

	leveldb, err := leveldb.New("./storage/test_statedb/"+strconv.Itoa(shardID), 128, 1024, "", false)
	if err != nil {
		panic(err)
	}

	rdb := rawdb.NewDatabase(leveldb)
	tdb := triedb.NewDatabase(rdb, nil)
	sdb := state.NewDatabase(tdb, nil)
	stateDB, err := state.New(types.EmptyRootHash, sdb)

	if err != nil {
		panic(err)
	}

	addresses := GetAddresses()
	for _, address := range addresses {
		if int(address[len(address)-1])%SHARD_NUM == shardID {
			stateDB.SetBalance(*address, uint256.NewInt(1e18+200000), tracing.BalanceChangeUnspecified)
			stateDB.SetNonce(*address, 0, tracing.NonceChangeUnspecified)
		}
	}

	fmt.Printf("Set Account for shard %v\n", shardID)

	root, err := stateDB.Commit(0, true, false)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Commit Root: %v\n", root)

	file, err := os.Create(fmt.Sprintf("./storage/test_statedb/shard%v_root.txt", shardID))
	if err != nil {
		panic(err)
	}
	defer file.Close()

	if _, err = file.WriteString(root.Hex()); err != nil {
		panic(err)
	}

	if err := tdb.Commit(root, false); err != nil {
		panic(err)
	}
	leveldb.Close()

}
