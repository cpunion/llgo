//go:build wasip1 && wasm && llgo.wasi_threads

package runtime

import _ "unsafe"

// WASI clockid_t is a pointer to an SDK object, not a POSIX integer ID.
// Select CLOCK_MONOTONIC in C so main and worker Ms use the same clock.
//
//go:linkname wasiMonotonicTime C.llgo_wasi_monotonic_time
func wasiMonotonicTime() int64

func nanotime1() int64 {
	now := wasiMonotonicTime()
	if now < 0 {
		throw("runtime: CLOCK_MONOTONIC unavailable")
	}
	return now
}
