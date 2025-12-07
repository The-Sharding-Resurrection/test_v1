#!/usr/bin/env python3
"""
Test script for cross-shard contract calls.
Deploys a contract on one shard and calls it from another shard.

Run after: docker compose up -d
"""

import sys
import time
from client import ShardNetwork

# Deployer on shard 0 (0x00 % 6 = 0)
DEPLOYER = "0x0000000000000000000000000000000000000000"

# Caller on shard 1 (0x01 % 6 = 1)
CALLER = "0x0000000000000000000000000000000000000001"

FUND_AMOUNT = "10000000000000000000"  # 10 ETH

# Simple Storage contract bytecode
# contract Storage {
#     uint256 public value;
#     function set(uint256 v) external { value = v; }
#     function get() external view returns (uint256) { return value; }
# }
STORAGE_BYTECODE = "0x6080604052348015600f57600080fd5b5060ac8061001e6000396000f3fe6080604052348015600f57600080fd5b506004361060325760003560e01c806360fe47b11460375780636d4ce63c146049575b600080fd5b60476042366004605e565b600055565b005b60005460405190815260200160405180910390f35b600060208284031215606f57600080fd5b503591905056fea2646970667358221220000000000000000000000000000000000000000000000000000000000000000064736f6c63430008130033"

# Function selectors
SET_SELECTOR = "0x60fe47b1"  # set(uint256)
GET_SELECTOR = "0x6d4ce63c"  # get()


def encode_uint256(value: int) -> str:
    """Encode a uint256 value as hex (32 bytes, zero-padded)."""
    return format(value, '064x')


def decode_uint256(hex_data: str) -> int:
    """Decode a hex string to uint256."""
    # Remove 0x prefix if present
    if hex_data.startswith("0x"):
        hex_data = hex_data[2:]
    if not hex_data:
        return 0
    return int(hex_data, 16)


def test_cross_shard_contract_call():
    """Test calling a contract on shard 0 from a sender on shard 1."""
    print("=== Cross-Shard Contract Call Test ===\n")

    network = ShardNetwork()

    # 1. Health checks
    print("1. Checking health...")
    health = network.check_health()
    if health["orchestrator"].get("error"):
        print(f"   ERROR: Orchestrator not healthy: {health['orchestrator']}")
        return False
    print(f"   Orchestrator: OK")
    for i in range(min(2, len(network.shards))):
        status = "OK" if not health["shards"].get(i, {}).get("error") else "ERROR"
        print(f"   Shard {i}: {status}")

    # 2. Fund deployer and caller
    print(f"\n2. Funding accounts...")
    result = network.shard(0).faucet(DEPLOYER, FUND_AMOUNT)
    print(f"   Deployer (shard 0): {result.get('success', result)}")
    result = network.shard(1).faucet(CALLER, FUND_AMOUNT)
    print(f"   Caller (shard 1): {result.get('success', result)}")

    # 3. Deploy contract on shard 0
    print(f"\n3. Deploying Storage contract on shard 0...")
    deploy_result = network.shard(0).deploy(
        from_addr=DEPLOYER,
        bytecode=STORAGE_BYTECODE,
        gas=500000
    )

    if not deploy_result.get("success"):
        print(f"   ERROR: Deploy failed: {deploy_result.get('error')}")
        return False

    contract_addr = deploy_result.get("address")
    print(f"   Contract deployed at: {contract_addr}")

    # Contract is deployed on the shard determined by its address
    # The deploy endpoint automatically forwards code to the target shard
    target_shard = deploy_result.get("target_shard", int(contract_addr[-2:], 16) % 6)
    print(f"   Contract shard: {target_shard}")

    # 4. Verify contract code exists on the target shard
    print(f"\n4. Verifying contract code on shard {target_shard}...")
    code_result = network.shard(target_shard).get_code(contract_addr)
    if code_result.get("code") and len(code_result["code"]) > 2:
        print(f"   Code length: {len(code_result['code'])} bytes")
    else:
        print(f"   ERROR: No code at contract address on shard {target_shard}")
        return False

    # 5. Local call to set value (call on the contract's shard)
    print(f"\n5. Setting initial value via local call...")
    set_data = SET_SELECTOR + encode_uint256(42)
    local_result = network.shard(target_shard).call(
        from_addr=DEPLOYER,
        to_addr=contract_addr,
        data=set_data,
        gas=100000
    )
    print(f"   Result: {local_result.get('success', local_result)}")

    # 6. Verify value was set
    print(f"\n6. Reading value via static call...")
    read_result = network.shard(target_shard).static_call(
        from_addr=DEPLOYER,
        to_addr=contract_addr,
        data=GET_SELECTOR,
        gas=100000
    )
    if read_result.get("success"):
        value = decode_uint256(read_result.get("return", "0x0"))
        print(f"   Current value: {value}")
    else:
        print(f"   ERROR: {read_result.get('error')}")

    # 7. Cross-shard call: caller on shard 1 calls contract
    print(f"\n7. Initiating cross-shard contract call...")
    print(f"   Caller (shard 1) -> Contract (shard {target_shard})")
    print(f"   Setting value to 123...")

    # Build calldata: set(123)
    set_data_cross = SET_SELECTOR + encode_uint256(123)

    # Submit via orchestrator's cross-shard/call endpoint
    rw_set = [
        {
            "address": contract_addr,
            "reference_block": {"shard_num": target_shard}
        }
    ]

    result = network.orchestrator.submit_call(
        from_shard=1,
        from_addr=CALLER,
        to_addr=contract_addr,
        rw_set=rw_set,
        data=set_data_cross,
        value="0",
        gas=100000
    )
    print(f"   Submit result: {result}")

    tx_id = result.get("tx_id")
    if not tx_id:
        print("   ERROR: No tx_id returned")
        return False

    # 8. Wait for simulation
    print(f"\n8. Waiting for simulation...")
    sim_status = network.orchestrator.wait_for_simulation(tx_id, timeout=15)
    print(f"   Simulation status: {sim_status.get('status')}")

    if sim_status.get("status") == "failed":
        print(f"   ERROR: Simulation failed: {sim_status.get('error')}")
        return False

    if sim_status.get("rw_set"):
        print(f"   RwSet discovered: {len(sim_status['rw_set'])} entries")

    # 9. Wait for 2PC to complete
    print(f"\n9. Waiting for 2PC to complete...")
    for i in range(5):
        time.sleep(2)
        status = network.orchestrator.tx_status(tx_id)
        print(f"   Status after {(i+1)*2}s: {status.get('status')}")
        if status.get("status") in ("committed", "aborted"):
            break

    # 10. Check final status
    final_status = network.orchestrator.tx_status(tx_id)
    print(f"\n10. Final transaction status: {final_status.get('status')}")

    # 11. Verify value changed (if committed)
    if final_status.get("status") == "committed":
        print(f"\n11. Verifying value was updated...")
        read_result = network.shard(target_shard).static_call(
            from_addr=DEPLOYER,
            to_addr=contract_addr,
            data=GET_SELECTOR,
            gas=100000
        )
        if read_result.get("success"):
            value = decode_uint256(read_result.get("return", "0x0"))
            print(f"    Current value: {value}")
            if value == 123:
                print(f"\n    SUCCESS: Cross-shard contract call committed!")
                return True
            else:
                print(f"    WARNING: Value is {value}, expected 123")
                return False
        else:
            print(f"    ERROR reading value: {read_result.get('error')}")
            return False
    else:
        print(f"\n    FAILED: Transaction was {final_status.get('status')}")
        return False


def test_cross_shard_via_tx_submit():
    """Test cross-shard contract call via unified /tx/submit endpoint."""
    print("\n=== Cross-Shard Contract Call via /tx/submit ===\n")

    network = ShardNetwork()

    # 1. Fund accounts
    print("1. Funding accounts...")
    network.shard(0).faucet(DEPLOYER, FUND_AMOUNT)
    network.shard(1).faucet(CALLER, FUND_AMOUNT)

    # 2. Deploy contract
    print("\n2. Deploying contract on shard 0...")
    deploy_result = network.shard(0).deploy(
        from_addr=DEPLOYER,
        bytecode=STORAGE_BYTECODE,
        gas=500000
    )

    if not deploy_result.get("success"):
        print(f"   ERROR: Deploy failed: {deploy_result.get('error')}")
        return False

    contract_addr = deploy_result.get("address")
    target_shard = deploy_result.get("target_shard", int(contract_addr[-2:], 16) % 6)
    print(f"   Contract at {contract_addr} (shard {target_shard})")

    # 3. Set initial value
    print("\n3. Setting initial value to 100...")
    set_data = SET_SELECTOR + encode_uint256(100)
    network.shard(target_shard).call(
        from_addr=DEPLOYER,
        to_addr=contract_addr,
        data=set_data,
        gas=100000
    )

    # 4. Submit cross-shard call via /tx/submit on caller's shard
    print("\n4. Submitting cross-shard call via /tx/submit...")
    print(f"   Caller on shard 1 -> Contract on shard {target_shard}")

    # This goes through the shard's /tx/submit which auto-detects cross-shard
    set_data_cross = SET_SELECTOR + encode_uint256(999)
    submit_result = network.shard(1).submit_tx(
        from_addr=CALLER,
        to_addr=contract_addr,
        value="0",
        data=set_data_cross,
        gas=100000
    )

    print(f"   Result: {submit_result}")

    # Check if it was forwarded to orchestrator
    if submit_result.get("cross_shard"):
        print("   Detected as cross-shard, forwarded to orchestrator")
        tx_id = submit_result.get("tx_id")

        # Wait for completion
        print("\n5. Waiting for 2PC...")
        final = network.orchestrator.wait_for_tx(tx_id, timeout=15)
        print(f"   Final status: {final.get('status')}")

        if final.get("status") == "committed":
            # Verify
            read_result = network.shard(target_shard).static_call(
                from_addr=DEPLOYER,
                to_addr=contract_addr,
                data=GET_SELECTOR,
                gas=100000
            )
            value = decode_uint256(read_result.get("return", "0x0"))
            print(f"\n6. Final value: {value}")
            if value == 999:
                print("   SUCCESS!")
                return True

    return False


def main():
    print("=" * 60)
    print("Cross-Shard Contract Call Test Suite")
    print("=" * 60)

    results = []

    try:
        results.append(("Direct orchestrator call", test_cross_shard_contract_call()))
    except Exception as e:
        print(f"   EXCEPTION: {e}")
        import traceback
        traceback.print_exc()
        results.append(("Direct orchestrator call", False))

    try:
        results.append(("Via /tx/submit", test_cross_shard_via_tx_submit()))
    except Exception as e:
        print(f"   EXCEPTION: {e}")
        import traceback
        traceback.print_exc()
        results.append(("Via /tx/submit", False))

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
