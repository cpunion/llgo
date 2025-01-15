package runtime

/*
#include <stdbool.h>
#include <stdint.h>
#include <signal.h>
#include <sys/syscall.h>
#include <unistd.h>

extern bool iscgo;

static void sigreturn__sigaction() {
	syscall(SYS_rt_sigreturn);
}

extern void sighandler(int, void*, void*, void*) __asm__("runtime.sigaction");
*/
import "C"
import (
	"github.com/goplus/llgo/runtime/internal/lib/internal/abi"
)

type sigset [2]uint32

type sigactiont struct {
	sa_handler  uintptr
	sa_flags    uint32
	sa_restorer uintptr
	sa_mask     uint64
}

//go:linkname sigfillset runtime.sigfillset
func sigfillset(mask *uint64)

//go:linkname sigaction runtime.sigaction
func sigaction(sig uint32, new, old *sigactiont)

// //go:linkname sighandler runtime.sighandler
// func sighandler(sig uint32, info *uintptr, ctxt unsafe.Pointer, gp *uintptr)

func setsig(i uint32, fn uintptr) {
	var sa sigactiont
	sa.sa_flags = C.SA_SIGINFO | C.SA_ONSTACK | C.SA_RESTART
	sigfillset(&sa.sa_mask)
	if fn == uintptr(C.sighandler) { // abi.FuncPCABIInternal(sighandler) matches the callers in signal_unix.go
		if C.iscgo {
			fn = abi.FuncPCABI0(cgoSigtramp)
		} else {
			fn = abi.FuncPCABI0(sigtramp)
		}
	}
	sa.sa_handler = fn
	sigaction(i, &sa, nil)
}
