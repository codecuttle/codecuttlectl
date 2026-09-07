#!/usr/bin/env python3
"""Offline R1 characterization of freshly built CLI modes; NOT parity acceptance.

Runs text/catalog, multi-tool failure/correction, and max-step scenarios through
TUI, REPL and one-shot. No live API, credentials, or installed binaries are used.
Known differences are asserted and must be replaced in their repair PRs.
"""
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = Path(__file__).resolve().parent.parent
PROMPT = "Run the offline runtime scenario."
NATIVES = {"todo_manage", "tool_info", "get_skill", "scaffold_plugin", "reload_plugins"}


def tool(identity, command):
    return {"id": identity, "type": "function", "function": {
        "name": "bash_exec", "arguments": json.dumps({"command": command})}}


def run_case(binary, plugins, mode, scenario, root):
    work = root / f"{mode}-{scenario}"
    work.mkdir()
    records, errors = [], []
    lock = threading.Lock()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            if self.path != "/models":
                self.send_error(404)
                return
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b'{"data":[{"id":"fixture","context_length":1048576}]}')

        def do_POST(self):
            if self.path != "/chat/completions":
                self.send_error(404)
                return
            request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            # Identify auxiliary title requests by content, not by !stream:
            # one-shot primary inference is also non-streaming.
            messages = request.get("messages", [])
            title = len(messages) == 1 and str(messages[0].get("content", "")).startswith("Generate a 2-5 word title")
            if title:
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{"choices":[{"message":{"content":"Runtime fixture"},"finish_reason":"stop"}]}')
                return
            with lock:
                records.append(request)  # JSON decoding owns a fresh immutable snapshot
                step = len(records)
            if step > 3:
                with lock:
                    errors.append("scenario exceeded three primary requests")
                self.send_error(400)
                return
            calls = []
            if scenario in ("multi", "limit") and step == 1:
                calls = [tool("first", "printf A >> effects"),
                         tool("failure", "printf './main.go:1:1: undefined: missingSymbol\\n' >&2; exit 1")]
            elif scenario == "multi" and step == 2:
                calls = [tool("corrected", "printf B >> effects")]
            finish = "tool_calls" if calls else "stop"
            content = "" if calls else "RUNTIME_DONE"
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream" if request.get("stream") else "application/json")
            self.end_headers()
            if request.get("stream"):
                delta = {"tool_calls": [dict(c, index=i) for i, c in enumerate(calls)]} if calls else {"content": content}
                payloads = [{"choices": [{"index": 0, "delta": delta, "finish_reason": finish}]},
                            {"usage": {"prompt_tokens": 100, "completion_tokens": 2}}]
                for payload in payloads:
                    self.wfile.write(("data: " + json.dumps(payload) + "\n\n").encode())
                self.wfile.write(b"data: [DONE]\n\n")
            else:
                self.wfile.write(json.dumps({"choices": [{"message": {"role": "assistant", "content": content, "tool_calls": calls},
                                                         "finish_reason": finish}],
                                            "usage": {"prompt_tokens": 100, "completion_tokens": 2}}).encode())

    env = {key: value for key, value in os.environ.items()
           if key in ("PATH", "LANG", "LC_ALL", "TMPDIR")}
    env.update(HOME=str(work), XDG_CONFIG_HOME=str(work / "config"),
               XDG_DATA_HOME=str(work / "data"), TERM="xterm-256color",
               OPENROUTER_API_KEY="offline-fixture")
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    command = [str(binary), "--provider", "openrouter", "--model", "fixture",
               "--openrouter-url", f"http://127.0.0.1:{server.server_port}",
               "--workdir", str(work), "--plugin-dir", str(plugins),
               "--max-steps", "1" if scenario == "limit" else "4"]
    socket = str(work / "tmux.sock")

    def tmux(*args, check=True):
        return subprocess.run(["tmux", "-S", socket, *args], env=env, capture_output=True,
                              text=True, check=check, timeout=10).stdout

    def wait_screen(needle):
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            screen = tmux("capture-pane", "-p", "-t", "runtime", check=False)
            if needle in screen:
                return screen
            time.sleep(0.05)
        raise AssertionError(f"{mode}/{scenario}: missing {needle!r}\n{screen}")

    try:
        if mode == "tui":
            tmux("new-session", "-d", "-s", "runtime", "-x", "120", "-y", "45", shlex.join(command))
            wait_screen("Type a message")
            tmux("send-keys", "-t", "runtime", "-l", PROMPT)
            wait_screen(PROMPT)
            with lock:
                assert not records, "typing triggered inference before Enter"
            tmux("send-keys", "-t", "runtime", "Enter")
            output = wait_screen("RUNTIME_DONE")
            assert "Error:" not in output and "panic:" not in output, output
        else:
            command += ["--message", PROMPT] if mode == "one-shot" else ["--no-tui"]
            process = subprocess.run(command, env=env, cwd=work, input=None if mode == "one-shot" else PROMPT + "\nexit\n",
                                     capture_output=True, text=True, timeout=30)
            output = process.stdout + process.stderr
            if scenario == "limit":
                assert "exceeded maximum steps (1)" in output, output
                assert process.returncode == (1 if mode == "one-shot" else 0), output
            else:
                assert process.returncode == 0 and "RUNTIME_DONE" in output, output
        with lock:
            trace = list(records)
            assert not errors, errors
        expected_count = 1 if scenario == "text" or (scenario == "limit" and mode != "tui") else 2 if scenario == "limit" else 3
        assert len(trace) == expected_count, (mode, scenario, len(trace), expected_count)
        names = {t["function"]["name"] for t in trace[0].get("tools", [])}
        assert names == ({"bash_exec"} if mode == "tui" else NATIVES | {"bash_exec"}), names
        if scenario != "text":
            assert (work / "effects").read_text() == ("AB" if scenario == "multi" else "A"), "wrong side effects"
        if len(trace) >= 2:
            results = [m for m in trace[1]["messages"] if m["role"] == "tool"]
            assert [m["tool_call_id"] for m in results] == ["first", "failure"], results
            assert "undefined: missingSymbol" in results[1]["content"], results
            assert "Command exited with code 1" in results[1]["content"], results
            system = "\n".join(m["content"] for m in trace[1]["messages"] if m["role"] == "system")
            assert "Inkwell Diagnostic Alert" not in system, "baseline advice injection changed"
        if scenario == "multi":
            results = [m["tool_call_id"] for m in trace[2]["messages"] if m["role"] == "tool"]
            assert results == ["first", "failure", "corrected"], results
        print(f"PASS baseline {mode}/{scenario}: requests={len(trace)}, native_tools={len(names & NATIVES)}", flush=True)
    finally:
        if mode == "tui":
            tmux("kill-server", check=False)
        server.shutdown()
        server.server_close()
        worker.join(timeout=5)


def main():
    with tempfile.TemporaryDirectory(prefix="codecuttle-runtime-") as tmp:
        root = Path(tmp)
        binary = root / "codecuttlectl"
        plugins = root / "plugins"
        plugins.mkdir()
        for target, source in [(binary, "./cmd/codecuttlectl"), (plugins / "cuttlebone-bash-exec", "./plugins/cuttlebone-bash-exec")]:
            subprocess.run(["go", "build", "-o", str(target), source], cwd=ROOT, check=True, timeout=300)
        for scenario in ("text", "multi", "limit"):
            for mode in ("tui", "repl", "one-shot"):
                run_case(binary, plugins, mode, scenario, root)


if __name__ == "__main__":
    main()
