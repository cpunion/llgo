/* Fault handler with fault-site context: see the comment on
 * llgo_install_fault_handler below. Separate file because darwin's
 * ucontext.h demands _XOPEN_SOURCE before any system header. */
#define _XOPEN_SOURCE 700
#define _DARWIN_C_SOURCE 1
#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE
#endif

/* Fault handler with fault-site context. The plain signal() handler the
 * runtime core installs cannot see the interrupted registers, and walking
 * from the handler's own frame stops at the signal trampoline — so the
 * panic pc snapshot for hardware faults (nil derefs compiled with
 * null_pointer_is_valid, faults inside C code, integer division on x86)
 * starts from the ucontext's pc/fp instead. */
#include <errno.h>
#include <signal.h>
#include <stdint.h>
#include <sys/mman.h>
#include <unistd.h>

static long llgo_pagesz; /* primed at handler install, out of signal context */

#if defined(__APPLE__) || defined(__linux__)
#include <ucontext.h>

static void (*llgo_fault_go)(uintptr_t pc, uintptr_t fp, uintptr_t addr,
                             int sig, uint32_t policy);

enum {
    LLGO_FAULT_PANIC_DEFAULT = 1u << 0,
    LLGO_FAULT_MEMORY = 1u << 1
};

/* profile.c arms this recovery point only while dereferencing a sampled
 * frame-pointer chain. A bad frame truncates that sample instead of entering
 * the Go fault path while the profiler ring lock is held. */
extern int llgo_cpu_profile_fault_recover(void);

/* Dynamic-libunwind fault unwinding (dynunwind.c); no-ops when disabled
 * (LLGO_DYNUNWIND=0) or no libunwind flavor resolved. */
extern void llgo_dynunwind_init(void);
extern void llgo_dynunwind_capture(void *uctx);

/* The legacy Go adapter publishes one process-global unwind snapshot and must
 * therefore preserve its original process-wide recursion/fault exclusion.
 * The coroutine adapter keeps its exact snapshot on M and can admit one fault
 * per physical executor, so its recursion guard is thread-local. */
static volatile sig_atomic_t llgo_legacy_in_fault;
static _Thread_local volatile sig_atomic_t llgo_coro_in_fault;
static volatile sig_atomic_t llgo_coro_fault_mode;

static inline volatile sig_atomic_t *llgo_fault_guard(void)
{
    return llgo_coro_fault_mode ? &llgo_coro_in_fault : &llgo_legacy_in_fault;
}

static int llgo_fault_is_async(siginfo_t *info)
{
    int code;
    if (info == 0)
        return 0;
    code = info->si_code;
    if (code == SI_USER)
        return 1;
#if defined(SI_QUEUE)
    if (code == SI_QUEUE)
        return 1;
#endif
#if defined(SI_TIMER)
    if (code == SI_TIMER)
        return 1;
#endif
#if defined(SI_ASYNCIO)
    if (code == SI_ASYNCIO)
        return 1;
#endif
#if defined(SI_MESGQ)
    if (code == SI_MESGQ)
        return 1;
#endif
#if defined(SI_TKILL)
    if (code == SI_TKILL)
        return 1;
#endif
    return 0;
}

/* Collapse platform si_code details into the two facts consumed by the Go
 * runtime. User-generated SIGSEGV/SIGBUS/SIGFPE remains a process signal;
 * synchronous FPE and a kernel-certified nil-page memory fault panic by
 * default, while other memory faults require SetPanicOnFault. */
static uint32_t llgo_fault_policy(int sig, siginfo_t *info, uintptr_t addr)
{
    int code = info != 0 ? info->si_code : 0;
    uint32_t policy = 0;
    if (llgo_fault_is_async(info))
        return 0;
    if (sig == SIGFPE)
        return LLGO_FAULT_PANIC_DEFAULT;
    if (sig != SIGSEGV && sig != SIGBUS)
        return 0;
    policy = LLGO_FAULT_MEMORY;
    if (addr >= 0x1000)
        return policy;
    if (info == 0)
        return policy | LLGO_FAULT_PANIC_DEFAULT;
    if (sig == SIGSEGV &&
        (code == SEGV_MAPERR || code == SEGV_ACCERR))
        return policy | LLGO_FAULT_PANIC_DEFAULT;
    if (sig == SIGBUS && code == BUS_ADRERR)
        return policy | LLGO_FAULT_PANIC_DEFAULT;
    return policy;
}

static void llgo_fault_trampoline(int sig, siginfo_t *info, void *uctx)
{
    uintptr_t pc = 0, fp = 0;
    uintptr_t addr = info != 0 ? (uintptr_t)info->si_addr : 0;
    uint32_t policy = llgo_fault_policy(sig, info, addr);
    ucontext_t *uc = (ucontext_t *)uctx;
    volatile sig_atomic_t *guard = llgo_fault_guard();
    if (llgo_cpu_profile_fault_recover())
        return;
    if (*guard) {
        signal(sig, SIG_DFL);
        raise(sig);
        return;
    }
    *guard = 1;
#if defined(__APPLE__) && defined(__aarch64__)
    pc = (uintptr_t)uc->uc_mcontext->__ss.__pc;
    fp = (uintptr_t)uc->uc_mcontext->__ss.__fp;
#elif defined(__APPLE__) && defined(__x86_64__)
    pc = (uintptr_t)uc->uc_mcontext->__ss.__rip;
    fp = (uintptr_t)uc->uc_mcontext->__ss.__rbp;
#elif defined(__linux__) && defined(__aarch64__)
    pc = (uintptr_t)uc->uc_mcontext.pc;
    fp = (uintptr_t)uc->uc_mcontext.regs[29];
#elif defined(__linux__) && defined(__x86_64__)
    pc = (uintptr_t)uc->uc_mcontext.gregs[16 /* REG_RIP */];
    fp = (uintptr_t)uc->uc_mcontext.gregs[10 /* REG_RBP */];
#endif
    if (!llgo_coro_fault_mode)
        llgo_dynunwind_capture(uctx);
    llgo_fault_go(pc, fp, addr, sig, policy);
    /* A recoverable callback leaves through siglongjmp and never returns.
     * Returning means the coroutine policy rejected this signal (or a runtime
     * invariant failed), so preserve native fatal-signal behavior without
     * running Go defers or making the fault recoverable. */
    signal(sig, SIG_DFL);
    raise(sig);
    _exit(2);
}

void llgo_fault_capture_done(void)
{
    /* Belt and braces alongside SA_NODEFER: make sure no fault signal
     * stays blocked once this fault becomes an ordinary panic. */
    sigset_t set;
    sigemptyset(&set);
    sigaddset(&set, SIGSEGV);
    sigaddset(&set, SIGBUS);
    sigaddset(&set, SIGFPE);
    sigprocmask(SIG_UNBLOCK, &set, 0);
    *llgo_fault_guard() = 0;
}

int llgo_mem_readable(void *p);

static void llgo_install_fault_handler_mode(
    void (*cb)(uintptr_t, uintptr_t, uintptr_t, int, uint32_t), int coro_mode)
{
    struct sigaction sa;
    int sigs[3] = {SIGSEGV, SIGBUS, SIGFPE};
    int i;
    /* Installation runs at startup, before user code. dlopen probes for
     * libunwind (and sysconf/sigaction) may set errno; a leaked errno is
     * visible to the first two-result cgo call ("v, err := C.f()"), which
     * turns it into a spurious non-nil err. */
    int saved_errno = errno;
    llgo_fault_go = cb;
    llgo_coro_fault_mode = coro_mode != 0;
    llgo_pagesz = sysconf(_SC_PAGESIZE);
    if (!llgo_coro_fault_mode)
        llgo_dynunwind_init();
    for (i = 0; i < 3; i++) {
        sa.sa_sigaction = llgo_fault_trampoline;
        sigemptyset(&sa.sa_mask);
        /* SA_NODEFER: the handler converts the fault to a Go panic that
         * longjmps out through jmpbufs saved with savemask=0, so an
         * auto-blocked signal would stay blocked forever — the next
         * genuine fault would then be force-delivered with the default
         * action (core) instead of reaching us. Recursion is guarded by
         * the mode-specific guard above. */
        sa.sa_flags = SA_SIGINFO | SA_NODEFER;
        sigaction(sigs[i], &sa, 0);
    }
    errno = saved_errno;
}

void llgo_install_fault_handler(
    void (*cb)(uintptr_t, uintptr_t, uintptr_t, int, uint32_t))
{
    llgo_install_fault_handler_mode(cb, 0);
}

void llgo_install_coro_fault_handler(
    void (*cb)(uintptr_t, uintptr_t, uintptr_t, int, uint32_t))
{
    llgo_install_fault_handler_mode(cb, 1);
}
#endif

/* Probe whether one byte at p is mapped-readable: msync on the containing
 * page returns ENOMEM for unmapped ranges. Used by the fault-context
 * walks — an arithmetic-valid frame pointer can still point into a hole,
 * and faulting inside the fault path recurses. */
int llgo_mem_readable(void *p)
{
    int saved_errno = errno;
    char *page;
    long sz = llgo_pagesz;
    if (sz <= 0)
        sz = 4096;
    page = (char *)((uintptr_t)p & ~(uintptr_t)(sz - 1));
    if (msync(page, 1, MS_ASYNC) == 0)
        { int r = 1; errno = saved_errno; return r; }
    { int r = errno != ENOMEM; errno = saved_errno; return r; }
}
