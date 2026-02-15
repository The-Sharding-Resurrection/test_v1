# Quick Start Guide

Get started with protocol visualizations in 5 minutes.

## Prerequisites

You need **either** Python (for videos) **or** a web browser (for interactive demos). Or both!

### For Python/Manim Videos

```bash
# Install Manim
pip install -r animation/python/requirements.txt
```

### For JavaScript/D3.js Interactive Demos

Just a web browser and a simple HTTP server (Python, Node, or VS Code).

## Option 1: Watch Videos (Python/Manim)

### Step 1: Generate Your First Video

```bash
cd animation/python
manim -pql baseline_final.py BaselineProtocol
```

This will:
- Render a 720p preview video (~30 seconds)
- Automatically open the video when complete
- Show the Baseline protocol animation (60 seconds)

### Step 2: Generate All Videos

```bash
# High-quality 1080p videos for presentations
manim -pqh baseline_final.py BaselineProtocol
manim -pqh twopc_final.py TwoPCProtocol
manim -pqh comparison.py Comparison
```

**Output location:** `animation/python/media/videos/`

**File sizes:** ~5-15 MB each

### Step 3: Copy to Output Directory

```bash
cd ../..  # Back to project root
mkdir -p animation/output
cp animation/python/media/videos/*/*.mp4 animation/output/
```

**Done!** You now have three MP4 videos ready for presentations.

## Option 2: Interactive Demos (JavaScript/D3.js)

### Step 1: Start Local Server

```bash
cd animation/javascript

# Choose one:
python -m http.server 8000          # Python 3
python -m SimpleHTTPServer 8000     # Python 2
npx http-server -p 8000             # Node.js
```

### Step 2: Open in Browser

Navigate to: **http://localhost:8000/index.html**

### Step 3: Explore

Try each tab:

1. **Baseline Protocol** - Click "Play" to watch hop-based execution
2. **2PC Protocol** - Click "Play" to watch simulation-based commit
3. **Side-by-Side** - Click "Play" to compare both protocols

**Controls:**
- **Play/Pause** - Start/stop animation
- **Step** - Advance one phase at a time
- **Reset** - Return to initial state
- **Speed Slider** - Adjust animation speed

**Done!** You have a fully interactive protocol comparison.

## What You'll See

### TravelAgency Example

Both visualizations show the same scenario:

```
User calls: TravelAgency.bookTrainAndHotel()
  └─> Shard A (Agency) → Shard B (Train) → Shard C (Hotel)
```

### Baseline Protocol (Hop-Based)

**Flow:**
1. **Hop 0:** Shard A executes → NoStateError → Forward to Orchestrator
2. **Hop 1:** Shard B executes → NoStateError → Forward to Orchestrator
3. **Hop 2:** Shard C executes → Success → Finalize all shards

**Result:** 3 hops, ~10 seconds total

**Key Feature:** Progressive RwSet accumulation

### 2PC Protocol (Simulation-Based)

**Flow:**
1. **Simulation:** Orchestrator fetches state, runs EVM, discovers RwSet
2. **PREPARE:** Broadcast to all shards → Lock state → Collect votes
3. **COMMIT:** Broadcast decision → Atomic finalization

**Result:** 2 rounds, ~6 seconds total

**Key Feature:** Atomic guarantee across all shards

## Comparison Summary

| Metric | Baseline | 2PC |
|--------|----------|-----|
| Latency | ~10s | ~6s |
| Hops | 3 | 2 rounds |
| Orchestrator | Stateless | Stateful |
| Complexity | Low | High |
| Atomicity | Sequential | Guaranteed |

## Next Steps

### For Presentations

1. Use **MP4 videos** in slides (PowerPoint, Google Slides, Keynote)
2. High quality: `animation/output/baseline.mp4`, `twopc.mp4`, `comparison.mp4`
3. Embed directly or link to GitHub

### For Documentation

1. Link to **JavaScript demos** in README.md
2. Deploy to GitHub Pages for public access
3. Embed screenshots in markdown docs

### For Research

1. Reference visualizations in papers
2. Use comparison metrics table
3. Cite protocol documentation

### For Development

1. Read `animation/TESTING.md` for testing guide
2. Modify animations to match code changes
3. Update when protocols evolve

## Troubleshooting

### Python: "manim: command not found"

```bash
pip install --upgrade manim
# Or check: pip show manim
```

### JavaScript: Page won't load

```bash
# Try a different port
python -m http.server 8001

# Check if port is in use
lsof -i :8000  # Linux/Mac
netstat -ano | findstr :8000  # Windows
```

### Videos: Low quality

```bash
# Use high quality flag
manim -pqh baseline_final.py BaselineProtocol

# Quality flags:
# -pql = 720p (preview)
# -pqm = 1080p
# -pqh = 1080p (high quality)
# -pqk = 4K
```

### Animations: Stuttering

- Lower speed slider (try 0.5x)
- Close other browser tabs
- Use Chrome or Firefox
- Disable browser extensions

## Getting Help

1. **Testing issues?** → Read `animation/TESTING.md`
2. **Usage questions?** → Read `animation/README.md`
3. **JavaScript issues?** → Read `animation/javascript/README.md`
4. **Protocol accuracy?** → Check `docs/2pc-protocol.md` and `docs/baseline-protocol.md`

## Quick Commands Reference

```bash
# Verify setup
./animation/verify_setup.sh

# Python: Generate preview (fast)
cd animation/python
manim -pql baseline_final.py BaselineProtocol

# Python: Generate production (high quality)
manim -pqh twopc_final.py TwoPCProtocol

# Python: Generate all videos
manim -pqh *.py

# JavaScript: Start server
cd animation/javascript
python -m http.server 8000

# JavaScript: Open in browser
# http://localhost:8000/index.html
```

## What's Next?

### Immediate:
- [ ] Generate all three videos in high quality
- [ ] Test interactive demos in browser
- [ ] Share with team for feedback

### Soon:
- [ ] Deploy JavaScript demos to GitHub Pages
- [ ] Update protocol documentation with video links
- [ ] Use in presentations/papers

### Later:
- [ ] Add error scenarios (vote rejection)
- [ ] Visualize additional protocols
- [ ] Create mobile app version

## Success Criteria

You're ready to use the visualizations when:

✅ All three Python videos render without errors
✅ JavaScript demos load and play smoothly
✅ You understand the Baseline vs 2PC differences
✅ Timing matches expected values (10s vs 6s)

---

**Time to get started:** 5 minutes
**Total implementation:** Complete
**Status:** Ready for use ✅

Enjoy visualizing cross-shard protocols!
