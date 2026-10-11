"""Bounded, paired Windows BDWGC probe runs; no dependencies or test retries."""
import argparse
import ctypes
import json
import os
from pathlib import Path
import struct
import subprocess
import time


def exports(path):
    """Read exported counter RVAs without loading the probe or its DLLs."""
    data = Path(path).read_bytes()
    pe = struct.unpack_from("<I", data, 0x3C)[0]
    if data[pe:pe + 4] != b"PE\0\0":
        raise ValueError("not a PE image")
    sections, optional_size = struct.unpack_from("<H12xH", data, pe + 6)
    optional = pe + 24
    magic = struct.unpack_from("<H", data, optional)[0]
    directory = optional + (96 if magic == 0x10B else 112)
    export_rva = struct.unpack_from("<I", data, directory)[0]
    regions = []
    for i in range(sections):
        entry = optional + optional_size + 40 * i
        virtual_size, rva, raw_size, offset = struct.unpack_from("<IIII", data, entry + 8)
        regions.append((rva, max(virtual_size, raw_size), offset))

    def at(rva):
        for begin, size, offset in regions:
            if begin <= rva < begin + size:
                return offset + rva - begin
        raise ValueError(f"unmapped RVA {rva:x}")

    table = at(export_rva)
    names_count, addresses, names, ordinals = struct.unpack_from("<IIII", data, table + 24)
    result = {}
    for i in range(names_count):
        name_rva = struct.unpack_from("<I", data, at(names) + i * 4)[0]
        start = at(name_rva)
        name = data[start:data.index(0, start)].decode("ascii").lstrip("_")
        if name.startswith("fairness_"):
            ordinal = struct.unpack_from("<H", data, at(ordinals) + i * 2)[0]
            result[name] = struct.unpack_from("<I", data, at(addresses) + ordinal * 4)[0]
    if len(result) != 5:
        raise ValueError(f"expected five exported counters, got {result}")
    return result


class Sampler:
    def __init__(self, pid, executable, counters):
        from ctypes import wintypes as w

        class Module(ctypes.Structure):
            _fields_ = [
                ("dwSize", w.DWORD), ("th32ModuleID", w.DWORD),
                ("th32ProcessID", w.DWORD), ("GlblcntUsage", w.DWORD),
                ("ProccntUsage", w.DWORD), ("modBaseAddr", ctypes.c_void_p),
                ("modBaseSize", w.DWORD), ("hModule", w.HANDLE),
                ("szModule", w.WCHAR * 256), ("szExePath", w.WCHAR * 260),
            ]

        self.api = ctypes.WinDLL("kernel32", use_last_error=True)
        self.api.OpenProcess.argtypes = [w.DWORD, w.BOOL, w.DWORD]
        self.api.OpenProcess.restype = w.HANDLE
        self.api.CloseHandle.argtypes = [w.HANDLE]
        self.api.CreateToolhelp32Snapshot.argtypes = [w.DWORD, w.DWORD]
        self.api.CreateToolhelp32Snapshot.restype = w.HANDLE
        self.api.Module32FirstW.argtypes = [w.HANDLE, ctypes.POINTER(Module)]
        self.api.Module32NextW.argtypes = [w.HANDLE, ctypes.POINTER(Module)]
        self.api.ReadProcessMemory.argtypes = [w.HANDLE, ctypes.c_void_p,
                                             ctypes.c_void_p, ctypes.c_size_t,
                                             ctypes.POINTER(ctypes.c_size_t)]
        self.api.ReadProcessMemory.restype = w.BOOL
        self.handle = self.api.OpenProcess(0x0010 | 0x0400, False, pid)
        if not self.handle:
            raise OSError(ctypes.get_last_error(), "OpenProcess")
        self.pid, self.Module = pid, Module
        self.executable = Path(executable).name.lower()
        self.counters, self.base = counters, None

    def sample(self):
        if self.base is None:
            snapshot = self.api.CreateToolhelp32Snapshot(0x08 | 0x10, self.pid)
            if snapshot == ctypes.c_void_p(-1).value:
                return {"sample_error": f"snapshot {ctypes.get_last_error()}"}
            try:
                item = self.Module()
                item.dwSize = ctypes.sizeof(item)
                found = self.api.Module32FirstW(snapshot, ctypes.byref(item))
                while found:
                    if item.szModule.lower() == self.executable:
                        self.base = item.modBaseAddr
                        break
                    found = self.api.Module32NextW(snapshot, ctypes.byref(item))
            finally:
                self.api.CloseHandle(snapshot)
            if self.base is None:
                return {"sample_error": "executable not yet present"}
        result = {}
        for name, rva in self.counters.items():
            value = ctypes.c_int32()
            read = ctypes.c_size_t()
            if self.api.ReadProcessMemory(self.handle, self.base + rva,
                                          ctypes.byref(value), 4, ctypes.byref(read)):
                result[name] = value.value
            else:
                result[name] = f"read error {ctypes.get_last_error()}"
        return result

    def close(self):
        self.api.CloseHandle(self.handle)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("executable", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--rounds", type=int, default=3)
    parser.add_argument("--timeout", type=float, default=90)
    parser.add_argument("--modes", default="0,1", help="0=no collector, 1=Sleep(0), 2=no yield")
    args = parser.parse_args()
    if os.name != "nt":
        parser.error("execution requires Windows; PE export parsing is portable")
    args.executable = args.executable.resolve()
    counters = exports(args.executable)
    modes = [int(mode) for mode in args.modes.split(",")]
    if args.rounds < 1 or args.timeout <= 0 or not modes or any(m not in (0, 1, 2) for m in modes):
        parser.error("invalid rounds, timeout or modes")
    args.output.mkdir(parents=True, exist_ok=True)
    report = {"executable": str(args.executable), "counter_rvas": counters,
              "timeout": args.timeout, "rounds": args.rounds, "runs": []}
    failed = False
    for round_index in range(args.rounds):
        # Rotate order so a fixed first-run/host-load effect cannot favor a mode.
        order = modes[round_index % len(modes):] + modes[:round_index % len(modes)]
        for mode in order:
            result = {"round": round_index, "mode": mode, "samples": []}
            log = args.output / f"round-{round_index}-mode-{mode}.log"
            with log.open("wb") as stream:
                start = time.monotonic()
                child = subprocess.Popen([str(args.executable), str(mode)],
                                         stdout=stream, stderr=subprocess.STDOUT)
                sampler = None
                try:
                    # A very short successful process may exit before OpenProcess.
                    if child.poll() is None:
                        try:
                            sampler = Sampler(child.pid, args.executable, counters)
                        except OSError as exc:
                            result["sampling_error"] = str(exc)
                    while child.poll() is None:
                        elapsed = time.monotonic() - start
                        if sampler:
                            result["samples"].append({"seconds": round(elapsed, 3), **sampler.sample()})
                        if elapsed >= args.timeout:
                            result["timeout"] = True
                            child.kill()
                            break
                        try:
                            child.wait(timeout=min(1, args.timeout - elapsed))
                        except subprocess.TimeoutExpired:
                            pass
                    child.wait(timeout=10)
                finally:
                    if sampler:
                        sampler.close()
                    if child.poll() is None:
                        child.kill()
                        child.wait(timeout=10)
                result["seconds"] = round(time.monotonic() - start, 3)
                result["exit_code"] = child.returncode
                failed |= child.returncode != 0 or result.get("timeout", False)
            result["output"] = log.read_text(errors="replace").strip()
            report["runs"].append(result)
            (args.output / "results.json").write_text(json.dumps(report, indent=2) + "\n")
            print(json.dumps({k: v for k, v in result.items() if k != "samples"}), flush=True)
    return int(failed)


if __name__ == "__main__":
    raise SystemExit(main())
