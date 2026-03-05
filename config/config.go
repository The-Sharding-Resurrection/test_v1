package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config holds all configurable parameters for the application
type Config struct {
	ShardNum       int    `json:"shard_num"`
	StorageDir     string `json:"storage_dir"`
	TestAccountNum int    `json:"test_account_num"`
	NumContracts   int    `json:"num_contracts"`
	BlockTimeMs    int    `json:"block_time_ms"`
	// BlockTimeSeconds is kept for backward compatibility and converted to ms.
	BlockTimeSeconds int              `json:"block_time_seconds,omitempty"`
	Network          NetworkConfig    `json:"network,omitempty"`
	Benchmark        *BenchmarkConfig `json:"benchmark,omitempty"`
}

// BenchmarkConfig holds benchmark-specific settings from config.json
type BenchmarkConfig struct {
	Enabled          bool             `json:"enabled"`
	DurationSeconds  int              `json:"duration_seconds"`
	WarmupSeconds    int              `json:"warmup_seconds"`
	CooldownSeconds  int              `json:"cooldown_seconds"`
	Workload         WorkloadConfig   `json:"workload"`
	Output           OutputConfig     `json:"output"`
	FinalizationTimeoutSeconds int    `json:"finalization_timeout_seconds"`
	FinalizationPollIntervalMs int    `json:"finalization_poll_interval_ms"`
}

// WorkloadConfig holds workload generation parameters
type WorkloadConfig struct {
	CTRatio           float64 `json:"ct_ratio"`
	SendContractRatio float64 `json:"send_contract_ratio"`
	InjectionRate     int     `json:"injection_rate"`
	SkewnessTheta     float64 `json:"skewness_theta"`
	InvolvedShards    int     `json:"involved_shards"`
}

// OutputConfig holds benchmark output settings
type OutputConfig struct {
	Format             string `json:"format"`
	File               string `json:"file"`
	IncludeRawLatencies bool  `json:"include_raw_latencies"`
}

// NetworkConfig holds network simulation parameters
type NetworkConfig struct {
	DelayEnabled bool `json:"delay_enabled"`
	MinDelayMs   int  `json:"min_delay_ms"` // Minimum delay in milliseconds
	MaxDelayMs   int  `json:"max_delay_ms"` // Maximum delay in milliseconds
}

// Load reads and parses the config.json file
func Load(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Normalize legacy seconds setting to milliseconds.
	if cfg.BlockTimeMs == 0 && cfg.BlockTimeSeconds > 0 {
		cfg.BlockTimeMs = cfg.BlockTimeSeconds * 1000
	}

	return cfg, nil
}

// LoadDefault loads the default config from config.json in the current directory
func LoadDefault() (*Config, error) {
	paths := []string{
		// Try multiple paths for compatibility
		"/config/config.json",   // Docker absolute path
		"config/config.json",    // Relative path (local dev)
		"../config/config.json", // From subdirectory
	}

	for _, path := range paths {
		cfg, err := Load(path)
		if err == nil {
			return cfg, nil
		}
	}
	return nil, fmt.Errorf("config.json not found in any expected location")
}
