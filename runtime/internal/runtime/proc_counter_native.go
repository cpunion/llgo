//go:build !baremetal && !wasm

package runtime

import "github.com/xgo-dev/llgo/runtime/internal/sync/atomic"

// Preserve native counter layout and intrinsic-only dependencies. In
// particular, freestanding targets must not import a hosted Go runtime just
// to declare otherwise unused scheduler counters.
var sched struct {
	goidgen uint64
	midgen  int64
	pidgen  int32
	gstate  uint64
}

// LLGo's atomic.Add returns the value before the addition. G and M reserve ID
// zero, while P IDs are zero-based like the Go runtime.
func nextGoid(gp *g) uint64 {
	return atomic.Add(&sched.goidgen, uint64(1)) + 1
}

func nextMid(mp *m) int64 {
	return atomic.Add(&sched.midgen, int64(1)) + 1
}

func nextPid(pp *p) int32 {
	return atomic.Add(&sched.pidgen, int32(1))
}

func retainG() {
	atomic.Add(&sched.gstate, uint64(1))
}

// releaseG drops one registered context and returns the remaining count and
// main-exited state from the same atomic result.
func releaseG() (remaining uint64, mainExited bool) {
	// llgo.atomicSub follows LLVM atomicrmw and returns the value before the
	// subtraction, unlike sync/atomic.Add. Convert it to the post-release
	// state before deciding whether this was the last context.
	state := atomic.Sub(&sched.gstate, uint64(1)) - 1
	return state & gCountMask, state&mainExitedBit != 0
}

func markMainExited() {
	atomic.Or(&sched.gstate, mainExitedBit)
}

func gState() (count uint64, mainExited bool) {
	state := atomic.Load(&sched.gstate)
	return state & gCountMask, state&mainExitedBit != 0
}
