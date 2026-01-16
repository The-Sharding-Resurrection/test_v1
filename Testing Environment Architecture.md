## Plan: Testing Environment Architecture

**TL;DR**: Design a unified Python-based benchmarking framework that measures TPS and latency while varying key parameters (CT ratio, shard count, injection rate, data skewness, involved shards per TX). A single `benchmark.py` script will be driven by an extended `config.json` to enable knob-style parameter tuning.

### Steps

1. **Extend config.json** to include all benchmark parameters: CT ratio, injection rate, skewness (θ), involved shards range, and test duration.

2. **Create scripts/benchmark.py** as the unified benchmark script that orchestrates workload generation, transaction submission, metric collection, and result output.

3. **Implement workload generators** in `benchmark.py`: Zipfian distribution for account selection, configurable CT vs local ratio, and variable involved-shard counts per transaction.

4. **Add metric collection** using timestamps around transaction submission and completion polling to calculate TPS and latency percentiles (p50, p95, p99).

5. **Output results** in CSV/JSON format suitable for graphing, with columns for all X-axis parameters and Y-axis metrics.

### Further Considerations

1. **Should we support dynamic shard scaling?** Currently 6 shards are fixed at Docker Compose level—changing shard count requires compose file changes. Option A: Parameterize docker-compose / Option B: Test only with fixed shards / Option C: Create compose templates.

2. **Concurrent submission vs sequential?** High injection rates require parallel submission—should we use `asyncio` or `ThreadPoolExecutor`? Recommend: ThreadPoolExecutor for simplicity.

3. **Warm-up and cool-down periods?** Standard benchmarks exclude initial/final periods from measurements. Should we add configurable warm-up time? Recommend: Yes, 10-second default.

---

Here's the detailed README for the testing environment architecture:

---

# Benchmark Testing Environment for Ethereum State & Transaction Sharding

## Overview

This document describes the architecture for a standardized performance testing environment. The benchmark framework measures **throughput (TPS)** and **latency** across multiple dimensions of the sharding system.

## Metrics (Y-Axis)

| Metric | Description | Unit |
|--------|-------------|------|
| **TPS** | Transactions per second (committed) | tx/s |
| **Latency (p50)** | Median transaction completion time | ms |
| **Latency (p95)** | 95th percentile latency | ms |
| **Latency (p99)** | 99th percentile latency | ms |
| **Abort Rate** | Percentage of transactions aborted | % |
| **Local TPS** | TPS for local-only transactions | tx/s |
| **Cross-Shard TPS** | TPS for cross-shard transactions | tx/s |

## Parameters (X-Axis)

| Parameter             | Description                                      | Range                               |
| --------------------- | ------------------------------------------------ | ----------------------------------- |
| **CT Ratio**          | Cross-shard transaction ratio vs local           | 0.0 - 1.0                           |
| **Send/Contract Ratio** | Balance transfer vs Contract call ratio        | 0.0 (all send) - 1.0 (all contract) |
| **Read/Write Ratio**  | Read-only vs Write contract calls                | 0.0 (all read) - 1.0 (all write)    |
| **Shard Count**       | Number of state shards                           | 4, 6, 8                             |
| **Injection Rate**    | Transactions submitted per second                | 10 - 1000 tx/s                      |
| **Skewness (θ)**      | Zipfian distribution parameter                   | 0.0 (uniform) - 0.9 (highly skewed) |
| **Involved Shards**   | Number of shards touched per cross-shard TX      | 3 - 8 (must be ≤ Shard Count)       |

### Transaction Type Hierarchy

```
All Transactions
├── Local Transactions (1 - CT Ratio)
│   ├── Send (1 - Send/Contract Ratio)
│   └── Contract (Send/Contract Ratio)
│       ├── Read (1 - Read/Write Ratio)
│       └── Write (Read/Write Ratio)
│
└── Cross-Shard Transactions (CT Ratio)
    ├── Send (1 - Send/Contract Ratio)
    └── Contract (Send/Contract Ratio)
        ├── Read (1 - Read/Write Ratio)
        └── Write (Read/Write Ratio)
```

### Involved Shards & Contract Mapping

Cross-shard contract transactions use the **TravelAgency** pattern with configurable booking contracts:

| Contract              | Role      | Required |
| --------------------- | --------- | -------- |
| TravelAgency.sol      | Orchestrator | Yes (always) |
| TrainBooking.sol      | Default   | Yes (always) |
| HotelBooking.sol      | Default   | Yes (always) |
| PlaneBooking.sol      | Optional  | If involved_shards ≥ 4 |
| TaxiBooking.sol       | Optional  | If involved_shards ≥ 5 |
| YachtBooking.sol      | Optional  | If involved_shards ≥ 6 |
| MovieBooking.sol      | Optional  | If involved_shards ≥ 7 |
| RestaurantBooking.sol | Optional  | If involved_shards ≥ 8 |

**Validation Rule**: `involved_shards` must be ≤ `shard_count`. Configuration will revert if violated.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                      config.json                                │
│  (All benchmark parameters: CT ratio, injection rate, etc.)     │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                      benchmark.py                               │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐  │
│  │ Config      │  │ Workload    │  │ Metric Collector        │  │
│  │ Loader      │  │ Generator   │  │ (TPS, Latency, Aborts)  │  │
│  └─────────────┘  └─────────────┘  └─────────────────────────┘  │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐  │
│  │ TX          │  │ Account     │  │ Result Exporter         │  │
│  │ Submitter   │  │ Selector    │  │ (CSV/JSON)              │  │
│  │ (Parallel)  │  │ (Zipfian)   │  │                         │  │
│  └─────────────┘  └─────────────┘  └─────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Sharding Network                             │
│   ┌─────────────┐    ┌─────────┐ ┌─────────┐ ┌─────────┐       │
│   │ Orchestrator│◄──►│ Shard 0 │ │ Shard 1 │ │ Shard N │       │
│   │   (2PC)     │    └─────────┘ └─────────┘ └─────────┘       │
│   └─────────────┘                                               │
└─────────────────────────────────────────────────────────────────┘
```

## Configuration Schema

Extended `config.json` structure:

```json
{
  "shard_num": 6,
  "test_account_num": 100,
  "storage_dir": "/storage/test_statedb/",
  
  "benchmark": {
    "enabled": true,
    "duration_seconds": 60,
    "warmup_seconds": 10,
    "cooldown_seconds": 5,
    
    "workload": {
      "ct_ratio": 0.5,
      "send_contract_ratio": 0.5,
      "read_write_ratio": 0.5,
      "injection_rate": 100,
      "skewness_theta": 0.0,
      "involved_shards": 3
    },
    
    "output": {
      "format": "csv",
      "file": "results/benchmark_results.csv",
      "include_raw_latencies": false
    },
    
    "sweep": {
      "enabled": false,
      "parameter": "ct_ratio",
      "values": [0.0, 0.25, 0.5, 0.75, 1.0]
    }
  }
}
```

**Validation Rules**:
- `involved_shards` must be in range [3, 8]
- `involved_shards` must be ≤ `shard_num` (reverts otherwise)
- All ratios must be in range [0.0, 1.0]

## Component Design

### 1. Config Loader

Reads `config.json` and provides typed access to all parameters. Validates parameter ranges.

```python
@dataclass
class BenchmarkConfig:
    duration_seconds: int
    warmup_seconds: int
    ct_ratio: float              # 0.0 - 1.0 (local vs cross-shard)
    send_contract_ratio: float   # 0.0 - 1.0 (send vs contract)
    read_write_ratio: float      # 0.0 - 1.0 (read vs write for contracts)
    injection_rate: int          # tx/s target
    skewness_theta: float        # Zipfian θ
    involved_shards: int         # 3-8, must be ≤ shard_num
    shard_num: int
    
    def validate(self):
        if self.involved_shards > self.shard_num:
            raise ValueError(f"involved_shards ({self.involved_shards}) must be <= shard_num ({self.shard_num})")
        if not (3 <= self.involved_shards <= 8):
            raise ValueError(f"involved_shards must be in range [3, 8]")
```

### 2. Workload Generator

Generates transactions based on configuration:

- **CT Ratio**: `random() < ct_ratio` → cross-shard, else local
- **Send/Contract Ratio**: `random() < send_contract_ratio` → contract call, else balance transfer
- **Read/Write Ratio**: For contract calls, `random() < read_write_ratio` → write, else read
- **Zipfian Distribution**: Account selection with configurable skewness
- **Involved Shards**: Select N shards (3 mandatory + optional contracts)

```python
class WorkloadGenerator:
    def __init__(self, config: BenchmarkConfig, accounts: Dict[str, List[str]]):
        self.zipf = ZipfianGenerator(len(accounts), config.skewness_theta)
        self.config = config
        self.accounts = accounts  # Keyed by prefix pattern
    
    def generate_tx(self) -> Transaction:
        # Step 1: Local or Cross-shard?
        is_cross_shard = random.random() < self.config.ct_ratio
        
        # Step 2: Send or Contract?
        is_contract = random.random() < self.config.send_contract_ratio
        
        # Step 3: If contract, Read or Write?
        is_write = is_contract and (random.random() < self.config.read_write_ratio)
        
        # Step 4: Select account based on type
        account = self.select_account(is_cross_shard, is_contract, is_write)
        
        # Step 5: Build transaction
        if is_contract:
            return self.build_contract_tx(account, is_cross_shard, is_write)
        else:
            return self.build_send_tx(account, is_cross_shard)
```

### 3. Transaction Submitter

Parallel submission using ThreadPoolExecutor:

```python
class TxSubmitter:
    def __init__(self, network: ShardNetwork, max_workers: int = 32):
        self.executor = ThreadPoolExecutor(max_workers=max_workers)
    
    def submit_batch(self, txs: List[Transaction]) -> List[Future]:
        return [self.executor.submit(self._submit_one, tx) for tx in txs]
```

### 4. Metric Collector

Records timestamps and calculates statistics:

```python
@dataclass
class TxMetric:
    tx_id: str
    tx_type: str  # "local" or "cross_shard"
    submit_time: float
    complete_time: float
    status: str  # "committed" or "aborted"
    involved_shards: int

class MetricCollector:
    def calculate_results(self) -> BenchmarkResults:
        latencies = [m.complete_time - m.submit_time for m in self.metrics]
        return BenchmarkResults(
            tps=len(committed) / duration,
            latency_p50=np.percentile(latencies, 50),
            latency_p95=np.percentile(latencies, 95),
            latency_p99=np.percentile(latencies, 99),
            abort_rate=len(aborted) / len(total),
        )
```

### 5. Zipfian Distribution

Implementation for data skewness:

```python
class ZipfianGenerator:
    """
    Zipfian distribution with parameter θ (theta).
    θ = 0: uniform distribution
    θ = 0.9: highly skewed (few accounts get most transactions)
    """
    def __init__(self, num_items: int, theta: float):
        self.num_items = num_items
        self.theta = theta
        self._precompute_cdf()
    
    def next(self) -> int:
        # Return index following Zipfian distribution
        ...
```

### 6. Account Generation (Deterministic)

Accounts are generated deterministically with human-readable prefixes that encode their purpose.

#### Address Format

```
0x[S][C][T][O]...remaining 36 hex chars...

Where:
  [S] = Shard number (0-7)
  [C] = Cross-shard flag (0 = local, 1 = cross-shard)
  [T] = Transaction type (0 = send, 1 = contract)
  [O] = Operation type (0 = send, 1 = read, 2 = write)
```

#### Examples

| Address                                      | Meaning                                           |
| -------------------------------------------- | ------------------------------------------------- |
| `0x0000...` | Shard 0, Local, Send, Send operation              |
| `0x0011...` | Shard 0, Local, Contract, Read operation          |
| `0x0012...` | Shard 0, Local, Contract, Write operation         |
| `0x4100...` | Shard 4, Cross-shard, Send, Send operation        |
| `0x5111...` | Shard 5, Cross-shard, Contract, Read operation    |
| `0x3112...` | Shard 3, Cross-shard, Contract, Write operation   |

#### Generation in `create_storage.go`

```go
// GenerateDeterministicAccounts creates accounts with encoded prefixes
func GenerateDeterministicAccounts(shardNum, accountsPerType int) []common.Address {
    var accounts []common.Address
    
    for shard := 0; shard < shardNum; shard++ {
        for crossShard := 0; crossShard <= 1; crossShard++ {     // 0=local, 1=cross
            for txType := 0; txType <= 1; txType++ {             // 0=send, 1=contract
                for opType := 0; opType <= 2; opType++ {         // 0=send, 1=read, 2=write
                    if txType == 0 && opType != 0 {
                        continue // Send tx only has opType=0
                    }
                    for i := 0; i < accountsPerType; i++ {
                        // Build prefix: shard + crossShard + txType + opType
                        prefix := fmt.Sprintf("%d%d%d%d", shard, crossShard, txType, opType)
                        // Generate deterministic suffix based on index
                        suffix := generateSuffix(shard, crossShard, txType, opType, i)
                        addr := common.HexToAddress("0x" + prefix + suffix)
                        accounts = append(accounts, addr)
                    }
                }
            }
        }
    }
    return accounts
}
```

#### Classification in `benchmark.py`

```python
def classify_account(addr: str) -> dict:
    """
    Parse account address to determine its transaction properties.
    
    Args:
        addr: Ethereum address string (0x...)
    
    Returns:
        dict with keys: shard, is_cross_shard, is_contract, operation
    """
    # Strip 0x prefix
    hex_part = addr[2:]
    
    shard = int(hex_part[0])
    is_cross_shard = hex_part[1] == '1'
    is_contract = hex_part[2] == '1'
    
    op_code = hex_part[3]
    if op_code == '0':
        operation = 'send'
    elif op_code == '1':
        operation = 'read'
    else:
        operation = 'write'
    
    return {
        'shard': shard,
        'is_cross_shard': is_cross_shard,
        'is_contract': is_contract,
        'operation': operation
    }

# Usage in WorkloadGenerator
def select_account(self, is_cross_shard: bool, is_contract: bool, is_write: bool) -> str:
    """Select account matching the desired transaction profile."""
    cross_flag = '1' if is_cross_shard else '0'
    tx_flag = '1' if is_contract else '0'
    
    if not is_contract:
        op_flag = '0'  # Send
    elif is_write:
        op_flag = '2'  # Write
    else:
        op_flag = '1'  # Read
    
    # Build prefix pattern and select from matching accounts
    prefix_pattern = f"{cross_flag}{tx_flag}{op_flag}"
    matching_accounts = self.accounts_by_pattern[prefix_pattern]
    
    # Use Zipfian distribution to select within matching accounts
    idx = self.zipf.next() % len(matching_accounts)
    return matching_accounts[idx]
```

## Usage

### Single Run

```bash
# Edit config.json to set parameters
vim config/config.json

# Run benchmark
python scripts/benchmark.py

# Results in results/benchmark_results.csv
```

### Parameter Sweep

```bash
# Enable sweep in config.json
# "sweep": {"enabled": true, "parameter": "ct_ratio", "values": [0.0, 0.5, 1.0]}

python scripts/benchmark.py --sweep

# Generates results for each value
```

### CLI Override

```bash
# Override specific parameters from command line
python scripts/benchmark.py --ct-ratio 0.5 --injection-rate 200 --duration 120
```

## Output Format

### CSV Output

```csv
timestamp,ct_ratio,send_contract_ratio,read_write_ratio,shard_count,injection_rate,skewness,involved_shards,tps,latency_p50,latency_p95,latency_p99,abort_rate,local_tps,cross_shard_tps,send_tps,contract_tps
2026-01-16T10:00:00,0.5,0.5,0.5,6,100,0.0,3,85.2,156,342,520,0.02,95.1,75.3,90.2,80.1
```

### JSON Output

```json
{
  "config": {
    "ct_ratio": 0.5,
    "send_contract_ratio": 0.5,
    "read_write_ratio": 0.5,
    "shard_count": 6,
    "injection_rate": 100,
    "skewness_theta": 0.0,
    "involved_shards": 3
  },
  "results": {
    "tps": 85.2,
    "latency_p50_ms": 156,
    "latency_p95_ms": 342,
    "latency_p99_ms": 520,
    "abort_rate": 0.02,
    "total_committed": 5112,
    "total_aborted": 104,
    "by_type": {
      "local_send_tps": 48.1,
      "local_contract_read_tps": 23.5,
      "local_contract_write_tps": 23.5,
      "cross_shard_send_tps": 38.2,
      "cross_shard_contract_read_tps": 18.4,
      "cross_shard_contract_write_tps": 18.5
    }
  }
}
```

## File Structure

```
scripts/
├── benchmark.py          # Main benchmark script (NEW)
├── client.py             # Existing ShardNetwork client
├── test_travel_booking.py
└── ...

config/
├── config.json           # Extended with benchmark parameters

contracts/src/
├── TravelAgency.sol      # Updated to support all booking contracts
├── TrainBooking.sol      # Default (required)
├── HotelBooking.sol      # Default (required)
├── PlaneBooking.sol      # Optional (NEW)
├── TaxiBooking.sol       # Optional (NEW)
├── YachtBooking.sol      # Optional (NEW)
├── MovieBooking.sol      # Optional (NEW)
├── RestaurantBooking.sol # Optional (NEW)
└── ...

storage/
├── create_storage.go     # Updated for deterministic account generation
├── address.txt           # Generated accounts with encoded prefixes
├── travelAddress.txt
├── trainAddress.txt
├── hotelAddress.txt
├── planeAddress.txt      # NEW
├── taxiAddress.txt       # NEW
├── yachtAddress.txt      # NEW
├── movieAddress.txt      # NEW
├── restaurantAddress.txt # NEW
└── ...

results/                  # Output directory (NEW)
├── benchmark_results.csv
├── raw_latencies.json    # Optional detailed data
└── plots/                # Generated graphs
```

## Smart Contract Architecture

### TravelAgency.sol (Updated)

The TravelAgency contract orchestrates bookings across multiple service contracts:

```solidity
// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

contract TravelAgency {
    // Required booking contracts (must always succeed)
    address public trainBooking;
    address public hotelBooking;
    
    // Optional booking contracts (only checked if provided)
    address public planeBooking;
    address public taxiBooking;
    address public yachtBooking;
    address public movieBooking;
    address public restaurantBooking;
    
    struct BookingRequest {
        bool bookTrain;      // Required - must be true
        bool bookHotel;      // Required - must be true
        bool bookPlane;      // Optional
        bool bookTaxi;       // Optional
        bool bookYacht;      // Optional
        bool bookMovie;      // Optional
        bool bookRestaurant; // Optional
    }
    
    function bookTrip(BookingRequest calldata request) external returns (bool) {
        // Required bookings - must succeed
        require(request.bookTrain, "Train booking required");
        require(request.bookHotel, "Hotel booking required");
        require(_bookTrain(), "Train booking failed");
        require(_bookHotel(), "Hotel booking failed");
        
        // Optional bookings - only check if requested
        if (request.bookPlane) {
            require(_bookPlane(), "Plane booking failed");
        }
        if (request.bookTaxi) {
            require(_bookTaxi(), "Taxi booking failed");
        }
        if (request.bookYacht) {
            require(_bookYacht(), "Yacht booking failed");
        }
        if (request.bookMovie) {
            require(_bookMovie(), "Movie booking failed");
        }
        if (request.bookRestaurant) {
            require(_bookRestaurant(), "Restaurant booking failed");
        }
        
        return true;
    }
    
    // Read-only function for read transactions
    function checkAvailability(BookingRequest calldata request) external view returns (bool) {
        // Check availability without state changes
        ...
    }
}
```

### Optional Booking Contracts (New)

Each optional contract follows the same pattern as TrainBooking.sol and HotelBooking.sol:

```solidity
// PlaneBooking.sol, TaxiBooking.sol, YachtBooking.sol, 
// MovieBooking.sol, RestaurantBooking.sol
contract XxxBooking {
    uint256 public bookedCount;
    
    function book() external returns (bool) {
        bookedCount++;
        return true;
    }
    
    function checkAvailability() external view returns (bool) {
        return true;
    }
    
    function getBookedCount() external view returns (uint256) {
        return bookedCount;
    }
}
```

## Benchmark Scenarios

### Scenario 1: CT Ratio Impact

Measure how cross-shard ratio affects throughput.

```json
{"sweep": {"parameter": "ct_ratio", "values": [0.0, 0.2, 0.4, 0.6, 0.8, 1.0]}}
```

### Scenario 2: Scalability (Shard Count)

Requires modifying docker-compose for different shard counts.

```json
{"sweep": {"parameter": "shard_count", "values": [2, 4, 6, 8]}}
```

### Scenario 3: Saturation (Injection Rate)

Find maximum TPS before latency degrades.

```json
{"sweep": {"parameter": "injection_rate", "values": [50, 100, 200, 400, 800]}}
```

### Scenario 4: Skewness Impact

Test hotspot scenarios with Zipfian distribution.

```json
{"sweep": {"parameter": "skewness_theta", "values": [0.0, 0.3, 0.6, 0.9]}}
```

### Scenario 5: TX Complexity (Involved Shards)

Vary number of shards per cross-shard transaction.

```json
{"workload": {"involved_shards": 3}}  // Train + Hotel + TravelAgency only
{"workload": {"involved_shards": 5}}  // + Plane + Taxi
{"workload": {"involved_shards": 8}}  // All 8 contracts
```

**Note**: `involved_shards` must be ≤ `shard_num`. Configuration reverts if violated.

### Scenario 6: Transaction Type Mix

Vary the ratio of Send vs Contract transactions.

```json
{"sweep": {"parameter": "send_contract_ratio", "values": [0.0, 0.25, 0.5, 0.75, 1.0]}}
```

### Scenario 7: Contract Operation Type

Vary the ratio of Read vs Write contract operations.

```json
{"sweep": {"parameter": "read_write_ratio", "values": [0.0, 0.25, 0.5, 0.75, 1.0]}}
```

## Implementation Notes

1. **Rate Limiting**: Use token bucket or sleep-based rate limiting to achieve target injection rate
2. **Completion Tracking**: Poll orchestrator for cross-shard TX status, track local TX via immediate response
3. **Account Setup**: Pre-fund deterministic accounts via faucet before benchmark starts
4. **Block Timing**: Cross-shard TXs require ~2 block rounds (6+ seconds) due to 2PC protocol
5. **Thread Safety**: Use thread-safe queues for metric collection from parallel submitters
6. **Account Classification**: Parse address prefix to determine TX type (no random selection)
7. **Involved Shards Validation**: Reject config where `involved_shards > shard_num`
8. **Contract Deployment**: Deploy all booking contracts before benchmark, store addresses in storage/*.txt

## Dependencies

```
numpy          # Percentile calculations
dataclasses    # Config structures  
concurrent.futures  # Parallel submission
requests       # HTTP client (existing)
```

## Implementation Checklist

### Phase 1: Contract Updates
- [ ] Create `PlaneBooking.sol`
- [ ] Create `TaxiBooking.sol`
- [ ] Create `YachtBooking.sol`
- [ ] Create `MovieBooking.sol`
- [ ] Create `RestaurantBooking.sol`
- [ ] Update `TravelAgency.sol` to support all contracts with optional arguments

### Phase 2: Account Generation
- [ ] Update `create_storage.go` with deterministic address generation
- [ ] Generate addresses with encoded prefixes: `0x[shard][cross][type][op]...`
- [ ] Store addresses in respective files under `storage/`

### Phase 3: Benchmark Script
- [ ] Create `benchmark.py` with all components
- [ ] Implement account classification by prefix parsing
- [ ] Implement workload generator with CT ratio, Send/Contract ratio, Read/Write ratio
- [ ] Implement Zipfian distribution for skewness
- [ ] Implement parallel TX submission
- [ ] Implement metric collection and export

### Phase 4: Configuration
- [ ] Extend `config.json` with all benchmark parameters
- [ ] Add validation for `involved_shards <= shard_num`
- [ ] Support parameter sweep mode