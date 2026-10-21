/* A collector must not allocate CRT memory while another thread is stopped
 * with the Windows process heap locked. BDWGC's MinGW signal-based root-scan
 * guard can do exactly that: https://github.com/bdwgc/bdwgc/issues/924.
 *
 * Create both registered threads before locking the heap, and collect on a
 * fresh thread whose CRT signal table has not been allocated yet. */
#define GC_THREADS
#define GC_NO_THREAD_REDIRECTS
#include <gc/gc.h>
#include <windows.h>
#include <process.h>
#include <stdio.h>

static HANDLE lock_start, locked, collector_ready, collect_start, release_heap;

static unsigned __stdcall hold_heap(void *unused)
{
    (void)unused;
    WaitForSingleObject(lock_start, INFINITE);
    if (!HeapLock(GetProcessHeap()))
        TerminateProcess(GetCurrentProcess(), 3);
    SetEvent(locked);
    WaitForSingleObject(release_heap, INFINITE);
    HeapUnlock(GetProcessHeap());
    return 0;
}

static unsigned __stdcall collect(void *unused)
{
    (void)unused;
    SetEvent(collector_ready);
    WaitForSingleObject(collect_start, INFINITE);
    GC_gcollect();
    SetEvent(release_heap);
    return 0;
}

int main(void)
{
    HANDLE threads[2];
    GC_INIT();
    lock_start = CreateEventW(NULL, TRUE, FALSE, NULL);
    locked = CreateEventW(NULL, TRUE, FALSE, NULL);
    collector_ready = CreateEventW(NULL, TRUE, FALSE, NULL);
    collect_start = CreateEventW(NULL, TRUE, FALSE, NULL);
    release_heap = CreateEventW(NULL, TRUE, FALSE, NULL);
    threads[0] = (HANDLE)GC_beginthreadex(NULL, 0, hold_heap, NULL, 0, NULL);
    threads[1] = (HANDLE)GC_beginthreadex(NULL, 0, collect, NULL, 0, NULL);
    if (!threads[0] || !threads[1])
        return 4;
    if (WaitForSingleObject(collector_ready, 10000) != WAIT_OBJECT_0)
        return 5;
    SetEvent(lock_start);
    if (WaitForSingleObject(locked, 10000) != WAIT_OBJECT_0)
        TerminateProcess(GetCurrentProcess(), 5);
    SetEvent(collect_start);
    if (WaitForMultipleObjects(2, threads, TRUE, 10000) != WAIT_OBJECT_0)
        TerminateProcess(GetCurrentProcess(), 6);
    CloseHandle(threads[0]);
    CloseHandle(threads[1]);
    CloseHandle(lock_start);
    CloseHandle(locked);
    CloseHandle(collector_ready);
    CloseHandle(collect_start);
    CloseHandle(release_heap);
    puts("GC completed while another registered thread held the process heap lock");
    return 0;
}
