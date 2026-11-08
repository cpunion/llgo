import json, os, pathlib, subprocess, time
root = pathlib.Path.cwd()
out = root / ".compat111"
out.mkdir(exist_ok=True)
goroot = pathlib.Path(subprocess.check_output(["go", "env", "GOROOT"], text=True).strip())
llgo = os.environ["LLGO_TEST_LLGO"]
results = []
def run(name, args, timeout=600):
    print("BEGIN", name, flush=True)
    start = time.monotonic()
    try:
        result = subprocess.run([str(x) for x in args], capture_output=True, text=True, timeout=timeout)
        elapsed = time.monotonic() - start
        (out / (name + ".stdout")).write_text(result.stdout)
        (out / (name + ".stderr")).write_text(result.stderr)
        row = dict(name=name, seconds=elapsed, code=result.returncode)
    except subprocess.TimeoutExpired as exc:
        row = dict(name=name, seconds=time.monotonic()-start, timeout=True)
        (out / (name + ".stdout")).write_bytes(exc.stdout or b"")
        (out / (name + ".stderr")).write_bytes(exc.stderr or b"")
    results.append(row)
    (out / "results.json").write_text(json.dumps(results, indent=2))
    print("END", json.dumps(row), flush=True)
    return row
# The generator must use the target's int width.
generator = subprocess.run(["go", "run", str(goroot / "test/convinline.go")], capture_output=True, text=True, check=True)
generated = out / "convinline.go"
generated.write_text(generator.stdout)
for name, src in [("convinline", generated), ("issue30476", goroot/"test/fixedbugs/issue30476.go"), ("issue39541", goroot/"test/fixedbugs/issue39541.go")]:
    binary = out / (name + ".exe")
    build = run(name+"-build", [llgo, "build", "-o", binary, src])
    if build.get("code") == 0:
        run(name+"-run", [binary])
# The instrumented copy reports progress after each 10,000-call worker.
source = (goroot / "test/fixedbugs/issue39541.go").read_text()
source = source.replace("\t\t<-c\n", "\t\t<-c; println(\"completed\", i+1)\n")
progress = out / "issue39541-progress.go"
progress.write_text(source)
binary = out / "issue39541-progress.exe"
if run("progress-build", [llgo, "build", "-o", binary, progress]).get("code") == 0:
    run("progress-run", [binary])
print(json.dumps(results, indent=2), flush=True)
if any(r.get("code", 0) != 0 or r.get("timeout") for r in results):
    raise SystemExit(1)
