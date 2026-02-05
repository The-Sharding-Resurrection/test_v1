"""
Sharding network client library.
Provides clean interfaces for interacting with shards and orchestrator.
"""

import requests
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry
import time
from dataclasses import dataclass
from typing import Optional


def create_session() -> requests.Session:
    """Create a session with connection pooling and retries."""
    session = requests.Session()
    retry = Retry(total=3, backoff_factor=0.05, status_forcelist=[502, 503, 504])
    adapter = HTTPAdapter(
        pool_connections=100,  # Connections to keep in pool
        pool_maxsize=200,      # Max connections per host
        max_retries=retry,
        pool_block=False       # Don't block when pool is full
    )
    session.mount('http://', adapter)
    session.mount('https://', adapter)
    return session


@dataclass
class ShardConfig:
    """Configuration for shard endpoints."""
    orchestrator: str = "http://localhost:8080"
    base_port: int = 8545
    num_shards: int = 8  # Docker setup has 8 shards

    def shard_url(self, shard_id: int) -> str:
        return f"http://localhost:{self.base_port + shard_id}"


class ShardClient:
    """Client for interacting with a single shard."""

    def __init__(self, base_url: str, session: requests.Session = None):
        self.base_url = base_url.rstrip('/')
        self.session = session or create_session()

    def health(self) -> dict:
        return self.session.get(f"{self.base_url}/health", timeout=5).json()

    def balance(self, address: str) -> dict:
        return self.session.get(f"{self.base_url}/balance/{address}", timeout=5).json()

    def faucet(self, address: str, amount: str) -> dict:
        return self.session.post(
            f"{self.base_url}/faucet",
            json={"address": address, "amount": amount},
            timeout=5
        ).json()

    def transfer(self, from_addr: str, to_addr: str, amount: str) -> dict:
        return self.session.post(
            f"{self.base_url}/transfer",
            json={"from": from_addr, "to": to_addr, "amount": amount},
            timeout=5
        ).json()

    def cross_shard_transfer(
        self, from_addr: str, to_addr: str, to_shard: int, amount: str
    ) -> dict:
        return self.session.post(
            f"{self.base_url}/cross-shard/transfer",
            json={
                "from": from_addr,
                "to": to_addr,
                "to_shard": to_shard,
                "amount": amount
            },
            timeout=5
        ).json()

    def deploy(
        self, from_addr: str, bytecode: str, gas: int = 3_000_000, value: str = "0"
    ) -> dict:
        return self.session.post(
            f"{self.base_url}/evm/deploy",
            json={"from": from_addr, "bytecode": bytecode, "gas": gas, "value": value},
            timeout=30
        ).json()

    def call(
        self, from_addr: str, to_addr: str, data: str,
        gas: int = 1_000_000, value: str = "0"
    ) -> dict:
        return self.session.post(
            f"{self.base_url}/evm/call",
            json={"from": from_addr, "to": to_addr, "data": data, "gas": gas, "value": value},
            timeout=10
        ).json()

    def static_call(
        self, from_addr: str, to_addr: str, data: str, gas: int = 1_000_000
    ) -> dict:
        return self.session.post(
            f"{self.base_url}/evm/staticcall",
            json={"from": from_addr, "to": to_addr, "data": data, "gas": gas},
            timeout=10
        ).json()

    def get_code(self, address: str) -> dict:
        return self.session.get(f"{self.base_url}/evm/code/{address}", timeout=5).json()

    def submit_tx(
        self, from_addr: str, to_addr: str, value: str = "0",
        data: str = "0x", gas: int = 21000
    ) -> dict:
        """Submit transaction via unified /tx/submit endpoint (auto-detects cross-shard)."""
        return self.session.post(
            f"{self.base_url}/tx/submit",
            json={
                "from": from_addr,
                "to": to_addr,
                "value": value,
                "data": data,
                "gas": gas
            },
            timeout=10
        ).json()

    def is_cross_shard_finalized(self, tx_id: str) -> bool:
        """Check if a cross-shard tx has been finalized on this shard.

        Finalization means the state changes have been applied
        (Finalize/Debit/Credit executed), not just that the
        orchestrator decided to commit.
        """
        resp = requests.get(f"{self.base_url}/cross-shard/finalized/{tx_id}")
        if resp.status_code != 200:
            return False
        return resp.json().get("finalized", False)

    def is_local_tx_finalized(self, tx_id: str) -> bool:
        """Check if a local tx has been finalized (included in a block)."""
        resp = requests.get(f"{self.base_url}/local/finalized/{tx_id}")
        if resp.status_code != 200:
            return False
        return resp.json().get("finalized", False)


class OrchestratorClient:
    """Client for interacting with the orchestrator."""

    def __init__(self, base_url: str, session: requests.Session = None):
        self.base_url = base_url.rstrip('/')
        self.session = session or create_session()

    def health(self) -> dict:
        return self.session.get(f"{self.base_url}/health", timeout=5).json()

    def shards(self) -> list:
        return self.session.get(f"{self.base_url}/shards", timeout=5).json()

    def tx_status(self, tx_id: str) -> dict:
        resp = self.session.get(f"{self.base_url}/cross-shard/status/{tx_id}", timeout=5)
        if resp.status_code == 404:
            return {"status": "not_found"}
        return resp.json()

    def submit_call(
        self, from_shard: int, from_addr: str, rw_set: list,
        to_addr: str = "", data: str = "", value: str = "0", gas: int = 1_000_000
    ) -> dict:
        """Submit a cross-shard contract call for simulation."""
        resp = self.session.post(
            f"{self.base_url}/cross-shard/call",
            json={
                "from_shard": from_shard,
                "from": from_addr,
                "to": to_addr,
                "rw_set": rw_set,
                "data": data,
                "value": value,
                "gas": gas
            },
            timeout=10
        )

        # Be robust to non-JSON error responses
        try:
            return resp.json()
        except Exception:
            return {
                "status_code": resp.status_code,
                "text": resp.text,
                "error": "non-JSON response from orchestrator"
            }

    def submit_transfer(
        self, from_shard: int, from_addr: str, to_addr: str,
        to_shard: int, value: str = "0", gas: int = 21000
    ) -> dict:
        """Submit a cross-shard balance transfer (no simulation needed)."""
        rw_set = [
            {"address": to_addr, "reference_block": {"shard_num": to_shard}}
        ]
        resp = self.session.post(
            f"{self.base_url}/cross-shard/submit",
            json={
                "from_shard": from_shard,
                "from": from_addr,
                "to": to_addr,
                "rw_set": rw_set,
                "value": value,
                "gas": gas
            },
            timeout=10
        )
        try:
            return resp.json()
        except Exception:
            return {
                "status_code": resp.status_code,
                "text": resp.text,
                "error": "non-JSON response from orchestrator"
            }

    def simulation_status(self, tx_id: str) -> dict:
        """Get simulation status for a transaction."""
        return self.session.get(f"{self.base_url}/cross-shard/simulation/{tx_id}", timeout=5).json()

    def wait_for_simulation(
        self, tx_id: str, timeout: float = 30, poll_interval: float = 0.5
    ) -> dict:
        """Wait for simulation to complete."""
        start = time.time()
        while time.time() - start < timeout:
            status = self.simulation_status(tx_id)
            if status.get("status") in ("success", "failed"):
                return status
            time.sleep(poll_interval)
        return self.simulation_status(tx_id)

    def wait_for_tx(
        self, tx_id: str, timeout: float = 30, poll_interval: float = 0.5
    ) -> dict:
        """Wait for a transaction to reach a terminal state."""
        start = time.time()
        while time.time() - start < timeout:
            status = self.tx_status(tx_id)
            if status.get("status") in ("committed", "aborted"):
                return status
            time.sleep(poll_interval)
        return self.tx_status(tx_id)


class ShardNetwork:
    """High-level interface to the entire sharding network."""

    def __init__(self, config: Optional[ShardConfig] = None):
        self.config = config or ShardConfig()
        # Share a single session across all clients for connection pooling
        self.session = create_session()
        self.orchestrator = OrchestratorClient(self.config.orchestrator, self.session)
        self.shards = [
            ShardClient(self.config.shard_url(i), self.session)
            for i in range(self.config.num_shards)
        ]

    def shard(self, shard_id: int) -> ShardClient:
        return self.shards[shard_id]

    def check_health(self) -> dict:
        """Check health of all components."""
        results = {"orchestrator": None, "shards": {}}
        try:
            results["orchestrator"] = self.orchestrator.health()
        except Exception as e:
            results["orchestrator"] = {"error": str(e)}

        for i, shard in enumerate(self.shards):
            try:
                results["shards"][i] = shard.health()
            except Exception as e:
                results["shards"][i] = {"error": str(e)}

        return results

    def cross_shard_transfer(
        self, from_shard: int, from_addr: str,
        to_shard: int, to_addr: str, amount: str,
        wait: bool = True
    ) -> dict:
        """Execute a cross-shard transfer."""
        result = self.shards[from_shard].cross_shard_transfer(
            from_addr, to_addr, to_shard, amount
        )
        if wait and "tx_id" in result:
            result["final_status"] = self.orchestrator.wait_for_tx(result["tx_id"])
        return result
