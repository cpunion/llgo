#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <tlhelp32.h>
#include <dbghelp.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int main(int argc, char **argv)
{
    DWORD pid;
    HANDLE process, snapshot, file;
    THREADENTRY32 entry;
    char image[4096];
    DWORD image_size = sizeof(image);
    if (argc != 4)
        return 2;
    pid = (DWORD)strtoul(argv[1], NULL, 10);
    process = OpenProcess(PROCESS_QUERY_INFORMATION | PROCESS_VM_READ, FALSE, pid);
    if (!process)
        return 3;
    if (!QueryFullProcessImageNameA(process, 0, image, &image_size)
        || _stricmp(image, argv[2]) != 0) {
        CloseHandle(process);
        return 4;
    }
    printf("owned child pid=%lu image=%s\n", (unsigned long)pid, image);
    file = CreateFileA(argv[3], GENERIC_WRITE, 0, NULL, CREATE_ALWAYS,
                       FILE_ATTRIBUTE_NORMAL, NULL);
    if (file == INVALID_HANDLE_VALUE
        || !MiniDumpWriteDump(process, pid, file,
                             (MINIDUMP_TYPE)(MiniDumpWithDataSegs
                                 | MiniDumpWithThreadInfo | MiniDumpWithFullMemoryInfo),
                             NULL, NULL, NULL)) {
        if (file != INVALID_HANDLE_VALUE)
            CloseHandle(file);
        CloseHandle(process);
        return 7;
    }
    CloseHandle(file);
    fflush(stdout);
    SymSetOptions(SYMOPT_DEFERRED_LOADS | SYMOPT_UNDNAME | SYMOPT_FAIL_CRITICAL_ERRORS);
    if (!SymInitialize(process, NULL, TRUE)) {
        CloseHandle(process);
        return 5;
    }
    {
        const char *names[] = {"diag_collect_phase", "diag_heap_phase",
                              "diag_last_event", "diag_event_count", "diag_parallel"};
        unsigned index;
        for (index = 0; index < sizeof(names) / sizeof(names[0]); ++index) {
            char storage[sizeof(SYMBOL_INFO) + 512];
            SYMBOL_INFO *symbol = (SYMBOL_INFO *)storage;
            LONG value;
            SIZE_T read;
            memset(storage, 0, sizeof(storage));
            symbol->SizeOfStruct = sizeof(SYMBOL_INFO);
            symbol->MaxNameLen = 511;
            if (SymFromName(process, names[index], symbol)
                && ReadProcessMemory(process, (void *)(ULONG_PTR)symbol->Address,
                                     &value, sizeof(value), &read)
                && read == sizeof(value))
                printf("%s=%ld\n", names[index], (long)value);
        }
        fflush(stdout);
    }
    snapshot = CreateToolhelp32Snapshot(TH32CS_SNAPTHREAD, 0);
    if (snapshot == INVALID_HANDLE_VALUE) {
        SymCleanup(process);
        CloseHandle(process);
        return 6;
    }
    entry.dwSize = sizeof(entry);
    if (Thread32First(snapshot, &entry)) {
        do {
            HANDLE thread;
            DWORD prior;
            CONTEXT context;
            STACKFRAME64 frame;
            unsigned index;
            if (entry.th32OwnerProcessID != pid)
                continue;
            thread = OpenThread(THREAD_SUSPEND_RESUME | THREAD_GET_CONTEXT
                                | THREAD_QUERY_INFORMATION, FALSE, entry.th32ThreadID);
            if (!thread)
                continue;
            prior = SuspendThread(thread);
            if (prior == (DWORD)-1) {
                CloseHandle(thread);
                continue;
            }
            memset(&context, 0, sizeof(context));
            context.ContextFlags = CONTEXT_FULL;
            if (GetThreadContext(thread, &context)) {
                printf("thread=%lu prior_suspend=%lu ip=0x%08lx sp=0x%08lx\n",
                       (unsigned long)entry.th32ThreadID, (unsigned long)prior,
                       (unsigned long)context.Eip, (unsigned long)context.Esp);
                memset(&frame, 0, sizeof(frame));
                frame.AddrPC.Offset = context.Eip;
                frame.AddrPC.Mode = AddrModeFlat;
                frame.AddrFrame.Offset = context.Ebp;
                frame.AddrFrame.Mode = AddrModeFlat;
                frame.AddrStack.Offset = context.Esp;
                frame.AddrStack.Mode = AddrModeFlat;
                for (index = 0; index < 48; ++index) {
                    char storage[sizeof(SYMBOL_INFO) + 512];
                    SYMBOL_INFO *symbol = (SYMBOL_INFO *)storage;
                    DWORD64 displacement = 0;
                    if (!StackWalk64(IMAGE_FILE_MACHINE_I386, process, thread,
                                     &frame, &context, NULL,
                                     SymFunctionTableAccess64, SymGetModuleBase64, NULL))
                        break;
                    memset(storage, 0, sizeof(storage));
                    symbol->SizeOfStruct = sizeof(SYMBOL_INFO);
                    symbol->MaxNameLen = 511;
                    if (SymFromAddr(process, frame.AddrPC.Offset, &displacement, symbol))
                        printf("  %u 0x%llx %s+0x%llx\n", index,
                               (unsigned long long)frame.AddrPC.Offset, symbol->Name,
                               (unsigned long long)displacement);
                    else
                        printf("  %u 0x%llx\n", index,
                               (unsigned long long)frame.AddrPC.Offset);
                }
            }
            /* Undo only our own increment; preserve the collector's suspension. */
            ResumeThread(thread);
            CloseHandle(thread);
        } while (Thread32Next(snapshot, &entry));
    }
    CloseHandle(snapshot);
    SymCleanup(process);
    CloseHandle(process);
    return 0;
}
