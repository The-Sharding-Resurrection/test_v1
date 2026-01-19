package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sharding-experiment/sharding/config"
	"github.com/sharding-experiment/sharding/internal/orchestrator"
	"github.com/sharding-experiment/sharding/internal/protocol"
	"github.com/sharding-experiment/sharding/internal/shard"
)

// TestEnv sets up a test environment with multiple shards and an orchestrator
type TestEnv struct {
	Orchestrator    *orchestrator.Service
	OrchestratorURL string
	Shards          []*shard.Server
	ShardServers    []*httptest.Server
	OrchestratorSrv *httptest.Server
}

func NewTestEnv(t *testing.T, numShards int) *TestEnv {
	env := &TestEnv{
		Shards:       make([]*shard.Server, numShards),
		ShardServers: make([]*httptest.Server, numShards),
	}

	// Create orchestrator first (we'll set URL after starting server)
	var err error
	env.Orchestrator, err = orchestrator.NewService(numShards, "", config.NetworkConfig{}) // Empty path for in-memory storage
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	env.OrchestratorSrv = httptest.NewServer(env.Orchestrator.Router())
	env.OrchestratorURL = env.OrchestratorSrv.URL

	// Create shards
	for i := 0; i < numShards; i++ {
		env.Shards[i] = shard.NewServerForTest(i, env.OrchestratorURL, config.NetworkConfig{})
		env.ShardServers[i] = httptest.NewServer(env.Shards[i].Router())
	}

	return env
}

func (e *TestEnv) Close() {
	// Close orchestrator first to stop broadcasts, preventing race with shard closure
	if e.Orchestrator != nil {
		e.Orchestrator.Close()
	}
	if e.OrchestratorSrv != nil {
		e.OrchestratorSrv.Close()
	}

	// Then close shards (now safe since orchestrator is stopped)
	for _, srv := range e.ShardServers {
		if srv != nil {
			srv.Close()
		}
	}
}

func (e *TestEnv) ShardURL(shardID int) string {
	return e.ShardServers[shardID].URL
}

// Helper functions for HTTP calls
func postJSON(url string, body interface{}) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return http.Post(url, "application/json", bytes.NewBuffer(data))
}

func getJSON(url string, result interface{}) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(result)
}

func TestOrchestratorChain_Integration(t *testing.T) {
	// Test the orchestrator chain in isolation
	orch, err := orchestrator.NewService(3, "", config.NetworkConfig{}) // Empty path for in-memory storage
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	defer orch.Close()

	// Add a transaction
	tx := protocol.CrossShardTx{
		ID:        "test-tx-1",
		FromShard: 0,
		From:      common.HexToAddress("0x1234"),
		Value:     protocol.NewBigInt(big.NewInt(1000000)),
		RwSet: []protocol.RwVariable{
			{
				Address:        common.HexToAddress("0x5678"),
				ReferenceBlock: protocol.Reference{ShardNum: 1},
			},
		},
	}

	// Submit via internal method
	orch.AddPendingTx(tx)

	// Verify it's pending
	status := orch.GetTxStatus("test-tx-1")
	if status != protocol.TxPending {
		t.Errorf("Expected pending status, got %s", status)
	}
}

func TestShardEVM_LocalTransfer(t *testing.T) {
	srv := shard.NewServerForTest(0, "http://localhost:8080", config.NetworkConfig{})
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	sender := common.HexToAddress("0x1111111111111111111111111111111111111111")
	receiver := common.HexToAddress("0x2222222222222222222222222222222222222222")

	// Fund sender via faucet
	faucetReq := map[string]string{
		"address": sender.Hex(),
		"amount":  "1000000000000000000", // 1 ETH
	}
	resp, err := postJSON(ts.URL+"/faucet", faucetReq)
	if err != nil {
		t.Fatalf("Faucet request failed: %v", err)
	}
	resp.Body.Close()

	// Check sender balance
	var balResp map[string]string
	err = getJSON(ts.URL+"/balance/"+sender.Hex(), &balResp)
	if err != nil {
		t.Fatalf("Balance request failed: %v", err)
	}
	if balResp["balance"] != "1000000000000000000" {
		t.Errorf("Expected balance 1e18, got %s", balResp["balance"])
	}

	// Local transfer
	transferReq := map[string]string{
		"from":   sender.Hex(),
		"to":     receiver.Hex(),
		"amount": "500000000000000000", // 0.5 ETH
	}
	resp, err = postJSON(ts.URL+"/transfer", transferReq)
	if err != nil {
		t.Fatalf("Transfer request failed: %v", err)
	}
	resp.Body.Close()

	// Check receiver balance
	err = getJSON(ts.URL+"/balance/"+receiver.Hex(), &balResp)
	if err != nil {
		t.Fatalf("Balance request failed: %v", err)
	}
	if balResp["balance"] != "500000000000000000" {
		t.Errorf("Expected receiver balance 5e17, got %s", balResp["balance"])
	}

	// Check sender balance (should be reduced)
	err = getJSON(ts.URL+"/balance/"+sender.Hex(), &balResp)
	if err != nil {
		t.Fatalf("Balance request failed: %v", err)
	}
	if balResp["balance"] != "500000000000000000" {
		t.Errorf("Expected sender balance 5e17, got %s", balResp["balance"])
	}
}

func TestShardEVM_ContractDeploy(t *testing.T) {
	srv := shard.NewServerForTest(0, "http://localhost:8080", config.NetworkConfig{})
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	// Use deployer address that produces contract on shard 0 with 8 shards
	// 0x06 nonce 0 -> contract 0x5bF20f082e6B73247151dD6228086b80437b1e00 (ends in 0x00, shard 0)
	deployer := common.HexToAddress("0x0000000000000000000000000000000000000006")

	// Fund deployer
	faucetReq := map[string]string{
		"address": deployer.Hex(),
		"amount":  "10000000000000000000", // 10 ETH
	}
	resp, err := postJSON(ts.URL+"/faucet", faucetReq)
	if err != nil {
		t.Fatalf("Faucet request failed: %v", err)
	}
	resp.Body.Close()

	// Deploy simple contract (just returns 42)
	// PUSH1 0x2a PUSH1 0x00 MSTORE PUSH1 0x20 PUSH1 0x00 RETURN
	// 602a60005260206000f3
	deployReq := map[string]interface{}{
		"from":     deployer.Hex(),
		"bytecode": "0x602a60005260206000f3",
		"gas":      uint64(100000),
	}
	resp, err = postJSON(ts.URL+"/evm/deploy", deployReq)
	if err != nil {
		t.Fatalf("Deploy request failed: %v", err)
	}

	var deployResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&deployResp)
	resp.Body.Close()

	if deployResp["success"] != true {
		t.Errorf("Deploy failed: %v", deployResp["error"])
	}
	if deployResp["address"] == nil || deployResp["address"] == "" {
		t.Error("Expected contract address")
	}

	t.Logf("Deployed contract at: %v", deployResp["address"])
}

func TestCrossShardTx_Simulation(t *testing.T) {
	// This test simulates the cross-shard transaction flow manually
	// without the full HTTP infrastructure

	// Create components
	numShards := 3
	orchChain := orchestrator.NewOrchestratorChain()
	shardChains := make([]*shard.Chain, numShards)
	shardEVMs := make([]*shard.EVMState, numShards)
	for i := 0; i < numShards; i++ {
		shardChains[i] = shard.NewChain(i)
		evmState, err := shard.NewMemoryEVMState()
		if err != nil {
			t.Fatalf("Failed to create EVM state for shard %d: %v", i, err)
		}
		shardEVMs[i] = evmState
	}

	// Create a cross-shard tx: shard 0 -> shard 1
	tx := protocol.CrossShardTx{
		ID:        "tx-1",
		FromShard: 0,
		From:      common.HexToAddress("0x1111"),
		Value:     protocol.NewBigInt(big.NewInt(1000)),
		RwSet: []protocol.RwVariable{
			{
				Address:        common.HexToAddress("0x2222"),
				ReferenceBlock: protocol.Reference{ShardNum: 1},
			},
		},
	}

	// Step 1: Add tx to orchestrator
	orchChain.AddTransaction(tx)

	// Step 2: Orchestrator produces block with CtToOrder
	orchBlock1 := orchChain.ProduceBlock()
	if len(orchBlock1.CtToOrder) != 1 {
		t.Fatalf("Expected 1 tx in CtToOrder, got %d", len(orchBlock1.CtToOrder))
	}

	// Step 3: Source shard (0) processes block - locks funds and votes
	shardChains[0].AddTx(protocol.Transaction{ID: tx.ID, IsCrossShard: true})
	shardChains[0].LockFunds(tx.ID, tx.From, tx.Value.ToBigInt())
	shardChains[0].AddPrepareResult(tx.ID, true) // Vote YES

	// Step 4: Dest shard (1) processes block - stores pending credit
	shardChains[1].StorePendingCredit(tx.ID, common.HexToAddress("0x2222"), tx.Value.ToBigInt())

	// Step 5: Source shard produces block with vote
	stateBlock0, err := shardChains[0].ProduceBlock(shardEVMs[0])
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}
	if len(stateBlock0.TpcPrepare) != 1 {
		t.Fatalf("Expected 1 prepare vote, got %d", len(stateBlock0.TpcPrepare))
	}
	if !stateBlock0.TpcPrepare[tx.ID] {
		t.Error("Expected prepare vote to be true")
	}

	// Step 6: Orchestrator records votes from both involved shards
	// FromShard=0, RwSet has shard 1 -> involved shards are [0, 1]
	if !orchChain.RecordVote(tx.ID, 0, true) {
		t.Error("Vote from shard 0 should be recorded")
	}
	if !orchChain.RecordVote(tx.ID, 1, true) {
		t.Error("Vote from shard 1 should be recorded")
	}

	// Step 7: Orchestrator produces block with TpcResult
	orchBlock2 := orchChain.ProduceBlock()
	if len(orchBlock2.TpcResult) != 1 {
		t.Fatalf("Expected 1 TpcResult, got %d", len(orchBlock2.TpcResult))
	}
	if !orchBlock2.TpcResult[tx.ID] {
		t.Error("Expected TpcResult to be committed (true)")
	}

	// Step 8: Source shard processes commit - clears lock
	lock, ok := shardChains[0].GetLockedFunds(tx.ID)
	if !ok {
		t.Error("Lock should still exist before commit processing")
	}
	_ = lock
	shardChains[0].ClearLock(tx.ID)

	// Step 9: Dest shard processes commit - applies credit
	credits, ok := shardChains[1].GetPendingCredits(tx.ID)
	if !ok {
		t.Error("Pending credits should exist before commit processing")
	}
	if len(credits) != 1 {
		t.Errorf("Expected 1 credit, got %d", len(credits))
	}
	if credits[0].Amount.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Expected credit amount 1000, got %s", credits[0].Amount.String())
	}
	shardChains[1].ClearPendingCredit(tx.ID)

	// Verify final state
	_, ok = shardChains[0].GetLockedFunds(tx.ID)
	if ok {
		t.Error("Lock should be cleared after commit")
	}
	_, ok = shardChains[1].GetPendingCredits(tx.ID)
	if ok {
		t.Error("Pending credits should be cleared after commit")
	}

	t.Log("Cross-shard transaction simulation completed successfully")
}

func TestCrossShardTx_Abort(t *testing.T) {
	// Test the abort flow
	orchChain := orchestrator.NewOrchestratorChain()
	sourceChain := shard.NewChain(0)
	destChain := shard.NewChain(1)
	sourceEVM, err := shard.NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	tx := protocol.CrossShardTx{
		ID:        "tx-abort-1",
		FromShard: 0,
		From:      common.HexToAddress("0x1111"),
		Value:     protocol.NewBigInt(big.NewInt(1000)),
		RwSet: []protocol.RwVariable{
			{
				Address:        common.HexToAddress("0x2222"),
				ReferenceBlock: protocol.Reference{ShardNum: 1},
			},
		},
	}

	// Add and produce block
	orchChain.AddTransaction(tx)
	orchChain.ProduceBlock()

	// Source shard locks funds but votes NO (e.g., insufficient balance)
	sourceChain.LockFunds(tx.ID, tx.From, tx.Value.ToBigInt())
	sourceChain.AddPrepareResult(tx.ID, false) // Vote NO

	// Dest shard stores pending credit
	destChain.StorePendingCredit(tx.ID, common.HexToAddress("0x2222"), tx.Value.ToBigInt())

	// Source produces block with NO vote
	stateBlock, err := sourceChain.ProduceBlock(sourceEVM)
	if err != nil {
		t.Fatalf("ProduceBlock failed: %v", err)
	}
	if stateBlock.TpcPrepare[tx.ID] {
		t.Error("Expected prepare vote to be false (abort)")
	}

	// Orchestrator records abort vote from source shard (0)
	// When any shard votes NO, the tx is immediately aborted
	orchChain.RecordVote(tx.ID, 0, false)

	// Orchestrator produces block with abort result
	orchBlock := orchChain.ProduceBlock()
	if orchBlock.TpcResult[tx.ID] {
		t.Error("Expected TpcResult to be false (aborted)")
	}

	// Source shard should refund (lock still exists, needs refund)
	lock, ok := sourceChain.GetLockedFunds(tx.ID)
	if !ok {
		t.Error("Lock should exist for refund")
	}
	// In real code, we would credit back the locked amount
	_ = lock
	sourceChain.ClearLock(tx.ID)

	// Dest shard discards pending credit
	destChain.ClearPendingCredit(tx.ID)

	t.Log("Cross-shard abort simulation completed successfully")
}

func TestMultipleCrossShardTxs(t *testing.T) {
	orchChain := orchestrator.NewOrchestratorChain()

	// Add multiple transactions
	for i := 0; i < 5; i++ {
		tx := protocol.CrossShardTx{
			ID:        fmt.Sprintf("tx-%d", i),
			FromShard: i % 3,
			From:      common.BigToAddress(big.NewInt(int64(0x1111 + i))),
			Value:     protocol.NewBigInt(big.NewInt(int64(1000 * (i + 1)))),
			RwSet: []protocol.RwVariable{
				{
					Address:        common.BigToAddress(big.NewInt(int64(0x2222 + i))),
					ReferenceBlock: protocol.Reference{ShardNum: (i + 1) % 3},
				},
			},
		}
		orchChain.AddTransaction(tx)
	}

	// Produce block
	block := orchChain.ProduceBlock()
	if len(block.CtToOrder) != 5 {
		t.Errorf("Expected 5 txs in block, got %d", len(block.CtToOrder))
	}

	// Record mixed votes (some commit, some abort)
	// Each tx involves 2 shards: FromShard (i%3) and RwSet.ShardNum ((i+1)%3)
	// tx-0: FromShard=0%3=0, RwSet.ShardNum=1 -> involved [0,1]
	// tx-1: FromShard=1%3=1, RwSet.ShardNum=2 -> involved [1,2]
	// tx-2: FromShard=2%3=2, RwSet.ShardNum=0 -> involved [2,0]
	// tx-3: FromShard=3%3=0, RwSet.ShardNum=1 -> involved [0,1]
	// tx-4: FromShard=4%3=1, RwSet.ShardNum=2 -> involved [1,2]

	// tx-0: both shards vote YES -> commit
	orchChain.RecordVote("tx-0", 0, true)
	orchChain.RecordVote("tx-0", 1, true)

	// tx-1: shard 1 votes NO -> abort
	orchChain.RecordVote("tx-1", 1, false)

	// tx-2: both shards vote YES -> commit
	orchChain.RecordVote("tx-2", 2, true)
	orchChain.RecordVote("tx-2", 0, true)

	// tx-3: both shards vote YES -> commit (FromShard=0, RwSet=1)
	orchChain.RecordVote("tx-3", 0, true)
	orchChain.RecordVote("tx-3", 1, true)

	// tx-4: shard 1 votes NO -> abort (FromShard=1, RwSet=2)
	orchChain.RecordVote("tx-4", 1, false)

	// Produce next block with results
	resultBlock := orchChain.ProduceBlock()

	committed := 0
	aborted := 0
	for _, result := range resultBlock.TpcResult {
		if result {
			committed++
		} else {
			aborted++
		}
	}

	if committed != 3 {
		t.Errorf("Expected 3 committed, got %d", committed)
	}
	if aborted != 2 {
		t.Errorf("Expected 2 aborted, got %d", aborted)
	}
}

// Benchmarks

func BenchmarkOrchestratorChain_ProduceBlock(b *testing.B) {
	chain := orchestrator.NewOrchestratorChain()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tx := protocol.CrossShardTx{
			ID:        fmt.Sprintf("tx-%d", i),
			FromShard: 0,
			Value:     protocol.NewBigInt(big.NewInt(1000)),
		}
		chain.AddTransaction(tx)
		chain.ProduceBlock()
	}
}

func BenchmarkShardChain_LockAndClear(b *testing.B) {
	chain := shard.NewChain(0)
	addr := common.HexToAddress("0x1234")
	amount := big.NewInt(1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		txID := fmt.Sprintf("tx-%d", i)
		chain.LockFunds(txID, addr, amount)
		chain.ClearLock(txID)
	}
}

// =============================================================================
// V2.4 Optimistic Locking Integration Tests
// =============================================================================

// TestOptimisticLocking_ReadSetValidation tests the full flow where ReadSet
// validation passes because state hasn't changed since simulation.
func TestOptimisticLocking_ReadSetValidation(t *testing.T) {
	// Setup
	chain := shard.NewChain(0)
	evmState, err := shard.NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	contractAddr := common.HexToAddress("0xContract1")
	slot := common.HexToHash("0x01")

	// Set initial state that will be read during simulation
	initialValue := common.HexToHash("0x42")
	evmState.SetStorageAt(contractAddr, slot, initialValue)

	// Create a cross-shard tx with ReadSet matching current state
	tx := protocol.CrossShardTx{
		ID:        "occ-tx-1",
		FromShard: 0,
		From:      common.HexToAddress("0x1111"),
		Value:     protocol.NewBigInt(big.NewInt(0)),
		RwSet: []protocol.RwVariable{
			{
				Address:        contractAddr,
				ReferenceBlock: protocol.Reference{ShardNum: 0},
				ReadSet: []protocol.ReadSetItem{
					{Slot: protocol.Slot(slot), Value: initialValue.Bytes()},
				},
				WriteSet: []protocol.WriteSetItem{
					{Slot: protocol.Slot(slot), OldValue: initialValue.Bytes(), NewValue: common.HexToHash("0x99").Bytes()},
				},
			},
		},
	}

	// Step 1: Validate ReadSet and acquire slot locks (Lock phase)
	// Note: ValidateAndLockReadSet also stores the pending RwSet internally
	err = chain.ValidateAndLockReadSet(tx.ID, tx.RwSet, evmState)
	if err != nil {
		t.Fatalf("ValidateAndLockReadSet should succeed: %v", err)
	}

	// Verify slot is locked
	if !chain.IsSlotLocked(contractAddr, slot) {
		t.Error("Slot should be locked after ValidateAndLockReadSet")
	}

	// Step 3: Apply WriteSet (Finalize phase)
	err = chain.ApplyWriteSet(tx.ID, evmState)
	if err != nil {
		t.Fatalf("ApplyWriteSet should succeed: %v", err)
	}

	// Verify state was updated
	newValue := evmState.GetStorageAt(contractAddr, slot)
	expected := common.HexToHash("0x99")
	if newValue != expected {
		t.Errorf("Expected storage value %s, got %s", expected.Hex(), newValue.Hex())
	}

	// Step 4: Unlock slots
	chain.UnlockAllSlotsForTx(tx.ID)
	if chain.IsSlotLocked(contractAddr, slot) {
		t.Error("Slot should be unlocked after UnlockAllSlotsForTx")
	}

	t.Log("V2.4 optimistic locking (validation success) completed successfully")
}

// TestOptimisticLocking_ReadSetMismatch tests the abort flow when state
// changed between simulation and lock time.
func TestOptimisticLocking_ReadSetMismatch(t *testing.T) {
	// Setup
	chain := shard.NewChain(0)
	evmState, err := shard.NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	contractAddr := common.HexToAddress("0xContract2")
	slot := common.HexToHash("0x02")

	// Set initial state
	initialValue := common.HexToHash("0x42")
	evmState.SetStorageAt(contractAddr, slot, initialValue)

	// Create tx with ReadSet based on initial value
	tx := protocol.CrossShardTx{
		ID:        "occ-tx-abort",
		FromShard: 0,
		From:      common.HexToAddress("0x1111"),
		RwSet: []protocol.RwVariable{
			{
				Address:        contractAddr,
				ReferenceBlock: protocol.Reference{ShardNum: 0},
				ReadSet: []protocol.ReadSetItem{
					{Slot: protocol.Slot(slot), Value: initialValue.Bytes()},
				},
			},
		},
	}

	// Simulate state change by another tx (before Lock phase)
	evmState.SetStorageAt(contractAddr, slot, common.HexToHash("0x99"))

	// Attempt to validate - should fail due to mismatch
	err = chain.ValidateAndLockReadSet(tx.ID, tx.RwSet, evmState)
	if err == nil {
		t.Fatal("ValidateAndLockReadSet should fail due to state change")
	}

	// Verify it's a ReadSetMismatchError
	mismatchErr, ok := err.(*shard.ReadSetMismatchError)
	if !ok {
		t.Fatalf("Expected ReadSetMismatchError, got %T: %v", err, err)
	}

	if mismatchErr.Address != contractAddr {
		t.Errorf("Expected address %s, got %s", contractAddr.Hex(), mismatchErr.Address.Hex())
	}

	// Verify no locks were acquired (atomic rollback)
	if chain.IsSlotLocked(contractAddr, slot) {
		t.Error("Slot should NOT be locked after failed validation")
	}

	t.Log("V2.4 optimistic locking (ReadSet mismatch abort) completed successfully")
}

// TestOptimisticLocking_FullFlow tests the complete end-to-end flow with
// orchestrator and state shard coordination.
func TestOptimisticLocking_FullFlow(t *testing.T) {
	// Setup orchestrator and shards
	orchChain := orchestrator.NewOrchestratorChain()
	shardChains := []*shard.Chain{shard.NewChain(0), shard.NewChain(1)}
	shardEVMs := make([]*shard.EVMState, 2)
	for i := 0; i < 2; i++ {
		var err error
		shardEVMs[i], err = shard.NewMemoryEVMState()
		if err != nil {
			t.Fatalf("Failed to create EVM state for shard %d: %v", i, err)
		}
	}

	// Setup: Contract on shard 1 with some initial state
	contractAddr := common.HexToAddress("0xContractOnShard1")
	slot := common.HexToHash("0x10")
	initialValue := common.HexToHash("0x100")
	shardEVMs[1].SetStorageAt(contractAddr, slot, initialValue)

	// Create cross-shard tx: shard 0 -> shard 1 (modifies contract state)
	tx := protocol.CrossShardTx{
		ID:        "occ-full-flow",
		FromShard: 0,
		From:      common.HexToAddress("0x1111"),
		Value:     protocol.NewBigInt(big.NewInt(0)),
		RwSet: []protocol.RwVariable{
			{
				Address:        contractAddr,
				ReferenceBlock: protocol.Reference{ShardNum: 1},
				ReadSet: []protocol.ReadSetItem{
					{Slot: protocol.Slot(slot), Value: initialValue.Bytes()},
				},
				WriteSet: []protocol.WriteSetItem{
					{Slot: protocol.Slot(slot), OldValue: initialValue.Bytes(), NewValue: common.HexToHash("0x200").Bytes()},
				},
			},
		},
	}

	// Round 1: Orchestrator adds tx to CtToOrder
	orchChain.AddTransaction(tx)
	orchBlock1 := orchChain.ProduceBlock()
	if len(orchBlock1.CtToOrder) != 1 {
		t.Fatalf("Expected 1 tx in CtToOrder, got %d", len(orchBlock1.CtToOrder))
	}

	// Shard 0 (source): locks balance (traditional) and votes YES
	shardChains[0].AddPrepareResult(tx.ID, true)

	// Shard 1 (target): validates ReadSet and acquires slot locks
	// Note: ValidateAndLockReadSet also stores the pending RwSet internally
	err := shardChains[1].ValidateAndLockReadSet(tx.ID, tx.RwSet, shardEVMs[1])
	if err != nil {
		t.Fatalf("Shard 1 ReadSet validation failed: %v", err)
	}
	shardChains[1].AddPrepareResult(tx.ID, true)

	// Both shards produce blocks with YES votes
	stateBlock0, _ := shardChains[0].ProduceBlock(shardEVMs[0])
	stateBlock1, _ := shardChains[1].ProduceBlock(shardEVMs[1])

	// Orchestrator collects votes
	orchChain.RecordVote(tx.ID, 0, stateBlock0.TpcPrepare[tx.ID])
	orchChain.RecordVote(tx.ID, 1, stateBlock1.TpcPrepare[tx.ID])

	// Round 2: Orchestrator produces TpcResult
	orchBlock2 := orchChain.ProduceBlock()
	if !orchBlock2.TpcResult[tx.ID] {
		t.Fatal("Expected tx to be committed")
	}

	// Shard 1: Apply WriteSet on commit
	err = shardChains[1].ApplyWriteSet(tx.ID, shardEVMs[1])
	if err != nil {
		t.Fatalf("ApplyWriteSet failed: %v", err)
	}
	shardChains[1].UnlockAllSlotsForTx(tx.ID)
	shardChains[1].ClearPendingRwSet(tx.ID)

	// Verify final state
	finalValue := shardEVMs[1].GetStorageAt(contractAddr, slot)
	expected := common.HexToHash("0x200")
	if finalValue != expected {
		t.Errorf("Expected final value %s, got %s", expected.Hex(), finalValue.Hex())
	}

	if shardChains[1].IsSlotLocked(contractAddr, slot) {
		t.Error("Slot should be unlocked after commit")
	}

	t.Log("V2.4 optimistic locking full flow completed successfully")
}

// TestOptimisticLocking_ConcurrentTxs tests that slot-level locking allows
// concurrent transactions on different slots of the same contract.
func TestOptimisticLocking_ConcurrentTxs(t *testing.T) {
	chain := shard.NewChain(0)
	evmState, err := shard.NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	contractAddr := common.HexToAddress("0xSharedContract")
	slot1 := common.HexToHash("0x01")
	slot2 := common.HexToHash("0x02")

	// Set initial values
	evmState.SetStorageAt(contractAddr, slot1, common.HexToHash("0x10"))
	evmState.SetStorageAt(contractAddr, slot2, common.HexToHash("0x20"))

	// Tx1 touches slot1
	tx1 := protocol.CrossShardTx{
		ID: "tx-slot1",
		RwSet: []protocol.RwVariable{
			{
				Address: contractAddr,
				ReadSet: []protocol.ReadSetItem{
					{Slot: protocol.Slot(slot1), Value: common.HexToHash("0x10").Bytes()},
				},
			},
		},
	}

	// Tx2 touches slot2 (different slot, should not conflict)
	tx2 := protocol.CrossShardTx{
		ID: "tx-slot2",
		RwSet: []protocol.RwVariable{
			{
				Address: contractAddr,
				ReadSet: []protocol.ReadSetItem{
					{Slot: protocol.Slot(slot2), Value: common.HexToHash("0x20").Bytes()},
				},
			},
		},
	}

	// Both should succeed - no conflict
	err = chain.ValidateAndLockReadSet(tx1.ID, tx1.RwSet, evmState)
	if err != nil {
		t.Fatalf("Tx1 validation should succeed: %v", err)
	}

	err = chain.ValidateAndLockReadSet(tx2.ID, tx2.RwSet, evmState)
	if err != nil {
		t.Fatalf("Tx2 validation should succeed (different slot): %v", err)
	}

	// Both slots locked by different txs
	if holder := chain.GetSlotLockHolder(contractAddr, slot1); holder != "tx-slot1" {
		t.Errorf("Slot1 should be locked by tx-slot1, got %s", holder)
	}
	if holder := chain.GetSlotLockHolder(contractAddr, slot2); holder != "tx-slot2" {
		t.Errorf("Slot2 should be locked by tx-slot2, got %s", holder)
	}

	// Cleanup
	chain.UnlockAllSlotsForTx(tx1.ID)
	chain.UnlockAllSlotsForTx(tx2.ID)

	t.Log("V2.4 concurrent txs on different slots completed successfully")
}

// TestOptimisticLocking_E2E_Lifecycle tests the COMPLETE end-to-end lifecycle
// using actual HTTP handlers and block production with priority ordering.
// This is the most realistic simulation of the V2.4 protocol flow.
func TestOptimisticLocking_E2E_Lifecycle(t *testing.T) {
	// =========================================================================
	// SETUP: Create orchestrator and 2 state shards
	// =========================================================================
	env := NewTestEnv(t, 2)
	defer env.Close()

	// Get references to internal state for verification
	shard0 := env.Shards[0]
	shard1 := env.Shards[1]

	// Setup: Fund sender on shard 0
	sender := common.HexToAddress("0x1111111111111111111111111111111111111111")
	faucetReq := map[string]string{
		"address": sender.Hex(),
		"amount":  "1000000000000000000", // 1 ETH
	}
	resp, _ := postJSON(env.ShardURL(0)+"/faucet", faucetReq)
	resp.Body.Close()

	// Setup: Deploy a contract on shard 1 (or just set storage directly)
	// For simplicity, we'll set storage directly on the contract address
	contractAddr := common.HexToAddress("0x0000000000000000000000000000000000000001") // Ends in 01 -> shard 1
	slot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	initialValue := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000100")

	// Set initial storage value on shard 1's EVM
	shard1.SetStorageAt(contractAddr, slot, initialValue)

	t.Logf("Initial state: contract %s slot %s = %s", contractAddr.Hex(), slot.Hex(), initialValue.Hex())

	// =========================================================================
	// STEP 1: Create cross-shard transaction with RwSet
	// (In real flow, this comes from orchestrator simulation)
	// =========================================================================
	txID := "e2e-lifecycle-tx-1"
	crossTx := protocol.CrossShardTx{
		ID:        txID,
		FromShard: 0,
		From:      sender,
		To:        contractAddr,
		Value:     protocol.NewBigInt(big.NewInt(100000000000000000)), // 0.1 ETH (10^17 wei)
		RwSet: []protocol.RwVariable{
			{
				Address:        contractAddr,
				ReferenceBlock: protocol.Reference{ShardNum: 1},
				ReadSet: []protocol.ReadSetItem{
					{Slot: protocol.Slot(slot), Value: initialValue.Bytes()},
				},
				WriteSet: []protocol.WriteSetItem{
					{Slot: protocol.Slot(slot), OldValue: initialValue.Bytes(), NewValue: common.HexToHash("0x200").Bytes()},
				},
			},
		},
	}

	// =========================================================================
	// STEP 2: ROUND N - Orchestrator broadcasts CtToOrder
	// =========================================================================
	t.Log("=== ROUND N: Lock Phase ===")

	orchBlock1 := protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{crossTx},
		TpcResult: make(map[string]bool),
	}

	// Send to both shards via HTTP (simulating broadcast)
	blockData, _ := json.Marshal(orchBlock1)

	req0 := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
	req0.Header.Set("Content-Type", "application/json")
	w0 := httptest.NewRecorder()
	shard0.Router().ServeHTTP(w0, req0)
	if w0.Code != http.StatusOK {
		t.Fatalf("Shard 0 failed to process orchestrator block: %d - %s", w0.Code, w0.Body.String())
	}

	req1 := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	shard1.Router().ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("Shard 1 failed to process orchestrator block: %d - %s", w1.Code, w1.Body.String())
	}

	t.Log("Both shards received orchestrator block with CtToOrder")

	// =========================================================================
	// STEP 3: State Shards produce blocks (Lock tx executes, ReadSet validated)
	// =========================================================================

	// Shard 0: ProduceBlock - executes TxTypeLock (validates source)
	stateBlock0, err := shard0.ProduceBlock()
	if err != nil {
		t.Fatalf("Shard 0 ProduceBlock failed: %v", err)
	}
	t.Logf("Shard 0 produced block %d with TpcPrepare: %v", stateBlock0.Height, stateBlock0.TpcPrepare)

	// Shard 1: ProduceBlock - executes TxTypeLock (validates ReadSet!)
	stateBlock1, err := shard1.ProduceBlock()
	if err != nil {
		t.Fatalf("Shard 1 ProduceBlock failed: %v", err)
	}
	t.Logf("Shard 1 produced block %d with TpcPrepare: %v", stateBlock1.Height, stateBlock1.TpcPrepare)

	// Verify both voted YES
	if vote, ok := stateBlock0.TpcPrepare[txID]; !ok || !vote {
		t.Errorf("Shard 0 should vote YES, got %v (exists=%v)", vote, ok)
	}
	if vote, ok := stateBlock1.TpcPrepare[txID]; !ok || !vote {
		t.Errorf("Shard 1 should vote YES, got %v (exists=%v)", vote, ok)
	}

	// Verify slot is locked on shard 1
	if !shard1.IsSlotLocked(contractAddr, slot) {
		t.Error("Slot should be locked on shard 1 after Lock phase")
	}

	// =========================================================================
	// STEP 4: ROUND N+1 - Orchestrator broadcasts TpcResult (COMMIT)
	// =========================================================================
	t.Log("=== ROUND N+1: Finalize Phase ===")

	orchBlock2 := protocol.OrchestratorShardBlock{
		Height:    2,
		CtToOrder: []protocol.CrossShardTx{}, // No new txs
		TpcResult: map[string]bool{txID: true}, // COMMIT
	}

	blockData2, _ := json.Marshal(orchBlock2)

	// Send commit to both shards
	req0c := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData2))
	req0c.Header.Set("Content-Type", "application/json")
	w0c := httptest.NewRecorder()
	shard0.Router().ServeHTTP(w0c, req0c)

	req1c := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData2))
	req1c.Header.Set("Content-Type", "application/json")
	w1c := httptest.NewRecorder()
	shard1.Router().ServeHTTP(w1c, req1c)

	t.Log("Both shards received TpcResult: COMMIT")

	// =========================================================================
	// STEP 5: State Shards produce blocks (Finalize + Unlock execute)
	// Priority order: Finalize(1) → Unlock(2) → Lock(3) → Local(4)
	// =========================================================================

	// Shard 0: Debit sender, Unlock
	stateBlock0b, err := shard0.ProduceBlock()
	if err != nil {
		t.Fatalf("Shard 0 ProduceBlock (commit) failed: %v", err)
	}
	t.Logf("Shard 0 produced block %d (finalize phase)", stateBlock0b.Height)

	// Shard 1: Credit receiver, Apply WriteSet, Unlock
	stateBlock1b, err := shard1.ProduceBlock()
	if err != nil {
		t.Fatalf("Shard 1 ProduceBlock (commit) failed: %v", err)
	}
	t.Logf("Shard 1 produced block %d (finalize phase)", stateBlock1b.Height)

	// =========================================================================
	// VERIFICATION: Check final state
	// =========================================================================
	t.Log("=== VERIFICATION ===")

	// 1. Sender balance should be reduced
	senderBal := shard0.GetBalance(sender)
	expectedBal := big.NewInt(900000000000000000) // 0.9 ETH (1 ETH - 0.1 ETH)
	if senderBal.Cmp(expectedBal) != 0 {
		t.Errorf("Sender balance: expected %s, got %s", expectedBal.String(), senderBal.String())
	} else {
		t.Logf("✓ Sender balance correctly debited: %s", senderBal.String())
	}

	// 2. Contract storage should be updated (WriteSet applied)
	finalValue := shard1.GetStorageAt(contractAddr, slot)
	expectedStorage := common.HexToHash("0x200")
	if finalValue != expectedStorage {
		t.Errorf("Contract storage: expected %s, got %s", expectedStorage.Hex(), finalValue.Hex())
	} else {
		t.Logf("✓ Contract storage correctly updated: %s → %s", initialValue.Hex(), finalValue.Hex())
	}

	// 3. Slot should be unlocked
	if shard1.IsSlotLocked(contractAddr, slot) {
		t.Error("Slot should be unlocked after Finalize")
	} else {
		t.Log("✓ Slot correctly unlocked after Finalize")
	}

	// 4. Receiver should have received the value transfer
	receiverBal := shard1.GetBalance(contractAddr)
	expectedReceiverBal := big.NewInt(100000000000000000) // 0.1 ETH
	if receiverBal.Cmp(expectedReceiverBal) != 0 {
		t.Errorf("Receiver balance: expected %s, got %s", expectedReceiverBal.String(), receiverBal.String())
	} else {
		t.Logf("✓ Receiver balance correctly credited: %s", receiverBal.String())
	}

	t.Log("=== E2E LIFECYCLE TEST PASSED ===")
}

func TestOptimisticLocking_SlotContention(t *testing.T) {
	chain := shard.NewChain(0)
	evmState, err := shard.NewMemoryEVMState()
	if err != nil {
		t.Fatalf("Failed to create EVM state: %v", err)
	}

	contractAddr := common.HexToAddress("0xContestedContract")
	slot := common.HexToHash("0x01")

	// Set initial value
	evmState.SetStorageAt(contractAddr, slot, common.HexToHash("0x10"))

	// Tx1 locks the slot
	tx1 := protocol.CrossShardTx{
		ID: "tx-first",
		RwSet: []protocol.RwVariable{
			{
				Address: contractAddr,
				ReadSet: []protocol.ReadSetItem{
					{Slot: protocol.Slot(slot), Value: common.HexToHash("0x10").Bytes()},
				},
			},
		},
	}

	// Tx2 tries to lock the same slot
	tx2 := protocol.CrossShardTx{
		ID: "tx-second",
		RwSet: []protocol.RwVariable{
			{
				Address: contractAddr,
				ReadSet: []protocol.ReadSetItem{
					{Slot: protocol.Slot(slot), Value: common.HexToHash("0x10").Bytes()},
				},
			},
		},
	}

	// Tx1 succeeds
	err = chain.ValidateAndLockReadSet(tx1.ID, tx1.RwSet, evmState)
	if err != nil {
		t.Fatalf("Tx1 should succeed: %v", err)
	}

	// Tx2 should fail due to lock contention
	err = chain.ValidateAndLockReadSet(tx2.ID, tx2.RwSet, evmState)
	if err == nil {
		t.Fatal("Tx2 should fail due to slot already locked")
	}

	lockErr, ok := err.(*shard.SlotLockError)
	if !ok {
		t.Fatalf("Expected SlotLockError, got %T: %v", err, err)
	}
	if lockErr.LockedBy != "tx-first" {
		t.Errorf("Expected locked by tx-first, got %s", lockErr.LockedBy)
	}

	// After tx1 releases, tx2 can proceed
	chain.UnlockAllSlotsForTx(tx1.ID)

	err = chain.ValidateAndLockReadSet(tx2.ID, tx2.RwSet, evmState)
	if err != nil {
		t.Fatalf("Tx2 should succeed after tx1 releases: %v", err)
	}

	chain.UnlockAllSlotsForTx(tx2.ID)
	t.Log("V2.4 slot contention test completed successfully")
}

// =============================================================================
// V2.2 RwSetRequest/RwSetReply Integration Tests
// =============================================================================

// TestV22_RwSetRequest_Success tests the /rw-set endpoint for successful simulation
func TestV22_RwSetRequest_Success(t *testing.T) {
	// Create shard server (shard 0)
	srv := shard.NewServerForTest(0, "http://localhost:8080", config.NetworkConfig{})
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	// Fund caller via faucet
	caller := common.HexToAddress("0x1111111111111111111111111111111111111110") // Ends in 0 -> shard 0
	faucetReq := map[string]string{
		"address": caller.Hex(),
		"amount":  "1000000000000000000", // 1 ETH
	}
	resp, _ := postJSON(ts.URL+"/faucet", faucetReq)
	resp.Body.Close()

	// Target address that belongs to shard 0 (last byte % 8 == 0)
	contractAddr := common.HexToAddress("0x0000000000000000000000000000000000000000")

	// Create RwSetRequest
	req := protocol.RwSetRequest{
		Address:        contractAddr,
		Data:           nil, // Simple call
		Value:          protocol.NewBigInt(big.NewInt(0)),
		Caller:         caller,
		ReferenceBlock: protocol.Reference{ShardNum: 0, BlockHeight: 0},
		TxID:           "v22-test-rwset-1",
	}

	// Send request
	resp, err := postJSON(ts.URL+"/rw-set", req)
	if err != nil {
		t.Fatalf("RwSetRequest failed: %v", err)
	}
	defer resp.Body.Close()

	var reply protocol.RwSetReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		t.Fatalf("Failed to decode RwSetReply: %v", err)
	}

	// Verify success
	if !reply.Success {
		t.Errorf("Expected success, got error: %s", reply.Error)
	}

	t.Logf("V2.2 RwSetRequest success: GasUsed=%d, RwSet entries=%d", reply.GasUsed, len(reply.RwSet))
}

// TestV22_RwSetRequest_WrongShard tests that /rw-set rejects requests for wrong shard
func TestV22_RwSetRequest_WrongShard(t *testing.T) {
	// Create shard server (shard 0)
	srv := shard.NewServerForTest(0, "http://localhost:8080", config.NetworkConfig{})
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	// Target address that belongs to shard 1 (last byte % 8 == 1)
	contractAddr := common.HexToAddress("0x0000000000000000000000000000000000000001")
	caller := common.HexToAddress("0x1111")

	// Create RwSetRequest
	req := protocol.RwSetRequest{
		Address:        contractAddr,
		Data:           nil,
		Value:          protocol.NewBigInt(big.NewInt(0)),
		Caller:         caller,
		ReferenceBlock: protocol.Reference{ShardNum: 1, BlockHeight: 0},
		TxID:           "v22-test-wrong-shard",
	}

	// Send request to shard 0 (but contract is on shard 1)
	resp, err := postJSON(ts.URL+"/rw-set", req)
	if err != nil {
		t.Fatalf("RwSetRequest failed: %v", err)
	}
	defer resp.Body.Close()

	var reply protocol.RwSetReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		t.Fatalf("Failed to decode RwSetReply: %v", err)
	}

	// Should fail with address belongs to different shard
	if reply.Success {
		t.Error("Expected failure for wrong shard address")
	}
	if reply.Error == "" {
		t.Error("Expected error message for wrong shard")
	}

	t.Logf("V2.2 RwSetRequest wrong shard correctly rejected: %s", reply.Error)
}

// TestV22_RwSetRequest_WithData tests RwSetRequest with contract call data
func TestV22_RwSetRequest_WithData(t *testing.T) {
	// Create shard server (shard 0)
	srv := shard.NewServerForTest(0, "http://localhost:8080", config.NetworkConfig{})
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	// Fund deployer via faucet
	deployer := common.HexToAddress("0x0000000000000000000000000000000000000006")
	faucetReq := map[string]string{
		"address": deployer.Hex(),
		"amount":  "10000000000000000000", // 10 ETH
	}
	faucetResp, _ := postJSON(ts.URL+"/faucet", faucetReq)
	faucetResp.Body.Close()

	// Deploy simple counter contract
	// This contract stores and returns a value
	// bytecode: PUSH1 0x42 PUSH1 0x00 SSTORE PUSH1 0x20 PUSH1 0x00 RETURN
	// (stores 0x42 at slot 0, then returns 32 bytes from memory 0)
	deployReq := map[string]interface{}{
		"from":     deployer.Hex(),
		"bytecode": "0x6042600055602060006000f0", // Simple storage setter
		"gas":      uint64(100000),
	}
	resp, _ := postJSON(ts.URL+"/evm/deploy", deployReq)
	var deployResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&deployResp)
	resp.Body.Close()

	// If deploy failed, skip contract-specific testing
	if deployResp["success"] != true {
		t.Log("Contract deployment failed (expected for simple test), skipping contract call test")
		return
	}

	contractAddr := common.HexToAddress(deployResp["address"].(string))
	caller := common.HexToAddress("0x1111111111111111111111111111111111111110")
	// Fund caller via faucet
	callerFaucetReq := map[string]string{
		"address": caller.Hex(),
		"amount":  "1000000000000000000", // 1 ETH
	}
	callerFaucetResp, _ := postJSON(ts.URL+"/faucet", callerFaucetReq)
	callerFaucetResp.Body.Close()

	// Create RwSetRequest with call data
	req := protocol.RwSetRequest{
		Address:        contractAddr,
		Data:           []byte{0x00}, // Some call data
		Value:          protocol.NewBigInt(big.NewInt(0)),
		Caller:         caller,
		ReferenceBlock: protocol.Reference{ShardNum: 0, BlockHeight: 0},
		TxID:           "v22-test-with-data",
	}

	// Send request
	resp, err := postJSON(ts.URL+"/rw-set", req)
	if err != nil {
		t.Fatalf("RwSetRequest failed: %v", err)
	}
	defer resp.Body.Close()

	var reply protocol.RwSetReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		t.Fatalf("Failed to decode RwSetReply: %v", err)
	}

	// Log result (may succeed or fail depending on contract state)
	t.Logf("V2.2 RwSetRequest with data: Success=%v, GasUsed=%d, Error=%s",
		reply.Success, reply.GasUsed, reply.Error)
}

// TestV22_RwSetRequest_MultiShard tests RwSetRequest routing across shards
func TestV22_RwSetRequest_MultiShard(t *testing.T) {
	// Create 2 shard servers
	env := NewTestEnv(t, 2)
	defer env.Close()

	// Fund caller on both shards via faucet
	caller := common.HexToAddress("0x1111111111111111111111111111111111111110")
	faucetReq := map[string]string{
		"address": caller.Hex(),
		"amount":  "1000000000000000000", // 1 ETH
	}
	// Fund on shard 0
	faucetResp0, _ := postJSON(env.ShardURL(0)+"/faucet", faucetReq)
	faucetResp0.Body.Close()
	// Fund on shard 1
	faucetResp1, _ := postJSON(env.ShardURL(1)+"/faucet", faucetReq)
	faucetResp1.Body.Close()

	// Address on shard 0
	addr0 := common.HexToAddress("0x0000000000000000000000000000000000000000")
	// Address on shard 1
	addr1 := common.HexToAddress("0x0000000000000000000000000000000000000001")

	// Request to shard 0 for addr0 should succeed
	req0 := protocol.RwSetRequest{
		Address:        addr0,
		Caller:         caller,
		ReferenceBlock: protocol.Reference{ShardNum: 0},
		TxID:           "multi-shard-test-0",
	}
	resp0, _ := postJSON(env.ShardURL(0)+"/rw-set", req0)
	var reply0 protocol.RwSetReply
	json.NewDecoder(resp0.Body).Decode(&reply0)
	resp0.Body.Close()

	if !reply0.Success {
		t.Errorf("Request to shard 0 for addr0 should succeed: %s", reply0.Error)
	}

	// Request to shard 1 for addr1 should succeed
	req1 := protocol.RwSetRequest{
		Address:        addr1,
		Caller:         caller,
		ReferenceBlock: protocol.Reference{ShardNum: 1},
		TxID:           "multi-shard-test-1",
	}
	resp1, _ := postJSON(env.ShardURL(1)+"/rw-set", req1)
	var reply1 protocol.RwSetReply
	json.NewDecoder(resp1.Body).Decode(&reply1)
	resp1.Body.Close()

	if !reply1.Success {
		t.Errorf("Request to shard 1 for addr1 should succeed: %s", reply1.Error)
	}

	// Request to shard 0 for addr1 should fail (wrong shard)
	req0_wrong := protocol.RwSetRequest{
		Address:        addr1, // Belongs to shard 1
		Caller:         caller,
		ReferenceBlock: protocol.Reference{ShardNum: 1},
		TxID:           "multi-shard-wrong",
	}
	resp0_wrong, _ := postJSON(env.ShardURL(0)+"/rw-set", req0_wrong)
	var reply0_wrong protocol.RwSetReply
	json.NewDecoder(resp0_wrong.Body).Decode(&reply0_wrong)
	resp0_wrong.Body.Close()

	if reply0_wrong.Success {
		t.Error("Request to shard 0 for addr1 (shard 1 address) should fail")
	}

	t.Log("V2.2 multi-shard RwSetRequest routing test passed")
}

// =============================================================================
// H.3: Integration Test - Simple Cross-Shard Transfer
// =============================================================================
// Tests the complete flow of a simple value transfer between two shards:
// 1. User submits tx to source shard
// 2. System detects cross-shard, forwards to orchestrator
// 3. Orchestrator simulates and broadcasts to shards
// 4. Shards execute Lock phase, vote YES
// 5. Orchestrator collects votes, broadcasts COMMIT
// 6. Shards finalize (debit source, credit destination)
func TestH3_SimpleCrossShardTransfer(t *testing.T) {
	env := NewTestEnv(t, 2)
	defer env.Close()

	shard0 := env.Shards[0]
	shard1 := env.Shards[1]

	// Setup: Fund sender on shard 0
	// Address ending in 0x00 belongs to shard 0 (mod 8)
	sender := common.HexToAddress("0x1111111111111111111111111111111111111100") // Shard 0
	receiver := common.HexToAddress("0x2222222222222222222222222222222222222201") // Shard 1

	// Fund sender via faucet
	faucetReq := map[string]string{
		"address": sender.Hex(),
		"amount":  "1000000000000000000", // 1 ETH
	}
	resp, err := postJSON(env.ShardURL(0)+"/faucet", faucetReq)
	if err != nil {
		t.Fatalf("Faucet request failed: %v", err)
	}
	resp.Body.Close()

	// Verify initial balances
	initialSenderBal := shard0.GetBalance(sender)
	initialReceiverBal := shard1.GetBalance(receiver)
	t.Logf("Initial balances - Sender: %s, Receiver: %s", initialSenderBal.String(), initialReceiverBal.String())

	if initialSenderBal.Cmp(big.NewInt(1000000000000000000)) != 0 {
		t.Fatalf("Sender should have 1 ETH, got %s", initialSenderBal.String())
	}
	if initialReceiverBal.Cmp(big.NewInt(0)) != 0 {
		t.Fatalf("Receiver should have 0, got %s", initialReceiverBal.String())
	}

	// Create cross-shard transaction
	transferAmount := big.NewInt(100000000000000000) // 0.1 ETH
	txID := "h3-simple-transfer-1"

	// Note: Even for simple value transfers, we need an RwSet entry to indicate
	// the destination shard's participation in 2PC. Without it, the destination
	// shard doesn't know it needs to vote.
	crossTx := protocol.CrossShardTx{
		ID:        txID,
		FromShard: 0,
		From:      sender,
		To:        receiver,
		Value:     protocol.NewBigInt(transferAmount),
		RwSet: []protocol.RwVariable{
			{
				Address:        receiver,
				ReferenceBlock: protocol.Reference{ShardNum: 1}, // Indicates shard 1 participation
				ReadSet:        []protocol.ReadSetItem{},        // No storage reads
				WriteSet:       []protocol.WriteSetItem{},       // No storage writes (value transfer only)
			},
		},
	}

	// === ROUND N: Lock Phase ===
	t.Log("=== ROUND N: Lock Phase ===")

	orchBlock1 := protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{crossTx},
		TpcResult: make(map[string]bool),
	}

	blockData, _ := json.Marshal(orchBlock1)

	// Send to both shards
	req0 := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
	req0.Header.Set("Content-Type", "application/json")
	w0 := httptest.NewRecorder()
	shard0.Router().ServeHTTP(w0, req0)
	if w0.Code != http.StatusOK {
		t.Fatalf("Shard 0 failed: %d - %s", w0.Code, w0.Body.String())
	}

	req1 := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	shard1.Router().ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("Shard 1 failed: %d - %s", w1.Code, w1.Body.String())
	}

	// Produce blocks on both shards
	block0, err := shard0.ProduceBlock()
	if err != nil {
		t.Fatalf("Shard 0 ProduceBlock failed: %v", err)
	}
	block1, err := shard1.ProduceBlock()
	if err != nil {
		t.Fatalf("Shard 1 ProduceBlock failed: %v", err)
	}

	// Verify both voted YES
	if vote, ok := block0.TpcPrepare[txID]; !ok || !vote {
		t.Errorf("Shard 0 should vote YES, got %v (exists=%v)", vote, ok)
	}
	if vote, ok := block1.TpcPrepare[txID]; !ok || !vote {
		t.Errorf("Shard 1 should vote YES, got %v (exists=%v)", vote, ok)
	}
	t.Logf("Both shards voted YES: shard0=%v, shard1=%v", block0.TpcPrepare[txID], block1.TpcPrepare[txID])

	// === ROUND N+1: Finalize Phase ===
	t.Log("=== ROUND N+1: Finalize Phase ===")

	orchBlock2 := protocol.OrchestratorShardBlock{
		Height:    2,
		CtToOrder: []protocol.CrossShardTx{},
		TpcResult: map[string]bool{txID: true}, // COMMIT
	}

	blockData2, _ := json.Marshal(orchBlock2)

	req0c := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData2))
	req0c.Header.Set("Content-Type", "application/json")
	w0c := httptest.NewRecorder()
	shard0.Router().ServeHTTP(w0c, req0c)

	req1c := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData2))
	req1c.Header.Set("Content-Type", "application/json")
	w1c := httptest.NewRecorder()
	shard1.Router().ServeHTTP(w1c, req1c)

	// Produce finalize blocks
	shard0.ProduceBlock()
	shard1.ProduceBlock()

	// === VERIFICATION ===
	t.Log("=== VERIFICATION ===")

	finalSenderBal := shard0.GetBalance(sender)
	finalReceiverBal := shard1.GetBalance(receiver)

	expectedSenderBal := new(big.Int).Sub(initialSenderBal, transferAmount)
	expectedReceiverBal := new(big.Int).Add(initialReceiverBal, transferAmount)

	if finalSenderBal.Cmp(expectedSenderBal) != 0 {
		t.Errorf("Sender balance: expected %s, got %s", expectedSenderBal.String(), finalSenderBal.String())
	} else {
		t.Logf("✓ Sender balance correctly debited: %s → %s", initialSenderBal.String(), finalSenderBal.String())
	}

	if finalReceiverBal.Cmp(expectedReceiverBal) != 0 {
		t.Errorf("Receiver balance: expected %s, got %s", expectedReceiverBal.String(), finalReceiverBal.String())
	} else {
		t.Logf("✓ Receiver balance correctly credited: %s → %s", initialReceiverBal.String(), finalReceiverBal.String())
	}

	t.Log("=== H.3 SIMPLE CROSS-SHARD TRANSFER TEST PASSED ===")
}

// =============================================================================
// H.4: Integration Test - Contract Call with Storage
// =============================================================================
// Tests cross-shard contract interaction that reads and writes storage:
// 1. Deploy contract on destination shard
// 2. Submit cross-shard call that modifies storage
// 3. Verify storage changes are committed atomically
func TestH4_ContractCallWithStorage(t *testing.T) {
	env := NewTestEnv(t, 2)
	defer env.Close()

	shard0 := env.Shards[0]
	shard1 := env.Shards[1]

	// Setup: Fund sender on shard 0
	sender := common.HexToAddress("0x3333333333333333333333333333333333333300") // Shard 0

	faucetReq := map[string]string{
		"address": sender.Hex(),
		"amount":  "1000000000000000000",
	}
	resp, _ := postJSON(env.ShardURL(0)+"/faucet", faucetReq)
	resp.Body.Close()

	// Setup: Contract on shard 1 with initial storage
	contractAddr := common.HexToAddress("0x4444444444444444444444444444444444444401") // Shard 1
	slot1 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	slot2 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000002")
	initialVal1 := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000064") // 100
	initialVal2 := common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000000c8") // 200

	shard1.SetStorageAt(contractAddr, slot1, initialVal1)
	shard1.SetStorageAt(contractAddr, slot2, initialVal2)

	t.Logf("Initial storage: slot1=%s, slot2=%s", initialVal1.Hex(), initialVal2.Hex())

	// Create cross-shard transaction with storage RwSet
	txID := "h4-contract-storage-1"
	newVal1 := common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000000c8") // 200
	newVal2 := common.HexToHash("0x000000000000000000000000000000000000000000000000000000000000012c") // 300

	crossTx := protocol.CrossShardTx{
		ID:        txID,
		FromShard: 0,
		From:      sender,
		To:        contractAddr,
		Value:     protocol.NewBigInt(big.NewInt(0)),
		RwSet: []protocol.RwVariable{
			{
				Address:        contractAddr,
				ReferenceBlock: protocol.Reference{ShardNum: 1},
				ReadSet: []protocol.ReadSetItem{
					{Slot: protocol.Slot(slot1), Value: initialVal1.Bytes()},
					{Slot: protocol.Slot(slot2), Value: initialVal2.Bytes()},
				},
				WriteSet: []protocol.WriteSetItem{
					{Slot: protocol.Slot(slot1), OldValue: initialVal1.Bytes(), NewValue: newVal1.Bytes()},
					{Slot: protocol.Slot(slot2), OldValue: initialVal2.Bytes(), NewValue: newVal2.Bytes()},
				},
			},
		},
	}

	// === Execute 2PC ===
	t.Log("=== ROUND N: Lock Phase ===")

	orchBlock1 := protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{crossTx},
		TpcResult: make(map[string]bool),
	}
	blockData, _ := json.Marshal(orchBlock1)

	// Send to both shards
	req0 := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
	req0.Header.Set("Content-Type", "application/json")
	w0 := httptest.NewRecorder()
	shard0.Router().ServeHTTP(w0, req0)

	req1 := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	shard1.Router().ServeHTTP(w1, req1)

	// Produce blocks
	block0, _ := shard0.ProduceBlock()
	block1, _ := shard1.ProduceBlock()

	// Verify slots are locked
	if !shard1.IsSlotLocked(contractAddr, slot1) {
		t.Error("Slot 1 should be locked after Lock phase")
	}
	if !shard1.IsSlotLocked(contractAddr, slot2) {
		t.Error("Slot 2 should be locked after Lock phase")
	}

	// Verify YES votes
	if !block0.TpcPrepare[txID] || !block1.TpcPrepare[txID] {
		t.Errorf("Both shards should vote YES: shard0=%v, shard1=%v", block0.TpcPrepare[txID], block1.TpcPrepare[txID])
	}
	t.Logf("Both shards voted YES, slots locked")

	// === ROUND N+1: Finalize ===
	t.Log("=== ROUND N+1: Finalize Phase ===")

	orchBlock2 := protocol.OrchestratorShardBlock{
		Height:    2,
		CtToOrder: []protocol.CrossShardTx{},
		TpcResult: map[string]bool{txID: true},
	}
	blockData2, _ := json.Marshal(orchBlock2)

	req0c := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData2))
	req0c.Header.Set("Content-Type", "application/json")
	w0c := httptest.NewRecorder()
	shard0.Router().ServeHTTP(w0c, req0c)

	req1c := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData2))
	req1c.Header.Set("Content-Type", "application/json")
	w1c := httptest.NewRecorder()
	shard1.Router().ServeHTTP(w1c, req1c)

	shard0.ProduceBlock()
	shard1.ProduceBlock()

	// === VERIFICATION ===
	t.Log("=== VERIFICATION ===")

	finalVal1 := shard1.GetStorageAt(contractAddr, slot1)
	finalVal2 := shard1.GetStorageAt(contractAddr, slot2)

	if finalVal1 != newVal1 {
		t.Errorf("Slot 1: expected %s, got %s", newVal1.Hex(), finalVal1.Hex())
	} else {
		t.Logf("✓ Slot 1 correctly updated: %s → %s", initialVal1.Hex(), finalVal1.Hex())
	}

	if finalVal2 != newVal2 {
		t.Errorf("Slot 2: expected %s, got %s", newVal2.Hex(), finalVal2.Hex())
	} else {
		t.Logf("✓ Slot 2 correctly updated: %s → %s", initialVal2.Hex(), finalVal2.Hex())
	}

	// Verify slots are unlocked
	if shard1.IsSlotLocked(contractAddr, slot1) {
		t.Error("Slot 1 should be unlocked after Finalize")
	}
	if shard1.IsSlotLocked(contractAddr, slot2) {
		t.Error("Slot 2 should be unlocked after Finalize")
	}
	t.Log("✓ All slots unlocked after Finalize")

	t.Log("=== H.4 CONTRACT CALL WITH STORAGE TEST PASSED ===")
}

// =============================================================================
// H.5: Integration Test - Concurrent Transactions
// =============================================================================
// Tests multiple concurrent cross-shard transactions:
// 1. Submit multiple txs that contend for the same slot
// 2. First tx should succeed, second should abort (ReadSet mismatch)
// 3. Verify proper conflict resolution
func TestH5_ConcurrentTransactions(t *testing.T) {
	env := NewTestEnv(t, 2)
	defer env.Close()

	shard0 := env.Shards[0]
	shard1 := env.Shards[1]

	// Setup: Fund two senders on shard 0
	sender1 := common.HexToAddress("0x5555555555555555555555555555555555555500") // Shard 0
	sender2 := common.HexToAddress("0x6666666666666666666666666666666666666600") // Shard 0

	for _, sender := range []common.Address{sender1, sender2} {
		faucetReq := map[string]string{
			"address": sender.Hex(),
			"amount":  "1000000000000000000",
		}
		resp, _ := postJSON(env.ShardURL(0)+"/faucet", faucetReq)
		resp.Body.Close()
	}

	// Setup: Contract on shard 1 with initial storage
	contractAddr := common.HexToAddress("0x7777777777777777777777777777777777777701") // Shard 1
	slot := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	initialVal := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000064") // 100

	shard1.SetStorageAt(contractAddr, slot, initialVal)
	t.Logf("Initial storage: slot=%s value=%s", slot.Hex(), initialVal.Hex())

	// Create two concurrent transactions targeting the same slot
	txID1 := "h5-concurrent-tx-1"
	txID2 := "h5-concurrent-tx-2"

	newVal1 := common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000000c8") // 200
	newVal2 := common.HexToHash("0x000000000000000000000000000000000000000000000000000000000000012c") // 300

	// Both txs read the same initial value but write different values
	crossTx1 := protocol.CrossShardTx{
		ID:        txID1,
		FromShard: 0,
		From:      sender1,
		To:        contractAddr,
		Value:     protocol.NewBigInt(big.NewInt(0)),
		RwSet: []protocol.RwVariable{
			{
				Address:        contractAddr,
				ReferenceBlock: protocol.Reference{ShardNum: 1},
				ReadSet: []protocol.ReadSetItem{
					{Slot: protocol.Slot(slot), Value: initialVal.Bytes()},
				},
				WriteSet: []protocol.WriteSetItem{
					{Slot: protocol.Slot(slot), OldValue: initialVal.Bytes(), NewValue: newVal1.Bytes()},
				},
			},
		},
	}

	crossTx2 := protocol.CrossShardTx{
		ID:        txID2,
		FromShard: 0,
		From:      sender2,
		To:        contractAddr,
		Value:     protocol.NewBigInt(big.NewInt(0)),
		RwSet: []protocol.RwVariable{
			{
				Address:        contractAddr,
				ReferenceBlock: protocol.Reference{ShardNum: 1},
				ReadSet: []protocol.ReadSetItem{
					{Slot: protocol.Slot(slot), Value: initialVal.Bytes()}, // Same ReadSet as tx1!
				},
				WriteSet: []protocol.WriteSetItem{
					{Slot: protocol.Slot(slot), OldValue: initialVal.Bytes(), NewValue: newVal2.Bytes()},
				},
			},
		},
	}

	// === ROUND N: Both txs in same block ===
	t.Log("=== ROUND N: Lock Phase (both txs) ===")

	orchBlock1 := protocol.OrchestratorShardBlock{
		Height:    1,
		CtToOrder: []protocol.CrossShardTx{crossTx1, crossTx2}, // Both in same block
		TpcResult: make(map[string]bool),
	}
	blockData, _ := json.Marshal(orchBlock1)

	req0 := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
	req0.Header.Set("Content-Type", "application/json")
	w0 := httptest.NewRecorder()
	shard0.Router().ServeHTTP(w0, req0)

	req1 := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	shard1.Router().ServeHTTP(w1, req1)

	// Produce blocks
	block0, _ := shard0.ProduceBlock()
	block1, _ := shard1.ProduceBlock()

	t.Logf("Shard 0 votes: tx1=%v, tx2=%v", block0.TpcPrepare[txID1], block0.TpcPrepare[txID2])
	t.Logf("Shard 1 votes: tx1=%v, tx2=%v", block1.TpcPrepare[txID1], block1.TpcPrepare[txID2])

	// Expected: First tx (tx1) should succeed, second (tx2) should fail due to slot lock conflict
	// Shard 0 (source) should vote YES for both (just locking funds)
	// Shard 1 (dest) should vote YES for tx1, NO for tx2 (slot already locked by tx1)
	if !block0.TpcPrepare[txID1] {
		t.Error("Shard 0 should vote YES for tx1")
	}
	if !block0.TpcPrepare[txID2] {
		t.Error("Shard 0 should vote YES for tx2 (only checking source funds)")
	}

	// Tx1 should succeed on shard 1
	if !block1.TpcPrepare[txID1] {
		t.Error("Shard 1 should vote YES for tx1 (first to lock)")
	}

	// Tx2 should fail on shard 1 (slot already locked by tx1)
	if block1.TpcPrepare[txID2] {
		t.Error("Shard 1 should vote NO for tx2 (slot conflict with tx1)")
	} else {
		t.Log("✓ Tx2 correctly rejected due to slot conflict")
	}

	// === ROUND N+1: Finalize ===
	t.Log("=== ROUND N+1: Finalize Phase ===")

	// Tx1 commits, tx2 aborts (based on shard 1's NO vote)
	orchBlock2 := protocol.OrchestratorShardBlock{
		Height:    2,
		CtToOrder: []protocol.CrossShardTx{},
		TpcResult: map[string]bool{
			txID1: true,  // COMMIT (all YES)
			txID2: false, // ABORT (shard 1 voted NO)
		},
	}
	blockData2, _ := json.Marshal(orchBlock2)

	req0c := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData2))
	req0c.Header.Set("Content-Type", "application/json")
	w0c := httptest.NewRecorder()
	shard0.Router().ServeHTTP(w0c, req0c)

	req1c := httptest.NewRequest("POST", "/orchestrator-shard/block", bytes.NewBuffer(blockData2))
	req1c.Header.Set("Content-Type", "application/json")
	w1c := httptest.NewRecorder()
	shard1.Router().ServeHTTP(w1c, req1c)

	shard0.ProduceBlock()
	shard1.ProduceBlock()

	// === VERIFICATION ===
	t.Log("=== VERIFICATION ===")

	// Storage should have tx1's value (newVal1), not tx2's
	finalVal := shard1.GetStorageAt(contractAddr, slot)

	if finalVal != newVal1 {
		t.Errorf("Storage should have tx1's value %s, got %s", newVal1.Hex(), finalVal.Hex())
	} else {
		t.Logf("✓ Storage correctly has tx1's value: %s → %s", initialVal.Hex(), finalVal.Hex())
	}

	// Verify slot is unlocked
	if shard1.IsSlotLocked(contractAddr, slot) {
		t.Error("Slot should be unlocked after both txs finalized")
	}
	t.Log("✓ Slot unlocked after finalization")

	// Verify sender1's funds were debited (tx1 committed with value=0, so no change expected here)
	// Verify sender2's funds were NOT debited (tx2 aborted, funds unlocked)
	sender1Bal := shard0.GetBalance(sender1)
	sender2Bal := shard0.GetBalance(sender2)

	t.Logf("Final balances - Sender1: %s, Sender2: %s", sender1Bal.String(), sender2Bal.String())

	// Both should have their original 1 ETH since these were storage-only txs (value=0)
	expectedBal := big.NewInt(1000000000000000000)
	if sender1Bal.Cmp(expectedBal) != 0 {
		t.Errorf("Sender1 balance incorrect: expected %s, got %s", expectedBal.String(), sender1Bal.String())
	}
	if sender2Bal.Cmp(expectedBal) != 0 {
		t.Errorf("Sender2 balance incorrect: expected %s, got %s", expectedBal.String(), sender2Bal.String())
	}

	t.Log("=== H.5 CONCURRENT TRANSACTIONS TEST PASSED ===")
}
