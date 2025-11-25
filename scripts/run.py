#!/usr/bin/env python3
"""
Quick start script for the sharding network.
"""

import subprocess
import sys
import time
from client import ShardNetwork


def run_command(cmd: list[str], description: str) -> bool:
    """Run a command and return success status."""
    print(f"{description}...")
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        print(f"  ERROR: {result.stderr}")
        return False
    return True


def main():
    print("=== Sharding Network Startup ===\n")

    # Build and start
    if not run_command(
        ["docker", "compose", "up", "--build", "-d"],
        "Building and starting sharding network"
    ):
        sys.exit(1)

    # Wait for services
    print("\nWaiting for services to be ready...")
    time.sleep(5)

    # Health checks
    print("\nChecking service health...")
    network = ShardNetwork()

    max_retries = 10
    for attempt in range(max_retries):
        try:
            health = network.orchestrator.health()
            if health.get("status") == "healthy":
                print("  Orchestrator: OK")
                break
        except Exception:
            pass
        if attempt < max_retries - 1:
            time.sleep(1)
    else:
        print("  Orchestrator: FAILED")
        sys.exit(1)

    # Check shards
    healthy_shards = 0
    for i in range(6):
        try:
            health = network.shard(i).health()
            if health.get("status") == "healthy":
                healthy_shards += 1
                if i < 2:  # Only print first 2
                    print(f"  Shard {i}: OK")
        except Exception:
            if i < 2:
                print(f"  Shard {i}: FAILED")

    print(f"  ... ({healthy_shards}/6 shards healthy)")

    # Print endpoints
    print("\n" + "=" * 40)
    print("Network is running!")
    print("=" * 40)
    print("\nEndpoints:")
    print("  Orchestrator: http://localhost:8080")
    for i in range(6):
        print(f"  Shard {i}:      http://localhost:{8545 + i}")

    print("\nTest commands:")
    print("  python scripts/test_cross_shard.py")
    print("  python scripts/test_state_sharding.py")

    print("\nStop with: docker compose down")


if __name__ == "__main__":
    main()
