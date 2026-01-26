package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config holds all configurable parameters for the application
type Config struct {
	ShardNum       int           `json:"shard_num"`
	StorageDir     string        `json:"storage_dir"`
	TestAccountNum int           `json:"test_account_num"`
	Network        NetworkConfig `json:"network,omitempty"`
}

// NetworkConfig holds network simulation parameters
type NetworkConfig struct {
	DelayEnabled bool `json:"delay_enabled"`
	MinDelayMs   int  `json:"min_delay_ms"` // Minimum delay in milliseconds
	MaxDelayMs   int  `json:"max_delay_ms"` // Maximum delay in milliseconds
}

var Configuration Config

func GetConfig() Config {
	return Configuration
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

	Configuration = *cfg
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
