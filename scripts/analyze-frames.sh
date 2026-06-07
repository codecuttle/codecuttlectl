#!/bin/bash
# analyze-frames.sh — Basic frame analysis for visual test output.
#
# Compares consecutive frames and reports pixel difference percentages.
# High diff between frames when the TUI should be idle indicates a jump/flicker bug.
#
# Usage:
#   ./scripts/analyze-frames.sh [frame_dir] [threshold]
#
# Arguments:
#   frame_dir  — Directory containing frame_NNNN.png files (default: /tmp/frames)
#   threshold  — Max acceptable diff percentage for idle frames (default: 2.0)
#
# Requires: ImageMagick (compare command)
set -e

FRAME_DIR="${1:-/tmp/frames}"
THRESHOLD="${2:-2.0}"

if [ ! -d "$FRAME_DIR" ]; then
    echo "ERROR: Frame directory not found: $FRAME_DIR"
    exit 1
fi

FRAMES=($(ls "$FRAME_DIR"/frame_*.png 2>/dev/null | sort))
COUNT=${#FRAMES[@]}

if [ "$COUNT" -lt 2 ]; then
    echo "ERROR: Need at least 2 frames to compare. Found: $COUNT"
    exit 1
fi

echo "Analyzing $COUNT frames in $FRAME_DIR (threshold: ${THRESHOLD}%)"
echo "---"

JUMPS=0
MAX_DIFF=0

for ((i=1; i<COUNT; i++)); do
    PREV="${FRAMES[$((i-1))]}"
    CURR="${FRAMES[$i]}"

    # Use ImageMagick to compute normalized pixel difference
    # AE = Absolute Error (number of different pixels)
    DIFF_PIXELS=$(compare -metric AE "$PREV" "$CURR" /dev/null 2>&1 || true)

    # Get total pixels
    DIMENSIONS=$(identify -format "%w %h" "$CURR" 2>/dev/null)
    WIDTH=$(echo "$DIMENSIONS" | cut -d' ' -f1)
    HEIGHT=$(echo "$DIMENSIONS" | cut -d' ' -f2)
    TOTAL=$((WIDTH * HEIGHT))

    if [ "$TOTAL" -eq 0 ]; then
        continue
    fi

    # Calculate percentage
    PERCENT=$(echo "scale=4; $DIFF_PIXELS * 100 / $TOTAL" | bc 2>/dev/null || echo "0")

    # Track max
    if (( $(echo "$PERCENT > $MAX_DIFF" | bc -l 2>/dev/null || echo 0) )); then
        MAX_DIFF="$PERCENT"
    fi

    # Check threshold
    OVER=$(echo "$PERCENT > $THRESHOLD" | bc -l 2>/dev/null || echo 0)
    if [ "$OVER" = "1" ]; then
        JUMPS=$((JUMPS + 1))
        echo "⚠️  JUMP: frame $((i-1))→$i: ${PERCENT}% diff ($(basename $PREV) → $(basename $CURR))"
    fi
done

echo "---"
echo "Results:"
echo "  Frames analyzed: $((COUNT - 1)) transitions"
echo "  Max diff: ${MAX_DIFF}%"
echo "  Jumps detected: $JUMPS (threshold: ${THRESHOLD}%)"

if [ "$JUMPS" -gt 0 ]; then
    echo ""
    echo "FAIL: $JUMPS viewport jump(s) detected above ${THRESHOLD}% threshold."
    exit 1
else
    echo ""
    echo "PASS: No viewport jumps detected."
    exit 0
fi
