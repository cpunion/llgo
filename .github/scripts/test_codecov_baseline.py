"""The optional baseline job must measure its actual source before any upload."""

import contextlib
import importlib.util
import os
from pathlib import Path
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
    def fixture(self, module):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "source"
            output = Path(directory) / "evidence"
            names = [".go-version", ".github/codecov.yml", *module.WORKFLOWS]
            for name in names:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(subprocess.check_output(["git", "show", f"{SOURCE}:{name}"], cwd=ROOT))

            def git(actual_root, *args):
                self.assertEqual(actual_root, root)
                return {("rev-parse", "HEAD"): SOURCE,
                        ("status", "--porcelain"): "",
                        ("ls-files", "-z"): "\0".join(names) + "\0"}[args]

            with mock.patch.object(module, "git", side_effect=git):
                yield root, output

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

    def test_every_script_and_report_comes_from_original_workflow(self):
        module = self.helper()
        self.assertEqual(module.SOURCE, SOURCE)
        for producer in module.PRODUCERS:
            with self.subTest(producer=producer), self.fixture(module) as (root, output):
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
                result = module.prepare(root, output / producer, producer)
                for name in result["files"]:
                    path = root / name
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_text("mode: set\nexample.go:1.1,1.2 1 1\n")
                module.verify(root, output / producer)
            self.assertEqual(set(module.verify_downloads(root, output)),
                             {producer + "_files" for producer in module.PRODUCERS})
            path = output / "go/reports/coverage-main-host.txt"
            path.write_text("changed\n")
            with self.assertRaises(ValueError):
                module.verify_downloads(root, output)

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
