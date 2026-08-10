//go:build !baremetal

package runtime

import "github.com/xgo-dev/llgo/runtime/internal/clite/sync/atomic"

type memProfileCounter = uint64

// Native memory-profile sampling state is per physical thread, matching gc's
// per-M sampling state and keeping recursive allocator entry local to the
// thread that is currently capturing a stack.
//
//llgo:tls
var (
	memProfileRemaining uintptr
	memProfileRandState uint64
	memProfileInSample  bool
)

func memProfileAddObject(p *memProfileCounter) {
	atomic.Add(p, memProfileCounter(1))
}

func memProfileAddN(p *memProfileCounter, n uint64) {
	atomic.Add(p, memProfileCounter(n))
}

func memProfileLoadObjects(p *memProfileCounter) memProfileCounter {
	return atomic.Load(p)
}
