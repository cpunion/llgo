import os
from pathlib import Path
import subprocess
import time

root = Path("protocol-diagnostics")
root.mkdir(exist_ok=True)
for mode in ("normal", "single-cpu"):
    prefix = [] if mode == "normal" else ["taskset", "-c", str(min(os.sched_getaffinity(0)))]
    for i in range(50):
        started = time.monotonic()
        process = subprocess.Popen(prefix + ["/tmp/protocol-ci"], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        try:
            output, _ = process.communicate(timeout=20)
        except subprocess.TimeoutExpired:
            dump = subprocess.run(["sudo", "gdb", "-q", "-batch", "-ex", "set pagination off", "-ex", "p allcall_phase", "-ex", "thread apply all bt full", "-ex", "detach", "-p", str(process.pid)], capture_output=True, timeout=30)
            (root / f"{mode}-{i}.gdb").write_bytes(dump.stdout + dump.stderr)
            process.kill()
            output, _ = process.communicate()
            (root / f"{mode}-{i}.log").write_bytes(output)
            print(output.decode(errors="replace"), flush=True)
            raise RuntimeError(f"{mode} round {i} exceeded 20 seconds; stack dump collected")
        (root / f"{mode}-{i}.log").write_bytes(output)
        print(f"{mode} round {i}: exit={process.returncode} {time.monotonic()-started:.3f}s", flush=True)
        if process.returncode or b"allthreads-ok" not in output:
            print(output.decode(errors="replace"), flush=True)
            raise RuntimeError("protocol failed")
