//go:build !baremetal && wasm

package runtime

import stdatomic "sync/atomic"

// Typed atomics supply the compiler-recognized align64 marker required by
// wasm32. Keep this hosted standard-library dependency off non-wasm targets.
var sched struct {
	goidgen stdatomic.Uint64
	midgen  stdatomic.Int64
	pidgen  stdatomic.Int32

	// gstate packs the live/registered goroutine count with the main-exited
	// bit. The goroutine whose release observes count zero can therefore make
	// the deadlock decision from one atomic result.
	gstate stdatomic.Uint64
}

// G and M reserve ID zero, while P IDs are zero-based like the Go runtime.
func nextGoid(gp *g) uint64 {
	return sched.goidgen.Add(1)
}

func nextMid(mp *m) int64 {
	return sched.midgen.Add(1)
}

func nextPid(pp *p) int32 {
	return sched.pidgen.Add(1) - 1
}

func retainG() {
	sched.gstate.Add(1)
}

// releaseG drops one registered context and returns the remaining count and
// main-exited state from the same atomic result.
func releaseG() (remaining uint64, mainExited bool) {
	state := sched.gstate.Add(^uint64(0))
	return state & gCountMask, state&mainExitedBit != 0
}

func markMainExited() {
	// Uint64.Or is unavailable in Go 1.20-1.22. Preserve the packed-state
	// update atomically without raising the supported GOROOT requirement.
	for {
		state := sched.gstate.Load()
		if sched.gstate.CompareAndSwap(state, state|mainExitedBit) {
			return
		}
	}
}

func gState() (count uint64, mainExited bool) {
	state := sched.gstate.Load()
	return state & gCountMask, state&mainExitedBit != 0
}
