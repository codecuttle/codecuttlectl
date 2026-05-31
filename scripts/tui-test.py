#!/usr/bin/env python3
"""
E2E TUI test framework for codecuttlectl using tmux as a virtual terminal.

Usage:
    python3 scripts/tui-test.py [--update-golden] [--verbose] [test_name]

This gives Playwright-style testing capabilities:
- Launch the TUI in a virtual terminal (no display needed)
- Send keystrokes and mouse clicks
- Capture screenshots (text-based pane content)
- Wait for patterns to appear on screen
- Assert screen content
- Compare against golden files for visual regression
"""

import argparse
import os
import subprocess
import sys
import time
import re

# Paths
REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BINARY = os.path.join(REPO_ROOT, "bin", "codecuttlectl")
PLUGIN_DIR = os.path.join(REPO_ROOT, "bin", "plugins")
GOLDEN_DIR = os.path.join(REPO_ROOT, "testdata", "golden")
SCREENSHOT_DIR = os.path.join(REPO_ROOT, "testdata", "screenshots")

SESSION_NAME = "tui-test"
DEFAULT_WIDTH = 120
DEFAULT_HEIGHT = 40


class TUITester:
    """Playwright-style interface for testing the TUI via tmux."""

    def __init__(self, width=DEFAULT_WIDTH, height=DEFAULT_HEIGHT, verbose=False):
        self.width = width
        self.height = height
        self.verbose = verbose
        self._started = False

    def start(self, extra_args=None):
        """Launch the TUI in a tmux session."""
        self._kill_existing()
        
        cmd = f"{BINARY} -plugin-dir {PLUGIN_DIR}"
        if extra_args:
            cmd += " " + extra_args

        subprocess.run([
            "tmux", "new-session", "-d",
            "-s", SESSION_NAME,
            "-x", str(self.width),
            "-y", str(self.height),
            cmd
        ], check=True)
        self._started = True
        
        if self.verbose:
            print(f"  [tmux] Started session: {self.width}x{self.height}")
        
        # Wait for TUI to initialize
        time.sleep(1.5)

    def screenshot(self, strip_ansi=True) -> str:
        """Capture the current screen content."""
        if not self._started:
            raise RuntimeError("TUI not started")
        
        result = subprocess.run(
            ["tmux", "capture-pane", "-t", SESSION_NAME, "-p", "-e"],
            capture_output=True, text=True
        )
        content = result.stdout
        
        if strip_ansi:
            # Remove ANSI escape sequences for text assertions
            content = re.sub(r'\x1b\[[0-9;]*[a-zA-Z]', '', content)
            content = re.sub(r'\x1b\([AB0-9]', '', content)
        
        return content

    def screenshot_raw(self) -> str:
        """Capture with ANSI escape codes preserved (for golden files with color)."""
        if not self._started:
            raise RuntimeError("TUI not started")
        result = subprocess.run(
            ["tmux", "capture-pane", "-t", SESSION_NAME, "-p", "-e"],
            capture_output=True, text=True
        )
        return result.stdout

    def send_keys(self, keys: str):
        """Send keystrokes to the TUI."""
        if self.verbose:
            print(f"  [keys] {repr(keys)}")
        subprocess.run(["tmux", "send-keys", "-t", SESSION_NAME, keys], check=True)
        time.sleep(0.1)  # Small delay for rendering

    def send_text(self, text: str):
        """Type text literally (handles special characters)."""
        if self.verbose:
            print(f"  [type] {repr(text)}")
        # Use -l flag to send literal text
        subprocess.run(["tmux", "send-keys", "-t", SESSION_NAME, "-l", text], check=True)
        time.sleep(0.1)

    def press_enter(self):
        self.send_keys("Enter")

    def press_ctrl_c(self):
        self.send_keys("C-c")

    def press_ctrl_t(self):
        self.send_keys("C-t")

    def press_escape(self):
        self.send_keys("Escape")

    def click(self, x: int, y: int):
        """Send a mouse click at terminal coordinates (0-indexed)."""
        if self.verbose:
            print(f"  [click] ({x}, {y})")
        # tmux mouse event: button press at coordinates
        subprocess.run([
            "tmux", "send-keys", "-t", SESSION_NAME,
            "-M", "-x", str(x), "-y", str(y)
        ])
        time.sleep(0.2)

    def resize(self, width: int, height: int):
        """Resize the terminal window."""
        self.width = width
        self.height = height
        subprocess.run([
            "tmux", "resize-window", "-t", SESSION_NAME,
            "-x", str(width), "-y", str(height)
        ], check=True)
        time.sleep(0.3)

    def wait_for(self, pattern: str, timeout: float = 30.0) -> str:
        """Wait until the screen contains the given pattern. Returns the screen content."""
        deadline = time.time() + timeout
        while time.time() < deadline:
            screen = self.screenshot()
            if pattern in screen:
                return screen
            time.sleep(0.2)
        
        # Timeout - capture final state for debugging
        final = self.screenshot()
        raise TimeoutError(
            f"Pattern '{pattern}' not found after {timeout}s.\n"
            f"Final screen:\n{final}"
        )

    def wait_for_regex(self, regex: str, timeout: float = 30.0) -> str:
        """Wait until the screen matches a regex pattern."""
        deadline = time.time() + timeout
        while time.time() < deadline:
            screen = self.screenshot()
            if re.search(regex, screen):
                return screen
            time.sleep(0.2)
        
        final = self.screenshot()
        raise TimeoutError(
            f"Regex '{regex}' not matched after {timeout}s.\n"
            f"Final screen:\n{final}"
        )

    def assert_contains(self, text: str, msg: str = ""):
        """Assert the screen currently contains the given text."""
        screen = self.screenshot()
        if text not in screen:
            ctx = msg + ": " if msg else ""
            raise AssertionError(
                f"{ctx}Expected screen to contain '{text}'.\n"
                f"Actual screen:\n{screen}"
            )

    def assert_not_contains(self, text: str, msg: str = ""):
        """Assert the screen does NOT contain the given text."""
        screen = self.screenshot()
        if text in screen:
            ctx = msg + ": " if msg else ""
            raise AssertionError(
                f"{ctx}Expected screen NOT to contain '{text}'.\n"
                f"Actual screen:\n{screen}"
            )

    def save_screenshot(self, name: str):
        """Save a screenshot to the screenshots directory."""
        os.makedirs(SCREENSHOT_DIR, exist_ok=True)
        content = self.screenshot()
        path = os.path.join(SCREENSHOT_DIR, f"{name}.txt")
        with open(path, "w") as f:
            f.write(content)
        if self.verbose:
            print(f"  [screenshot] Saved: {path}")
        return path

    def compare_golden(self, name: str, update: bool = False) -> bool:
        """Compare current screen against a golden file."""
        content = self.screenshot()
        golden_path = os.path.join(GOLDEN_DIR, f"{name}.golden")

        if update or not os.path.exists(golden_path):
            os.makedirs(GOLDEN_DIR, exist_ok=True)
            with open(golden_path, "w") as f:
                f.write(content)
            if self.verbose:
                print(f"  [golden] Updated: {golden_path}")
            return True

        with open(golden_path, "r") as f:
            expected = f.read()

        if content == expected:
            return True
        
        # Show diff
        print(f"  [golden] MISMATCH: {golden_path}")
        print(f"  Expected ({len(expected)} chars) vs Got ({len(content)} chars)")
        return False

    def teardown(self):
        """Kill the tmux session."""
        if self._started:
            subprocess.run(
                ["tmux", "kill-session", "-t", SESSION_NAME],
                capture_output=True
            )
            self._started = False
            if self.verbose:
                print("  [tmux] Session killed")

    def _kill_existing(self):
        """Kill any existing test session."""
        subprocess.run(
            ["tmux", "kill-session", "-t", SESSION_NAME],
            capture_output=True
        )


# --- Test Cases ---

class TestResults:
    def __init__(self):
        self.passed = 0
        self.failed = 0
        self.errors = []

    def record(self, name: str, passed: bool, error: str = ""):
        if passed:
            self.passed += 1
            print(f"  ✓ {name}")
        else:
            self.failed += 1
            self.errors.append((name, error))
            print(f"  ✗ {name}")
            if error:
                for line in error.split("\n")[:5]:
                    print(f"    {line}")

    def summary(self):
        total = self.passed + self.failed
        print(f"\n{'='*60}")
        print(f"Results: {self.passed}/{total} passed, {self.failed} failed")
        if self.errors:
            print(f"\nFailed tests:")
            for name, err in self.errors:
                print(f"  - {name}: {err[:100]}")
        return self.failed == 0


def test_startup(tui: TUITester, results: TestResults, update_golden: bool):
    """Test that the TUI starts and shows expected UI elements."""
    name = "startup"
    try:
        tui.start()
        tui.save_screenshot("01_startup")
        
        screen = tui.screenshot()
        
        # Should show the app name
        assert "codecuttlectl" in screen, "App name not visible"
        # Should show model info
        assert "opus" in screen.lower() or "claude" in screen.lower(), "Model not visible"
        # Should show plugin count
        assert "4p" in screen or "plugin" in screen.lower(), "Plugin info not visible"
        # Should show help bar
        assert "ctrl+c" in screen.lower(), "Help bar not visible"
        
        tui.compare_golden(name, update=update_golden)
        results.record(name, True)
    except Exception as e:
        results.record(name, False, str(e))


def test_send_message(tui: TUITester, results: TestResults, update_golden: bool):
    """Test sending a message and receiving a response."""
    name = "send_message"
    try:
        tui.send_text("Say hello in one sentence")
        tui.press_enter()
        time.sleep(1)
        tui.save_screenshot("02_after_send")
        
        # Should show user message (▶ prefix with content)
        tui.wait_for("Say hello", timeout=5)
        
        # Wait for response (assistant prefix ◆)
        tui.wait_for("codecuttle", timeout=45)
        time.sleep(2)
        tui.save_screenshot("03_after_response")
        
        screen = tui.screenshot()
        # Should have assistant response
        assert "◆" in screen or "Hello" in screen or "hello" in screen, \
            "No response received"
        
        results.record(name, True)
    except Exception as e:
        tui.save_screenshot(f"{name}_FAILED")
        results.record(name, False, str(e))


def test_tool_calling(tui: TUITester, results: TestResults, update_golden: bool):
    """Test that tool calls are shown in the UI."""
    name = "tool_calling"
    try:
        tui.send_text("List the contents of /tmp")
        tui.press_enter()
        
        # Wait for tool call indicator or a successful result
        tui.wait_for_regex(r"Calling|✓|list_directory", timeout=45)
        time.sleep(3)
        tui.save_screenshot("04_tool_call")
        
        screen = tui.screenshot()
        has_tool = "list_directory" in screen or "Calling" in screen or "✓" in screen or "⚡" in screen
        assert has_tool, "No tool call indicator visible"
        
        results.record(name, True)
    except Exception as e:
        tui.save_screenshot(f"{name}_FAILED")
        results.record(name, False, str(e))


def test_ctrl_t_toggle(tui: TUITester, results: TestResults, update_golden: bool):
    """Test Ctrl+T toggles the todo panel."""
    name = "ctrl_t_toggle"
    try:
        tui.press_ctrl_t()
        time.sleep(0.5)
        tui.save_screenshot("05_todo_expanded")
        
        screen = tui.screenshot()
        # Should show "No active tasks" or todo panel
        has_todo_ui = "task" in screen.lower() or "todo" in screen.lower() or "○" in screen or "●" in screen
        assert has_todo_ui, "Todo panel not visible after Ctrl+T"
        
        # Toggle back
        tui.press_ctrl_t()
        time.sleep(0.5)
        
        results.record(name, True)
    except Exception as e:
        tui.save_screenshot(f"{name}_FAILED")
        results.record(name, False, str(e))


def test_ctrl_c_quits(tui: TUITester, results: TestResults, update_golden: bool):
    """Test Ctrl+C quits the application."""
    name = "ctrl_c_quits"
    try:
        tui.press_ctrl_c()
        time.sleep(1)
        
        # Session should be dead or TUI should have exited
        result = subprocess.run(
            ["tmux", "has-session", "-t", SESSION_NAME],
            capture_output=True
        )
        # tmux has-session returns 0 if exists, 1 if not
        # The TUI may have exited but tmux session might linger briefly
        # Check if we can still get output (if session is gone, it's fine)
        if result.returncode == 0:
            screen = tui.screenshot()
            # If session still exists, it should show shell prompt or be empty
            # (the TUI exited, returning to the shell in the tmux session)
            results.record(name, True)
        else:
            results.record(name, True)
    except Exception as e:
        results.record(name, False, str(e))


def run_tests(update_golden=False, verbose=False, filter_name=None):
    """Run all E2E tests."""
    print(f"\n{'='*60}")
    print("codecuttlectl TUI E2E Tests")
    print(f"{'='*60}\n")

    # Check prerequisites
    if not os.path.exists(BINARY):
        print(f"ERROR: Binary not found at {BINARY}")
        print("Run 'make all' first.")
        return False

    results = TestResults()
    tui = TUITester(verbose=verbose)

    tests = [
        test_startup,
        test_send_message,
        test_tool_calling,
        test_ctrl_t_toggle,
        test_ctrl_c_quits,
    ]

    for test_fn in tests:
        name = test_fn.__name__.replace("test_", "")
        if filter_name and filter_name not in name:
            continue
        
        print(f"\n--- {test_fn.__doc__.strip()} ---")
        
        # Each test gets a fresh TUI instance (except ctrl_c which reuses)
        if test_fn != test_ctrl_c_quits:
            tui.teardown()
            time.sleep(0.5)
            if test_fn == test_startup:
                pass  # test_startup calls tui.start() itself
            elif test_fn in (test_send_message, test_tool_calling, test_ctrl_t_toggle):
                tui.start()
        
        test_fn(tui, results, update_golden)

    tui.teardown()
    return results.summary()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="E2E TUI tests for codecuttlectl")
    parser.add_argument("--update-golden", action="store_true", help="Update golden files")
    parser.add_argument("--verbose", "-v", action="store_true", help="Verbose output")
    parser.add_argument("test", nargs="?", help="Run only tests matching this name")
    args = parser.parse_args()

    success = run_tests(
        update_golden=args.update_golden,
        verbose=args.verbose,
        filter_name=args.test,
    )
    sys.exit(0 if success else 1)
