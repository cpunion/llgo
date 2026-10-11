/* Standalone Windows BDWGC progress probe; diagnostic artifact only.
 * No Windows SDK or GC headers are required to compile this translation unit.
 * Link against the same gc.dll import library as the LLGo executable.
 */
typedef __SIZE_TYPE__ size_t;
typedef __UINTPTR_TYPE__ uintptr_t;
typedef unsigned long DWORD;
typedef long LONG;
typedef void *HANDLE;
typedef struct { void *value; } SRWLOCK;
typedef struct { void *value; } CONDITION_VARIABLE;
#if defined(_WIN64)
#define WINAPI
#else
#define WINAPI __attribute__((stdcall))
#endif
#define IMPORT __declspec(dllimport)
#define EXPORT __declspec(dllexport)
IMPORT void WINAPI AcquireSRWLockExclusive(SRWLOCK *);
IMPORT void WINAPI ReleaseSRWLockExclusive(SRWLOCK *);
IMPORT int WINAPI SleepConditionVariableSRW(CONDITION_VARIABLE *, SRWLOCK *, DWORD, unsigned long);
IMPORT void WINAPI WakeConditionVariable(CONDITION_VARIABLE *);
IMPORT void WINAPI Sleep(DWORD);
IMPORT DWORD WINAPI WaitForSingleObject(HANDLE, DWORD);
IMPORT int WINAPI CloseHandle(HANDLE);
IMPORT int WINAPI TerminateProcess(HANDLE, unsigned int);
IMPORT HANDLE WINAPI GetCurrentProcess(void);
int printf(const char *, ...);
void GC_init(void);
void GC_allow_register_threads(void);
void *GC_malloc(size_t);
void *GC_malloc_uncollectable(size_t);
void GC_free(void *);
void GC_gcollect(void);
void *GC_do_blocking(void *(*)(void *), void *);
uintptr_t GC_beginthreadex(void *, unsigned int,
                         unsigned int (WINAPI *)(void *), void *,
                         unsigned int, unsigned int *);

/* Exported integer counters are sampled by the parent through ReadProcessMemory.
 * Nothing prints or polls a timer on the hot allocation/collection paths.
 */
EXPORT volatile LONG fairness_created;
EXPORT volatile LONG fairness_completed;
EXPORT volatile LONG fairness_drained;
EXPORT volatile LONG fairness_collections;
EXPORT volatile LONG fairness_error;
static volatile LONG stopped;

static LONG InterlockedIncrement(volatile LONG *p) { return __atomic_add_fetch(p, 1, __ATOMIC_SEQ_CST); }
static LONG InterlockedExchange(volatile LONG *p, LONG v) { return __atomic_exchange_n(p, v, __ATOMIC_SEQ_CST); }
static LONG InterlockedCompareExchange(volatile LONG *p, LONG v, LONG old)
{
    __atomic_compare_exchange_n(p, &old, v, 0, __ATOMIC_SEQ_CST, __ATOMIC_SEQ_CST);
    return old;
}

static void fail(LONG code)
{
    InterlockedExchange(&fairness_error, code);
    TerminateProcess(GetCurrentProcess(), (unsigned int)code);
    for (;;) { }
}

static void *alloc(size_t n, int root)
{
    void *p = root ? GC_malloc_uncollectable(n) : GC_malloc(n);
    if (!p) fail(10);
    return p;
}

typedef struct {
    SRWLOCK lock;
    CONDITION_VARIABLE available;
    CONDITION_VARIABLE space;
    unsigned count;
} channel;

typedef struct {
    CONDITION_VARIABLE *cond;
    SRWLOCK *lock;
    int ok;
} wait_call;

static void *wait_inactive(void *raw)
{
    wait_call *call = raw;
    call->ok = SleepConditionVariableSRW(call->cond, call->lock, 0xffffffffUL, 0);
    return 0;
}

static void wait(channel *ch, CONDITION_VARIABLE *condition)
{
    wait_call call = { condition, &ch->lock, 0 };
    GC_do_blocking(wait_inactive, &call);
    if (!call.ok) fail(11);
}

typedef struct payload {
    struct payload *next;
    unsigned words[24];
} payload;
typedef struct {
    channel *ch;
    payload *value;
    unsigned id;
} closure;
typedef struct {
    closure *call;
    /* Model the separately rooted runtimeContext used during thread startup. */
    unsigned char context[256];
} startup;

static void verify(closure *call)
{
    unsigned depth = 0, i;
    payload *p;
    for (p = call->value; p; p = p->next) {
        for (i = 0; i != 24; ++i)
            if (p->words[i] != (call->id * 101 + depth * 31 + i)) fail(12);
        if (++depth > 3) fail(13);
    }
    if (depth != 3) fail(14);
}

static unsigned int WINAPI worker(void *raw)
{
    startup *start = raw;
    closure *call = start->call;
    channel *ch = call->ch;
    /* An extra allocation models callback entry work, while start keeps the
     * entire closure graph rooted through the callback and its blocking wait. */
    payload *scratch = alloc(sizeof(*scratch), 0);
    scratch->words[0] = call->id;
    verify(call);
    AcquireSRWLockExclusive(&ch->lock);
    while (ch->count == 100) wait(ch, &ch->space);
    verify(call);
    if (scratch->words[0] != call->id) fail(15);
    ++ch->count;
    WakeConditionVariable(&ch->available);
    ReleaseSRWLockExclusive(&ch->lock);
    GC_free(start);
    InterlockedIncrement(&fairness_completed);
    return 0;
}

static unsigned int WINAPI collector(void *raw)
{
    int yield = (int)(uintptr_t)raw;
    while (!InterlockedCompareExchange(&stopped, 0, 0)) {
        GC_gcollect();
        InterlockedIncrement(&fairness_collections);
        if (yield) Sleep(0);
    }
    return 0;
}

static HANDLE start_thread(unsigned int (WINAPI *fn)(void *), void *arg)
{
    HANDLE handle = (HANDLE)GC_beginthreadex(0, 0, fn, arg, 0, 0);
    if (!handle) fail(16);
    return handle;
}

int main(int argc, char **argv)
{
    /* mode: 0=no concurrent collection, 1=collect+Sleep(0), 2=collect only. */
    int mode = argc > 1 ? argv[1][0] - '0' : 1;
    unsigned id, depth, i;
    channel *ch;
    HANDLE gc = 0;
    HANDLE workers[200];
    if (mode < 0 || mode > 2) return 2;
    GC_init();
    GC_allow_register_threads();
    if (mode) gc = start_thread(collector, (void *)(uintptr_t)(mode == 1));
    ch = alloc(sizeof(*ch), 1); /* GC allocations are zero-initialized. */
    for (id = 0; id != 200; ++id) {
        startup *start = alloc(sizeof(*start), 1);
        closure *call = alloc(sizeof(*call), 0);
        payload **tail = &call->value;
        start->call = call;
        call->id = id;
        call->ch = ch;
        for (depth = 0; depth != 3; ++depth) {
            payload *p = alloc(sizeof(*p), 0);
            for (i = 0; i != 24; ++i) p->words[i] = id * 101 + depth * 31 + i;
            *tail = p;
            tail = &p->next;
        }
        workers[id] = start_thread(worker, start);
        InterlockedIncrement(&fairness_created);
    }
    for (i = 0; i != 200; ++i) {
        AcquireSRWLockExclusive(&ch->lock);
        while (ch->count == 0) wait(ch, &ch->available);
        --ch->count;
        WakeConditionVariable(&ch->space);
        ReleaseSRWLockExclusive(&ch->lock);
        InterlockedIncrement(&fairness_drained);
    }
    /* Stop the collector before joining: no injected sleep or retry loop. */
    InterlockedExchange(&stopped, 1);
    if (gc) {
        if (WaitForSingleObject(gc, 0xffffffffUL) != 0) fail(17);
        CloseHandle(gc);
    }
    for (i = 0; i != 200; ++i) {
        if (WaitForSingleObject(workers[i], 0xffffffffUL) != 0) fail(18);
        CloseHandle(workers[i]);
    }
    GC_free(ch);
    printf("mode=%d created=%ld completed=%ld drained=%ld collections=%ld error=%ld\n",
           mode, fairness_created, fairness_completed, fairness_drained,
           fairness_collections, fairness_error);
    return fairness_created != 200 || fairness_completed != 200 || fairness_drained != 200;
}
