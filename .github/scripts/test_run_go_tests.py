import contextlib
import io
import json
import os
from pathlib import Path
import queue
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock

import run_go_tests


class GoTestReportTests(unittest.TestCase):
    def batch_command(self, root, package, mode="set", extra=()):
        child = root / "fake_go.py"
        if not child.exists():
            child.write_text('''import argparse
import json
import pathlib
import sys
import time

parser = argparse.ArgumentParser()
parser.add_argument('-coverprofile')
parser.add_argument('-covermode')
parser.add_argument('--release')
parser.add_argument('--wrong-mode', action='store_true')
parser.add_argument('--bad-statements', type=int, default=0)
parser.add_argument('package')
args, unknown = parser.parse_known_args()
name = args.package.rsplit('/', 1)[-1]
status = 7 if name == 'Failure' else 0
print(json.dumps(dict(Package=args.package, Test='Test' + name,
                     Action='fail' if status else 'pass', Elapsed=1)), flush=True)
if args.release and name == 'First':
    deadline = time.monotonic() + 10
    while not pathlib.Path(args.release).exists():
        if time.monotonic() > deadline:
            sys.exit(9)
        time.sleep(0.01)
if name != 'Missing':
    mode = 'count' if args.wrong_mode else args.covermode
    hits = 1 if mode == 'set' or name == 'First' else 2
    statements = 2 if args.bad_statements and name == 'Second' else 1
    data = (f'mode: {mode}\\nshared.go:1.1,2.1 {statements} {hits}\\n'
            f'{args.package}.go:1.1,2.1 1 1\\n')
    if name == 'Duplicate':
        data += 'shared.go:1.1,2.1 1 1\\n'
    pathlib.Path(args.coverprofile).write_text(data)
sys.exit(status)
''', encoding="utf-8")
        return [sys.executable, str(child),
                "-coverprofile=" + str(root / "coverage-main.txt"),
                "-covermode=" + mode, *extra, "example.invalid/" + package]

    def run_batch(self, root, package, **kwargs):
        command = self.batch_command(root, package, **kwargs)
        with contextlib.redirect_stdout(io.StringIO()):
            return run_go_tests.run(command, root, "main")

    def test_repeated_stage_retains_both_commands_cases_and_coverage(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            child = root / "fake_go.py"
            child.write_text(
                "import json,pathlib,sys\n"
                "profile=next(a.split('=',1)[1] for a in sys.argv if a.startswith('-coverprofile='))\n"
                "mode=next(a.split('=',1)[1] for a in sys.argv if a.startswith('-covermode='))\n"
                "package=sys.argv[-1]\n"
                "name=package.rsplit('/',1)[-1]\n"
                "pathlib.Path(profile).write_text(f'mode: {mode}\\n{package}.go:1.1,2.1 1 1\\n')\n"
                "print(json.dumps(dict(Package=package,Test='Test'+name,Action='pass',Elapsed=1)))\n",
                encoding="utf-8")
            commands = []
            for package in ("example.invalid/First", "example.invalid/Second"):
                command = [sys.executable, str(child),
                           "-coverprofile=" + str(root / "coverage-main.txt"),
                           "-covermode=set", package]
                commands.append(command)
                with contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(run_go_tests.run(command, root, "main"), 0)
            cases = json.loads((root / "main-times.json").read_text())
            self.assertEqual({case["Test"] for case in cases}, {"TestFirst", "TestSecond"})
            self.assertEqual(len((root / "main.jsonl").read_text().splitlines()), 2)
            profile = (root / "coverage-main.txt").read_text()
            self.assertIn("example.invalid/First.go:1.1,2.1 1 1", profile)
            self.assertIn("example.invalid/Second.go:1.1,2.1 1 1", profile)
            provenance = json.loads((root / "main-runs.json").read_text())
            self.assertTrue(provenance["success"])
            self.assertEqual([r["command"] for r in provenance["runs"]], commands)
            self.assertEqual([r["exit"] for r in provenance["runs"]], [0, 0])
            effective = [r["coverage"]["path"] for r in provenance["runs"]]
            self.assertEqual(len(set(effective)), 2)
            for run in provenance["runs"]:
                self.assertTrue(Path(run["coverage"]["path"]).exists())

    def test_set_or_count_sum_and_counter_schema_rejections(self):
        for mode, expected in (("set", 1), ("count", 3), ("atomic", 3)):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                self.assertEqual(self.run_batch(root, "First", mode=mode), 0)
                self.assertEqual(self.run_batch(root, "Second", mode=mode), 0)
                self.assertIn(f"shared.go:1.1,2.1 1 {expected}\n",
                              (root / "coverage-main.txt").read_text())
        for package, extra in (("Duplicate", ()), ("First", ("--wrong-mode",))):
            with self.subTest(package=package, extra=extra), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                self.assertNotEqual(self.run_batch(root, package, extra=extra), 0)
                self.assertFalse(json.loads((root / "main-runs.json").read_text())["success"])
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            extra = ("--bad-statements=1",)
            self.assertEqual(self.run_batch(root, "First", extra=extra), 0)
            original = (root / "coverage-main.txt").read_bytes()
            self.assertNotEqual(self.run_batch(root, "Second", extra=extra), 0)
            self.assertEqual((root / "coverage-main.txt").read_bytes(), original)
            self.assertIn("statement counts differ",
                          str(json.loads((root / "main-runs.json").read_text())["errors"]))

    def test_failed_or_missing_profile_never_becomes_successful_union(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.assertEqual(self.run_batch(root, "Failure"), 7)
            self.assertEqual(self.run_batch(root, "Second"), 7)
            report = json.loads((root / "main-runs.json").read_text())
            self.assertEqual([r["exit"] for r in report["runs"]], [7, 0])
            self.assertFalse(report["success"])
            self.assertEqual(len(json.loads((root / "main-times.json").read_text())), 2)
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.assertNotEqual(self.run_batch(root, "Missing"), 0)
            self.assertFalse(json.loads((root / "main-runs.json").read_text())["success"])

    def test_foreign_command_target_or_unknown_record_rejects_union(self):
        for mutation in ("command", "target", "missing-command", "unknown-state"):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                self.assertEqual(self.run_batch(root, "First"), 0)
                before = (root / "coverage-main.txt").read_bytes()
                if mutation in ("missing-command", "unknown-state"):
                    record_path = next((root / "main-runs").glob("*/run.json"))
                    record = json.loads(record_path.read_text())
                    if mutation == "missing-command":
                        del record["command"]
                    else:
                        record["state"] = "unknown"
                    record_path.write_text(json.dumps(record))
                extra = ("--foreign-flag",) if mutation == "command" else ()
                target = "foreign" if mutation == "target" else os.getenv("GOOS", "")
                with mock.patch.dict(os.environ, GOOS=target):
                    self.assertNotEqual(self.run_batch(root, "Second", extra=extra), 0)
                self.assertFalse(json.loads((root / "main-runs.json").read_text())["success"])
                self.assertEqual((root / "coverage-main.txt").read_bytes(), before)

    def test_package_named_flag_values_do_not_disappear_from_command_scope(self):
        for flag in ("-coverpkg", "-run"):
            with self.subTest(flag=flag), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                first = (flag, "example.invalid/First")
                second = (flag, "example.invalid/Second")
                self.assertEqual(self.run_batch(root, "First", extra=first), 0)
                original = (root / "coverage-main.txt").read_bytes()
                self.assertNotEqual(self.run_batch(root, "Second", extra=second), 0)
                self.assertFalse(json.loads((root / "main-runs.json").read_text())["success"])
                self.assertEqual((root / "coverage-main.txt").read_bytes(), original)

    def test_coverage_options_do_not_rewrite_flag_values_or_test_arguments(self):
        commands = (
            ["go", "test", "-run", "-coverprofile=literal-regex", "example.invalid/First"],
            ["go", "test", "example.invalid/First", "-args", "-coverprofile=payload"],
            ["go", "test", "example.invalid/First", "--", "-coverprofile=payload"],
        )
        for command in commands:
            with self.subTest(command=command), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                _, record, _ = run_go_tests.claim(command, root, "main")
                self.assertEqual(record["effective_command"], command)
                self.assertIsNone(record["coverage"])
        for payload in (("-run", "-covermode=count"),
                        ("-args", "-covermode=count"), ("--", "-covermode=count")):
            with self.subTest(payload=payload), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                command = ["go", "test", "-coverprofile", str(root / "coverage.txt"),
                           "-covermode=set", *payload]
                batch, record, _ = run_go_tests.claim(command, root, "main")
                self.assertEqual(record["effective_command"][-2:], list(payload))
                (batch / "coverage.txt").write_text("mode: set\nf.go:1.1,2.1 1 1\n")
                run_go_tests.merge_profiles([record], root / "combined.txt")
                self.assertEqual((root / "combined.txt").read_text().splitlines()[0], "mode: set")

    def test_go_boolean_flags_keep_following_coverage_option_visible(self):
        for boolean in ("-trimpath", "-cover", "-work", "-buildvcs", "-benchmem", "-fullpath"):
            with self.subTest(boolean=boolean), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                command = ["go", "test", "-json", boolean,
                           "-coverprofile=" + str(root / "coverage.txt"), "example.invalid/First"]
                batch, record, _ = run_go_tests.claim(command, root, "main")
                self.assertIsNotNone(record["coverage"])
                self.assertEqual(record["effective_command"][-2],
                                 "-coverprofile=" + str(batch / "coverage.txt"))
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            command = ["go", "test", "--trimpath", "--test.coverprofile", str(root / "out"),
                       "-test.benchmem", "example.invalid/First"]
            batch, record, _ = run_go_tests.claim(command, root, "main")
            self.assertEqual(record["effective_command"][3:5],
                             ["--test.coverprofile", str(batch / "coverage.txt")])
            record["packages"] = ["example.invalid/First"]
            self.assertEqual(run_go_tests.command_scope(record)[0], command[:-1])

    def test_unknown_bare_option_cannot_hide_a_possible_coverage_destination(self):
        with tempfile.TemporaryDirectory() as temporary:
            command = ["go", "test", "-custom-bool", "-coverprofile=out", "example.invalid/First"]
            with self.assertRaisesRegex(ValueError, "ambiguous coverage"):
                run_go_tests.claim(command, Path(temporary), "main")

    def test_concurrent_batches_keep_inflight_incomplete_and_finalize_both(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            release = root / "release"
            script = Path(run_go_tests.__file__).resolve()
            first_command = self.batch_command(root, "First", extra=("--release", str(release)))
            second_command = self.batch_command(root, "Second", extra=("--release", str(release)))
            prefix = [sys.executable, str(script), "--output-dir", str(root), "main"]
            with subprocess.Popen(prefix + first_command, stdout=subprocess.PIPE,
                                  stderr=subprocess.PIPE) as first:
                try:
                    deadline = time.monotonic() + 5
                    while not (root / "main.jsonl").exists() or not (root / "main.jsonl").stat().st_size:
                        self.assertLess(time.monotonic(), deadline)
                        time.sleep(0.01)
                    second = subprocess.run(prefix + second_command, capture_output=True, timeout=5)
                    self.assertEqual(second.returncode, 0, second.stderr)
                    report = json.loads((root / "main-runs.json").read_text())
                    self.assertFalse(report["complete"])
                    self.assertFalse(report["success"])
                    self.assertIsNone(first.poll())
                finally:
                    release.touch()
                    stdout, stderr = first.communicate(timeout=5)
                self.assertEqual(first.returncode, 0, stdout + stderr)
            report = json.loads((root / "main-runs.json").read_text())
            self.assertTrue(report["complete"])
            self.assertTrue(report["success"])
            self.assertEqual(len(report["runs"]), 2)
            self.assertEqual(len((root / "main.jsonl").read_text().splitlines()), 2)

    def test_unstarted_command_keeps_provenance_without_completed_union(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            command = [str(root / "missing-program")]
            with self.assertRaises(OSError):
                run_go_tests.run(command, root, "main")
            report = json.loads((root / "main-runs.json").read_text())
            self.assertFalse(report["complete"])
            self.assertFalse(report["success"])
            record = json.loads(next((root / "main-runs").glob("*/run.json")).read_text())
            self.assertEqual(record["command"], command)
            self.assertEqual(record["state"], "incomplete")
            self.assertFalse(record["spawned"])
            self.assertIn("spawn_error", record)
            self.assertIsNone(record["exit"])

    def invoke(self, lines, status=0):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        summary = root / "summary.md"
        command = [sys.executable, "-c", "import sys; print(" +
                   repr("\n".join(lines)) + f"); sys.exit({status})"]
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            result = run_go_tests.run(command, root, "build", summary)
        return root, result, output.getvalue(), summary.read_text()

    def test_preserves_package_identity_nested_cases_and_null_timings(self):
        events = [
            {"Package": "p", "Test": "TestSame/sub|case", "Action": "pass", "Elapsed": 2},
            {"Package": "p", "Test": "TestSame", "Action": "pass", "Elapsed": 3},
            {"Package": "q", "Test": "TestSame", "Action": "skip"},
            {"Package": "p", "Action": "pass", "Elapsed": 4},
        ]
        root, status, _, summary = self.invoke([json.dumps(event) for event in events])
        self.assertEqual(status, 0)
        cases = json.loads((root / "build-times.json").read_text())
        self.assertEqual([(case["Package"], case["Test"]) for case in cases],
                         [("p", "TestSame"), ("p", "TestSame/sub|case"), ("q", "TestSame")])
        self.assertIn("Slowest tests", summary)
        self.assertIn("Slowest subtests", summary)
        self.assertIn("sub&#124;case", summary)
        self.assertEqual(len((root / "build.jsonl").read_text().splitlines()), 4)

    def test_prints_every_case_beyond_the_summary_limit(self):
        events = [{"Package": "p", "Test": f"TestCase{i:02}", "Action": "pass", "Elapsed": i}
                  for i in range(20)]
        root, status, output, summary = self.invoke([json.dumps(event) for event in events])
        self.assertEqual(status, 0)
        timings = (root / "build-times.txt").read_text()
        self.assertIn(timings, output)
        self.assertIn("Parent times include subtests", timings)
        self.assertLess(timings.index("TestCase19"), timings.index("TestCase00"))
        for event in events:
            self.assertIn(event["Test"], timings)
        self.assertNotIn("TestCase00", summary)

    def test_failure_status_and_plain_diagnostics_reach_the_outer_wrapper(self):
        events = [{"Action": "output", "Output": "fatal error: test failure\n"},
                  {"Package": "p", "Test": "TestBroken", "Action": "fail", "Elapsed": 5}]
        root, status, output, summary = self.invoke([json.dumps(event) for event in events], 7)
        self.assertEqual(status, 7)
        self.assertTrue(output.startswith("fatal error: test failure\n"))
        self.assertIn("5.00\tfail\tp\tTestBroken", output)
        self.assertIn("exit 7", summary)
        self.assertEqual(json.loads((root / "build-times.json").read_text())[0]["Action"], "fail")

    def test_unstructured_tool_output_is_visible(self):
        _, status, output, _ = self.invoke(["compiler diagnostic", "[1, 2]"])
        self.assertEqual(status, 0)
        self.assertTrue(output.startswith("compiler diagnostic\n[1, 2]\n"))

    def test_cli_preserves_unicode_and_child_status_with_cp1252_stdout(self):
        package, name = "example.com/\u4e2d\u6587\u2215", "TestUnicode/\u00b7\U0001f642"
        diagnostic = "compiler diagnostic: \u4e2d\u6587 \u2215 \U0001f642\n"
        script = Path(run_go_tests.__file__).resolve()
        for status in (0, 23):
            for timings_only in (False, True):
                with self.subTest(status=status, timings_only=timings_only), \
                        tempfile.TemporaryDirectory() as temporary:
                    root = Path(temporary)
                    terminal = {"Package": package, "Test": name, "Elapsed": 1.25,
                                "Action": "fail" if status else "pass"}
                    lines = [] if timings_only else [
                        json.dumps({"Action": "output", "Output": diagnostic}, ensure_ascii=False),
                        diagnostic.rstrip("\n"), json.dumps([diagnostic], ensure_ascii=False)]
                    lines.append(json.dumps(terminal, ensure_ascii=False))
                    payload = ("\n".join(lines) + "\n").encode("utf-8")
                    source = root / "events.jsonl"
                    source.write_bytes(payload)
                    summary = root / "summary.md"
                    child = ("import pathlib,sys; "
                             "sys.stdout.buffer.write(pathlib.Path(sys.argv[1]).read_bytes()); "
                             "sys.exit(int(sys.argv[2]))")
                    result = subprocess.run(
                        [sys.executable, str(script), "--output-dir", str(root), "build",
                         sys.executable, "-c", child, str(source), str(status)],
                        capture_output=True, timeout=15,
                        env={**os.environ, "PYTHONIOENCODING": "cp1252:strict", "PYTHONUTF8": "0",
                             "GITHUB_STEP_SUMMARY": str(summary)})
                    self.assertEqual(result.returncode, status, result.stderr)
                    rendered = result.stdout.decode("utf-8")
                    if not timings_only:
                        self.assertTrue(rendered.startswith(diagnostic + diagnostic))
                        self.assertIn(json.dumps([diagnostic], ensure_ascii=False), rendered)
                    self.assertEqual((root / "build.jsonl").read_bytes(), payload)
                    self.assertEqual(json.loads((root / "build-times.json").read_text(encoding="utf-8")),
                                     [terminal])
                    timings = (root / "build-times.txt").read_text(encoding="utf-8")
                    self.assertIn(timings, rendered)
                    self.assertIn(f"1.25\t{terminal['Action']}\t{package}\t{name}", timings)
                    report = summary.read_text(encoding="utf-8")
                    self.assertIn(f"exit {status}", report)
                    self.assertIn(package, report)
                    self.assertIn(name, report)

    def test_cli_preserves_unicode_command_diagnostics_with_cp1252_stderr(self):
        with tempfile.TemporaryDirectory() as temporary:
            diagnostic = "\u4e2d\u6587\u2215\U0001f642"
            result = subprocess.run(
                [sys.executable, str(Path(run_go_tests.__file__).resolve()),
                 "--output-dir", temporary, "build", sys.executable, "-c", "pass", diagnostic],
                capture_output=True, timeout=15,
                env={**os.environ, "PYTHONIOENCODING": "cp1252:strict", "PYTHONUTF8": "0"})
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn(diagnostic, result.stderr.decode("utf-8"))

    def test_native_log_pipe_preserves_large_output_stderr_and_exit_status(self):
        repo = Path(__file__).resolve().parents[2]
        payload = ("native output \u4e2d\u6587\r\n".encode("utf-8") * 400000)
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source, log = root / "source", root / "log"
            source.write_bytes(payload)
            child = ("import pathlib,sys; "
                     f"sys.stdout.buffer.write(pathlib.Path({str(source)!r}).read_bytes()); "
                     "sys.stdout.buffer.flush(); "
                     "sys.stderr.buffer.write(b'final diagnostic\\n'); sys.exit(23)")
            result = subprocess.run(
                [sys.executable, str(repo / "dev/run_logged_command.py"), str(log),
                 sys.executable, "-c", child], capture_output=True, timeout=15)
            self.assertEqual(result.returncode, 23, result.stderr)
            self.assertEqual(result.stdout, payload + b"final diagnostic\n")
            self.assertEqual(log.read_bytes(), result.stdout)

    def test_progress_and_json_log_are_visible_before_child_exits(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            release = root / "release"
            event = json.dumps({"Action": "output", "Output": "probe running\n"})
            child = ("import pathlib,time; "
                     f"print({event!r},flush=True); "
                     f"release=pathlib.Path({str(release)!r}); "
                     "exec('while not release.exists(): time.sleep(0.01)')")
            script = Path(run_go_tests.__file__).resolve()
            command = [sys.executable, str(script), "--output-dir", str(root),
                       "build", sys.executable, "-c", child]
            with subprocess.Popen(command, stdout=subprocess.PIPE,
                                  stderr=subprocess.PIPE, text=True) as process:
                lines = queue.Queue()
                reader = threading.Thread(target=lambda: lines.put(process.stdout.readline()))
                reader.start()
                try:
                    self.assertEqual(lines.get(timeout=5), "probe running\n")
                    self.assertIsNone(process.poll())
                    self.assertEqual((root / "build.jsonl").read_text().strip(), event)
                finally:
                    release.touch()
                    reader.join(timeout=5)
                    stdout, stderr = process.communicate(timeout=5)
                self.assertEqual(process.returncode, 0, stdout + stderr)

    def test_windows_quarantine_still_rejects_unrelated_failures_after_json_rendering(self):
        repo = Path(__file__).resolve().parents[2]
        for extra in ("", "--- FAIL: TestRealFailure (0.01s)\n"):
            with self.subTest(extra=extra), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                go = root / "go"
                go.write_text(f"#!{sys.executable}\nprint('github.com/xgo-dev/llgo')\n")
                go.chmod(0o755)
                diagnostic = (
                    "fatal error: receive on synctest channel from outside bubble\n"
                    "testing.(*T).Run\nFAIL github.com/xgo-dev/llgo/test/runtime 0.1s\n" + extra)
                event = json.dumps({"Action": "output", "Output": diagnostic})
                command = ["bash", str(repo / "dev/go_test_windows.sh"), sys.executable,
                           str(repo / ".github/scripts/run_go_tests.py"), "main", sys.executable,
                           "-c", f"import sys; print({event!r}); sys.exit(1)"]
                output = root / "outputs"
                output.touch()
                result = subprocess.run(command, cwd=root, capture_output=True, text=True,
                                        env={**os.environ, "PATH": str(root) + os.pathsep + os.environ["PATH"],
                                             "GITHUB_OUTPUT": str(output),
                                             "GITHUB_STEP_SUMMARY": str(root / "summary")})
                self.assertEqual(result.returncode, 1 if extra else 0, result.stderr)
                self.assertEqual("windows_runtime_corruption=true" in output.read_text(), not extra)


if __name__ == "__main__":
    unittest.main()
