// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	catomic "github.com/goplus/llgo/runtime/internal/clite/sync/atomic"
	_ "unsafe"
)

// These functions provide runtime/cgo.Handle without pulling in the gc
// runtime's C callback bridge. LLGo supplies its own C callback trampoline.

//llgo:managedlink
//go:linkname cgoNewHandle runtime/cgo.NewHandle
func cgoNewHandle(v any) uintptr {
	cgoHandleState.gate.Lock()
	if cgoHandleState.handles == nil {
		cgoHandleState.handles = make(map[uintptr]any)
	}
	cgoHandleState.next++
	h := cgoHandleState.next
	if h == 0 {
		cgoHandleState.gate.Unlock()
		panic("runtime/cgo: ran out of handle space")
	}
	cgoHandleState.handles[h] = v
	cgoHandleState.gate.Unlock()
	return h
}

//llgo:managedlink
//go:linkname cgoHandleValue runtime/cgo.Handle.Value
func cgoHandleValue(h uintptr) any {
	cgoHandleState.gate.Lock()
	v, ok := cgoHandleState.handles[h]
	cgoHandleState.gate.Unlock()
	if !ok {
		panic("runtime/cgo: misuse of an invalid Handle")
	}
	return v
}

//llgo:managedlink
//go:linkname cgoHandleDelete runtime/cgo.Handle.Delete
func cgoHandleDelete(h uintptr) {
	cgoHandleState.gate.Lock()
	_, ok := cgoHandleState.handles[h]
	if ok {
		delete(cgoHandleState.handles, h)
	}
	cgoHandleState.gate.Unlock()
	if !ok {
		panic("runtime/cgo: misuse of an invalid Handle")
	}
}

// cgoHandleGate keeps the runtime/cgo handle table independent of pthreads.
// Its holder may be preempted while executing a managed map operation, so a
// contender yields its stackless frame instead of blocking an executor.
type cgoHandleGate struct {
	state uint32
}

func (gate *cgoHandleGate) Lock() {
	for {
		if _, acquired := catomic.CompareAndExchange(&gate.state, uint32(0), uint32(1)); acquired {
			return
		}
		coroSchedulerYield()
	}
}

func (gate *cgoHandleGate) Unlock() {
	if _, released := catomic.CompareAndExchange(&gate.state, uint32(1), uint32(0)); !released {
		throw("runtime: invalid cgo handle gate release")
	}
}

var cgoHandleState struct {
	gate    cgoHandleGate
	next    uintptr
	handles map[uintptr]any
}
