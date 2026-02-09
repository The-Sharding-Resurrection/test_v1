# Cross-Shard Protocol Visualizations

This directory contains visualizations comparing two experimental approaches for cross-shard transactions:

1. **Baseline Protocol** (hop-based iterative execution)
2. **2PC Protocol** (simulation-based two-phase commit)

## Two Implementations

### Python/Manim (Offline Videos)
Professional-quality animated videos for presentations and documentation.

**Location:** `animation/python/`

**Usage:**
```bash
# Install dependencies
pip install -r animation/python/requirements.txt

# Generate baseline protocol video (720p preview)
cd animation/python
manim -pql baseline_protocol.py BaselineProtocol

# Generate 2PC protocol video (1080p high quality)
manim -pqh twopc_protocol.py TwoPCProtocol

# Generate side-by-side comparison video
manim -pqh comparison.py Comparison

# Generate all videos at once
manim -pqh *.py
```

**Outputs:** Videos saved to `animation/python/media/videos/`

### JavaScript/D3.js (Interactive Web Demos)
Interactive browser-based visualizations with playback controls.

**Location:** `animation/javascript/`

**Usage:**
```bash
# Serve locally
cd animation/javascript
python -m http.server 8000
# Open http://localhost:8000/index.html

# Or use VS Code Live Server extension
```

**Features:**
- Play/Pause/Step controls
- Adjustable animation speed
- Hover tooltips
- Side-by-side comparison mode

## Protocol Overview

### Baseline Protocol
- **Approach:** Hop-based iterative execution
- **Orchestrator:** Stateless (simple routing)
- **Detection:** NoStateError on external calls
- **RwSet:** Accumulated progressively per hop
- **Locking:** Progressive locking per hop
- **Complexity:** Low (minimal coordination)

### 2PC Protocol
- **Approach:** Simulation-based atomic commit
- **Orchestrator:** Stateful (runs EVM simulation)
- **Detection:** Pre-execution tracer detects all dependencies
- **RwSet:** Populated upfront via simulation
- **Locking:** Coordinated prepare/commit phases
- **Complexity:** High (vote aggregation, crash recovery)

## Example Scenario

Both visualizations use the **TravelAgency** example:
```solidity
// User calls TravelAgency.bookTrainAndHotel() on Shard A
// → Calls Train.checkSeat() on Shard B
// → Calls Hotel.checkRoom() on Shard C
```

**Baseline:** 3 hops (A → B → C), ~9 seconds
**2PC:** 2 rounds (prepare + commit), ~6 seconds

## File Structure

```
animation/
├── python/                  # Manim animations
│   ├── common.py           # Shared network utilities
│   ├── baseline_protocol.py
│   ├── twopc_protocol.py
│   ├── comparison.py
│   └── requirements.txt
├── javascript/              # D3.js visualizations
│   ├── index.html          # Main navigation
│   ├── baseline.js
│   ├── twopc.js
│   ├── comparison.html
│   └── styles.css
├── output/                  # Generated video files
│   ├── baseline.mp4
│   ├── twopc.mp4
│   └── comparison.mp4
└── README.md               # This file
```

## Documentation Links

- **Baseline Protocol:** `docs/baseline-protocol.md`
- **2PC Protocol:** `docs/2pc-protocol.md`
- **Architecture:** `docs/architecture.md`
- **V2 Specification:** `docs/V2.md`

## Dependencies

**Python/Manim:**
```bash
pip install manim
# System requirements: Python 3.8+, FFmpeg
```

**JavaScript/D3.js:**
- Modern web browser
- Local HTTP server (Python, Node.js, or VS Code Live Server)

## Contributing

When updating protocol implementations:
1. Update corresponding visualization
2. Verify timing/flow matches documentation
3. Test both Python and JavaScript versions
4. Update this README if flow changes
