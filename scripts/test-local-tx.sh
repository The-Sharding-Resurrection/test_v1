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
curl -s $SHARD0/health | jq .
curl -s $SHARD1/health | jq .
curl -s $SHARD2/health | jq .
curl -s $SHARD3/health | jq .
curl -s $SHARD4/health | jq .
curl -s $SHARD5/health | jq .

# Test Account
S0ACCFROM="0x94E1A3a1567DaDA04e8128C7319a63b8080a081E"
S1ACCFROM="0x8ED078248c6f4689377165942059C793e744c631"
S2ACCFROM="0x72b151Ee881E6Bd788b4955A047F9183Dc292da4"
S3ACCFROM="0x78da5ab538Fb9d6824e6D116c353F8C534d2A1aB"
S4ACCFROM="0x39F49329130D331B35F4718BD0eF43c5274E3aB2"
S5ACCFROM="0x00A2466227c9F6F8e3Ed72E473711CF4e7dEAa05"

S0ACCTO="0x8456722d99f550EDE7E19345c41dc0220746fEA8"
S1ACCTO="0x7f642DdAfaF3BE117EAea13ABF038c1F1d0C2085"
S2ACCTO="0xb3412aDd0496fC380227Bc4386f29F27079da874"
S3ACCTO="0x3536C264525263f25cA9E9AD9f9c9217320bF903"
S4ACCTO="0xBD569Ba8F35bC7FBd0520d387987c22C10E12aE2"
S5ACCTO="0x0983176e9e35F3E9003f6A3A74403CA9d1d67C05"

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