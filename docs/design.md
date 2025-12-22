## Terminology

- Local Transaction
    - Transaction that accesses state of only a single shard
    - Can be processed with a single intra-shard consensus
- Cross-shard Transaction
    - Transaction that accesses multiple
- Orchestration Shard
    - A shard that manage Two-Phase Commit Protocol for processing cross-shard transactions
    - Only one shard can be Orchestration Shard in the system
    - Run light client of each State Shard to receieve State Shard blocks, so that it can identify the latest state root and current status of 2PC protocol
    - Process cross-shard transaction simulation to extract the fine-grained read/write set of cross-shard transactions
    - Does not maintain explicit state
    - Maintain every contract code that are deployed in State Shards(like a cache used for cross-shard transacion simulation)
- State Shard
    - All shards other than the Orchestration Shard
    - Finalize state differences by the result of 2PC
    - Execute local transaction

# End-to-end Protocol for Cross-Shard Transaction

## Example Contract

### TravelAgency Smart Contract

```solidity
// SPDX-License-Identifier: MIT
pragma solidity ^0.8.23;

contract TravelAgency {
  address public immutable trainBooking;
  address public immutable hotelBooking;
  mapping(address=>bool) customers;

  constructor(address _trainBooking, address _hotelBooking) {
    trainBooking = _trainBooking;
    hotelBooking = _hotelBooking;
  }

  function bookTrainAndHotel() public {
    bool trainAvailable;
    bool hotelAvailable;
    bool bookingSuccess;

    (trainAvailable, ) = trainBooking.staticcall(abi.encodeWithSignature("checkSeatAvailability()"));
    require(trainAvailable, "Train seat is not available.");

    (hotelAvailable, ) = hotelBooking.staticcall(abi.encodeWithSignature("checkRoomAvailability()"));
    require(hotelAvailable, "Hotel room is not available.");

    (bookingSuccess, ) = trainBooking.call(abi.encodeWithSignature("bookTrain(address)",msg.sender));
    require(bookingSuccess, "Train booking failed.");

    (bookingSuccess, ) = hotelBooking.call(abi.encodeWithSignature("bookHotel(address)",msg.sender));
    require(bookingSuccess, "Hotel booking failed.");
    
    customers[msg.sender] = true;
  }
}
```

### Train Smart Contract

```solidity
// SPDX-License-Identifier: MIT
pragma solidity ^0.8.23;

contract TrainBooking {
  uint256 public constant MAX_SEATS = 300;
  uint256 public ticketSold;
  address[MAX_SEATS] public tickets;

  function checkSeatAvailability() public view {
    require(ticketSold < MAX_SEATS, "No more ticket available");
  }

  function bookTrain(address account) public {
    tickets[ticketSold++] = account;
  }
}
```

### Hotel Smart Contract

```solidity
// SPDX-License-Identifier: MIT
pragma solidity ^0.8.23;

contract HotelBooking {
  uint256 public constant MAX_ROOMS = 300;
  uint256 public roomReserved;
  address[MAX_ROOMS] public rooms;

  function checkRoomAvailability() public view{
    require(roomReserved < MAX_ROOMS, "No more room available");
  }

  function bookHotel(address account) public {
    rooms[roomReserved++] = account;
  }
}
```

## Types

```go
type BlockHash [32]byte

type OrchestratorShardBlock struct {
	Height    uint64          `json:"height"`
	PrevHash  BlockHash       `json:"prev_hash"`
	Timestamp uint64          `json:"timestamp"`
	TpcResult map[string]bool `json:"tpc_result"`  // txID -> committed
	CtToOrder []Transaction   `json:"ct_to_order"` // New cross-shard txs
}

type StateShardBlock struct {
	ShardID    int             `json:"shard_id"`    // Which shard produced this block
	Height     uint64          `json:"height"`
	PrevHash   BlockHash       `json:"prev_hash"`
	Timestamp  uint64          `json:"timestamp"`
	StateRoot  common.Hash     `json:"state_root"`
	TxOrdering []Transaction   `json:"tx_ordering"` // Local + cross-shard txs 
}

type Transaction struct {
	ID           string         `json:"id,omitempty"`
	TxHash       common.Hash    `json:"tx_hash,omitempty"`
	From         common.Address `json:"from"`
	To           common.Address `json:"to"`
	Value        *BigInt        `json:"value"`
	Gas          uint64         `json:"gas,omitempty"`
	Data         HexBytes       `json:"data,omitempty"`
	RwSet        []RwVariable   `json:"rw_set"`
	IsCrossShard bool           `json:"is_cross_shard"`
}

type Slot common.Hash

type Reference struct {
	ShardNum    int         `json:"shard_num"`
	BlockHash   common.Hash `json:"block_hash"`
	BlockHeight uint64      `json:"block_height"`
}

type ReadSetItem struct {
	Slot  Slot     `json:"slot"`
	Value []byte   `json:"value"`
	Proof [][]byte `json:"proof"` // Merkle proof (empty for now, deferred)
}

type WriteSetItem struct {
	Slot     Slot   `json:"slot"`
	OldValue []byte `json:"old_value"` // Value before simulation
	NewValue []byte `json:"new_value"` // Value after simulation
}

type RwVariable struct {
	Address        common.Address `json:"address"`
	ReferenceBlock Reference      `json:"reference_block"`
	ReadSet        []ReadSetItem  `json:"read_set"`
	WriteSet       []WriteSetItem `json:"write_set"` // Now includes values
}

```

## 0. Broadcast Transaction

1. A transaction is sent to State Shard of `To` address
2. State Shard simulates transaction to check for cross-shard access(can be identified with execution errors)
3. During the simulation, State Shard updates Transaction’s `RwSet` 
    - If it is cross-shard transaction, update `RwSet` with all state variables accessed until NoStateError
    - If it is local transaction, update whole `RwSet`
4. Sent to Orchestration Shard if it is identified as a cross-shard transaction(if not, it is stored at State Shard’s mempool)

## 1. Cross-Shard Transaction Simulation

Orchestration Shard initiates cross-shard transaction simulation right after receiving cross-shard transactions

```go
type RwSetRequest struct {
	Address          common.Address
	Data             HexBytes
	ReferenceBlock   Reference
}

type RwSetReply struct {
	RwVariable       []RwVariable
}

func simulateCall(cstx *Transaction) {
    // Loop until simulation completes or fails permanently
    for {
        // Step 1: Validate RwSet using Merkle Proof and state root
        // ReferenceBlock refers to the snapshot used for this simulation
        isValid := validateMerkleProof(cstx.RwSet, cstx.ReferenceBlock.StateRoot)
        if !isValid {
            handleInvalidTx(cstx, "Invalid Merkle Proof")
            return
        }

        // Step 2: Set state of ReadSetItem
        // Initialize a temporary StateDB with the data currently available in cstx.RwSet
        tempStateDB := NewStateDB()
        tempStateDB.ApplyReadSet(cstx.RwSet.ReadItems)

        // Step 3: Start simulating EVM call
        evm := NewEVM(tempStateDB)
        result, err := evm.Call(cstx.From, cstx.To, cstx.Data)

        // Case A: Execution Successful
        if err == nil {
            finalizeSimulation(cstx, result)
            return
        }

        // Case B: NoStateError (External Call to another shard required)
        if isNoStateError(err) {
            // Check consistency: Compare declared RwSet vs Actual accessed state so far
            accessedSoFar := tempStateDB.GetAccessedItems()
            if !verifyConsistency(cstx.RwSet, accessedSoFar) {
                handleInvalidTx(cstx, "RwSet inconsistency detected")
                return
            }

            // Prepare Request for the corresponding State Shard
            // Extract the call data that caused the NoStateError
            missingCall := err.GetCallInfo() 
            targetShardID := getShardID(missingCall.Address)

            req := RwSetRequest{
                Address:        missingCall.Address,
                Data:           missingCall.Data,
                ReferenceBlock: cstx.ReferenceBlock,
            }

            // Step 4 & 5: Request RwSet and Wait for Reply
            // The State Shard will simulate locally and return its RwSet
            reply := sendRwSetRequest(targetShardID, req) // Blocking call

            // Update cstx.RwSet with new variables from the State Shard
            cstx.RwSet.Merge(reply.RwVariable)

            // Step 6: Repeat from Step 1 with the expanded RwSet
            continue
        }

        // Case C: Other EVM Errors (Revert, OOG, etc.)
        handleExecutionFailure(cstx, err)
        return
    }
}
```

1. Validate `RwSet` of the cross-shard transaction using Merkle Proof and state root of the referencing block(Skip validation for already validated ones)
2. Set state of `ReadSetItem`
3. State simulating evm call
4. If NoStateError occur by external call
    1. Check whether the `RwSet` field of `Transaction` and state variables that are actually accessed during simulation until NoStateError are identical
    2. Request RwSet to the corresponding State Shard using the data of external call that caused NoStateError
    3. State Shard that received RwSetRequest simulate that call to extract RwSet of its shard until NoStateError occur, and send RwSetReply back to Orchestration Shard
    4. Append newly extracted RwSet into `RwSet` of the cross-shard transaction
    5. Repeat from Step 1 to re-simulate cross-shard transaction from the point of external call that caused the simulation to stop
5. Simulation is done

## 2. Two-Phase Commit

![protocol_v2.png](protocol_v2.png)

### Phase 1 (Orchestrator Shard)

1. Orchestration Shard collects cross-shards transactions that have complete read/write set, and batches them into `CtToOrder`
2. Orchestration creates Orchestration Shard Block with CtToOrder, and broadcasts it to State Shards

### Phase 1 (State Shard)

1. When State Shard receives Orchestation Shard Block, it creates Lock transactions(tries to lock state access of other transactions) for every `ReadSetItem` of cross-shard transactions inside `CtToOrder`, and stores them inside its mempool
2. State Shard creates `TxOrdering` with a batch of transaction with the following sequential order
    - Finalize transaction
        - Finalizes differences caused by cross-shard transactions by appling new value of `WriteSet`
    - Unlock transaction
        - Unlocks the state variables locked by the completed cross-shard transaction whether it succeed or aborted
    - Lock transaction
        - Tries to lock the state access of other transactions for integrity
        - Fails if the current value of state variable are different from the value used in simulation(`ReadSetItem` of cross-shard transaction)
    - Local transaction
3. State Shard creates a State Shard Block with `TxOrdering`  and send it to Orchestrator Shard

### Phase 2 (Orchestrator Shard)

1. By receiving each State Shard blocks, Orchestrator Shard identifies the result of 2PC
    - If all Lock transaction for specific cross-shard transaction succeeds at each State Shard, that cross-shard transaction is set to prepared(If not, aborted)
2. Orchestrator Shard batches the result of cross-shard transaction 2PC into `TpcResult`
3. Orchestrator Shard creates Orchestrator Shard Block with `TpcResult`, and broadcasts it to State Shards

### Phase 2 (State Shard)

1. When State Shard receives Orchestation Shard Block, it creates Finalize and Unlock transactions with the result data inside `TpcResult` and cached transaction data during phase 1, and stores them inside its mempool
2. State Shard creates `TxOrdering` with a batch of transaction with the following sequential order
    - Finalize transaction
    - Unlock transaction
    - Lock transaction
    - Local transaction
3. State Shard creates a State Shard Block with `TxOrdering`  and send it to Orchestrator Shard

<aside>
💡

Remember that Phase 1 and Phase 2 can be processed parallely(Phase 2 of previous CtToOrder and Phase 1 of current CtToOrder)

</aside>

# End-To-End Protocol for Local Transaction

## 0. Broadcast Transaction

1. A transaction is sent to State Shard of `To` address
2. State Shard simulates transaction to check for cross-shard access(can be identified with execution errors)
3. During the simulation, State Shard updates Transaction’s `RwSet` 
    - If it is cross-shard transaction, update `RwSet` with all state variables accessed until NoStateError
    - If it is local transaction, update whole `RwSet`
4. Sent to Orchestration Shard if it is identified as a cross-shard transaction(if not, it is stored at State Shard’s mempool)

## 2. Local Transaction Execution

1. State Shard creates `TxOrdering` with a batch of transaction with the following sequential order
    - Finalize transaction
    - Unlock transaction
    - Lock transaction
    - Local transaction
        - Fails when it tries to write on locked state variable
2. State Shard creates a State Shard Block with `TxOrdering`  and send it to Orchestrator Shard
