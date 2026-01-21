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
	numShards := flag.Int("shards", 6, "Number of shards")
	port := flag.Int("port", 8080, "HTTP port")
	bytecodePath := flag.String("bytecode-path", "", "Path for persistent bytecode storage")
	flag.Parse()

	// Allow environment variable overrides
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

	// Load network config (optional - defaults to no delay)
	cfg, err := config.LoadDefault()
	var networkConfig config.NetworkConfig
	if err != nil {
		log.Printf("No config.json found, using default network settings (no delay)")
	} else {
		networkConfig = cfg.Network
		if networkConfig.DelayEnabled {
			log.Printf("Network delay simulation enabled: %d-%dms",
				networkConfig.MinDelayMs, networkConfig.MaxDelayMs)
		}
	}

	service, err := orchestrator.NewService(*numShards, *bytecodePath, networkConfig)
	if err != nil {
		log.Fatalf("Failed to create orchestrator service: %v", err)
	}
	defer service.Close()

	log.Fatal(service.Start(*port))
}
