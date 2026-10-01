"""Temporary fork-only native debugger qualification, not a product test."""
import os
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
certificate = out / "gdb.crt"
key = out / "gdb.key"
p12 = out / "gdb.p12"
keychain = out / "gdb.keychain-db"
subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
                "-subj", "/CN=LLGo CI GDB", "-addext", "extendedKeyUsage=codeSigning",
                "-keyout", str(key), "-out", str(certificate)], check=True)
subprocess.run(["openssl", "pkcs12", "-export", "-inkey", str(key), "-in", str(certificate),
                "-out", str(p12), "-passout", "pass:", "-legacy"], check=True)
subprocess.run(["security", "create-keychain", "-p", "", str(keychain)], check=True)
subprocess.run(["security", "unlock-keychain", "-p", "", str(keychain)], check=True)
subprocess.run(["security", "import", str(p12), "-k", str(keychain), "-P", "", "-T", "/usr/bin/codesign"], check=True)
subprocess.run(["security", "set-key-partition-list", "-S", "apple-tool:,apple:", "-s", "-k", "", str(keychain)], check=True)
subprocess.run(["sudo", "security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k",
                "/Library/Keychains/System.keychain", str(certificate)], check=True)
entitlements = out / "gdb-entitlements.plist"
entitlements.write_text('<?xml version="1.0"?><plist version="1.0"><dict><key>com.apple.security.cs.debugger</key><true/></dict></plist>')
subprocess.run(["codesign", "-f", "-s", "LLGo CI GDB", "--keychain", str(keychain),
                "--entitlements", str(entitlements), gdb], check=True)
subprocess.run(["sudo", "DevToolsSecurity", "-enable"], check=True)
subprocess.run(["sudo", "killall", "taskgated"], check=False)
# Keep ephemeral signing material out of the uploaded evidence.
for item in [key, p12, keychain]:
    item.unlink(missing_ok=True)
args = [gdb, "--nx", "--batch", str(binary), "-ex", "set startup-with-shell off",
        "-ex", "set pagination off", "-ex", "break main", "-ex", "run",
        "-ex", "print probe_value", "-ex", "backtrace", "-ex", "continue"]
passed = []
for name, prefix in [("user", []), ("sudo", ["sudo", "-n"])]:
    try:
        result = subprocess.run(prefix + args, text=True, stdout=subprocess.PIPE,
                                stderr=subprocess.STDOUT, timeout=90)
        output = result.stdout
        good = result.returncode == 0 and "$1 = 42" in output and "exited normally" in output
    except subprocess.TimeoutExpired as exc:
        output = str(exc.stdout)
        good = False
    (out / f"{name}.log").write_text(output)
    print(f"{name}: {'PASS' if good else 'FAIL'}\n{output}", flush=True)
    if good:
        passed.append(name)
if not passed:
    raise SystemExit("No working native Darwin GDB execution mode")
