#!/bin/bash

# Verification script for protocol visualizations setup
# Checks that all files are present and dependencies are available

echo "=================================="
echo "Protocol Visualization Setup Check"
echo "=================================="
echo ""

# Color codes
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Counters
PASS=0
FAIL=0

# Function to check file existence
check_file() {
    if [ -f "$1" ]; then
        echo -e "${GREEN}✓${NC} $1"
        ((PASS++))
    else
        echo -e "${RED}✗${NC} $1 (missing)"
        ((FAIL++))
    fi
}

# Function to check directory existence
check_dir() {
    if [ -d "$1" ]; then
        echo -e "${GREEN}✓${NC} $1/"
        ((PASS++))
    else
        echo -e "${RED}✗${NC} $1/ (missing)"
        ((FAIL++))
    fi
}

# Function to check command availability
check_command() {
    if command -v "$1" &> /dev/null; then
        VERSION=$($1 --version 2>&1 | head -n 1)
        echo -e "${GREEN}✓${NC} $1 ($VERSION)"
        ((PASS++))
    else
        echo -e "${YELLOW}⚠${NC} $1 (not found - optional for $2)"
    fi
}

echo "=== Directory Structure ==="
check_dir "animation"
check_dir "animation/python"
check_dir "animation/javascript"
echo ""

echo "=== Python/Manim Files ==="
check_file "animation/python/common.py"
check_file "animation/python/baseline_protocol.py"
check_file "animation/python/twopc_protocol.py"
check_file "animation/python/comparison.py"
check_file "animation/python/requirements.txt"
echo ""

echo "=== JavaScript/D3.js Files ==="
check_file "animation/javascript/index.html"
check_file "animation/javascript/baseline.js"
check_file "animation/javascript/twopc.js"
check_file "animation/javascript/comparison.js"
check_file "animation/javascript/styles.css"
check_file "animation/javascript/README.md"
echo ""

echo "=== Documentation Files ==="
check_file "animation/README.md"
check_file "animation/TESTING.md"
check_file "animation/IMPLEMENTATION_SUMMARY.md"
echo ""

echo "=== Dependencies ==="
check_command "python" "Python/Manim animations"
check_command "python3" "Python/Manim animations"
check_command "pip" "installing Python dependencies"
check_command "manim" "rendering animations"
check_command "ffmpeg" "video encoding"
check_command "node" "serving JavaScript visualizations"
check_command "npx" "serving JavaScript visualizations"
echo ""

echo "=== Python Package Check ==="
if command -v python3 &> /dev/null; then
    if python3 -c "import manim" 2>/dev/null; then
        echo -e "${GREEN}✓${NC} manim Python package installed"
        ((PASS++))
    else
        echo -e "${YELLOW}⚠${NC} manim Python package not installed"
        echo "  Install with: pip install -r animation/python/requirements.txt"
    fi
else
    echo -e "${YELLOW}⚠${NC} Python 3 not found, cannot check manim package"
fi
echo ""

echo "=== Git Status ==="
if [ -f ".gitignore" ]; then
    if grep -q "animation/output/" .gitignore && grep -q "animation/python/media/" .gitignore; then
        echo -e "${GREEN}✓${NC} .gitignore configured for animation outputs"
        ((PASS++))
    else
        echo -e "${YELLOW}⚠${NC} .gitignore may need animation entries"
        echo "  Add: animation/output/ and animation/python/media/"
    fi
else
    echo -e "${RED}✗${NC} .gitignore not found"
    ((FAIL++))
fi
echo ""

echo "=== Summary ==="
echo -e "Passed: ${GREEN}${PASS}${NC}"
if [ $FAIL -gt 0 ]; then
    echo -e "Failed: ${RED}${FAIL}${NC}"
else
    echo -e "Failed: ${GREEN}0${NC}"
fi
echo ""

if [ $FAIL -eq 0 ]; then
    echo -e "${GREEN}✓ Setup verification complete!${NC}"
    echo ""
    echo "Next steps:"
    echo "1. Install dependencies: pip install -r animation/python/requirements.txt"
    echo "2. Test Python animations: cd animation/python && manim -pql baseline_protocol.py BaselineProtocol"
    echo "3. Test JavaScript visualizations: cd animation/javascript && python -m http.server 8000"
    echo "4. Read testing guide: animation/TESTING.md"
else
    echo -e "${RED}✗ Some checks failed. Please review the output above.${NC}"
    exit 1
fi
