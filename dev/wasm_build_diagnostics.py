#!/usr/bin/env python3
"""Bounded, opt-in diagnostics; never change the compiler's optimization flags."""

import argparse
import datetime
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import threading


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def compiler_args(args):
    # The GOROOT runner may also query a tool; only builds accept -x.
    if args and args[0] == "build" and "-x" not in args:
        return [args[0], "-x", *args[1:]]
    return args


def output_path(args):
    for index, arg in enumerate(args[:-1]):
        if arg == "-o":
            return Path(args[index + 1])
    return None


def wasm_inputs(args):
    # Ignore output operands, including an already-created empty output file.
    seen = set()
    for index, arg in enumerate(args):
        if arg.startswith("-") or (index and args[index - 1] == "-o"):
            continue
        path = Path(arg)
        if path.is_file() and path.resolve() not in seen:
            seen.add(path.resolve())
            yield path


def save_file(source, destination):
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
    return {"path": str(source.resolve()), "bytes": source.stat().st_size,
            "saved_as": str(destination)}


def run_tool(kind, args):
    evidence = Path(os.environ["LLGO_DIAG_EVIDENCE"])
    command_dir = evidence / "commands" / f"{kind}-{os.getpid()}"
    command_dir.mkdir(parents=True)
    real = os.environ["LLGO_DIAG_REAL_LLGO" if kind == "llgo" else "LLGO_DIAG_REAL_WASMOPT"]
    actual = compiler_args(args) if kind == "llgo" else args
    record = {"tool": kind, "pid": os.getpid(), "ppid": os.getppid(),
              "started": now(), "cwd": str(Path.cwd()), "argv": [real, *actual],
              "inputs": []}
    if kind == "wasm-opt":
        for index, path in enumerate(wasm_inputs(actual)):
            record["inputs"].append(save_file(path, command_dir / f"input-{index}.wasm"))
    elif args and args[0] == "build":
        # These two cases are single-directory Go programs. Retain their exact
        # staged sources, not the workspace's GOPATH symlink or compiler cache.
        for path in sorted(Path.cwd().iterdir()):
            if path.is_file() and (path.suffix == ".go" or path.name in ("go.mod", "go.sum")):
                record["inputs"].append(save_file(path, command_dir / "source" / path.name))
    record_path = command_dir / "command.json"
    record_path.write_text(json.dumps(record, indent=2) + "\n")
    # This start record survives the runner's process-group SIGKILL. An absent
    # finished timestamp/time report is itself useful phase evidence.
    print(f"[wasm-build-diag] {record['started']} {kind} pid={os.getpid()} {record_path}",
          file=sys.stderr, flush=True)
    result = subprocess.run(["/usr/bin/time", "-v", "-o", str(command_dir / "time.txt"),
                             real, *actual], check=False)
    record.update(finished=now(), returncode=result.returncode)
    output = output_path(actual)
    if kind == "llgo" and output is not None and output.is_file():
        record["output"] = save_file(output, command_dir / "output.wasm")
    record_path.write_text(json.dumps(record, indent=2) + "\n")
    return result.returncode if result.returncode >= 0 else 128 - result.returncode


def sample_processes(stop, destination):
    with destination.open("w", buffering=1) as stream:
        while not stop.is_set():
            stream.write(f"\n{now()}\n")
            # comm is the executable name, not its argv or environment. PGID
            # lets the report reconstruct the exact runner-guard RSS sum.
            subprocess.run(["ps", "-eo", "pid,ppid,pgid,rss,pcpu,comm"],
                           stdout=stream, stderr=stream, check=False)
            stream.flush()
            stop.wait(5)


def runner_args(runner, goroot, wrapper, report):
    return [str(runner), "-test.v", "-test.run=^TestGoRootRunCases$", "-test.count=1",
            "-test.timeout=15m", "-goroot", str(goroot), "-llgo", str(wrapper),
            "-wasm-profile=GWASI", "-directive-mode=coverage", "-directives=run,runoutput",
            "-case=^(cmplxdivide\\.go|fixedbugs/issue5162\\.go)$", "-shard-index=0",
            "-shard-total=1", "-keepwork", "-build-timeout=180s", "-run-timeout=60s",
            "-max-rss-mib=4096", "-rss-warn-mib=1024", "-min-memory-free-percent=15",
            "-min-swap-free-mib=512", "-progress=60s", "-report", str(report)]


def run_diagnostics(args):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--llgo", type=Path, required=True)
    parser.add_argument("--runner", type=Path, required=True)
    parser.add_argument("--goroot", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    opts = parser.parse_args(args)
    if sys.platform != "linux":
        parser.error("diagnostic execution requires Linux ps and GNU time")
    output = opts.output.resolve()
    output.mkdir()  # Refuse to overwrite any earlier diagnostic evidence.
    evidence = output / "evidence"
    evidence.mkdir()
    work = output / "work"
    work.mkdir()
    wrappers = output / "bin"
    wrappers.mkdir()
    script = Path(__file__).resolve()
    for name in ("llgo", "wasm-opt"):
        (wrappers / name).symlink_to(script)
    real_wasm_opt = shutil.which("wasm-opt")
    if real_wasm_opt is None:
        parser.error("wasm-opt must be installed before running diagnostics")
    env = dict(os.environ, LLGO_DIAG_REAL_LLGO=str(opts.llgo.resolve()),
               LLGO_DIAG_REAL_WASMOPT=real_wasm_opt, LLGO_DIAG_EVIDENCE=str(evidence),
               WASMOPT=str(wrappers / "wasm-opt"), TMPDIR=str(work))
    command = runner_args(opts.runner.resolve(), opts.goroot.resolve(), wrappers / "llgo",
                          evidence / "goroot-GWASI.json")
    # Do not serialize env: runner tokens and other unrelated secrets must not
    # enter diagnostics. Leave GOMAXPROCS/BINARYEN_CORES/optimization unchanged.
    (evidence / "runner-command.json").write_text(json.dumps(command, indent=2) + "\n")
    stop = threading.Event()
    monitor = threading.Thread(target=sample_processes, args=(stop, evidence / "processes.log"))
    monitor.start()
    try:
        with (evidence / "goroot.log").open("w", buffering=1) as log:
            with subprocess.Popen(command, cwd=script.parent.parent / "test" / "goroot",
                                  env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                  text=True) as process:
                for line in process.stdout:
                    log.write(line)
                    print(line, end="", flush=True)
                status = process.wait()
    finally:
        stop.set()
        monitor.join()
    # Work remains on this disposable runner (-keepwork). Artifact upload is
    # restricted to evidence: exact staged sources, Binaryen inputs, outputs,
    # and logs. Never recursively archive caches or symlinked repositories.
    (evidence / "result.json").write_text(json.dumps({"finished": now(), "returncode": status,
                                                     "kept_work": str(work)}, indent=2) + "\n")
    return status


if __name__ == "__main__":
    tool = Path(sys.argv[0]).name
    if tool in ("llgo", "wasm-opt"):
        sys.exit(run_tool(tool, sys.argv[1:]))
    sys.exit(run_diagnostics(sys.argv[1:]))
