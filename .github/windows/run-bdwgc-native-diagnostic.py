"""Run only the owned native GC reproducer; failures remain failures."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import time


ROOT = Path.cwd()
OUT = ROOT / "_out/bdwgc-native-diagnostic"
OUT.mkdir(parents=True, exist_ok=False)
TOOLCHAIN = Path(os.environ["BDWGC_LLVM_MINGW"])
PREFIX = Path(os.environ["BDWGC_PREFIX"])
COMPILER = TOOLCHAIN / "bin/i686-w64-mingw32-clang.exe"


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def command(label, args):
    result = subprocess.run(args, capture_output=True, timeout=120)
    (OUT / (label + ".stdout")).write_bytes(result.stdout)
    (OUT / (label + ".stderr")).write_bytes(result.stderr)
    if result.returncode:
        raise RuntimeError(f"{label} failed: exit {result.returncode}")
    return result.stdout.decode(errors="replace")


version = command("compiler-version", [str(COMPILER), "--version"])
if "clang version 22." not in version:
    raise RuntimeError("the diagnostic requires the pinned LLVM 22 compiler")
head = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
source = (ROOT / ".github/windows/test-bdwgc-heap-lock.c").read_text()
(OUT / "bare.c").write_text(source)
diagnostic = source.replace(
    "static volatile LONG client_faults;",
    """static volatile LONG client_faults;
__declspec(dllexport) volatile LONG diag_collect_phase;
__declspec(dllexport) volatile LONG diag_heap_phase;
__declspec(dllexport) volatile LONG diag_last_event;
__declspec(dllexport) volatile LONG diag_event_count;
__declspec(dllexport) volatile LONG diag_parallel;
static int diag_no_heap;
""",
)
diagnostic = diagnostic.replace(
    "    if (!HeapLock(GetProcessHeap()))",
    "    InterlockedExchange(&diag_heap_phase, 1);\n"
    "    if (!diag_no_heap && !HeapLock(GetProcessHeap()))",
).replace(
    "    SetEvent(locked);",
    "    InterlockedExchange(&diag_heap_phase, 2);\n    SetEvent(locked);",
).replace(
    "    HeapUnlock(GetProcessHeap());",
    "    InterlockedExchange(&diag_heap_phase, 3);\n"
    "    if (!diag_no_heap) HeapUnlock(GetProcessHeap());\n"
    "    InterlockedExchange(&diag_heap_phase, 4);",
).replace(
    "    if (event == GC_EVENT_PRE_STOP_WORLD",
    "    InterlockedExchange(&diag_last_event, (LONG)event);\n"
    "    InterlockedIncrement(&diag_event_count);\n"
    "    if (event == GC_EVENT_PRE_STOP_WORLD",
).replace(
    "    SetEvent(collector_ready);",
    "    if (GetEnvironmentVariableA(\"LLGO_GC_WARM_SIGNAL\", NULL, 0))\n"
    "        signal(SIGSEGV, SIG_DFL);\n"
    "    InterlockedExchange(&diag_collect_phase, 1);\n    SetEvent(collector_ready);",
).replace(
    "    GC_gcollect();\n    SetEvent(release_heap);",
    "    InterlockedExchange(&diag_collect_phase, 2);\n"
    "    GC_gcollect();\n    InterlockedExchange(&diag_collect_phase, 3);\n"
    "    SetEvent(release_heap);",
).replace(
    "    GC_gcollect();\n    if (signal(SIGSEGV, SIG_DFL)",
    "    InterlockedExchange(&diag_collect_phase, 4);\n"
    "    GC_gcollect();\n    InterlockedExchange(&diag_collect_phase, 5);\n"
    "    if (signal(SIGSEGV, SIG_DFL)",
).replace(
    "    GC_INIT();",
    "    diag_no_heap = GetEnvironmentVariableA(\"LLGO_GC_NO_HEAP\", NULL, 0) != 0;\n"
    "    GC_INIT();\n    diag_parallel = GC_get_parallel();",
)
diagnostic = diagnostic.replace(
    "int main(void)",
    """static unsigned __stdcall delayed_registered_heap(void *unused)
{
    struct GC_stack_base base;
    unsigned result;
    Sleep(100);
    if (GC_get_stack_base(&base) != GC_SUCCESS
        || GC_register_my_thread(&base) != GC_SUCCESS)
        TerminateProcess(GetCurrentProcess(), 11);
    result = hold_heap(unused);
    GC_unregister_my_thread();
    return result;
}

int main(void)""",
).replace(
    "    threads[0] = (HANDLE)GC_beginthreadex(NULL, 0, hold_heap, NULL, 0, NULL);",
    "    if (GetEnvironmentVariableA(\"LLGO_GC_DELAY_REGISTER\", NULL, 0)) {\n"
    "        GC_allow_register_threads();\n"
    "        threads[0] = (HANDLE)_beginthreadex(NULL, 0, delayed_registered_heap, NULL, 0, NULL);\n"
    "    } else {\n"
    "        threads[0] = (HANDLE)GC_beginthreadex(NULL, 0, hold_heap, NULL, 0, NULL);\n"
    "    }",
)
if diagnostic == source or diagnostic.count("diag_collect_phase, 2") != 1:
    raise RuntimeError("diagnostic source markers do not match the original fixture")
(OUT / "diagnostic.c").write_text(diagnostic)

include = "-I" + str(PREFIX / "include")
library = "-L" + str(PREFIX / "lib")
for name in ("bare", "diagnostic"):
    command("compile-" + name, [str(COMPILER), "-O2", *(["-g"] if name == "diagnostic" else []),
            str(OUT / (name + ".c")), include, library, "-lgc",
            "-o", str(OUT / (name + ".exe"))])
command("compile-dumper", [str(COMPILER), "-O0", "-g",
        ".github/windows/bdwgc-thread-dump.c", "-ldbghelp",
        "-o", str(OUT / "thread-dump.exe")])

environment = {key: os.environ[key] for key in
               ("SystemRoot", "WINDIR", "SystemDrive", "COMSPEC") if key in os.environ}
environment.update(TEMP=str(OUT), TMP=str(OUT))
environment["PATH"] = os.pathsep.join((str(PREFIX / "bin"),
    str(TOOLCHAIN / "i686-w64-mingw32/bin"), str(TOOLCHAIN / "bin"),
    str(Path(os.environ["SystemRoot"]) / "System32"), os.environ["SystemRoot"]))
metadata = {"head": head, "compiler_sha256": sha(COMPILER),
            "collector_DLLs": {p.name: sha(p) for p in (PREFIX / "bin").glob("*.dll")},
            "source_files": {p.name: sha(p) for p in OUT.glob("*.c")},
            "child_environment_keys": sorted(environment),
            "processor_count": os.cpu_count(),
            "thread_dump_scope": "only the exact owned child executable; original suspension counts restored"}
(OUT / "inputs.json").write_text(json.dumps(metadata, indent=2) + "\n")
results = []
cases = [("bare", "default", 50), ("diagnostic", "default", 50),
         ("diagnostic", "warm-signal", 50),
         ("diagnostic", "delayed-registration", 20),
         ("diagnostic", "one-marker", 10), ("diagnostic", "four-markers", 10),
         ("diagnostic", "no-heap", 5)]
for binary, mode, count in cases:
    for index in range(count):
        label = f"{binary}-{mode}-{index}"
        owned = OUT / label
        owned.mkdir()
        env = dict(environment)
        if mode == "one-marker":
            env["GC_MARKERS"] = "1"
        elif mode == "four-markers":
            env["GC_MARKERS"] = "4"
        elif mode == "no-heap":
            env["LLGO_GC_NO_HEAP"] = "1"
        elif mode == "warm-signal":
            env["LLGO_GC_WARM_SIGNAL"] = "1"
        elif mode == "delayed-registration":
            env["LLGO_GC_DELAY_REGISTER"] = "1"
        executable = (OUT / (binary + ".exe")).resolve()
        started = time.monotonic()
        record = {"binary": binary, "mode": mode, "index": index}
        with (owned / "stdout").open("wb") as stdout, (owned / "stderr").open("wb") as stderr:
            child = subprocess.Popen([str(executable)], env=env, cwd=owned,
                                     stdout=stdout, stderr=stderr)
            record["PID"] = child.pid
            try:
                child.wait(timeout=3)
            except subprocess.TimeoutExpired:
                pass
            if child.poll() is None:
                dump = owned / "child.dmp"
                with (owned / "threads.stdout").open("wb") as so, (owned / "threads.stderr").open("wb") as se:
                    try:
                        result = subprocess.run([str(OUT / "thread-dump.exe"),
                            str(child.pid), str(executable), str(dump)], env=env,
                            stdout=so, stderr=se, timeout=15)
                        record["dumper_exit"] = result.returncode
                    except subprocess.TimeoutExpired:
                        record["dumper_timeout"] = True
            try:
                record["exit"] = child.wait(timeout=30)
                record["natural_exit"] = True
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait()
                record.update(exit=child.returncode, natural_exit=False, timeout=True)
        record["seconds"] = time.monotonic() - started
        results.append(record)
        (OUT / "results.json").write_text(json.dumps(results, indent=2) + "\n")
        print(json.dumps(record), flush=True)
failed = [row for row in results if row["exit"] != 0 or not row["natural_exit"]]
print(json.dumps({"executions": len(results), "failed": len(failed),
                  "this_is_diagnostic_not_a_passing_regression": bool(failed)}))
raise SystemExit(bool(failed))
