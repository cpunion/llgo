//go:build wasip1 && wasm

package runtime

import "unsafe"

// WASI Preview 1 uses the same level-triggered poll_oneoff contract as Go's
// runtime/netpoll_wasip1.go. Bound each host wait to one millisecond, then park
// this goroutine through the timer scheduler before retrying. In particular,
// a blocked read must not prevent another goroutine from closing the descriptor
// or changing its deadline. Batched scheduler-owned subscriptions can replace
// this bounded polling without changing internal/poll's descriptor contract.

const (
	pollNoError        = 0
	pollErrClosing     = 1
	pollErrTimeout     = 2
	pollErrNotPollable = 3
)

// These are WASI wire records, not Go value layouts. Explicit padding keeps
// both the 48-byte subscription and 32-byte event correct on C32 profiles.
type wasiPollSubscription struct {
	userdata  uint64
	kind      uint64
	id        uint32
	_         uint32
	timeout   uint64
	precision uint64
	flags     uint64
}

type wasiPollEvent struct {
	userdata uint64
	errno    uint16
	kind     uint8
	_        [5]byte
	nbytes   uint64
	flags    uint16
	_        [6]byte
}

//go:wasmimport wasi_snapshot_preview1 poll_oneoff
//go:noescape
func wasiPollOneoff(in, out unsafe.Pointer, count uint32, ready *uint32) uint32

type wasiPollDesc struct {
	fd      uint32
	closing bool
	rd, wd  int64
}

// internal/poll holds an integer context, so keep the descriptor rooted until
// close. Only the single worker accesses this registry.
var wasiPollRoots map[uintptr]*wasiPollDesc

//go:linkname poll_runtime_pollServerInit internal/poll.runtime_pollServerInit
func poll_runtime_pollServerInit() {}

//go:linkname poll_runtime_pollOpen internal/poll.runtime_pollOpen
func poll_runtime_pollOpen(fd uintptr) (uintptr, int) {
	pd := &wasiPollDesc{fd: uint32(fd)}
	ctx := uintptr(unsafe.Pointer(pd))
	if wasiPollRoots == nil {
		wasiPollRoots = make(map[uintptr]*wasiPollDesc)
	}
	wasiPollRoots[ctx] = pd
	return ctx, 0
}

//go:linkname poll_runtime_pollClose internal/poll.runtime_pollClose
func poll_runtime_pollClose(ctx uintptr) {
	if pd := wasiPollRoots[ctx]; pd != nil {
		pd.closing = true
		delete(wasiPollRoots, ctx)
	}
}

//go:linkname poll_runtime_pollWait internal/poll.runtime_pollWait
func poll_runtime_pollWait(ctx uintptr, mode int) int {
	for {
		if err := poll_runtime_pollReset(ctx, mode); err != pollNoError {
			return err
		}
		pd := wasiPollRoots[ctx]
		kind := uint64(1) // fd_read
		if mode == 'w' {
			kind = 2 // fd_write
		}
		wait := int64(1e6)
		if deadline := wasiPollDeadline(pd, mode); deadline != 0 {
			if remaining := deadline - runtimeNano(); remaining < wait {
				wait = remaining
			}
		}
		if wait <= 0 {
			return pollErrTimeout
		}
		in := [2]wasiPollSubscription{
			{kind: kind, id: pd.fd},
			// A zero timeout can cancel an asynchronous host file operation
			// before it has a chance to report readiness on every retry.
			{kind: 0, id: 1, timeout: uint64(wait)},
		}
		var out [2]wasiPollEvent
		var ready uint32
		errno := wasiPollOneoff(unsafe.Pointer(&in[0]), unsafe.Pointer(&out[0]), 2, &ready)
		if errno != 0 && errno != 27 { // EINTR: yield and retry
			return pollErrNotPollable
		}
		if errno == 0 {
			for i := uint32(0); i < ready; i++ {
				if out[i].kind == 0 {
					continue
				}
				if out[i].errno != 0 {
					return pollErrNotPollable
				}
				return pollNoError
			}
		}
		timeSleep(wait)
	}
}

//go:linkname poll_runtime_pollWaitCanceled internal/poll.runtime_pollWaitCanceled
func poll_runtime_pollWaitCanceled(ctx uintptr, mode int) {
	_, _ = ctx, mode
}

//go:linkname poll_runtime_pollReset internal/poll.runtime_pollReset
func poll_runtime_pollReset(ctx uintptr, mode int) int {
	pd := wasiPollRoots[ctx]
	if pd == nil || pd.closing {
		return pollErrClosing
	}
	if deadline := wasiPollDeadline(pd, mode); deadline != 0 && deadline <= runtimeNano() {
		return pollErrTimeout
	}
	return pollNoError
}

func wasiPollDeadline(pd *wasiPollDesc, mode int) int64 {
	if mode == 'r' {
		return pd.rd
	}
	return pd.wd
}

//go:linkname poll_runtime_pollSetDeadline internal/poll.runtime_pollSetDeadline
func poll_runtime_pollSetDeadline(ctx uintptr, d int64, mode int) {
	pd := wasiPollRoots[ctx]
	if pd == nil {
		return
	}
	if d > 0 {
		now := runtimeNano()
		if d > (1<<63-1)-now {
			d = 1<<63 - 1
		} else {
			d += now
		}
	}
	// Negative durations are already expired; zero clears the deadline.
	if mode == 'r' || mode == 'r'+'w' {
		pd.rd = d
	}
	if mode == 'w' || mode == 'r'+'w' {
		pd.wd = d
	}
}

//go:linkname poll_runtime_pollUnblock internal/poll.runtime_pollUnblock
func poll_runtime_pollUnblock(ctx uintptr) {
	if pd := wasiPollRoots[ctx]; pd != nil {
		pd.closing = true
	}
}

//go:linkname poll_runtime_isPollServerDescriptor internal/poll.runtime_isPollServerDescriptor
func poll_runtime_isPollServerDescriptor(fd uintptr) bool {
	_ = fd
	return false
}
