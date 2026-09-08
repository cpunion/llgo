#!/usr/bin/env python3
"""Fork-only replay of a preserved input, with separate pass/RSS accounting."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time


INPUT_SHA256 = "eff14a9d7ae3ee2e95743bb693896e13c088c1aacaf03355294caf7ee1b7d987"


def group_rss(pgid):
    rows = subprocess.check_output(["ps", "-eo", "pgid=,rss="], text=True).splitlines()
    return sum(int(rss) for group, rss in (row.split() for row in rows) if int(group) == pgid)


def bounded_run(name, command, output, seconds=180, max_rss_mib=4096):
    directory = output / name
    directory.mkdir()
    record = {"argv": command, "timeout_seconds": seconds, "max_rss_mib": max_rss_mib}
    (directory / "command.json").write_text(json.dumps(record, indent=2) + "\n")
    started = time.monotonic()
    peak = 0
    reason = None
    with (directory / "output.log").open("w") as log:
        process = subprocess.Popen(["/usr/bin/time", "-v", "-o", str(directory / "time.txt"),
                                    *command], stdout=log, stderr=subprocess.STDOUT,
                                   start_new_session=True)
        try:
            while process.poll() is None:
                peak = max(peak, group_rss(process.pid))
                elapsed = time.monotonic() - started
                if peak > max_rss_mib * 1024:
                    reason = "RSS limit"
                elif elapsed > seconds:
                    reason = "timeout"
                if reason is not None:
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    break
                time.sleep(0.1)
        finally:
            if process.poll() is None and reason is None:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            status = process.wait()
    record.update(returncode=status, seconds=time.monotonic()-started,
                  peak_group_rss_kib=peak, limit=reason)
    (directory / "result.json").write_text(json.dumps(record, indent=2) + "\n")
    print(json.dumps({"stage": name, **record}), flush=True)
    return status == 0 and reason is None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    opts = parser.parse_args()
    source = opts.input.resolve()
    if hashlib.sha256(source.read_bytes()).hexdigest() != INPUT_SHA256:
        parser.error("input does not match the preserved issue5162 module")
    wasm_opt, wasmtime = shutil.which("wasm-opt"), shutil.which("wasmtime")
    if wasm_opt is None or wasmtime is None:
        parser.error("wasm-opt and wasmtime are required")
    output = opts.output.resolve()
    output.mkdir()
    combined, asyncified, split = [output / f"{name}.wasm" for name in ("combined", "asyncified", "split")]
    combined_ok = bounded_run("combined", [wasm_opt, "--asyncify", "--translate-to-exnref", "-Os",
                                           str(source), "-o", str(combined)], output)
    asyncify_ok = bounded_run("asyncify", [wasm_opt, "--asyncify", "--translate-to-exnref",
                                          str(source), "-o", str(asyncified)], output)
    split_ok = asyncify_ok and bounded_run("post-opt", [wasm_opt, "-Os", str(asyncified), "-o", str(split)], output)
    executions = []
    for name, module, built in (("combined", combined, combined_ok), ("split", split, split_ok)):
        if built:
            executions.append(bounded_run("run-" + name, [wasmtime, "run", "-W", "exceptions=y", str(module)],
                                          output, seconds=60))
    # A failed diagnostic comparison remains visible; never report a single
    # successful variant as the unchanged acceptance command having passed.
    return 0 if combined_ok and split_ok and all(executions) else 1


if __name__ == "__main__":
    raise SystemExit(main())
