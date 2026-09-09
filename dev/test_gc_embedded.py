#!/usr/bin/env python3
"""Run a single-mutator GC firmware until its checked completion marker.

Firmware need not implement process exit. A timeout, early exit, or mere emulator
startup is never a pass. Only the fixture's exact completion line is accepted.
"""

import argparse
import json
import os
from pathlib import Path
import selectors
import signal
import subprocess
import time


def signal_owned_process(process, signum):
    # The emulator may finish between poll() and killpg(). Never signal a
    # different process or turn that normal exit race into a test failure.
    try:
        os.killpg(process.pid, signum)
    except ProcessLookupError:
        pass


def run_until_marker(command, timeout_seconds=60):
    started = time.monotonic()
    process = subprocess.Popen(command, stdout=subprocess.PIPE,
                               stderr=subprocess.STDOUT, start_new_session=True)
    output = bytearray()
    result = "fail"
    reason = "process-exit"
    marker = b"gc standalone ok"
    with selectors.DefaultSelector() as events:
        events.register(process.stdout, selectors.EVENT_READ)
        try:
            while True:
                remaining = timeout_seconds - (time.monotonic() - started)
                if remaining <= 0:
                    result, reason = "timeout", "deadline"
                    break
                for key, _ in events.select(min(0.1, remaining)):
                    chunk = os.read(key.fileobj.fileno(), 65536)
                    if chunk:
                        output.extend(chunk)
                # Only complete lines count, never a substring in diagnostics.
                lines = bytes(output).split(b"\n")[:-1]
                if marker in [line.rstrip(b"\r") for line in lines]:
                    result, reason = "pass", "completion-marker"
                    if process.poll() not in (None, 0):
                        result, reason = "fail", "process-error"
                    break
                if process.poll() is not None:
                    # Read to EOF before evaluating an exited process.
                    output.extend(process.stdout.read())
                    lines = bytes(output).split(b"\n")[:-1]
                    if process.returncode == 0 and marker in [line.rstrip(b"\r") for line in lines]:
                        result, reason = "pass", "completion-marker"
                    break
        finally:
            if process.poll() is None:
                signal_owned_process(process, signal.SIGTERM)
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    signal_owned_process(process, signal.SIGKILL)
                    process.wait()
            else:
                process.wait()
            process.stdout.close()
    return {"result": result, "reason": reason, "exit_code": process.returncode,
            "seconds": time.monotonic() - started}, output.decode(errors="replace")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--llgo", required=True)
    parser.add_argument("--target", choices=["esp32", "esp32c3-basic", "cortex-m-qemu", "riscv-qemu"], required=True)
    parser.add_argument("--evidence", type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    evidence = args.evidence.resolve()
    evidence.mkdir(parents=True, exist_ok=True)
    executable = evidence / "gc.elf"
    command = [args.llgo, "build", "-target", args.target, "-o", str(executable)]
    esp = args.target in ("esp32", "esp32c3-basic")
    if esp:
        # Reuse the supported embedded ports' existing GC regression suite,
        # including register roots, defer liveness, graph release and pressure.
        command += ["-oimg", "./testdata/esp32-serial/gc-runtime"]
        build_dir = root / "_demo/embed"
    else:
        # A file-list executable is not a runtime package. Otherwise link
        # planning may defer the fixture's own archive as runtime-only code.
        command += ["./internal/test/gc-standalone/main.go"]
        build_dir = root / "runtime"
    report = {"target": args.target, "mutators": 1, "result": "incomplete",
              "concurrent_mutators": "unsupported", "interrupt_allocation": "unsupported"}
    try:
        with (evidence / "build.log").open("w") as log:
            subprocess.run(command, cwd=build_dir, stdout=log,
                           stderr=subprocess.STDOUT, timeout=300, check=True)
        if esp:
            executable_name = "qemu-system-xtensa" if args.target == "esp32" else "qemu-system-riscv32"
            machine = "esp32" if args.target == "esp32" else "esp32c3"
            emulator = [executable_name, "-semihosting", "-machine", machine, "-nographic",
                        "-drive", "file=" + str(evidence / "gc.img") + ",if=mtd,format=raw"]
            if machine == "esp32c3":
                emulator += ["-serial", "mon:stdio"]
        elif args.target == "cortex-m-qemu":
            emulator = ["qemu-system-arm", "-machine", "lm3s6965evb", "-semihosting",
                        "-nographic", "-kernel", str(executable)]
        else:
            # The target JSON's inherited cores setting is NOT a supported
            # concurrent-GC contract. Exercise this fixture with one hart.
            emulator = ["qemu-system-riscv32", "-machine", "virt,aclint=on", "-smp", "1",
                        "-nographic", "-bios", "none", "-kernel", str(executable)]
        verdict, output = run_until_marker(emulator)
        (evidence / "execution.log").write_text(output)
        report.update(verdict)
        print(output, end="")
    except (OSError, subprocess.SubprocessError) as error:
        report.update(result="fail", reason=str(error))
        build_log = evidence / "build.log"
        if build_log.exists():
            print(build_log.read_text(errors="replace"))
    (evidence / "result.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report))
    return 0 if report["result"] == "pass" else 1


if __name__ == "__main__":
    raise SystemExit(main())
