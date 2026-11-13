"""One-off fork audit of issue 367 using the original math demo."""

import json
import os
from pathlib import Path
import platform
import subprocess
import sys


llgo = Path(sys.argv[1]).resolve()
output = Path(sys.argv[2]).resolve()
output.mkdir(parents=True, exist_ok=True)
results = {"platform": platform.platform(), "machine": platform.machine(), "sdks": []}
failed = False

for sdk_arg in sys.argv[3:]:
    sdk = Path(sdk_arg).resolve()
    version = (sdk / "VERSION").read_text().splitlines()[0]
    work = output / version
    work.mkdir(exist_ok=True)
    (work / "go.mod").write_text("module issue367\n\ngo 1.20\n")
    (work / "main.go").write_text('''package main

import "math"

func main() {
    println(math.Sqrt(2))
    println(math.Abs(-1.2))
    println(math.Ldexp(1.2, 3))
}
''')
    env = dict(os.environ, GOROOT=str(sdk), GOTOOLCHAIN="local", GOEXPERIMENT="", GOFLAGS="")
    env["PATH"] = str(sdk / "bin") + os.pathsep + env["PATH"]
    result = {"version": version, "sdk": str(sdk), "passed": False}
    results["sdks"].append(result)

    def run(name, args):
        proc = subprocess.run([str(arg) for arg in args], cwd=work, env=env,
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=300)
        text = proc.stdout.decode(errors="replace")
        (work / (name + ".log")).write_text(text)
        if proc.returncode:
            raise RuntimeError(f"{name}: exit {proc.returncode}\n{text}")
        return text

    try:
        selected = json.loads(run("environment", [llgo, "env", "-json", "GOROOT", "GOVERSION", "GOOS", "GOARCH"]))
        assert selected["GOVERSION"] == version, selected
        assert Path(selected["GOROOT"]).resolve() == sdk, selected
        result["environment"] = selected
        run("go-build", [sdk / "bin/go", "build", "-o", work / "go-main", "."])
        expected = run("go-run", [work / "go-main"])
        for opt in (0, 2):
            exe = work / f"llgo-O{opt}"
            run(f"llgo-O{opt}-build", [llgo, "build", f"-O{opt}", "-o", exe, "."])
            actual = run(f"llgo-O{opt}-run", [exe])
            assert actual == expected, f"O{opt}: expected {expected!r}, got {actual!r}"
        result["passed"] = True
        result["output"] = expected
    except Exception as error:
        result["error"] = str(error)
        failed = True
    (output / "results.json").write_text(json.dumps(results, indent=2) + "\n")
    print(json.dumps(result), flush=True)

sys.exit(1 if failed else 0)
