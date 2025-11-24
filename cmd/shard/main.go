package main

import (
	"flag"
	"log"
	"os"
	"strconv"

	"github.com/sharding-experiment/sharding/internal/shard"
)

func main() {
	shardID := flag.Int("id", -1, "Shard ID")
	port := flag.Int("port", 8545, "HTTP port")
	orchestrator := flag.String("orchestrator", "http://shard-orch:8080", "Orchestrator URL")
	flag.Parse()

	// Allow environment variable override
	if *shardID == -1 {
		if id, err := strconv.Atoi(os.Getenv("SHARD_ID")); err == nil {
			*shardID = id
		} else {
			log.Fatal("SHARD_ID required")
		}
	}

	if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			*port = p
		}
	}

	if envOrch := os.Getenv("ORCHESTRATOR_URL"); envOrch != "" {
		*orchestrator = envOrch
	}

	server := shard.NewServer(*shardID, *orchestrator)
	log.Fatal(server.Start(*port))
}
