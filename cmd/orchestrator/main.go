package main

import (
	"flag"
	"log"
	"os"
	"strconv"

	"github.com/sharding-experiment/sharding/internal/orchestrator"
)

func main() {
	numShards := flag.Int("shards", 6, "Number of shards")
	port := flag.Int("port", 8080, "HTTP port")
	flag.Parse()

	// Allow environment variable override
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

	service := orchestrator.NewService(*numShards)
	log.Fatal(service.Start(*port))
}
