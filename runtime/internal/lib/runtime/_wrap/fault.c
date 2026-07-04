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
#include <signal.h>
#include <stdint.h>
#if defined(__APPLE__) || defined(__linux__)
#include <ucontext.h>
#include <errno.h>
#include <sys/mman.h>
#include <unistd.h>

static void (*llgo_fault_go)(uintptr_t pc, uintptr_t fp, int sig);
static long llgo_pagesz; /* page size, primed at handler install */

static volatile int llgo_in_fault;

static void llgo_fault_trampoline(int sig, siginfo_t *info, void *uctx)
{
    uintptr_t pc = 0, fp = 0;
    ucontext_t *uc = (ucontext_t *)uctx;
    (void)info;
    if (llgo_in_fault) {
        /* Fault while handling a fault (e.g. inside the traceback path):
         * restore the default disposition and re-raise for one clean
         * core instead of recursing. */
        signal(sig, SIG_DFL);
        raise(sig);
        return;
    }
    llgo_in_fault = 1;
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
    llgo_fault_go(pc, fp, sig);
}

/* Called from the Go side once the risky capture work is done: the fault
 * converts to an ordinary panic and later faults must be handled afresh. */
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
    llgo_in_fault = 0;
}

void llgo_install_fault_handler(void (*cb)(uintptr_t, uintptr_t, int))
{
    struct sigaction sa;
    int sigs[3] = {SIGSEGV, SIGBUS, SIGFPE};
    int i;
    llgo_fault_go = cb;
    llgo_pagesz = sysconf(_SC_PAGESIZE); /* prime out of signal context */
    for (i = 0; i < 3; i++) {
        sa.sa_sigaction = llgo_fault_trampoline;
        sigemptyset(&sa.sa_mask);
        /* SA_NODEFER: the handler converts the fault to a Go panic that
         * longjmps out through jmpbufs saved with savemask=0, so an
         * auto-blocked signal would stay blocked forever — the next
         * genuine fault would then be force-delivered with the default
         * action (core) instead of reaching us. Recursion is guarded by
         * llgo_in_fault above. */
        sa.sa_flags = SA_SIGINFO | SA_NODEFER;
        sigaction(sigs[i], &sa, 0);
    }
}
#endif

/* Probe whether one byte at p is mapped-readable, without risking a fault:
 * msync on the containing page returns ENOMEM for unmapped ranges. Used by
 * the cold traceback/snapshot walks — a frame-pointer chain that passed the
 * arithmetic guards can still point into an unmapped hole, and faulting
 * inside the fault path would recurse. */

int llgo_mem_readable(void *p)
{
    char *page;
    long sz = llgo_pagesz;
    if (sz <= 0)
        sz = 4096; /* install-time prime failed; conservative default */
    page = (char *)((uintptr_t)p & ~(uintptr_t)(sz - 1));
    if (msync(page, 1, MS_ASYNC) == 0)
        return 1;
    return errno != ENOMEM;
}
