#!/usr/bin/env python3

"""Exercise the experimental WASI pthread backend under WAMR."""

import os
import pathlib
import shutil
import subprocess
import tempfile


ROOT = pathlib.Path(__file__).resolve().parent.parent
LLGO = os.environ.get("LLGO", "llgo")
IWASM = os.environ.get("IWASM", "iwasm")


def main():
    if shutil.which(IWASM) is None:
        raise SystemExit(f"WAMR runner not found: {IWASM}")

    env = os.environ.copy()
    env["LLGO_ROOT"] = str(ROOT)
    env["LLGO_WASI_THREADS"] = "1"
    with tempfile.TemporaryDirectory(prefix="llgo-wasi-threads-") as directory:
        module = pathlib.Path(directory) / "threads.wasm"
        subprocess.run(
            [LLGO, "build", "-target", "wasi", "-tags", "nogc", "-o", str(module),
             str(ROOT / "internal/build/testdata/wasm-wasi-threads")],
            check=True,
            env=env,
            timeout=180,
        )
        result = subprocess.run(
            [IWASM, "--max-threads=8", "--stack-size=1048576",
             "--heap-size=67108864", str(module)],
            capture_output=True,
            text=True,
            timeout=30,
        )
        print(result.stdout, end="")
        print(result.stderr, end="")
        if result.returncode != 0 or "wasi threads ok" not in result.stdout + result.stderr:
            raise SystemExit(f"WAMR WASI pthread probe failed with exit code {result.returncode}")


if __name__ == "__main__":
    main()
