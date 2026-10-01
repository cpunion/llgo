"""Temporary fork-only native debugger qualification, not a product test."""
from pathlib import Path
import shutil
import subprocess

out = Path("debugger-evidence").resolve()
out.mkdir(exist_ok=True)
source = out / "probe.c"
source.write_text(r'''#include <pthread.h>
#include <stdint.h>
volatile int probe_value = 42;
static pthread_mutex_t lock = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t ready_cond = PTHREAD_COND_INITIALIZER;
static pthread_cond_t release_cond = PTHREAD_COND_INITIALIZER;
static int ready, released;
__attribute__((noinline)) static void worker_wait(void) {
  pthread_mutex_lock(&lock);
  ready++;
  pthread_cond_signal(&ready_cond);
  while (!released) pthread_cond_wait(&release_cond, &lock);
  pthread_mutex_unlock(&lock);
}
__attribute__((noinline)) static void *worker_entry(void *arg) {
  worker_wait();
  return arg;
}
__attribute__((noinline)) static void control_breakpoint(void) {
  __asm__ volatile ("" ::: "memory");
}
int main(void) {
  pthread_t threads[2];
  for (uintptr_t i = 0; i != 2; ++i)
    if (pthread_create(&threads[i], 0, worker_entry, (void *)i)) return 2;
  pthread_mutex_lock(&lock);
  while (ready != 2) pthread_cond_wait(&ready_cond, &lock);
  pthread_mutex_unlock(&lock);
  control_breakpoint();
  pthread_mutex_lock(&lock);
  released = 1;
  pthread_cond_broadcast(&release_cond);
  pthread_mutex_unlock(&lock);
  for (int i = 0; i != 2; ++i) pthread_join(threads[i], 0);
  return probe_value == 42 ? 0 : 1;
}
''')
binary = out / "probe"
subprocess.run(["clang", "-O0", "-gdwarf-4", str(source), "-o", str(binary)], check=True)
gdb = str(out / "gdb")
shutil.copy2(shutil.which("gdb"), gdb)
subprocess.run([gdb, "--configuration"], check=True)
# First qualify the existing unsigned debugger. Only its subprocess receives
# elevation, as a prospective LLGO_GDB wrapper would do; builds/tests stay user.
args = [gdb, "--nx", "--batch", str(binary), "-ex", "set startup-with-shell off",
        "-ex", "set pagination off", "-ex", "break control_breakpoint", "-ex", "run",
        "-ex", "print probe_value", "-ex", "thread apply all backtrace",
        "-ex", "info sharedlibrary", "-ex", "thread apply all info registers rbp rsp rip",
        "-ex", "thread apply all x/8gx $rsp", "-ex", "thread apply all x/4gx $rbp",
        "-ex", "python print('GDB_PYTHON_PTID=' + str(gdb.selected_thread().ptid))",
        "-ex", "continue"]
passed = []
for name, prefix in [("sudo", ["sudo", "-n"]), ("user", [])]:
    try:
        result = subprocess.run(prefix + args, text=True, stdout=subprocess.PIPE,
                                stderr=subprocess.STDOUT, timeout=90)
        output = result.stdout
        good = (result.returncode == 0 and "$1 = 42" in output and
                "control_breakpoint" in output and "GDB_PYTHON_PTID=" in output and
                output.count("in worker_wait") >= 2 and
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
print("GDB_NATIVE_PTHREAD_DWARF4_QUALIFIED=" + ",".join(passed))
