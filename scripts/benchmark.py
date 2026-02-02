#!/usr/bin/env python3
"""
Benchmark script for Ethereum State & Transaction Sharding.

This script measures TPS and latency across multiple dimensions:
- CT Ratio (cross-shard vs local)
- Send/Contract Ratio
- Injection Rate
- Data Skewness (Zipfian distribution)

Architecture:
- Fire-and-forget submission (non-blocking)
- Separate completion tracker thread
- Async-friendly design for high throughput

Usage:
    python scripts/benchmark.py                    # Run with config.json settings
    python scripts/benchmark.py --injection-rate 1000 --duration 30
"""

import argparse
import json
import os
import random
import sys
import time
import threading
from collections import defaultdict
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from datetime import datetime
from queue import Queue, Empty
from typing import Dict, List, Optional, Tuple

import numpy as np
import requests

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
    shard_num: int = 8
    test_account_num: int = 100

    # Benchmark settings
    enabled: bool = True
    duration_seconds: int = 30
    warmup_seconds: int = 5
    cooldown_seconds: int = 10

    # Workload settings
    ct_ratio: float = 0.5              # 0.0 - 1.0 (local vs cross-shard)
    send_contract_ratio: float = 0.5   # 0.0 - 1.0 (send vs contract)
    injection_rate: int = 100          # tx/s target
    skewness_theta: float = 0.0        # Zipfian θ (0 = uniform, 0.9 = highly skewed)
    involved_shards: int = 3           # 3-8, must be <= shard_num

    # Output settings
    output_format: str = "csv"
    output_file: str = "results/benchmark_results.csv"

    # Debug settings
    debug: bool = False

    # Block time for calculations
    block_time_ms: int = 200

    # Concurrency for high throughput
    max_workers: int = 256

    def validate(self):
        """Validate configuration parameters."""
        if self.involved_shards > self.shard_num:
            raise ValueError(f"involved_shards ({self.involved_shards}) must be <= shard_num ({self.shard_num})")
        if not (0.0 <= self.ct_ratio <= 1.0):
            raise ValueError(f"ct_ratio must be in range [0.0, 1.0], got {self.ct_ratio}")
        if not (0.0 <= self.send_contract_ratio <= 1.0):
            raise ValueError(f"send_contract_ratio must be in range [0.0, 1.0], got {self.send_contract_ratio}")


def load_config(config_path: str = "config/config.json") -> BenchmarkConfig:
    """Load configuration from JSON file."""
    with open(config_path, 'r') as f:
        data = json.load(f)

    config = BenchmarkConfig(
        shard_num=data.get("shard_num", 8),
        test_account_num=data.get("test_account_num", 100),
        block_time_ms=data.get("block_time_ms", 800),
    )

    # Load benchmark settings if present
    if "benchmark" in data:
        bench = data["benchmark"]
        config.enabled = bench.get("enabled", True)
        config.duration_seconds = bench.get("duration_seconds", 30)
        config.warmup_seconds = bench.get("warmup_seconds", 5)
        config.cooldown_seconds = bench.get("cooldown_seconds", 10)

        if "workload" in bench:
            wl = bench["workload"]
            config.ct_ratio = wl.get("ct_ratio", 0.5)
            config.send_contract_ratio = wl.get("send_contract_ratio", 0.5)
            config.injection_rate = wl.get("injection_rate", 100)
            config.skewness_theta = wl.get("skewness_theta", 0.0)
            config.involved_shards = wl.get("involved_shards", 3)

        if "output" in bench:
            out = bench["output"]
            config.output_format = out.get("format", "csv")
            config.output_file = out.get("file", "results/benchmark_results.csv")

    return config


# =============================================================================
# Account Management
# =============================================================================

def classify_account(addr: str) -> Dict:
    """
    Parse account address to determine its shard and properties.
    Address format: 0x[S][C][T]... where S=shard, C=cross flag, T=tx type
    """
    hex_part = addr[2:]
    try:
        shard = int(hex_part[0])
        is_cross_shard = hex_part[1] == '1'
        is_contract = hex_part[2] == '1'
        return {'shard': shard, 'is_cross_shard': is_cross_shard, 'is_contract': is_contract}
    except (IndexError, ValueError):
        return {'shard': int(addr[-2:], 16) % 8, 'is_cross_shard': False, 'is_contract': False}


def load_accounts(storage_dir: str = "storage") -> Dict[str, List[str]]:
    """Load accounts from storage and classify by pattern."""
    accounts_by_pattern: Dict[str, List[str]] = {}
    try:
        with open(os.path.join(storage_dir, "address.txt"), 'r') as f:
            for line in f:
                addr = line.strip()
                if not addr:
                    continue
                info = classify_account(addr)
                cross_flag = '1' if info['is_cross_shard'] else '0'
                tx_flag = '1' if info['is_contract'] else '0'
                pattern = f"{cross_flag}{tx_flag}"
                if pattern not in accounts_by_pattern:
                    accounts_by_pattern[pattern] = []
                accounts_by_pattern[pattern].append(addr)
    except FileNotFoundError:
        print(f"Warning: address.txt not found in {storage_dir}")
    return accounts_by_pattern


def load_contract_addresses(storage_dir: str = "storage", num_shards: int = 8) -> Dict:
    """Load contract addresses for all booking contracts."""
    contracts = {}
    contract_types = ["train", "hotel", "plane", "taxi", "yacht", "movie", "restaurant", "travel"]

    for contract_type in contract_types:
        contracts[contract_type] = {"local": {s: [] for s in range(num_shards)}, "cross": {s: [] for s in range(num_shards)}}
        filename = os.path.join(storage_dir, f"{contract_type}Address.txt")
        try:
            with open(filename, 'r') as f:
                for line in f:
                    addr = line.strip()
                    if not addr:
                        continue
                    info = classify_account(addr)
                    shard = info['shard']
                    key = "cross" if info['is_cross_shard'] else "local"
                    contracts[contract_type][key][shard].append(addr)
        except FileNotFoundError:
            pass
    return contracts


# =============================================================================
# Zipfian Distribution
# =============================================================================

class ZipfianGenerator:
    """Zipfian distribution generator."""

    def __init__(self, num_items: int, theta: float):
        self.num_items = num_items
        self.theta = theta
        self.cdf = None
        if theta > 0 and num_items > 0:
            probs = np.array([1.0 / (i ** theta) for i in range(1, num_items + 1)])
            probs /= probs.sum()
            self.cdf = np.cumsum(probs)

    def next(self) -> int:
        if self.num_items == 0:
            return 0
        if self.cdf is None:
            return random.randint(0, self.num_items - 1)
        return int(np.searchsorted(self.cdf, random.random()))


# =============================================================================
# Transaction Types
# =============================================================================

@dataclass
class Transaction:
    """Represents a transaction to be submitted."""
    tx_type: str  # "local_send", "local_contract", "cross_send", "cross_contract"
    from_addr: str
    to_addr: str
    from_shard: int
    to_shard: int = -1
    value: str = "1000"
    data: str = "0x"
    gas: int = 100000


@dataclass
class TxRecord:
    """Record of a submitted transaction."""
    tx_id: str
    tx_type: str
    submit_time: float
    complete_time: float = 0.0
    status: str = "pending"


# =============================================================================
# Workload Generator
# =============================================================================

class WorkloadGenerator:
    """Generates transactions based on configuration."""

    BOOK_TRAIN_AND_HOTEL_SELECTOR = "0x5710ddcd"

    def __init__(self, config: BenchmarkConfig, accounts: Dict[str, List[str]], contracts: Dict):
        self.config = config
        self.accounts = accounts
        self.contracts = contracts
        self.zipf_generators = {
            pattern: ZipfianGenerator(len(addrs), config.skewness_theta)
            for pattern, addrs in accounts.items()
        }

    def generate_tx(self) -> Transaction:
        """Generate a single transaction based on configuration ratios."""
        is_cross_shard = random.random() < self.config.ct_ratio
        is_contract = random.random() < self.config.send_contract_ratio
        from_addr, from_shard = self._select_account(is_cross_shard, is_contract)

        if is_contract:
            return self._build_contract_tx(from_addr, from_shard, is_cross_shard)
        else:
            return self._build_send_tx(from_addr, from_shard, is_cross_shard)

    def _select_account(self, is_cross_shard: bool, is_contract: bool) -> Tuple[str, int]:
        """Select account matching the desired transaction profile."""
        pattern = f"{'1' if is_cross_shard else '0'}{'1' if is_contract else '0'}"

        if pattern in self.accounts and self.accounts[pattern]:
            matching = self.accounts[pattern]
            zipf = self.zipf_generators.get(pattern)
            idx = zipf.next() % len(matching) if zipf else random.randint(0, len(matching) - 1)
            addr = matching[idx]
            return addr, classify_account(addr)['shard']

        # Fallback
        for p, addrs in self.accounts.items():
            if addrs:
                addr = random.choice(addrs)
                return addr, classify_account(addr)['shard']

        shard = random.randint(0, self.config.shard_num - 1)
        return f"0x{shard}00{'0'*37}", shard

    def _build_send_tx(self, from_addr: str, from_shard: int, is_cross_shard: bool) -> Transaction:
        """Build a simple balance transfer transaction."""
        if is_cross_shard:
            to_shard = (from_shard + random.randint(1, self.config.shard_num - 1)) % self.config.shard_num
            to_addr = f"0x{to_shard}00{'0'*37}"
            tx_type = "cross_send"
        else:
            to_shard = from_shard
            to_addr = f"0x{from_shard}00{'0'*35}01"
            tx_type = "local_send"

        return Transaction(
            tx_type=tx_type, from_addr=from_addr, to_addr=to_addr,
            from_shard=from_shard, to_shard=to_shard,
            value="1000", data="0x", gas=21000
        )

    def _build_contract_tx(self, from_addr: str, from_shard: int, is_cross_shard: bool) -> Transaction:
        """Build a contract call transaction."""
        contract_key = "cross" if is_cross_shard else "local"

        if is_cross_shard:
            all_contracts = []
            for shard_contracts in self.contracts.get("travel", {}).get("cross", {}).values():
                all_contracts.extend(shard_contracts)
            travel_contracts = all_contracts
        else:
            travel_contracts = self.contracts.get("travel", {}).get("local", {}).get(from_shard, [])

        if not travel_contracts:
            return self._build_send_tx(from_addr, from_shard, is_cross_shard)

        travel_addr = random.choice(travel_contracts)
        tx_type = "cross_contract" if is_cross_shard else "local_contract"

        return Transaction(
            tx_type=tx_type, from_addr=from_addr, to_addr=travel_addr,
            from_shard=from_shard, to_shard=-1,
            value="0", data=self.BOOK_TRAIN_AND_HOTEL_SELECTOR, gas=500000
        )


# =============================================================================
# Non-Blocking Transaction Submitter
# =============================================================================

class AsyncTxSubmitter:
    """
    Non-blocking transaction submitter with separate completion tracking.

    Architecture:
    - submit() returns immediately after sending HTTP request
    - Pending cross-shard tx IDs are queued for status polling
    - Completion tracker thread polls status in batches
    """

    def __init__(self, network: ShardNetwork, config: BenchmarkConfig):
        self.network = network
        self.config = config
        self.debug = config.debug

        # Thread pool for submission
        self.executor = ThreadPoolExecutor(max_workers=config.max_workers)

        # Records for all transactions
        self.records: List[TxRecord] = []
        self.records_lock = threading.Lock()

        # Queue for pending cross-shard tx IDs
        self.pending_queue: Queue = Queue()

        # Counters
        self.tx_counter = 0
        self.tx_counter_lock = threading.Lock()
        self.submitted_count = 0
        self.local_committed = 0
        self.cross_pending = 0

        # Tracker thread control
        self.tracker_running = False
        self.tracker_thread = None

        # Shard ports
        self._shard_ports = {i: 8545 + i for i in range(config.shard_num)}

    def start_tracker(self):
        """Start the completion tracker thread."""
        self.tracker_running = True
        self.tracker_thread = threading.Thread(target=self._tracker_loop, daemon=True)
        self.tracker_thread.start()

    def stop_tracker(self, timeout: float = 30.0):
        """Stop the tracker thread and wait for pending completions."""
        self.tracker_running = False
        if self.tracker_thread:
            self.tracker_thread.join(timeout=timeout)

    def _tracker_loop(self):
        """Background thread that polls for cross-shard tx completion."""
        pending: Dict[str, TxRecord] = {}

        while self.tracker_running or pending or not self.pending_queue.empty():
            # Collect new pending txs from queue
            try:
                while True:
                    record = self.pending_queue.get_nowait()
                    pending[record.tx_id] = record
            except Empty:
                pass

            if not pending:
                time.sleep(0.1)
                continue

            # Poll status for all pending txs
            completed = []
            for tx_id, record in pending.items():
                try:
                    status = self.network.orchestrator.tx_status(tx_id)
                    st = status.get("status", "pending")
                    if st in ("committed", "aborted", "not_found"):
                        record.status = st
                        record.complete_time = time.time()
                        completed.append(tx_id)
                        if self.debug:
                            print(f"  [TRACKER] {tx_id}: {st}")
                except Exception as e:
                    if self.debug:
                        print(f"  [TRACKER] Error polling {tx_id}: {e}")

            # Remove completed from pending
            for tx_id in completed:
                del pending[tx_id]

            # Brief sleep to avoid hammering
            time.sleep(0.2)

    def submit(self, tx: Transaction) -> Optional[TxRecord]:
        """Submit a transaction (non-blocking for cross-shard)."""
        with self.tx_counter_lock:
            self.tx_counter += 1
            local_id = f"bench-{self.tx_counter}"

        record = TxRecord(
            tx_id=local_id,
            tx_type=tx.tx_type,
            submit_time=time.time()
        )

        try:
            if "cross" in tx.tx_type:
                # Cross-shard: submit and queue for tracking
                result = self._submit_cross_shard(tx)

                if result.get("error"):
                    record.status = "error"
                    record.complete_time = time.time()
                elif result.get("tx_id"):
                    record.tx_id = result["tx_id"]
                    record.status = "pending"
                    self.pending_queue.put(record)
                    self.cross_pending += 1
                else:
                    record.status = "failed"
                    record.complete_time = time.time()
            else:
                # Local: submit and assume committed on success
                result = self.network.shard(tx.from_shard).submit_tx(
                    from_addr=tx.from_addr,
                    to_addr=tx.to_addr,
                    value=tx.value,
                    data=tx.data,
                    gas=tx.gas
                )

                if result.get("error"):
                    record.status = "error"
                elif result.get("success") or result.get("status") == "queued":
                    record.status = "committed"
                    self.local_committed += 1
                else:
                    record.status = "aborted"

                record.complete_time = time.time()

        except Exception as e:
            record.status = "error"
            record.complete_time = time.time()
            if self.debug:
                print(f"  [ERROR] {local_id}: {e}")

        with self.records_lock:
            self.records.append(record)

        self.submitted_count += 1
        return record

    def _submit_cross_shard(self, tx: Transaction) -> dict:
        """Submit cross-shard transaction via orchestrator."""
        if tx.tx_type == "cross_send":
            return self.network.orchestrator.submit_transfer(
                from_shard=tx.from_shard,
                from_addr=tx.from_addr,
                to_addr=tx.to_addr,
                to_shard=tx.to_shard,
                value=tx.value,
                gas=tx.gas
            )
        else:
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

    def submit_batch(self, txs: List[Transaction]):
        """Submit a batch of transactions concurrently."""
        futures = [self.executor.submit(self.submit, tx) for tx in txs]
        return futures

    def shutdown(self):
        """Shutdown executor."""
        self.executor.shutdown(wait=True)


# =============================================================================
# Results Calculator
# =============================================================================

@dataclass
class BenchmarkResults:
    """Aggregated benchmark results."""
    timestamp: str
    ct_ratio: float
    send_contract_ratio: float
    shard_count: int
    injection_rate: int
    duration_seconds: int

    total_submitted: int = 0
    total_committed: int = 0
    total_aborted: int = 0
    total_pending: int = 0
    total_error: int = 0

    actual_tps: float = 0.0
    achieved_injection_rate: float = 0.0
    latency_p50_ms: float = 0.0
    latency_p95_ms: float = 0.0
    latency_p99_ms: float = 0.0
    commit_rate: float = 0.0

    local_committed: int = 0
    cross_committed: int = 0
    local_tps: float = 0.0
    cross_tps: float = 0.0


def calculate_results(records: List[TxRecord], config: BenchmarkConfig,
                      actual_duration: float) -> BenchmarkResults:
    """Calculate benchmark results from transaction records."""
    results = BenchmarkResults(
        timestamp=datetime.now().isoformat(),
        ct_ratio=config.ct_ratio,
        send_contract_ratio=config.send_contract_ratio,
        shard_count=config.shard_num,
        injection_rate=config.injection_rate,
        duration_seconds=int(actual_duration),
    )

    if not records:
        return results

    # Count by status
    by_status = defaultdict(list)
    for r in records:
        by_status[r.status].append(r)

    committed = by_status["committed"]
    aborted = by_status["aborted"]
    pending = by_status["pending"]
    errors = by_status["error"] + by_status["failed"]

    results.total_submitted = len(records)
    results.total_committed = len(committed)
    results.total_aborted = len(aborted)
    results.total_pending = len(pending)
    results.total_error = len(errors)

    # TPS calculations
    if actual_duration > 0:
        results.actual_tps = len(committed) / actual_duration
        results.achieved_injection_rate = len(records) / actual_duration

    if len(records) > 0:
        results.commit_rate = len(committed) / len(records)

    # Latency percentiles (only for committed with valid times)
    latencies = [
        (r.complete_time - r.submit_time) * 1000
        for r in committed
        if r.complete_time > r.submit_time
    ]
    if latencies:
        results.latency_p50_ms = np.percentile(latencies, 50)
        results.latency_p95_ms = np.percentile(latencies, 95)
        results.latency_p99_ms = np.percentile(latencies, 99)

    # By type breakdown
    local_committed = [r for r in committed if "local" in r.tx_type]
    cross_committed = [r for r in committed if "cross" in r.tx_type]

    results.local_committed = len(local_committed)
    results.cross_committed = len(cross_committed)

    if actual_duration > 0:
        results.local_tps = len(local_committed) / actual_duration
        results.cross_tps = len(cross_committed) / actual_duration

    return results


# =============================================================================
# Result Exporter
# =============================================================================

def export_results(results: BenchmarkResults, config: BenchmarkConfig):
    """Export results to CSV file."""
    output_dir = os.path.dirname(config.output_file)
    if output_dir:
        os.makedirs(output_dir, exist_ok=True)

    file_exists = os.path.exists(config.output_file)

    with open(config.output_file, 'a') as f:
        if not file_exists:
            headers = [
                "timestamp", "ct_ratio", "send_contract_ratio", "shard_count",
                "injection_rate", "achieved_rate", "duration_seconds",
                "total_submitted", "total_committed", "total_aborted", "total_pending",
                "actual_tps", "commit_rate", "latency_p50", "latency_p95", "latency_p99",
                "local_committed", "cross_committed", "local_tps", "cross_tps"
            ]
            f.write(",".join(headers) + "\n")

        values = [
            results.timestamp, results.ct_ratio, results.send_contract_ratio,
            results.shard_count, results.injection_rate, f"{results.achieved_injection_rate:.1f}",
            results.duration_seconds, results.total_submitted, results.total_committed,
            results.total_aborted, results.total_pending,
            f"{results.actual_tps:.2f}", f"{results.commit_rate:.4f}",
            f"{results.latency_p50_ms:.1f}", f"{results.latency_p95_ms:.1f}",
            f"{results.latency_p99_ms:.1f}",
            results.local_committed, results.cross_committed,
            f"{results.local_tps:.2f}", f"{results.cross_tps:.2f}"
        ]
        f.write(",".join(str(v) for v in values) + "\n")

    print(f"Results appended to {config.output_file}")


# =============================================================================
# Benchmark Runner
# =============================================================================

class BenchmarkRunner:
    """Main benchmark orchestrator."""

    def __init__(self, config: BenchmarkConfig):
        self.config = config
        self.network = ShardNetwork(ShardConfig(num_shards=config.shard_num))
        self.accounts = load_accounts()
        self.contracts = load_contract_addresses(num_shards=config.shard_num)
        self.workload_gen = WorkloadGenerator(config, self.accounts, self.contracts)

    def check_health(self) -> bool:
        """Verify network is healthy."""
        print("Checking network health...")
        try:
            health = self.network.orchestrator.health()
            if health.get("error"):
                print(f"  Orchestrator: UNHEALTHY")
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
        print("Starting Benchmark (Non-Blocking Mode)")
        print(f"{'='*60}")
        print(f"  CT Ratio: {self.config.ct_ratio}")
        print(f"  Send/Contract Ratio: {self.config.send_contract_ratio}")
        print(f"  Target Injection Rate: {self.config.injection_rate} tx/s")
        print(f"  Duration: {self.config.duration_seconds}s")
        print(f"  Cooldown: {self.config.cooldown_seconds}s")
        print()

        submitter = AsyncTxSubmitter(self.network, self.config)
        submitter.start_tracker()

        # Calculate timing
        interval = 1.0 / self.config.injection_rate if self.config.injection_rate > 0 else 1.0
        batch_size = max(1, self.config.injection_rate // 10)  # Submit in batches of ~100ms worth

        start_time = time.time()
        end_time = start_time + self.config.duration_seconds
        last_progress = start_time

        print("Phase: Injecting transactions...")

        # Main injection loop
        while time.time() < end_time:
            batch_start = time.time()

            # Generate and submit a batch
            batch = [self.workload_gen.generate_tx() for _ in range(batch_size)]
            submitter.submit_batch(batch)

            # Progress update every 2 seconds
            now = time.time()
            if now - last_progress >= 2.0:
                elapsed = now - start_time
                rate = submitter.submitted_count / elapsed if elapsed > 0 else 0
                print(f"  [{elapsed:.0f}s] Submitted: {submitter.submitted_count}, "
                      f"Rate: {rate:.0f} tx/s, Local OK: {submitter.local_committed}")
                last_progress = now

            # Rate limiting
            batch_elapsed = time.time() - batch_start
            target_batch_time = batch_size * interval
            if batch_elapsed < target_batch_time:
                time.sleep(target_batch_time - batch_elapsed)

        injection_end = time.time()
        actual_injection_duration = injection_end - start_time

        print(f"\nPhase: Cooldown (submitted {submitter.submitted_count} txs, waiting for completion...)")

        # Wait for pending cross-shard txs to complete
        time.sleep(self.config.cooldown_seconds)
        submitter.stop_tracker(timeout=10.0)
        submitter.shutdown()

        # Calculate results
        results = calculate_results(submitter.records, self.config, actual_injection_duration)

        # Print summary
        print(f"\n{'='*60}")
        print("Benchmark Results")
        print(f"{'='*60}")
        print(f"  Total Submitted: {results.total_submitted}")
        print(f"  Total Committed: {results.total_committed}")
        print(f"  Total Aborted: {results.total_aborted}")
        print(f"  Total Pending: {results.total_pending}")
        print(f"  Total Errors: {results.total_error}")
        print()
        print(f"  Achieved Injection Rate: {results.achieved_injection_rate:.1f} tx/s")
        print(f"  Actual TPS (committed): {results.actual_tps:.2f}")
        print(f"  Commit Rate: {results.commit_rate:.2%}")
        print()
        print(f"  Latency P50: {results.latency_p50_ms:.1f} ms")
        print(f"  Latency P95: {results.latency_p95_ms:.1f} ms")
        print(f"  Latency P99: {results.latency_p99_ms:.1f} ms")
        print()
        print(f"  Local Committed: {results.local_committed} ({results.local_tps:.2f} tps)")
        print(f"  Cross Committed: {results.cross_committed} ({results.cross_tps:.2f} tps)")
        print()

        # Export
        export_results(results, self.config)

        return results


# =============================================================================
# Main
# =============================================================================

def main():
    parser = argparse.ArgumentParser(description="Benchmark for Ethereum Sharding")
    parser.add_argument("--config", default="config/config.json", help="Config file path")
    parser.add_argument("--debug", action="store_true", help="Enable debug output")
    parser.add_argument("--ct-ratio", type=float, help="Override CT ratio")
    parser.add_argument("--send-contract-ratio", type=float, help="Override Send/Contract ratio")
    parser.add_argument("--injection-rate", type=int, help="Override injection rate")
    parser.add_argument("--duration", type=int, help="Override duration")
    parser.add_argument("--cooldown", type=int, help="Override cooldown")

    args = parser.parse_args()

    # Load config
    config = load_config(args.config)

    # Apply CLI overrides
    if args.debug:
        config.debug = True
    if args.ct_ratio is not None:
        config.ct_ratio = args.ct_ratio
    if args.send_contract_ratio is not None:
        config.send_contract_ratio = args.send_contract_ratio
    if args.injection_rate is not None:
        config.injection_rate = args.injection_rate
    if args.duration is not None:
        config.duration_seconds = args.duration
    if args.cooldown is not None:
        config.cooldown_seconds = args.cooldown

    # Validate
    config.validate()

    # Run
    runner = BenchmarkRunner(config)
    if runner.check_health():
        runner.run()
    else:
        print("Network unhealthy. Start with: docker compose up --build -d")
        sys.exit(1)


if __name__ == "__main__":
    main()
