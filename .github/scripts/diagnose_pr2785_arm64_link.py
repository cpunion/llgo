import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess


root = Path.cwd()
probes = root / "probes"
probes.mkdir(exist_ok=True)
cache = Path(os.environ["LOCALAPPDATA"]) / "llgo" / "build"
results = []
demos = ["export", "reflect", "ifaceconv", "abimethod"]


def build_demo(round_name, demo):
    output = probes / f"{round_name}-{demo}.exe"
    log = probes / f"{round_name}-{demo}.log"
    with log.open("w", encoding="utf-8") as stream:
        command = ["llgo", "build", "-x", "-v", "-p=1", "-o", str(output), "."]
        result = subprocess.run(command, cwd=root / "_demo" / "go" / demo,
                                stdout=stream, stderr=subprocess.STDOUT, timeout=180)
        code = result.returncode
        if code == 0:
            code = subprocess.run([str(output)], stdout=stream,
                                  stderr=subprocess.STDOUT, timeout=90).returncode
    return {"round": round_name, "demo": demo, "code": code}


def snapshot_bisect(round_name):
    destination = probes / round_name / "archives"
    destination.mkdir(parents=True, exist_ok=True)
    for package in ("bisect", "godebug"):
        for archive in cache.glob(f"*/internal/{package}/*.a"):
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            name = destination / f"{package}-{archive.name}"
            shutil.copy2(archive, name)
            for suffix in (".manifest", ".meta"):
                source = archive.with_suffix(suffix)
                if source.exists():
                    shutil.copy2(source, name.with_suffix(suffix))
            with name.with_suffix(".nm").open("w", encoding="utf-8") as stream:
                stream.write(f"source={archive}\nsha256={digest}\n")
                subprocess.run(["llvm-nm", str(archive)], stdout=stream,
                               stderr=subprocess.STDOUT, timeout=30)


for index in range(9):
    name = f"round-{index:02d}"
    if index:
        shutil.rmtree(cache, ignore_errors=True)
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
        batch = list(executor.map(lambda demo: build_demo(name, demo), demos))
    batch.append(build_demo(name + "-warm", "export"))
    results.extend(batch)
    snapshot_bisect(name)
    (probes / "results.json").write_text(json.dumps(results, indent=2), encoding="utf-8")
    print(json.dumps(batch), flush=True)
    if any(result["code"] for result in batch):
        raise SystemExit(1)
