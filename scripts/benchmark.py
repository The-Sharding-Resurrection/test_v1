#!/usr/bin/env python3
"""
Benchmark script for Ethereum State & Transaction Sharding.

This script measures TPS and latency across multiple dimensions:
- CT Ratio (cross-shard vs local)
- Send/Contract Ratio
- Read/Write Ratio
- Injection Rate
- Data Skewness (Zipfian distribution)
- Involved Shards per transaction

Usage:
    python scripts/benchmark.py                    # Run with config.json settings
    python scripts/benchmark.py --sweep            # Run parameter sweep
    python scripts/benchmark.py --ct-ratio 0.5     # Override specific parameter
"""

import argparse
import json
import math
import os
import random
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from dataclasses import dataclass, field, asdict
from datetime import datetime
from threading import Lock
from typing import Dict, List, Optional, Tuple

import numpy as np

# Add parent directory to path for client import
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from scripts.client import ShardNetwork, ShardConfig


# =============================================================================
# Configuration
# =============================================================================

@dataclass
class BenchmarkConfig:
    """Benchmark configuration loaded from config.json."""
    # Core settings
    shard_num: int = 6
    test_account_num: int = 100
    
    # Benchmark settings
    enabled: bool = True
    duration_seconds: int = 60
    warmup_seconds: int = 10
    cooldown_seconds: int = 5
    
    # Workload settings
    ct_ratio: float = 0.5              # 0.0 - 1.0 (local vs cross-shard)
    send_contract_ratio: float = 0.5   # 0.0 - 1.0 (send vs contract)
    read_write_ratio: float = 0.5      # 0.0 - 1.0 (read vs write for contracts)
    injection_rate: int = 100          # tx/s target
    skewness_theta: float = 0.0        # Zipfian θ (0 = uniform, 0.9 = highly skewed)
    involved_shards: int = 3           # 3-8, must be <= shard_num
    
    # Output settings
    output_format: str = "csv"
    output_file: str = "results/benchmark_results.csv"
    include_raw_latencies: bool = False
    
    # Sweep settings
    sweep_enabled: bool = False
    sweep_parameter: str = "ct_ratio"
    sweep_values: List[float] = field(default_factory=lambda: [0.0, 0.25, 0.5, 0.75, 1.0])
    
    def validate(self):
        """Validate configuration parameters."""
        if self.involved_shards > self.shard_num:
            raise ValueError(f"involved_shards ({self.involved_shards}) must be <= shard_num ({self.shard_num})")
        if not (3 <= self.involved_shards <= 8):
            raise ValueError(f"involved_shards must be in range [3, 8], got {self.involved_shards}")
        if not (0.0 <= self.ct_ratio <= 1.0):
            raise ValueError(f"ct_ratio must be in range [0.0, 1.0], got {self.ct_ratio}")
        if not (0.0 <= self.send_contract_ratio <= 1.0):
            raise ValueError(f"send_contract_ratio must be in range [0.0, 1.0], got {self.send_contract_ratio}")
        if not (0.0 <= self.read_write_ratio <= 1.0):
            raise ValueError(f"read_write_ratio must be in range [0.0, 1.0], got {self.read_write_ratio}")
        if not (0.0 <= self.skewness_theta <= 1.0):
            raise ValueError(f"skewness_theta must be in range [0.0, 1.0], got {self.skewness_theta}")


def load_config(config_path: str = "config/config.json") -> BenchmarkConfig:
    """Load configuration from JSON file."""
    with open(config_path, 'r') as f:
        data = json.load(f)
    
    config = BenchmarkConfig(
        shard_num=data.get("shard_num", 6),
        test_account_num=data.get("test_account_num", 100),
    )
    
    # Load benchmark settings if present
    if "benchmark" in data:
        bench = data["benchmark"]
        config.enabled = bench.get("enabled", True)
        config.duration_seconds = bench.get("duration_seconds", 60)
        config.warmup_seconds = bench.get("warmup_seconds", 10)
        config.cooldown_seconds = bench.get("cooldown_seconds", 5)
        
        if "workload" in bench:
            wl = bench["workload"]
            config.ct_ratio = wl.get("ct_ratio", 0.5)
            config.send_contract_ratio = wl.get("send_contract_ratio", 0.5)
            config.read_write_ratio = wl.get("read_write_ratio", 0.5)
            config.injection_rate = wl.get("injection_rate", 100)
            config.skewness_theta = wl.get("skewness_theta", 0.0)
            config.involved_shards = wl.get("involved_shards", 3)
        
        if "output" in bench:
            out = bench["output"]
            config.output_format = out.get("format", "csv")
            config.output_file = out.get("file", "results/benchmark_results.csv")
            config.include_raw_latencies = out.get("include_raw_latencies", False)
        
        if "sweep" in bench:
            sw = bench["sweep"]
            config.sweep_enabled = sw.get("enabled", False)
            config.sweep_parameter = sw.get("parameter", "ct_ratio")
            config.sweep_values = sw.get("values", [0.0, 0.25, 0.5, 0.75, 1.0])
    
    return config


# =============================================================================
# Account Management
# =============================================================================

def classify_account(addr: str) -> Dict:
    """
    Parse account address to determine its transaction properties.
    
    Address Format: 0x[S][C][T][O]...remaining 36 hex chars...
    Where:
      [S] = Shard number (0-7)
      [C] = Cross-shard flag (0 = local, 1 = cross-shard)
      [T] = Transaction type (0 = send, 1 = contract)
      [O] = Operation type (0 = send, 1 = read, 2 = write)
    
    Args:
        addr: Ethereum address string (0x...)
    
    Returns:
        dict with keys: shard, is_cross_shard, is_contract, operation
    """
    hex_part = addr[2:]  # Strip 0x prefix
    
    try:
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
    except (IndexError, ValueError):
        # Fallback for legacy addresses
        return {
            'shard': int(addr[-2:], 16) % 6,
            'is_cross_shard': False,
            'is_contract': False,
            'operation': 'send'
        }


def load_accounts(storage_dir: str = "storage") -> Dict[str, List[str]]:
    """
    Load accounts from storage and classify by prefix pattern.
    
    Returns:
        Dict mapping pattern (e.g., "000", "112") to list of addresses
    """
    accounts_by_pattern: Dict[str, List[str]] = {}
    
    try:
        with open(os.path.join(storage_dir, "address.txt"), 'r') as f:
            for line in f:
                addr = line.strip()
                if not addr:
                    continue
                
                info = classify_account(addr)
                
                # Build pattern key: cross_shard + tx_type + op_type
                cross_flag = '1' if info['is_cross_shard'] else '0'
                tx_flag = '1' if info['is_contract'] else '0'
                
                if not info['is_contract']:
                    op_flag = '0'
                elif info['operation'] == 'read':
                    op_flag = '1'
                else:
                    op_flag = '2'
                
                pattern = f"{cross_flag}{tx_flag}{op_flag}"
                
                if pattern not in accounts_by_pattern:
                    accounts_by_pattern[pattern] = []
                accounts_by_pattern[pattern].append(addr)
    except FileNotFoundError:
        print(f"Warning: address.txt not found in {storage_dir}")
    
    return accounts_by_pattern


def load_contract_addresses(storage_dir: str = "storage") -> Dict[str, List[str]]:
    """Load contract addresses for all booking contracts."""
    contracts = {}
    contract_types = ["train", "hotel", "plane", "taxi", "yacht", "movie", "restaurant", "travel"]
    
    for contract_type in contract_types:
        filename = os.path.join(storage_dir, f"{contract_type}Address.txt")
        try:
            with open(filename, 'r') as f:
                contracts[contract_type] = [line.strip() for line in f if line.strip()]
        except FileNotFoundError:
            contracts[contract_type] = []
    
    return contracts


# =============================================================================
# Zipfian Distribution
# =============================================================================

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
    
    def _precompute_cdf(self):
        """Precompute CDF for efficient sampling."""
        if self.theta == 0 or self.num_items == 0:
            self.cdf = None
            return
        
        # Compute Zipfian probabilities
        probs = np.array([1.0 / (i ** self.theta) for i in range(1, self.num_items + 1)])
        probs /= probs.sum()
        self.cdf = np.cumsum(probs)
    
    def next(self) -> int:
        """Return next index following Zipfian distribution."""
        if self.num_items == 0:
            return 0
        
        if self.cdf is None:
            # Uniform distribution
            return random.randint(0, self.num_items - 1)
        
        r = random.random()
        return int(np.searchsorted(self.cdf, r))


# =============================================================================
# Transaction Types
# =============================================================================

@dataclass
class Transaction:
    """Represents a transaction to be submitted."""
    tx_type: str  # "local_send", "local_contract_read", "local_contract_write", 
                  # "cross_send", "cross_contract_read", "cross_contract_write"
    from_addr: str
    to_addr: str
    from_shard: int
    to_shard: int = -1  # For cross-shard
    value: str = "1000"
    data: str = "0x"
    gas: int = 100000
    involved_shards: int = 1


@dataclass
class TxMetric:
    """Metrics for a single transaction."""
    tx_id: str
    tx_type: str
    submit_time: float
    complete_time: float = 0.0
    status: str = "pending"  # "committed", "aborted", "timeout"
    involved_shards: int = 1
    
    @property
    def latency_ms(self) -> float:
        if self.complete_time > 0:
            return (self.complete_time - self.submit_time) * 1000
        return 0.0


# =============================================================================
# Workload Generator
# =============================================================================

class WorkloadGenerator:
    """Generates transactions based on configuration."""
    
    # Function selectors
    BOOK_TRIP_SELECTOR = "0x5710ddcd"  # bookTrainAndHotel()
    CHECK_AVAILABILITY_SELECTOR = "0x12345678"  # checkAvailability(...)
    
    def __init__(self, config: BenchmarkConfig, accounts: Dict[str, List[str]], 
                 contracts: Dict[str, List[str]]):
        self.config = config
        self.accounts = accounts
        self.contracts = contracts
        
        # Create Zipfian generators for each pattern
        self.zipf_generators: Dict[str, ZipfianGenerator] = {}
        for pattern, addrs in accounts.items():
            self.zipf_generators[pattern] = ZipfianGenerator(len(addrs), config.skewness_theta)
    
    def generate_tx(self) -> Transaction:
        """Generate a single transaction based on configuration ratios."""
        # Step 1: Local or Cross-shard?
        is_cross_shard = random.random() < self.config.ct_ratio
        
        # Step 2: Send or Contract?
        is_contract = random.random() < self.config.send_contract_ratio
        
        # Step 3: If contract, Read or Write?
        is_write = is_contract and (random.random() < self.config.read_write_ratio)
        
        # Step 4: Select account based on type
        from_addr, from_shard = self._select_account(is_cross_shard, is_contract, is_write)
        
        # Step 5: Build transaction
        if is_contract:
            return self._build_contract_tx(from_addr, from_shard, is_cross_shard, is_write)
        else:
            return self._build_send_tx(from_addr, from_shard, is_cross_shard)
    
    def _select_account(self, is_cross_shard: bool, is_contract: bool, is_write: bool) -> Tuple[str, int]:
        """Select account matching the desired transaction profile."""
        cross_flag = '1' if is_cross_shard else '0'
        tx_flag = '1' if is_contract else '0'
        
        if not is_contract:
            op_flag = '0'  # Send
        elif is_write:
            op_flag = '2'  # Write
        else:
            op_flag = '1'  # Read
        
        pattern = f"{cross_flag}{tx_flag}{op_flag}"
        
        # Get matching accounts
        if pattern in self.accounts and self.accounts[pattern]:
            matching = self.accounts[pattern]
            zipf = self.zipf_generators.get(pattern)
            if zipf:
                idx = zipf.next() % len(matching)
            else:
                idx = random.randint(0, len(matching) - 1)
            addr = matching[idx]
            info = classify_account(addr)
            return addr, info['shard']
        
        # Fallback: use any available account
        for p, addrs in self.accounts.items():
            if addrs:
                addr = random.choice(addrs)
                info = classify_account(addr)
                return addr, info['shard']
        
        # Last resort: generate random address
        shard = random.randint(0, self.config.shard_num - 1)
        return f"0x{shard}000000000000000000000000000000000000000", shard
    
    def _build_send_tx(self, from_addr: str, from_shard: int, is_cross_shard: bool) -> Transaction:
        """Build a simple balance transfer transaction."""
        if is_cross_shard:
            # Pick a different shard for destination
            to_shard = (from_shard + 1) % self.config.shard_num
            # Generate a destination address on that shard
            to_addr = f"0x{to_shard}000000000000000000000000000000000000001"
            tx_type = "cross_send"
        else:
            to_shard = from_shard
            to_addr = f"0x{from_shard}000000000000000000000000000000000000002"
            tx_type = "local_send"
        
        return Transaction(
            tx_type=tx_type,
            from_addr=from_addr,
            to_addr=to_addr,
            from_shard=from_shard,
            to_shard=to_shard,
            value="1000",
            data="0x",
            gas=21000,
            involved_shards=1 if not is_cross_shard else 2
        )
    
    def _build_contract_tx(self, from_addr: str, from_shard: int, 
                           is_cross_shard: bool, is_write: bool) -> Transaction:
        """Build a contract call transaction."""
        # Get TravelAgency contract
        if not self.contracts.get("travel"):
            # Fallback to send if no contracts
            return self._build_send_tx(from_addr, from_shard, is_cross_shard)
        
        # Select a travel contract (randomly for now)
        travel_addr = random.choice(self.contracts["travel"])
        
        if is_write:
            data = self.BOOK_TRIP_SELECTOR
            tx_type = "cross_contract_write" if is_cross_shard else "local_contract_write"
        else:
            data = self.CHECK_AVAILABILITY_SELECTOR
            tx_type = "cross_contract_read" if is_cross_shard else "local_contract_read"
        
        involved = self.config.involved_shards if is_cross_shard else 1
        
        return Transaction(
            tx_type=tx_type,
            from_addr=from_addr,
            to_addr=travel_addr,
            from_shard=from_shard,
            to_shard=-1,  # Determined by contract location
            value="0",
            data=data,
            gas=500000,
            involved_shards=involved
        )


# =============================================================================
# Transaction Submitter
# =============================================================================

class TxSubmitter:
    """Submits transactions in parallel and tracks metrics."""
    
    def __init__(self, network: ShardNetwork, config: BenchmarkConfig, max_workers: int = 32):
        self.network = network
        self.config = config
        self.executor = ThreadPoolExecutor(max_workers=max_workers)
        self.metrics: List[TxMetric] = []
        self.metrics_lock = Lock()
        self.tx_counter = 0
        self.tx_counter_lock = Lock()
    
    def submit(self, tx: Transaction) -> Optional[TxMetric]:
        """Submit a single transaction and return its metric."""
        with self.tx_counter_lock:
            self.tx_counter += 1
            tx_id = f"bench-{self.tx_counter}"
        
        metric = TxMetric(
            tx_id=tx_id,
            tx_type=tx.tx_type,
            submit_time=time.time(),
            involved_shards=tx.involved_shards
        )
        
        try:
            if "cross" in tx.tx_type:
                # Cross-shard transaction - submit via orchestrator
                result = self._submit_cross_shard(tx)
                if result.get("tx_id"):
                    metric.tx_id = result["tx_id"]
                    # Wait for completion
                    final = self.network.orchestrator.wait_for_tx(
                        result["tx_id"], timeout=30, poll_interval=1.0
                    )
                    metric.status = final.get("status", "timeout")
                else:
                    metric.status = "failed"
            else:
                # Local transaction - submit directly to shard
                result = self.network.shard(tx.from_shard).submit_tx(
                    from_addr=tx.from_addr,
                    to_addr=tx.to_addr,
                    value=tx.value,
                    data=tx.data,
                    gas=tx.gas
                )
                metric.status = "committed" if result.get("success") else "aborted"
            
            metric.complete_time = time.time()
            
        except Exception as e:
            metric.status = "error"
            metric.complete_time = time.time()
        
        with self.metrics_lock:
            self.metrics.append(metric)
        
        return metric
    
    def _submit_cross_shard(self, tx: Transaction) -> dict:
        """Submit cross-shard transaction via orchestrator."""
        # Build rw_set based on involved shards
        rw_set = [{"address": tx.to_addr, "reference_block": {"shard_num": tx.from_shard}}]
        
        return self.network.orchestrator.submit_call(
            from_shard=tx.from_shard,
            from_addr=tx.from_addr,
            to_addr=tx.to_addr,
            rw_set=rw_set,
            data=tx.data,
            value=tx.value,
            gas=tx.gas
        )
    
    def submit_batch_async(self, txs: List[Transaction]):
        """Submit multiple transactions asynchronously."""
        futures = [self.executor.submit(self.submit, tx) for tx in txs]
        return futures
    
    def shutdown(self):
        """Shutdown the executor."""
        self.executor.shutdown(wait=True)


# =============================================================================
# Metric Collector
# =============================================================================

@dataclass
class BenchmarkResults:
    """Aggregated benchmark results."""
    # Configuration
    timestamp: str
    ct_ratio: float
    send_contract_ratio: float
    read_write_ratio: float
    shard_count: int
    injection_rate: int
    skewness: float
    involved_shards: int
    duration_seconds: int
    
    # Overall metrics
    total_submitted: int = 0
    total_committed: int = 0
    total_aborted: int = 0
    total_timeout: int = 0
    
    tps: float = 0.0
    latency_p50_ms: float = 0.0
    latency_p95_ms: float = 0.0
    latency_p99_ms: float = 0.0
    abort_rate: float = 0.0
    
    # By type metrics
    local_tps: float = 0.0
    cross_shard_tps: float = 0.0
    send_tps: float = 0.0
    contract_tps: float = 0.0
    
    # Detailed by type
    local_send_tps: float = 0.0
    local_contract_read_tps: float = 0.0
    local_contract_write_tps: float = 0.0
    cross_send_tps: float = 0.0
    cross_contract_read_tps: float = 0.0
    cross_contract_write_tps: float = 0.0


class MetricCollector:
    """Collects and analyzes transaction metrics."""
    
    def __init__(self, config: BenchmarkConfig):
        self.config = config
    
    def calculate_results(self, metrics: List[TxMetric], 
                          actual_duration: float) -> BenchmarkResults:
        """Calculate benchmark results from collected metrics."""
        results = BenchmarkResults(
            timestamp=datetime.now().isoformat(),
            ct_ratio=self.config.ct_ratio,
            send_contract_ratio=self.config.send_contract_ratio,
            read_write_ratio=self.config.read_write_ratio,
            shard_count=self.config.shard_num,
            injection_rate=self.config.injection_rate,
            skewness=self.config.skewness_theta,
            involved_shards=self.config.involved_shards,
            duration_seconds=int(actual_duration),
        )
        
        if not metrics:
            return results
        
        # Count by status
        committed = [m for m in metrics if m.status == "committed"]
        aborted = [m for m in metrics if m.status == "aborted"]
        timeout = [m for m in metrics if m.status in ("timeout", "error")]
        
        results.total_submitted = len(metrics)
        results.total_committed = len(committed)
        results.total_aborted = len(aborted)
        results.total_timeout = len(timeout)
        
        # Overall TPS and abort rate
        if actual_duration > 0:
            results.tps = len(committed) / actual_duration
        if len(metrics) > 0:
            results.abort_rate = len(aborted) / len(metrics)
        
        # Latency percentiles (only for committed)
        if committed:
            latencies = [m.latency_ms for m in committed if m.latency_ms > 0]
            if latencies:
                results.latency_p50_ms = np.percentile(latencies, 50)
                results.latency_p95_ms = np.percentile(latencies, 95)
                results.latency_p99_ms = np.percentile(latencies, 99)
        
        # By type breakdown
        by_type = {}
        for m in committed:
            if m.tx_type not in by_type:
                by_type[m.tx_type] = 0
            by_type[m.tx_type] += 1
        
        if actual_duration > 0:
            # Local vs Cross-shard
            local_count = sum(v for k, v in by_type.items() if "local" in k)
            cross_count = sum(v for k, v in by_type.items() if "cross" in k)
            results.local_tps = local_count / actual_duration
            results.cross_shard_tps = cross_count / actual_duration
            
            # Send vs Contract
            send_count = sum(v for k, v in by_type.items() if "send" in k)
            contract_count = sum(v for k, v in by_type.items() if "contract" in k)
            results.send_tps = send_count / actual_duration
            results.contract_tps = contract_count / actual_duration
            
            # Detailed
            results.local_send_tps = by_type.get("local_send", 0) / actual_duration
            results.local_contract_read_tps = by_type.get("local_contract_read", 0) / actual_duration
            results.local_contract_write_tps = by_type.get("local_contract_write", 0) / actual_duration
            results.cross_send_tps = by_type.get("cross_send", 0) / actual_duration
            results.cross_contract_read_tps = by_type.get("cross_contract_read", 0) / actual_duration
            results.cross_contract_write_tps = by_type.get("cross_contract_write", 0) / actual_duration
        
        return results


# =============================================================================
# Result Exporter
# =============================================================================

class ResultExporter:
    """Exports benchmark results to CSV/JSON."""
    
    def __init__(self, config: BenchmarkConfig):
        self.config = config
        self._ensure_output_dir()
    
    def _ensure_output_dir(self):
        """Create output directory if needed."""
        output_dir = os.path.dirname(self.config.output_file)
        if output_dir:
            os.makedirs(output_dir, exist_ok=True)
    
    def export(self, results: BenchmarkResults):
        """Export results in configured format."""
        if self.config.output_format == "json":
            self._export_json(results)
        else:
            self._export_csv(results)
    
    def _export_csv(self, results: BenchmarkResults):
        """Append results to CSV file."""
        file_exists = os.path.exists(self.config.output_file)
        
        with open(self.config.output_file, 'a') as f:
            if not file_exists:
                # Write header
                headers = [
                    "timestamp", "ct_ratio", "send_contract_ratio", "read_write_ratio",
                    "shard_count", "injection_rate", "skewness", "involved_shards",
                    "duration_seconds", "total_submitted", "total_committed", "total_aborted",
                    "tps", "latency_p50", "latency_p95", "latency_p99", "abort_rate",
                    "local_tps", "cross_shard_tps", "send_tps", "contract_tps"
                ]
                f.write(",".join(headers) + "\n")
            
            # Write data
            values = [
                results.timestamp, results.ct_ratio, results.send_contract_ratio,
                results.read_write_ratio, results.shard_count, results.injection_rate,
                results.skewness, results.involved_shards, results.duration_seconds,
                results.total_submitted, results.total_committed, results.total_aborted,
                f"{results.tps:.2f}", f"{results.latency_p50_ms:.1f}",
                f"{results.latency_p95_ms:.1f}", f"{results.latency_p99_ms:.1f}",
                f"{results.abort_rate:.4f}",
                f"{results.local_tps:.2f}", f"{results.cross_shard_tps:.2f}",
                f"{results.send_tps:.2f}", f"{results.contract_tps:.2f}"
            ]
            f.write(",".join(str(v) for v in values) + "\n")
        
        print(f"Results appended to {self.config.output_file}")
    
    def _export_json(self, results: BenchmarkResults):
        """Export results to JSON file."""
        output = {
            "config": {
                "ct_ratio": results.ct_ratio,
                "send_contract_ratio": results.send_contract_ratio,
                "read_write_ratio": results.read_write_ratio,
                "shard_count": results.shard_count,
                "injection_rate": results.injection_rate,
                "skewness_theta": results.skewness,
                "involved_shards": results.involved_shards,
            },
            "results": {
                "tps": results.tps,
                "latency_p50_ms": results.latency_p50_ms,
                "latency_p95_ms": results.latency_p95_ms,
                "latency_p99_ms": results.latency_p99_ms,
                "abort_rate": results.abort_rate,
                "total_committed": results.total_committed,
                "total_aborted": results.total_aborted,
                "by_type": {
                    "local_send_tps": results.local_send_tps,
                    "local_contract_read_tps": results.local_contract_read_tps,
                    "local_contract_write_tps": results.local_contract_write_tps,
                    "cross_send_tps": results.cross_send_tps,
                    "cross_contract_read_tps": results.cross_contract_read_tps,
                    "cross_contract_write_tps": results.cross_contract_write_tps,
                }
            }
        }
        
        json_file = self.config.output_file.replace(".csv", ".json")
        with open(json_file, 'w') as f:
            json.dump(output, f, indent=2)
        
        print(f"Results written to {json_file}")


# =============================================================================
# Benchmark Runner
# =============================================================================

class BenchmarkRunner:
    """Main benchmark orchestrator."""
    
    def __init__(self, config: BenchmarkConfig):
        self.config = config
        self.network = ShardNetwork(ShardConfig(num_shards=config.shard_num))
        self.accounts = load_accounts()
        self.contracts = load_contract_addresses()
        self.workload_gen = WorkloadGenerator(config, self.accounts, self.contracts)
        self.metric_collector = MetricCollector(config)
        self.exporter = ResultExporter(config)
    
    def check_health(self) -> bool:
        """Verify network is healthy."""
        print("Checking network health...")
        try:
            health = self.network.orchestrator.health()
            if health.get("error"):
                print(f"  Orchestrator: UNHEALTHY - {health}")
                return False
            print("  Orchestrator: OK")
            
            for i in range(self.config.shard_num):
                try:
                    shard_health = self.network.shard(i).health()
                    if shard_health.get("error"):
                        print(f"  Shard {i}: UNHEALTHY")
                        return False
                    print(f"  Shard {i}: OK")
                except Exception as e:
                    print(f"  Shard {i}: UNREACHABLE - {e}")
                    return False
            
            return True
        except Exception as e:
            print(f"Health check failed: {e}")
            return False
    
    def run(self) -> BenchmarkResults:
        """Run the benchmark."""
        print(f"\n{'='*60}")
        print("Starting Benchmark")
        print(f"{'='*60}")
        print(f"  CT Ratio: {self.config.ct_ratio}")
        print(f"  Send/Contract Ratio: {self.config.send_contract_ratio}")
        print(f"  Read/Write Ratio: {self.config.read_write_ratio}")
        print(f"  Injection Rate: {self.config.injection_rate} tx/s")
        print(f"  Duration: {self.config.duration_seconds}s")
        print(f"  Warmup: {self.config.warmup_seconds}s")
        print(f"  Skewness (θ): {self.config.skewness_theta}")
        print(f"  Involved Shards: {self.config.involved_shards}")
        print()
        
        submitter = TxSubmitter(self.network, self.config)
        
        # Calculate injection interval
        interval = 1.0 / self.config.injection_rate if self.config.injection_rate > 0 else 1.0
        
        total_duration = (self.config.warmup_seconds + 
                         self.config.duration_seconds + 
                         self.config.cooldown_seconds)
        
        start_time = time.time()
        warmup_end = start_time + self.config.warmup_seconds
        measure_end = warmup_end + self.config.duration_seconds
        
        print("Phase: Warmup")
        warmup_count = 0
        measure_count = 0
        
        # Main injection loop
        while time.time() < measure_end:
            current_time = time.time()
            
            # Check phase
            if current_time >= warmup_end and warmup_count > 0 and measure_count == 0:
                print(f"Phase: Measurement (submitted {warmup_count} warmup txs)")
            
            # Generate and submit transaction
            tx = self.workload_gen.generate_tx()
            submitter.executor.submit(submitter.submit, tx)
            
            if current_time < warmup_end:
                warmup_count += 1
            else:
                measure_count += 1
            
            # Rate limiting
            elapsed = time.time() - current_time
            sleep_time = interval - elapsed
            if sleep_time > 0:
                time.sleep(sleep_time)
        
        print(f"Phase: Cooldown (submitted {measure_count} measured txs)")
        
        # Wait for pending transactions
        time.sleep(self.config.cooldown_seconds)
        submitter.shutdown()
        
        # Filter metrics to only those submitted during measurement phase
        measure_start = warmup_end
        measured_metrics = [
            m for m in submitter.metrics 
            if measure_start <= m.submit_time < measure_end
        ]
        
        actual_duration = self.config.duration_seconds
        
        # Calculate results
        results = self.metric_collector.calculate_results(measured_metrics, actual_duration)
        
        # Print summary
        print(f"\n{'='*60}")
        print("Benchmark Results")
        print(f"{'='*60}")
        print(f"  Total Submitted: {results.total_submitted}")
        print(f"  Committed: {results.total_committed}")
        print(f"  Aborted: {results.total_aborted}")
        print(f"  Timeout/Error: {results.total_timeout}")
        print()
        print(f"  TPS: {results.tps:.2f}")
        print(f"  Latency P50: {results.latency_p50_ms:.1f} ms")
        print(f"  Latency P95: {results.latency_p95_ms:.1f} ms")
        print(f"  Latency P99: {results.latency_p99_ms:.1f} ms")
        print(f"  Abort Rate: {results.abort_rate:.2%}")
        print()
        print(f"  Local TPS: {results.local_tps:.2f}")
        print(f"  Cross-Shard TPS: {results.cross_shard_tps:.2f}")
        print()
        
        # Export results
        self.exporter.export(results)
        
        return results


# =============================================================================
# Main
# =============================================================================

def main():
    parser = argparse.ArgumentParser(description="Benchmark for Ethereum Sharding")
    parser.add_argument("--config", default="config/config.json", help="Config file path")
    parser.add_argument("--sweep", action="store_true", help="Run parameter sweep")
    parser.add_argument("--ct-ratio", type=float, help="Override CT ratio")
    parser.add_argument("--send-contract-ratio", type=float, help="Override Send/Contract ratio")
    parser.add_argument("--read-write-ratio", type=float, help="Override Read/Write ratio")
    parser.add_argument("--injection-rate", type=int, help="Override injection rate")
    parser.add_argument("--duration", type=int, help="Override duration")
    parser.add_argument("--skewness", type=float, help="Override skewness theta")
    parser.add_argument("--involved-shards", type=int, help="Override involved shards")
    
    args = parser.parse_args()
    
    # Load config
    config = load_config(args.config)
    
    # Apply CLI overrides
    if args.ct_ratio is not None:
        config.ct_ratio = args.ct_ratio
    if args.send_contract_ratio is not None:
        config.send_contract_ratio = args.send_contract_ratio
    if args.read_write_ratio is not None:
        config.read_write_ratio = args.read_write_ratio
    if args.injection_rate is not None:
        config.injection_rate = args.injection_rate
    if args.duration is not None:
        config.duration_seconds = args.duration
    if args.skewness is not None:
        config.skewness_theta = args.skewness
    if args.involved_shards is not None:
        config.involved_shards = args.involved_shards
    
    # Validate
    config.validate()
    
    # Check if sweep mode
    if args.sweep or config.sweep_enabled:
        print(f"Running parameter sweep on: {config.sweep_parameter}")
        print(f"Values: {config.sweep_values}")
        
        for value in config.sweep_values:
            setattr(config, config.sweep_parameter, value)
            config.validate()
            
            runner = BenchmarkRunner(config)
            if runner.check_health():
                runner.run()
            else:
                print("Network unhealthy, skipping this run")
    else:
        # Single run
        runner = BenchmarkRunner(config)
        if runner.check_health():
            runner.run()
        else:
            print("Network unhealthy. Start with: docker compose up --build -d")
            sys.exit(1)


if __name__ == "__main__":
    main()
