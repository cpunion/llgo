"""The optional baseline job must measure its actual source before any upload."""

import contextlib
import hashlib
import importlib.util
import os
from pathlib import Path
import json
import shutil
import subprocess
import tempfile
import unittest
from unittest import mock

import yaml


ROOT = Path(__file__).resolve().parents[2]
SOURCE = "d4b248e8c7790ad7083325ea2b001c16e4a5a8a5"


class BaselineCoverageTests(unittest.TestCase):
    def helper(self):
        path = Path(__file__).with_name("codecov_baseline.py")
        self.assertTrue(path.is_file(), "baseline source guard is required")
        spec = importlib.util.spec_from_file_location("baseline", path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module

    def workflow(self):
        path = ROOT / ".github/workflows/codecov-baseline-d4b.yml"
        self.assertTrue(path.is_file(), "an exact-topic baseline workflow is required")
        return yaml.load(path.read_text(), Loader=yaml.BaseLoader)

    @contextlib.contextmanager
    def fixture(self, module, producer="go"):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "source"
            output = Path(directory) / "evidence"
            names = [".gitignore", ".go-version", ".github/codecov.yml", *module.WORKFLOWS,
                     "_demo/c/cargs/demo.go", ".github/workflows/test_demo.sh"]
            for name in names:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(subprocess.check_output(["git", "show", f"{SOURCE}:{name}"], cwd=ROOT))

            subprocess.run(["git", "init", "-q", str(root)], check=True)
            subprocess.run(["git", "add", "."], cwd=root, check=True)
            subprocess.run(["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                            "commit", "-qm", "unit fixture, not actual baseline"], cwd=root, check=True)
            original_git = module.git

            def git(actual_root, *args):
                self.assertEqual(actual_root, root)
                return SOURCE if args == ("rev-parse", "HEAD") else original_git(root, *args)

            synthetic_qemu = {name: hashlib.sha256(("synthetic dependency fixture: " + name + "\n").encode()).hexdigest()
                              for name in module.QEMU_FILES}
            with mock.patch.object(module, "git", side_effect=git), mock.patch.object(module, "QEMU_FILES", synthetic_qemu):
                self.setup_dependencies(root, producer, module)
                yield root, output

    def setup_dependencies(self, root, producer, module):
        directory = root / "_demo/c/cargs/libs"
        if directory.exists():
            shutil.rmtree(directory)
        if producer != "browser":
            for name in ("cargs_linux_amd64.zip", "conaninfo.txt", "conanmanifest.txt", "include/cargs.h",
                         "lib/libcargs.so", "lib/pkgconfig/cargs.pc.tmpl", "lib/pkgconfig/cargs.pc", "licenses/LICENSE.md"):
                path = directory / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("synthetic dependency fixture: " + name + "\n")
        qemu = root / ".cache/qemu"
        if qemu.exists():
            shutil.rmtree(qemu)
        if producer in ("go", "llgo"):
            for name in module.QEMU_FILES:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("synthetic dependency fixture: " + name + "\n")

    def reports(self, root, result):
        for name in result["files"]:
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("mode: set\nexample.go:1.1,1.2 1 1\n")

    def test_exact_cp_topic_only_all_producers_gate_upload(self):
        workflow = self.workflow()
        self.assertEqual(workflow["on"], {"push": {"branches": ["codex/codecov-base-d4b-20261109"]}})
        jobs = workflow["jobs"]
        self.assertEqual(set(jobs), {"baseline", "upload"})
        self.assertEqual(jobs["baseline"]["strategy"]["matrix"]["producer"],
                         ["go", "llgo", "dev", "browser"])
        self.assertEqual(jobs["upload"]["needs"], "baseline")
        for job in jobs.values():
            self.assertEqual(job["runs-on"], "ubuntu-24.04")
            self.assertEqual(job["if"], "github.repository == 'cpunion/llgo' && github.ref == 'refs/heads/codex/codecov-base-d4b-20261109'")
            self.assertNotIn("continue-on-error", job)
            self.assertNotIn("max-parallel", job.get("strategy", {}))
            for step in job["steps"]:
                self.assertNotIn("continue-on-error", step)
        uploads = [step for step in jobs["upload"]["steps"] if step.get("uses", "").startswith("codecov/")]
        self.assertEqual([step["with"]["flags"] for step in uploads],
                         ["go-tests", "llgo-tests", "dev-lto-globaldce", "go-tests"])
        for step in uploads:
            self.assertEqual(step["with"]["override_commit"], SOURCE)
            self.assertEqual(step["with"]["override_branch"], "codex/ci-base-main-d4b-20261105")
            self.assertEqual(step["with"]["use_oidc"], "true")
            self.assertEqual(step["with"]["fail_ci_if_error"], "true")
            self.assertNotIn("override_pr", step["with"])
            self.assertNotIn("if", step)
        artifact = next(step for step in jobs["baseline"]["steps"]
                        if step.get("uses", "").startswith("actions/upload-artifact@"))
        self.assertEqual(artifact["with"]["path"], "${{ runner.temp }}/base-codecov")
        self.assertEqual(artifact["with"].get("include-hidden-files"), "true")

    def test_every_script_and_report_comes_from_original_workflow(self):
        module = self.helper()
        self.assertEqual(module.SOURCE, SOURCE)
        for producer in module.PRODUCERS:
            with self.subTest(producer=producer), self.fixture(module, producer) as (root, output):
                result = module.prepare(root, output, producer)
                original = yaml.load((root / result["workflow"]).read_text(), Loader=yaml.BaseLoader)
                steps = original["jobs"][result["job"]]["steps"]
                upload = next(step for step in steps if step.get("uses", "").startswith("codecov/"))
                self.assertEqual(result["files"], upload["with"]["files"].split(","))
                self.assertEqual(result["flag"], upload["with"]["flags"])
                for key, name in module.PRODUCERS[producer][2].items():
                    raw = next(step["run"] for step in steps if step.get("name") == name)
                    self.assertEqual((output / f"{key}.sh").read_text(),
                                     raw.replace("${{ matrix.windows_abi }}", ""))
                    subprocess.run(["bash", "-n", str(output / f"{key}.sh")], check=True)
                workflow = self.workflow()
                coverage = next(step for step in workflow["jobs"]["baseline"]["steps"]
                                if step.get("if") == f"matrix.producer == '{producer}'"
                                and step.get("run") == 'bash --noprofile --norc -e -o pipefail "$BASE_COVERAGE/coverage.sh"')
                name = module.PRODUCERS[producer][2]["coverage"]
                source_env = next(step for step in steps if step.get("name") == name).get("env", {})
                expected_env = {key: value.replace("${{ steps.windows_go_runtime.outputs.overlay }}", "")
                                .replace("${{ runner.os == 'Linux' && '1' || '0' }}", "1")
                                for key, value in source_env.items()}
                self.assertEqual(coverage.get("env", {}), expected_env)

    def test_unknown_workflow_expression_is_rejected(self):
        module = self.helper()
        with self.fixture(module) as (root, output):
            module.prepare(root, output, "go")
            path = root / ".github/workflows/go.yml"
            path.write_text(path.read_text().replace("go build -v ./...", "echo '${{ unknown.value }}'"))
            with self.assertRaises(ValueError):
                module.definition(root, "go")

    def test_valid_source_precedes_identity_and_mutation_rejections(self):
        module = self.helper()
        with self.fixture(module) as (root, output):
            module.prepare(root, output, "go")
            with mock.patch.object(module, "git", return_value="wrong"):
                with self.assertRaises(ValueError):
                    module.prepare(root, output, "go")
            module.prepare(root, output, "go")
            (root / ".go-version").write_text("1.27.3\n")
            with self.assertRaises(ValueError):
                module.prepare(root, output, "go")

    def test_complete_reports_and_unchanged_source_are_required(self):
        module = self.helper()
        with self.fixture(module) as (root, output):
            result = module.prepare(root, output, "go")
            for name in result["files"]:
                (root / name).write_text("mode: set\nexample.go:1.1,1.2 1 1\n")
            module.verify(root, output)
            path = root / result["files"][0]
            saved = path.read_bytes()
            path.unlink()
            with self.assertRaises(ValueError):
                module.verify(root, output)
            path.write_bytes(saved)
            module.verify(root, output)
            (root / ".github/codecov.yml").write_text("coverage: {}\n")
            with self.assertRaises(ValueError):
                module.verify(root, output)

    def test_downloads_require_all_four_real_producer_report_sets(self):
        module = self.helper()
        with self.fixture(module) as (root, output):
            for producer in module.PRODUCERS:
                self.setup_dependencies(root, producer, module)
                result = module.prepare(root, output / producer, producer)
                for name in result["files"]:
                    path = root / name
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_text("mode: set\nexample.go:1.1,1.2 1 1\n")
                module.verify(root, output / producer)
                for name in result["files"]:
                    (root / name).unlink()
            self.assertEqual(set(module.verify_downloads(root, output)),
                             {producer + "_files" for producer in module.PRODUCERS})
            path = output / "go/reports/coverage-main-host.txt"
            path.write_text("changed\n")
            with self.assertRaises(ValueError):
                module.verify_downloads(root, output)

    def test_real_git_setup_and_exact_outputs_are_separate_from_source(self):
        module = self.helper()
        with self.fixture(module) as (root, output):
            self.assertIn("?? _demo/c/cargs/libs/", module.git(root, "status", "--porcelain"))
            result = module.prepare(root, output, "go")
            self.assertFalse(any("/libs/" in name for name in result["source_files"]))
            self.assertEqual(len(result["dependencies"]), 19)
            self.reports(root, result)
            for name in ("coverage-main.txt", "coverage-build.txt", "coverage-compiler.txt",
                         "go-test-timings/main.jsonl", "go-test-timings/main-times.json",
                         "go-test-timings/main-times.txt", "go-test-timings/build-shards.json"):
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("synthetic test output\n")
            module.verify(root, output)
            verified = json.loads((output / "verified.json").read_text())
            self.assertEqual(verified["dependencies"], result["dependencies"])
            self.assertIn("go-test-timings/main.jsonl", verified["outputs"])
            self.assertEqual(verified["source_files"], result["source_files"])
            # These reports are permitted only after the selected commands run.
            with self.assertRaises(ValueError):
                module.prepare(root, output / "premature", "go")

    def test_real_git_unknown_source_and_dependency_changes_are_rejected(self):
        module = self.helper()
        with self.fixture(module) as (root, output):
            self.reports(root, module.prepare(root, output, "go"))
            module.verify(root, output)
            for name in ("unexpected.go", "_demo/c/cargs/libs/include/injected.go",
                         "_demo/c/cargs/libs/include/injected.S", "_demo/c/cargs/libs/include/injected.h",
                         "go-test-timings/injected.go", "go-test-timings/injected.c",
                         "go-test-timings/go.mod", "go-test-timings/other.json",
                         "coverage-browser/unknown.out", "coverage-unknown.txt"):
                with self.subTest(name=name):
                    path = root / name
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_text("injected source or unknown output\n")
                    with self.assertRaises(ValueError):
                        module.verify(root, output)
                    path.unlink()
                    module.verify(root, output)
            for name in (".go-version", "_demo/c/cargs/libs/lib/libcargs.so"):
                path = root / name
                original = path.read_bytes()
                path.write_bytes(original + b"changed\n")
                with self.assertRaises(ValueError):
                    module.verify(root, output)
                path.write_bytes(original)
                module.verify(root, output)
            script = output / "build.sh"
            original = script.read_bytes()
            script.write_bytes(original + b"echo changed\n")
            with self.assertRaisesRegex(ValueError, "original command changed"):
                module.verify(root, output)
            script.write_bytes(original)
            module.verify(root, output)
            library = root / "_demo/c/cargs/libs/lib/libcargs.so"
            original = library.read_bytes()
            escaped = output / "outside.so"
            escaped.write_bytes(original)
            library.unlink()
            library.symlink_to(escaped)
            with self.assertRaises(ValueError):
                module.verify(root, output)
            library.unlink()
            library.write_bytes(original)
            module.verify(root, output)

    def test_prepare_records_diagnostics_before_rejecting_untracked_source(self):
        module = self.helper()
        with self.fixture(module) as (root, output):
            (root / "untracked.go").write_text("package injected\n")
            with self.assertRaises(ValueError):
                module.prepare(root, output, "go")
            actual = json.loads((output / "initial-source.json").read_text())
            self.assertEqual(actual["head"], SOURCE)
            self.assertIn("untracked.go", actual["status"])
            step = next(step["run"] for step in self.workflow()["jobs"]["baseline"]["steps"]
                        if step.get("name") == "Materialize the exact original Linux commands")
            for evidence in ("mkdir -p", "source.txt", "git-status.txt", "go-version.txt", "llvm-version.txt"):
                self.assertLess(step.index(evidence), step.index('test "$(go env GOVERSION)"'))

    def test_original_embedded_setup_covers_ignored_payloads_and_rejects_injection(self):
        module = self.helper()
        # Complete union of the two SHA-pinned Linux packages in d4b's installer.
        names = ["bin/qemu-system-riscv32", "bin/qemu-system-xtensa", "include/fdt.h",
                 "include/libfdt.h", "include/libfdt_env.h", "lib/x86_64-linux-gnu/libfdt.a",
                 "lib/x86_64-linux-gnu/pkgconfig/libfdt.pc", "share/qemu/esp32s3_rev0_rom.bin",
                 "share/qemu/esp32-v3-rom.bin", "share/qemu/esp32c3-rom.bin", "share/qemu/esp32-v3-rom-app.bin"]
        with self.fixture(module) as (root, output):
            expected = {}
            for name in names:
                path = root / ".cache/qemu" / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("synthetic dependency fixture: " + path.relative_to(root).as_posix() + "\n")
                expected[path.relative_to(root).as_posix()] = module.digest(path)
            with mock.patch.object(module, "QEMU_FILES", expected, create=True):
                result = module.prepare(root, output, "go")
                self.assertEqual(len(result["dependencies"]), 19)
                self.reports(root, result)
                module.verify(root, output)
                for name in (".cache/qemu/include/extra.go", ".cache/qemu/include/extra.h",
                             ".cache/qemu/share/qemu/extra.bin", ".cache/other/injected.go"):
                    path = root / name
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_text("injected\n")
                    with self.assertRaises(ValueError):
                        module.verify(root, output)
                    path.unlink()
                    module.verify(root, output)
                library = root / ".cache/qemu/lib/x86_64-linux-gnu/libfdt.a"
                saved = library.read_bytes()
                library.write_bytes(saved + b"changed\n")
                with self.assertRaises(ValueError):
                    module.verify(root, output)
                library.write_bytes(saved)
                module.verify(root, output)

    def test_downloads_verify_each_producer_dependency_and_output_archive(self):
        module = self.helper()
        with self.fixture(module) as (root, output):
            for producer in module.PRODUCERS:
                self.setup_dependencies(root, producer, module)
                result = module.prepare(root, output / producer, producer)
                self.reports(root, result)
                module.verify(root, output / producer)
                for name in result["files"]:
                    (root / name).unlink()
            # The upload executor has never installed native demo dependencies.
            self.assertFalse((root / "_demo/c/cargs/libs").exists())
            self.assertEqual(len(module.verify_downloads(root, output)), 4)
            archived = output / "go/dependencies/.cache/qemu/bin/qemu-system-riscv32"
            self.assertTrue(archived.is_file(), "the hidden dependency subtree must reach the artifact")
            extra = output / "go/.unexpected"
            extra.write_text("unrecorded artifact file\n")
            with self.assertRaises(ValueError):
                module.verify_downloads(root, output)
            extra.unlink()
            module.verify_downloads(root, output)
            for name in ("go/dependencies/_demo/c/cargs/libs/lib/libcargs.so", "go/verified.json",
                         "go/outputs/coverage-main-host.txt", "go/build.sh"):
                path = output / name
                original = path.read_bytes()
                path.write_bytes(original.replace(b"sha256", b"tamper") if name.endswith("json")
                                 else original + b"changed")
                with self.assertRaises((ValueError, KeyError)):
                    module.verify_downloads(root, output)
                path.write_bytes(original)
                module.verify_downloads(root, output)

    def test_original_demo_summary_is_output_only_for_selected_demo_command(self):
        module = self.helper()
        for producer in ("dev", "go"):
            with self.subTest(producer=producer), self.fixture(module, producer) as (root, output):
                result = module.prepare(root, output, producer)
                self.reports(root, result)
                module.verify(root, output)
                (root / "result.md").write_text(":white_check_mark: All demo tests passed\n")
                if producer == "dev":
                    module.verify(root, output)
                    manifest = json.loads((output / "verified.json").read_text())
                    self.assertEqual(manifest["outputs"]["result.md"], module.digest(root / "result.md"))
                    self.assertNotIn("result.md", manifest["reports"])
                else:
                    with self.assertRaises(ValueError):
                        module.verify(root, output)

    def test_original_script_failures_stop_before_success_or_partial_reports(self):
        steps = self.workflow()["jobs"]["baseline"]["steps"]
        invocation = next(step["run"] for step in steps if step.get("name") == "Original compiler build")
        for failure in ("false", "false | true"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                script, report = root / "build.sh", root / "coverage.txt"
                success = 'printf "mode: set\\nfixture.go:1.1,1.2 1 1\\n" > "$REPORT"\n'
                environment = dict(os.environ, BASE_COVERAGE=directory, REPORT=str(report))
                script.write_text(success)
                positive = subprocess.run(["bash", "-c", invocation], env=environment, capture_output=True)
                self.assertEqual(positive.returncode, 0)
                self.assertTrue(report.is_file())
                report.unlink()
                script.write_text(failure + '\nprintf "after-failure\\n"\n' + success)
                negative = subprocess.run(["bash", "-c", invocation], env=environment, capture_output=True)
                self.assertNotEqual(negative.returncode, 0)
                self.assertFalse(report.exists())
                self.assertNotIn(b"after-failure", negative.stdout)
        for step in steps:
            if step.get("run", "").startswith("bash ") and "$BASE_COVERAGE/" in step["run"]:
                self.assertTrue(step["run"].startswith("bash --noprofile --norc -e -o pipefail "))


if __name__ == "__main__":
    unittest.main()
