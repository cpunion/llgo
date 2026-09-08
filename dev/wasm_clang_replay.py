#!/usr/bin/env python3
"""Replay preserved LLVM IR with backend pass tracing, without rebuilding Go."""

import argparse
import hashlib
from pathlib import Path
import shutil
import subprocess

from wasm_optimizer_replay import bounded_run


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    opts = parser.parse_args()
    source = opts.input.resolve()
    if hashlib.sha256(source.read_bytes()).hexdigest() != "988c4d49c460633fd1680a5a222dfd369f7abdb3c9b13ea391e707311e9ab719":
        parser.error("input does not match the preserved cmplxdivide IR")
    clang = shutil.which("clang++")
    if clang is None or "clang version 22." not in subprocess.check_output([clang, "--version"], text=True):
        parser.error("LLVM 22 clang++ is required")
    output = opts.output.resolve()
    output.mkdir()
    # The input is already typed LLVM IR, so C/C++ sysroot/header paths from
    # the original runner are not needed. Preserve its backend target flags.
    command = [clang, "-Os", "-target", "wasm32-unknown-wasip1", "-matomics", "-mbulk-memory",
               "-fwasm-exceptions", "-mllvm", "-wasm-enable-sjlj", "-c", str(source),
               "-o", str(output / "main.o"), "-Wno-override-module",
               "-Xclang", "-fdebug-pass-manager", "-mllvm", "-debug-pass=Executions"]
    return 0 if bounded_run("backend", command, output) else 1


if __name__ == "__main__":
    raise SystemExit(main())
