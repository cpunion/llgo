/* Standalone Windows ARM64/x64 unwind control; no LLGo runtime.
 * SDK-independent Win32 ABI declarations let the exact source also be
 * cross-compiled on a Unix host. All mutable shared state is protected by
 * gate_lock, except thread handles/IDs owned exclusively by main.
 */
#if !defined(_WIN64)
#error This diagnostic requires 64-bit Windows.
#endif

typedef unsigned long DWORD;
typedef int BOOL;
typedef unsigned long long SIZE_T;
typedef void *HANDLE;
typedef struct { void *state; } SRWLOCK;
typedef struct { void *state; } CONDITION_VARIABLE;
typedef DWORD (*THREAD_ENTRY)(void *);

__declspec(dllimport) HANDLE CreateEventW(void *, BOOL, BOOL, const unsigned short *);
__declspec(dllimport) BOOL SetEvent(HANDLE);
__declspec(dllimport) HANDLE CreateThread(void *, SIZE_T, THREAD_ENTRY, void *, DWORD, DWORD *);
__declspec(dllimport) DWORD WaitForSingleObject(HANDLE, DWORD);
__declspec(dllimport) DWORD WaitForMultipleObjects(DWORD, const HANDLE *, BOOL, DWORD);
__declspec(dllimport) BOOL CloseHandle(HANDLE);
__declspec(dllimport) void AcquireSRWLockExclusive(SRWLOCK *);
__declspec(dllimport) void ReleaseSRWLockExclusive(SRWLOCK *);
__declspec(dllimport) BOOL SleepConditionVariableSRW(CONDITION_VARIABLE *, SRWLOCK *, DWORD, DWORD);
__declspec(dllimport) void WakeAllConditionVariable(CONDITION_VARIABLE *);
__declspec(dllimport) __declspec(noreturn) void ExitProcess(unsigned int);

#define INFINITE_WAIT ((DWORD)0xffffffffUL)
#define WAIT_OBJECT_0 ((DWORD)0)

static SRWLOCK gate_lock = {0};
static CONDITION_VARIABLE gate_cond = {0};
static HANDLE ready_event;
static DWORD ready_workers;
static int release_workers;
static volatile DWORD control_witness;
DWORD worker_ids[2];

__declspec(noinline) void worker_wait(void)
{
    AcquireSRWLockExclusive(&gate_lock);
    ++ready_workers;
    if (ready_workers == 2 && !SetEvent(ready_event))
        ExitProcess(21);
    while (!release_workers) {
        if (!SleepConditionVariableSRW(&gate_cond, &gate_lock, INFINITE_WAIT, 0))
            ExitProcess(22);
    }
    ReleaseSRWLockExclusive(&gate_lock);
}

__declspec(noinline) DWORD worker_entry(void *unused)
{
    (void)unused;
    worker_wait();
    return 0;
}

__declspec(noinline) void control_breakpoint(void)
{
    control_witness = ready_workers;
}

int main(void)
{
    HANDLE workers[2];
    ready_event = CreateEventW((void *)0, 1, 0, (void *)0);
    if (!ready_event)
        return 10;
    for (DWORD i = 0; i < 2; ++i) {
        workers[i] = CreateThread((void *)0, 0, worker_entry, (void *)0, 0, &worker_ids[i]);
        if (!workers[i])
            ExitProcess(11);
    }
    if (WaitForSingleObject(ready_event, 10000) != WAIT_OBJECT_0)
        ExitProcess(12);

    /* The second worker signals while holding gate_lock. Taking that lock
     * guarantees it reached the condition wait's atomic unlock/wait step.
     * Both workers remain blocked until release_workers is set below.
     */
    AcquireSRWLockExclusive(&gate_lock);
    if (ready_workers != 2)
        ExitProcess(13);
    ReleaseSRWLockExclusive(&gate_lock);

    control_breakpoint();

    AcquireSRWLockExclusive(&gate_lock);
    release_workers = 1;
    WakeAllConditionVariable(&gate_cond);
    ReleaseSRWLockExclusive(&gate_lock);
    if (WaitForMultipleObjects(2, workers, 1, 10000) != WAIT_OBJECT_0)
        ExitProcess(14);
    for (DWORD i = 0; i < 2; ++i)
        if (!CloseHandle(workers[i]))
            ExitProcess(15);
    if (!CloseHandle(ready_event))
        return 16;
    return control_witness == 2 ? 0 : 17;
}
