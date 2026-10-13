//go:build !windows && !wasm && !baremetal

package thread

import (
	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	csyscall "github.com/xgo-dev/llgo/runtime/internal/clite/syscall"
)

func createWithRetry(native *nativeThread, attr *nativeAttr, routine RoutineFunc, arg c.Pointer) c.Int {
	ret := create(native, attr, routine, arg)
	// Detached threads can briefly exhaust host resources while completed
	// threads are still being reclaimed. Use Go's bounded EAGAIN backoff.
	for retry := 0; ret == c.Int(csyscall.EAGAIN) && retry < 20; retry++ {
		c.Usleep(c.Uint(retry+1) * 1000)
		ret = create(native, attr, routine, arg)
	}
	return ret
}
