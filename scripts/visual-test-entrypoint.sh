#!/bin/bash
# visual-test-entrypoint.sh — Starts the virtual display environment
# for TUI visual testing. Launches Xvfb, optionally VNC/noVNC, then
# executes the provided command (or drops to shell).
set -e

SCREEN_WIDTH="${SCREEN_WIDTH:-1920}"
SCREEN_HEIGHT="${SCREEN_HEIGHT:-1080}"
ENABLE_VNC="${ENABLE_VNC:-true}"

echo "[visual-test] Starting Xvfb :99 (${SCREEN_WIDTH}x${SCREEN_HEIGHT}x24)"
Xvfb :99 -screen 0 "${SCREEN_WIDTH}x${SCREEN_HEIGHT}x24" -ac +extension GLX +render -noreset &
XVFB_PID=$!
sleep 1

# Verify Xvfb is running
if ! kill -0 $XVFB_PID 2>/dev/null; then
    echo "[visual-test] ERROR: Xvfb failed to start"
    exit 1
fi

export DISPLAY=:99

if [ "$ENABLE_VNC" = "true" ]; then
    echo "[visual-test] Starting x11vnc on :5900"
    x11vnc -display :99 -forever -shared -nopw -rfbport 5900 -quiet &
    sleep 0.5

    echo "[visual-test] Starting noVNC on :6080"
    websockify --web /usr/share/novnc 6080 localhost:5900 > /dev/null 2>&1 &
    sleep 0.5

    echo "[visual-test] noVNC available at http://localhost:6080/vnc.html"
fi

# Create frame output directory
mkdir -p "${FRAME_DIR:-/tmp/frames}"

echo "[visual-test] Environment ready. DISPLAY=:99"
echo "[visual-test] Executing: $*"

# Execute the test command (or shell if none provided)
if [ $# -eq 0 ]; then
    exec bash
else
    exec "$@"
fi
