# Testing Protocol Visualizations

This guide helps you verify that both Python/Manim and JavaScript/D3.js visualizations work correctly.

## Prerequisites

### Python/Manim
```bash
pip install -r animation/python/requirements.txt
```

**System requirements:**
- Python 3.8+
- FFmpeg (usually installed with Manim)
- Optional: LaTeX for math rendering

### JavaScript/D3.js
- Modern web browser (Chrome, Firefox, Safari, Edge)
- Local HTTP server (Python, Node.js, or VS Code Live Server)

## Testing Python/Manim Animations

### Test 1: Baseline Protocol (Quick Preview)

```bash
cd animation/python
manim -pql baseline_protocol.py BaselineProtocol
```

**Expected output:**
- Opens video player with 60-second animation
- Shows 3 hops: Shard A → B → C
- NoStateError messages visible
- RwSet accumulation displayed
- Total time: ~10 seconds shown

**Verification checklist:**
- [ ] Network topology rendered (orchestrator + shards)
- [ ] Hop 0: Shard A turns yellow, error shown
- [ ] Hop 1: Shard B executes, another error
- [ ] Hop 2: Shard C completes successfully
- [ ] All shards finalize (turn green)
- [ ] Timeline shows ~10 seconds

### Test 2: 2PC Protocol (Quick Preview)

```bash
manim -pql twopc_protocol.py TwoPCProtocol
```

**Expected output:**
- Opens video player with 60-second animation
- Shows simulation phase, then PREPARE, then COMMIT
- Vote collection visible
- Total time: ~6 seconds shown

**Verification checklist:**
- [ ] Network topology rendered
- [ ] Orchestrator turns blue (simulating)
- [ ] State fetch from all shards
- [ ] PREPARE broadcast to A, B, C
- [ ] All shards lock and vote YES
- [ ] COMMIT broadcast
- [ ] Atomic finalization (all shards green simultaneously)
- [ ] Timeline shows ~6 seconds

### Test 3: Side-by-Side Comparison (Quick Preview)

```bash
manim -pql comparison.py Comparison
```

**Expected output:**
- Split-screen animation (90 seconds)
- Left: Baseline (yellow theme)
- Right: 2PC (blue theme)
- Synchronized timeline at bottom

**Verification checklist:**
- [ ] Both networks visible side-by-side
- [ ] Timeline shows different completion times
- [ ] Comparison table appears at end
- [ ] Metrics match: Baseline ~10s, 2PC ~6s

### Test 4: High-Quality Renders (Production)

Generate 1080p videos for presentations:

```bash
# Baseline (high quality)
manim -pqh baseline_protocol.py BaselineProtocol

# 2PC (high quality)
manim -pqh twopc_protocol.py TwoPCProtocol

# Comparison (high quality)
manim -pqh comparison.py Comparison
```

**Output location:** `animation/python/media/videos/`

**Expected:**
- 1080p MP4 files
- Smooth 60 FPS playback
- Crisp text rendering
- File sizes: ~5-15 MB each

### Test 5: All Animations at Once

```bash
cd animation/python
manim -pqh baseline_protocol.py twopc_protocol.py comparison.py
```

Renders all three videos in high quality.

## Testing JavaScript/D3.js Visualizations

### Test 1: Start Local Server

```bash
cd animation/javascript

# Option 1: Python
python -m http.server 8000

# Option 2: VS Code Live Server
# Right-click index.html → "Open with Live Server"
```

### Test 2: Open in Browser

Navigate to: http://localhost:8000/index.html

**Expected:**
- Page loads with header "Cross-Shard Protocol Visualization"
- Three tabs visible: Baseline | 2PC | Side-by-Side
- Baseline tab active by default

### Test 3: Baseline Protocol Interactive

**On Baseline tab:**

1. Click **Play** button
   - [ ] Animation starts automatically
   - [ ] Shard A turns yellow (executing)
   - [ ] Messages appear between nodes
   - [ ] State info updates in panel
   - [ ] Timeline advances

2. Click **Pause**
   - [ ] Animation stops
   - [ ] Can resume with Play

3. Click **Step**
   - [ ] Advances one phase at a time
   - [ ] State display updates

4. Adjust **Speed Slider**
   - [ ] Animation speed changes (0.5x - 2.0x)
   - [ ] Display shows current speed

5. Click **Reset**
   - [ ] Returns to initial state
   - [ ] All nodes green (idle)
   - [ ] Time resets to 0s

**Verification checklist:**
- [ ] 3 hops visible: A → B → C
- [ ] Lock icons appear on nodes
- [ ] Status badges show PENDING/SUCCESS
- [ ] RwSet displayed in state panel
- [ ] Total time: ~10 seconds

### Test 4: 2PC Protocol Interactive

**On 2PC tab:**

1. Click **Play**
   - [ ] Orchestrator turns blue (simulating)
   - [ ] State fetch messages to all shards
   - [ ] PREPARE broadcast
   - [ ] All shards vote YES
   - [ ] COMMIT phase
   - [ ] Atomic finalization

**Verification checklist:**
- [ ] Simulation phase visible
- [ ] RwSet discovery shown
- [ ] Vote collection: 3/3 YES
- [ ] Atomic commit (all shards green at once)
- [ ] Total time: ~6 seconds

### Test 5: Side-by-Side Comparison

**On Comparison tab:**

1. Click **Play**
   - [ ] Both sides animate simultaneously
   - [ ] Left: Baseline (yellow theme)
   - [ ] Right: 2PC (blue theme)
   - [ ] Time displays update independently
   - [ ] 2PC finishes first (~6s vs ~10s)

2. Check **Metrics Table**
   - [ ] Table visible at bottom
   - [ ] Shows 5 comparison metrics
   - [ ] Values match expected (10s vs 6s, etc.)

**Verification checklist:**
- [ ] Synchronized playback
- [ ] Different timing visible
- [ ] Comparison table accurate
- [ ] Both protocols complete successfully

### Test 6: Cross-Browser Testing

Test in multiple browsers:

- [ ] **Chrome** - Primary target
- [ ] **Firefox** - Should work identically
- [ ] **Safari** - Check on macOS
- [ ] **Edge** - Check on Windows

**Common issues:**
- D3.js not loading: Check console, verify CDN access
- Animations choppy: Lower speed, close other tabs
- Controls not working: Enable JavaScript

## Validation Against Documentation

### Protocol Accuracy Check

Compare animations to documentation:

1. **Baseline Protocol**
   - Read: `docs/baseline-protocol.md`
   - Verify: 3 hops match spec
   - Verify: NoStateError detection accurate
   - Verify: RwSet accumulation correct

2. **2PC Protocol**
   - Read: `docs/2pc-protocol.md` (lines 263-289)
   - Verify: Simulation phase matches
   - Verify: Vote aggregation correct
   - Verify: PREPARE → COMMIT flow accurate

3. **Timing Comparison**
   - Baseline: 3 hops × ~3s = 9-10s ✓
   - 2PC: Simulation (3s) + Prepare (2s) + Commit (1s) = 6s ✓

### Code Integration Check

Verify animations match actual implementation:

```bash
# Check baseline implementation
grep -r "NoStateError" internal/shard/baseline_*.go

# Check 2PC implementation
grep -r "TpcResult" internal/orchestrator/chain.go
grep -r "RwSetRequest" internal/orchestrator/simulator.go
```

**Expected:**
- Baseline: NoStateError detection in `baseline_evm.go`
- 2PC: TpcResult in `chain.go`, RwSetRequest in `simulator.go`

## Troubleshooting

### Python/Manim Issues

**Problem:** `manim: command not found`
```bash
pip install --upgrade manim
# Or with conda:
conda install -c conda-forge manim
```

**Problem:** LaTeX errors during rendering
```bash
# Disable LaTeX temporarily
manim -pql --disable_caching baseline_protocol.py BaselineProtocol
```

**Problem:** Video doesn't open automatically
```bash
# Just generate, don't play
manim -ql baseline_protocol.py BaselineProtocol
# Then open manually from media/videos/
```

### JavaScript Issues

**Problem:** Page not loading
- Check if server is running: `curl http://localhost:8000`
- Try different port: `python -m http.server 8001`
- Check firewall settings

**Problem:** D3.js not loading
- Check browser console for errors
- Try downloading D3.js locally:
  ```bash
  curl https://d3js.org/d3.v7.min.js > d3.min.js
  # Update index.html to use local copy
  ```

**Problem:** Animations stuttering
- Lower speed slider
- Close other browser tabs
- Disable browser extensions
- Try in private/incognito mode

## Performance Benchmarks

Expected rendering times (on moderate hardware):

**Python/Manim:**
- Baseline (720p): ~30 seconds
- Baseline (1080p): ~2 minutes
- 2PC (720p): ~30 seconds
- 2PC (1080p): ~2 minutes
- Comparison (720p): ~45 seconds
- Comparison (1080p): ~3 minutes

**JavaScript/D3.js:**
- Initial load: < 1 second
- Animation playback: 60 FPS (real-time)
- Memory usage: < 50 MB

## Success Criteria

All tests pass if:

✅ **Python/Manim:**
1. All three animations render without errors
2. Videos play smoothly at 60 FPS
3. Protocol flows match documentation
4. Timing matches expected values
5. Output files created in `media/videos/`

✅ **JavaScript/D3.js:**
1. Page loads in all major browsers
2. All three tabs functional
3. Play/Pause/Step/Reset controls work
4. Speed adjustment works
5. Animations are smooth
6. State displays update correctly
7. Comparison shows correct timing differences

## Next Steps After Testing

Once all tests pass:

1. **Copy videos to output directory:**
   ```bash
   mkdir -p animation/output
   cp animation/python/media/videos/*/*.mp4 animation/output/
   ```

2. **Update documentation with video links:**
   - `docs/2pc-protocol.md`
   - `docs/baseline-protocol.md`
   - Root `README.md`

3. **Deploy JavaScript visualizations:**
   - GitHub Pages
   - Or any static hosting service

4. **Share with team:**
   - Link to hosted visualizations
   - Embed videos in presentations
   - Reference in papers/reports

## Feedback and Issues

If you encounter issues:

1. Check this testing guide first
2. Review `animation/README.md` for usage instructions
3. Check individual READMEs in `python/` and `javascript/`
4. Review documentation in `docs/`
5. Open GitHub issue if bug found

## Continuous Testing

When updating protocols:

1. **Code changes** → Update animations
2. **Run tests** → Verify accuracy
3. **Update docs** → Keep in sync
4. **Re-deploy** → Push to hosting

This ensures visualizations always reflect current implementation.
