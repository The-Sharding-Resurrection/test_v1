#!/bin/bash

# Test script for cross-shard transfers
# Run after: docker compose up -d

set -e

ORCH="http://localhost:8080"
SHARD0="http://localhost:8545"
SHARD1="http://localhost:8546"

echo "=== Sharding Network Test ==="

# Health checks
echo -e "\n1. Checking health..."
curl -s $ORCH/health | jq .
curl -s $SHARD0/health | jq .
curl -s $SHARD1/health | jq .

# Fund accounts via faucet
echo -e "\n2. Funding accounts on shard 0..."
curl -s -X POST $SHARD0/faucet \
  -H "Content-Type: application/json" \
  -d '{"address": "0x1234567890123456789012345678901234567890", "amount": "1000000000000000000000"}' | jq .

# Check balance
echo -e "\n3. Checking balance on shard 0..."
curl -s $SHARD0/balance/0x1234567890123456789012345678901234567890 | jq .

# Initiate cross-shard transfer
echo -e "\n4. Initiating cross-shard transfer (shard 0 -> shard 1)..."
RESULT=$(curl -s -X POST $SHARD0/cross-shard/transfer \
  -H "Content-Type: application/json" \
  -d '{
    "from": "0x1234567890123456789012345678901234567890",
    "to": "0xabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd",
    "to_shard": 1,
    "amount": "100000000000000000"
  }')
echo $RESULT | jq .

TX_ID=$(echo $RESULT | jq -r '.tx_id')

# Wait for processing
echo -e "\n5. Waiting for transaction to process..."
sleep 2

# Check transaction status
echo -e "\n6. Checking transaction status..."
curl -s $ORCH/cross-shard/status/$TX_ID | jq .

# Check balances after transfer
echo -e "\n7. Checking balances after transfer..."
echo "Sender (shard 0):"
curl -s $SHARD0/balance/0x1234567890123456789012345678901234567890 | jq .
echo "Receiver (shard 1):"
curl -s $SHARD1/balance/0xabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd | jq .

echo -e "\n=== Test Complete ==="
