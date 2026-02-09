# Install Manim and Generate Videos

Since I need sudo access to install system dependencies, please run these commands:

## Quick Install (1 command)

```bash
cd /mnt/c/Users/USER/Desktop/ooo/sharding
bash animation/install_manim.sh
```

This will:
1. Install system packages (pkg-config, cairo, pango, ffmpeg)
2. Install manim in your ~/.fullpython virtual environment
3. Verify the installation

**Time:** ~2-3 minutes

## Manual Install (if script doesn't work)

```bash
# Install system dependencies
sudo apt-get update
sudo apt-get install -y pkg-config libcairo2-dev libpango1.0-dev ffmpeg

# Install manim
~/.fullpython/bin/pip install manim

# Verify
~/.fullpython/bin/manim --version
```

## Generate Videos (after installation)

### Quick Preview (720p, ~30 seconds each)

```bash
cd animation/python

# Baseline protocol
~/.fullpython/bin/manim -pql baseline_protocol.py BaselineProtocol

# 2PC protocol
~/.fullpython/bin/manim -pql twopc_protocol.py TwoPCProtocol

# Comparison
~/.fullpython/bin/manim -pql comparison.py Comparison
```

### High Quality (1080p, ~2 minutes each)

```bash
cd animation/python

# Generate all three in high quality
~/.fullpython/bin/manim -pqh baseline_protocol.py BaselineProtocol
~/.fullpython/bin/manim -pqh twopc_protocol.py TwoPCProtocol
~/.fullpython/bin/manim -pqh comparison.py Comparison
```

## Output Location

Videos will be saved to:
```
animation/python/media/videos/baseline_protocol/1080p60/BaselineProtocol.mp4
animation/python/media/videos/twopc_protocol/1080p60/TwoPCProtocol.mp4
animation/python/media/videos/comparison/1080p60/Comparison.mp4
```

## Copy to Output Directory

```bash
mkdir -p animation/output
cp animation/python/media/videos/*/*.mp4 animation/output/
```

## Troubleshooting

**If installation fails:**
- Make sure you have sudo access
- Check internet connection
- Try: `sudo apt-get update` first

**If videos don't auto-open (WSL issue):**
- Use `-q` instead of `-pql` flag
- Open videos manually from `media/videos/` directory

**If rendering is slow:**
- Use `-ql` (low quality) for faster preview
- Close other applications
- Be patient - first render is always slower

## Expected Results

- **Baseline video:** 60 seconds, shows 3 hops (A → B → C)
- **2PC video:** 60 seconds, shows simulation + 2PC phases
- **Comparison video:** 90 seconds, side-by-side comparison

All videos at 60 FPS, 1080p high quality.
