#!/usr/bin/env python3
"""Check browser-runner teardown when a browser descendant retains stderr."""

import base64
import hashlib
import http.server
import json
import os
from pathlib import Path
import shlex
import signal
import struct
import subprocess
import sys
import tempfile
import unittest


def fake_chrome():
    profile = Path(next(arg.split("=", 1)[1] for arg in sys.argv
                        if arg.startswith("--user-data-dir=")))
    # Model a browser helper that outlives the main browser process while
    # retaining the inherited diagnostic pipe. The test owns its cleanup.
    child = subprocess.Popen(
        [sys.executable, "-c", "import time; time.sleep(60)"],
        stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
        stderr=sys.stderr, start_new_session=True,
    )
    Path(os.environ["LLGO_BROWSER_TEST_CHILD_PID"]).write_text(str(child.pid))

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_PUT(self):
            data = json.dumps({
                "webSocketDebuggerUrl": f"ws://127.0.0.1:{self.server.server_port}/ws",
            }).encode()
            self.send_response(200)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self):
            key = self.headers["Sec-WebSocket-Key"]
            digest = hashlib.sha1(
                (key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()
            ).digest()
            self.send_response(101)
            self.send_header("Upgrade", "websocket")
            self.send_header("Connection", "Upgrade")
            self.send_header("Sec-WebSocket-Accept", base64.b64encode(digest).decode())
            self.end_headers()
            while True:
                header = self.rfile.read(2)
                if not header:
                    return
                length = header[1] & 127
                if length == 126:
                    length = struct.unpack("!H", self.rfile.read(2))[0]
                elif length == 127:
                    length = struct.unpack("!Q", self.rfile.read(8))[0]
                mask = self.rfile.read(4) if header[1] & 128 else None
                data = self.rfile.read(length)
                if mask:
                    data = bytes(value ^ mask[i % 4] for i, value in enumerate(data))
                if header[0] & 15 == 8:
                    return
                request = json.loads(data)
                result = {}
                if request["method"] == "Runtime.evaluate":
                    state = os.environ["LLGO_BROWSER_TEST_RESULT"]
                    result = {"result": {"value": json.dumps({
                        "url": "http://fixture/", "result": state,
                        "text": "pass" if state == "pass" else "intentional browser failure",
                    })}}
                reply = json.dumps({"id": request["id"], "result": result}).encode()
                frame = (bytes([129, len(reply)]) if len(reply) < 126
                         else bytes([129, 126]) + struct.pack("!H", len(reply)))
                self.wfile.write(frame + reply)
                self.wfile.flush()

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    (profile / "DevToolsActivePort").write_text(f"{server.server_port}\n")
    server.serve_forever()


class BrowserRunnerTest(unittest.TestCase):
    def check_result(self, state, expected_code):
        repo = Path(__file__).resolve().parent.parent
        runner = os.environ.get("LLGO_BROWSER_RUNNER", str(
            repo / "internal/build/testdata/wasm-workers/browser-runner.mjs"))
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            browser = root / "fake-chrome"
            browser.write_text(
                "#!/bin/sh\nexec " + shlex.quote(sys.executable) + " "
                + shlex.quote(str(Path(__file__).resolve())) + ' --fake-chrome "$@"\n')
            browser.chmod(0o755)
            pid_file = root / "child.pid"
            env = dict(os.environ, LLGO_BROWSER_TEST_CHILD_PID=str(pid_file),
                       LLGO_BROWSER_TEST_RESULT=state)
            try:
                result = subprocess.run(
                    [os.environ.get("NODE", "node"), runner, str(browser), "http://fixture/"],
                    env=env, capture_output=True, text=True, timeout=8,
                )
                self.assertEqual(result.returncode, expected_code, result.stderr)
                if state == "pass":
                    self.assertEqual(result.stdout.strip(), "pass")
                else:
                    self.assertIn("intentional browser failure", result.stderr)
            finally:
                if pid_file.exists():
                    try:
                        os.kill(int(pid_file.read_text()), signal.SIGKILL)
                    except ProcessLookupError:
                        pass

    def test_success_exits_with_inherited_stderr(self):
        self.check_result("pass", 0)

    def test_failure_exits_with_inherited_stderr(self):
        self.check_result("fail", 1)


if __name__ == "__main__":
    if "--fake-chrome" in sys.argv:
        fake_chrome()
    else:
        unittest.main()
