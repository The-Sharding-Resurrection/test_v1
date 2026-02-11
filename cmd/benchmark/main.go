package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
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

	// Per-shard breakdown
	ShardSubmitted []int64 // Per-shard submission count
	ShardCommitted []int64 // Per-shard commit count

	// Per-type breakdown
	LocalTransfers   int64
	LocalContracts   int64
	CrossTransfers   int64
	CrossContracts   int64
}

// ZipfianGenerator generates Zipfian-distributed random numbers.
// Each generator has its own RNG source to avoid contention under concurrent access.
type ZipfianGenerator struct {
	numItems int
	theta    float64
	zeta     float64
	cdf      []float64
	rng      *rand.Rand
	mu       sync.Mutex
}

// NewZipfianGenerator creates a new Zipfian generator with its own RNG source.
func NewZipfianGenerator(numItems int, theta float64) *ZipfianGenerator {
	rng := rand.New(rand.NewSource(rand.Int63()))

	if numItems <= 0 {
		return &ZipfianGenerator{numItems: 1, theta: 0, rng: rng}
	}
	if theta <= 0 {
		return &ZipfianGenerator{numItems: numItems, theta: 0, rng: rng}
	}

	// Calculate zeta(N, theta) = sum(1/i^theta for i=1..N)
	zeta := 0.0
	for i := 1; i <= numItems; i++ {
		zeta += math.Exp(-theta * math.Log(float64(i)))
	}

	// Build CDF
	cdf := make([]float64, numItems)
	cumsum := 0.0
	for i := 1; i <= numItems; i++ {
		prob := math.Exp(-theta*math.Log(float64(i))) / zeta
		cumsum += prob
		cdf[i-1] = cumsum
	}
	// Normalize final entry to exactly 1.0 to avoid floating-point edge cases
	cdf[numItems-1] = 1.0

	return &ZipfianGenerator{
		numItems: numItems,
		theta:    theta,
		zeta:     zeta,
		cdf:      cdf,
		rng:      rng,
	}
}

// Next returns the next Zipfian-distributed index [0, numItems)
func (z *ZipfianGenerator) Next() int {
	z.mu.Lock()
	defer z.mu.Unlock()

	if z.theta <= 0 || z.cdf == nil {
		return z.rng.Intn(z.numItems)
	}

	// Binary search in CDF
	u := z.rng.Float64()
	left, right := 0, z.numItems-1
	for left < right {
		mid := (left + right) / 2
		if z.cdf[mid] < u {
			left = mid + 1
		} else {
			right = mid
		}
	}
	return left
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
	NumWorkers      int
	OrchestratorURL string
	BaseShardPort   int
	BlockTimeMs     int  // For latency context
	RateLimit       bool // Strictly enforce injection rate
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
	ByShard    map[int][]string
	ZipfGens   map[int]*ZipfianGenerator // Zipfian generator per shard (for from-address)
	SkewTheta  float64                   // Zipfian skew parameter

	// Global pool for to-address selection (Zipfian across all shards)
	AllAddresses  []string              // flat list of all addresses
	AddressShards []int                 // shard index for each address in AllAddresses
	ToZipf        *ZipfianGenerator     // global Zipfian generator for to-address
}

// ContractStore holds contract addresses grouped by shard and type (local/cross)
type ContractStore struct {
	// Travel contracts that make cross-shard calls to train+hotel
	TravelByShard map[int][]string
	// Local-only contracts with their selectors
	LocalByShard map[int][]ContractEntry

	// Global pool of travel contracts for Zipfian selection
	AllTravel       []struct{ addr string; shard int }
	TravelZipf      *ZipfianGenerator

	// Per-shard Zipfian generators for local contracts
	LocalZipfGens   map[int]*ZipfianGenerator
}

// LoadAccounts reads accounts from storage/address.txt
func LoadAccounts(path string, numShards int, zipfTheta float64) (*AccountStore, error) {
	store := &AccountStore{
		ByShard:   make(map[int][]string),
		ZipfGens:  make(map[int]*ZipfianGenerator),
		SkewTheta: zipfTheta,
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

	// Initialize Zipfian generators for each shard (used for from-address selection)
	for shard := 0; shard < numShards; shard++ {
		count := len(store.ByShard[shard])
		store.ZipfGens[shard] = NewZipfianGenerator(count, zipfTheta)
	}

	// Build global address pool for to-address selection
	for shard := 0; shard < numShards; shard++ {
		for _, addr := range store.ByShard[shard] {
			store.AllAddresses = append(store.AllAddresses, addr)
			store.AddressShards = append(store.AddressShards, shard)
		}
	}
	store.ToZipf = NewZipfianGenerator(len(store.AllAddresses), zipfTheta)

	return store, scanner.Err()
}

// RandomFromShard returns a random account from the given shard (using Zipfian if configured)
func (s *AccountStore) RandomFromShard(shard int) string {
	accounts := s.ByShard[shard]
	if len(accounts) == 0 {
		return ""
	}

	if s.SkewTheta > 0 && s.ZipfGens[shard] != nil {
		idx := s.ZipfGens[shard].Next()
		return accounts[idx]
	}

	return accounts[rand.Intn(len(accounts))]
}

// RandomToAddress returns a Zipfian-skewed to-address from the global pool,
// ensuring it's on a different shard than excludeShard (for cross-shard txs).
func (s *AccountStore) RandomToAddress(excludeShard int, numShards int) (string, int) {
	if s.SkewTheta <= 0 || s.ToZipf == nil || len(s.AllAddresses) == 0 {
		// Uniform fallback: pick random different shard, then random address
		toShard := (excludeShard + 1 + rand.Intn(numShards-1)) % numShards
		return s.RandomFromShard(toShard), toShard
	}

	// Global Zipfian: pick from entire address pool, retry if same shard
	for attempt := 0; attempt < 20; attempt++ {
		idx := s.ToZipf.Next()
		addr := s.AllAddresses[idx]
		shard := s.AddressShards[idx]
		if shard != excludeShard {
			return addr, shard
		}
	}

	// Fallback after retries: pick random different shard with per-shard Zipfian
	toShard := (excludeShard + 1 + rand.Intn(numShards-1)) % numShards
	return s.RandomFromShard(toShard), toShard
}

// RandomToLocal returns a Zipfian-skewed to-address on the same shard as from.
func (s *AccountStore) RandomToLocal(shard int) string {
	return s.RandomFromShard(shard)
}

// LoadContracts reads contract addresses from storage files
func LoadContracts(storageDir string, numShards int, zipfTheta float64) (*ContractStore, error) {
	store := &ContractStore{
		TravelByShard: make(map[int][]string),
		LocalByShard:  make(map[int][]ContractEntry),
		LocalZipfGens: make(map[int]*ZipfianGenerator),
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

	// Build global travel contract pool + Zipfian generator
	for sh := 0; sh < numShards; sh++ {
		for _, a := range store.TravelByShard[sh] {
			store.AllTravel = append(store.AllTravel, struct{ addr string; shard int }{a, sh})
		}
	}
	store.TravelZipf = NewZipfianGenerator(len(store.AllTravel), zipfTheta)

	// Build per-shard Zipfian generators for local contracts
	for sh := 0; sh < numShards; sh++ {
		store.LocalZipfGens[sh] = NewZipfianGenerator(len(store.LocalByShard[sh]), zipfTheta)
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

// RandomTravelContract returns a Zipfian-skewed travel contract from the global pool
func (s *ContractStore) RandomTravelContract(numShards int) (addr string, shard int) {
	if len(s.AllTravel) == 0 {
		return "", -1
	}
	if s.TravelZipf != nil && s.TravelZipf.theta > 0 {
		idx := s.TravelZipf.Next()
		return s.AllTravel[idx].addr, s.AllTravel[idx].shard
	}
	choice := s.AllTravel[rand.Intn(len(s.AllTravel))]
	return choice.addr, choice.shard
}

// RandomLocalContract returns a Zipfian-skewed local contract from the given shard
func (s *ContractStore) RandomLocalContract(shard int) (addr string, selector string) {
	contracts := s.LocalByShard[shard]
	if len(contracts) == 0 {
		return "", ""
	}
	if gen, ok := s.LocalZipfGens[shard]; ok && gen != nil && gen.theta > 0 {
		idx := gen.Next()
		return contracts[idx].Address, contracts[idx].Selector
	}
	entry := contracts[rand.Intn(len(contracts))]
	return entry.Address, entry.Selector
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
	numWorkers := flag.Int("workers", 1000, "Number of concurrent workers")
	rateLimit := flag.Bool("rate-limit", false, "Strictly enforce injection rate (for latency testing)")
	csvFile := flag.String("csv", "", "Output results to CSV file (e.g., results.csv)")
	zipfTheta := flag.Float64("zipf", 0.0, "Zipfian skew parameter (0.0=uniform, 0.9=highly skewed)")
	flag.Parse()

	// Validate CLI flags
	if *duration <= 0 {
		log.Fatal("--duration must be positive")
	}
	if *cooldown < 0 {
		log.Fatal("--cooldown must be non-negative")
	}
	if *injectionRate <= 0 {
		log.Fatal("--injection-rate must be positive")
	}
	if *ctRatio < 0 || *ctRatio > 1 {
		log.Fatal("--ct-ratio must be between 0.0 and 1.0")
	}
	if *contractRatio < 0 || *contractRatio > 1 {
		log.Fatal("--contract-ratio must be between 0.0 and 1.0")
	}
	if *numWorkers <= 0 {
		log.Fatal("--workers must be positive")
	}
	if *zipfTheta < 0 || *zipfTheta >= 1 {
		log.Fatal("--zipf must be in range [0.0, 1.0)")
	}

	// Load config (config-first: config.json provides defaults, CLI flags override)
	cfg, err := config.LoadDefault()
	if err != nil {
		log.Printf("Warning: Could not load config, using defaults: %v", err)
		cfg = &config.Config{ShardNum: 8}
	}

	// Track which flags were explicitly set on CLI
	flagsSet := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { flagsSet[f.Name] = true })

	// Apply config.json benchmark settings as defaults, CLI flags override
	if cfg.Benchmark != nil {
		b := cfg.Benchmark
		if !flagsSet["duration"] && b.DurationSeconds > 0 {
			*duration = b.DurationSeconds
		}
		if !flagsSet["cooldown"] && b.CooldownSeconds > 0 {
			*cooldown = b.CooldownSeconds
		}
		if !flagsSet["injection-rate"] && b.Workload.InjectionRate > 0 {
			*injectionRate = b.Workload.InjectionRate
		}
		if !flagsSet["ct-ratio"] {
			*ctRatio = b.Workload.CTRatio
		}
		if !flagsSet["contract-ratio"] {
			*contractRatio = b.Workload.SendContractRatio
		}
		if !flagsSet["zipf"] {
			*zipfTheta = b.Workload.SkewnessTheta
		}
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
		ContractRatio:   *contractRatio,
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
	accounts, err := LoadAccounts("storage/address.txt", benchCfg.NumShards, *zipfTheta)
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
		contracts, err = LoadContracts("storage", benchCfg.NumShards, *zipfTheta)
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
	fmt.Printf("  Target Injection Rate: %d tx/s\n", benchCfg.InjectionRate)
	fmt.Printf("  Duration: %v\n", benchCfg.Duration)
	fmt.Printf("  Cooldown: %v\n", benchCfg.Cooldown)
	fmt.Printf("  Workers: %d\n", benchCfg.NumWorkers)
	if *zipfTheta > 0 {
		fmt.Printf("  Zipfian Skew (θ): %.2f\n", *zipfTheta)
	}
	if *csvFile != "" {
		fmt.Printf("  CSV Output: %s\n", *csvFile)
	}
	fmt.Println()

	stats := &BenchmarkStats{
		SubmitLatencies:  make([]float64, 0, benchCfg.InjectionRate*int(benchCfg.Duration.Seconds())),
		CrossTxIDs:       make([]string, 0, 500),
		CrossSubmitTimes: make(map[string]time.Time, 500),
		maxCrossTxIDs:    500, // Sample size for status checking
		ShardSubmitted:   make([]int64, benchCfg.NumShards),
		ShardCommitted:   make([]int64, benchCfg.NumShards),
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
	fmt.Println()

	// Enhanced monitoring: Per-shard breakdown
	fmt.Println("  Per-Shard Breakdown:")
	for shard := 0; shard < benchCfg.NumShards; shard++ {
		submitted := atomic.LoadInt64(&stats.ShardSubmitted[shard])
		committed := atomic.LoadInt64(&stats.ShardCommitted[shard])
		tps := float64(committed) / actualDuration
		fmt.Printf("    Shard %d: %d submitted, %d committed (%.2f tps)\n",
			shard, submitted, committed, tps)
	}
	fmt.Println()

	// Enhanced monitoring: Per-type breakdown
	fmt.Println("  Transaction Type Breakdown:")
	fmt.Printf("    Local Transfers:  %d\n", atomic.LoadInt64(&stats.LocalTransfers))
	fmt.Printf("    Local Contracts:  %d\n", atomic.LoadInt64(&stats.LocalContracts))
	fmt.Printf("    Cross Transfers:  %d\n", atomic.LoadInt64(&stats.CrossTransfers))
	fmt.Printf("    Cross Contracts:  %d\n", atomic.LoadInt64(&stats.CrossContracts))

	// CSV export if requested
	if *csvFile != "" {
		if err := exportToCSV(*csvFile, benchCfg, stats, accounts, actualDuration, totalCommitted, e2eP50, e2eP95, e2eP99); err != nil {
			log.Printf("Failed to export CSV: %v", err)
		} else {
			fmt.Printf("\n  Results exported to %s\n", *csvFile)
		}
	}
}

// exportToCSV exports benchmark results to a CSV file
func exportToCSV(filename string, cfg BenchmarkConfig, stats *BenchmarkStats, accounts *AccountStore, duration float64, totalCommitted int64, e2eP50, e2eP95, e2eP99 float64) error {
	// Check if file exists to determine if we need headers
	fileExists := false
	if _, err := os.Stat(filename); err == nil {
		fileExists = true
	}

	// Open file in append mode
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write headers if new file
	if !fileExists {
		headers := []string{
			"timestamp",
			"duration_s",
			"injection_rate",
			"ct_ratio",
			"contract_ratio",
			"zipf_theta",
			"total_submitted",
			"total_committed",
			"total_aborted",
			"total_pending",
			"total_errors",
			"actual_tps",
			"commit_rate",
			"submit_p50_ms",
			"submit_p95_ms",
			"submit_p99_ms",
			"e2e_p50_ms",
			"e2e_p95_ms",
			"e2e_p99_ms",
			"local_transfers",
			"local_contracts",
			"cross_transfers",
			"cross_contracts",
		}
		if err := writer.Write(headers); err != nil {
			return err
		}
	}

	// Write data row
	totalSubmitted := atomic.LoadInt64(&stats.TotalSubmitted)
	actualTPS := float64(totalCommitted) / duration
	commitRate := 0.0
	if totalSubmitted > 0 {
		commitRate = float64(totalCommitted) / float64(totalSubmitted)
	}

	row := []string{
		time.Now().Format(time.RFC3339),
		fmt.Sprintf("%.2f", duration),
		fmt.Sprintf("%d", cfg.InjectionRate),
		fmt.Sprintf("%.2f", cfg.CTRatio),
		fmt.Sprintf("%.2f", cfg.ContractRatio),
		fmt.Sprintf("%.2f", accounts.SkewTheta),
		fmt.Sprintf("%d", totalSubmitted),
		fmt.Sprintf("%d", totalCommitted),
		fmt.Sprintf("%d", atomic.LoadInt64(&stats.TotalAborted)),
		fmt.Sprintf("%d", atomic.LoadInt64(&stats.TotalPending)),
		fmt.Sprintf("%d", atomic.LoadInt64(&stats.TotalErrors)),
		fmt.Sprintf("%.2f", actualTPS),
		fmt.Sprintf("%.4f", commitRate),
		fmt.Sprintf("%.1f", stats.SubmitPercentile(50)),
		fmt.Sprintf("%.1f", stats.SubmitPercentile(95)),
		fmt.Sprintf("%.1f", stats.SubmitPercentile(99)),
		fmt.Sprintf("%.1f", e2eP50),
		fmt.Sprintf("%.1f", e2eP95),
		fmt.Sprintf("%.1f", e2eP99),
		fmt.Sprintf("%d", atomic.LoadInt64(&stats.LocalTransfers)),
		fmt.Sprintf("%d", atomic.LoadInt64(&stats.LocalContracts)),
		fmt.Sprintf("%d", atomic.LoadInt64(&stats.CrossTransfers)),
		fmt.Sprintf("%d", atomic.LoadInt64(&stats.CrossContracts)),
	}

	return writer.Write(row)
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

	// Track per-shard submission
	atomic.AddInt64(&stats.ShardSubmitted[fromShard], 1)

	if isContract {
		if isCrossShard {
			// Cross-shard contract call via travel contract
			submitCrossShardContract(client, cfg, fromShard, fromAddr, contracts, stats, crossPending, startTime)
			atomic.AddInt64(&stats.CrossContracts, 1)
		} else {
			// Local contract call
			submitLocalContract(client, cfg, fromShard, fromAddr, contracts, stats, localOK, startTime)
			atomic.AddInt64(&stats.LocalContracts, 1)
		}
	} else {
		// Simple transfer
		if isCrossShard {
			// Global Zipfian for to-address: concentrates on hot targets across all shards
			toAddr, toShard := accounts.RandomToAddress(fromShard, cfg.NumShards)
			if toAddr == "" {
				atomic.AddInt64(&stats.TotalErrors, 1)
				return
			}
			submitCrossShard(client, cfg, fromShard, fromAddr, toAddr, toShard, stats, crossPending, startTime)
			atomic.AddInt64(&stats.CrossTransfers, 1)
		} else {
			toAddr := accounts.RandomToLocal(fromShard)
			if toAddr == "" {
				atomic.AddInt64(&stats.TotalErrors, 1)
				return
			}
			submitLocal(client, cfg, fromShard, fromAddr, toAddr, stats, localOK, startTime)
			atomic.AddInt64(&stats.LocalTransfers, 1)
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
		atomic.AddInt64(&stats.ShardCommitted[shard], 1)
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
		atomic.AddInt64(&stats.ShardCommitted[shard], 1)
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
	// Get a travel contract (which makes cross-shard calls to train+hotel)
	travelAddr, travelShard := contracts.RandomTravelContract(cfg.NumShards)
	if travelAddr == "" {
		// No travel contracts, fall back to cross-shard transfer
		toShard := (fromShard + 1) % cfg.NumShards
		toAddr := fmt.Sprintf("0x%d000000000000000000000000000000000000001", toShard)
		submitCrossShard(client, cfg, fromShard, from, toAddr, toShard, stats, crossPending, startTime)
		return
	}

	// Use /cross-shard/call so the orchestrator runs EVM simulation to discover
	// actual storage slot access (RwSet with ReadSet/WriteSet).
	// This enables slot-level locking and meaningful conflict detection.
	url := cfg.OrchestratorURL + "/cross-shard/call"

	bookingID := rand.Intn(1000)
	req := CrossShardContractRequest{
		FromShard: fromShard,
		From:      from,
		To:        travelAddr,
		RwSet: []RwSetEntry{
			{
				Address:        travelAddr,
				ReferenceBlock: ReferenceBlock{ShardNum: travelShard},
			},
		},
		Value: "0",
		Data:  fmt.Sprintf("%s%064x", BookTrainAndHotelSelector, bookingID),
		Gas:   500000,
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
