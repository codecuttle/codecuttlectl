#!/usr/bin/env python3
"""Build and exercise the real TUI in an isolated tmux terminal.

Default: offline OpenRouter fixture. --live: one authorized OpenRouter request,
128 output tokens maximum, no plugins, no title-generation API calls. Credentials
are resolved by the application and forwarded only to https://openrouter.ai.
Requires Go, Python 3 and tmux. Never overwrites bin/codecuttlectl or user sessions.
"""

import argparse
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import HTTPError
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parent.parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--live", action="store_true", help="opt in to one paid request")
    parser.add_argument("--model", default="google/gemini-2.5-flash")
    parser.add_argument("--binary", type=Path, help="test an existing binary instead of building")
    args = parser.parse_args()
    state = {"requests": 0, "finished": False, "shapes": [], "failure": None}
    lock = threading.Lock()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass  # Never log authorization headers or request bodies.

        def do_GET(self):
            if self.path != "/models":
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"data": [{"id": args.model, "context_length": 1048576}]}).encode())

        def do_POST(self):
            if self.path != "/chat/completions":
                self.send_error(404)
                return
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            if not body.get("stream"):
                # Session titles aren't part of the inference smoke test.
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{"choices":[{"message":{"role":"assistant","content":"Smoke test"},"finish_reason":"stop"}]}')
                return
            with lock:
                state["requests"] += 1
                count = state["requests"]
            if count > 1:
                self.send_error(429, "Smoke test request limit reached")
                return
            try:
                if args.live:
                    body["model"] = args.model
                    body.pop("models", None)
                    body["max_tokens"] = 128
                    body["reasoning"] = {"enabled": False}
                    req = Request("https://openrouter.ai/api/v1/chat/completions",
                                  data=json.dumps(body).encode(), headers={
                                      "Authorization": self.headers.get("Authorization", ""),
                                      "Content-Type": "application/json",
                                  })
                    with urlopen(req, timeout=45) as response:
                        lines = list(response)
                else:
                    payloads = [
                        {"choices": [{"index": 0, "delta": {"content": "SMOKE_OK"}, "finish_reason": None}]},
                        {"choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]},
                        # OpenRouter can repeat the terminal choice on its usage frame.
                        {"choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
                         "usage": {"prompt_tokens": 123, "completion_tokens": 4}},
                    ]
                    lines = [("data: " + json.dumps(p) + "\n\n").encode() for p in payloads]
                    lines.append(b"data: [DONE]\n\n")
                shapes = []
                for line in lines:
                    if not line.startswith(b"data:"):
                        continue
                    try:
                        chunk = json.loads(line[5:])
                    except ValueError:
                        continue
                    shapes.append({"choices": [{"finish": c.get("finish_reason"),
                                                "delta_keys": list((c.get("delta") or {}).keys())}
                                               for c in chunk.get("choices", [])],
                                   "usage": list((chunk.get("usage") or {}).keys()),
                                   "error": "error" in chunk})
                with lock:
                    state["shapes"] = shapes[-5:]
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.end_headers()
                for line in lines:
                    self.wfile.write(line)
                    self.wfile.flush()
            except (HTTPError, OSError) as exc:
                with lock:
                    state["failure"] = f"{type(exc).__name__}: {getattr(exc, 'code', 'transport failure')}"
                self.send_error(502, "Smoke test upstream failure")
            finally:
                with lock:
                    state["finished"] = True

    with tempfile.TemporaryDirectory(prefix="codecuttle-tui-smoke-") as tmp:
        tmp = Path(tmp)
        binary = args.binary.resolve() if args.binary else tmp / "codecuttlectl"
        if not args.binary:
            subprocess.run(["go", "build", "-o", str(binary), "./cmd/codecuttlectl"], cwd=ROOT, check=True, timeout=300)
        env = os.environ.copy()
        env["XDG_DATA_HOME"] = str(tmp / "data")
        env["TERM"] = "xterm-256color"
        if not args.live:
            env["OPENROUTER_API_KEY"] = "offline-smoke-fixture"
            env["HOME"] = str(tmp)
            env["XDG_CONFIG_HOME"] = str(tmp / "config")
        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        worker = threading.Thread(target=server.serve_forever, daemon=True)
        worker.start()
        socket = str(tmp / "tmux.sock")

        def tmux(*command, check=True):
            return subprocess.run(["tmux", "-S", socket, *command], env=env, text=True,
                                  capture_output=True, check=check, timeout=10).stdout

        def screen():
            return tmux("capture-pane", "-p", "-t", "smoke", check=False)

        def wait_for(predicate, description, seconds=15):
            deadline = time.monotonic() + seconds
            while time.monotonic() < deadline:
                current = screen()
                if predicate(current):
                    return current
                time.sleep(0.1)
            raise RuntimeError(f"Timed out: {description}\nTerminal:\n{screen()}")

        try:
            command = [str(binary), "-provider", "openrouter", "-model", args.model,
                       "-openrouter-url", f"http://127.0.0.1:{server.server_port}",
                       "-workdir", str(tmp), "-max-steps", "1"]
            tmux("new-session", "-d", "-s", "smoke", "-x", "120", "-y", "40", shlex.join(command))
            wait_for(lambda s: "Type a message" in s, "TUI startup")
            tmux("send-keys", "-t", "smoke", "-l", "Reply with exactly SMOKE_OK. Do not use tools.")
            wait_for(lambda s: "Do not use tools." in s, "typed input rendered")
            time.sleep(0.3)
            with lock:
                assert state["requests"] == 0, "Typing triggered inference before Enter"
            assert "Error:" not in screen(), "Error while typing"
            print("PASS: fresh binary starts and typing renders without inference", flush=True)
            tmux("send-keys", "-t", "smoke", "Enter")
            wait_for(lambda _: state["finished"], "provider response", seconds=60)
            time.sleep(1)
            current = screen()
            with lock:
                diagnostic = json.dumps(state, indent=2)
            if state["failure"] or "Error:" in current or "panic:" in current:
                raise RuntimeError(f"Submission failed\nSafe stream metadata:\n{diagnostic}\nTerminal:\n{current}")
            assert state["requests"] == 1, "Unexpected additional inference requests"
            # The user prompt also contains the sentinel; require another occurrence.
            wait_for(lambda s: s.count("SMOKE_OK") >= 2, "assistant response displayed")
            print("PASS: Enter completes one response without a TUI error", flush=True)
            print("Safe stream metadata: " + json.dumps(state["shapes"]), flush=True)
        finally:
            tmux("kill-server", check=False)
            server.shutdown()
            server.server_close()
            worker.join(timeout=5)


if __name__ == "__main__":
    main()
