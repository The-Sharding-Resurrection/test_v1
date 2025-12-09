#!/bin/bash

# Test script for cross-shard transfers
# Run after: docker compose up -d

set -e

SHARD0="http://localhost:8545"
SHARD1="http://localhost:8546"
SHARD2="http://localhost:8547"
SHARD3="http://localhost:8548"
SHARD4="http://localhost:8549"
SHARD5="http://localhost:8550"

echo "=== Sharding Network Test ==="

# Health checks
echo -e "\n1. Checking health..."
curl -s $ORCH/health | jq .
curl -s $SHARD0/health | jq .
curl -s $SHARD1/health | jq .
curl -s $SHARD2/health | jq .
curl -s $SHARD3/health | jq .
curl -s $SHARD4/health | jq .
curl -s $SHARD5/health | jq .

# Test Account
S0ACCFROM="0xaCb39919bdAB6d9cBFD152559Db057EB79a58612"
S1ACCFROM="0xf723ACb3Ff832A78dBE7Ec766597FC8dEA1381c7"
S2ACCFROM="0xF1d84D11Aed718De63BB6D47b8cDB27D59D85656"
S3ACCFROM="0x55dA9a31Baf2a9Fe1c51656BD740A21Ef8BAe615"
S4ACCFROM="0x50799048483a13B2141589Bf39D9cf46341a8b88"
S5ACCFROM="0xed402a19Aa9Ea449732407A293B3794DE3073e95"

S0ACCTO="0xb5cE22D12322C7979e13F173644AF7AB75b65a72"
S1ACCTO="0xe38231A280817947A8D4bff76faB24e8A41C471f"
S2ACCTO="0xADC5682FB842Dfd4006cd4ee56F1895f648159c2"
S3ACCTO="0xF9D233924E6f6caFfbaFa71021c78F3e7B1A95Cf"
S4ACCTO="0x62e7B74aC5cC3860D07A196A96455a4Bfc2cE722"
S5ACCTO="0xaC7e3c48BCcDab3cC988382af7f9B2d67B9e7683"

# Check balance
echo -e "\n3. Checking balance on shard 0..."
curl -s $SHARD0/balance/$S0ACCFROM | jq .

echo -e "\n3. Checking balance on shard 1..."
curl -s $SHARD1/balance/$S1ACCFROM | jq .
echo -e "\n3. Checking balance on shard 2..."
curl -s $SHARD2/balance/$S2ACCFROM | jq .

echo -e "\n3. Checking balance on shard 3..."
curl -s $SHARD3/balance/$S3ACCFROM | jq .

echo -e "\n3. Checking balance on shard 4..."
curl -s $SHARD4/balance/$S4ACCFROM | jq .

echo -e "\n3. Checking balance on shard 5..."
curl -s $SHARD5/balance/$S5ACCFROM | jq .

echo -e "\n3. Checking balance on shard 0..."
curl -s $SHARD0/balance/$S0ACCTO | jq .

echo -e "\n3. Checking balance on shard 1..."
curl -s $SHARD1/balance/$S1ACCTO | jq .
echo -e "\n3. Checking balance on shard 2..."
curl -s $SHARD2/balance/$S2ACCTO | jq .

echo -e "\n3. Checking balance on shard 3..."
curl -s $SHARD3/balance/$S3ACCTO | jq .

echo -e "\n3. Checking balance on shard 4..."
curl -s $SHARD4/balance/$S4ACCTO | jq .

echo -e "\n3. Checking balance on shard 5..."
curl -s $SHARD5/balance/$S5ACCTO | jq .

# Initiate local transfer
echo -e "\n4. Initiating local transfer (shard 0)..."
curl -s -X POST $SHARD0/tx/submit \
  -H "Content-Type: application/json" \
  -d '{
    "from": "'$S0ACCFROM'",
    "to": "'$S0ACCTO'",
    "value": "100"
  }' | jq .

echo -e "\n4. Initiating local transfer (shard 1)..."
curl -s -X POST $SHARD1/tx/submit \
  -H "Content-Type: application/json" \
  -d '{
    "from": "'$S1ACCFROM'",
    "to": "'$S1ACCTO'",
    "value": "100"
  }' | jq .

echo -e "\n4. Initiating local transfer (shard 2)..."
curl -s -X POST $SHARD2/tx/submit \
  -H "Content-Type: application/json" \
  -d '{
    "from": "'$S2ACCFROM'",
    "to": "'$S2ACCTO'",
    "value": "100"
  }' | jq .

echo -e "\n4. Initiating local transfer (shard 3)..."
curl -s -X POST $SHARD3/tx/submit \
  -H "Content-Type: application/json" \
  -d '{
    "from": "'$S3ACCFROM'",
    "to": "'$S3ACCTO'",
    "value": "100"
  }' | jq .

echo -e "\n4. Initiating local transfer (shard 4)..."
curl -s -X POST $SHARD4/tx/submit \
  -H "Content-Type: application/json" \
  -d '{
    "from": "'$S4ACCFROM'",
    "to": "'$S4ACCTO'",
    "value": "100"
  }' | jq .

echo -e "\n4. Initiating local transfer (shard 5)..."
curl -s -X POST $SHARD5/tx/submit \
  -H "Content-Type: application/json" \
  -d '{
    "from": "'$S5ACCFROM'",
    "to": "'$S5ACCTO'",
    "value": "100"
  }' | jq .


# Wait for processing
echo -e "\n5. Waiting for transaction to process..."
sleep 5

# Check balances after transfer
echo -e "\n7. Checking balances after transfer..."
echo "Sender (shard 0):"
curl -s $SHARD0/balance/$S0ACCFROM | jq .
echo "Receiver (shard 0):"
curl -s $SHARD0/balance/$S0ACCTO | jq .
echo "Sender (shard 1):"
curl -s $SHARD1/balance/$S1ACCFROM | jq .
echo "Receiver (shard 1):"
curl -s $SHARD1/balance/$S1ACCTO | jq .
echo "Sender (shard 2):"
curl -s $SHARD2/balance/$S2ACCFROM | jq .
echo "Receiver (shard 2):"
curl -s $SHARD2/balance/$S2ACCTO | jq .
echo "Sender (shard 3):"
curl -s $SHARD3/balance/$S3ACCFROM | jq .
echo "Receiver (shard 3):"
curl -s $SHARD3/balance/$S3ACCTO | jq .
echo "Sender (shard 4):"
curl -s $SHARD4/balance/$S4ACCFROM | jq .
echo "Receiver (shard 4):"
curl -s $SHARD4/balance/$S4ACCTO | jq .
echo "Sender (shard 5):"
curl -s $SHARD5/balance/$S5ACCFROM | jq .
echo "Receiver (shard 5):"
curl -s $SHARD5/balance/$S5ACCTO | jq .

echo -e "\n=== Test Complete ==="