"""Strict current-runtime replay; no retries or expected-failure exemptions."""

import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time


ROOT = Path.cwd()
LLGO = os.getenv("LLGO_ISSUE_REPRO_COMPILER", "llgo")
OUTPUT = Path(os.getenv("LLGO_ISSUE_REPRO_OUTPUT", str(ROOT / "native-followups-results"))).resolve()
OUTPUT.mkdir(parents=True, exist_ok=True)
RESULTS = []


def save():
    (OUTPUT / "results.json").write_text(json.dumps(RESULTS, indent=2) + "\n")


def run(name, args, cwd=ROOT, timeout=90, env=None):
    started = time.monotonic()
    with (OUTPUT / (name + ".log")).open("wb") as log:
        process = subprocess.Popen(args, cwd=cwd, env=env, stdout=log, stderr=log,
                                   start_new_session=os.name != "nt")
        try:
            status = process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            try:
                debugger = os.getenv("LLGO_LLDB")
                if debugger:
                    with (OUTPUT / (name + "-threads.log")).open("wb") as stacks:
                        subprocess.run([debugger, "-b", "-o", f"process attach --pid {process.pid}",
                                        "-o", "thread backtrace all", "-o", "process detach", "-o", "quit"],
                                       stdout=stacks, stderr=stacks, timeout=30, check=False)
                if os.name == "nt":
                    with (OUTPUT / (name + "-processes.json")).open("wb") as snapshot:
                        subprocess.run(
                            ["powershell", "-NoProfile", "-Command",
                             "Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,Name,CommandLine | ConvertTo-Json"],
                            stdout=snapshot, stderr=snapshot, timeout=20,
                        )
            except (OSError, subprocess.TimeoutExpired) as error:
                log.write(("Diagnostic sampling failed: " + str(error) + "\n").encode())
            finally:
                if os.name == "nt":
                    try:
                        subprocess.run(["taskkill", "/F", "/T", "/PID", str(process.pid)],
                                       stdout=log, stderr=log, timeout=15, check=False)
                    finally:
                        if process.poll() is None:
                            process.kill()
                else:
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                process.wait(timeout=10)
            RESULTS.append(dict(name=name, status="timeout", limitSeconds=timeout,
                                elapsedSeconds=time.monotonic() - started))
            save()
            raise
    result = dict(name=name, exitCode=status, elapsedSeconds=time.monotonic() - started)
    RESULTS.append(result)
    save()
    print(json.dumps(result), flush=True)
    if status:
        raise RuntimeError(f"{name}: exit {status}; see retained log")


def main():
    identity = dict(
        revision=subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(),
        runtimeBase="c0d07ce2b58c8f2f62b25133234486d239021ee1",
        target=dict(goos=os.getenv("GOOS"), goarch=os.getenv("GOARCH"),
                    abi=os.getenv("LLGO_WINDOWS_ABI")),
        cases={},
    )
    (OUTPUT / "identity.json").write_text(json.dumps(identity, indent=2) + "\n")
    # The timeout patch and this fork-only harness do not change runtime code.
    subprocess.run(["git", "diff", "--exit-code", identity["runtimeBase"], "HEAD", "--", "runtime"], check=True)
    run("compiler-version", [LLGO, "version"])
    run("go-version", ["go", "version"])
    sources = ROOT / ".github" / "issue-repro" / "2780" / "go1267"
    manifest = json.loads((sources / "sources.json").read_text())
    identity["upstreamSources"] = manifest
    for filename, repetitions in [("issue25897a.go", 100), ("issue79186.go", 15), ("issue30041.go", 10)]:
        source = sources / filename
        digest = hashlib.sha256(source.read_bytes()).hexdigest()
        if digest != manifest["sha256"][filename]:
            raise ValueError("original Go source changed: " + filename)
        folder = OUTPUT / source.stem
        folder.mkdir()
        copied = folder / "main.go"
        shutil.copyfile(source, copied)
        identity["cases"][filename] = dict(goVersion=manifest["tag"], sha256=digest,
                                          repetitions=repetitions, timeoutSeconds=90)
        (OUTPUT / "identity.json").write_text(json.dumps(identity, indent=2) + "\n")
        (folder / "go.mod").write_text("module example.com/gcstress\n\ngo 1.27.0\n")
        binary = folder / "probe.exe"
        run(source.stem + "-build", [LLGO, "build", "-C", str(folder), "-o", str(binary), "."], timeout=300)
        for repetition in range(repetitions):
            run(f"{source.stem}-{repetition + 1:03d}", [str(binary)])
    run("gc-regressions", [LLGO, "test", "-count=20", "-timeout=2m", "-v",
                          "-run=^(TestRuntimeConcurrentGCRequests|TestRuntimeBlockedFinalizerDoesNotBlockGC|TestRuntimeConcurrentGCFinalizers)$",
                          "./test/go"], timeout=300)
    run("c-finalizer-reentry", [LLGO, "test", "-count=20", "-timeout=2m", "-v",
                               "-run=^TestRuntimeGCNativeFinalizerReentry$", "./test/llgoext"], timeout=300)


if __name__ == "__main__":
    main()
