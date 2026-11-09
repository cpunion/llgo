"""Materialize source-owned Linux coverage commands; never execute a compiler."""

import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
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
DEPENDENCY_ROOT = "_demo/c/cargs/libs"
# The Linux setup-demo-deps archive and its one rendered pkg-config file.
DEPENDENCY_FILES = {DEPENDENCY_ROOT + "/" + name for name in (
    "cargs_linux_amd64.zip", "conaninfo.txt", "conanmanifest.txt", "include/cargs.h",
    "lib/libcargs.so", "lib/pkgconfig/cargs.pc.tmpl", "lib/pkgconfig/cargs.pc", "licenses/LICENSE.md",
)}
# Complete extracted union of d4b's two SHA-pinned Linux ESP QEMU archives
# (riscv32, then xtensa); includes files hidden by the original Git ignores.
QEMU_FILES = {".cache/qemu/" + name: sha for name, sha in {
    "bin/qemu-system-riscv32": "9d5c8bd2afa79dccca8d1b7b830f746a2218563eac8765c76028bddced66b930",
    "bin/qemu-system-xtensa": "57cd2d1909c08c2b810f4bf7f6fb2c1d2523fc8d3b564e9d5e871c0f471381f7",
    "include/fdt.h": "0841a268996ad929ee062303409b9f1bd536c1dfce8516fc121bed3331e41f4e",
    "include/libfdt.h": "5d0c45df7f5b21e3a3e79e42b61c7c92b4be5683fe3776d317274d4bc343e6bc",
    "include/libfdt_env.h": "273bb264cacd60bb005cfa558bee084558469b9107eaf40d7e1362571ed1bc94",
    "lib/x86_64-linux-gnu/libfdt.a": "0c55489bee31bf638930a333d9153feb131b444a519ca4e5a2a838be8df85043",
    "lib/x86_64-linux-gnu/pkgconfig/libfdt.pc": "3e261cb9bb13388fdc0b780d747cf26afc9b9a7e80bed8bd4005ac26fb4e2cd4",
    "share/qemu/esp32s3_rev0_rom.bin": "b5c7090cc22efce51c1323cef31ca96fcd673a5b1d1b1017e219d393f2659913",
    "share/qemu/esp32-v3-rom.bin": "9115bbcd03da9f54d3baca5ad94d588a43da54ed332347137ba3dc9010df258b",
    "share/qemu/esp32c3-rom.bin": "0de1e65020e803bea0d7443dca149d61895e01fca3bb9c82d073234eebd73f99",
    "share/qemu/esp32-v3-rom-app.bin": "6d3240e7cdb1ec9c1d683515e0a8578e9fb8ece3e7736cb9a996512c656e8442",
}.items()}


def dependency_names(producer):
    return ((DEPENDENCY_FILES if producer != "browser" else set())
            | (set(QEMU_FILES) if producer in ("go", "llgo") else set()))


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def git(root, *args):
    return subprocess.check_output(["git", *args], cwd=root, text=True).strip("\n")


def snapshot(root):
    if (git(root, "rev-parse", "HEAD") != SOURCE
            or git(root, "status", "--porcelain", "--untracked-files=no")):
        raise ValueError("baseline tracked source must be the clean exact commit")
    if (root / ".go-version").read_text().strip() != "1.27.0":
        raise ValueError("baseline SDK must retain its original 1.27.0 pin")
    if any(digest(root / name) != expected for name, expected in WORKFLOWS.items()):
        raise ValueError("original coverage workflow changed")
    return {name: digest(root / name) for name in git(root, "ls-files", "-z").split("\0") if name}


def dependencies(root, producer):
    files = {}
    expected = dependency_names(producer)
    for directory in (root / DEPENDENCY_ROOT, root / ".cache/qemu"):
        if directory.is_symlink():
            raise ValueError("redirected dependency directory")
        for path in directory.rglob("*") if directory.exists() else ():
            if path.is_symlink() and path.resolve().is_dir():
                raise ValueError("redirected dependency subdirectory")
            if path.is_dir():
                continue
            name = path.relative_to(root).as_posix()
            if (name not in expected or not path.resolve().is_relative_to(directory.resolve())
                    or not stat.S_ISREG(path.stat().st_mode)):
                raise ValueError("unknown or redirected native dependency: " + name)
            files[name] = {"sha256": digest(path), "link": os.readlink(path) if path.is_symlink() else None}
            if name in QEMU_FILES and files[name]["sha256"] != QEMU_FILES[name]:
                raise ValueError("original QEMU package payload changed: " + name)
    if set(files) != expected:
        raise ValueError("incomplete original native dependency set")
    return files


def output_names(result):
    # Final names come from the original uploader; intermediate profile names
    # and timing suites come from its selected original commands, not a glob.
    scripts = "\n".join(result["scripts"].values())
    names = set(result["files"])
    names.update(name.removeprefix("../") for name in
                 re.findall(r'-coverprofile=["\']?([A-Za-z0-9_./-]+)', scripts))
    for group in re.findall(r"for profile in ([A-Za-z0-9 -]+); do", scripts):
        names.update(profile + ".txt" for profile in group.split())
    suites = re.findall(r"run_go_tests\.py(?: --output-dir \.\./go-test-timings)? ([a-z]+)", scripts)
    for suite in suites:
        names.update("go-test-timings/" + suite + suffix
                     for suffix in (".jsonl", "-times.json", "-times.txt"))
    if "run_build_tests.py" in scripts:
        names.add("go-test-timings/build-shards.json")
    if "bash .github/workflows/test_demo.sh" in scripts:
        names.add("result.md")
    return names


def outputs(root, result, after):
    allowed = output_names(result) if after else set()
    names = {name for name in allowed if (root / name).exists()}
    for directory in (root / "go-test-timings", root / "coverage-browser"):
        if directory.is_symlink():
            raise ValueError("redirected output directory")
        for path in directory.rglob("*") if directory.exists() else ():
            if not path.is_dir() or path.is_symlink():
                name = path.relative_to(root).as_posix()
                if name not in allowed:
                    raise ValueError("unknown test output: " + name)
    untracked = {name for name in git(root, "ls-files", "--others", "--exclude-standard", "-z").split("\0") if name}
    unknown = untracked - dependency_names(result["producer"]) - names
    if unknown:
        raise ValueError("unknown untracked baseline input: " + ", ".join(sorted(unknown)))
    for name in names:
        path = root / name
        if path.is_symlink() or not stat.S_ISREG(path.stat().st_mode):
            raise ValueError("redirected or nonregular test output: " + name)
    return {name: digest(root / name) for name in sorted(names)}


def archive(root, output, category, files):
    for name in files:
        target = output / category / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(root / name, target)


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
    if any("bash .github/workflows/test_demo.sh" in script for script in scripts.values()):
        if "tee -a result.md" not in (root / ".github/workflows/test_demo.sh").read_text():
            raise ValueError("original demo summary output is not declared by its source script")
    upload = next(step for step in steps if step.get("uses", "").startswith("codecov/"))
    return {"producer": producer, "workflow": workflow, "job": job,
            "flag": upload["with"]["flags"], "files": upload["with"]["files"].split(","),
            "scripts": scripts}


def prepare(root, output, producer):
    output.mkdir(parents=True, exist_ok=True)
    (output / "initial-source.json").write_text(json.dumps({
        "head": git(root, "rev-parse", "HEAD"),
        "status": git(root, "status", "--porcelain", "--untracked-files=all"),
    }, indent=2) + "\n")
    source = snapshot(root)
    result = definition(root, producer)
    deps = dependencies(root, producer)
    outputs(root, result, False)
    for key, script in result["scripts"].items():
        (output / (key + ".sh")).write_text(script)
    result.update(source_commit=SOURCE, source_files=source, dependencies=deps, original_go_version="1.27.0")
    (output / "provenance.json").write_text(json.dumps(result, indent=2) + "\n")
    return result


def verify(root, output):
    result = json.loads((output / "provenance.json").read_text())
    if result["source_commit"] != SOURCE or result["source_files"] != snapshot(root):
        raise ValueError("baseline source changed during tests")
    expected = definition(root, result["producer"])
    if any(result[key] != value for key, value in expected.items()):
        raise ValueError("coverage producer differs from original workflow")
    if any((output / (key + ".sh")).read_text() != script for key, script in expected["scripts"].items()):
        raise ValueError("materialized original command changed")
    deps = dependencies(root, result["producer"])
    if deps != result["dependencies"]:
        raise ValueError("native dependencies changed during tests")
    generated = outputs(root, result, True)
    reports = {}
    for name in result["files"]:
        path = root / name
        if not path.is_file() or not path.read_text().startswith("mode: ") or len(path.read_text().splitlines()) < 2:
            raise ValueError("missing or empty coverage report: " + name)
        reports[name] = digest(path)
        target = output / "reports" / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(path, target)
    archive(root, output, "dependencies", deps)
    archive(root, output, "outputs", generated)
    (output / "verified.json").write_text(json.dumps({
        "source_files": result["source_files"], "dependencies": deps,
        "outputs": generated, "reports": reports,
    }, indent=2) + "\n")
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
        if any((output / (key + ".sh")).read_text() != script for key, script in expected["scripts"].items()):
            raise ValueError("archived original command changed")
        verified = json.loads((output / "verified.json").read_text())
        if verified["source_files"] != source or verified["dependencies"] != result["dependencies"]:
            raise ValueError("producer pre/post provenance differs")
        deps = verified["dependencies"]
        if set(deps) != dependency_names(producer):
            raise ValueError("incomplete archived dependency set")
        for name, observation in deps.items():
            if name in QEMU_FILES and observation["sha256"] != QEMU_FILES[name]:
                raise ValueError("archived QEMU payload differs from original packages")
            if digest(output / "dependencies" / name) != observation["sha256"]:
                raise ValueError("archived native dependency changed")
        generated = verified["outputs"]
        if not set(generated) <= output_names(expected):
            raise ValueError("unknown archived test output")
        if any(digest(output / "outputs" / name) != value for name, value in generated.items()):
            raise ValueError("archived test output changed")
        reports = verified["reports"]
        if set(reports) != set(expected["files"]):
            raise ValueError("incomplete original report set")
        if any(generated.get(name) != value for name, value in reports.items()):
            raise ValueError("coverage report differs from output observation")
        paths = [output / "reports" / name for name in expected["files"]]
        if any(digest(path) != reports[name] for name, path in zip(expected["files"], paths)):
            raise ValueError("downloaded coverage report changed")
        allowed = {"initial-source.json", "provenance.json", "verified.json", "source.txt",
                   "git-status.txt", "go-version.txt", "go-env.json", "llvm-version.txt"}
        allowed.update(key + ".sh" for key in expected["scripts"])
        for category, manifest in (("dependencies", deps), ("outputs", generated), ("reports", reports)):
            allowed.update(category + "/" + name for name in manifest)
        for path in output.rglob("*"):
            if path.is_symlink() or (not path.is_dir() and path.relative_to(output).as_posix() not in allowed):
                raise ValueError("unknown or redirected baseline artifact entry")
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
