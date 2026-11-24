// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license.
// See LICENSES/Go-BSD-3-Clause.txt at this module root for license terms.

//go:build llgo && llgo_coro && go1.27 && !go1.28 && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal) || llgo_coro_host)

//llgo:require-source waitgroup.go 274e43a7cbc76eb0da4635a2680785200dfaf94d85e2d55cdd5122c76a4791d9

package sync

import (
	"internal/race"
	"internal/synctest"
	"unsafe"
)

//go:linkname llgoWaitGroupReadyPeer github.com/xgo-dev/llgo/runtime/internal/runtime.CoroSemaphoreReadyPeerV1
func llgoWaitGroupReadyPeer() bool

//go:linkname llgoWaitGroupYield llgo.coroYield
func llgoWaitGroupYield()

func (wg *WaitGroup) llgoTryWaitFast() uint8 {
	if race.Enabled {
		return 2
	}
	state := wg.state.Load()
	if state == 0 {
		return 0
	}
	v := int32(state >> 32)
	if v > 0 && state&0xffffffff == 0 && llgoWaitGroupReadyPeer() {
		return 1
	}
	return 2
}

func (wg *WaitGroup) llgoTryWaitDone() bool {
	return wg.state.Load() == 0
}

func (wg *WaitGroup) Wait() {
	switch wg.llgoTryWaitFast() {
	case 0:
		return
	case 1:
		llgoWaitGroupYield()
		if wg.llgoTryWaitDone() {
			return
		}
		wg.llgoWait(false)
	default:
		wg.llgoWait(race.Enabled)
	}
}

// llgoWait retains the complete waiting protocol. A compiler-proved direct
// Wait call may consume its one scheduling opportunity in the caller's frame
// and pass false here if a real semaphore wait is still necessary.
func (wg *WaitGroup) llgoWait(opportunity bool) {
	if race.Enabled {
		race.Disable()
	}
	for {
		state := wg.state.Load()
		v := int32(state >> 32)
		w := uint32(state & 0x7fffffff)
		if v == 0 {
			// Counter is 0, no need to wait.
			if race.Enabled {
				race.Enable()
				race.Acquire(unsafe.Pointer(wg))
			}
			if w == 0 && state&waitGroupBubbleFlag != 0 && synctest.IsAssociated(wg) {
				// Adds must not happen concurrently with wait when counter is 0,
				// so we can disassociate wg from its current bubble.
				if wg.state.CompareAndSwap(state, 0) {
					synctest.Disassociate(wg)
				}
			}
			return
		}
		// One local scheduling opportunity precedes waiter publication. The
		// normal yield validates the transition; the complete original loop
		// handles contention, cancellation, bubbles and all later waits.
		if opportunity {
			opportunity = false
			if v > 0 && w == 0 && state&waitGroupBubbleFlag == 0 && llgoWaitGroupReadyPeer() {
				llgoWaitGroupYield()
				continue
			}
		}
		// Increment waiters count.
		if wg.state.CompareAndSwap(state, state+1) {
			if race.Enabled && w == 0 {
				// Wait must be synchronized with the first Add.
				// Need to model this is as a write to race with the read in Add.
				// As a consequence, can do the write only for the first waiter,
				// otherwise concurrent Waits will race with each other.
				race.Write(unsafe.Pointer(&wg.sema))
			}
			synctestDurable := false
			if state&waitGroupBubbleFlag != 0 && synctest.IsInBubble() {
				if race.Enabled {
					race.Enable()
				}
				if synctest.IsAssociated(wg) {
					// Add was called within the current bubble,
					// so this Wait is durably blocking.
					synctestDurable = true
				}
				if race.Enabled {
					race.Disable()
				}
			}
			runtime_SemacquireWaitGroup(&wg.sema, synctestDurable)
			isReset := wg.state.Load() != 0
			if race.Enabled {
				race.Enable()
				race.Acquire(unsafe.Pointer(wg))
			}
			if isReset {
				panic("sync: WaitGroup is reused before previous Wait has returned")
			}
			return
		}
	}
}
