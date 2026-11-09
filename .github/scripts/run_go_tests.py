#!/usr/bin/env python3
"""Render go test -json output and retain per-case timings, including failures."""

import argparse
import contextlib
import heapq
import html
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time


@contextlib.contextmanager
def locked(output):
    with (output / ".go-tests.lock").open("a+b") as lock:
        if os.name == "nt":
            import msvcrt
            lock.seek(0)
            if not lock.read(1):
                lock.write(b"\0")
                lock.flush()
            lock.seek(0)
            msvcrt.locking(lock.fileno(), msvcrt.LK_LOCK, 1)
        else:
            import fcntl
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
        try:
            yield
        finally:
            if os.name == "nt":
                lock.seek(0)
                msvcrt.locking(lock.fileno(), msvcrt.LK_UNLCK, 1)
            else:
                fcntl.flock(lock.fileno(), fcntl.LOCK_UN)


def atomic_json(path, value):
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")
    os.replace(temporary, path)


def coverage_options(command):
    # Unknown bare options may consume the following word, even one starting
    # with '-'. Be conservative rather than rewriting another flag's payload.
    booleans = {"-json", "-v", "-race", "-short", "-failfast", "-cover"}
    index = 1
    while index < len(command):
        argument = command[index]
        if argument in ("-args", "--"):
            break
        name, separator, value = argument.partition("=")
        if name in ("-coverprofile", "-covermode"):
            if not separator:
                if index + 1 == len(command):
                    raise ValueError(f"missing value for {name}")
                value = command[index + 1]
            yield index, name, value, bool(separator)
        if argument.startswith("-") and not separator and name not in booleans:
            index += 1
        index += 1


def claim(command, output, suite):
    directory = output / (suite + "-runs")
    directory.mkdir(exist_ok=True)
    with locked(output):
        first = not any(directory.iterdir())
        if first and (output / (suite + ".jsonl")).exists():
            raise ValueError("existing stage output has no invocation provenance")
        batch = Path(tempfile.mkdtemp(prefix="run-", dir=directory))
        effective = list(command)
        coverage = None
        mode = None
        for index, name, value, joined in coverage_options(command):
            if name == "-covermode":
                if mode is not None:
                    raise ValueError("multiple coverage modes in one command")
                mode = value
            else:
                if coverage is not None:
                    raise ValueError("multiple coverage destinations in one command")
                coverage = {"destination": str(Path(value).resolve()),
                            "path": str(batch / "coverage.txt")}
                if joined:
                    effective[index] = "-coverprofile=" + coverage["path"]
                else:
                    effective[index + 1] = coverage["path"]
        if coverage is not None:
            coverage["mode"] = mode
        record = {"schema": 1, "claimed_ns": time.time_ns(), "started_ns": None,
                  "spawned": False, "command": list(command),
                  "effective_command": effective, "coverage": coverage,
                  "cwd": os.getcwd(), "state": "incomplete", "exit": None,
                  "environment": {name: os.getenv(name, "") for name in (
                      "PATH", "GOROOT", "GOOS", "GOARCH", "GOFLAGS", "GOTOOLCHAIN",
                      "CC", "CXX", "CGO_ENABLED", "CGO_CFLAGS", "CGO_CPPFLAGS",
                      "CGO_CXXFLAGS", "CGO_LDFLAGS", "GITHUB_SHA", "LLGO_ROOT",
                      "LLGO_REQUIRE_MSVC", "LLGO_TEST_EXTENDED_ABI")}}
        atomic_json(batch / "run.json", record)
        raw = batch / "output.jsonl"
        raw.touch()
        mirror = None
        if first:
            try:
                os.link(raw, output / (suite + ".jsonl"))
            except OSError:
                mirror = output / (suite + ".jsonl")
        atomic_json(output / (suite + "-runs.json"),
                    {"complete": False, "success": False, "runs": [str(batch)]})
    return batch, record, mirror


def command_scope(record):
    # The workflow gives xargs canonical import paths after its complete flag
    # prefix. Never remove an option value because it resembles a package.
    command = record["command"]
    packages = {name for name in record["packages"] if "/" in name
                and not name.startswith((".", "/", "-"))
                and not any(character.isspace() for character in name)
                and "\\" not in name and ":" not in name}
    start = len(command)
    if "-args" not in command and "--" not in command:
        while start and command[start - 1] in packages:
            start -= 1
        if start and command[start - 1].startswith("-") and "=" not in command[start - 1]:
            # A bare flag may consume the next word. Preserve it; an ambiguous
            # boolean spelling may refuse batching, but cannot erase a value.
            start = min(start + 1, len(command))
    return (command[:start], record["cwd"], record["environment"])


def merge_profiles(records, destination):
    mode, blocks = None, {}
    for record in records:
        with Path(record["coverage"]["path"]).open(encoding="utf-8") as profile:
            header = profile.readline().strip()
            if header not in ("mode: set", "mode: count", "mode: atomic") or mode not in (None, header):
                raise ValueError("coverage modes differ or are invalid")
            expected = record["coverage"]["mode"]
            if expected is not None and header != "mode: " + expected:
                raise ValueError("coverage mode differs from its command")
            mode = header
            seen = set()
            for line in profile:
                block, statements, hits = line.rstrip().rsplit(" ", 2)
                statements, hits = int(statements), int(hits)
                if block in seen or statements < 0 or hits < 0 or mode == "mode: set" and hits > 1:
                    raise ValueError("invalid or duplicate coverage block")
                seen.add(block)
                previous, count = blocks.get(block, (statements, 0))
                if previous != statements:
                    raise ValueError("coverage statement counts differ")
                blocks[block] = (statements, max(count, hits) if mode == "mode: set" else count + hits)
    temporary = destination.with_name(destination.name + ".tmp")
    try:
        with temporary.open("w", encoding="utf-8") as profile:
            profile.write(mode + "\n")
            for block, (statements, hits) in sorted(blocks.items()):
                profile.write(f"{block} {statements} {hits}\n")
        os.replace(temporary, destination)
    finally:
        temporary.unlink(missing_ok=True)


def publish(output, suite, summary):
    with locked(output):
        runs = []
        errors = []
        for batch in (output / (suite + "-runs")).iterdir():
            try:
                record = json.loads((batch / "run.json").read_text(encoding="utf-8"))
                if (record["schema"] != 1 or not isinstance(record["command"], list)
                        or record["state"] not in ("incomplete", "completed")):
                    raise ValueError("unknown invocation schema")
                if record["state"] == "completed":
                    if (not isinstance(record["exit"], int)
                            or not isinstance(record["packages"], list)
                            or not isinstance(record["cases"], int)):
                        raise ValueError("incomplete invocation result")
                runs.append((batch, record))
            except (OSError, ValueError, KeyError) as error:
                errors.append(f"{batch.name}: {error}")
        runs.sort(key=lambda pair: pair[1]["claimed_ns"])
        complete = not errors and all(record["state"] == "completed" for _, record in runs)
        report = {"complete": complete, "success": False, "errors": errors,
                  "runs": [{"directory": str(batch), **record} for batch, record in runs]}
        if not complete:
            atomic_json(output / (suite + "-runs.json"), report)
            return 1 if errors else None
        records = [record for _, record in runs]
        status = next((record["exit"] for record in records if record["exit"]), 0)
        covered = [record for record in records if record["coverage"] is not None]
        if covered:
            scope = command_scope(records[0])
            if len(covered) != len(records) or any(command_scope(record) != scope for record in records):
                errors.append("coverage command inventory, flags or environment differ")
            destinations = {record["coverage"]["destination"] for record in covered}
            if len(destinations) != 1:
                errors.append("coverage destinations differ")
            if not status and not errors:
                try:
                    merge_profiles(covered, Path(covered[0]["coverage"]["destination"]))
                except (OSError, ValueError) as error:
                    errors.append(str(error))
        status = status or (1 if errors else 0)
        temporary = output / (suite + ".jsonl.tmp")
        with temporary.open("wb") as combined:
            for batch, _ in runs:
                with (batch / "output.jsonl").open("rb") as source:
                    while chunk := source.read(65536):
                        combined.write(chunk)
        os.replace(temporary, output / (suite + ".jsonl"))
        with contextlib.ExitStack() as stack:
            streams = [stack.enter_context((batch / "cases.jsonl").open(encoding="utf-8"))
                       for batch, _ in runs]
            cases = heapq.merge(*(map(json.loads, stream) for stream in streams),
                                key=lambda case: -(case.get("Elapsed") or 0))
            render(cases, sum(record["cases"] for record in records), output, suite, status, summary)
        report.update(success=not status, exit=status)
        atomic_json(output / (suite + "-runs.json"), report)
        return status


def cell(value):
    return html.escape(str(value)).replace("|", "&#124;").replace("\n", " ")


def run(command, output, suite, summary=None):
    output.mkdir(parents=True, exist_ok=True)
    output = output.resolve()
    batch, record, mirror = claim(command, output, suite)
    cases = []
    packages = set()
    print(f"Starting Go test suite {suite}: {command!r}", file=sys.stderr, flush=True)
    with contextlib.ExitStack() as stack:
        log = stack.enter_context((batch / "output.jsonl").open("w", encoding="utf-8", buffering=1))
        live = stack.enter_context(mirror.open("w", encoding="utf-8", buffering=1)) if mirror else None
        try:
            process = subprocess.Popen(record["effective_command"], stdout=subprocess.PIPE, text=True,
                                       encoding="utf-8", errors="replace", bufsize=1)
        except OSError as error:
            record["spawn_error"] = str(error)
            atomic_json(batch / "run.json", record)
            raise
        record.update(spawned=True, pid=process.pid, started_ns=time.time_ns())
        atomic_json(batch / "run.json", record)
        with process:
            for line in process.stdout:
                log.write(line)
                if live:
                    live.write(line)
                try:
                    event = json.loads(line)
                except ValueError:
                    # Preserve unexpected tool output so diagnostics never disappear.
                    print(line, end="", flush=True)
                    continue
                if not isinstance(event, dict):
                    print(line, end="", flush=True)
                    continue
                if event.get("Package"):
                    packages.add(event["Package"])
                if isinstance(event.get("Output"), str):
                    print(event["Output"], end="", flush=True)
                if event.get("Test") and event.get("Action") in {"pass", "fail", "skip"}:
                    cases.append({key: event.get(key) for key in
                                  ("Package", "Test", "Action", "Elapsed")})
            status = process.wait()
    cases.sort(key=lambda case: case.get("Elapsed") or 0, reverse=True)
    with (batch / "cases.jsonl").open("w", encoding="utf-8") as destination:
        for case in cases:
            destination.write(json.dumps(case) + "\n")
    status = status if status >= 0 else 128 - status
    render(iter(cases), len(cases), batch, suite, status, None)
    record.update(state="completed", exit=status, completed_ns=time.time_ns(),
                  cases=len(cases), packages=sorted(packages))
    atomic_json(batch / "run.json", record)
    combined = publish(output, suite, summary)
    print()
    # Print only this immutable invocation outside the publication lock. A slow
    # Windows pipe must not block another batch's completion or repeat its log.
    with (batch / f"{suite}-times.txt").open(encoding="utf-8") as timings:
        for line in timings:
            print(line, end="", flush=True)
    return status if combined is None else combined


def render(cases, count, output, suite, status, summary):
    slow = {"Slowest tests": [], "Slowest subtests": []}
    # Keep every case in a plain-text artifact and the job log. The summary
    # remains bounded because GitHub limits its size. Parent elapsed times
    # include their subtests and must not be added to child elapsed times.
    with (output / f"{suite}-times.txt").open("w", encoding="utf-8") as report, \
            (output / f"{suite}-times.json").open("w", encoding="utf-8") as json_report:
        json_report.write("[\n")
        report.write(f"Go test timings: {suite} ({count} cases, exit {status})\n")
        report.write("Parent times include subtests; do not sum both.\n")
        report.write("Seconds\tResult\tPackage\tCase\n")
        for index, case in enumerate(cases):
            json_report.write((",\n" if index else "") + json.dumps(case))
            report.write(f"{case['Elapsed'] or 0:.2f}\t{case['Action']}\t"
                         f"{case['Package']}\t{case['Test']}\n")
            group = slow["Slowest subtests" if "/" in case["Test"] else "Slowest tests"]
            if len(group) < 15:
                group.append(case)
        json_report.write("\n]\n")
    if summary:
        with Path(summary).open("a", encoding="utf-8") as report:
            report.write(f"\n### Go tests: {cell(suite)} (exit {status})\n\n")
            for title, rows in slow.items():
                report.write(f"{title}\n\n| Seconds | Package | Case | Result |\n")
                report.write("| ---: | --- | --- | --- |\n")
                for case in rows[:15]:
                    report.write(f"| {case['Elapsed'] or 0:.2f} | {cell(case['Package'])} | "
                                 f"{cell(case['Test'])} | {case['Action']} |\n")
                report.write("\n")


def main():
    # Redirected Windows streams may default to cp1252, but Go diagnostics and
    # test names are UTF-8. Keep them intact on both the live log and stderr.
    sys.stdout.reconfigure(encoding="utf-8", errors="backslashreplace")
    sys.stderr.reconfigure(encoding="utf-8", errors="backslashreplace")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path, default=Path("go-test-timings"))
    parser.add_argument("suite", choices=("main", "build", "compiler", "language",
                                          "runtime-helpers", "embedded"))
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if not args.command:
        parser.error("a go test -json command is required")
    return run(args.command, args.output_dir, args.suite, os.getenv("GITHUB_STEP_SUMMARY"))


if __name__ == "__main__":
    sys.exit(main())
