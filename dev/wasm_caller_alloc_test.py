import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class CallerAllocationHarnessTest(unittest.TestCase):
    def run_fixture(self, profile, mode="ok"):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            stub = root / "tool"
            stub.write_text("""#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
tool = Path(sys.argv[0]).name
if tool == 'timeout':
    os.execvp(sys.argv[2], sys.argv[2:])
with open(os.environ['PROBE_LOG'], 'a') as stream:
    stream.write(json.dumps([tool, sys.argv[1:], os.getenv('GOGC'), os.getenv('GOOS')])+'\\n')
mode = os.environ['PROBE_MODE']
if tool == 'llgo':
    if mode != 'missing-module':
        Path(sys.argv[sys.argv.index('-o')+1]).write_bytes(b'module')
else:
    if mode != 'empty':
        print('wasm caller allocation ok')
    if mode == 'panic':
        print('panic: caller stack reuse allocated')
    if mode == 'exit-failure':
        sys.exit(2)
""")
            stub.chmod(0o755)
            for tool in ("timeout", "llgo", "node", "wasmtime"):
                (root / tool).symlink_to(stub)
            log = root / "commands.jsonl"
            environment = dict(os.environ, PATH=str(root)+os.pathsep+os.environ["PATH"],
                               LLGO=str(root / "llgo"), TMPDIR=str(root),
                               PROBE_LOG=str(log), PROBE_MODE=mode)
            script = Path(__file__).with_name("test_wasm_caller_alloc.sh")
            result = subprocess.run(["bash", str(script), profile], env=environment,
                                    capture_output=True, text=True, timeout=15)
            commands = [json.loads(line) for line in log.read_text().splitlines()]
            return result, commands

    def test_all_profiles_and_gc_policies(self):
        for profile in ("EC32", "EC64", "WC32", "GJS", "GWASI"):
            with self.subTest(profile=profile):
                result, commands = self.run_fixture(profile)
                self.assertEqual(result.returncode, 0, result.stdout+result.stderr)
                self.assertEqual([command[2] for command in commands[1:]], ["100", "1"])
                self.assertEqual(commands[1][0], "wasmtime" if profile in ("WC32", "GWASI") else "node")
                expected = {"EC32": "emscripten", "EC64": "emscripten-memory64", "WC32": "wasi"}
                if profile in expected:
                    self.assertIn(expected[profile], commands[0][1])
                else:
                    self.assertNotIn("-target", commands[0][1])
                    self.assertEqual(commands[0][3], "js" if profile == "GJS" else "wasip1")

    def test_missing_or_false_success_is_rejected(self):
        for mode in ("missing-module", "empty", "panic", "exit-failure"):
            with self.subTest(mode=mode):
                result, _ = self.run_fixture("EC64", mode)
                self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
