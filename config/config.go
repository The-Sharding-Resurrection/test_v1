package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config holds all configurable parameters for the application
type Config struct {
	ShardNum   int    `json:"shard_num"`
	StorageDir string `json:"storage_dir"`
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

	return cfg, nil
}

// LoadDefault loads the default config from config.json in the current directory
func LoadDefault() (*Config, error) {
	return Load("config/config.json")
}
