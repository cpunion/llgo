//go:build wasm && !baremetal

package runtime

import (
	"unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	ct "github.com/xgo-dev/llgo/runtime/internal/clite/time"
)

// Emscripten and WASI use the POSIX CLOCK_MONOTONIC id. The shared clite/time
// constant retains Darwin's value for compatibility with its original users;
// on Emscripten that value selects CLOCK_MONOTONIC_COARSE instead.
const wasmClockMonotonic = ct.ClockidT(1)

func nanotime1() int64 {
	tv := (*ct.Timespec)(c.Alloca(unsafe.Sizeof(ct.Timespec{})))
	if ct.ClockGettime(wasmClockMonotonic, tv) != 0 {
		// WAMR 2.4.5 exposes CLOCK_REALTIME, but its WASI pthread workers
		// cannot read CLOCK_MONOTONIC. The main thread's monotonic clock uses
		// the same epoch there, so the realtime fallback keeps timers moving.
		if ct.ClockGettime(ct.CLOCK_REALTIME, tv) != 0 {
			return 0
		}
	}
	return int64(tv.Sec)*1e9 + int64(tv.Nsec)
}
