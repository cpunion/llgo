"""Guard the integration contract so workflow edits cannot silently bypass policy."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

import yaml


WORKFLOWS = Path(__file__).resolve().parents[1] / "workflows"
PREPARE = "./.github/workflows/ci-prepare.yml"
CODE_WORKFLOWS = {
    "llgo.yml": 22, "go.yml": 6, "targets.yml": 2, "build-cache.yml": 4,
    "benchmark.yml": 9, "release-build.yml": 15, "doc.yml": 6, "fmt.yml": 1,
}


def load(name):
    # BaseLoader preserves the GitHub Actions 'on' key instead of YAML 1.1's True.
    return yaml.load((WORKFLOWS / name).read_text(), Loader=yaml.BaseLoader)


def needs(job):
    value = job.get("needs", [])
    return [value] if isinstance(value, str) else value


def matrix_size(job):
    matrix = job.get("strategy", {}).get("matrix", {})
    axes = [v for k, v in matrix.items() if k not in {"include", "exclude"}]
    if not axes:
        return len(matrix.get("include", [None]))
    count = 1
    for values in axes:
        count *= len(values)
    return count - len(matrix.get("exclude", []))


class WorkflowContractTests(unittest.TestCase):
    def test_gdb_windows_acceptance_covers_both_abis_and_all_native_architectures(self):
        job = load("llgo.yml")["jobs"]["llgo"]
        entries = job["strategy"]["matrix"]["include"]
        windows = {(entry["windows_abi"], entry["windows_arch"])
                   for entry in entries if entry["os"].startswith("windows")}
        self.assertEqual(windows, {(abi, arch) for abi in ("msvc", "mingw")
                                   for arch in ("amd64", "386", "arm64")})
        steps = job["steps"]
        install = next(step for step in steps
                       if step.get("name") == "Install Windows GDB")
        test = next(step for step in steps
                    if step.get("name") == "GDB native values and registry (Windows)")
        self.assertEqual(install["if"], "runner.os == 'Windows'")
        self.assertEqual(test["if"], "runner.os == 'Windows'")
        self.assertNotIn("continue-on-error", test)
        self.assertEqual(test["env"]["LLGO_GDB_INTEGRATION"], "1")
        # Its isolated MSYS2 setup changes the shell helper: it belongs after
        # all existing LLDB/host-shell gates, not before target qualification.
        shell_tests = [index for index, step in enumerate(steps)
                       if step.get("name", "").startswith(("LLDB integration", "Test from the"))]
        self.assertGreater(steps.index(install), max(shell_tests))
        complete = next(step for step in steps
                        if step.get("name") == "GDB complete worker unwind (Windows amd64 and 386)")
        self.assertEqual(complete["if"], "runner.os == 'Windows' && matrix.windows_arch != 'arm64'")
        self.assertIn("^TestGDBCompleteWorkerUnwind$", complete["run"])
        self.assertNotIn("continue-on-error", complete)
        linux = next(step for step in steps
                     if step.get("name") == "GDB values, registry and complete worker unwind (Linux)")
        self.assertIn("^TestGDB(Integration|CompleteWorkerUnwind)$", linux["run"])

    def test_intel_macos_runs_both_native_debuggers(self):
        job = load("llgo.yml")["jobs"]["native-debuggers-intel"]
        self.assertEqual(job["runs-on"], "macos-15-intel")
        commands = "\n".join(step.get("run", "") for step in job["steps"])
        self.assertIn("test/debug/runtime/runtest.sh -v", commands)
        self.assertIn("^TestGDBIntegration$", commands)
        self.assertNotIn("^TestGDBCompleteWorkerUnwind$", commands)
        self.assertIn("exec sudo -n", commands)
        self.assertNotIn("continue-on-error", job)
        self.assertTrue(all("continue-on-error" not in step for step in job["steps"]))

    def test_exact_assembly_regressions_execute_the_nested_module(self):
        jobs = load("llgo.yml")["jobs"]
        self.assertIn("assembly-regressions", jobs,
                      "root ./test/... cannot traverse the nested test/asm module")
        job = jobs["assembly-regressions"]
        self.assertEqual(job["strategy"]["matrix"]["os"],
                         ["ubuntu-24.04", "macos-latest"])
        self.assertEqual(job["strategy"]["fail-fast"], "false")
        self.assertNotIn("continue-on-error", job)
        steps = job["steps"]
        deps = next(step for step in steps
                    if step.get("uses") == "./.github/actions/setup-deps")
        self.assertEqual(deps["with"]["llvm-version"], "22")
        setup = next(step for step in steps
                     if step.get("uses") == "./.github/actions/setup-go")
        self.assertEqual(setup["with"]["go-version"], "1.27.1")
        build = next(step for step in steps
                     if step.get("name") == "Build the checkout compiler")
        self.assertIn("go build", build["run"])
        self.assertIn("./cmd/llgo", build["run"])
        cross = next(step for step in steps
                     if step.get("name") == "Execute required ARM64 memmove counterpart")
        self.assertEqual(cross["if"], "runner.os == 'Linux'")
        self.assertEqual(cross["env"]["PLAN9ASM_CROSS_EXEC"], "1")
        self.assertIn("./internal/plan9asm", cross["run"])
        self.assertIn("^TestRuntimeMemmove", cross["run"])
        install = next(step for step in steps
                       if step.get("name") == "Install pinned ARM64 execution tools")
        self.assertEqual(install["if"], "runner.os == 'Linux'")
        self.assertIn("scripts/install-ci-qemu.sh", install["run"])
        self.assertIn("aarch64-linux-gnu", install["run"])
        for name, command in (("Native Go assembly baselines", "go test"),
                              ("LLGo assembly compile, link and execute", '"$RUNNER_TEMP/assembly-bin/llgo" test')):
            step = next(step for step in steps if step.get("name") == name)
            self.assertEqual(step["working-directory"], "test/asm")
            self.assertIn("set -euo pipefail", step["run"])
            self.assertIn(command, step["run"])
            self.assertIn("-count=1", step["run"])
            self.assertIn("./...", step["run"])
            self.assertNotIn("continue-on-error", step)
            self.assertNotIn("|| true", step["run"])

    def test_failed_assembly_regression_does_not_hide_sibling_packages(self):
        steps = load("llgo.yml")["jobs"]["assembly-regressions"]["steps"]
        step = next(step for step in steps
                    if step.get("name") == "LLGo assembly compile, link and execute")
        packages = ["example.invalid/first", "example.invalid/failing", "example.invalid/last"]
        for case in ("all-pass", "one-failure", "list-failure", "empty-list"):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                log = root / "calls.jsonl"
                executable = root / "go"
                executable.write_text(f"#!{sys.executable}\n" + """
import os, sys
case = os.environ['TEST_ASSEMBLY_CASE']
if sys.argv[1:] != ['list', './...']:
    sys.exit(3)
if case == 'list-failure':
    sys.exit(2)
if case != 'empty-list':
    print(os.environ['TEST_ASSEMBLY_PACKAGES'])
""")
                executable.chmod(0o755)
                compiler = root / "assembly-bin" / "llgo"
                compiler.parent.mkdir()
                compiler.write_text(f"#!{sys.executable}\n" + """
import json, os, sys
with open(os.environ['TEST_ASSEMBLY_LOG'], 'a') as stream:
    stream.write(json.dumps(sys.argv[1:]) + '\\n')
if os.environ['TEST_ASSEMBLY_CASE'] == 'one-failure' and sys.argv[-1].endswith('/failing'):
    sys.exit(1)
""")
                compiler.chmod(0o755)
                result = subprocess.run(["bash", "-c", step["run"]], cwd=root,
                                        capture_output=True, text=True, env={**os.environ,
                                            "PATH": directory + os.pathsep + os.environ["PATH"],
                                            "RUNNER_TEMP": directory,
                                            "TEST_ASSEMBLY_CASE": case,
                                            "TEST_ASSEMBLY_PACKAGES": "\n".join(packages),
                                            "TEST_ASSEMBLY_LOG": str(log)})
                self.assertEqual(result.returncode == 0, case == "all-pass", result.stderr)
                calls = [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []
                if case in {"list-failure", "empty-list"}:
                    self.assertEqual(calls, [])
                else:
                    self.assertEqual([args[-1] for args in calls], packages)
                    for args in calls:
                        self.assertEqual(args[:-1], ["test", "-v", "-count=1", "-timeout=15m"])

    def test_assembly_regressions_preserve_actual_compiler_and_dependency_provenance(self):
        steps = load("llgo.yml")["jobs"]["assembly-regressions"]["steps"]
        build = next(step for step in steps
                     if step.get("name") == "Build the checkout compiler")
        for command in ("git rev-parse HEAD", "go version", "go env GOVERSION",
                        "go list -m -json github.com/xgo-dev/plan9asm",
                        "go version -m", '"$LLVM_CONFIG" --version',
                        '"$LLVM_CONFIG" --bindir', "llc", "shasum -a 256"):
            self.assertIn(command, build["run"])
        self.assertIn('tee "$RUNNER_TEMP/assembly-toolchain.log"', build["run"])
        self.assertIn("22.", build["run"])
        artifact = next(step for step in steps
                        if step.get("name") == "Preserve assembly regression logs")
        self.assertEqual(artifact["if"], "always()")
        self.assertIn("assembly-*.log", artifact["with"]["path"])
        self.assertEqual(artifact["with"]["if-no-files-found"], "error")

    def test_traceback_coverage_uses_bash_on_every_host(self):
        steps = load("go.yml")["jobs"]["test"]["steps"]
        step = next(step for step in steps
                    if step.get("name") == "Test traceback formatter with coverage")
        # PowerShell splits the unquoted -coverprofile=../... argument and Go
        # treats its value as a package. Preserve the shell when moving steps.
        self.assertEqual(step["shell"], "bash")
        self.assertEqual(step["working-directory"], "runtime")
        self.assertIn("-coverprofile=../coverage-traceback.txt", step["run"])

    def test_windows_exception_overlay_reaches_compiler_and_language_tests(self):
        step = next(step for step in load("go.yml")["jobs"]["test"]["steps"]
                    if step.get("id") == "test_coverage")
        script = step["run"].replace("${{ matrix.windows_abi }}", "msvc")
        for runner, overlay in (("Linux", ""), ("Windows", ""),
                                ("Windows", "C:/runner temp/runtime-overlay.json")):
            with self.subTest(runner=runner, overlay=overlay), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                log = root / "commands.jsonl"
                fake_go = root / "go"
                fake_go.write_text(f"#!{sys.executable}\n" + """
import json, os, sys
if sys.argv[1] == 'list':
    print('github.com/xgo-dev/llgo/internal/build')
else:
    with open(os.environ['TEST_GO_LOG'], 'a') as stream:
        stream.write(json.dumps(sys.argv[1:]) + '\\n')
""")
                fake_go.chmod(0o755)
                (root / "dev").mkdir()
                (root / "dev/go_test_windows.sh").write_text('exec "$@"\n')
                subprocess.run(["bash", "-c", script], cwd=root, check=True,
                               capture_output=True, text=True, env={**os.environ,
                                   "PATH": directory + os.pathsep + os.environ["PATH"],
                                   "RUNNER_OS": runner, "LLGO_GO_TEST_OVERLAY": overlay,
                                   "TEST_GO_LOG": str(log)})
                calls = [json.loads(line) for line in log.read_text().splitlines()]
                self.assertEqual(len(calls), 3)
                for package in ("./cl", "./test/go"):
                    args = next(args for args in calls if package in args)
                    self.assertEqual([arg for arg in args if arg.startswith("-overlay=")],
                                     [f"-overlay={overlay}"] if overlay else [])

    def test_code_and_document_jobs_consume_the_shared_decision(self):
        for filename in [*CODE_WORKFLOWS, "doc-link-checker.yml", "model-demo.yml"]:
            workflow = load(filename)
            flag = "run_doc_checks" if filename == "doc-link-checker.yml" else "run_code_ci"
            self.assertEqual(workflow["jobs"]["prepare"]["uses"], PREPARE)
            for name, job in workflow["jobs"].items():
                if name in {"prepare", "ci-gate"}:
                    continue
                with self.subTest(workflow=filename, job=name):
                    self.assertIn("prepare", needs(job))
                    self.assertIn(f"needs.prepare.outputs.{flag} == 'true'", job["if"])

    def test_main_push_is_not_path_filtered_and_cancels_older_main_runs(self):
        for path in WORKFLOWS.glob("*.yml"):
            workflow = load(path.name)
            events = workflow.get("on", {})
            if "push" not in events:
                continue
            with self.subTest(workflow=path.name):
                push = events["push"] or {}
                self.assertIn("main", push.get("branches", []))
                self.assertNotIn("paths", push)
                self.assertNotIn("paths-ignore", push)
                concurrency = workflow["concurrency"]
                # PRs retain their own groups; tags and other events keep
                # unique run IDs and do not cancel each other.
                self.assertEqual(concurrency["cancel-in-progress"],
                                 "${{ github.event_name == 'pull_request' || (github.event_name == 'push' && github.ref == 'refs/heads/main') }}")
                self.assertEqual(concurrency["group"],
                                 "${{ github.workflow }}-${{ github.event_name }}-${{ github.event.pull_request.number || (github.event_name == 'push' && github.ref == 'refs/heads/main' && 'main') || github.run_id }}")

    def test_gated_pr_workflows_always_start_the_prepare_job(self):
        for filename in [*CODE_WORKFLOWS, "doc-link-checker.yml"]:
            workflow = load(filename)
            with self.subTest(workflow=filename):
                trigger = workflow["on"]["pull_request"]
                self.assertNotIn("paths", trigger)
                self.assertNotIn("paths-ignore", trigger)
                self.assertNotIn("if", workflow["jobs"]["prepare"])
                self.assertNotIn("needs", workflow["jobs"]["prepare"])

    def test_platform_coverage_is_preserved(self):
        for filename, expected in CODE_WORKFLOWS.items():
            workflow = load(filename)
            count = sum(matrix_size(job) for name, job in workflow["jobs"].items()
                        if name not in {"prepare", "ci-gate", "release"})
            with self.subTest(workflow=filename):
                self.assertEqual(count, expected)

    def test_release_artifact_dependencies_and_tag_guard_are_preserved(self):
        jobs = load("release-build.yml")["jobs"]
        for consumer, producer in {
            "build": "populate-linux-sysroot", "build-windows": "build",
            "test-windows-artifacts": "build-windows", "prepare-winget": "test-windows-artifacts",
            "test-artifacts": "build",
        }.items():
            self.assertIn(producer, needs(jobs[consumer]))
        self.assertEqual(set(needs(jobs["release"])), {
            "prepare", "prepare-winget", "test-artifacts", "test-windows-artifacts", "populate-linux-sysroot"})
        self.assertIn("startsWith(github.ref, 'refs/tags/')", jobs["release"]["if"])

    def test_runner_policy_is_not_duplicated_in_workflows(self):
        for path in WORKFLOWS.glob("*.yml"):
            workflow = load(path.name)
            for name, job in workflow["jobs"].items():
                with self.subTest(workflow=path.name, job=name):
                    runner = str(job.get("runs-on", ""))
                    self.assertNotIn("qiniu", runner)
                    self.assertNotIn("github.repository_owner", runner)
                    if "needs.prepare" in runner:
                        self.assertIn("prepare", needs(job))

    def test_prepare_exports_each_decision_and_has_no_caller_concurrency(self):
        workflow = load("ci-prepare.yml")
        self.assertNotIn("concurrency", workflow)
        job = workflow["jobs"]["prepare"]
        outputs = workflow["on"]["workflow_call"]["outputs"]
        self.assertEqual(set(outputs), {"run_code_ci", "run_doc_checks", "pr_head_sha",
                                       "pr_merge_base", "linux_runner", "linux_large_runner"})
        for key in outputs:
            self.assertEqual(outputs[key]["value"], f"${{{{ jobs.prepare.outputs.{key} }}}}")
            self.assertEqual(job["outputs"][key], f"${{{{ steps.policy.outputs.{key} }}}}")
        checkout = job["steps"][0]
        self.assertEqual(checkout["with"]["fetch-depth"],
                         "${{ github.event_name == 'pull_request' && '0' || '1' }}")
        self.assertEqual(workflow["permissions"], {"contents": "read"})

    def test_pr_policy_comes_from_base_and_missing_base_enables_all_checks(self):
        step = load("ci-prepare.yml")["jobs"]["prepare"]["steps"][1]
        self.assertEqual(step["env"]["PR_BASE_SHA"], "${{ github.event.pull_request.base.sha }}")
        self.assertEqual(step["env"]["PR_HEAD_SHA"], "${{ github.event.pull_request.head.sha }}")
        for case in ("base-policy", "missing-base-policy", "docs-only"):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as directory:
                repo = Path(directory)

                def git(*args):
                    return subprocess.check_output(["git", *args], cwd=repo,
                                                   text=True, stderr=subprocess.PIPE).strip()

                git("init", "-q")
                git("config", "user.name", "CI Test")
                git("config", "user.email", "ci@example.invalid")
                (repo / "README.md").write_text("base\n")
                scripts = repo / ".github" / "scripts"
                if case != "missing-base-policy":
                    scripts.mkdir(parents=True)
                    for name in ("ci_policy.py", "ci_changes.py"):
                        (scripts / name).write_text((Path(__file__).parent / name).read_text())
                git("add", ".")
                git("commit", "-qm", "base")
                base = git("rev-parse", "HEAD")

                if case == "docs-only":
                    (repo / "README.md").write_text("updated prose\n")
                else:
                    scripts.mkdir(parents=True, exist_ok=True)
                    # A PR-controlled policy would suppress code checks here.
                    (scripts / "ci_policy.py").write_text(
                        "import os\nopen(os.environ['GITHUB_OUTPUT'], 'a').write('run_code_ci=false\\n')\n")
                    (repo / "compiler.go").write_text("package compiler\n")
                git("add", ".")
                git("commit", "-qm", "pr")
                head = git("rev-parse", "HEAD")

                event = repo / "event.json"
                event.write_text(json.dumps({"pull_request": {
                    "base": {"sha": base}, "head": {"sha": head}}}))
                output = repo / "outputs"
                summary = repo / "summary"
                subprocess.run(["bash", "-e", "-o", "pipefail", "-c", step["run"]],
                               cwd=repo, check=True, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, text=True, env={**os.environ,
                                   "GITHUB_EVENT_NAME": "pull_request",
                                   "GITHUB_EVENT_PATH": str(event),
                                   "GITHUB_REPOSITORY_OWNER": "cpunion",
                                   "GITHUB_WORKSPACE": str(repo),
                                   "GITHUB_OUTPUT": str(output),
                                   "GITHUB_STEP_SUMMARY": str(summary),
                                   "RUNNER_TEMP": directory,
                                   "PR_BASE_SHA": base, "PR_HEAD_SHA": head})
                values = dict(line.split("=", 1) for line in output.read_text().splitlines())
                self.assertEqual(values["run_code_ci"],
                                 "false" if case == "docs-only" else "true")
                self.assertEqual(values["run_doc_checks"], "true")
                self.assertEqual(values["pr_head_sha"], head)
                self.assertEqual(values["pr_merge_base"], base)

    def test_each_prepared_workflow_has_an_unskippable_gate(self):
        names = set()
        for filename in [*CODE_WORKFLOWS, "doc-link-checker.yml", "model-demo.yml"]:
            gate = load(filename)["jobs"]["ci-gate"]
            with self.subTest(workflow=filename):
                self.assertNotIn(gate["name"], names)
                names.add(gate["name"])
                self.assertEqual(needs(gate), ["prepare"])
                self.assertEqual(gate["if"], "always()")
                self.assertIn("needs.prepare.result", gate["steps"][0]["run"])
                self.assertIn('= "success"', gate["steps"][0]["run"])

    def test_policy_tests_cannot_skip_themselves(self):
        workflow = load("ci-policy.yml")
        self.assertNotIn("paths", workflow["on"]["pull_request"])
        job = workflow["jobs"]["policy"]
        self.assertNotIn("if", job)
        self.assertNotIn("needs", job)
        self.assertTrue(any("unittest discover" in step.get("run", "") for step in job["steps"]))

    def test_benchmarks_use_the_prepared_revision_pair(self):
        for name in ("benchmark", "wasm-benchmark"):
            steps = load("benchmark.yml")["jobs"][name]["steps"]
            for step in steps:
                self.assertNotIn("git merge-base", step.get("run", ""))
                options = step.get("with", {})
                path = options.get("path", "")
                if path.endswith("base-source"):
                    self.assertEqual(options["ref"], "${{ needs.prepare.outputs.pr_merge_base }}")
                if path.endswith("head-source"):
                    self.assertEqual(options["ref"], "${{ needs.prepare.outputs.pr_head_sha }}")

    def test_main_external_benchmark_dispatch_has_no_change_filter(self):
        job = load("notify-benchmarks.yml")["jobs"]["dispatch"]
        request = next(step for step in job["steps"] if step.get("name", "").startswith("Request benchmarks"))
        self.assertNotIn("if", request)
        self.assertEqual(job["if"], "github.repository == 'xgo-dev/llgo' && github.event_name != 'pull_request'")

    def test_wasm_baseline_metadata_requires_a_successful_measurement(self):
        steps = load("benchmark.yml")["jobs"]["wasm-benchmark"]["steps"]
        record = next(step for step in steps
                      if step.get("name") == "Record WebAssembly benchmark result")
        for key in ("baseline-benchmark-file", "baseline-repository", "baseline-sha", "baseline-ref"):
            with self.subTest(input=key):
                expression = " ".join(record["with"][key].split())
                self.assertTrue(expression.startswith(
                    "${{ github.event_name == 'pull_request' && "
                    "steps.measure-wasm-base.outcome == 'success' && "))
                self.assertTrue(expression.endswith(" || '' }}"))

    def test_publish_jobs_require_their_own_artifacts(self):
        jobs = load("benchmark-publish.yml")["jobs"]
        self.assertEqual(jobs["artifacts"]["if"], "github.event.workflow_run.conclusion == 'success'")
        for name, suite in (("publish", "baseline"), ("publish-wasm", "wasm")):
            self.assertIn("artifacts", needs(jobs[name]))
            self.assertEqual(jobs[name]["if"], f"needs.artifacts.outputs.{suite} == 'true'")

    def test_artifact_probe_handles_empty_partial_and_expired_results(self):
        self.assertIsNotNone(shutil.which("node"),
                             "Node is required to exercise the CI artifact probe, not an optional skip")
        script = load("benchmark-publish.yml")["jobs"]["artifacts"]["steps"][0]["with"]["script"]
        cases = [[], [{"name": "unrelated", "expired": False}],
                 [{"name": "go-benchmark-llgo-baseline-linux", "expired": True}],
                 [{"name": "go-benchmark-llgo-baseline-linux", "expired": False}],
                 [{"name": "go-benchmark-llgo-wasm-linux", "expired": False}],
                 [{"name": "go-benchmark-llgo-baseline-linux", "expired": False},
                  {"name": "go-benchmark-llgo-wasm-linux", "expired": False}]]
        harness = """
          const { script, cases } = JSON.parse(require('fs').readFileSync(0, 'utf8'));
          const AsyncFunction = Object.getPrototypeOf(async function(){}).constructor;
          (async () => {
            const results = [];
            for (const artifacts of cases) {
              const output = {};
              const github = {
                rest: { actions: { listWorkflowRunArtifacts: 'list' } },
                paginate: async (method, args) => {
                  if (method !== 'list' || args.run_id !== 123 || args.per_page !== 100)
                    throw new Error('must paginate artifacts from the triggering run');
                  return artifacts;
                }
              };
              await new AsyncFunction('github', 'context', 'core', script)(github,
                { repo: { owner: 'xgo-dev', repo: 'llgo' }, payload: { workflow_run: { id: 123 } } },
                { setOutput: (key, value) => { output[key] = value; }, info: () => {} });
              results.push(output);
            }
            console.log(JSON.stringify(results));
          })().catch(error => { console.error(error); process.exitCode = 1; });
        """
        result = subprocess.run(["node", "-e", harness],
                                input=json.dumps({"script": script, "cases": cases}),
                                text=True, capture_output=True, check=True)
        self.assertEqual(json.loads(result.stdout), [
            {"baseline": "false", "wasm": "false"}, {"baseline": "false", "wasm": "false"},
            {"baseline": "false", "wasm": "false"}, {"baseline": "true", "wasm": "false"},
            {"baseline": "false", "wasm": "true"}, {"baseline": "true", "wasm": "true"},
        ])


if __name__ == "__main__":
    unittest.main()
