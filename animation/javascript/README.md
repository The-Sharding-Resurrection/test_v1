# Interactive Protocol Visualizations

Browser-based interactive visualizations comparing Baseline and 2PC cross-shard protocols.

## Quick Start

```bash
# Navigate to this directory
cd animation/javascript

# Start a local web server (choose one):

# Option 1: Python 3
python -m http.server 8000

# Option 2: Python 2
python -m SimpleHTTPServer 8000

# Option 3: Node.js (npx http-server)
npx http-server -p 8000

# Option 4: VS Code Live Server extension
# Right-click index.html → "Open with Live Server"
```

Then open http://localhost:8000/index.html in your browser.

## Features

### Three Visualization Modes

1. **Baseline Protocol Tab**
   - Hop-based iterative execution
   - NoStateError detection
   - Progressive RwSet accumulation
   - Sequential routing through orchestrator

2. **2PC Protocol Tab**
   - Upfront EVM simulation
   - State fetching from all shards
   - Two-phase commit (PREPARE → COMMIT)
   - Atomic finalization

3. **Side-by-Side Comparison**
   - Synchronized playback
   - Real-time latency comparison
   - Metrics table showing differences

### Interactive Controls

- **Play** - Start animation from current state
- **Pause** - Pause animation
- **Step** - Advance one phase at a time
- **Reset** - Return to initial state
- **Speed Slider** - Adjust animation speed (0.5x - 2.0x)

### Visual Elements

- **Node Colors:**
  - Green: Idle
  - Yellow: Executing
  - Orange: Locked (PREPARE phase)
  - Blue: Simulating (2PC only)
  - Bright Green: Success

- **Lock Icons (🔒):** Indicate locked state during PREPARE
- **Status Badges:** Show voting status (YES, PENDING, SUCCESS)
- **Message Arrows:** Show communication between nodes

## Example Transaction

All visualizations demonstrate the **TravelAgency** scenario:

```
User submits: TravelAgency.bookTrainAndHotel()
  └─> Train.checkSeat() (Shard B)
      └─> Hotel.checkRoom() (Shard C)
```

**Baseline:** 3 hops (A → B → C) ≈ 10 seconds
**2PC:** 2 rounds (Prepare + Commit) ≈ 6 seconds

## Technical Details

### File Structure

```
javascript/
├── index.html          # Main page with navigation
├── styles.css          # Shared styles
├── baseline.js         # Baseline protocol logic
├── twopc.js            # 2PC protocol logic
├── comparison.js       # Side-by-side comparison
└── README.md           # This file
```

### Dependencies

- **D3.js v7** - Loaded from CDN in index.html
- No build step required
- Works in all modern browsers

### State Machines

**Baseline States:**
```
IDLE → HOP_0_EXECUTE → HOP_0_FORWARD →
HOP_1_EXECUTE → HOP_1_FORWARD →
HOP_2_EXECUTE → HOP_2_NOTIFY → COMPLETE
```

**2PC States:**
```
IDLE → SUBMIT → SIMULATION →
PREPARE_BROADCAST → PREPARE_VOTE →
COMMIT_BROADCAST → COMPLETE
```

## Customization

### Adjusting Timing

Edit the state machine methods in `baseline.js` or `twopc.js`:

```javascript
async startHop0() {
    this.timeElapsed += 1;  // Change this value
    // ...
}
```

### Adding More Shards

Modify the `initNetwork()` method to add additional shard nodes:

```javascript
const shardPositions = [
    { id: 'shard-a', label: 'Shard A', angle: -90 },
    { id: 'shard-b', label: 'Shard B', angle: -30 },
    { id: 'shard-c', label: 'Shard C', angle: 30 },
    { id: 'shard-d', label: 'Shard D', angle: 90 },  // New shard
];
```

### Changing Colors

Edit color constants in the `getNodeColor()` methods:

```javascript
getNodeColor(node) {
    if (node.status === 'executing') return '#YOUR_COLOR';
    // ...
}
```

## Troubleshooting

### Visualizations not loading
- Check browser console for errors
- Ensure D3.js CDN is accessible
- Try a different browser (Chrome, Firefox recommended)

### Animations not smooth
- Lower animation speed
- Close other browser tabs
- Check system resources

### Controls not working
- Make sure JavaScript is enabled
- Check for console errors
- Try refreshing the page

## Browser Compatibility

- ✅ Chrome 90+
- ✅ Firefox 88+
- ✅ Safari 14+
- ✅ Edge 90+

## Deployment

### GitHub Pages

1. Commit all files to git
2. Enable GitHub Pages in repository settings
3. Select branch and `/animation/javascript` folder
4. Access via: `https://username.github.io/repo/animation/javascript/`

### Static Hosting

Upload all files to any static hosting service:
- Netlify
- Vercel
- AWS S3 + CloudFront
- Cloudflare Pages

## Performance

- Optimized for 60 FPS animations
- Minimal DOM manipulation
- No external API calls
- Works offline after initial load

## Future Enhancements

Potential improvements:
- [ ] Hover tooltips with detailed state info
- [ ] Export animation as GIF/video
- [ ] Real-time mode (connect to live network)
- [ ] Custom transaction builder
- [ ] Error scenario visualization (vote rejection)
- [ ] Multi-hop baseline (5+ hops)
- [ ] Timeline scrubbing (seek to specific time)

## Contributing

To modify the visualizations:

1. Edit the relevant JS file (`baseline.js`, `twopc.js`, or `comparison.js`)
2. Refresh browser to see changes (no build step)
3. Test in multiple browsers
4. Update this README if adding new features

## Related Files

- **Python/Manim animations:** `../python/`
- **Documentation:** `../../docs/`
- **Protocol specs:**
  - `../../docs/baseline-protocol.md`
  - `../../docs/2pc-protocol.md`
