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

| Parameter           | Description                                 | Range                               |
| ------------------- | ------------------------------------------- | ----------------------------------- |
| **CT Ratio**        | Cross-shard transaction ratio vs local      | 0.0 - 1.0                           |
| **Shard Count**     | Number of state shards                      | 2, 4, 6, 8                          |
| **Injection Rate**  | Transactions submitted per second           | 10 - 1000 tx/s                      |
| **Skewness (θ)**    | Zipfian distribution parameter              | 0.0 (uniform) - 0.9 (highly skewed) |
| **Involved Shards** | Number of shards touched per cross-shard TX | 2 - 6                               |

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
      "injection_rate": 100,
      "skewness_theta": 0.0,
      "involved_shards_min": 2,
      "involved_shards_max": 3
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

## Component Design

### 1. Config Loader

Reads `config.json` and provides typed access to all parameters. Validates parameter ranges.

```python
@dataclass
class BenchmarkConfig:
    duration_seconds: int
    warmup_seconds: int
    ct_ratio: float          # 0.0 - 1.0
    injection_rate: int      # tx/s target
    skewness_theta: float    # Zipfian θ
    involved_shards_min: int
    involved_shards_max: int
```

### 2. Workload Generator

Generates transactions based on configuration:

- **CT Ratio**: `random() < ct_ratio` → cross-shard, else local
- **Zipfian Distribution**: Account selection with configurable skewness
- **Involved Shards**: Random selection of 2-N shards for cross-shard TXs

```python
class WorkloadGenerator:
    def __init__(self, config: BenchmarkConfig, accounts: List[str]):
        self.zipf = ZipfianGenerator(len(accounts), config.skewness_theta)
    
    def generate_tx(self) -> Transaction:
        is_cross_shard = random.random() < self.config.ct_ratio
        from_account = self.accounts[self.zipf.next()]
        # ... build transaction
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
timestamp,ct_ratio,shard_count,injection_rate,skewness,involved_shards,tps,latency_p50,latency_p95,latency_p99,abort_rate,local_tps,cross_shard_tps
2026-01-12T10:00:00,0.5,6,100,0.0,2,85.2,156,342,520,0.02,95.1,75.3
```

### JSON Output

```json
{
  "config": {
    "ct_ratio": 0.5,
    "shard_count": 6,
    "injection_rate": 100,
    "skewness_theta": 0.0,
    "involved_shards_avg": 2.3
  },
  "results": {
    "tps": 85.2,
    "latency_p50_ms": 156,
    "latency_p95_ms": 342,
    "latency_p99_ms": 520,
    "abort_rate": 0.02,
    "total_committed": 5112,
    "total_aborted": 104
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

results/                  # Output directory (NEW)
├── benchmark_results.csv
├── raw_latencies.json    # Optional detailed data
└── plots/                # Generated graphs
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
{"workload": {"involved_shards_min": 2, "involved_shards_max": 2}}
{"workload": {"involved_shards_min": 4, "involved_shards_max": 4}}
```

## Implementation Notes

1. **Rate Limiting**: Use token bucket or sleep-based rate limiting to achieve target injection rate
2. **Completion Tracking**: Poll orchestrator for cross-shard TX status, track local TX via immediate response
3. **Account Setup**: Pre-fund accounts via faucet before benchmark starts
4. **Block Timing**: Cross-shard TXs require ~2 block rounds (6+ seconds) due to 2PC protocol
5. **Thread Safety**: Use thread-safe queues for metric collection from parallel submitters

## Dependencies

```
numpy          # Percentile calculations
dataclasses    # Config structures  
concurrent.futures  # Parallel submission
requests       # HTTP client (existing)