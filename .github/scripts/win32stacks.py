"""Snapshot instruction pointers and frame-pointer chains of our test process."""
import ctypes as C
from ctypes import wintypes as W
import json
import struct
import sys

def snapshot(pid):
    k = C.WinDLL('kernel32', use_last_error=True)
    for name, args, result in [
        ('OpenProcess', [W.DWORD, W.BOOL, W.DWORD], W.HANDLE),
        ('OpenThread', [W.DWORD, W.BOOL, W.DWORD], W.HANDLE),
        ('CreateToolhelp32Snapshot', [W.DWORD, W.DWORD], W.HANDLE),
        ('SuspendThread', [W.HANDLE], W.DWORD),
        ('ResumeThread', [W.HANDLE], W.DWORD),
        ('CloseHandle', [W.HANDLE], W.BOOL),
        ('ReadProcessMemory', [W.HANDLE, C.c_void_p, C.c_void_p, C.c_size_t, C.POINTER(C.c_size_t)], W.BOOL),
        ('Wow64GetThreadContext', [W.HANDLE, C.c_void_p], W.BOOL),
        ('GetThreadContext', [W.HANDLE, C.c_void_p], W.BOOL),
        ('IsWow64Process', [W.HANDLE, C.POINTER(W.BOOL)], W.BOOL),
    ]:
        fn = getattr(k, name)
        fn.argtypes, fn.restype = args, result
    class Thread(C.Structure):
        _fields_ = [(n, W.DWORD) for n in ('size', 'usage', 'tid', 'pid')] + [('base', W.LONG), ('delta', W.LONG), ('flags', W.DWORD)]
    class Module(C.Structure):
        _fields_ = [('size', W.DWORD), ('id', W.DWORD), ('pid', W.DWORD), ('globalUsage', W.DWORD), ('processUsage', W.DWORD), ('base', C.c_void_p), ('length', W.DWORD), ('handle', W.HMODULE), ('name', W.WCHAR*256), ('path', W.WCHAR*260)]
    for name, typ in [('Thread32First', Thread), ('Thread32Next', Thread), ('Module32FirstW', Module), ('Module32NextW', Module)]:
        getattr(k, name).argtypes = [W.HANDLE, C.POINTER(typ)]
        getattr(k, name).restype = W.BOOL
    modules = []
    ms = k.CreateToolhelp32Snapshot(0x8 | 0x10, pid)
    module = Module(); module.size = C.sizeof(module)
    ok = k.Module32FirstW(ms, C.byref(module))
    while ok:
        modules.append(dict(name=module.name, path=module.path, base=module.base, size=module.length))
        ok = k.Module32NextW(ms, C.byref(module))
    k.CloseHandle(ms)
    def identify(address):
        result = dict(address=hex(address))
        for m in modules:
            if m['base'] <= address < m['base'] + m['size']:
                result.update(module=m['name'], offset=hex(address-m['base']))
                break
        return result
    process = k.OpenProcess(0x10 | 0x400, False, pid)
    if not process:
        return dict(pid=pid, error=C.get_last_error())
    wow64 = W.BOOL()
    k.IsWow64Process(process, C.byref(wow64))
    width = 4 if wow64.value else 8
    def read(address, count):
        data = C.create_string_buffer(count)
        size = C.c_size_t()
        if not k.ReadProcessMemory(process, address, data, count, C.byref(size)) or size.value != count:
            return None
        return data.raw
    threads = []
    ts = k.CreateToolhelp32Snapshot(4, 0)
    entry = Thread(); entry.size = C.sizeof(entry)
    ok = k.Thread32First(ts, C.byref(entry))
    try:
        while ok:
            if entry.pid == pid:
                row = dict(tid=entry.tid)
                threads.append(row)
                thread = k.OpenThread(2 | 8 | 0x40, False, entry.tid)
                if thread:
                    suspended = k.SuspendThread(thread) != 0xffffffff
                    try:
                        context = C.create_string_buffer(716 if width == 4 else 1232)
                        struct.pack_into('<I', context, 0 if width == 4 else 48, 0x10001 if width == 4 else 0x100001)
                        getcontext = k.Wow64GetThreadContext if width == 4 else k.GetThreadContext
                        if suspended and getcontext(thread, context):
                            fp = struct.unpack_from('<I' if width == 4 else '<Q', context, 180 if width == 4 else 160)[0]
                            pc = struct.unpack_from('<I' if width == 4 else '<Q', context, 184 if width == 4 else 248)[0]
                            row['sp'] = hex(struct.unpack_from('<I' if width == 4 else '<Q', context, 196 if width == 4 else 152)[0])
                            frames = [identify(pc)]
                            seen = set()
                            while fp and fp not in seen and len(frames) < 80:
                                seen.add(fp)
                                data = read(fp, 2*width)
                                if data is None:
                                    break
                                next_fp, return_pc = struct.unpack('<II' if width == 4 else '<QQ', data)
                                if return_pc:
                                    frames.append(identify(return_pc))
                                if next_fp <= fp or next_fp-fp > 16*1024*1024:
                                    break
                                fp = next_fp
                            row['frames'] = frames
                        else:
                            row['error'] = C.get_last_error()
                    finally:
                        if suspended:
                            k.ResumeThread(thread)
                        k.CloseHandle(thread)
                else:
                    row['error'] = C.get_last_error()
            ok = k.Thread32Next(ts, C.byref(entry))
    finally:
        k.CloseHandle(ts)
        k.CloseHandle(process)
    return dict(pid=pid, modules=modules, threads=threads)

if __name__ == '__main__':
    print(json.dumps(snapshot(int(sys.argv[1])), indent=2))
