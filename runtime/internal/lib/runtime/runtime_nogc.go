//go:build nogc

package runtime

import "runtime"

// ReadMemStats reports an empty managed heap for the explicit leaking/nogc
// profile. Allocations are owned by libc malloc and are not traced or reclaimed,
// so reporting them as a Go GC heap would falsely advertise collector state.
func ReadMemStats(m *runtime.MemStats) {
	if m != nil {
		*m = runtime.MemStats{}
	}
}

func GC() {
	// The leaking/nogc profile has no tracing collector.
}
