package main

import (
	"flag"
	"log"
	"os"
	"strconv"

	"github.com/sharding-experiment/sharding/config"
	"github.com/sharding-experiment/sharding/internal/orchestrator"
)

func main() {
	numShards := flag.Int("shards", 0, "Number of shards (0 = use config.json)")
	port := flag.Int("port", 8080, "HTTP port")
	bytecodePath := flag.String("bytecode-path", "", "Path for persistent bytecode storage")
	flag.Parse()

	// Load config first (primary source of truth)
	cfg, err := config.LoadDefault()
	var networkConfig config.NetworkConfig
	if err != nil {
		log.Printf("No config.json found, using defaults")
	} else {
		// Use config.json values as defaults
		if *numShards == 0 && cfg.ShardNum > 0 {
			*numShards = cfg.ShardNum
		}
		networkConfig = cfg.Network
		if networkConfig.DelayEnabled {
			log.Printf("Network delay simulation enabled: %d-%dms",
				networkConfig.MinDelayMs, networkConfig.MaxDelayMs)
		}
	}

	// Allow environment variable overrides (for backward compatibility)
	if envShards := os.Getenv("NUM_SHARDS"); envShards != "" {
		if n, err := strconv.Atoi(envShards); err == nil {
			*numShards = n
		}
	}

	if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			*port = p
		}
	}

	if envBytecodePath := os.Getenv("BYTECODE_STORE_PATH"); envBytecodePath != "" {
		*bytecodePath = envBytecodePath
	}

	// Final fallback if still not set
	if *numShards == 0 {
		*numShards = 8 // Default fallback
	}

	log.Printf("Starting orchestrator with %d shards", *numShards)

	service, err := orchestrator.NewService(*numShards, *bytecodePath, networkConfig)
	if err != nil {
		log.Fatalf("Failed to create orchestrator service: %v", err)
	}
	defer service.Close()

	log.Fatal(service.Start(*port))
}
