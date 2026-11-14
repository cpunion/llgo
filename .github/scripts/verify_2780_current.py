"""Strict current-runtime replay; no retries or expected-failure exemptions."""

import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time


ROOT = Path.cwd()
OUTPUT = ROOT / "native-followups-results"
OUTPUT.mkdir(exist_ok=True)
RESULTS = []


def save():
    (OUTPUT / "results.json").write_text(json.dumps(RESULTS, indent=2) + "\n")


def run(name, args, cwd=ROOT, timeout=90, env=None):
    started = time.monotonic()
    with (OUTPUT / (name + ".log")).open("wb") as log:
        process = subprocess.Popen(args, cwd=cwd, env=env, stdout=log, stderr=log)
        try:
            status = process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            with (OUTPUT / (name + "-processes.json")).open("wb") as snapshot:
                subprocess.run(
                    ["powershell", "-NoProfile", "-Command",
                     "Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,Name,CommandLine | ConvertTo-Json"],
                    stdout=snapshot, stderr=snapshot, timeout=20,
                )
            subprocess.run(["taskkill", "/F", "/T", "/PID", str(process.pid)],
                           stdout=log, stderr=log, timeout=15, check=False)
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
    # The timeout patch and this fork-only harness do not change runtime code.
    subprocess.run(["git", "diff", "--exit-code", identity["runtimeBase"], "HEAD", "--", "runtime"], check=True)
    (OUTPUT / "identity.json").write_text(json.dumps(identity, indent=2) + "\n")
    run("compiler-version", ["llgo", "version"])
    run("go-version", ["go", "version"])
    older_env = dict(os.environ, GOTOOLCHAIN="go1.26.7", GOOS="windows", GOARCH="amd64", GOWORK="off")
    with tempfile.TemporaryDirectory() as directory:
        goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], cwd=directory,
                                             env=older_env, text=True, timeout=180).strip())
    for filename, repetitions in [("issue25897a.go", 100), ("issue79186.go", 15), ("issue30041.go", 10)]:
        source = goroot / "test" / "fixedbugs" / filename
        folder = OUTPUT / source.stem
        folder.mkdir()
        copied = folder / "main.go"
        shutil.copyfile(source, copied)
        identity["cases"][filename] = dict(goVersion="go1.26.7", sha256=hashlib.sha256(copied.read_bytes()).hexdigest(),
                                          repetitions=repetitions, timeoutSeconds=90)
        (OUTPUT / "identity.json").write_text(json.dumps(identity, indent=2) + "\n")
        (folder / "go.mod").write_text("module example.com/gcstress\n\ngo 1.27.0\n")
        binary = folder / "probe.exe"
        run(source.stem + "-build", ["llgo", "build", "-C", str(folder), "-o", str(binary), "."], timeout=300)
        for repetition in range(repetitions):
            run(f"{source.stem}-{repetition + 1:03d}", [str(binary)])
    run("gc-regressions", ["llgo", "test", "-count=20", "-timeout=2m", "-v",
                          "-run=^(TestRuntimeConcurrentGCRequests|TestRuntimeBlockedFinalizerDoesNotBlockGC|TestRuntimeConcurrentGCFinalizers)$",
                          "./test/go"], timeout=300)
    run("c-finalizer-reentry", ["llgo", "test", "-count=20", "-timeout=2m", "-v",
                               "-run=^TestRuntimeGCNativeFinalizerReentry$", "./test/llgoext"], timeout=300)


if __name__ == "__main__":
    main()
