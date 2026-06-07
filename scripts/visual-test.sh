#!/bin/bash
# visual-test.sh — Manual visual test runner for codecuttlectl TUI.
#
# Usage:
#   ./scripts/visual-test.sh [scenario]
#
# Scenarios:
#   basic         — Launch TUI, type a message, capture response
#   stability     — Type a message, wait for response, capture idle frames
#   typing        — Type while streaming, check for jumps
#   resize        — Resize terminal mid-conversation
#   scroll        — Long conversation, verify scrolling
#   capture       — Just launch TUI and capture frames continuously
#
# Environment:
#   TERM_COLS     — Terminal columns (default: 100)
#   TERM_ROWS     — Terminal rows (default: 30)
#   FRAME_DIR     — Frame output directory (default: /tmp/frames)
#   FRAME_FPS     — Capture frame rate (default: 4)
#   C3_BINARY     — Path to codecuttlectl binary (default: /app/bin/codecuttlectl)
#   PLUGIN_DIR    — Path to plugins (default: /app/bin/plugins)
#   CAPTURE_DURATION — How long to record in seconds (default: 10)
#
set -e

SCENARIO="${1:-basic}"
TERM_COLS="${TERM_COLS:-100}"
TERM_ROWS="${TERM_ROWS:-30}"
FRAME_DIR="${FRAME_DIR:-/tmp/frames}"
FRAME_FPS="${FRAME_FPS:-4}"
C3_BINARY="${C3_BINARY:-/app/bin/codecuttlectl}"
PLUGIN_DIR="${PLUGIN_DIR:-/app/bin/plugins}"
CAPTURE_DURATION="${CAPTURE_DURATION:-10}"

# Ensure display is set
if [ -z "$DISPLAY" ]; then
    echo "ERROR: DISPLAY not set. Run inside the visual-test container."
    exit 1
fi

# Ensure xdotool is available
if ! command -v xdotool &>/dev/null; then
    echo "ERROR: xdotool not found. Install it or run inside the container."
    exit 1
fi

# Clean frame directory
rm -rf "${FRAME_DIR}"
mkdir -p "${FRAME_DIR}"

# --- Helper Functions ---

launch_terminal() {
    local cols="${1:-$TERM_COLS}"
    local rows="${2:-$TERM_ROWS}"

    echo "[test] Launching mlterm (${cols}x${rows})"
    DISPLAY=:99 mlterm -g "${cols}x${rows}" \
        -w 14 \
        -b "#1a1b1e" -f "#dee2e6" \
        --boxdraw=unicode \
        -y xterm-256color \
        -e bash -c "export COLORTERM=truecolor LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8; exec bash" &
    TERM_PID=$!
    sleep 2

    # Find the mlterm window
    TERM_WID=$(xdotool search --pid $TERM_PID 2>/dev/null | head -1)
    if [ -z "$TERM_WID" ]; then
        TERM_WID=$(xdotool search --class "mlterm" 2>/dev/null | tail -1)
    fi

    if [ -z "$TERM_WID" ]; then
        echo "ERROR: Could not find mlterm window"
        exit 1
    fi

    echo "[test] Terminal window: $TERM_WID (PID: $TERM_PID)"
    xdotool windowfocus "$TERM_WID"
    xdotool windowactivate "$TERM_WID"
    sleep 0.5
}

launch_c3() {
    echo "[test] Launching codecuttlectl in terminal"
    xdotool type --delay 20 "${C3_BINARY} -plugin-dir ${PLUGIN_DIR}"
    xdotool key Return
    sleep 2  # Wait for TUI to initialize and plugins to load
    echo "[test] TUI should be running"
}

type_message() {
    local msg="$1"
    local delay="${2:-30}"
    echo "[test] Typing: $msg"
    xdotool type --delay "$delay" "$msg"
}

press_key() {
    local key="$1"
    echo "[test] Pressing: $key"
    xdotool key "$key"
}

wait_seconds() {
    local secs="$1"
    echo "[test] Waiting ${secs}s..."
    sleep "$secs"
}

capture_frame() {
    local name="$1"
    local path="${FRAME_DIR}/${name}.png"
    import -window root -display :99 "$path" 2>/dev/null
    echo "[test] Captured: $path"
}

start_recording() {
    local duration="${1:-$CAPTURE_DURATION}"
    local fps="${2:-$FRAME_FPS}"
    echo "[test] Recording ${duration}s at ${fps}fps → ${FRAME_DIR}/frame_%04d.png"
    ffmpeg -y -f x11grab -video_size "${SCREEN_WIDTH:-1920}x${SCREEN_HEIGHT:-1080}" \
        -framerate "$fps" -i :99 \
        -t "$duration" \
        -vf "fps=$fps" \
        "${FRAME_DIR}/frame_%04d.png" \
        > /dev/null 2>&1 &
    FFMPEG_PID=$!
}

stop_recording() {
    if [ -n "$FFMPEG_PID" ] && kill -0 "$FFMPEG_PID" 2>/dev/null; then
        kill "$FFMPEG_PID" 2>/dev/null
        wait "$FFMPEG_PID" 2>/dev/null || true
    fi
    local count=$(ls "${FRAME_DIR}"/frame_*.png 2>/dev/null | wc -l)
    echo "[test] Recording stopped. ${count} frames captured."
}

cleanup() {
    echo "[test] Cleaning up..."
    [ -n "$FFMPEG_PID" ] && kill "$FFMPEG_PID" 2>/dev/null || true
    [ -n "$TERM_PID" ] && kill "$TERM_PID" 2>/dev/null || true
    wait 2>/dev/null || true
}
trap cleanup EXIT

# --- Scenarios ---

scenario_basic() {
    echo "=== Scenario: basic ==="
    echo "Launch TUI, type a message, capture the response."

    launch_terminal
    launch_c3
    capture_frame "01_tui_launched"

    type_message "What is 2 + 2?"
    press_key Return
    capture_frame "02_message_sent"

    wait_seconds 10
    capture_frame "03_response_received"

    echo "=== Done. Frames in ${FRAME_DIR}/ ==="
    ls -la "${FRAME_DIR}/"
}

scenario_stability() {
    echo "=== Scenario: stability ==="
    echo "Type a message, wait for response, then record idle frames to detect jumps."

    launch_terminal
    launch_c3
    wait_seconds 1

    type_message "Tell me a short joke"
    press_key Return

    # Wait for response to complete (streaming takes ~5-15s typically)
    wait_seconds 15
    capture_frame "01_after_response"

    # Now record idle frames — viewport should be completely stable
    echo "[test] Recording idle frames (should be stable)..."
    start_recording 5 4
    wait "$FFMPEG_PID" 2>/dev/null || true

    echo "=== Done. Analyze frames for stability ==="
    ls -la "${FRAME_DIR}/"
}

scenario_typing() {
    echo "=== Scenario: typing ==="
    echo "Type while the model is streaming to detect input-area jumps."

    launch_terminal
    launch_c3
    wait_seconds 1

    type_message "Count from 1 to 50, one number per line"
    press_key Return

    # Start recording immediately
    start_recording 15 4

    # Wait a bit for streaming to start, then type
    wait_seconds 3
    type_message "hello world"
    wait_seconds 2
    type_message " this is a test"
    wait_seconds 10

    stop_recording

    echo "=== Done. Check for input-area jumps ==="
    ls -la "${FRAME_DIR}/"
}

scenario_resize() {
    echo "=== Scenario: resize ==="
    echo "Resize terminal mid-conversation to check reflow."

    launch_terminal 120 40
    launch_c3
    wait_seconds 1

    type_message "Explain what a hash map is"
    press_key Return
    wait_seconds 10
    capture_frame "01_before_resize"

    # Resize the terminal window
    echo "[test] Resizing terminal to 80x24"
    xdotool windowsize "$TERM_WID" 680 420  # Approximate pixel size for 80x24
    wait_seconds 2
    capture_frame "02_after_resize"

    echo "=== Done. Compare before/after ==="
    ls -la "${FRAME_DIR}/"
}

scenario_scroll() {
    echo "=== Scenario: scroll ==="
    echo "Multiple messages to fill viewport, then scroll."

    launch_terminal
    launch_c3
    wait_seconds 1

    for i in 1 2 3; do
        type_message "Tell me fact #${i} about cuttlefish"
        press_key Return
        wait_seconds 10
    done

    capture_frame "01_end_of_conversation"

    # Scroll up
    echo "[test] Scrolling up..."
    xdotool click --window "$TERM_WID" 4  # scroll up
    xdotool click --window "$TERM_WID" 4
    xdotool click --window "$TERM_WID" 4
    xdotool click --window "$TERM_WID" 4
    xdotool click --window "$TERM_WID" 4
    wait_seconds 1
    capture_frame "02_scrolled_up"

    # Scroll back down
    echo "[test] Scrolling down..."
    xdotool click --window "$TERM_WID" 5
    xdotool click --window "$TERM_WID" 5
    xdotool click --window "$TERM_WID" 5
    xdotool click --window "$TERM_WID" 5
    xdotool click --window "$TERM_WID" 5
    wait_seconds 1
    capture_frame "03_scrolled_down"

    echo "=== Done. Compare scroll positions ==="
    ls -la "${FRAME_DIR}/"
}

scenario_capture() {
    echo "=== Scenario: capture ==="
    echo "Launch TUI and record continuously. Use noVNC to interact manually."

    launch_terminal
    launch_c3

    echo "[test] Recording for ${CAPTURE_DURATION}s. Interact via noVNC at http://localhost:6080/vnc.html"
    start_recording "$CAPTURE_DURATION" "$FRAME_FPS"
    wait "$FFMPEG_PID" 2>/dev/null || true

    echo "=== Done. ${FRAME_DIR}/ ==="
    ls -la "${FRAME_DIR}/" | tail -20
}

# --- Main ---

case "$SCENARIO" in
    basic)     scenario_basic ;;
    stability) scenario_stability ;;
    typing)    scenario_typing ;;
    resize)    scenario_resize ;;
    scroll)    scenario_scroll ;;
    capture)   scenario_capture ;;
    *)
        echo "Unknown scenario: $SCENARIO"
        echo "Available: basic, stability, typing, resize, scroll, capture"
        exit 1
        ;;
esac
