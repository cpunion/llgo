//go:build !baremetal && !wasm && !windows

package runtime

import (
	"unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
	ct "github.com/goplus/llgo/runtime/internal/clite/time"
)

func walltime() (sec int64, nsec int32) {
	tv := (*ct.Timespec)(c.Alloca(unsafe.Sizeof(ct.Timespec{})))
	ct.ClockGettime(ct.CLOCK_REALTIME, tv)
	return int64(tv.Sec), int32(tv.Nsec)
}
