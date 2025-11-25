# 가정

- 이더리움이 추후에 Reorg가 발생하지 않는 Single Slot Finality(PBFT와 같은 일반적인 BFT 합의 알고리즘과 같이 즉시 Finality 제공)를 달성하는 방향으로 합의 알고리즘을 개선할 것
    - [Possible futures of the Ethereum protocol, part 1: The Merge](https://vitalik.eth.limo/general/2024/10/14/futures1.html)
- 이더리움이 추후에 상태 값의 유효성을 증명하는 Proof 크기를 크게 줄이는 Verkle Tree 또는 Binary Tree를 도입할 것(Merkle Proof는 크기 매우 크지만, Verkle Proof는 상수 크기)
    - [Possible futures of the Ethereum protocol, part 1: The Merge](https://vitalik.eth.limo/general/2024/10/14/futures1.html)

# 시스템 모델

- Epoch
    - Epoch은 동일한 샤드 구성(검증자 집합)이 유지되는 일정한 시간 구간을 의미
    - Epoch이 진행되는 동안 각 샤드 검증자는 고정된 샤드 구성원들과 함께 샤드 내부 합의를 수행
    - Epoch 종료 시, 전역 합의를 통해 검증자의 소속 샤드를 무작위로 변경하는 샤드 재구성을 수행하여 새 Epoch에 돌입
        - 새로운 검증자 추가 또는 슬래싱은 재구성 과정을 통해 수행됨
        - 이러한 재구성 과정을 통해 검증자는 각 샤드의 구성원을 알고있음
- 샤드 간 전달되는 메시지는 각 샤드에서 합의된 블록
    - 블록 속 검증자 서명으로 검증 가능

# End-to-end Protocol

![image.png](attachment:dda86bab-e4e0-409a-914d-46d23bb58ad1:image.png)

### 다이어그램

```go
type Slot Hash

type Transaction struct {
	TxHash							Hash
	From								Address
	To									Address
	Value								int
	Data								[]byte
}

type Reference struct {
	ShardNum				int
	BlockHash				Hash
	BlockHeight			int
}

type ReadSetItem struct {
	Slot						Slot
	Value						[]byte
	Proof						[][]byte
}

type RwVariable struct {
	Address							Address
	ReferenceBlock			ReferenceBlock
	ReadSet							[]ReadSetItem 
	WriteSet						[]Slot
}

type CrossShardTransaction struct {
	Transaction,
	RwSet								[]RwVariable
}

type ContractShardBlock struct {
	tpc_result		map[Hash] bool
	ct_to_order 	[]CrossShardTransaction
}

type StateShardBlock struct {
	tx_ordering		[]Transaction
	tpc_prepare 	map[Hash] bool
}
```

### 용어

- Contract Shard
    - 크로스-샤드 트랜잭션의 Two-phase Commit을 시작하는 샤드
    - 각 샤드의 라이트 노드를 운영
        - 이를 통해 외부 샤드 상태 값 검증
    - 각 샤드에 디플로이 되어 있는 컨트랙트 코드를 자신의 상태로 유지
        - 각 샤드에서 처리되는 컨트랙트 디플로이 트랜잭션은 Contract Shard에서도 처리되도록
        - 아예 영구히 유지한다기 보단 Blob처럼 일정 기간 동안만 유지하다 selfdestruct해도 괜찮을 듯(사전 실행에 필요한 code 없으면 요청)
- State Shard
    - 컨트랙트 샤드를 제외한 나머지 샤드
- Leader Node
    - 각 샤드의 샤드 내부 합의 과정 중 샤드 블록을 제안하는 노드
- Slot
    - 특정 스마트 컨트랙트에 저장되어 있는 상태 변수를 특정하는 인덱스
    - 이더리움 스마트 컨트랙트의 상태는 {Address, Slot, Value} 형식으로 표현할 수 있음
- Merkle Proof
    - 이더리움 상태의 유효성을 증명할 수 있는 Proof
    - 이더리움 상태의 Commitment인 State Root와 Merkle Proof로 특정 상태 값의 유효성을 증명할 수 있음
    - In a Merkle Patricia Trie (MPT), proving the validity of a state value requires recomputing the hashes of all parent nodes along the path from the value’s leaf to the root, as this process verifies that the value is indeed included in the corresponding subtree. Given Ethereum’s
    hexadecimal address scheme, each MPT node can have up to 16 children, implying that a proof for a value in a state tree of depth $d$ requires $𝑂(15𝑑)$ data.

### Contract Shard 내부 합의 과정

1. Contract Shard의 Leader Node가 ContractShardBlock 합의를 시작
    - 시뮬레이션을 완료한 크로스-샤드 트랜잭션으로 []CrossShardTransaction 생성
        - 시뮬레이션 결과를 통해 각 크로스-샤드 트랜잭션은 명시해야 하는 데이터를 얻을 수 있음
    - 직전 크로스-샤드 트랜잭션 2PC 결과로 tpc_result 생성
2. Contract Shard의 다른 Node가 ContractShardBlock 수신 및 블록 검증
    - tpc_result가 올바른지
    - ct_to_order 속 invalid transaction은 없는지
    - 각 크로스-샤드 트랜잭션의 ReadSetItem.Value가 유효한지(ReadSetItem.Proof와 RwVariable .ReferenceBlock이 가리키는 StateShardBlock의 State Root로 검증 가능)
3. ContractShardBlock 합의 완료
4. 합의 완료된 ContractShardBlock을 각 샤드에 전파

…

1. 각 샤드에서 수신한 StateShardBlock을 통해 크로스-샤드 트랜잭션 오더링에 대한 2PC Prepare 결과를 다 확인 후, 해당 결과를 나타내는 tpc_result와 다음에 처리할 ct_to_order을 ContractShardBlock에 담아 제안

### State Shard 내부 합의 과정

**Contract Shard 블록 수신 직후 블록 합의**

1. State Shard의 Leader Node가 StateShardBlock 합의를 시작
    - 멤풀로부터 tx_ordering 생성
    - tpc_result에 따라 실행해야 하는 크로스-샤드 트랜잭션을 tx_ordering에 포함하여, 최종 tx_ordering 생성
    - tx_ordering 실행
        - 크로스-샤드 트랜잭션 실행에 필요한 외부 상태 값은 이미 임시적으로 상태에 반영되어 있음(다음 다음 불릿포인트 확인)
    - 실행 이후, ct_to_order에 명시된 ReadSet 속 Value와 State Shard의 현재 상태 속 Value가 일치하는지 확인
        - 불일치 시, 해당 크로스-샤드 트랜잭션의 tpc_prepare = false
        - 전부 일치 시, 해당 크로스-샤드 트랜잭션의 tpc_prepare = true
        - 이를 통해, 최종 tpc_prepare 생성
    - tpc_prepare = true인 크로스-샤드 트랜잭션에 한에, 해당 트랜잭션의 ReadSet을 임시적으로 상태에 반영
    - 단, StateShardBlock에 사용되는 State Root는 임시로 반영한 외부 상태는 반영하지 않은, 즉, 로컬 샤드의 상태만으로 계산된 State Root여야 함
2. State Shard의 다른 Node가 블록 수신 및 검증
    - tx_ordering에 처리해야 하는 크로스-샤드 트랜잭션이 알맞게 포함되었는지
    - tx_ordering 속 invalid transaction은 없는지
    - tpc_prepare는 올바르게 생성되었는지
3. StateShardBlock 합의 완료
4. 합의 완료된 StateShardBlock는 Contract Shard 노드가 유지하는 StateShard 라이트 노드로 전파

**크로스-샤드 트랜잭션 2PC 완료 대기 도중의 합의**

1. State Shard의 Leader Node가 StateShardBlock 합의를 시작
    - 멤풀로부터 tx_ordering 생성
    - tx_ordering 실행
2. State Shard의 다른 Node가 블록 수신 및 검증
3. StateShardBlock 합의 완료
4. 합의 완료된 StateShardBlock는 Contract Shard 노드가 유지하는 StateShard 라이트 노드로 전파

### 크로스-샤드 트랜잭션 시뮬레이션 과정

**실험용 스마트 컨트랙트 예시**

크로스-샤드 트랜잭션을 구현하기 위해 Travel, Train, 그리고 Hotel 컨트랙트 각 샤드에 배포

- bookTrainAndHotel 함수 호출 트랜잭션이 서로 다른 샤드에 위치한 Train 및 Hotel 컨트랙트의 함수를 호출하도록 설계
- `bookTrainAndHotel` 함수에서, 상태 변수 `customers[msg.sender]`에 대한 접근 여부는
Train 컨트랙트의 `checkSeat` 함수 그리고 Hotel 컨트랙트의 `checkRoom` 함수의 호출 결과에 의존(정적 분석 불가)

![image.png](attachment:4ba424ea-628a-4000-979c-7ad4e7a8bf44:image.png)

![code1.png](attachment:69819853-dad1-45b5-8ba0-32d6e391f356:code1.png)

![code2.png](attachment:1ef687d7-59e2-4fbf-83b4-14f6048798ca:code2.png)

**시뮬레이션 프로토콜**

![simulation_protocol.png](attachment:0b090d74-0ba1-43cb-94b8-ffcb18ef6cb4:simulation_protocol.png)

1. Contract Shard의 Leader Node는 자신이 유지하고 있는 스마트 컨트랙트 코드를 통해 크로스-샤드 트랜잭션의 사전 실행을 시작
2. 트랜잭션 사전 실행 중 State Shard의 상태 참조가 발생할 시, 해당 State Shard 노드에 `Request(ca, slot, referenceBlock)` 메시지를 전달
    - `ca`는 호출한 외부 스마트 컨트랙트의 주소(EVM 명령어 코드 실행 중 확인 가능)
    - `slot`은 `ca`에서 참조된 상태 변수의 슬롯 위치(EVM 명령어 코드 실행 중 확인 가능)
    - `referenceBlock` 은 해당 Contract Shard Leader Node가 알고 있는 State Shard의 최신 블록이자, 이번 시뮬레이션에 상태 참조에 사용할 블록
        - shardNum
        - blockHash
        - blockHeight
3. `Request` 메시지를 수신한 State Shard 노드는 `ca`, `slot`, 그리고 `referenceBlock`으로 특정되는 상태 값 `val`, 해당 상태 값이 MPT에 속해 있음을 증명하는 머클 증명 `wit`으로 `Reply(val, wit)` 메시지를 구성하고 사전 실행 노드에 전달
4. `Reply` 메시지를 수신한 Contract Shard의 Leader Node는 자신이 유지하고 있는 State Shard의 `ReferenceBlock.StateRoot` 그리고 `Reply` 메시지에 포함된 `wit`을 통해 외부 상태 값 `val`의 유효성을 검증
5. 검증 완료 시, 노드는 `val`을 참조하여 사전 실행을 재개
6. 최종적으로, 크로스-샤드 트랜잭션의 사전 실행은 완료되어 2PC 프로토콜이 요구하는 읽기/쓰기 집합은 정확하게 식별됨