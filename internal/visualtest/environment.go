// Package visualtest provides programmatic control of a virtual display
// environment for TUI visual testing. It launches a terminal emulator inside
// Xvfb, injects keystrokes, captures frames, and provides assertion helpers
// for detecting layout bugs like viewport jumping and text overflow.
//
// This package requires a running Xvfb display (DISPLAY env var set) and
// the following tools available on PATH: xdotool, import (ImageMagick),
// ffmpeg, mlterm.
//
// Intended to be used with the //go:build visual build tag so tests only
// run inside the visual test container.
package visualtest

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config controls the test environment setup.
type Config struct {
	Cols       int    // Terminal columns (default: 100)
	Rows       int    // Terminal rows (default: 30)
	FontSize   int    // Font size in pixels (default: 14)
	C3Binary   string // Path to codecuttlectl (default: from env or /app/bin/codecuttlectl)
	PluginDir  string // Path to plugins (default: from env or /app/bin/plugins)
	FrameDir   string // Directory for captured frames (default: /tmp/frames)
}

func (c *Config) defaults() {
	if c.Cols <= 0 {
		c.Cols = 100
	}
	if c.Rows <= 0 {
		c.Rows = 30
	}
	if c.FontSize <= 0 {
		c.FontSize = 14
	}
	if c.C3Binary == "" {
		c.C3Binary = envOr("C3_BINARY", "/app/bin/codecuttlectl")
	}
	if c.PluginDir == "" {
		c.PluginDir = envOr("PLUGIN_DIR", "/app/bin/plugins")
	}
	if c.FrameDir == "" {
		c.FrameDir = envOr("FRAME_DIR", "/tmp/frames")
	}
}

// Environment manages a virtual terminal with codecuttlectl running inside it.
type Environment struct {
	cfg       Config
	termPID   int
	windowID  string
	frameDir  string
	frameSeq  int
	recording *exec.Cmd
}

// NewEnvironment creates and launches the test environment.
// It starts mlterm at the configured size and waits for it to be ready.
// The caller must call Close() to clean up.
func NewEnvironment(cfg Config) (*Environment, error) {
	cfg.defaults()

	display := os.Getenv("DISPLAY")
	if display == "" {
		return nil, fmt.Errorf("DISPLAY not set — run inside the visual test container")
	}

	// Check required tools
	for _, tool := range []string{"xdotool", "import", "mlterm"} {
		if _, err := exec.LookPath(tool); err != nil {
			return nil, fmt.Errorf("required tool %q not found on PATH", tool)
		}
	}

	// Create frame directory
	frameDir := cfg.FrameDir
	os.MkdirAll(frameDir, 0755)

	env := &Environment{
		cfg:      cfg,
		frameDir: frameDir,
	}

	// Launch mlterm
	if err := env.launchTerminal(); err != nil {
		return nil, fmt.Errorf("launching terminal: %w", err)
	}

	return env, nil
}

// Close kills the terminal and any recording process.
func (e *Environment) Close() {
	if e.recording != nil {
		e.recording.Process.Signal(os.Interrupt)
		e.recording.Wait()
		e.recording = nil
	}
	if e.termPID > 0 {
		exec.Command("kill", strconv.Itoa(e.termPID)).Run()
	}
}

// LaunchTUI types the codecuttlectl command into the terminal and waits for startup.
func (e *Environment) LaunchTUI() error {
	cmd := fmt.Sprintf("%s -plugin-dir %s", e.cfg.C3Binary, e.cfg.PluginDir)
	if err := e.Type(cmd); err != nil {
		return err
	}
	if err := e.PressKey("Return"); err != nil {
		return err
	}
	// Wait for TUI to initialize (plugins load, prompt renders)
	time.Sleep(3 * time.Second)
	return nil
}

// Type injects text into the focused terminal via xdotool.
func (e *Environment) Type(text string) error {
	return exec.Command("xdotool", "type", "--delay", "30", text).Run()
}

// TypeSlow injects text with a longer delay between characters.
func (e *Environment) TypeSlow(text string) error {
	return exec.Command("xdotool", "type", "--delay", "80", text).Run()
}

// PressKey sends a key press (e.g., "Return", "ctrl+c", "ctrl+t").
func (e *Environment) PressKey(key string) error {
	return exec.Command("xdotool", "key", key).Run()
}

// Wait pauses for the specified duration.
func (e *Environment) Wait(d time.Duration) {
	time.Sleep(d)
}

// CaptureFrame takes a screenshot and returns the file path.
// The name is used as a prefix for the filename.
func (e *Environment) CaptureFrame(name string) (string, error) {
	e.frameSeq++
	filename := fmt.Sprintf("%03d_%s.png", e.frameSeq, name)
	path := filepath.Join(e.frameDir, filename)

	err := exec.Command("import", "-window", "root", "-display", os.Getenv("DISPLAY"), path).Run()
	if err != nil {
		return "", fmt.Errorf("capturing frame: %w", err)
	}
	return path, nil
}

// RecordFrames captures frames at the given FPS for the specified duration.
// Returns the list of frame file paths sorted chronologically.
// Higher FPS (e.g., 10-30) gives better temporal resolution for detecting jumps.
func (e *Environment) RecordFrames(duration time.Duration, fps int) ([]string, error) {
	if fps <= 0 {
		fps = 10
	}

	display := os.Getenv("DISPLAY")

	// Get screen dimensions from xdpyinfo or use default
	screenSize := "1920x1080"

	pattern := filepath.Join(e.frameDir, "rec_%04d.png")

	// Remove any previous recording files
	oldFiles, _ := filepath.Glob(filepath.Join(e.frameDir, "rec_*.png"))
	for _, f := range oldFiles {
		os.Remove(f)
	}

	cmd := exec.Command("ffmpeg",
		"-y",
		"-f", "x11grab",
		"-video_size", screenSize,
		"-framerate", strconv.Itoa(fps),
		"-i", display,
		"-t", fmt.Sprintf("%.1f", duration.Seconds()),
		"-vf", fmt.Sprintf("fps=%d", fps),
		pattern,
	)
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("recording frames: %w", err)
	}

	// Collect output files sorted
	matches, err := filepath.Glob(filepath.Join(e.frameDir, "rec_*.png"))
	if err != nil {
		return nil, err
	}

	// filepath.Glob returns sorted order (lexicographic = chronological for %04d)
	return matches, nil
}

// launchTerminal starts mlterm with the configured geometry.
func (e *Environment) launchTerminal() error {
	geometry := fmt.Sprintf("%dx%d", e.cfg.Cols, e.cfg.Rows)
	fontSize := strconv.Itoa(e.cfg.FontSize)

	cmd := exec.Command("mlterm",
		"-g", geometry,
		"-w", fontSize,
		"-b", "#1a1b1e",
		"-f", "#dee2e6",
		"--boxdraw=unicode",
		"-y", "xterm-256color",
		"-e", "bash", "-c",
		"export COLORTERM=truecolor LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8; exec bash",
	)
	cmd.Env = append(os.Environ(), "DISPLAY="+os.Getenv("DISPLAY"))

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting mlterm: %w", err)
	}
	e.termPID = cmd.Process.Pid

	// Wait for window to appear
	time.Sleep(2 * time.Second)

	// Find window ID
	out, err := exec.Command("xdotool", "search", "--pid", strconv.Itoa(e.termPID)).Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		// Fallback: search by class
		out, err = exec.Command("xdotool", "search", "--class", "mlterm").Output()
		if err != nil {
			return fmt.Errorf("could not find mlterm window: %w", err)
		}
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 {
		return fmt.Errorf("no mlterm window found")
	}
	e.windowID = lines[len(lines)-1]

	// Focus window
	exec.Command("xdotool", "windowfocus", e.windowID).Run()
	exec.Command("xdotool", "windowactivate", e.windowID).Run()
	time.Sleep(500 * time.Millisecond)

	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// LoadPNG reads a PNG file and returns it as an image.Image.
func LoadPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}
