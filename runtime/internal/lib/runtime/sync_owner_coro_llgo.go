//go:build llgo && llgo_coro

package runtime

import (
	c "github.com/goplus/llgo/runtime/internal/clite"
	catomic "github.com/goplus/llgo/runtime/internal/clite/sync/atomic"
)

// runtimeMutex protects short owner-local bookkeeping regions in the
// stackless runtime. It is intentionally not a general Go mutex: a legitimate
// caller runs on the single executor owner and never holds it across an actual
// park. Contention therefore identifies a reentrant or unaudited foreign entry
// and terminates instead of blocking the executor thread.
type runtimeMutex struct {
	state uint32
}

func runtimeMutexAbort() {
	c.Exit(2)
}

func (m *runtimeMutex) Init(_ *struct{}) int32 {
	if m == nil {
		runtimeMutexAbort()
		return -1
	}
	m.state = 0
	return 0
}

func (m *runtimeMutex) Lock() {
	if m == nil {
		runtimeMutexAbort()
		return
	}
	_, swapped := catomic.CompareAndExchange(&m.state, uint32(0), uint32(1))
	if !swapped {
		runtimeMutexAbort()
	}
}

func (m *runtimeMutex) Unlock() {
	if m == nil {
		runtimeMutexAbort()
		return
	}
	_, swapped := catomic.CompareAndExchange(&m.state, uint32(1), uint32(0))
	if !swapped {
		runtimeMutexAbort()
	}
}
