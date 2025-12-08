"""
Sharding network client library.
Provides clean interfaces for interacting with shards and orchestrator.
"""

import requests
import time
from dataclasses import dataclass
from typing import Optional


@dataclass
class ShardConfig:
    """Configuration for shard endpoints."""
    orchestrator: str = "http://localhost:8080"
    base_port: int = 8545
    num_shards: int = 6

    def shard_url(self, shard_id: int) -> str:
        return f"http://localhost:{self.base_port + shard_id}"


class ShardClient:
    """Client for interacting with a single shard."""

    def __init__(self, base_url: str):
        self.base_url = base_url.rstrip('/')

    def health(self) -> dict:
        return requests.get(f"{self.base_url}/health").json()

    def balance(self, address: str) -> dict:
        return requests.get(f"{self.base_url}/balance/{address}").json()

    def faucet(self, address: str, amount: str) -> dict:
        return requests.post(
            f"{self.base_url}/faucet",
            json={"address": address, "amount": amount}
        ).json()

    def transfer(self, from_addr: str, to_addr: str, amount: str) -> dict:
        return requests.post(
            f"{self.base_url}/transfer",
            json={"from": from_addr, "to": to_addr, "amount": amount}
        ).json()

    def cross_shard_transfer(
        self, from_addr: str, to_addr: str, to_shard: int, amount: str
    ) -> dict:
        return requests.post(
            f"{self.base_url}/cross-shard/transfer",
            json={
                "from": from_addr,
                "to": to_addr,
                "to_shard": to_shard,
                "amount": amount
            }
        ).json()

    def deploy(
        self, from_addr: str, bytecode: str, gas: int = 3_000_000, value: str = "0"
    ) -> dict:
        return requests.post(
            f"{self.base_url}/evm/deploy",
            json={"from": from_addr, "bytecode": bytecode, "gas": gas, "value": value}
        ).json()

    def call(
        self, from_addr: str, to_addr: str, data: str,
        gas: int = 1_000_000, value: str = "0"
    ) -> dict:
        return requests.post(
            f"{self.base_url}/evm/call",
            json={"from": from_addr, "to": to_addr, "data": data, "gas": gas, "value": value}
        ).json()

    def static_call(
        self, from_addr: str, to_addr: str, data: str, gas: int = 1_000_000
    ) -> dict:
        return requests.post(
            f"{self.base_url}/evm/staticcall",
            json={"from": from_addr, "to": to_addr, "data": data, "gas": gas}
        ).json()

    def get_code(self, address: str) -> dict:
        return requests.get(f"{self.base_url}/evm/code/{address}").json()

    def submit_tx(
        self, from_addr: str, to_addr: str, value: str = "0",
        data: str = "0x", gas: int = 21000
    ) -> dict:
        """Submit transaction via unified /tx/submit endpoint (auto-detects cross-shard)."""
        return requests.post(
            f"{self.base_url}/tx/submit",
            json={
                "from": from_addr,
                "to": to_addr,
                "value": value,
                "data": data,
                "gas": gas
            }
        ).json()


class OrchestratorClient:
    """Client for interacting with the orchestrator."""

    def __init__(self, base_url: str):
        self.base_url = base_url.rstrip('/')

    def health(self) -> dict:
        return requests.get(f"{self.base_url}/health").json()

    def shards(self) -> list:
        return requests.get(f"{self.base_url}/shards").json()

    def tx_status(self, tx_id: str) -> dict:
        resp = requests.get(f"{self.base_url}/cross-shard/status/{tx_id}")
        if resp.status_code == 404:
            return {"status": "not_found"}
        return resp.json()

    def submit_call(
        self, from_shard: int, from_addr: str, rw_set: list,
        to_addr: str = "", data: str = "", value: str = "0", gas: int = 1_000_000
    ) -> dict:
        """Submit a cross-shard contract call for simulation."""
        resp = requests.post(
            f"{self.base_url}/cross-shard/call",
            json={
                "from_shard": from_shard,
                "from": from_addr,
                "to": to_addr,
                "rw_set": rw_set,
                "data": data,
                "value": value,
                "gas": gas
            }
        )

        # Be robust to non-JSON error responses so callers see the real error instead of
        # a JSON decode exception (e.g., http.Error text or empty body on 4xx/5xx).
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
        return requests.get(f"{self.base_url}/cross-shard/simulation/{tx_id}").json()

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
        self.orchestrator = OrchestratorClient(self.config.orchestrator)
        self.shards = [
            ShardClient(self.config.shard_url(i))
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
