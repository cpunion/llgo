"""Fork-only reproduction with an external deadline and native stack capture."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time

root = Path(os.environ["RUNNER_TEMP"]) / "goroot-diagnostics"
root.mkdir(exist_ok=True)
os.environ["LLGO_DIAG_GOROOT"] = str(root)
os.environ["LLGO_STDIO_NOBUF"] = "1"
results = []


def capture(label, pid):
    snapshot = subprocess.run(
        ["powershell.exe", "-NoProfile", "-Command",
         "Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,"
         "Name,CommandLine,KernelModeTime,UserModeTime,WorkingSetSize | ConvertTo-Json -Depth 3"],
        capture_output=True, timeout=20,
    )
    (root / (label + "-processes.json")).write_bytes(snapshot.stdout)
    ids = [pid]
    marker = root / "pid.txt"
    if marker.exists():
        target_pid = int(marker.read_text())
        if target_pid not in ids:
            ids.append(target_pid)
    env = os.environ.copy()
    env["PYTHONHOME"] = env["LLGO_WINDOWS_LLDB_PYTHON_HOME"]
    env["PYTHONPATH"] = env["LLGO_WINDOWS_LLDB_PYTHONPATH"]
    env["PATH"] = env["PYTHONHOME"] + os.pathsep + env["PATH"]
    for target_pid in ids:
        with (root / f"{label}-stack-{target_pid}.txt").open("wb") as output:
            try:
                subprocess.run(
                    [env["LLGO_LLDB"], "--batch", "-p", str(target_pid),
                     "-o", "thread backtrace all", "-o", "process detach"],
                    stdout=output, stderr=subprocess.STDOUT, env=env, timeout=25,
                )
            except subprocess.TimeoutExpired:
                output.write(b"\nDebugger exceeded 25s\n")


def bounded(label, args, limit, cwd=None):
    print(f"START {label}: limit={limit}s {args}", flush=True)
    logfile = root / (label + ".log")
    start = time.monotonic()
    with logfile.open("wb") as output:
        process = subprocess.Popen(args, cwd=cwd, stdout=output, stderr=subprocess.STDOUT)
        offset = 0
        try:
            while True:
                with logfile.open("rb") as stream:
                    stream.seek(offset)
                    chunk = stream.read()
                    offset = stream.tell()
                if chunk:
                    print(chunk.decode("utf-8", errors="replace"), end="", flush=True)
                code = process.poll()
                if code is not None:
                    # Drain output written between the read above and process exit.
                    with logfile.open("rb") as stream:
                        stream.seek(offset)
                        print(stream.read().decode("utf-8", errors="replace"), end="", flush=True)
                    break
                if time.monotonic() - start > limit:
                    print(f"HANG {label}: pid={process.pid}; capturing before termination", flush=True)
                    try:
                        capture(label, process.pid)
                    finally:
                        subprocess.run(["taskkill.exe", "/PID", str(process.pid), "/T", "/F"], timeout=15)
                    code = 124
                    break
                time.sleep(1)
        finally:
            if process.poll() is None:
                process.kill()
    elapsed = round(time.monotonic() - start, 3)
    results.append(dict(label=label, seconds=elapsed, exit_code=code))
    (root / "results.json").write_text(json.dumps(results, indent=2))
    print(f"END {label}: exit={code} elapsed={elapsed}s", flush=True)
    return code


failed = bounded("full-shard", [shutil.which("bash"), "dev/test_go_version.sh", "1.27"], 420)
binary_marker = root / "binary.txt"
if binary_marker.exists():
    binary = binary_marker.read_text()
    directory = (root / "directory.txt").read_text()
    # This is the same executable linked in the shared package DAG, not rebuilt.
    shutil.copy2(binary, root / "goroot.test.exe")
    for index in range(1, 9):
        code = bounded(f"fresh-process-{index}",
                       [binary, "-test.v", "-test.count=1", "-test.timeout=30s"], 45, directory)
        if code:
            failed = code
            break
    if not failed:
        failed = bounded("repeat-process-tests", [binary, "-test.v", "-test.count=20",
                          "-test.run=^TestRun(Program|GeneratedProgram)", "-test.timeout=90s"], 110, directory)

# The target remains arm64, using official Go to compile the same package.
control = root / "goroot-official.test.exe"
code = bounded("official-go-build", ["go", "test", "-c", "-o", str(control), "./test/goroot"], 120)
if code == 0:
    code = bounded("official-go-tests", [str(control), "-test.v", "-test.count=10", "-test.timeout=60s"],
                   75, str(Path.cwd() / "test/goroot"))
failed = failed or code
sys.exit(1 if failed else 0)
