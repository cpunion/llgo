"""Materialize source-owned Linux coverage commands; never execute a compiler."""

import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

import yaml


SOURCE = "d4b248e8c7790ad7083325ea2b001c16e4a5a8a5"
WORKFLOWS = {
    ".github/workflows/go.yml": "d7b693085a2043d7ccda6033c0b2c630118bb6947307569c95662949b57a256c",
    ".github/workflows/llgo.yml": "e335f5f5c99cf4ecb8c1f9cfcb237cb5adb3ee8f4fd6bdac8f0718b6cf5b9d09",
}
PRODUCERS = {
    "go": ("go.yml", "test", {
        "build": "Build", "coverage": "Test with coverage",
        "aes": "Test LLVM AES on emulated CPU targets",
        "traceback": "Test traceback formatter with coverage",
        "embedded": "Test with embedded emulator env",
        "stdlib": "Check std symbol coverage",
        "filter": "Exclude host Go coverage of test fixtures",
    }),
    "llgo": ("llgo.yml", "llgo", {
        "build": "Build LLGo and test tools", "coverage": "Run Go 1.27 LLGo core tests",
    }),
    "dev": ("go.yml", "dev-lto-globaldce", {
        "build": "Build llgo with dev tag", "plugin": "Build LTO plugin",
        "coverage": "Run dev LTO GlobalDCE tests and demos with coverage",
    }),
    "browser": ("llgo.yml", "wasm-browser-debug", {
        "build": "Install llgo", "coverage": "Test real browser source breakpoints and paused variables",
    }),
}


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def git(root, *args):
    return subprocess.check_output(["git", *args], cwd=root, text=True).strip("\n")


def snapshot(root):
    if git(root, "rev-parse", "HEAD") != SOURCE or git(root, "status", "--porcelain"):
        raise ValueError("baseline must be the clean exact source commit")
    if (root / ".go-version").read_text().strip() != "1.27.0":
        raise ValueError("baseline SDK must retain its original 1.27.0 pin")
    if any(digest(root / name) != expected for name, expected in WORKFLOWS.items()):
        raise ValueError("original coverage workflow changed")
    return {name: digest(root / name) for name in git(root, "ls-files", "-z").split("\0") if name}


def definition(root, producer):
    filename, job, selected = PRODUCERS[producer]
    workflow = ".github/workflows/" + filename
    steps = yaml.load((root / workflow).read_text(), Loader=yaml.BaseLoader)["jobs"][job]["steps"]
    scripts = {}
    for key, name in selected.items():
        matches = [step for step in steps if step.get("name") == name]
        if len(matches) != 1 or "run" not in matches[0]:
            raise ValueError("missing or ambiguous original command: " + name)
        raw = matches[0]["run"]
        # This is the original Linux matrix's empty Windows ABI value.
        script = raw.replace("${{ matrix.windows_abi }}", "")
        if "${{" in script:
            raise ValueError("unresolved workflow expression: " + name)
        scripts[key] = script
    upload = next(step for step in steps if step.get("uses", "").startswith("codecov/"))
    return {"producer": producer, "workflow": workflow, "job": job,
            "flag": upload["with"]["flags"], "files": upload["with"]["files"].split(","),
            "scripts": scripts}


def prepare(root, output, producer):
    source = snapshot(root)
    result = definition(root, producer)
    output.mkdir(parents=True, exist_ok=True)
    for key, script in result["scripts"].items():
        (output / (key + ".sh")).write_text(script)
    result.update(source_commit=SOURCE, source_files=source, original_go_version="1.27.0")
    (output / "provenance.json").write_text(json.dumps(result, indent=2) + "\n")
    return result


def verify(root, output):
    result = json.loads((output / "provenance.json").read_text())
    if result["source_commit"] != SOURCE or result["source_files"] != snapshot(root):
        raise ValueError("baseline source changed during tests")
    expected = definition(root, result["producer"])
    if any(result[key] != value for key, value in expected.items()):
        raise ValueError("coverage producer differs from original workflow")
    reports = {}
    for name in result["files"]:
        path = root / name
        if not path.is_file() or not path.read_text().startswith("mode: ") or len(path.read_text().splitlines()) < 2:
            raise ValueError("missing or empty coverage report: " + name)
        reports[name] = digest(path)
        target = output / "reports" / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(path, target)
    (output / "verified.json").write_text(json.dumps(reports, indent=2) + "\n")
    return reports


def verify_downloads(root, directory):
    source = snapshot(root)
    outputs = {}
    for producer in PRODUCERS:
        output = directory / producer
        result = json.loads((output / "provenance.json").read_text())
        expected = definition(root, producer)
        if (result["source_commit"] != SOURCE or result["source_files"] != source
                or any(result[key] != value for key, value in expected.items())):
            raise ValueError("downloaded baseline producer identity mismatch")
        reports = json.loads((output / "verified.json").read_text())
        if set(reports) != set(expected["files"]):
            raise ValueError("incomplete original report set")
        paths = [output / "reports" / name for name in expected["files"]]
        if any(digest(path) != reports[name] for name, path in zip(expected["files"], paths)):
            raise ValueError("downloaded coverage report changed")
        outputs[producer + "_files"] = ",".join(map(str, paths))
    return outputs


if __name__ == "__main__":
    mode, producer, directory = sys.argv[1:]
    root, output = Path.cwd(), Path(directory)
    if mode == "prepare":
        prepare(root, output, producer)
    elif mode == "verify":
        verify(root, output)
    elif mode == "downloaded":
        with open(os.environ["GITHUB_OUTPUT"], "a") as stream:
            for key, value in verify_downloads(root, output).items():
                stream.write(key + "=" + value + "\n")
    else:
        raise ValueError("unknown baseline operation")
