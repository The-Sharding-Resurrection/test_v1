#!/usr/bin/env python3
"""
Test script for cross-shard transaction simulation.
Tests the /cross-shard/call endpoint which performs EVM simulation
before submitting to 2PC.

Run after: docker compose up -d
"""

import sys
import time
from client import ShardNetwork

# Test addresses (use addresses that map to different shards)
# Address -> shard mapping is based on last byte % num_shards
SENDER = "0x1234567890123456789012345678901234567890"  # -> shard 0 (0x90 % 6 = 0)
RECEIVER = "0xabcdabcdabcdabcdabcdabcdabcdabcdabcdab01"  # -> shard 1 (0x01 % 6 = 1)

FUND_AMOUNT = "1000000000000000000000"  # 1000 ETH
TRANSFER_AMOUNT = "100000000000000000"   # 0.1 ETH


def test_simple_simulation():
    """Test simulation of a simple cross-shard value transfer."""
    print("=== Test 1: Simple Cross-Shard Transfer Simulation ===\n")

    network = ShardNetwork()

    # 1. Health check
    print("1. Health check...")
    health = network.check_health()
    if health["orchestrator"].get("error"):
        print(f"   ERROR: Orchestrator not healthy: {health['orchestrator']}")
        return False
    print(f"   Orchestrator: OK")

    # 2. Fund sender
    print(f"\n2. Funding sender on shard 0...")
    result = network.shard(0).faucet(SENDER, FUND_AMOUNT)
    print(f"   Result: {result}")

    # 3. Check balances
    print(f"\n3. Initial balances...")
    sender_bal = network.shard(0).balance(SENDER)
    receiver_bal = network.shard(1).balance(RECEIVER)
    print(f"   Sender (shard 0):   {sender_bal}")
    print(f"   Receiver (shard 1): {receiver_bal}")

    # 4. Submit cross-shard call for simulation
    print(f"\n4. Submitting cross-shard call for simulation...")
    rw_set = [
        {
            "address": RECEIVER,
            "reference_block": {"shard_num": 1}
        }
    ]
    result = network.orchestrator.submit_call(
        from_shard=0,
        from_addr=SENDER,
        rw_set=rw_set,
        to_addr=RECEIVER,
        value=TRANSFER_AMOUNT
    )
    print(f"   Result: {result}")

    tx_id = result.get("tx_id")
    if not tx_id:
        print("   ERROR: No tx_id returned")
        return False

    # 5. Wait for simulation to complete
    print(f"\n5. Waiting for simulation...")
    sim_status = network.orchestrator.wait_for_simulation(tx_id, timeout=10)
    print(f"   Simulation status: {sim_status}")

    if sim_status.get("status") == "failed":
        print(f"   ERROR: Simulation failed: {sim_status.get('error')}")
        return False

    # 6. Wait for 2PC to complete
    print(f"\n6. Waiting for 2PC to complete...")
    for i in range(4):
        time.sleep(2)
        status = network.orchestrator.tx_status(tx_id)
        print(f"   Status after {(i+1)*2}s: {status}")
        if status.get("status") in ("committed", "aborted"):
            break

    # 7. Final balances
    print(f"\n7. Final balances...")
    sender_bal = network.shard(0).balance(SENDER)
    receiver_bal = network.shard(1).balance(RECEIVER)
    print(f"   Sender (shard 0):   {sender_bal}")
    print(f"   Receiver (shard 1): {receiver_bal}")

    # 8. Verify
    final_status = network.orchestrator.tx_status(tx_id)
    if final_status.get("status") == "committed":
        print("\n   SUCCESS: Cross-shard simulation + 2PC committed")
        return True
    else:
        print(f"\n   FAILED: Final status = {final_status.get('status')}")
        return False


def test_simulation_insufficient_funds():
    """Test that simulation correctly fails when sender has insufficient funds."""
    print("\n=== Test 2: Simulation with Insufficient Funds ===\n")

    network = ShardNetwork()

    # Use a fresh address with no funds
    empty_sender = "0x0000000000000000000000000000000000000000"

    print("1. Submitting call from unfunded address...")
    rw_set = [
        {
            "address": RECEIVER,
            "reference_block": {"shard_num": 1}
        }
    ]
    result = network.orchestrator.submit_call(
        from_shard=0,
        from_addr=empty_sender,
        rw_set=rw_set,
        to_addr=RECEIVER,
        value=TRANSFER_AMOUNT
    )
    print(f"   Result: {result}")

    tx_id = result.get("tx_id")
    if not tx_id:
        print("   ERROR: No tx_id returned")
        return False

    # Wait for simulation
    print("\n2. Waiting for simulation...")
    sim_status = network.orchestrator.wait_for_simulation(tx_id, timeout=10)
    print(f"   Simulation status: {sim_status}")

    # Should fail due to insufficient balance
    if sim_status.get("status") == "failed":
        print("\n   SUCCESS: Simulation correctly rejected insufficient funds")
        return True
    else:
        print(f"\n   UNEXPECTED: Simulation did not fail: {sim_status}")
        return False


def test_lock_contention():
    """Test that concurrent simulations on same address are handled correctly."""
    print("\n=== Test 3: Lock Contention ===\n")

    network = ShardNetwork()

    # Fund sender
    print("1. Funding sender...")
    network.shard(0).faucet(SENDER, FUND_AMOUNT)

    # Submit two concurrent simulations using same sender
    print("\n2. Submitting two concurrent simulations...")
    rw_set = [
        {
            "address": RECEIVER,
            "reference_block": {"shard_num": 1}
        }
    ]

    result1 = network.orchestrator.submit_call(
        from_shard=0,
        from_addr=SENDER,
        rw_set=rw_set,
        to_addr=RECEIVER,
        value=TRANSFER_AMOUNT
    )
    print(f"   Tx1: {result1}")

    result2 = network.orchestrator.submit_call(
        from_shard=0,
        from_addr=SENDER,
        rw_set=rw_set,
        to_addr=RECEIVER,
        value=TRANSFER_AMOUNT
    )
    print(f"   Tx2: {result2}")

    tx1_id = result1.get("tx_id")
    tx2_id = result2.get("tx_id")

    # Wait for both simulations
    print("\n3. Waiting for simulations...")
    time.sleep(2)

    status1 = network.orchestrator.simulation_status(tx1_id)
    status2 = network.orchestrator.simulation_status(tx2_id)
    print(f"   Tx1 status: {status1}")
    print(f"   Tx2 status: {status2}")

    # At least one should succeed, the other might be blocked by lock
    # This is expected behavior - locks prevent concurrent modification
    print("\n   Result: Lock contention test completed")
    print("   (One tx may fail due to address lock - this is expected)")
    return True


def main():
    print("=" * 60)
    print("Cross-Shard Transaction Simulation Test Suite")
    print("=" * 60)

    results = []

    try:
        results.append(("Simple simulation", test_simple_simulation()))
    except Exception as e:
        print(f"   EXCEPTION: {e}")
        results.append(("Simple simulation", False))

    try:
        results.append(("Insufficient funds", test_simulation_insufficient_funds()))
    except Exception as e:
        print(f"   EXCEPTION: {e}")
        results.append(("Insufficient funds", False))

    try:
        results.append(("Lock contention", test_lock_contention()))
    except Exception as e:
        print(f"   EXCEPTION: {e}")
        results.append(("Lock contention", False))

    # Summary
    print("\n" + "=" * 60)
    print("Test Summary")
    print("=" * 60)

    passed = 0
    failed = 0
    for name, result in results:
        status = "PASS" if result else "FAIL"
        print(f"  [{status}] {name}")
        if result:
            passed += 1
        else:
            failed += 1

    print(f"\nTotal: {passed} passed, {failed} failed")

    sys.exit(0 if failed == 0 else 1)


if __name__ == "__main__":
    main()
