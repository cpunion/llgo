//go:build !baremetal

package runtime

import "github.com/xgo-dev/llgo/runtime/internal/clite/sync/atomic"

type memProfileCounter = uint64

// Keep the hot-path fields in one TLS object so one address lookup serves the
// whole allocation decision. The recursion guard remains local to the thread
// that is currently capturing a stack.
//
//llgo:tls
var memProfileState memProfileThreadState

func memProfileAddObject(p *memProfileCounter) {
	atomic.Add(p, memProfileCounter(1))
}

func memProfileAddN(p *memProfileCounter, n uint64) {
	atomic.Add(p, memProfileCounter(n))
}

func memProfileLoadObjects(p *memProfileCounter) memProfileCounter {
	return atomic.Load(p)
}
