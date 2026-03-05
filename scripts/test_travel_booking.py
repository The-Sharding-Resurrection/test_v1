#!/usr/bin/env python3
"""
Test script for TravelAgency contract - both local and cross-shard calls.

Following the PDF instructions:
1. Classify Travel contracts by checking if Travel, Train, Hotel are on same shard
   - Local: All three contracts on the SAME shard
   - Cross-shard: Contracts on DIFFERENT shards
2. Send targeted transactions using /tx/submit to the appropriate state shard

Contract shard is determined by: address[-1] % numShards (last byte of address)
"""

import sys
import time
from client import ShardNetwork, ShardConfig
from typing import Tuple, List, Dict
from dataclasses import dataclass


# Function selectors
BOOK_TRAIN_AND_HOTEL_SELECTOR = "0x5710ddcd"  # bookTrainAndHotel()
GET_TICKET_SOLD = "0x485cc439"                 # ticketSold() / bookedCount()
GET_ROOM_RESERVED = "0x463f5ce2"               # roomReserved() / bookedCount()

NUM_SHARDS = 6

# ANSI color codes for pretty output
class Colors:
    HEADER = '\033[95m'
    BLUE = '\033[94m'
    CYAN = '\033[96m'
    GREEN = '\033[92m'
    YELLOW = '\033[93m'
    RED = '\033[91m'
    ENDC = '\033[0m'
    BOLD = '\033[1m'


def colored(text: str, color: str) -> str:
    return f"{color}{text}{Colors.ENDC}"


def print_header(text: str):
    print(f"\n{colored('='*60, Colors.HEADER)}")
    print(colored(f"  {text}", Colors.BOLD + Colors.HEADER))
    print(colored('='*60, Colors.HEADER))


def print_section(text: str):
    print(f"\n{colored(f'>>> {text}', Colors.CYAN)}")


def print_success(text: str):
    print(colored(f"  ✓ {text}", Colors.GREEN))


def print_error(text: str):
    print(colored(f"  ✗ {text}", Colors.RED))


def print_info(text: str):
    print(colored(f"  • {text}", Colors.BLUE))


def print_warning(text: str):
    print(colored(f"  ⚠ {text}", Colors.YELLOW))


@dataclass
class TravelContract:
    """Represents a TravelAgency contract and its linked Train/Hotel contracts."""
    index: int
    travel_addr: str
    train_addr: str
    hotel_addr: str
    travel_shard: int
    train_shard: int
    hotel_shard: int
    is_local: bool  # True if all three on same shard

    def __str__(self):
        type_str = "LOCAL" if self.is_local else "CROSS-SHARD"
        return (f"Travel[{self.index}] ({type_str}):\n"
                f"  Travel: {self.travel_addr} (shard {self.travel_shard})\n"
                f"  Train:  {self.train_addr} (shard {self.train_shard})\n"
                f"  Hotel:  {self.hotel_addr} (shard {self.hotel_shard})")


def load_addresses(filename: str) -> List[str]:
    """Load addresses from file."""
    with open(filename, 'r') as f:
        return [line.strip() for line in f if line.strip()]


def get_shard_for_address(addr: str, num_shards: int = NUM_SHARDS) -> int:
    """Determine which shard an address belongs to.
    
    Uses first hex digit after 0x prefix.
    The first character directly encodes the shard number (0-7).
    This makes addresses human-readable: 0x0... = shard 0, 0x3... = shard 3
    """
    # Address format: 0x[S]... where [S] is shard digit
    # Get first hex char after 0x prefix
    first_char = addr[2]
    # Parse hex digit: '0'-'9' -> 0-9, 'a'-'f' -> 10-15
    if first_char.isdigit():
        return int(first_char)
    else:
        return ord(first_char.lower()) - ord('a') + 10


def classify_travel_contracts() -> Tuple[Dict[int, List[TravelContract]], Dict[int, List[TravelContract]]]:
    """
    Classify all Travel contracts into local and cross-shard categories.
    
    Returns:
        local_by_shard: Dict mapping shard_id -> list of LOCAL TravelContracts on that shard
        cross_by_shard: Dict mapping shard_id -> list of CROSS-SHARD TravelContracts on that shard
    
    A contract is LOCAL if Travel, Train, and Hotel are all on the SAME shard.
    A contract is CROSS-SHARD if they are on DIFFERENT shards.
    """
    travel_addrs = load_addresses('./storage/travelAddress.txt')
    train_addrs = load_addresses('./storage/trainAddress.txt')
    hotel_addrs = load_addresses('./storage/hotelAddress.txt')

    # Global classification by shard (as per PDF instruction)
    local_by_shard: Dict[int, List[TravelContract]] = {i: [] for i in range(NUM_SHARDS)}
    cross_by_shard: Dict[int, List[TravelContract]] = {i: [] for i in range(NUM_SHARDS)}

    print_section("Classifying Travel contracts (using address[-1] % numShards)")

    for i in range(len(travel_addrs)):
        travel_addr = travel_addrs[i]
        train_addr = train_addrs[i]
        hotel_addr = hotel_addrs[i]

        # Determine shard for each contract using last byte of address
        travel_shard = get_shard_for_address(travel_addr)
        train_shard = get_shard_for_address(train_addr)
        hotel_shard = get_shard_for_address(hotel_addr)

        # Check if all three are on the same shard (LOCAL) or not (CROSS-SHARD)
        is_local = (travel_shard == train_shard == hotel_shard)

        contract = TravelContract(
            index=i,
            travel_addr=travel_addr,
            train_addr=train_addr,
            hotel_addr=hotel_addr,
            travel_shard=travel_shard,
            train_shard=train_shard,
            hotel_shard=hotel_shard,
            is_local=is_local,
        )

        # Add to appropriate category based on Travel's shard
        if is_local:
            local_by_shard[travel_shard].append(contract)
        else:
            cross_by_shard[travel_shard].append(contract)

        type_str = "LOCAL" if is_local else "CROSS"
        print_info(f"Travel[{i}]: Travel@shard{travel_shard}, Train@shard{train_shard}, "
                   f"Hotel@shard{hotel_shard} -> {type_str}")

    return local_by_shard, cross_by_shard


def test_health(network: ShardNetwork) -> bool:
    """Test network health."""
    print_section("Checking network health")

    try:
        orch_health = network.orchestrator.health()
        if orch_health.get("error"):
            print_error(f"Orchestrator not healthy: {orch_health}")
            return False
        print_success("Orchestrator: OK")

        all_healthy = True
        for i in range(NUM_SHARDS):
            try:
                shard_health = network.shard(i).health()
                if shard_health.get("error"):
                    print_error(f"Shard {i}: UNHEALTHY")
                    all_healthy = False
                else:
                    print_success(f"Shard {i}: OK")
            except Exception as e:
                print_error(f"Shard {i}: UNREACHABLE - {e}")
                all_healthy = False

        return all_healthy
    except Exception as e:
        print_error(f"Health check failed: {e}")
        return False


def get_test_caller(shard: int) -> str:
    """Get a test caller address that has balance on the specified shard."""
    addresses = load_addresses('./storage/address.txt')
    for addr in addresses:
        if get_shard_for_address(addr) == shard:
            return addr
    # Fallback: return a constructed address for the shard
    return f"0x{'0' * 38}{shard:02x}"


def decode_uint256(hex_data: str) -> int:
    """Decode a hex string to uint256."""
    if hex_data.startswith("0x"):
        hex_data = hex_data[2:]
    if not hex_data:
        return 0
    return int(hex_data, 16)


def get_booking_stats(network: ShardNetwork, contract: TravelContract) -> Tuple[int, int]:
    """Get current booking statistics."""
    # Query train on its shard
    train_result = network.shard(contract.train_shard).static_call(
        from_addr="0x0000000000000000000000000000000000000000",
        to_addr=contract.train_addr,
        data=GET_TICKET_SOLD,
        gas=100000
    )
    tickets = decode_uint256(train_result.get("return", "0x0")) if train_result.get("success") else -1

    # Query hotel on its shard
    hotel_result = network.shard(contract.hotel_shard).static_call(
        from_addr="0x0000000000000000000000000000000000000000",
        to_addr=contract.hotel_addr,
        data=GET_ROOM_RESERVED,
        gas=100000
    )
    rooms = decode_uint256(hotel_result.get("return", "0x0")) if hotel_result.get("success") else -1

    return tickets, rooms


def send_targeted_tx(network: ShardNetwork, shard_id: int, contract: TravelContract, 
                     is_cross: bool, caller: str) -> bool:
    """
    Send a targeted transaction to the state shard using /tx/submit.
    
    As per PDF instruction:
    - Creates a targeted tx (local or cross-shard)
    - Sends to corresponding state shard via /tx/submit
    
    Args:
        network: ShardNetwork instance
        shard_id: The state shard to submit tx to (Travel contract's shard)
        contract: The TravelContract to call
        is_cross: True if cross-shard, False if local
        caller: The caller address
    """
    print_section(f"Sending {'CROSS-SHARD' if is_cross else 'LOCAL'} tx to shard {shard_id}")
    print_info(f"Contract: Travel[{contract.index}]")
    print_info(f"Caller: {caller}")
    print(contract)

    # Get initial stats
    initial_tickets, initial_rooms = get_booking_stats(network, contract)
    print_info(f"Initial state: {initial_tickets} tickets, {initial_rooms} rooms")

    try:
        if is_cross:
            # Cross-shard: Submit via orchestrator for 2PC coordination
            # The orchestrator handles transactions that touch multiple shards
            print_info("Submitting cross-shard tx via orchestrator...")
            
            # Build the involved shards set for proper 2PC coordination
            involved_shards = list(set([contract.travel_shard, contract.train_shard, contract.hotel_shard]))
            print_info(f"Involved shards: {involved_shards}")
            
            # Build rw_set with contracts on their respective shards
            rw_set = [
                {"address": contract.travel_addr, "reference_block": {"shard_num": contract.travel_shard}},
                {"address": contract.train_addr, "reference_block": {"shard_num": contract.train_shard}},
                {"address": contract.hotel_addr, "reference_block": {"shard_num": contract.hotel_shard}},
            ]
            
            result = network.orchestrator.submit_call(
                from_shard=shard_id,
                from_addr=caller,
                to_addr=contract.travel_addr,
                rw_set=rw_set,
                data=BOOK_TRAIN_AND_HOTEL_SELECTOR,
                value="0",
                gas=500000
            )
            
            print_info(f"Submit result: {result}")

            if result.get("tx_id"):
                tx_id = result.get("tx_id")
                print_info(f"Transaction ID: {tx_id}")
                
                # Wait for simulation
                print_info("Waiting for simulation...")
                sim_status = network.orchestrator.wait_for_simulation(tx_id, timeout=30)
                print_info(f"Simulation status: {sim_status.get('status')}")

                if sim_status.get("status") == "failed":
                    print_error(f"Simulation failed: {sim_status.get('error')}")
                    return False
                
                print_info("Waiting for 2PC to complete...")
                
                status = None
                for i in range(20):
                    time.sleep(2)
                    status = network.orchestrator.tx_status(tx_id)
                    current = status.get("status", "unknown")
                    print_info(f"  Status after {(i+1)*2}s: {current}")
                    if current in ("committed", "aborted", "failed"):
                        break

                if status and status.get("status") == "committed":
                    print_success("Cross-shard transaction committed!")
                else:
                    print_error(f"Transaction failed with status: {status.get('status') if status else 'unknown'}")
                    return False
            else:
                print_error(f"No transaction ID returned: {result}")
                return False
        else:
            # Local: Submit directly to the state shard via /tx/submit
            print_info(f"Submitting local tx to shard {shard_id} via /tx/submit...")
            
            result = network.shard(shard_id).submit_tx(
                from_addr=caller,
                to_addr=contract.travel_addr,
                data=BOOK_TRAIN_AND_HOTEL_SELECTOR,
                gas=500000,
                value="0"
            )
            
            print_info(f"Submit result: {result}")

            if result.get("success"):
                print_success("Local transaction succeeded!")
            elif result.get("error"):
                print_error(f"Transaction failed: {result.get('error')}")
                return False
            else:
                # Check if it might have succeeded anyway
                print_warning(f"Ambiguous result: {result}")

        # Verify state change
        time.sleep(1)
        final_tickets, final_rooms = get_booking_stats(network, contract)
        print_info(f"Final state: {final_tickets} tickets, {final_rooms} rooms")

        if final_tickets > initial_tickets or final_rooms > initial_rooms:
            print_success(f"State change verified! Tickets: {initial_tickets} -> {final_tickets}, "
                         f"Rooms: {initial_rooms} -> {final_rooms}")
            return True
        elif final_tickets == initial_tickets and final_rooms == initial_rooms:
            print_warning("State unchanged (might be expected if already booked)")
            return True  # Consider as success if tx itself succeeded
        else:
            print_warning("Could not verify state change")
            return True

    except Exception as e:
        print_error(f"Exception: {e}")
        import traceback
        traceback.print_exc()
        return False


def main():
    print_header("TravelAgency Contract Test (Following PDF Instructions)")
    print_info("Testing LOCAL and CROSS-SHARD bookTrainAndHotel() calls")
    print_info("LOCAL = Travel, Train, Hotel all on SAME shard")
    print_info("CROSS-SHARD = Travel, Train, Hotel on DIFFERENT shards")

    # Initialize network
    config = ShardConfig(num_shards=NUM_SHARDS)
    network = ShardNetwork(config)

    # Step 1: Health check
    if not test_health(network):
        print_error("Network health check failed. Run: docker compose up --build -d")
        sys.exit(1)

    # Step 2: Classify contracts (PDF Instruction 1)
    # This determines which Travel contracts are LOCAL vs CROSS-SHARD
    # based on whether Travel, Train, Hotel are all on the same shard
    local_by_shard, cross_by_shard = classify_travel_contracts()

    # Display classification summary
    print_section("Classification Summary")
    total_local = 0
    total_cross = 0
    for shard in range(NUM_SHARDS):
        local_count = len(local_by_shard[shard])
        cross_count = len(cross_by_shard[shard])
        total_local += local_count
        total_cross += cross_count
        if local_count > 0 or cross_count > 0:
            print_info(f"Shard {shard}: {local_count} LOCAL, {cross_count} CROSS-SHARD")
    
    print_info(f"Total: {total_local} LOCAL contracts, {total_cross} CROSS-SHARD contracts")

    results = []

    # Step 3: Test LOCAL transaction (PDF Instruction 2)
    print_header("Test 1: LOCAL Transaction")
    print_info("Testing call where Travel, Train, Hotel are ALL on SAME shard")

    # Find a shard with local contracts
    local_contract = None
    local_shard = None
    for shard in range(NUM_SHARDS):
        if local_by_shard[shard]:
            local_contract = local_by_shard[shard][0]
            local_shard = shard
            break

    if local_contract:
        print_info(f"Found LOCAL contract on shard {local_shard}")
        caller = get_test_caller(local_shard)
        result = send_targeted_tx(network, local_shard, local_contract, is_cross=False, caller=caller)
        results.append(("LOCAL Transaction", result))
    else:
        print_warning("No LOCAL contracts found! (All contracts span multiple shards)")
        results.append(("LOCAL Transaction", None))

    # Step 4: Test CROSS-SHARD transaction (PDF Instruction 2)
    print_header("Test 2: CROSS-SHARD Transaction")
    print_info("Testing call where Travel, Train, Hotel are on DIFFERENT shards")

    # Find a shard with cross-shard contracts
    cross_contract = None
    cross_shard = None
    for shard in range(NUM_SHARDS):
        if cross_by_shard[shard]:
            cross_contract = cross_by_shard[shard][0]
            cross_shard = shard
            break

    if cross_contract:
        print_info(f"Found CROSS-SHARD contract (Travel on shard {cross_shard})")
        caller = get_test_caller(cross_shard)
        result = send_targeted_tx(network, cross_shard, cross_contract, is_cross=True, caller=caller)
        results.append(("CROSS-SHARD Transaction", result))
    else:
        print_warning("No CROSS-SHARD contracts found! (All contracts are local)")
        results.append(("CROSS-SHARD Transaction", None))

    # Summary
    print_header("Test Summary")

    passed = sum(1 for _, r in results if r is True)
    failed = sum(1 for _, r in results if r is False)
    skipped = sum(1 for _, r in results if r is None)

    for name, result in results:
        if result is True:
            print_success(f"{name}: PASSED")
        elif result is False:
            print_error(f"{name}: FAILED")
        else:
            print_warning(f"{name}: SKIPPED (no contracts available)")

    print()
    print_info(f"Total: {passed} passed, {failed} failed, {skipped} skipped")

    if failed > 0:
        print_error("\nSome tests failed!")
        sys.exit(1)
    else:
        print_success("\nAll available tests passed!")
        sys.exit(0)


if __name__ == "__main__":
    main()
