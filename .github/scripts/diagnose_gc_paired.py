"""Sample BDWGC collection number out-of-process without changing test code."""
import ctypes as C
from ctypes import wintypes as W
import json
from pathlib import Path
import re
import subprocess
import time

K = C.WinDLL('kernel32', use_last_error=True)
P = C.WinDLL('psapi', use_last_error=True)
K.OpenProcess.argtypes = [W.DWORD, W.BOOL, W.DWORD]
K.OpenProcess.restype = W.HANDLE
K.ReadProcessMemory.argtypes = [W.HANDLE, C.c_void_p, C.c_void_p, C.c_size_t, C.POINTER(C.c_size_t)]
K.CloseHandle.argtypes = [W.HANDLE]
K.GetProcessTimes.argtypes = [W.HANDLE] + [C.POINTER(W.FILETIME)] * 4
P.EnumProcessModulesEx.argtypes = [W.HANDLE, C.c_void_p, W.DWORD, C.POINTER(W.DWORD), W.DWORD]
P.GetModuleFileNameExW.argtypes = [W.HANDLE, C.c_void_p, W.LPWSTR, W.DWORD]


def collection_address(handle):
    modules = (C.c_void_p * 1024)()
    needed = W.DWORD()
    if not P.EnumProcessModulesEx(handle, modules, C.sizeof(modules), C.byref(needed), 3):
        return None
    for module in modules[:needed.value // C.sizeof(C.c_void_p)]:
        path = C.create_unicode_buffer(2048)
        if not P.GetModuleFileNameExW(handle, module, path, len(path)):
            continue
        if Path(path.value).name.lower() != 'gc.dll':
            continue
        exports = subprocess.check_output(['llvm-readobj', '--coff-exports', path.value], text=True)
        match = re.search(r'Name: _?GC_gc_no\s+RVA: (0x[0-9A-Fa-f]+)', exports)
        assert match, 'GC_gc_no export missing'
        print('GC DLL', path.value, 'base', hex(module), 'counter RVA', match[1], flush=True)
        return module + int(match[1], 16)
    return None


def sample(handle, address):
    out = {}
    if address:
        number, read = C.c_uint32(), C.c_size_t()
        if K.ReadProcessMemory(handle, address, C.byref(number), C.sizeof(number), C.byref(read)):
            out['collections'] = number.value
    times = [W.FILETIME() for _ in range(4)]
    if K.GetProcessTimes(handle, *(C.byref(v) for v in times)):
        out['cpu_seconds'] = sum((v.dwHighDateTime << 32) | v.dwLowDateTime for v in times[2:]) / 10_000_000
    return out


root = Path('probes').resolve()
results = []
for round in range(5):
    variants = ['current', 'caller-reuse']
    if round % 2:
        variants.reverse()
    for variant in variants:
        started = time.monotonic()
        with (root / f'{variant}-{round}.log').open('w') as output:
            process = subprocess.Popen([str(root / variant / 'gc.exe')], stdout=output, stderr=output)
            handle = K.OpenProcess(0x410, False, process.pid)
            assert handle, C.get_last_error()
            address = None
            samples = []
            result = None
            while time.monotonic() - started < 90:
                result = process.poll()
                if result is not None:
                    break
                if address is None:
                    address = collection_address(handle)
                item = sample(handle, address)
                item['seconds'] = time.monotonic() - started
                samples.append(item)
                time.sleep(1)
            if result is None:
                process.kill()
                process.wait()
                result = 'timeout'
            K.CloseHandle(handle)
        item = {'round': round, 'variant': variant, 'result': result,
                'seconds': time.monotonic() - started, 'samples': samples}
        results.append(item)
        (root / 'results.json').write_text(json.dumps(results, indent=2))
        print(json.dumps({k: v for k, v in item.items() if k != 'samples'}), flush=True)
        print('SAMPLES', json.dumps(samples[::10] + samples[-1:]), flush=True)
        if result not in (0, 'timeout'):
            raise SystemExit(1)
raise SystemExit(int(any(r['result'] != 0 for r in results)))
