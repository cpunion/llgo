#!/usr/bin/env python3
"""Build current J32/J64 artifacts and exercise the actual Chrome debugger.

Requires LLGO, LLGO_BROWSER_CHROME (Chrome for Testing or Chromium), LLVM,
Emscripten and the pinned LLGo Binaryen. Tests set a source breakpoint through
the installed extension, read a real paused C++ local, and observe the Go completion output.
"""

import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def run(command, env):
    print("+", " ".join(map(str, command)), flush=True)
    subprocess.run(command, cwd=ROOT, env=env, check=True, timeout=300)


def main():
    env = os.environ.copy()
    env["LLGO_ROOT"] = str(ROOT)
    if not env.get("LLGO_BROWSER_CHROME"):
        raise SystemExit("LLGO_BROWSER_CHROME is required; no skipped browser acceptance")
    llgo = env.get("LLGO", "llgo")
    with tempfile.TemporaryDirectory(prefix="llgo-browser-debug-") as directory:
        for profile, target in (("j32", "wasm"), ("j64", "emscripten-memory64")):
            for mode in ("embedded", "external"):
                stem = Path(directory) / f"{profile}-{mode}"
                run([llgo, "build", "-target", target, "-O0",
                     f"-debug-artifact={mode}", "-o", str(stem.with_suffix(".mjs")),
                     "./internal/build/testdata/wasm-debug"], env)
                env["LLGO_BROWSER_DEBUG_ARTIFACT"] = str(stem.with_suffix(".wasm"))
                run(["go", "test", "-count=1", "-timeout=3m", "-v",
                     "./internal/browserdebug", "./cmd/internal/browser"], env)


if __name__ == "__main__":
    main()
