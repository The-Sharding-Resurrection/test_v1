#!/usr/bin/env python3
"""
Test script for cross-shard transfers.
Run after: docker compose up -d
"""

import sys
import time
from client import ShardNetwork

# Test addresses
SENDER = "0x1234567890123456789012345678901234567890"
RECEIVER = "0xabcdabcdabcdabcdabcdabcdabcdabcdabcdab01"
FUND_AMOUNT = "1000000000000000000000"  # 1000 ETH
TRANSFER_AMOUNT = "100000000000000000"   # 0.1 ETH


def main():
    print("=== Cross-Shard Transfer Test ===\n")

    network = ShardNetwork()

    # 1. Health checks
    print("1. Checking health...")
    health = network.check_health()
    print(f"   Orchestrator: {health['orchestrator']}")
    for i in range(min(2, len(network.shards))):
        print(f"   Shard {i}: {health['shards'].get(i)}")

    # 2. Fund sender account
    print(f"\n2. Funding sender on shard 0...")
    result = network.shard(0).faucet(SENDER, FUND_AMOUNT)
    print(f"   Result: {result}")

    # 3. Check initial balance
    print(f"\n3. Checking initial balances...")
    sender_balance = network.shard(0).balance(SENDER)
    receiver_balance = network.shard(1).balance(RECEIVER)
    print(f"   Sender (shard 0):   {sender_balance}")
    print(f"   Receiver (shard 1): {receiver_balance}")

    # 4. Initiate cross-shard transfer
    print(f"\n4. Initiating cross-shard transfer (shard 0 -> shard 1)...")
    result = network.shard(0).cross_shard_transfer(
        from_addr=SENDER,
        to_addr=RECEIVER,
        to_shard=1,
        amount=TRANSFER_AMOUNT
    )
    print(f"   Result: {result}")

    tx_id = result.get("tx_id")
    if not tx_id:
        print("   ERROR: No tx_id returned")
        sys.exit(1)

    # 5. Wait for processing (block-based 2PC needs ~2 block intervals)
    print(f"\n5. Waiting for transaction to process...")
    print(f"   (Block interval is 3s, 2PC needs 2 rounds)")
    for i in range(3):
        time.sleep(6)
        status = network.orchestrator.tx_status(tx_id)
        print(f"   Status after {(i+1)*6}s: {status}")
        if status.get("status") in ("committed", "aborted"):
            break

    # 6. Final status
    print(f"\n6. Final transaction status...")
    final_status = network.orchestrator.tx_status(tx_id)
    print(f"   {final_status}")

    # 7. Check final balances
    print(f"\n7. Checking final balances...")
    sender_balance = network.shard(0).balance(SENDER)
    receiver_balance = network.shard(1).balance(RECEIVER)
    print(f"   Sender (shard 0):   {sender_balance}")
    print(f"   Receiver (shard 1): {receiver_balance}")

    # 8. Summary
    print(f"\n=== Test Complete ===")
    if final_status.get("status") == "committed":
        print("SUCCESS: Cross-shard transfer committed")
    elif final_status.get("status") == "aborted":
        print("ABORTED: Cross-shard transfer was aborted (insufficient funds?)")
    else:
        print(f"PENDING: Transaction still in status: {final_status.get('status')}")


if __name__ == "__main__":
    main()
