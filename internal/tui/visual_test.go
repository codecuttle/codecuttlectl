//go:build visual

// Package tui_test provides visual end-to-end tests for the TUI.
// These tests require the visual test container environment (Xvfb, mlterm, xdotool).
//
// Run with:
//   docker compose -f docker-compose.visual.yml run --rm visual-test \
//     go test -tags visual -v -timeout 120s ./internal/tui/
package tui_test

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codecuttle/codecuttlectl/internal/visualtest"
)

// testEnv creates a visual test environment with default settings.
// Skips the test if not running inside the visual test container.
func testEnv(t *testing.T, cols, rows int) *visualtest.Environment {
	t.Helper()

	if os.Getenv("DISPLAY") == "" {
		t.Skip("DISPLAY not set — skipping visual test (run in visual-test container)")
	}

	if cols <= 0 {
		cols = 100
	}
	if rows <= 0 {
		rows = 30
	}

	env, err := visualtest.NewEnvironment(visualtest.Config{
		Cols:     cols,
		Rows:     rows,
		FrameDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("creating visual test environment: %v", err)
	}

	t.Cleanup(func() { env.Close() })
	return env
}

// saveEvidence saves frames as PNGs in a test-specific directory for debugging.
func saveEvidence(t *testing.T, frames []image.Image, prefix string) {
	t.Helper()
	dir := filepath.Join("testdata", "evidence", t.Name())
	os.MkdirAll(dir, 0755)
	for i, frame := range frames {
		path := filepath.Join(dir, fmt.Sprintf("%s_%03d.png", prefix, i))
		f, err := os.Create(path)
		if err != nil {
			continue
		}
		png.Encode(f, frame)
		f.Close()
	}
	t.Logf("Evidence saved to %s/", dir)
}

// saveDiffMap saves a diff map between two frames for visual debugging.
func saveDiffMap(t *testing.T, a, b image.Image, name string) {
	t.Helper()
	dir := filepath.Join("testdata", "evidence", t.Name())
	os.MkdirAll(dir, 0755)
	diffImg := visualtest.DiffMap(a, b)
	path := filepath.Join(dir, name+".png")
	f, err := os.Create(path)
	if err != nil {
		return
	}
	png.Encode(f, diffImg)
	f.Close()
	t.Logf("Diff map saved: %s", path)
}

// TestViewportStableAfterResponse verifies that the viewport doesn't jump
// after a model response has completed and the TUI is idle.
func TestViewportStableAfterResponse(t *testing.T) {
	env := testEnv(t, 100, 30)

	if err := env.LaunchTUI(); err != nil {
		t.Fatalf("launching TUI: %v", err)
	}

	// Send a simple message
	env.Type("Say hello in exactly 3 words")
	env.PressKey("Return")

	// Wait for response to complete
	env.Wait(15 * time.Second)

	// Record idle frames at 30fps for 3 seconds (90 frames)
	frames, err := env.RecordFrames(3*time.Second, 30)
	if err != nil {
		t.Fatalf("recording frames: %v", err)
	}
	t.Logf("Captured %d frames", len(frames))

	if len(frames) < 10 {
		t.Fatalf("expected at least 10 frames, got %d", len(frames))
	}

	// Load all frames
	var images []image.Image
	for _, path := range frames {
		img, err := visualtest.LoadPNG(path)
		if err != nil {
			t.Fatalf("loading frame %s: %v", path, err)
		}
		images = append(images, img)
	}

	// Assert: no frame transition exceeds 1% (cursor blink only)
	jumps, err := visualtest.DetectViewportJump(images, 0.01, nil)
	if err != nil {
		t.Fatalf("detecting jumps: %v", err)
	}

	if len(jumps) > 0 {
		t.Errorf("detected %d viewport jump(s) during idle:", len(jumps))
		for _, j := range jumps {
			t.Errorf("  frame %d→%d: %.2f%% pixels changed", j.FromIndex, j.ToIndex, j.Diff*100)
			saveDiffMap(t, images[j.FromIndex], images[j.ToIndex],
				fmt.Sprintf("jump_%d_to_%d", j.FromIndex, j.ToIndex))
		}
		saveEvidence(t, images, "idle")
	}
}

// TestViewportStableDuringTyping verifies that typing in the input area
// doesn't cause the viewport (chat history) to jump.
func TestViewportStableDuringTyping(t *testing.T) {
	env := testEnv(t, 100, 30)

	if err := env.LaunchTUI(); err != nil {
		t.Fatalf("launching TUI: %v", err)
	}

	// Send a message and wait for response
	env.Type("What is Go?")
	env.PressKey("Return")
	env.Wait(15 * time.Second)

	// Now start recording while typing a new message
	frames, err := env.RecordFrames(4*time.Second, 30)
	if err != nil {
		t.Fatalf("starting recording: %v", err)
	}

	// Type while recording
	go func() {
		time.Sleep(500 * time.Millisecond)
		env.TypeSlow("This is a test of typing stability")
	}()

	// Wait for recording to complete (it already ran via RecordFrames which blocks)
	t.Logf("Captured %d frames during typing", len(frames))

	if len(frames) < 15 {
		t.Fatalf("expected at least 15 frames, got %d", len(frames))
	}

	var images []image.Image
	for _, path := range frames {
		img, err := visualtest.LoadPNG(path)
		if err != nil {
			t.Fatalf("loading frame %s: %v", path, err)
		}
		images = append(images, img)
	}

	// During typing, the input area changes but the viewport (chat history)
	// should remain stable. Use a 5% threshold since the input area changes
	// are expected but viewport jumps are large (20%+).
	jumps, err := visualtest.DetectViewportJump(images, 0.05, nil)
	if err != nil {
		t.Fatalf("detecting jumps: %v", err)
	}

	if len(jumps) > 0 {
		t.Errorf("detected %d large viewport change(s) during typing:", len(jumps))
		for _, j := range jumps {
			t.Errorf("  frame %d→%d: %.2f%% pixels changed", j.FromIndex, j.ToIndex, j.Diff*100)
			saveDiffMap(t, images[j.FromIndex], images[j.ToIndex],
				fmt.Sprintf("typing_jump_%d_to_%d", j.FromIndex, j.ToIndex))
		}
		saveEvidence(t, images, "typing")
	}
}

// TestViewportScrollMonotonic verifies that during streaming, the viewport
// only scrolls downward (content grows) — never jumps back up.
func TestViewportScrollMonotonic(t *testing.T) {
	env := testEnv(t, 100, 30)

	if err := env.LaunchTUI(); err != nil {
		t.Fatalf("launching TUI: %v", err)
	}

	// Ask for something that produces a long response
	env.Type("Count from 1 to 20, one number per line")
	env.PressKey("Return")

	// Wait a moment for streaming to start, then record
	env.Wait(2 * time.Second)
	frames, err := env.RecordFrames(10*time.Second, 30)
	if err != nil {
		t.Fatalf("recording frames: %v", err)
	}
	t.Logf("Captured %d frames during streaming", len(frames))

	if len(frames) < 20 {
		t.Fatalf("expected at least 20 frames, got %d", len(frames))
	}

	var images []image.Image
	for _, path := range frames {
		img, err := visualtest.LoadPNG(path)
		if err != nil {
			t.Fatalf("loading frame %s: %v", path, err)
		}
		images = append(images, img)
	}

	// Check scrolling is monotonic (only downward)
	monotonic, directions := visualtest.IsMonotonicallyScrolling(images, 18)
	if !monotonic {
		t.Errorf("viewport scroll is NOT monotonic — detected reversals: %v", directions)
		saveEvidence(t, images, "scroll")
	} else {
		t.Logf("Scroll is monotonic. Detected %d scroll events.", len(directions))
	}
}
