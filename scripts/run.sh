#!/bin/bash

# Quick start script for the sharding network

set -e

echo "Building and starting sharding network..."
docker compose up --build -d

echo "Waiting for services to be ready..."
sleep 5

echo "Checking service health..."
curl -s http://localhost:8080/health && echo " - Orchestrator OK"
curl -s http://localhost:8545/health && echo " - Shard 0 OK"

echo ""
echo "Network is running!"
echo ""
echo "Endpoints:"
echo "  Orchestrator: http://localhost:8080"
echo "  Shard 0:      http://localhost:8545"
echo "  Shard 1:      http://localhost:8546"
echo "  Shard 2:      http://localhost:8547"
echo "  Shard 3:      http://localhost:8548"
echo "  Shard 4:      http://localhost:8549"
echo "  Shard 5:      http://localhost:8550"
echo ""
echo "Run './scripts/test-cross-shard.sh' to test cross-shard transfers"
