"""Capture a hung Win32 regression without requiring a working live debugger."""
import ctypes as C
from ctypes import wintypes as W
import json
from pathlib import Path
import sys


class ThreadEntry(C.Structure):
    _fields_ = [(name, W.DWORD) for name in
                ('dwSize', 'cntUsage', 'th32ThreadID', 'th32OwnerProcessID')]
    _fields_ += [('tpBasePri', W.LONG), ('tpDeltaPri', W.LONG), ('dwFlags', W.DWORD)]


class FloatSave(C.Structure):
    _fields_ = [(name, W.DWORD) for name in
                ('ControlWord', 'StatusWord', 'TagWord', 'ErrorOffset',
                 'ErrorSelector', 'DataOffset', 'DataSelector')]
    _fields_ += [('RegisterArea', C.c_byte * 80), ('Cr0NpxState', W.DWORD)]


class Context32(C.Structure):
    _fields_ = [(name, W.DWORD) for name in
                ('ContextFlags', 'Dr0', 'Dr1', 'Dr2', 'Dr3', 'Dr6', 'Dr7')]
    _fields_ += [('FloatSave', FloatSave)]
    _fields_ += [(name, W.DWORD) for name in
                ('SegGs', 'SegFs', 'SegEs', 'SegDs', 'Edi', 'Esi', 'Ebx',
                 'Edx', 'Ecx', 'Eax', 'Ebp', 'Eip', 'SegCs', 'EFlags', 'Esp', 'SegSs')]
    _fields_ += [('ExtendedRegisters', C.c_byte * 512)]


def capture(pid, directory):
    directory = Path(directory)
    directory.mkdir(parents=True, exist_ok=True)
    kernel = C.WinDLL('kernel32', use_last_error=True)
    kernel.OpenProcess.argtypes = [W.DWORD, W.BOOL, W.DWORD]
    kernel.OpenProcess.restype = W.HANDLE
    kernel.OpenThread.argtypes = [W.DWORD, W.BOOL, W.DWORD]
    kernel.OpenThread.restype = W.HANDLE
    kernel.CloseHandle.argtypes = [W.HANDLE]
    kernel.CreateToolhelp32Snapshot.argtypes = [W.DWORD, W.DWORD]
    kernel.CreateToolhelp32Snapshot.restype = W.HANDLE
    kernel.Thread32First.argtypes = [W.HANDLE, C.POINTER(ThreadEntry)]
    kernel.Thread32Next.argtypes = [W.HANDLE, C.POINTER(ThreadEntry)]
    kernel.SuspendThread.argtypes = [W.HANDLE]
    kernel.ResumeThread.argtypes = [W.HANDLE]
    kernel.Wow64GetThreadContext.argtypes = [W.HANDLE, C.POINTER(Context32)]
    kernel.ReadProcessMemory.argtypes = [W.HANDLE, C.c_void_p, C.c_void_p,
                                        C.c_size_t, C.POINTER(C.c_size_t)]
    process = kernel.OpenProcess(0x0410, False, pid)
    if not process:
        raise C.WinError(C.get_last_error())
    snapshot = kernel.CreateToolhelp32Snapshot(4, 0)
    entry = ThreadEntry()
    entry.dwSize = C.sizeof(entry)
    threads = []
    more = kernel.Thread32First(snapshot, C.byref(entry))
    while more:
        if entry.th32OwnerProcessID == pid:
            thread = kernel.OpenThread(0x004A, False, entry.th32ThreadID)
            if thread:
                kernel.SuspendThread(thread)
                try:
                    context = Context32()
                    context.ContextFlags = 0x10007
                    item = dict(thread=entry.th32ThreadID)
                    if kernel.Wow64GetThreadContext(thread, C.byref(context)):
                        item['registers'] = {name: getattr(context, name) for name in
                                             ('Eip', 'Esp', 'Ebp', 'Eax', 'Ebx', 'Ecx', 'Edx', 'Esi', 'Edi')}
                        stack = (C.c_uint32 * 2048)()
                        size = C.c_size_t()
                        ok = kernel.ReadProcessMemory(process, context.Esp, stack,
                                                      C.sizeof(stack), C.byref(size))
                        item['stack'] = list(stack)[:size.value // 4]
                        item['stack_read_ok'] = bool(ok)
                    else:
                        item['error'] = C.get_last_error()
                    threads.append(item)
                    print('Captured', {k: v for k, v in item.items() if k != 'stack'}, flush=True)
                finally:
                    kernel.ResumeThread(thread)
                    kernel.CloseHandle(thread)
        more = kernel.Thread32Next(snapshot, C.byref(entry))
    kernel.CloseHandle(snapshot)
    (directory / 'threads.json').write_text(json.dumps(threads, indent=2))
    import msvcrt
    dbghelp = C.WinDLL('Dbghelp', use_last_error=True)
    dump = dbghelp.MiniDumpWriteDump
    dump.argtypes = [W.HANDLE, W.DWORD, W.HANDLE, W.DWORD,
                     C.c_void_p, C.c_void_p, C.c_void_p]
    dump.restype = W.BOOL
    with (directory / 'createtemp.dmp').open('wb') as file:
        ok = dump(process, pid, msvcrt.get_osfhandle(file.fileno()),
                  0x1002, None, None, None)
        print('MiniDumpWriteDump', bool(ok), 'error', C.get_last_error(), flush=True)
    kernel.CloseHandle(process)


if __name__ == '__main__':
    capture(int(sys.argv[1]), sys.argv[2])
