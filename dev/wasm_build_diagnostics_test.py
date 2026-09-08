import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest
from unittest.mock import MagicMock, patch


spec = importlib.util.spec_from_file_location(
    "diagnostics", Path(__file__).with_name("wasm_build_diagnostics.py"))
diag = importlib.util.module_from_spec(spec)
spec.loader.exec_module(diag)


class DiagnosticsTest(unittest.TestCase):
    def test_compiler_args_are_transparent_except_tracing(self):
        self.assertEqual(diag.compiler_args(["build", "-o", "a b.wasm", "."]),
                         ["build", "-x", "-o", "a b.wasm", "."])
        for args in ([], ["version"], ["build", "-x", "."]):
            self.assertEqual(diag.compiler_args(args), args)

    def test_wasm_inputs_exclude_outputs_and_keep_in_place_input(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source, output = root / "input.wasm", root / "output.wasm"
            source.write_bytes(b"wasm")
            output.write_bytes(b"")
            args = ["--asyncify", "-Os", str(source), "-o", str(output)]
            self.assertEqual(list(diag.wasm_inputs(args)), [source])
            self.assertEqual(diag.output_path(args), output)
            self.assertIsNone(diag.output_path(["-Os"]))
            self.assertEqual(list(diag.wasm_inputs([str(source), str(source), "-o", str(source)])),
                             [source])

    def test_original_limits_and_only_two_cases(self):
        args = diag.runner_args("runner", "goroot", "llgo", "report")
        for arg in ("-wasm-profile=GWASI", "-build-timeout=180s", "-run-timeout=60s",
                    "-max-rss-mib=4096", "-keepwork", "-shard-total=1",
                    "-case=^(cmplxdivide\\.go|fixedbugs/issue5162\\.go)$"):
            self.assertIn(arg, args)
        self.assertFalse(any("failfast" in arg or "BINARYEN" in arg for arg in args))

    def test_wrapper_records_without_environment_and_preserves_failure(self):
        for kind, returncode in (("llgo", 7), ("wasm-opt", -9)):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                source = root / "original.go"
                source.write_text("package main\nfunc main(){}\n")
                wasm = root / "in.wasm"
                wasm.write_bytes(b"wasm-input")
                output = root / "out.wasm"
                output.write_bytes(b"wasm-output")
                args = (["build", "-o", str(output), str(source)] if kind == "llgo" else
                        ["--asyncify", "-Os", str(wasm), "-o", str(output)])
                environment = {"LLGO_DIAG_EVIDENCE": str(root / "evidence"),
                               "LLGO_DIAG_REAL_LLGO": "/real/llgo",
                               "LLGO_DIAG_REAL_WASMOPT": "/real/wasm-opt",
                               "PRIVATE_TOKEN": "must-not-be-recorded"}
                with patch.dict(os.environ, environment), patch.object(Path, "cwd", return_value=root), \
                     patch.object(diag.subprocess, "run", return_value=subprocess.CompletedProcess([], returncode)) as run:
                    status = diag.run_tool(kind, args)
                self.assertEqual(status, 7 if kind == "llgo" else 137)
                command = run.call_args.args[0]
                self.assertEqual(command[:3], ["/usr/bin/time", "-v", "-o"])
                self.assertEqual(command[4], "/real/" + kind)
                logs = list((root / "evidence" / "commands").glob("*/command.json"))
                self.assertEqual(len(logs), 1)
                text = logs[0].read_text()
                self.assertNotIn("must-not-be-recorded", text)
                record = json.loads(text)
                self.assertEqual(record["returncode"], returncode)
                self.assertIn("finished", record)
                saved = Path(record["inputs"][0]["saved_as"])
                self.assertEqual(saved.read_bytes(), source.read_bytes() if kind == "llgo" else wasm.read_bytes())
                if kind == "llgo":
                    self.assertEqual(Path(record["output"]["saved_as"]).read_bytes(), output.read_bytes())

    def test_ps_uses_no_argv_or_environment(self):
        with tempfile.TemporaryDirectory() as directory:
            stop = threading.Event()
            def sample(*args, **kwargs):
                self.assertEqual(args[0], ["ps", "-eo", "pid,ppid,pgid,rss,pcpu,comm"])
                stop.set()
            with patch.object(diag.subprocess, "run", side_effect=sample):
                diag.sample_processes(stop, Path(directory) / "ps.log")

    def test_driver_retains_both_case_failures_and_original_status(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            process = MagicMock()
            lines = ["=== RUN cmplxdivide.go\n", "--- FAIL cmplxdivide.go\n",
                     "=== RUN fixedbugs/issue5162.go\n", "--- FAIL fixedbugs/issue5162.go\n"]
            process.stdout = iter(lines)
            process.wait.return_value = 1
            process.__enter__.return_value = process
            with patch.object(diag.sys, "platform", "linux"), \
                 patch.object(diag.shutil, "which", return_value="/real/wasm-opt"), \
                 patch.object(diag, "sample_processes"), \
                 patch.object(diag.subprocess, "Popen", return_value=process) as popen:
                status = diag.run_diagnostics(["--llgo", "/real/llgo", "--runner", "/real/runner",
                                               "--goroot", "/real/goroot", "--output", str(root / "run")])
            self.assertEqual(status, 1)
            evidence = root / "run" / "evidence"
            self.assertEqual((evidence / "goroot.log").read_text(), "".join(lines))
            self.assertEqual(json.loads((evidence / "result.json").read_text())["returncode"], 1)
            self.assertTrue((root / "run" / "work").is_dir())
            for name in ("llgo", "wasm-opt"):
                self.assertEqual((root / "run" / "bin" / name).resolve(), Path(diag.__file__).resolve())
            env = popen.call_args.kwargs["env"]
            self.assertEqual(env["WASMOPT"], str(root.resolve() / "run" / "bin" / "wasm-opt"))
            self.assertNotIn("BINARYEN_PASS_DEBUG", env)
            self.assertEqual(env.get("BINARYEN_CORES"), os.environ.get("BINARYEN_CORES"))


if __name__ == "__main__":
    unittest.main()
