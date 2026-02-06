package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
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
	FromShard int             `json:"from_shard"`
	From      string          `json:"from"`
	To        string          `json:"to"`
	RwSet     []RwSetEntry    `json:"rw_set"`
	Value     string          `json:"value"`
	Gas       uint64          `json:"gas"`
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
	ContractRatio   float64 // Ratio of contract calls vs simple transfers
	SkewnessTheta   float64 // Zipfian distribution parameter (0.0=uniform, 0.9=highly skewed)
	InvolvedShards  int     // Number of shards per cross-shard TX (3-8)
	NumWorkers      int
	OrchestratorURL string
	BaseShardPort   int
	BlockTimeMs     int  // For latency context
	RateLimit       bool // Strictly enforce injection rate
}

// ZipfianGenerator implements Zipfian distribution for skewed account selection
type ZipfianGenerator struct {
	n     int       // number of items
	theta float64   // skewness parameter
	cdf   []float64 // precomputed CDF
}

// NewZipfianGenerator creates a new Zipfian generator
// theta=0.0 gives uniform distribution, theta=0.9 gives highly skewed
func NewZipfianGenerator(n int, theta float64) *ZipfianGenerator {
	if theta < 0 {
		theta = 0
	}
	if theta >= 1 {
		theta = 0.99
	}

	z := &ZipfianGenerator{
		n:     n,
		theta: theta,
		cdf:   make([]float64, n),
	}

	// Compute Zipfian probabilities: P(i) = 1/i^theta / sum(1/j^theta)
	var sum float64
	for i := 1; i <= n; i++ {
		sum += 1.0 / math.Pow(float64(i), theta)
	}

	// Build CDF
	var cumulative float64
	for i := 0; i < n; i++ {
		prob := (1.0 / math.Pow(float64(i+1), theta)) / sum
		cumulative += prob
		z.cdf[i] = cumulative
	}

	return z
}

// Next returns the next item index following Zipfian distribution
func (z *ZipfianGenerator) Next() int {
	if z.theta == 0 || z.n == 0 {
		return rand.Intn(z.n)
	}

	r := rand.Float64()
	// Binary search for the index
	lo, hi := 0, z.n-1
	for lo < hi {
		mid := (lo + hi) / 2
		if z.cdf[mid] < r {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Function selectors for contract calls
const (
	// BookTrainAndHotel(uint256,uint256) selector - TravelAgency cross-shard call
	BookTrainAndHotelSelector = "0x5710ddcd"
	// bookTrain(address) selector - local TrainBooking contract
	BookTrainSelector = "0x87a362a4"
	// bookHotel(address) selector - local HotelBooking contract
	BookHotelSelector = "0x165fcb2d"
	// book(address) selector - Plane/Taxi booking contracts
	BookGenericSelector = "0x7ca81460"
)

// Contract with its type for correct selector lookup
type ContractEntry struct {
	Address  string
	Selector string // Which booking selector to use
}

// AccountStore holds pre-funded accounts grouped by shard
type AccountStore struct {
	ByShard   map[int][]string
	zipfGen   map[int]*ZipfianGenerator // Per-shard Zipfian generators
	skewness  float64
}

// ContractStore holds contract addresses grouped by shard and type (local/cross)
type ContractStore struct {
	// Travel contracts that make cross-shard calls to train+hotel
	TravelByShard map[int][]string
	// Local-only contracts with their selectors
	LocalByShard map[int][]ContractEntry
	// Booking contracts by type (for involved shards configuration)
	// Keys: "train", "hotel", "plane", "taxi", "yacht", "movie", "restaurant"
	BookingByShard map[string]map[int][]string
}

// LoadAccounts reads accounts from storage/address.txt
func LoadAccounts(path string, numShards int, skewness float64) (*AccountStore, error) {
	store := &AccountStore{
		ByShard:  make(map[int][]string),
		zipfGen:  make(map[int]*ZipfianGenerator),
		skewness: skewness,
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

	// Initialize Zipfian generators for each shard after loading
	for shard, accounts := range store.ByShard {
		if len(accounts) > 0 {
			store.zipfGen[shard] = NewZipfianGenerator(len(accounts), skewness)
		}
	}

	return store, scanner.Err()
}

// RandomFromShard returns an account from the given shard using Zipfian distribution
func (s *AccountStore) RandomFromShard(shard int) string {
	accounts := s.ByShard[shard]
	if len(accounts) == 0 {
		return ""
	}
	// Use Zipfian generator if available (skewness > 0)
	if gen, ok := s.zipfGen[shard]; ok && s.skewness > 0 {
		return accounts[gen.Next()]
	}
	return accounts[rand.Intn(len(accounts))]
}

// LoadContracts reads contract addresses from storage files
func LoadContracts(storageDir string, numShards int) (*ContractStore, error) {
	store := &ContractStore{
		TravelByShard:  make(map[int][]string),
		LocalByShard:   make(map[int][]ContractEntry),
		BookingByShard: make(map[string]map[int][]string),
	}
	for i := 0; i < numShards; i++ {
		store.TravelByShard[i] = make([]string, 0)
		store.LocalByShard[i] = make([]ContractEntry, 0)
	}

	// Load travel contracts (these make cross-shard calls)
	travelPath := storageDir + "/travelAddress.txt"
	if err := loadContractFile(travelPath, store.TravelByShard, numShards); err != nil {
		// Not fatal - travel contracts may not exist
		log.Printf("Warning: Could not load travel contracts: %v", err)
	}

	// All booking contract types for involved shards configuration
	// Order matters: first 3 are required, rest are optional based on involved_shards
	bookingTypes := []string{"train", "hotel", "plane", "taxi", "yacht", "movie", "restaurant"}

	// Load local contracts with their specific selectors
	localTypeSelectors := map[string]string{
		"train": BookTrainSelector,
		"hotel": BookHotelSelector,
		"plane": BookGenericSelector,
		"taxi":  BookGenericSelector,
	}
	for ctype, selector := range localTypeSelectors {
		path := storageDir + "/" + ctype + "Address.txt"
		if err := loadContractFileWithSelector(path, store.LocalByShard, numShards, selector); err != nil {
			// Not fatal
			continue
		}
	}

	// Load all booking contracts by type (for cross-shard involved shards)
	for _, btype := range bookingTypes {
		store.BookingByShard[btype] = make(map[int][]string)
		for i := 0; i < numShards; i++ {
			store.BookingByShard[btype][i] = make([]string, 0)
		}
		path := storageDir + "/" + btype + "Address.txt"
		if err := loadContractFile(path, store.BookingByShard[btype], numShards); err != nil {
			// Not fatal - optional contracts may not exist
			continue
		}
	}

	return store, nil
}

func loadContractFile(path string, byShard map[int][]string, numShards int) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		addr := strings.TrimSpace(scanner.Text())
		if addr == "" {
			continue
		}
		shard := addressToShard(addr, numShards)
		byShard[shard] = append(byShard[shard], addr)
	}
	return scanner.Err()
}

func loadContractFileWithSelector(path string, byShard map[int][]ContractEntry, numShards int, selector string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		addr := strings.TrimSpace(scanner.Text())
		if addr == "" {
			continue
		}
		shard := addressToShard(addr, numShards)
		byShard[shard] = append(byShard[shard], ContractEntry{
			Address:  addr,
			Selector: selector,
		})
	}
	return scanner.Err()
}

// RandomTravelContract returns a random travel contract from any shard
func (s *ContractStore) RandomTravelContract(numShards int) (addr string, shard int) {
	// Collect all travel contracts
	var all []struct {
		addr  string
		shard int
	}
	for sh := 0; sh < numShards; sh++ {
		for _, a := range s.TravelByShard[sh] {
			all = append(all, struct {
				addr  string
				shard int
			}{a, sh})
		}
	}
	if len(all) == 0 {
		return "", -1
	}
	choice := all[rand.Intn(len(all))]
	return choice.addr, choice.shard
}

// RandomLocalContract returns a random local contract from the given shard with its selector
func (s *ContractStore) RandomLocalContract(shard int) (addr string, selector string) {
	contracts := s.LocalByShard[shard]
	if len(contracts) == 0 {
		return "", ""
	}
	entry := contracts[rand.Intn(len(contracts))]
	return entry.Address, entry.Selector
}

// GetBookingContractsForInvolvedShards returns booking contracts spanning exactly involvedShards distinct shards
// Returns: travelAddr, travelShard, list of (addr, shard) pairs for additional booking contracts
// involved_shards mapping:
//   3 = TravelAgency + Train + Hotel (base)
//   4 = + Plane
//   5 = + Taxi
//   6 = + Yacht
//   7 = + Movie
//   8 = + Restaurant
func (s *ContractStore) GetBookingContractsForInvolvedShards(numShards, involvedShards int) (travelAddr string, travelShard int, bookings []struct {
	addr  string
	shard int
	btype string
}) {
	if involvedShards > numShards {
		involvedShards = numShards
	}

	// Step 1: Pre-select involvedShards distinct random shards
	allShards := make([]int, numShards)
	for i := 0; i < numShards; i++ {
		allShards[i] = i
	}
	rand.Shuffle(len(allShards), func(i, j int) {
		allShards[i], allShards[j] = allShards[j], allShards[i]
	})
	selectedShards := allShards[:involvedShards]

	// Step 2: Get TravelAgency from first selected shard (or fallback to any with contracts)
	travelShard = selectedShards[0]
	if len(s.TravelByShard[travelShard]) > 0 {
		travelAddr = s.TravelByShard[travelShard][rand.Intn(len(s.TravelByShard[travelShard]))]
	} else {
		// Fallback: find any shard in selection with travel contracts
		for _, sh := range selectedShards {
			if len(s.TravelByShard[sh]) > 0 {
				travelShard = sh
				travelAddr = s.TravelByShard[sh][rand.Intn(len(s.TravelByShard[sh]))]
				break
			}
		}
	}
	if travelAddr == "" {
		return "", -1, nil
	}

	// Step 3: For additional shards beyond base 3, add optional contracts on DISTINCT shards
	// Train and Hotel are called internally by TravelAgency, so we add plane/taxi/etc on remaining shards
	optionalTypes := []string{"plane", "taxi", "yacht", "movie", "restaurant"}
	optionalIdx := 0

	// Iterate over remaining selected shards (skip index 0 which has TravelAgency)
	for i := 1; i < len(selectedShards) && optionalIdx < len(optionalTypes); i++ {
		targetShard := selectedShards[i]
		btype := optionalTypes[optionalIdx]

		if contracts, ok := s.BookingByShard[btype]; ok && len(contracts[targetShard]) > 0 {
			addr := contracts[targetShard][rand.Intn(len(contracts[targetShard]))]
			bookings = append(bookings, struct {
				addr  string
				shard int
				btype string
			}{addr, targetShard, btype})
		}
		optionalIdx++
	}

	return travelAddr, travelShard, bookings
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
	contractRatio := flag.Float64("contract-ratio", 0.0, "Contract call ratio (0.0-1.0, 0=transfers only)")
	skewness := flag.Float64("skewness", 0.0, "Zipfian skewness theta (0.0=uniform, 0.9=highly skewed)")
	involvedShards := flag.Int("involved-shards", 3, "Number of shards per cross-shard contract TX (3-8)")
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

	// Validate involved shards
	if *involvedShards < 3 || *involvedShards > 8 {
		log.Fatalf("involved-shards must be in range [3, 8], got %d", *involvedShards)
	}
	if *involvedShards > cfg.ShardNum {
		log.Fatalf("involved-shards (%d) must be <= shard_num (%d)", *involvedShards, cfg.ShardNum)
	}

	benchCfg := BenchmarkConfig{
		NumShards:       cfg.ShardNum,
		Duration:        time.Duration(*duration) * time.Second,
		Cooldown:        time.Duration(*cooldown) * time.Second,
		InjectionRate:   *injectionRate,
		CTRatio:         *ctRatio,
		ContractRatio:   *contractRatio,
		SkewnessTheta:   *skewness,
		InvolvedShards:  *involvedShards,
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
	accounts, err := LoadAccounts("storage/address.txt", benchCfg.NumShards, benchCfg.SkewnessTheta)
	if err != nil {
		log.Fatalf("Failed to load accounts: %v", err)
	}
	totalAccounts := 0
	for shard, accs := range accounts.ByShard {
		fmt.Printf("  Shard %d: %d accounts\n", shard, len(accs))
		totalAccounts += len(accs)
	}
	fmt.Printf("  Total: %d accounts\n", totalAccounts)

	// Load contracts if contract ratio > 0
	var contracts *ContractStore
	if benchCfg.ContractRatio > 0 {
		fmt.Println("Loading contracts...")
		contracts, err = LoadContracts("storage", benchCfg.NumShards)
		if err != nil {
			log.Printf("Warning: Failed to load contracts: %v", err)
		} else {
			totalTravel := 0
			totalLocal := 0
			for shard := 0; shard < benchCfg.NumShards; shard++ {
				totalTravel += len(contracts.TravelByShard[shard])
				totalLocal += len(contracts.LocalByShard[shard])
			}
			fmt.Printf("  Travel contracts (cross-shard): %d\n", totalTravel)
			fmt.Printf("  Local contracts: %d\n", totalLocal)
			if totalTravel == 0 && totalLocal == 0 {
				fmt.Println("  Warning: No contracts loaded, falling back to transfers only")
				benchCfg.ContractRatio = 0
			}
		}
	}

	// Run benchmark
	fmt.Printf("\n%s\n", "============================================================")
	fmt.Println("Starting Go Benchmark")
	fmt.Printf("%s\n", "============================================================")
	fmt.Printf("  CT Ratio: %.2f\n", benchCfg.CTRatio)
	fmt.Printf("  Contract Ratio: %.2f\n", benchCfg.ContractRatio)
	fmt.Printf("  Skewness (θ): %.2f\n", benchCfg.SkewnessTheta)
	fmt.Printf("  Involved Shards: %d\n", benchCfg.InvolvedShards)
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

	// Start background E2E latency poller BEFORE injection
	// This captures accurate commit times instead of delayed detection
	e2eResults := make(chan e2ePollResult, 1)
	go pollE2EBackground(client, benchCfg, stats, e2eResults)

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
				submitTx(client, benchCfg, accounts, contracts, stats, &localOK, &crossPending)
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

	// Collect E2E latency data from background poller
	fmt.Println("Collecting cross-shard E2E latency data...")
	e2eResult := <-e2eResults

	// Build E2E latencies from background poller's accurate commit times
	stats.crossTxIDsMu.Lock()
	submitTimes := make(map[string]time.Time, len(stats.CrossSubmitTimes))
	for k, v := range stats.CrossSubmitTimes {
		submitTimes[k] = v
	}
	stats.crossTxIDsMu.Unlock()

	var e2eLatencies []float64
	for txID, commitTime := range e2eResult.commitTimes {
		if submitTime, ok := submitTimes[txID]; ok {
			e2eLatencies = append(e2eLatencies, commitTime.Sub(submitTime).Seconds()*1000)
		}
	}

	// Quick status poll for commit rate calculation (separate from E2E latency)
	fmt.Println("Checking cross-shard transaction status (polling)...")
	pollTimeout := benchCfg.Cooldown
	if pollTimeout < 5*time.Second {
		pollTimeout = 5 * time.Second
	}
	crossCommittedSample, crossAbortedSample, crossPendingSample, _ := checkCrossShardStatus(client, benchCfg, stats, pollTimeout)
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
	var e2eP25, e2eP50, e2eP95, e2eP99 float64
	if len(e2eLatencies) > 0 {
		sort.Float64s(e2eLatencies)
		e2eP25 = e2eLatencies[int(float64(len(e2eLatencies)-1)*0.25)]
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
		fmt.Printf("    P25: %.1f ms, P50: %.1f ms, P95: %.1f ms, P99: %.1f ms\n", e2eP25, e2eP50, e2eP95, e2eP99)
		theoreticalMin := float64(benchCfg.BlockTimeMs) * 3
		theoreticalMax := float64(benchCfg.BlockTimeMs) * 10
		fmt.Printf("    Theoretical: %.0f-%.0fms (3-10 block cycles x %dms)\n",
			theoreticalMin, theoreticalMax, benchCfg.BlockTimeMs)
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

func submitTx(client *http.Client, cfg BenchmarkConfig, accounts *AccountStore, contracts *ContractStore, stats *BenchmarkStats, localOK, crossPending *int64) {
	startTime := time.Now()

	// Decide: cross-shard vs local, then contract vs transfer
	isCrossShard := rand.Float64() < cfg.CTRatio
	isContract := contracts != nil && rand.Float64() < cfg.ContractRatio

	fromShard := rand.Intn(cfg.NumShards)
	fromAddr := accounts.RandomFromShard(fromShard)
	if fromAddr == "" {
		atomic.AddInt64(&stats.TotalErrors, 1)
		return
	}

	if isContract {
		if isCrossShard {
			// Cross-shard contract call via travel contract
			submitCrossShardContract(client, cfg, fromShard, fromAddr, contracts, stats, crossPending, startTime)
		} else {
			// Local contract call
			submitLocalContract(client, cfg, fromShard, fromAddr, contracts, stats, localOK, startTime)
		}
	} else {
		// Simple transfer
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
			submitCrossShard(client, cfg, fromShard, fromAddr, toAddr, toShard, stats, crossPending, startTime)
		} else {
			submitLocal(client, cfg, fromShard, fromAddr, toAddr, stats, localOK, startTime)
		}
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

func submitLocalContract(client *http.Client, cfg BenchmarkConfig, shard int, from string, contracts *ContractStore, stats *BenchmarkStats, localOK *int64, startTime time.Time) {
	contractAddr, selector := contracts.RandomLocalContract(shard)
	if contractAddr == "" {
		// No local contracts on this shard, fall back to transfer
		toAddr := fmt.Sprintf("0x%d000000000000000000000000000000000000001", shard)
		submitLocal(client, cfg, shard, from, toAddr, stats, localOK, startTime)
		return
	}

	url := fmt.Sprintf("http://localhost:%d/tx/submit", cfg.BaseShardPort+shard)

	// Call book function with the from address as parameter (padded to 32 bytes)
	// Remove 0x prefix from address and left-pad to 32 bytes
	addrParam := strings.TrimPrefix(from, "0x")
	req := TxSubmitRequest{
		From:  from,
		To:    contractAddr,
		Value: "0",
		Data:  fmt.Sprintf("%s%064s", selector, addrParam),
		Gas:   100000, // Contract calls need more gas
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
	url := cfg.OrchestratorURL + "/cross-shard/submit"

	req := CrossShardSubmitRequest{
		FromShard: fromShard,
		From:      from,
		To:        to,
		RwSet: []RwSetEntry{
			{
				Address:        to,
				ReferenceBlock: ReferenceBlock{ShardNum: toShard},
			},
		},
		Value: "1",
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

// CrossShardContractRequest for contract calls via orchestrator
type CrossShardContractRequest struct {
	FromShard int          `json:"from_shard"`
	From      string       `json:"from"`
	To        string       `json:"to"`
	RwSet     []RwSetEntry `json:"rw_set"`
	Value     string       `json:"value"`
	Data      string       `json:"data"`
	Gas       uint64       `json:"gas"`
}

func submitCrossShardContract(client *http.Client, cfg BenchmarkConfig, fromShard int, from string, contracts *ContractStore, stats *BenchmarkStats, crossPending *int64, startTime time.Time) {
	// Get travel contract and additional booking contracts based on involved_shards
	travelAddr, travelShard, additionalBookings := contracts.GetBookingContractsForInvolvedShards(cfg.NumShards, cfg.InvolvedShards)
	if travelAddr == "" {
		// No travel contracts, fall back to cross-shard transfer
		toShard := (fromShard + 1) % cfg.NumShards
		toAddr := fmt.Sprintf("0x%d000000000000000000000000000000000000001", toShard)
		submitCrossShard(client, cfg, fromShard, from, toAddr, toShard, stats, crossPending, startTime)
		return
	}

	url := cfg.OrchestratorURL + "/cross-shard/submit"

	// Build RwSet with TravelAgency and all additional booking contracts
	rwSet := []RwSetEntry{
		{
			Address:        travelAddr,
			ReferenceBlock: ReferenceBlock{ShardNum: travelShard},
		},
	}
	for _, booking := range additionalBookings {
		rwSet = append(rwSet, RwSetEntry{
			Address:        booking.addr,
			ReferenceBlock: ReferenceBlock{ShardNum: booking.shard},
		})
	}

	// BookTrainAndHotel call - this contract internally calls train and hotel on other shards
	bookingID := rand.Intn(1000)
	req := CrossShardContractRequest{
		FromShard: fromShard,
		From:      from,
		To:        travelAddr,
		RwSet:     rwSet,
		Value:     "0",
		Data:      fmt.Sprintf("%s%064x", BookTrainAndHotelSelector, bookingID),
		Gas:       500000 + uint64(len(additionalBookings))*100000, // More gas for more contracts
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

// e2ePollResult holds results from background E2E latency polling
type e2ePollResult struct {
	commitTimes map[string]time.Time
}

// pollE2EBackground continuously polls sample TX status during injection
// to capture accurate commit times (not delayed by injection/cooldown phases)
func pollE2EBackground(client *http.Client, cfg BenchmarkConfig, stats *BenchmarkStats, result chan<- e2ePollResult) {
	commitTimes := make(map[string]time.Time)
	completed := make(map[string]bool)
	timeout := time.After(30 * time.Second)

	for {
		select {
		case <-timeout:
			result <- e2ePollResult{commitTimes: commitTimes}
			return
		default:
		}

		// Get current sample TX IDs
		stats.crossTxIDsMu.Lock()
		txIDs := make([]string, len(stats.CrossTxIDs))
		copy(txIDs, stats.CrossTxIDs)
		stats.crossTxIDsMu.Unlock()

		if len(txIDs) == 0 {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Poll uncompleted TXs in parallel
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, txID := range txIDs {
			if completed[txID] {
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
					completed[id] = true
					if status.Status == "committed" {
						commitTimes[id] = checkTime
					}
					mu.Unlock()
				}
			}(txID)
		}
		wg.Wait()

		// Check if all sample TXs are resolved
		if len(completed) >= len(txIDs) && len(txIDs) >= stats.maxCrossTxIDs {
			break
		}

		time.Sleep(50 * time.Millisecond)
	}

	result <- e2ePollResult{commitTimes: commitTimes}
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
