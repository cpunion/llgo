"""Temporary fork-only native debugger qualification, not a product test."""
from pathlib import Path
import shutil
import subprocess

out = Path("debugger-evidence").resolve()
out.mkdir(exist_ok=True)
source = out / "probe.c"
source.write_text('volatile int probe_value = 42;\nint main(void) { return probe_value == 42 ? 0 : 1; }\n')
binary = out / "probe"
subprocess.run(["clang", "-O0", "-gdwarf-4", str(source), "-o", str(binary)], check=True)
gdb = str(out / "gdb")
shutil.copy2(shutil.which("gdb"), gdb)
subprocess.run([gdb, "--configuration"], check=True)
# First qualify the existing unsigned debugger. Only its subprocess receives
# elevation, as a prospective LLGO_GDB wrapper would do; builds/tests stay user.
args = [gdb, "--nx", "--batch", str(binary), "-ex", "set startup-with-shell off",
        "-ex", "set pagination off", "-ex", "break main", "-ex", "run",
        "-ex", "print probe_value", "-ex", "backtrace",
        "-ex", "python print('GDB_PYTHON_PTID=' + str(gdb.selected_thread().ptid))",
        "-ex", "continue"]
passed = []
for name, prefix in [("sudo", ["sudo", "-n"]), ("user", [])]:
    try:
        result = subprocess.run(prefix + args, text=True, stdout=subprocess.PIPE,
                                stderr=subprocess.STDOUT, timeout=90)
        output = result.stdout
        good = (result.returncode == 0 and "$1 = 42" in output and
                "probe.c:2" in output and "GDB_PYTHON_PTID=" in output and
                "exited normally" in output)
    except subprocess.TimeoutExpired as exc:
        output = str(exc.stdout)
        good = False
    (out / f"{name}.log").write_text(output)
    print(f"{name}: {'PASS' if good else 'FAIL'}\n{output}", flush=True)
    if good:
        passed.append(name)
if not passed:
    raise SystemExit("No working native Darwin GDB execution mode")
print("GDB_NATIVE_DWARF4_QUALIFIED=" + ",".join(passed))
