package main

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
	"github.com/sharding-experiment/sharding/config"
)

// Bytecodes for the contracts

const trainBookingBytecode = "0x6080604052348015600e575f5ffd5b506104638061001c5f395ff3fe608060405234801561000f575f5ffd5b5060043610610055575f3560e01c8063485cc439146100595780634a6e480e1461007757806350b447121461008157806387a362a4146100b157806394c2c24f146100cd575b5f5ffd5b6100616100eb565b60405161006e91906101f5565b60405180910390f35b61007f6100f0565b005b61009b6004803603810190610096919061023c565b610137565b6040516100a891906102a6565b60405180910390f35b6100cb60048036038101906100c691906102e9565b61016c565b005b6100d56101d7565b6040516100e291906101f5565b60405180910390f35b5f5481565b61012c5f5410610135576040517f08c379a000000000000000000000000000000000000000000000000000000000815260040161012c9061036e565b60405180910390fd5b565b60018161012c8110610147575f80fd5b015f915054906101000a900473ffffffffffffffffffffffffffffffffffffffff1681565b8060015f5f815480929190610180906103b9565b9190505561012c811061019657610195610400565b5b015f6101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff16021790555050565b61012c81565b5f819050919050565b6101ef816101dd565b82525050565b5f6020820190506102085f8301846101e6565b92915050565b5f5ffd5b61021b816101dd565b8114610225575f5ffd5b50565b5f8135905061023681610212565b92915050565b5f602082840312156102515761025061020e565b5b5f61025e84828501610228565b91505092915050565b5f73ffffffffffffffffffffffffffffffffffffffff82169050919050565b5f61029082610267565b9050919050565b6102a081610286565b82525050565b5f6020820190506102b95f830184610297565b92915050565b6102c881610286565b81146102d2575f5ffd5b50565b5f813590506102e3816102bf565b92915050565b5f602082840312156102fe576102fd61020e565b5b5f61030b848285016102d5565b91505092915050565b5f82825260208201905092915050565b7f4e6f206d6f7265207469636b657420617661696c61626c6500000000000000005f82015250565b5f610358601883610314565b915061036382610324565b602082019050919050565b5f6020820190508181035f8301526103858161034c565b9050919050565b7f4e487b71000000000000000000000000000000000000000000000000000000005f52601160045260245ffd5b5f6103c3826101dd565b91507fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff82036103f5576103f461038c565b5b600182019050919050565b7f4e487b71000000000000000000000000000000000000000000000000000000005f52603260045260245ffdfea26469706673582212203661167993d0c5918daee56e2dde779a7a72f4ee74ebbd82e97303025c1d54dc64736f6c634300081e0033"

const hotelBookingBytecode = "0x6080604052348015600e575f5ffd5b506104638061001c5f395ff3fe608060405234801561000f575f5ffd5b5060043610610055575f3560e01c80630e424b2b14610059578063165fcb2d146100635780631bae0ac81461007f578063427eaabb146100af578063463f5ce2146100cd575b5f5ffd5b6100616100eb565b005b61007d6004803603810190610078919061023b565b610132565b005b61009960048036038101906100949190610299565b61019d565b6040516100a691906102d3565b60405180910390f35b6100b76101d2565b6040516100c491906102fb565b60405180910390f35b6100d56101d8565b6040516100e291906102fb565b60405180910390f35b61012c5f5410610130576040517f08c379a00000000000000000000000000000000000000000000000000000000081526004016101279061036e565b60405180910390fd5b565b8060015f5f815480929190610146906103b9565b9190505561012c811061015c5761015b610400565b5b015f6101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff16021790555050565b60018161012c81106101ad575f80fd5b015f915054906101000a900473ffffffffffffffffffffffffffffffffffffffff1681565b61012c81565b5f5481565b5f5ffd5b5f73ffffffffffffffffffffffffffffffffffffffff82169050919050565b5f61020a826101e1565b9050919050565b61021a81610200565b8114610224575f5ffd5b50565b5f8135905061023581610211565b92915050565b5f602082840312156102505761024f6101dd565b5b5f61025d84828501610227565b91505092915050565b5f819050919050565b61027881610266565b8114610282575f5ffd5b50565b5f813590506102938161026f565b92915050565b5f602082840312156102ae576102ad6101dd565b5b5f6102bb84828501610285565b91505092915050565b6102cd81610200565b82525050565b5f6020820190506102e65f8301846102c4565b92915050565b6102f581610266565b82525050565b5f60208201905061030e5f8301846102ec565b92915050565b5f82825260208201905092915050565b7f4e6f206d6f726520726f6f6d20617661696c61626c65000000000000000000005f82015250565b5f610358601683610314565b915061036382610324565b602082019050919050565b5f6020820190508181035f8301526103858161034c565b9050919050565b7f4e487b71000000000000000000000000000000000000000000000000000000005f52601160045260245ffd5b5f6103c382610266565b91507fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff82036103f5576103f461038c565b5b600182019050919050565b7f4e487b71000000000000000000000000000000000000000000000000000000005f52603260045260245ffdfea26469706673582212203cf6fa93e2e40237c83ef8408ac14c8423d616929843be14e1dcd43083bd5ca964736f6c634300081e0033"

// Generic booking contract bytecode (PlaneBooking, TaxiBooking, YachtBooking, MovieBooking, RestaurantBooking)
// These all have the same interface: checkAvailability(), book(address), getBookedCount()
const genericBookingBytecode = "0x6080604052348015600e575f5ffd5b506104638061001c5f395ff3fe608060405234801561000f575f5ffd5b5060043610610055575f3560e01c80630e424b2b14610059578063165fcb2d146100635780631bae0ac81461007f578063427eaabb146100af578063463f5ce2146100cd575b5f5ffd5b6100616100eb565b005b61007d6004803603810190610078919061023b565b610132565b005b61009960048036038101906100949190610299565b61019d565b6040516100a691906102d3565b60405180910390f35b6100b76101d2565b6040516100c491906102fb565b60405180910390f35b6100d56101d8565b6040516100e291906102fb565b60405180910390f35b61012c5f5410610130576040517f08c379a00000000000000000000000000000000000000000000000000000000081526004016101279061036e565b60405180910390fd5b565b8060015f5f815480929190610146906103b9565b9190505561012c811061015c5761015b610400565b5b015f6101000a81548173ffffffffffffffffffffffffffffffffffffffff021916908373ffffffffffffffffffffffffffffffffffffffff16021790555050565b60018161012c81106101ad575f80fd5b015f915054906101000a900473ffffffffffffffffffffffffffffffffffffffff1681565b61012c81565b5f5481565b5f5ffd5b5f73ffffffffffffffffffffffffffffffffffffffff82169050919050565b5f61020a826101e1565b9050919050565b61021a81610200565b8114610224575f5ffd5b50565b5f8135905061023581610211565b92915050565b5f602082840312156102505761024f6101dd565b5b5f61025d84828501610227565b91505092915050565b5f819050919050565b61027881610266565b8114610282575f5ffd5b50565b5f813590506102938161026f565b92915050565b5f602082840312156102ae576102ad6101dd565b5b5f6102bb84828501610285565b91505092915050565b6102cd81610200565b82525050565b5f6020820190506102e65f8301846102c4565b92915050565b6102f581610266565b82525050565b5f60208201905061030e5f8301846102ec565b92915050565b5f82825260208201905092915050565b7f4e6f206d6f726520736c6f747320617661696c61626c650000000000000000005f82015250565b5f610358601783610314565b915061036382610324565b602082019050919050565b5f6020820190508181035f8301526103858161034c565b9050919050565b7f4e487b71000000000000000000000000000000000000000000000000000000005f52601160045260245ffd5b5f6103c382610266565b91507fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff82036103f5576103f461038c565b5b600182019050919050565b7f4e487b71000000000000000000000000000000000000000000000000000000005f52603260045260245ffdfea26469706673582212203cf6fa93e2e40237c83ef8408ac14c8423d616929843be14e1dcd43083bd5ca964736f6c634300081e0033"

// TravelAgency bytecode with 7-argument constructor (train, hotel, plane, taxi, yacht, movie, restaurant)
const travelAgencyBytecode = "0x60c060405234801561000f575f5ffd5b50604051610a8b380380610a8b833981810160405281019061003191906100fe565b8173ffffffffffffffffffffffffffffffffffffffff1660808173ffffffffffffffffffffffffffffffffffffffff16815250508073ffffffffffffffffffffffffffffffffffffffff1660a08173ffffffffffffffffffffffffffffffffffffffff1681525050505061013c565b5f5ffd5b5f73ffffffffffffffffffffffffffffffffffffffff82169050919050565b5f6100cd826100a4565b9050919050565b6100dd816100c3565b81146100e7575f5ffd5b50565b5f815190506100f8816100d4565b92915050565b5f5f60408385031215610114576101136100a0565b5b5f610121858286016100ea565b9250506020610132858286016100ea565b9150509250929050565b60805160a0516109136101785f395f81816101d801528181610478015261064b01525f8181608e01528181610322015261062701526109135ff3fe608060405234801561000f575f5ffd5b506004361061003f575f3560e01c80635710ddcd1461004357806384e786791461004d5780639af6c63a1461006b575b5f5ffd5b61004b610089565b005b610055610625565b60405161006291906106ac565b60405180910390f35b610073610649565b60405161008091906106ac565b60405180910390f35b5f5f5f7f000000000000000000000000000000000000000000000000000000000000000073ffffffffffffffffffffffffffffffffffffffff166040516024016040516020818303038152906040527f4a6e480e000000000000000000000000000000000000000000000000000000007bffffffffffffffffffffffffffffffffffffffffffffffffffffffff19166020820180517bffffffffffffffffffffffffffffffffffffffffffffffffffffffff83818316178352505050506040516101539190610717565b5f60405180830381855afa9150503d805f811461018b576040519150601f19603f3d011682016040523d82523d5f602084013e610190565b606091505b505080935050826101d6576040517f08c379a00000000000000000000000000000000000000000000000000000000081526004016101cd90610787565b60405180910390fd5b7f000000000000000000000000000000000000000000000000000000000000000073ffffffffffffffffffffffffffffffffffffffff166040516024016040516020818303038152906040527f0e424b2b000000000000000000000000000000000000000000000000000000007bffffffffffffffffffffffffffffffffffffffffffffffffffffffff19166020820180517bffffffffffffffffffffffffffffffffffffffffffffffffffffffff838183161783525050505060405161029d9190610717565b5f60405180830381855afa9150503d805f81146102d5576040519150601f19603f3d011682016040523d82523d5f602084013e6102da565b606091505b50508092505081610320576040517f08c379a0000000000000000000000000000000000000000000000000000000008152600401610317906107ef565b60405180910390fd5b7f000000000000000000000000000000000000000000000000000000000000000073ffffffffffffffffffffffffffffffffffffffff163360405160240161036891906106ac565b6040516020818303038152906040527f87a362a4000000000000000000000000000000000000000000000000000000007bffffffffffffffffffffffffffffffffffffffffffffffffffffffff19166020820180517bffffffffffffffffffffffffffffffffffffffffffffffffffffffff83818316178352505050506040516103f29190610717565b5f604051808303815f865af19150503d805f811461042b576040519150601f19603f3d011682016040523d82523d5f602084013e610430565b606091505b50508091505080610476576040517f08c379a000000000000000000000000000000000000000000000000000000000815260040161046d90610857565b60405180910390fd5b7f000000000000000000000000000000000000000000000000000000000000000073ffffffffffffffffffffffffffffffffffffffff16336040516024016104be91906106ac565b6040516020818303038152906040527f165fcb2d000000000000000000000000000000000000000000000000000000007bffffffffffffffffffffffffffffffffffffffffffffffffffffffff19166020820180517bffffffffffffffffffffffffffffffffffffffffffffffffffffffff83818316178352505050506040516105489190610717565b5f604051808303815f865af19150503d805f8114610581576040519150601f19603f3d011682016040523d82523d5f602084013e610586565b606091505b505080915050806105cc576040517f08c379a00000000000000000000000000000000000000000000000000000000081526004016105c3906108bf565b60405180910390fd5b60015f5f3373ffffffffffffffffffffffffffffffffffffffff1673ffffffffffffffffffffffffffffffffffffffff1681526020019081526020015f205f6101000a81548160ff021916908315150217905550505050565b7f000000000000000000000000000000000000000000000000000000000000000081565b7f000000000000000000000000000000000000000000000000000000000000000081565b5f73ffffffffffffffffffffffffffffffffffffffff82169050919050565b5f6106968261066d565b9050919050565b6106a68161068c565b82525050565b5f6020820190506106bf5f83018461069d565b92915050565b5f81519050919050565b5f81905092915050565b8281835e5f83830152505050565b5f6106f1826106c5565b6106fb81856106cf565b935061070b8185602086016106d9565b80840191505092915050565b5f61072282846106e7565b915081905092915050565b5f82825260208201905092915050565b7f547261696e2073656174206973206e6f7420617661696c61626c652e000000005f82015250565b5f610771601c8361072d565b915061077c8261073d565b602082019050919050565b5f6020820190508181035f83015261079e81610765565b9050919050565b7f486f74656c20726f6f6d206973206e6f7420617661696c61626c652e000000005f82015250565b5f6107d9601c8361072d565b91506107e4826107a5565b602082019050919050565b5f6020820190508181035f830152610806816107cd565b9050919050565b7f547261696e20626f6f6b696e67206661696c65642e00000000000000000000005f82015250565b5f61084160158361072d565b915061084c8261080d565b602082019050919050565b5f6020820190508181035f83015261086e81610835565b9050919050565b7f486f74656c20626f6f6b696e67206661696c65642e00000000000000000000005f82015250565b5f6108a960158361072d565b91506108b482610875565b602082019050919050565b5f6020820190508181035f8301526108d68161089d565b905091905056fea264697066735822122056af691efd63de244933240be323e45ac1d4eaac399a3ae95d3cb91420f79c7564736f6c634300081e0033"

func main() {

	// Create test_statedb directory
	err := os.MkdirAll("./storage/test_statedb/", 0755)
	if err != nil {
		panic(err)
	}

	cfg, err := config.LoadDefault()
	if err != nil {
		panic(err)
	}

	// Generate address files with deterministic addresses
	err = GenerateDeterministicAddresses(cfg.ShardNum, cfg.TestAccountNum)
	if err != nil {
		panic(err)
	}

	// Generate contract addresses for all booking contracts
	numContracts := cfg.NumContracts
	if numContracts <= 0 {
		numContracts = 10 // default
	}

	err = GenerateHotelAddresses(numContracts)
	if err != nil {
		panic(err)
	}

	err = GenerateTrainAddresses(numContracts)
	if err != nil {
		panic(err)
	}

	err = GeneratePlaneAddresses(numContracts)
	if err != nil {
		panic(err)
	}

	err = GenerateTaxiAddresses(numContracts)
	if err != nil {
		panic(err)
	}

	err = GenerateYachtAddresses(numContracts)
	if err != nil {
		panic(err)
	}

	err = GenerateMovieAddresses(numContracts)
	if err != nil {
		panic(err)
	}

	err = GenerateRestaurantAddresses(numContracts)
	if err != nil {
		panic(err)
	}

	err = GenerateTravelAddresses(numContracts)
	if err != nil {
		panic(err)
	}

	// Create storage for each shard
	for i := 0; i < cfg.ShardNum; i++ {
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

func GetHotelAddresses() []*common.Address {
	return getAddressesFromFile("./storage/hotelAddress.txt")
}

func GetTrainAddresses() []*common.Address {
	return getAddressesFromFile("./storage/trainAddress.txt")
}

func GetTravelAddresses() []*common.Address {
	return getAddressesFromFile("./storage/travelAddress.txt")
}

func GetPlaneAddresses() []*common.Address {
	return getAddressesFromFile("./storage/planeAddress.txt")
}

func GetTaxiAddresses() []*common.Address {
	return getAddressesFromFile("./storage/taxiAddress.txt")
}

func GetYachtAddresses() []*common.Address {
	return getAddressesFromFile("./storage/yachtAddress.txt")
}

func GetMovieAddresses() []*common.Address {
	return getAddressesFromFile("./storage/movieAddress.txt")
}

func GetRestaurantAddresses() []*common.Address {
	return getAddressesFromFile("./storage/restaurantAddress.txt")
}

func getAddressesFromFile(filename string) []*common.Address {
	addresses, err := os.ReadFile(filename)
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

// GenerateDeterministicAddresses creates accounts with encoded prefixes
// Format: 0x[S][C][T][O]...remaining 36 hex chars...
// Where:
//
//	[S] = Shard number (0-7)
//	[C] = Cross-shard flag (0 = local, 1 = cross-shard)
//	[T] = Transaction type (0 = send, 1 = contract)
//	[O] = Operation type (0 = send, 1 = read, 2 = write)
func GenerateDeterministicAddresses(shardNum, accountsPerType int) error {
	file, err := os.Create("./storage/address.txt")
	if err != nil {
		return err
	}
	defer file.Close()

	accountIndex := 0
	for shard := 0; shard < shardNum; shard++ {
		for crossShard := 0; crossShard <= 1; crossShard++ { // 0=local, 1=cross
			for txType := 0; txType <= 1; txType++ { // 0=send, 1=contract
				for opType := 0; opType <= 2; opType++ { // 0=send, 1=read, 2=write
					// Send tx only has opType=0
					if txType == 0 && opType != 0 {
						continue
					}
					for i := 0; i < accountsPerType; i++ {
						// Build prefix: shard + crossShard + txType + opType
						prefix := fmt.Sprintf("%d%d%d%d", shard, crossShard, txType, opType)
						// Generate deterministic suffix based on index
						suffix := generateDeterministicSuffix(shard, crossShard, txType, opType, i)
						addr := "0x" + prefix + suffix
						if _, err := fmt.Fprintln(file, addr); err != nil {
							return err
						}
						accountIndex++
					}
				}
			}
		}
	}
	fmt.Printf("Generated %d deterministic addresses\n", accountIndex)
	return nil
}

// generateDeterministicSuffix creates a deterministic 36-char hex suffix for address
func generateDeterministicSuffix(shard, crossShard, txType, opType, index int) string {
	seed := fmt.Sprintf("account-s%d-c%d-t%d-o%d-i%d", shard, crossShard, txType, opType, index)
	hash := sha256.Sum256([]byte(seed))
	// Take 18 bytes (36 hex chars) from hash
	return fmt.Sprintf("%x", hash[:18])
}

// Legacy function for backward compatibility
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

func GenerateHotelAddresses(numContracts int) error {
	return generateContractAddresses("./storage/hotelAddress.txt", "hotel", numContracts)
}

func GenerateTrainAddresses(numContracts int) error {
	return generateContractAddresses("./storage/trainAddress.txt", "train", numContracts)
}

func GeneratePlaneAddresses(numContracts int) error {
	return generateContractAddresses("./storage/planeAddress.txt", "plane", numContracts)
}

func GenerateTaxiAddresses(numContracts int) error {
	return generateContractAddresses("./storage/taxiAddress.txt", "taxi", numContracts)
}

func GenerateYachtAddresses(numContracts int) error {
	return generateContractAddresses("./storage/yachtAddress.txt", "yacht", numContracts)
}

func GenerateMovieAddresses(numContracts int) error {
	return generateContractAddresses("./storage/movieAddress.txt", "movie", numContracts)
}

func GenerateRestaurantAddresses(numContracts int) error {
	return generateContractAddresses("./storage/restaurantAddress.txt", "restaurant", numContracts)
}

func GenerateTravelAddresses(numContracts int) error {
	return generateContractAddresses("./storage/travelAddress.txt", "travel", numContracts)
}

func generateContractAddresses(filename, contractType string, numContracts int) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	cfg, err := config.LoadDefault()
	if err != nil {
		return err
	}

	// Generate contracts across shards (round-robin distribution)
	// Format: 0x[S][C][T][O]... where:
	//   [S] = shard (0-7)
	//   [C] = 1 (contracts are cross-shard accessible)
	//   [T] = 1 (contract)
	//   [O] = 2 (write operations)
	for i := 0; i < numContracts; i++ {
		shard := i % cfg.ShardNum
		// Prefix: shard + cross(1) + contract(1) + write(2)
		prefix := fmt.Sprintf("%d112", shard)
		suffix := generateContractSuffix(contractType, shard, i)
		addr := "0x" + prefix + suffix
		if _, err := fmt.Fprintln(file, addr); err != nil {
			return err
		}
	}
	return nil
}

// generateContractSuffix creates a deterministic 36-char hex suffix for contract address
func generateContractSuffix(contractType string, shard, index int) string {
	seed := fmt.Sprintf("contract-%s-s%d-i%d", contractType, shard, index)
	hash := sha256.Sum256([]byte(seed))
	// Take 18 bytes (36 hex chars) from hash
	return fmt.Sprintf("%x", hash[:18])
}

func CreateStorage(shardID int) {
	cfg, err := config.LoadDefault()
	if err != nil {
		panic(err)
	}

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
		// For deterministic addresses, shard is determined by first hex digit after 0x
		addrHex := address.Hex()[2:] // Remove "0x"
		shardDigit := int(addrHex[0] - '0')
		if shardDigit%cfg.ShardNum == shardID {
			stateDB.SetBalance(*address, uint256.NewInt(1e18+200000), tracing.BalanceChangeUnspecified)
			stateDB.SetNonce(*address, 0, tracing.NonceChangeUnspecified)
		}
	}

	// Chain config with Shanghai enabled (for PUSH0 opcode support)
	chainCfg := &params.ChainConfig{
		ChainID:             big.NewInt(1337),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
		ShanghaiTime:        new(uint64), // Enable Shanghai at time 0
	}

	// Create EVM instance for contract deployment
	blockContext := vm.BlockContext{
		CanTransfer: func(db vm.StateDB, addr common.Address, amount *uint256.Int) bool {
			return db.GetBalance(addr).Cmp(amount) >= 0
		},
		Transfer: func(db vm.StateDB, from, to common.Address, amount *uint256.Int) {
			db.SubBalance(from, amount, tracing.BalanceChangeTransfer)
			db.AddBalance(to, amount, tracing.BalanceChangeTransfer)
		},
		GetHash:     func(n uint64) common.Hash { return common.Hash{} },
		Coinbase:    common.Address{},
		BlockNumber: big.NewInt(0),
		Time:        0,
		Difficulty:  big.NewInt(0),
		GasLimit:    100000000,
		BaseFee:     big.NewInt(0),
		Random:      &common.Hash{},
	}
	evm := vm.NewEVM(blockContext, stateDB, chainCfg, vm.Config{})
	evm.TxContext = vm.TxContext{
		Origin:   common.Address{},
		GasPrice: big.NewInt(0),
	}

	// Deployer account for EVM.Create
	deployer := common.HexToAddress("0x9999999999999999999999999999999999999999")
	stateDB.SetBalance(deployer, uint256.NewInt(1e18), tracing.BalanceChangeUnspecified)

	// Set contract accounts using EVM.Create to get runtime bytecode
	// Required contracts (train, hotel)
	setContractAccounts(evm, stateDB, deployer, GetHotelAddresses(), cfg.ShardNum, shardID, "hotel")
	setContractAccounts(evm, stateDB, deployer, GetTrainAddresses(), cfg.ShardNum, shardID, "train")

	// Optional contracts (plane, taxi, yacht, movie, restaurant)
	setContractAccounts(evm, stateDB, deployer, GetPlaneAddresses(), cfg.ShardNum, shardID, "plane")
	setContractAccounts(evm, stateDB, deployer, GetTaxiAddresses(), cfg.ShardNum, shardID, "taxi")
	setContractAccounts(evm, stateDB, deployer, GetYachtAddresses(), cfg.ShardNum, shardID, "yacht")
	setContractAccounts(evm, stateDB, deployer, GetMovieAddresses(), cfg.ShardNum, shardID, "movie")
	setContractAccounts(evm, stateDB, deployer, GetRestaurantAddresses(), cfg.ShardNum, shardID, "restaurant")

	// TravelAgency (depends on all others)
	setContractAccounts(evm, stateDB, deployer, GetTravelAddresses(), cfg.ShardNum, shardID, "travel")

	fmt.Printf("Set Account for shard %v\n", shardID)
	// EVM Create fn / stateDB 가지고 놀기
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

func setContractAccounts(evm *vm.EVM, stateDB *state.StateDB, deployer common.Address, addresses []*common.Address, numShards, shardID int, contractType string) {
	// Load all addresses once for pairing (only for TravelAgency)
	var allTrainAddrs, allHotelAddrs, allTravelAddrs []*common.Address
	var allPlaneAddrs, allTaxiAddrs, allYachtAddrs, allMovieAddrs, allRestaurantAddrs []*common.Address
	if contractType == "travel" {
		allTrainAddrs = GetTrainAddresses()
		allHotelAddrs = GetHotelAddresses()
		allTravelAddrs = GetTravelAddresses()
		allPlaneAddrs = GetPlaneAddresses()
		allTaxiAddrs = GetTaxiAddresses()
		allYachtAddrs = GetYachtAddresses()
		allMovieAddrs = GetMovieAddresses()
		allRestaurantAddrs = GetRestaurantAddresses()
	}

	// Get the creation bytecode for this contract type
	var creationBytecode []byte
	switch contractType {
	case "train":
		creationBytecode = common.FromHex(trainBookingBytecode)
	case "hotel":
		creationBytecode = common.FromHex(hotelBookingBytecode)
	case "plane", "taxi", "yacht", "movie", "restaurant":
		creationBytecode = common.FromHex(genericBookingBytecode)
	case "travel":
		creationBytecode = common.FromHex(travelAgencyBytecode)
	}

	for _, address := range addresses {
		// Shard determined by first hex digit after 0x
		addrHex := address.Hex()[2:] // Remove "0x"
		shardDigit := int(addrHex[0] - '0')
		if shardDigit%numShards != shardID {
			continue
		}

		var deployCode []byte
		if contractType == "travel" {
			// Find index of this TravelAgency address
			index := -1
			for i, travelAddr := range allTravelAddrs {
				if travelAddr.Hex() == address.Hex() {
					index = i
					break
				}
			}
			if index == -1 || index >= len(allTrainAddrs) || index >= len(allHotelAddrs) {
				panic(fmt.Sprintf("No matching Train/Hotel for TravelAgency at index %d", index))
			}
			// Get deploy code with constructor arguments (all 7 addresses)
			trainAddrHex := allTrainAddrs[index].Hex()[2:] // Remove "0x" prefix
			hotelAddrHex := allHotelAddrs[index].Hex()[2:]
			planeAddrHex := getAddressOrZero(allPlaneAddrs, index)
			taxiAddrHex := getAddressOrZero(allTaxiAddrs, index)
			yachtAddrHex := getAddressOrZero(allYachtAddrs, index)
			movieAddrHex := getAddressOrZero(allMovieAddrs, index)
			restaurantAddrHex := getAddressOrZero(allRestaurantAddrs, index)

			deployCode = GetDeployCode("travel", []byte(common.Bytes2Hex(creationBytecode)),
				trainAddrHex, hotelAddrHex, planeAddrHex, taxiAddrHex, yachtAddrHex, movieAddrHex, restaurantAddrHex)
		} else {
			deployCode = GetDeployCode(contractType, []byte(common.Bytes2Hex(creationBytecode)), "", "", "", "", "", "", "")
		}

		// Execute EVM.Create to run constructor and get runtime bytecode
		runtimeCode, _, _, err := evm.Create(deployer, deployCode, 10000000, uint256.NewInt(0))
		if err != nil {
			panic(fmt.Sprintf("Failed to create %s contract: %v", contractType, err))
		}

		// Set the runtime bytecode to our predetermined address
		stateDB.SetCode(*address, runtimeCode, tracing.CodeChangeUnspecified)
		stateDB.SetNonce(*address, 1, tracing.NonceChangeUnspecified)

		fmt.Printf("Shard %d: Deployed %s contract at %s\n", shardID, contractType, address.Hex())
	}
}

func getAddressOrZero(addrs []*common.Address, index int) string {
	if index < len(addrs) {
		return addrs[index].Hex()[2:] // Remove "0x" prefix
	}
	return "0000000000000000000000000000000000000000" // Zero address
}

func GetDeployCode(contractName string, code []byte, arg1, arg2, arg3, arg4, arg5, arg6, arg7 string) []byte {
	switch contractName {
	case "train", "hotel", "plane", "taxi", "yacht", "movie", "restaurant":
		// These contracts have no constructor arguments
		return hexutil.MustDecode("0x" + string(code))
	case "travel":
		// Travel: 생성자가 7개 주소 받음 (train, hotel, plane, taxi, yacht, movie, restaurant)
		// ABI 인코딩: 주소는 32바이트(64 hex)로 패딩 필요
		return hexutil.MustDecode("0x" + string(code) +
			"000000000000000000000000" + arg1 + // Train 주소 (32바이트 패딩)
			"000000000000000000000000" + arg2 + // Hotel 주소 (32바이트 패딩)
			"000000000000000000000000" + arg3 + // Plane 주소 (32바이트 패딩)
			"000000000000000000000000" + arg4 + // Taxi 주소 (32바이트 패딩)
			"000000000000000000000000" + arg5 + // Yacht 주소 (32바이트 패딩)
			"000000000000000000000000" + arg6 + // Movie 주소 (32바이트 패딩)
			"000000000000000000000000" + arg7) // Restaurant 주소 (32바이트 패딩)
	}
	return nil
}
