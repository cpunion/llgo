import json
import os
from pathlib import Path
import subprocess
import sys
import unittest


class WasmTestEnvironmentTest(unittest.TestCase):
    script = Path(__file__).with_name("wasm_test_env.sh")

    def run_command(self, args, environment):
        return subprocess.run(["bash", str(self.script), *args],
                              env=environment, capture_output=True, text=True,
                              timeout=10)

    def test_host_controls_are_removed_but_program_environment_is_preserved(self):
        host = {
            "ACTIONS_ID_TOKEN_REQUEST_TOKEN": "synthetic-credential" * 1024,
            "ACTIONS_ID_TOKEN_REQUEST_URL": "https://example.invalid/oidc",
            "ACTIONS_RUNTIME_TOKEN": "synthetic-runtime-token",
            "GITHUB_ENV": "/host/environment",
            "RUNNER_TEMP": "/host/tmp",
            "LLGO_DIAG_EVIDENCE": "/host/diagnostics",
            "LLGO_R4_BACKEND_TRACE": "/host/bitcode",
        }
        program = {
            "PATH": os.defpath,
            "GOOS": "js", "GOARCH": "wasm", "GOMAXPROCS": "1",
            "GOGC": "1", "GODEBUG": "checkptr=1",
            "GOFLAGS": "-tags=llvm22", "LLGO_BUILD_CACHE": "on",
            "LLGO_ROOT": "/source with spaces", "TMPDIR": "/program/tmp",
            "USER_CASE": "line one\nline two=kept",
            "MY_GITHUB_VARIABLE": "kept", "ACTIONS": "not a host prefix",
        }
        environment = dict(host, **program)
        command = [sys.executable, "-c",
                   "import json, os; print(json.dumps(dict(os.environ)))"]
        result = self.run_command(command, environment)
        self.assertEqual(result.returncode, 0, result.stderr)
        actual = json.loads(result.stdout)
        for key in host:
            self.assertNotIn(key, actual, key)
        for key, value in program.items():
            self.assertEqual(actual.get(key), value, key)

    def test_arguments_and_failure_status_are_preserved(self):
        args = ["two words", "", "literal $HOME; not a command"]
        command = [sys.executable, "-c",
                   "import json, sys; print(json.dumps(sys.argv[1:])); sys.exit(37)",
                   *args]
        result = self.run_command(command, {"PATH": os.defpath})
        self.assertEqual(result.returncode, 37)
        self.assertEqual(json.loads(result.stdout), args)

    def test_missing_command_is_an_error(self):
        result = self.run_command([], {"PATH": os.defpath})
        self.assertEqual(result.returncode, 2)
        self.assertIn("usage:", result.stderr)


if __name__ == "__main__":
    unittest.main()
