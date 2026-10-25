#define _XOPEN_SOURCE 700
#define _DARWIN_C_SOURCE 1
#if defined(__linux__) && !defined(_GNU_SOURCE)
#define _GNU_SOURCE 1
#endif
#include <dlfcn.h>
#include <errno.h>
#include <setjmp.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

/* Compile the real fault.c separately and use the current production source
 * for dynunwind-under-test.c. No resolver, unwinder or signal-handler mocks. */
#include "dynunwind-under-test.c"

void llgo_install_fault_handler(void (*)(uintptr_t, uintptr_t, uintptr_t, int, uint32_t));
void llgo_install_coro_fault_handler(void (*)(uintptr_t, uintptr_t, uintptr_t, int, uint32_t));
void llgo_fault_capture_done(void);
int llgo_cpu_profile_fault_recover(void) { return 0; }
/* This C-only fixture has no registered native worker island. The optional
 * worker landing therefore declines before the real core fault path runs. */
int __llgo_coro_worker_try_fault_v1(int signal_number, siginfo_t *info, void *context) {
    (void)signal_number; (void)info; (void)context;
    return 0;
}

static sigjmp_buf return_point;
static volatile sig_atomic_t callbacks, callback_signal, callback_policy;
static volatile uintptr_t callback_pc, callback_address;
static volatile uint64_t observed;

static void capture_callback(uintptr_t pc, uintptr_t fp, uintptr_t address,
                             int signal_number, uint32_t policy) {
    (void)fp;
    ++callbacks;
    callback_pc = pc;
    callback_address = address;
    callback_signal = signal_number;
    callback_policy = (sig_atomic_t)policy;
    llgo_fault_capture_done();
    siglongjmp(return_point, 1);
}

__attribute__((noinline)) void dynunw_contract_fault_leaf(volatile uint64_t *pointer) {
    observed = *pointer;
}
__attribute__((noinline)) void dynunw_contract_fault_middle(volatile uint64_t *pointer) {
    dynunw_contract_fault_leaf(pointer);
    observed ^= UINT64_C(0x1);
}
__attribute__((noinline)) void dynunw_contract_fault_outer(volatile uint64_t *pointer) {
    dynunw_contract_fault_middle(pointer);
    observed ^= UINT64_C(0x2);
}

static int has_frame(const char *name) {
    uintptr_t *pcs = llgo_dynunwind_pcbuf();
    for (int index = 0; index < llgo_dynunwind_pccount(); ++index) {
        Dl_info info;
        if (dladdr((void *)pcs[index], &info) && info.dli_sname &&
            strstr(info.dli_sname, name)) return 1;
    }
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 3) return 1;
    int coroutine = strcmp(argv[1], "coro") == 0;
    int disabled = strcmp(argv[2], "disabled") == 0;
    if ((!coroutine && strcmp(argv[1], "legacy") != 0) ||
        (!disabled && strcmp(argv[2], "default") != 0)) return 2;
    if (disabled) {
        if (setenv("LLGO_DYNUNWIND", "0", 1) != 0) return 3;
    } else if (unsetenv("LLGO_DYNUNWIND") != 0) return 4;
    long page_size = sysconf(_SC_PAGESIZE);
    if (page_size <= 0) return 5;
    void *page = mmap(NULL, (size_t)page_size, PROT_NONE,
                      MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (page == MAP_FAILED) return 6;
    errno = EDOM;
    if (coroutine) llgo_install_coro_fault_handler(capture_callback);
    else llgo_install_fault_handler(capture_callback);
    if (errno != EDOM) return 7;
    if (!coroutine && !disabled && !dynunw_enabled) {
        fprintf(stderr, "required local libunwind did not resolve: %s\n", llgo_dynunwind_flavor);
        return 8;
    }
    if ((coroutine || disabled) && dynunw_enabled) return 9;

    for (volatile int round = 0; round < 16; ++round) {
        if (sigsetjmp(return_point, 1) == 0) {
            dynunw_contract_fault_outer(page);
            return 10;
        }
        if (callbacks != round + 1 || !callback_pc ||
            callback_address != (uintptr_t)page || !(callback_policy & 2) ||
            (callback_signal != SIGSEGV && callback_signal != SIGBUS)) return 11;
        if (!coroutine && !disabled) {
            if (llgo_dynunwind_pccount() < 3 || llgo_dynunwind_pcbuf()[0] != callback_pc ||
                !has_frame("dynunw_contract_fault_leaf") ||
                !has_frame("dynunw_contract_fault_middle") ||
                !has_frame("dynunw_contract_fault_outer")) {
                fprintf(stderr, "fault context lost: flavor=%s count=%d leaf=%d middle=%d outer=%d\n",
                    llgo_dynunwind_flavor, llgo_dynunwind_pccount(),
                    has_frame("dynunw_contract_fault_leaf"),
                    has_frame("dynunw_contract_fault_middle"),
                    has_frame("dynunw_contract_fault_outer"));
                return 12;
            }
        } else if (llgo_dynunwind_pccount() != 0) return 13;
    }
    if (munmap(page, (size_t)page_size) != 0) return 14;
    printf("dynunwind real fault contract ok mode=%s setting=%s flavor=%s faults=%d frames=%d\n",
           argv[1], argv[2], llgo_dynunwind_flavor, callbacks, llgo_dynunwind_pccount());
    return 0;
}
