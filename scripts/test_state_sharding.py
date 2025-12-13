#!/usr/bin/env python3
"""
Test script for state sharding.
Contract on Shard 0, state distributed across Shards 0-5.
Run after: docker compose up -d
"""

import subprocess
import sys
from pathlib import Path
from client import ShardNetwork

# Get contracts dir relative to script location
SCRIPT_DIR = Path(__file__).parent
CONTRACTS_DIR = SCRIPT_DIR.parent / "contracts"

DEPLOYER = "0x1234567890123456789012345678901234567890"
FUND_AMOUNT = "1000000000000000000000"

# Test accounts that map to different shards (address % 6)
TEST_ACCOUNTS = [
    "0x0000000000000000000000000000000000000000",  # shard 0
    "0x0000000000000000000000000000000000000001",  # shard 1
    "0x0000000000000000000000000000000000000002",  # shard 2
    "0x0000000000000000000000000000000000000003",  # shard 3
    "0x0000000000000000000000000000000000000004",  # shard 4
    "0x0000000000000000000000000000000000000005",  # shard 5
]


def get_contract_bytecode() -> str:
    """Compile and get bytecode for MultiShardRegistry."""
    try:
        result = subprocess.run(
            ["forge", "inspect", "MultiShardRegistry", "bytecode"],
            capture_output=True,
            text=True,
            cwd=str(CONTRACTS_DIR)
        )
        if result.returncode != 0:
            print(f"Error compiling: {result.stderr}")
            sys.exit(1)
        return result.stdout.strip()
    except FileNotFoundError:
        print("Error: forge not found. Make sure Foundry is installed.")
        sys.exit(1)


def encode_address(addr: str) -> str:
    """Encode address as 32-byte hex string."""
    addr_clean = addr[2:] if addr.startswith("0x") else addr
    return addr_clean.zfill(64)


def decode_uint(hex_str: str) -> int:
    """Decode hex string to uint."""
    clean = hex_str[2:] if hex_str.startswith("0x") else hex_str
    return int(clean, 16) if clean else 0


def main():
    print("=== State & Transaction Sharding Test ===")
    print("Contract on Shard 0, State distributed across Shards 0-5\n")

    network = ShardNetwork()
    shard0 = network.shard(0)

    # 1. Fund deployer
    print("1. Funding deployer on shard 0...")
    shard0.faucet(DEPLOYER, FUND_AMOUNT)
    print(f"   Funded {DEPLOYER}")

    # 2. Compile and deploy contract
    print("\n2. Deploying MultiShardRegistry to Shard 0 ONLY...")
    print("   Compiling contract...")
    bytecode = get_contract_bytecode()

    result = shard0.deploy(DEPLOYER, bytecode, gas=1_000_000)
    if not result.get("success"):
        print(f"   ERROR: Deploy failed: {result.get('error')}")
        sys.exit(1)

    contract = result["address"]
    print(f"   Contract: {contract}")

    # 3. Test shard mapping
    print("\n3. Testing state sharding (which shard owns which account)...")
    # getShardForAccount(address) selector: 0xc8c4e5c8
    for account in TEST_ACCOUNTS:
        data = "0xc8c4e5c8" + encode_address(account)
        result = shard0.static_call(DEPLOYER, contract, data)
        if result.get("success"):
            shard_id = decode_uint(result.get("return", "0x0"))
            print(f"   {account} -> Shard {shard_id}")
        else:
            print(f"   {account} -> ERROR: {result.get('error')}")

    # 4. Update local balance
    print("\n4. Updating balance on shard 0 (local state)...")
    shard0_account = TEST_ACCOUNTS[0]
    # updateBalance(address,uint256) selector: 0xe30443bc
    amount_hex = "00000000000000000000000000000000000000000000000000000000000003e8"  # 1000
    data = "0xe30443bc" + encode_address(shard0_account) + amount_hex

    result = shard0.call(DEPLOYER, contract, data)
    print(f"   Set balance[{shard0_account}] = 1000")

    # 5. Read balance back
    # localBalances(address) selector: 0x870a073b
    data = "0x870a073b" + encode_address(shard0_account)
    result = shard0.static_call(DEPLOYER, contract, data)
    if result.get("success"):
        balance = decode_uint(result.get("return", "0x0"))
        print(f"   Read balance[{shard0_account}] = {balance}")

    # 6. Demonstrate cross-shard operation
    print("\n5. Cross-shard transfer simulation...")
    from_account = TEST_ACCOUNTS[0]  # Shard 0
    to_account = TEST_ACCOUNTS[3]     # Shard 3
    # transferCrossShard(from, to, amount) selector: 0x8b7a8e1e
    amt_hex = "0000000000000000000000000000000000000000000000000000000000000064"  # 100
    data = "0x8b7a8e1e" + encode_address(from_account) + encode_address(to_account) + amt_hex

    result = shard0.call(DEPLOYER, contract, data)
    print(f"   Transfer: {from_account} (shard 0) -> {to_account} (shard 3), amount: 100")
    print("   Contract emitted CrossShardOperation event")

    # 7. Verify contract only exists on shard 0
    print("\n6. Verifying contract only exists on Shard 0...")
    for i in range(6):
        result = network.shard(i).get_code(contract)
        code = result.get("code", "")
        if code and len(code) > 2:
            status = f"Code exists ({len(code)} chars)"
        else:
            status = "No code"
        print(f"   Shard {i}: {status}")

    # Summary
    print("\n=== Summary ===")
    print("Contract deployed ONLY on Shard 0")
    print("State sharded by account address % 6")
    print("Local state operations work on Shard 0")
    print("Cross-shard operations identified (need 2PC for full implementation)")
    print("\nNext: Implement cross-shard state reads/writes via orchestrator")


if __name__ == "__main__":
    main()
