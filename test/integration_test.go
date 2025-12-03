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
	"github.com/sharding-experiment/sharding/internal/orchestrator"
	"github.com/sharding-experiment/sharding/internal/protocol"
	"github.com/sharding-experiment/sharding/internal/shard"
)

// TestEnv sets up a test environment with multiple shards and an orchestrator
type TestEnv struct {
	Orchestrator     *orchestrator.Service
	OrchestratorURL  string
	Shards           []*shard.Server
	ShardServers     []*httptest.Server
	OrchestratorSrv  *httptest.Server
}

func NewTestEnv(t *testing.T, numShards int) *TestEnv {
	env := &TestEnv{
		Shards:       make([]*shard.Server, numShards),
		ShardServers: make([]*httptest.Server, numShards),
	}

	// Create orchestrator first (we'll set URL after starting server)
	env.Orchestrator = orchestrator.NewService(numShards)
	env.OrchestratorSrv = httptest.NewServer(env.Orchestrator.Router())
	env.OrchestratorURL = env.OrchestratorSrv.URL

	// Create shards
	for i := 0; i < numShards; i++ {
		env.Shards[i] = shard.NewServerForTest(i, env.OrchestratorURL)
		env.ShardServers[i] = httptest.NewServer(env.Shards[i].Router())
	}

	return env
}

func (e *TestEnv) Close() {
	for _, srv := range e.ShardServers {
		if srv != nil {
			srv.Close()
		}
	}
	if e.OrchestratorSrv != nil {
		e.OrchestratorSrv.Close()
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
	orch := orchestrator.NewService(3)

	// Add a transaction
	tx := protocol.CrossShardTx{
		ID:        "test-tx-1",
		FromShard: 0,
		From:      common.HexToAddress("0x1234"),
		Value:     big.NewInt(1000000),
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
	srv := shard.NewServerForTest(0, "http://localhost:8080")
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
	srv := shard.NewServerForTest(0, "http://localhost:8080")
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	deployer := common.HexToAddress("0x1111111111111111111111111111111111111111")

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
	for i := 0; i < numShards; i++ {
		shardChains[i] = shard.NewChain()
	}

	// Create a cross-shard tx: shard 0 -> shard 1
	tx := protocol.CrossShardTx{
		ID:        "tx-1",
		FromShard: 0,
		From:      common.HexToAddress("0x1111"),
		Value:     big.NewInt(1000),
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
	shardChains[0].AddTx(tx.ID, true)
	shardChains[0].LockFunds(tx.ID, tx.From, tx.Value)
	shardChains[0].AddPrepareResult(tx.ID, true) // Vote YES

	// Step 4: Dest shard (1) processes block - stores pending credit
	shardChains[1].StorePendingCredit(tx.ID, common.HexToAddress("0x2222"), tx.Value)

	// Step 5: Source shard produces block with vote
	stateBlock0 := shardChains[0].ProduceBlock(common.Hash{})
	if len(stateBlock0.TpcPrepare) != 1 {
		t.Fatalf("Expected 1 prepare vote, got %d", len(stateBlock0.TpcPrepare))
	}
	if !stateBlock0.TpcPrepare[tx.ID] {
		t.Error("Expected prepare vote to be true")
	}

	// Step 6: Orchestrator records vote
	if !orchChain.RecordVote(tx.ID, true) {
		t.Error("Vote should be recorded")
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
	credit, ok := shardChains[1].GetPendingCredit(tx.ID)
	if !ok {
		t.Error("Pending credit should exist before commit processing")
	}
	if credit.Amount.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Expected credit amount 1000, got %s", credit.Amount.String())
	}
	shardChains[1].ClearPendingCredit(tx.ID)

	// Verify final state
	_, ok = shardChains[0].GetLockedFunds(tx.ID)
	if ok {
		t.Error("Lock should be cleared after commit")
	}
	_, ok = shardChains[1].GetPendingCredit(tx.ID)
	if ok {
		t.Error("Pending credit should be cleared after commit")
	}

	t.Log("Cross-shard transaction simulation completed successfully")
}

func TestCrossShardTx_Abort(t *testing.T) {
	// Test the abort flow
	orchChain := orchestrator.NewOrchestratorChain()
	sourceChain := shard.NewChain()
	destChain := shard.NewChain()

	tx := protocol.CrossShardTx{
		ID:        "tx-abort-1",
		FromShard: 0,
		From:      common.HexToAddress("0x1111"),
		Value:     big.NewInt(1000),
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
	sourceChain.LockFunds(tx.ID, tx.From, tx.Value)
	sourceChain.AddPrepareResult(tx.ID, false) // Vote NO

	// Dest shard stores pending credit
	destChain.StorePendingCredit(tx.ID, common.HexToAddress("0x2222"), tx.Value)

	// Source produces block with NO vote
	stateBlock := sourceChain.ProduceBlock(common.Hash{})
	if stateBlock.TpcPrepare[tx.ID] {
		t.Error("Expected prepare vote to be false (abort)")
	}

	// Orchestrator records abort vote
	orchChain.RecordVote(tx.ID, false)

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
			Value:     big.NewInt(int64(1000 * (i + 1))),
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
	orchChain.RecordVote("tx-0", true)
	orchChain.RecordVote("tx-1", false)
	orchChain.RecordVote("tx-2", true)
	orchChain.RecordVote("tx-3", true)
	orchChain.RecordVote("tx-4", false)

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
			Value:     big.NewInt(1000),
		}
		chain.AddTransaction(tx)
		chain.ProduceBlock()
	}
}

func BenchmarkShardChain_LockAndClear(b *testing.B) {
	chain := shard.NewChain()
	addr := common.HexToAddress("0x1234")
	amount := big.NewInt(1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		txID := fmt.Sprintf("tx-%d", i)
		chain.LockFunds(txID, addr, amount)
		chain.ClearLock(txID)
	}
}
