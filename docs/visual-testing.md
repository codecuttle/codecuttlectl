# Visual Testing Harness

## Overview

The Visual Testing Harness provides end-to-end testing for codecuttlectl's TUI by running it inside a virtual display, capturing frames, and analyzing visual output for layout bugs, rendering issues, and interaction correctness.

This enables the agent to **see its own TUI** — launch it, type into it, observe the visual result, identify rendering problems, and verify fixes — all without a human watching the screen.

## Problem Statement

TUI bugs are uniquely difficult to debug programmatically:

- **Layout issues** (text overflow, viewport jumping) are invisible in unit tests
- **Streaming behavior** (content appearing character-by-character) creates temporal visual artifacts
- **Terminal resize** interactions can't be tested without a real PTY
- **Scrolling behavior** depends on actual rendered line heights, not string lengths
- The Bubble Tea framework renders via ANSI escape sequences — the "output" is meaningless without a terminal to interpret it

Traditional approaches fail:
- Unit tests verify data flow, not visual correctness
- Golden-file testing of raw ANSI output is brittle and unreadable
- Manual testing requires a human to watch and report

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Host (Coder workspace)                     │
│                                                              │
│  ┌────────────────────────────────────────────────────────┐  │
│  │              Docker Container                           │  │
│  │                                                        │  │
│  │  ┌──────────┐    ┌───────────┐    ┌──────────────┐   │  │
│  │  │   Xvfb   │◀──│  Terminal  │◀──│  codecuttlectl│   │  │
│  │  │ (display) │   │ (alacritty│    │  (c3 TUI)    │   │  │
│  │  │ :99      │   │  or xterm) │    │              │   │  │
│  │  └────┬─────┘   └───────────┘    └──────────────┘   │  │
│  │       │                                               │  │
│  │       ├──────────┐                                    │  │
│  │       │          │                                    │  │
│  │  ┌────▼─────┐  ┌─▼──────────┐                       │  │
│  │  │ x11vnc   │  │  ffmpeg    │                        │  │
│  │  │ (VNC)    │  │ (x11grab)  │                        │  │
│  │  └────┬─────┘  └─────┬──────┘                        │  │
│  │       │               │                               │  │
│  └───────┼───────────────┼───────────────────────────────┘  │
│          │               │                                   │
│     ┌────▼─────┐   ┌────▼──────────┐                       │
│     │  noVNC   │   │  Frame store  │                        │
│     │ (web UI) │   │  /tmp/frames/ │                        │
│     │ :6080    │   │  PNG sequence │                        │
│     └──────────┘   └───────┬───────┘                        │
│                             │                                │
│                    ┌────────▼────────┐                       │
│                    │  Analysis Agent │                       │
│                    │  (reads frames, │                       │
│                    │   detects bugs) │                       │
│                    └─────────────────┘                       │
└─────────────────────────────────────────────────────────────┘
```

## Components

### 1. Virtual Display (Xvfb)

A headless X11 framebuffer that provides a "screen" without physical hardware.

```bash
Xvfb :99 -screen 0 1920x1080x24 -ac &
export DISPLAY=:99
```

Configurable resolution. Default: 1920x1080 (matches common dev screens).

### 2. Terminal Emulator

A real terminal emulator running inside the virtual display. This is critical — it interprets ANSI escape sequences, handles cursor positioning, and provides the PTY that Bubble Tea needs.

**Options:**
- `alacritty` — GPU-accelerated, fast, good Unicode support
- `xterm` — Lightweight, universally available, reliable
- `kitty` — Modern, good rendering, supports image display

The terminal must be configurable:
- Fixed size (columns × rows) for reproducible tests
- Known font / cell dimensions for pixel-accurate assertions
- Scrollback buffer for history verification

### 3. Input Injection (xdotool)

Simulates keyboard and mouse input to the focused terminal window.

```bash
# Type text
xdotool type --delay 50 "Hello, world"

# Press special keys
xdotool key Return
xdotool key ctrl+c
xdotool key ctrl+t

# Mouse operations
xdotool mousemove 500 300
xdotool click 1  # left click
xdotool click 4  # scroll up
xdotool click 5  # scroll down
```

### 4. Frame Capture

Two modes of capture:

**Screenshot mode** — capture a single frame at a point in time:
```bash
import -window root -display :99 /tmp/frames/frame_001.png
# or
scrot -d 0 /tmp/frames/frame_001.png
```

**Recording mode** — continuous capture as video or frame sequence:
```bash
# Video (for human review)
ffmpeg -f x11grab -video_size 1920x1080 -i :99 -c:v libx264 -preset fast output.mp4

# Frame sequence (for agent analysis)
ffmpeg -f x11grab -video_size 1920x1080 -i :99 -vf fps=4 /tmp/frames/frame_%04d.png
```

Frame rate of 4fps is sufficient for TUI analysis (changes happen on ~100ms timescale).

### 5. noVNC (Human Observation)

Optional web-based VNC viewer for when a human wants to watch the automated test run in real-time.

```bash
x11vnc -display :99 -forever -shared -nopw &
websockify --web /usr/share/novnc 6080 localhost:5900 &
# Access at http://localhost:6080/vnc.html
```

### 6. Analysis Agent

The agent (codecuttlectl itself, or a separate analysis script) reads captured frames and evaluates:

- **Layout correctness**: Is text within terminal bounds?
- **Stability**: Are consecutive frames stable when no input is occurring?
- **Scroll behavior**: Does viewport scroll smoothly or jump?
- **Content integrity**: Is rendered text readable and properly wrapped?

Frame analysis can use:
- **OCR** (tesseract) for text extraction from screenshots
- **Pixel diff** between consecutive frames to detect unexpected changes
- **Image hashing** to detect visual regression vs. known-good baselines
- **Direct model analysis** — send frames to Claude/GPT-4V for visual reasoning

## Test Scenarios

### Scenario 1: Text Overflow Detection

```yaml
name: text_overflow
steps:
  - launch_tui:
      terminal_size: "80x24"
  - type: "Tell me about the history of computing in great detail"
  - wait_for_response: true
  - capture_frame: "after_response"
  - assert:
      - no_text_beyond_column_80
      - all_lines_visible
      - no_horizontal_scroll_indicator
```

### Scenario 2: Viewport Stability During Streaming

```yaml
name: viewport_stability
steps:
  - launch_tui:
      terminal_size: "120x40"
  - type: "Write me a long poem"
  - start_recording:
      fps: 10
      duration: 30s
  - wait_for_done: true
  - stop_recording
  - analyze:
      - frame_diff_threshold: 0.05  # No more than 5% of pixels should change between adjacent frames when no new content arrives
      - no_viewport_jumps: true     # Content position should only move downward monotonically
```

### Scenario 3: Input While Streaming

```yaml
name: input_during_stream
steps:
  - launch_tui:
      terminal_size: "100x30"
  - type: "Count to 100 slowly"
  - wait: 2s  # Let streaming start
  - start_recording:
      fps: 10
  - type_slowly: "hello"  # Type while response is streaming
  - wait_for_done: true
  - stop_recording
  - analyze:
      - input_area_stable: true   # Input text should not jump
      - viewport_no_flicker: true # Viewport shouldn't flash/jump from typing
```

### Scenario 4: Terminal Resize

```yaml
name: resize_reflow
steps:
  - launch_tui:
      terminal_size: "120x40"
  - type: "What is Rust?"
  - wait_for_response: true
  - capture_frame: "before_resize"
  - resize_terminal: "80x24"
  - wait: 500ms
  - capture_frame: "after_resize"
  - assert:
      - content_reflowed_correctly
      - no_text_cutoff
      - viewport_at_bottom
```

### Scenario 5: Long Conversation Scroll

```yaml
name: long_conversation_scroll
steps:
  - launch_tui:
      terminal_size: "100x30"
  - repeat: 5
    steps:
      - type: "Tell me a short joke"
      - wait_for_response: true
  - capture_frame: "end_of_conversation"
  - scroll_up: 10
  - capture_frame: "scrolled_up"
  - scroll_down: 10
  - capture_frame: "scrolled_back"
  - assert:
      - scroll_up_shows_earlier_content
      - scroll_down_returns_to_bottom
      - no_content_corruption
```

## Docker Image

### Dockerfile

```dockerfile
FROM ubuntu:24.04

# Avoid interactive prompts during build
ENV DEBIAN_FRONTEND=noninteractive

# Core display and capture tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    xvfb \
    x11vnc \
    xdotool \
    xterm \
    imagemagick \
    ffmpeg \
    scrot \
    novnc \
    websockify \
    fonts-dejavu-core \
    fonts-noto-mono \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Optional: tesseract for OCR-based assertions
RUN apt-get update && apt-get install -y --no-install-recommends \
    tesseract-ocr \
    && rm -rf /var/lib/apt/lists/*

# Install Go (for building codecuttlectl inside the container)
RUN curl -fsSL https://go.dev/dl/go1.25.0.linux-amd64.tar.gz | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:${PATH}"

# Working directory
WORKDIR /app

# Copy source and build
COPY . /app/
RUN make all

# Startup script
COPY scripts/visual-test-entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENV DISPLAY=:99
ENV TERM=xterm-256color

ENTRYPOINT ["/entrypoint.sh"]
```

### Entrypoint Script

```bash
#!/bin/bash
set -e

# Start virtual display
Xvfb :99 -screen 0 ${SCREEN_WIDTH:-1920}x${SCREEN_HEIGHT:-1080}x24 -ac &
sleep 1

# Start VNC server (optional, for human observation)
if [ "${ENABLE_VNC:-true}" = "true" ]; then
    x11vnc -display :99 -forever -shared -nopw -rfbport 5900 &
    websockify --web /usr/share/novnc 6080 localhost:5900 &
fi

# Create frame output directory
mkdir -p /tmp/frames

# Execute the test command (or drop to shell)
exec "$@"
```

## Test Runner

The test runner is a Go program (or shell script) that orchestrates:

1. Launches the terminal emulator at a specified size
2. Starts codecuttlectl inside it
3. Executes the test scenario (typing, waiting, capturing)
4. Collects frames and runs assertions
5. Reports pass/fail with visual evidence

### Go Test Integration

```go
// internal/tui/visual_test.go (build tag: visual)

//go:build visual

package tui_test

import (
    "testing"
    "github.com/codecuttle/codecuttlectl/internal/visualtest"
)

func TestViewportStability(t *testing.T) {
    env := visualtest.NewEnvironment(t, visualtest.Config{
        Cols: 100, Rows: 30,
    })
    defer env.Close()

    env.LaunchTUI()
    env.Type("Tell me about Go generics")
    env.WaitForResponse(30 * time.Second)

    // Capture 5 seconds of "idle" frames after response completes
    frames := env.RecordFrames(5*time.Second, 4) // 4 fps

    // Assert no frame differs from its predecessor by more than 1%
    // (accounts for cursor blink)
    for i := 1; i < len(frames); i++ {
        diff := visualtest.PixelDiff(frames[i-1], frames[i])
        if diff > 0.01 {
            t.Errorf("frame %d→%d: %.2f%% pixel diff (threshold 1%%)", i-1, i, diff*100)
            visualtest.SaveEvidence(t, frames[i-1], frames[i])
        }
    }
}
```

## Frame Analysis Techniques

### 1. Pixel Diff (Stability)

Compare consecutive frames pixel-by-pixel. A stable viewport should show ≤1% change (cursor blink). Jumps show as 20%+ diff.

```go
func PixelDiff(a, b image.Image) float64 {
    bounds := a.Bounds()
    total := bounds.Dx() * bounds.Dy()
    changed := 0
    for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
        for x := bounds.Min.X; x < bounds.Max.X; x++ {
            if a.At(x, y) != b.At(x, y) {
                changed++
            }
        }
    }
    return float64(changed) / float64(total)
}
```

### 2. Row-Based Content Tracking (Scroll Detection)

Slice the frame into rows (based on known cell height). Hash each row. Between frames, track which rows moved where. Legitimate scrolling shifts all rows by the same offset. A "jump" is when rows shuffle non-monotonically.

### 3. OCR Extraction (Content Verification)

Use tesseract to extract text from a frame region. Verify that expected content is present and properly positioned.

```bash
tesseract /tmp/frames/frame_042.png stdout --psm 6
```

### 4. Model-Based Visual Analysis

Send frames directly to a vision-capable model for subjective analysis:

```
"Here are frames 40-45 from the TUI. The user typed 'hello' between
frames 42 and 43. Is the viewport stable? Does the input area show
the typed text correctly? Is any content cut off?"
```

This is the most powerful approach for detecting subtle visual issues that rule-based checks miss.

## Implementation Plan

| Phase | What | Dependencies |
|-------|------|-------------|
| 1 | Dockerfile + entrypoint + docker-compose | None |
| 2 | `scripts/visual-test.sh` — manual test runner (launch, type, capture) | Phase 1 |
| 3 | `internal/visualtest/` Go package — programmatic environment control | Phase 1 |
| 4 | Pixel diff + stability assertions | Phase 3 |
| 5 | Go test integration (`go test -tags visual ./internal/tui/`) | Phase 3, 4 |
| 6 | OCR-based content assertions | Phase 3, tesseract |
| 7 | CI integration (GitHub Actions with Docker) | Phase 5 |
| 8 | Model-based visual analysis (send frames to Claude) | Phase 3, Bedrock |

## Usage

### Running Tests Locally

```bash
# Build the visual test container
docker compose -f docker-compose.visual.yml build

# Run all visual tests
docker compose -f docker-compose.visual.yml run --rm visual-test go test -tags visual ./internal/tui/

# Run a specific scenario
docker compose -f docker-compose.visual.yml run --rm visual-test ./scripts/visual-test.sh viewport_stability

# Watch in real-time via noVNC (start container, open browser)
docker compose -f docker-compose.visual.yml up -d
# Open http://localhost:6080/vnc.html
```

### Manual Debugging

```bash
# Start the environment and drop to a shell
docker compose -f docker-compose.visual.yml run --rm visual-test bash

# Inside the container:
xterm -geometry 100x30 &  # Launch terminal
# Manually run c3 inside xterm, observe via noVNC
```

## Design Principles

1. **Reproducible** — Fixed terminal size, fixed font, fixed display resolution. Same test = same pixel output.
2. **Agent-accessible** — All operations (launch, type, capture, analyze) are scriptable. No human needed in the loop.
3. **Evidence-preserving** — Failed tests save the frame sequence as PNGs for post-mortem analysis.
4. **Fast feedback** — Individual scenarios complete in <30 seconds. Full suite <5 minutes.
5. **Layered assertions** — Pixel diff for stability, OCR for content, model for subjective quality.
6. **No flakiness** — Wait for explicit signals (response complete, idle state) rather than fixed timeouts where possible.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SCREEN_WIDTH` | 1920 | Virtual display width |
| `SCREEN_HEIGHT` | 1080 | Virtual display height |
| `ENABLE_VNC` | true | Start VNC + noVNC for human observation |
| `TERM_COLS` | 120 | Terminal columns for test scenarios |
| `TERM_ROWS` | 40 | Terminal rows for test scenarios |
| `FRAME_FPS` | 4 | Frames per second for recording |
| `FRAME_DIR` | /tmp/frames | Where captured frames are stored |
| `C3_BINARY` | /app/bin/codecuttlectl | Path to built binary |
| `PLUGIN_DIR` | /app/bin/plugins | Path to plugin binaries |
