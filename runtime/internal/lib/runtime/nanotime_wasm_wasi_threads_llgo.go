//go:build wasip1 && wasm && llgo.wasi_threads

package runtime

import (
	"sync/atomic"
	"unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	ct "github.com/xgo-dev/llgo/runtime/internal/clite/time"
)

var wasiThreadNanoLast int64

func nanotime1() int64 {
	tv := (*ct.Timespec)(c.Alloca(unsafe.Sizeof(ct.Timespec{})))
	// WAMR 2.4.5 exposes CLOCK_REALTIME on pthread workers but cannot read
	// CLOCK_MONOTONIC there. Use one clock domain on every M so deadlines
	// created on one thread can be consumed by the timer thread.
	if ct.ClockGettime(ct.CLOCK_REALTIME, tv) != 0 {
		return atomic.LoadInt64(&wasiThreadNanoLast)
	}
	now := int64(tv.Sec)*1e9 + int64(tv.Nsec)
	for {
		last := atomic.LoadInt64(&wasiThreadNanoLast)
		if now <= last {
			return last
		}
		if atomic.CompareAndSwapInt64(&wasiThreadNanoLast, last, now) {
			return now
		}
	}
}
