"""Reproduce the unmodified PR 2549 host test package on Windows runners."""

import json
import subprocess
import time
from pathlib import Path


def main():
    output_dir = Path("diag-results").resolve()
    executable = output_dir / "go.test.exe"
    results = []
    for iteration in range(1, 31):
        log = output_dir / f"run-{iteration:02d}.log"
        command = [
            str(executable),
            "-test.v",
            "-test.count=1",
            "-test.timeout=90s",
            "-test.coverprofile=" + str(output_dir / f"coverage-{iteration:02d}.txt"),
        ]
        print(f"START iteration {iteration}", flush=True)
        started = time.monotonic()
        with log.open("wb") as stream:
            try:
                process = subprocess.run(
                    command, stdout=stream, stderr=subprocess.STDOUT, timeout=105
                )
                returncode = process.returncode
            except subprocess.TimeoutExpired:
                returncode = "timeout"
        text = log.read_text(encoding="utf-8", errors="replace")
        result = {
            "iteration": iteration,
            "returncode": returncode,
            "seconds": round(time.monotonic() - started, 3),
            "log": log.name,
            "tests_started": sum(line.startswith("=== RUN") for line in text.splitlines()),
        }
        results.append(result)
        (output_dir / "results.json").write_text(json.dumps(results, indent=2))
        print(json.dumps(result), flush=True)
        if returncode != 0 or "PASS" not in text:
            print("FAILURE LOG TAIL", flush=True)
            print("\n".join(text.splitlines()[-160:]), flush=True)
            raise SystemExit(1)
    print("All 30 fresh-process runs passed.", flush=True)


if __name__ == "__main__":
    main()
