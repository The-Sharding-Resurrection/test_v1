#!/bin/bash

# Installation script for Manim and its dependencies
# Run this script with: bash animation/install_manim.sh

echo "=================================="
echo "Manim Installation Script"
echo "=================================="
echo ""

# Color codes
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Check if running as root or with sudo
if [ "$EUID" -ne 0 ]; then
    echo -e "${YELLOW}This script needs sudo access to install system packages.${NC}"
    echo "Re-running with sudo..."
    sudo "$0" "$@"
    exit $?
fi

echo "=== Step 1: Update package lists ==="
apt-get update || { echo -e "${RED}Failed to update packages${NC}"; exit 1; }
echo -e "${GREEN}✓ Package lists updated${NC}"
echo ""

echo "=== Step 2: Install system dependencies ==="
apt-get install -y \
    pkg-config \
    libcairo2-dev \
    libpango1.0-dev \
    ffmpeg \
    || { echo -e "${RED}Failed to install system packages${NC}"; exit 1; }
echo -e "${GREEN}✓ System dependencies installed${NC}"
echo ""

echo "=== Step 3: Install Manim in virtual environment ==="
# Find the user who invoked sudo
REAL_USER=${SUDO_USER:-$USER}
REAL_HOME=$(eval echo ~$REAL_USER)

if [ -d "$REAL_HOME/.fullpython" ]; then
    echo "Installing manim in $REAL_HOME/.fullpython..."
    sudo -u $REAL_USER $REAL_HOME/.fullpython/bin/pip install manim || {
        echo -e "${RED}Failed to install manim${NC}"
        exit 1
    }
    echo -e "${GREEN}✓ Manim installed in virtual environment${NC}"
else
    echo -e "${YELLOW}Warning: ~/.fullpython not found, installing system-wide${NC}"
    pip3 install manim || { echo -e "${RED}Failed to install manim${NC}"; exit 1; }
    echo -e "${GREEN}✓ Manim installed system-wide${NC}"
fi
echo ""

echo "=== Step 4: Verify installation ==="
if [ -d "$REAL_HOME/.fullpython" ]; then
    sudo -u $REAL_USER $REAL_HOME/.fullpython/bin/manim --version || {
        echo -e "${RED}Manim verification failed${NC}"
        exit 1
    }
else
    manim --version || { echo -e "${RED}Manim verification failed${NC}"; exit 1; }
fi
echo -e "${GREEN}✓ Manim is ready to use${NC}"
echo ""

echo "=================================="
echo -e "${GREEN}Installation complete!${NC}"
echo "=================================="
echo ""
echo "Next steps:"
echo "1. Generate videos:"
echo "   cd animation/python"
if [ -d "$REAL_HOME/.fullpython" ]; then
    echo "   ~/.fullpython/bin/manim -pql baseline_protocol.py BaselineProtocol"
else
    echo "   manim -pql baseline_protocol.py BaselineProtocol"
fi
echo ""
echo "2. See all options:"
echo "   animation/TESTING.md"
