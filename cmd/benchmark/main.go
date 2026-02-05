package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sharding-experiment/sharding/config"
)

// TxSubmitRequest matches the shard's expected format
type TxSubmitRequest struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Value string `json:"value"`
	Data  string `json:"data"`
	Gas   uint64 `json:"gas"`
}

// TxSubmitResponse from the shard
type TxSubmitResponse struct {
	Success    bool   `json:"success"`
	TxID       string `json:"tx_id"`
	Status     string `json:"status"`
	CrossShard bool   `json:"cross_shard"`
	Error      string `json:"error"`
}

// CrossShardSubmitRequest for orchestrator
type CrossShardSubmitRequest struct {
	FromShard int          `json:"from_shard"`
	From      string       `json:"from"`
	To        string       `json:"to"`
	RwSet     []RwSetEntry `json:"rw_set"`
	Value     string       `json:"value"`
	Gas       uint64       `json:"gas"`
}

type RwSetEntry struct {
	Address        string         `json:"address"`
	ReferenceBlock ReferenceBlock `json:"reference_block"`
}

type ReferenceBlock struct {
	ShardNum int `json:"shard_num"`
}

// CrossShardResponse from orchestrator
type CrossShardResponse struct {
	TxID   string `json:"tx_id"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

// TxStatusResponse from orchestrator status endpoint
type TxStatusResponse struct {
	TxID   string `json:"tx_id"`
	Status string `json:"status"`
}

// TxRecord tracks a submitted transaction
type TxRecord struct {
	TxID       string
	TxType     string // "local" or "cross"
	SubmitTime time.Time
	EndTime    time.Time
	Status     string // "committed", "aborted", "pending", "error"
}

// BenchmarkStats holds aggregated results
type BenchmarkStats struct {
	TotalSubmitted int64
	TotalCommitted int64
	TotalAborted   int64
	TotalPending   int64
	TotalErrors    int64

	LocalCommitted int64
	CrossCommitted int64

	SubmitLatencies []float64 // HTTP submission latency in ms
	mu              sync.Mutex

	// Track sample of cross-shard tx IDs for status checking
	CrossTxIDs       []string
	CrossSubmitTimes map[string]time.Time // Track submit time for E2E latency
	crossTxIDsMu     sync.Mutex
	maxCrossTxIDs    int
}

func (s *BenchmarkStats) AddSubmitLatency(ms float64) {
	s.mu.Lock()
	s.SubmitLatencies = append(s.SubmitLatencies, ms)
	s.mu.Unlock()
}

func (s *BenchmarkStats) AddCrossTxID(txID string, submitTime time.Time) {
	s.crossTxIDsMu.Lock()
	if len(s.CrossTxIDs) < s.maxCrossTxIDs {
		s.CrossTxIDs = append(s.CrossTxIDs, txID)
		s.CrossSubmitTimes[txID] = submitTime
	}
	s.crossTxIDsMu.Unlock()
}

func (s *BenchmarkStats) SubmitPercentile(p float64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.SubmitLatencies) == 0 {
		return 0
	}

	sorted := make([]float64, len(s.SubmitLatencies))
	copy(sorted, s.SubmitLatencies)
	sort.Float64s(sorted)

	idx := int(float64(len(sorted)-1) * p / 100.0)
	return sorted[idx]
}

// Config for the benchmark
type BenchmarkConfig struct {
	NumShards       int
	Duration        time.Duration
	Cooldown        time.Duration
	InjectionRate   int // tx/s
	CTRatio         float64
	NumWorkers      int
	OrchestratorURL string
	BaseShardPort   int
	BlockTimeMs     int  // For latency context
	RateLimit       bool // Strictly enforce injection rate
}

// AccountStore holds pre-funded accounts grouped by shard
type AccountStore struct {
	ByShard map[int][]string
}

// LoadAccounts reads accounts from storage/address.txt
func LoadAccounts(path string, numShards int) (*AccountStore, error) {
	store := &AccountStore{
		ByShard: make(map[int][]string),
	}
	for i := 0; i < numShards; i++ {
		store.ByShard[i] = make([]string, 0)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		addr := strings.TrimSpace(scanner.Text())
		if addr == "" {
			continue
		}
		shard := addressToShard(addr, numShards)
		store.ByShard[shard] = append(store.ByShard[shard], addr)
	}

	return store, scanner.Err()
}

// RandomFromShard returns a random account from the given shard
func (s *AccountStore) RandomFromShard(shard int) string {
	accounts := s.ByShard[shard]
	if len(accounts) == 0 {
		return ""
	}
	return accounts[rand.Intn(len(accounts))]
}

// Get shard from address (first hex digit)
func addressToShard(addr string, numShards int) int {
	if len(addr) < 3 {
		return 0
	}
	hex := addr[2:3]
	var shard int
	fmt.Sscanf(hex, "%x", &shard)
	return shard % numShards
}

func main() {
	// Parse flags
	duration := flag.Int("duration", 4, "Benchmark duration in seconds")
	cooldown := flag.Int("cooldown", 1, "Cooldown period in seconds")
	injectionRate := flag.Int("injection-rate", 10000, "Target transactions per second")
	ctRatio := flag.Float64("ct-ratio", 0.5, "Cross-shard transaction ratio (0.0-1.0)")
	numWorkers := flag.Int("workers", 1000, "Number of concurrent workers")
	rateLimit := flag.Bool("rate-limit", false, "Strictly enforce injection rate (for latency testing)")
	flag.Parse()

	// Load config
	cfg, err := config.LoadDefault()
	if err != nil {
		log.Printf("Warning: Could not load config, using defaults: %v", err)
		cfg = &config.Config{ShardNum: 8}
	}

	blockTimeMs := 200 // default
	if cfg.BlockTimeMs > 0 {
		blockTimeMs = cfg.BlockTimeMs
	}

	benchCfg := BenchmarkConfig{
		NumShards:       cfg.ShardNum,
		Duration:        time.Duration(*duration) * time.Second,
		Cooldown:        time.Duration(*cooldown) * time.Second,
		InjectionRate:   *injectionRate,
		CTRatio:         *ctRatio,
		NumWorkers:      *numWorkers,
		OrchestratorURL: "http://localhost:8080",
		BaseShardPort:   8545,
		BlockTimeMs:     blockTimeMs,
		RateLimit:       *rateLimit,
	}

	// Create HTTP client with connection pooling
	transport := &http.Transport{
		MaxIdleConns:        benchCfg.NumWorkers * 2,
		MaxIdleConnsPerHost: benchCfg.NumWorkers,
		MaxConnsPerHost:     benchCfg.NumWorkers,
		IdleConnTimeout:     90 * time.Second,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	// Check health
	fmt.Println("Checking network health...")
	if !checkHealth(client, benchCfg) {
		log.Fatal("Network health check failed")
	}

	// Load accounts
	fmt.Println("Loading accounts...")
	accounts, err := LoadAccounts("storage/address.txt", benchCfg.NumShards)
	if err != nil {
		log.Fatalf("Failed to load accounts: %v", err)
	}
	totalAccounts := 0
	for shard, accs := range accounts.ByShard {
		fmt.Printf("  Shard %d: %d accounts\n", shard, len(accs))
		totalAccounts += len(accs)
	}
	fmt.Printf("  Total: %d accounts\n", totalAccounts)

	// Run benchmark
	fmt.Printf("\n%s\n", "============================================================")
	fmt.Println("Starting Go Benchmark")
	fmt.Printf("%s\n", "============================================================")
	fmt.Printf("  CT Ratio: %.2f\n", benchCfg.CTRatio)
	fmt.Printf("  Target Injection Rate: %d tx/s\n", benchCfg.InjectionRate)
	fmt.Printf("  Duration: %v\n", benchCfg.Duration)
	fmt.Printf("  Cooldown: %v\n", benchCfg.Cooldown)
	fmt.Printf("  Workers: %d\n", benchCfg.NumWorkers)
	fmt.Println()

	stats := &BenchmarkStats{
		SubmitLatencies:  make([]float64, 0, benchCfg.InjectionRate*int(benchCfg.Duration.Seconds())),
		CrossTxIDs:       make([]string, 0, 500),
		CrossSubmitTimes: make(map[string]time.Time, 500),
		maxCrossTxIDs:    500, // Sample size for status checking
	}

	// Channel for transaction jobs - large buffer to avoid blocking
	jobs := make(chan struct{}, benchCfg.NumWorkers*10)

	// Counters
	var submitted int64
	var localOK int64
	var crossPending int64

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < benchCfg.NumWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				submitTx(client, benchCfg, accounts, stats, &localOK, &crossPending)
				atomic.AddInt64(&submitted, 1)
			}
		}()
	}

	// Injection loop
	mode := "flood"
	if benchCfg.RateLimit {
		mode = "rate-limited"
	}
	fmt.Printf("Phase: Injecting transactions (%s mode)...\n", mode)
	startTime := time.Now()
	endTime := startTime.Add(benchCfg.Duration)

	lastProgress := startTime

	if benchCfg.RateLimit {
		// Rate-limited mode: send batches at fixed intervals
		batchInterval := 100 * time.Millisecond
		batchSize := benchCfg.InjectionRate / 10 // 10 batches per second
		if batchSize < 1 {
			batchSize = 1
		}

		ticker := time.NewTicker(batchInterval)
		defer ticker.Stop()

		for time.Now().Before(endTime) {
			<-ticker.C

			// Send exactly batchSize jobs
			for i := 0; i < batchSize; i++ {
				select {
				case jobs <- struct{}{}:
				default:
					// Channel full, skip
				}
			}

			// Progress update
			now := time.Now()
			if now.Sub(lastProgress) >= 2*time.Second {
				elapsed := now.Sub(startTime).Seconds()
				sub := atomic.LoadInt64(&submitted)
				rate := float64(sub) / elapsed
				lok := atomic.LoadInt64(&localOK)
				fmt.Printf("  [%.0fs] Submitted: %d, Rate: %.0f tx/s, Local OK: %d\n",
					elapsed, sub, rate, lok)
				lastProgress = now
			}
		}
	} else {
		// Flood mode: push jobs as fast as workers can handle
		for time.Now().Before(endTime) {
			select {
			case jobs <- struct{}{}:
				// Job queued
			default:
				// Channel full - check progress and yield
				now := time.Now()
				if now.Sub(lastProgress) >= 2*time.Second {
					elapsed := now.Sub(startTime).Seconds()
					sub := atomic.LoadInt64(&submitted)
					rate := float64(sub) / elapsed
					lok := atomic.LoadInt64(&localOK)
					fmt.Printf("  [%.0fs] Submitted: %d, Rate: %.0f tx/s, Local OK: %d\n",
						elapsed, sub, rate, lok)
					lastProgress = now
				}
				// Brief yield to let workers catch up
				time.Sleep(10 * time.Microsecond)
			}
		}
	}

	injectionEnd := time.Now()
	actualDuration := injectionEnd.Sub(startTime).Seconds()

	// Close jobs channel and wait for workers
	close(jobs)

	fmt.Printf("\nPhase: Cooldown (submitted %d txs, waiting for completion...)\n",
		atomic.LoadInt64(&submitted))

	// Wait for workers to finish current work
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(benchCfg.Cooldown):
	}

	// Brief additional cooldown for cross-shard completion
	time.Sleep(benchCfg.Cooldown)

	// Check cross-shard transaction status (sample) - poll until complete or timeout
	fmt.Println("Checking cross-shard transaction status (polling)...")
	pollTimeout := benchCfg.Cooldown
	if pollTimeout < 5*time.Second {
		pollTimeout = 5 * time.Second // Minimum 5s for polling
	}
	crossCommittedSample, crossAbortedSample, crossPendingSample, e2eLatencies := checkCrossShardStatus(client, benchCfg, stats, pollTimeout)
	sampleSize := crossCommittedSample + crossAbortedSample + crossPendingSample

	// Calculate final stats
	totalSubmitted := atomic.LoadInt64(&submitted)
	localCommitted := atomic.LoadInt64(&localOK)
	crossSubmitted := atomic.LoadInt64(&crossPending)

	// Estimate cross-shard commit rate from sample
	var crossCommitRate float64
	if sampleSize > 0 {
		crossCommitRate = float64(crossCommittedSample) / float64(sampleSize)
	}

	// Estimate total cross-shard committed
	crossCommittedEst := int64(float64(crossSubmitted) * crossCommitRate)
	totalCommitted := localCommitted + crossCommittedEst

	// Calculate E2E latency percentiles for cross-shard
	var e2eP50, e2eP95, e2eP99 float64
	if len(e2eLatencies) > 0 {
		sort.Float64s(e2eLatencies)
		e2eP50 = e2eLatencies[int(float64(len(e2eLatencies)-1)*0.50)]
		e2eP95 = e2eLatencies[int(float64(len(e2eLatencies)-1)*0.95)]
		e2eP99 = e2eLatencies[int(float64(len(e2eLatencies)-1)*0.99)]
	}

	fmt.Printf("\n%s\n", "============================================================")
	fmt.Println("Benchmark Results")
	fmt.Printf("%s\n", "============================================================")
	fmt.Printf("  Total Submitted: %d\n", totalSubmitted)
	fmt.Printf("  Local Submitted: %d\n", totalSubmitted-crossSubmitted)
	fmt.Printf("  Cross Submitted: %d\n", crossSubmitted)
	fmt.Println()
	fmt.Printf("  Local Committed: %d (%.2f tps)\n", localCommitted, float64(localCommitted)/actualDuration)
	fmt.Printf("  Cross Committed (sample %d/%d = %.1f%%): ~%d (%.2f tps)\n",
		crossCommittedSample, sampleSize, crossCommitRate*100,
		crossCommittedEst, float64(crossCommittedEst)/actualDuration)
	fmt.Printf("  Total Committed: ~%d\n", totalCommitted)
	fmt.Printf("  Total Errors: %d\n", stats.TotalErrors)
	fmt.Println()
	fmt.Printf("  Achieved Injection Rate: %.1f tx/s\n", float64(totalSubmitted)/actualDuration)
	fmt.Printf("  Actual TPS (committed): %.2f\n", float64(totalCommitted)/actualDuration)
	if totalSubmitted > 0 {
		fmt.Printf("  Commit Rate: %.2f%%\n", float64(totalCommitted)/float64(totalSubmitted)*100)
	}
	fmt.Println()
	fmt.Println("  Latency (HTTP submission round-trip):")
	fmt.Printf("    P50: %.1f ms, P95: %.1f ms, P99: %.1f ms\n",
		stats.SubmitPercentile(50), stats.SubmitPercentile(95), stats.SubmitPercentile(99))
	if len(e2eLatencies) > 0 {
		fmt.Println("  Latency (Cross-shard E2E, submit to confirm):")
		fmt.Printf("    P50: %.1f ms, P95: %.1f ms, P99: %.1f ms\n", e2eP50, e2eP95, e2eP99)
	}
	fmt.Printf("\n  Note: Local confirmation adds up to 1 block cycle (~%dms)\n", benchCfg.BlockTimeMs)
}

func checkHealth(client *http.Client, cfg BenchmarkConfig) bool {
	// Check orchestrator
	resp, err := client.Get(cfg.OrchestratorURL + "/health")
	if err != nil {
		fmt.Printf("  Orchestrator: UNREACHABLE - %v\n", err)
		return false
	}
	resp.Body.Close()
	fmt.Println("  Orchestrator: OK")

	// Check shards
	for i := 0; i < cfg.NumShards; i++ {
		url := fmt.Sprintf("http://localhost:%d/health", cfg.BaseShardPort+i)
		resp, err := client.Get(url)
		if err != nil {
			fmt.Printf("  Shard %d: UNREACHABLE - %v\n", i, err)
			return false
		}
		resp.Body.Close()
		fmt.Printf("  Shard %d: OK\n", i)
	}
	return true
}

func submitTx(client *http.Client, cfg BenchmarkConfig, accounts *AccountStore, stats *BenchmarkStats, localOK, crossPending *int64) {
	startTime := time.Now()

	// Decide local vs cross-shard
	isCrossShard := rand.Float64() < cfg.CTRatio

	fromShard := rand.Intn(cfg.NumShards)
	fromAddr := accounts.RandomFromShard(fromShard)
	if fromAddr == "" {
		atomic.AddInt64(&stats.TotalErrors, 1)
		return
	}

	var toShard int
	if isCrossShard {
		// Pick a different shard
		toShard = (fromShard + 1 + rand.Intn(cfg.NumShards-1)) % cfg.NumShards
	} else {
		toShard = fromShard
	}
	toAddr := accounts.RandomFromShard(toShard)
	if toAddr == "" {
		atomic.AddInt64(&stats.TotalErrors, 1)
		return
	}

	if isCrossShard {
		// Submit to orchestrator
		submitCrossShard(client, cfg, fromShard, fromAddr, toAddr, toShard, stats, crossPending, startTime)
	} else {
		// Submit to local shard
		submitLocal(client, cfg, fromShard, fromAddr, toAddr, stats, localOK, startTime)
	}
}

func submitLocal(client *http.Client, cfg BenchmarkConfig, shard int, from, to string, stats *BenchmarkStats, localOK *int64, startTime time.Time) {
	url := fmt.Sprintf("http://localhost:%d/tx/submit", cfg.BaseShardPort+shard)

	req := TxSubmitRequest{
		From:  from,
		To:    to,
		Value: "1",
		Data:  "0x",
		Gas:   21000,
	}

	body, _ := json.Marshal(req)
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		atomic.AddInt64(&stats.TotalErrors, 1)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result TxSubmitResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		atomic.AddInt64(&stats.TotalErrors, 1)
		return
	}

	if result.Success || result.Status == "queued" {
		atomic.AddInt64(localOK, 1)
		latency := time.Since(startTime).Seconds() * 1000
		stats.AddSubmitLatency(latency)
	} else {
		atomic.AddInt64(&stats.TotalAborted, 1)
	}
}

func submitCrossShard(client *http.Client, cfg BenchmarkConfig, fromShard int, from, to string, toShard int, stats *BenchmarkStats, crossPending *int64, startTime time.Time) {
	url := fmt.Sprintf("http://localhost:%d/tx/submit", cfg.BaseShardPort+fromShard)

	req := CrossShardSubmitRequest{
		FromShard: fromShard,
		From:      from,
		To:        to,
		Value:     "1",
		Gas:       21000,
	}

	body, _ := json.Marshal(req)
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		atomic.AddInt64(&stats.TotalErrors, 1)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result CrossShardResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		atomic.AddInt64(&stats.TotalErrors, 1)
		return
	}

	if result.TxID != "" {
		atomic.AddInt64(crossPending, 1)
		latency := time.Since(startTime).Seconds() * 1000
		stats.AddSubmitLatency(latency)
		stats.AddCrossTxID(result.TxID, startTime)
	} else if result.Error != "" {
		atomic.AddInt64(&stats.TotalErrors, 1)
	}
}

// checkCrossShardStatus polls cross-shard transactions in parallel until they complete or timeout
// Returns counts and end-to-end latencies for committed transactions
func checkCrossShardStatus(client *http.Client, cfg BenchmarkConfig, stats *BenchmarkStats, pollTimeout time.Duration) (committed, aborted, pending int, e2eLatencies []float64) {
	stats.crossTxIDsMu.Lock()
	txIDs := make([]string, len(stats.CrossTxIDs))
	copy(txIDs, stats.CrossTxIDs)
	submitTimes := make(map[string]time.Time, len(stats.CrossSubmitTimes))
	for k, v := range stats.CrossSubmitTimes {
		submitTimes[k] = v
	}
	stats.crossTxIDsMu.Unlock()

	if len(txIDs) == 0 {
		return 0, 0, 0, nil
	}

	e2eLatencies = make([]float64, 0, len(txIDs))

	// Track completion status and times with mutex
	var mu sync.Mutex
	completedTxs := make(map[string]bool)
	commitTimes := make(map[string]time.Time)

	pollStart := time.Now()
	pollEnd := pollStart.Add(pollTimeout)

	// Poll in parallel until all complete or timeout
	for time.Now().Before(pollEnd) && len(completedTxs) < len(txIDs) {
		var wg sync.WaitGroup

		for _, txID := range txIDs {
			mu.Lock()
			alreadyDone := completedTxs[txID]
			mu.Unlock()
			if alreadyDone {
				continue
			}

			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				checkTime := time.Now()

				url := fmt.Sprintf("%s/cross-shard/status/%s", cfg.OrchestratorURL, id)
				resp, err := client.Get(url)
				if err != nil {
					return
				}

				respBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()

				var status TxStatusResponse
				if err := json.Unmarshal(respBody, &status); err != nil {
					return
				}

				if status.Status == "committed" || status.Status == "aborted" {
					mu.Lock()
					completedTxs[id] = true
					if status.Status == "committed" {
						commitTimes[id] = checkTime
					}
					mu.Unlock()
				}
			}(txID)
		}

		wg.Wait()

		// Brief pause between poll rounds
		mu.Lock()
		remaining := len(txIDs) - len(completedTxs)
		mu.Unlock()
		if remaining > 0 {
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Calculate final counts and latencies
	for _, txID := range txIDs {
		if commitTime, ok := commitTimes[txID]; ok {
			committed++
			if submitTime, ok := submitTimes[txID]; ok {
				e2eMs := commitTime.Sub(submitTime).Seconds() * 1000
				e2eLatencies = append(e2eLatencies, e2eMs)
			}
		} else if completedTxs[txID] {
			aborted++
		} else {
			pending++
		}
	}

	return
}
