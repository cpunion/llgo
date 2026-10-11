"""Run the existing Windows helper and capture stacks before killing a hang."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys


def main():
    binary = Path(sys.argv[1]).resolve()
    label = sys.argv[2]
    root = Path("probes") / label
    root.mkdir(parents=True, exist_ok=True)
    results = []
    failures = 0
    debugger = os.environ.get("LLGO_LLDB") or shutil.which("lldb.exe") or shutil.which("lldb")
    with (root / "parent.log").open("wb") as stream:
        parent = subprocess.Popen(
            [str(binary), "-test.run=^TestExitDuringThreadCreation$", "-test.count=3", "-test.timeout=3m"],
            stdout=stream,
            stderr=subprocess.STDOUT,
        )
        try:
            parent_code = parent.wait(timeout=200)
        except subprocess.TimeoutExpired:
            parent.kill()
            parent_code = parent.wait(timeout=10)
        print(json.dumps(dict(label=label, parent_code=parent_code)), flush=True)
    for mode in ["return", "os-exit"]:
        for iteration in range(512):
            output = root / f"{mode}-{iteration}.log"
            env = dict(os.environ, LLGO_WINDOWS_EXIT_THREAD_CREATE_HELPER=mode)
            with output.open("wb") as stream:
                process = subprocess.Popen(
                    [str(binary), "-test.run=^TestExitDuringThreadCreation$"],
                    env=env,
                    stdout=stream,
                    stderr=subprocess.STDOUT,
                )
                hung = False
                try:
                    code = process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    hung = True
                    stack = root / f"{mode}-{iteration}-stack.log"
                    if debugger:
                        with stack.open("wb") as trace:
                            try:
                                subprocess.run(
                                    [debugger, "--batch", "-o", f"process attach -p {process.pid}",
                                     "-o", "thread backtrace all", "-o", "process detach"],
                                    stdout=trace,
                                    stderr=subprocess.STDOUT,
                                    timeout=30,
                                )
                            except subprocess.TimeoutExpired:
                                trace.write(b"\nDebugger attach timed out.\n")
                    process.kill()
                    code = process.wait(timeout=10)
                result = dict(mode=mode, iteration=iteration, code=code, hung=hung)
                results.append(result)
                if hung or code != 0:
                    failures += 1
                    print(label, json.dumps(result), flush=True)
            if failures >= 3:
                break
        if failures >= 3:
            break
    summary = dict(label=label, parent_code=parent_code, total=len(results), failures=failures, results=results)
    (root / "results.json").write_text(json.dumps(summary, indent=2))
    print(json.dumps({key: summary[key] for key in ["label", "total", "failures"]}), flush=True)


if __name__ == "__main__":
    main()
