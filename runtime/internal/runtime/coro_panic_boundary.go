//go:build coro_panic_boundary_adapter_test || (llgo && llgo_coro && !baremetal && !wasm && !tinygo.wasm && (darwin || linux))

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package runtime

import (
	"unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	"github.com/xgo-dev/llgo/runtime/internal/coro"
)

// coroPanicBoundaryPush installs one synthetic legacy defer frame around an
// exact physical coroutine resume. boundary and env have native activation
// lifetime and must not survive a coroutine suspension.
func coroPanicBoundaryPush(task *coro.G, handle unsafe.Pointer, boundary *Defer, env unsafe.Pointer) bool {
	if task == nil || handle == nil || boundary == nil || env == nil ||
		*boundary != (Defer{}) || !coro.ResumePanicBoundaryActive(task, handle) {
		return false
	}
	gp := getg()
	if gp == nil {
		return false
	}
	*boundary = Defer{
		Addr: env,
		Link: gp.defer_,
		Reth: handle,
		Rund: unsafe.Pointer(task),
	}
	gp.defer_ = boundary
	if !signalFaultBoundaryEnter() {
		gp.defer_ = boundary.Link
		*boundary = Defer{}
		return false
	}
	return true
}

func validCoroPanicBoundary(task *coro.G, handle unsafe.Pointer, boundary *Defer, gp *g) bool {
	return task != nil && handle != nil && boundary != nil && gp != nil &&
		gp.defer_ == boundary && boundary.Addr != nil && boundary.Bits == 0 &&
		boundary.Reth == handle && boundary.Rund == unsafe.Pointer(task) && boundary.Args == nil &&
		signalFaultBoundaryActive()
}

// coroPanicBoundaryPop closes a normally returned physical resume. A panic
// which still belongs to this boundary must take the nonlocal stage path.
const (
	coroPanicBoundaryPopOK uint32 = iota
	coroPanicBoundaryPopInvalidRecord
	coroPanicBoundaryPopInvalidResume
	coroPanicBoundaryPopActivePanic
)

func coroPanicBoundaryPopStatus(task *coro.G, handle unsafe.Pointer, boundary *Defer) uint32 {
	gp := getg()
	if !validCoroPanicBoundary(task, handle, boundary, gp) {
		return coroPanicBoundaryPopInvalidRecord
	}
	if !coro.ResumePanicBoundaryReturning(task, handle) {
		return coroPanicBoundaryPopInvalidResume
	}
	if ptr := gp.panic_; ptr != nil && (*panicNode)(ptr).defer_ == boundary {
		return coroPanicBoundaryPopActivePanic
	}
	if !signalFaultBoundaryExit() {
		return coroPanicBoundaryPopInvalidRecord
	}
	gp.defer_ = boundary.Link
	*boundary = Defer{}
	return coroPanicBoundaryPopOK
}

func coroPanicBoundaryPop(task *coro.G, handle unsafe.Pointer, boundary *Defer) bool {
	return coroPanicBoundaryPopStatus(task, handle, boundary) == coroPanicBoundaryPopOK
}

// coroPanicBoundaryStage owns the siglongjmp return. Rethrow has already moved
// the top panic node and the legacy unwind cursor to boundary. Replace that
// temporary stack address with the exact coroutine handle, pop the boundary,
// and leave the node rooted until the compiler resume landing consumes it.
func coroPanicBoundaryStage(task *coro.G, handle unsafe.Pointer, boundary *Defer) bool {
	gp := getg()
	if !validCoroPanicBoundary(task, handle, boundary, gp) ||
		!signalFaultPanicAdmitted() ||
		!coro.ResumePanicBoundaryMayLand(task, handle) || gp.panic_ == nil {
		return false
	}
	node := (*panicNode)(gp.panic_)
	if node.defer_ != boundary || gp.recoverPanic == unsafe.Pointer(node) {
		return false
	}
	promoteSignalFaultPC()
	if !signalFaultBoundaryExit() {
		return false
	}
	gp.defer_ = boundary.Link
	node.defer_ = (*Defer)(handle)
	*boundary = Defer{}
	return true
}

// __llgo_coro_panic_boundary_push_v1 is the compiler inline-resume adapter.
//
//export __llgo_coro_panic_boundary_push_v1
func __llgo_coro_panic_boundary_push_v1(g, handle, boundary, env unsafe.Pointer) {
	if !coroPanicBoundaryPush((*coro.G)(g), handle, (*Defer)(boundary), env) {
		coroRuntimeAbort("invalid coroutine panic boundary push")
	}
}

//export __llgo_coro_panic_boundary_pop_v1
func __llgo_coro_panic_boundary_pop_v1(g, handle, boundary unsafe.Pointer) {
	switch coroPanicBoundaryPopStatus((*coro.G)(g), handle, (*Defer)(boundary)) {
	case coroPanicBoundaryPopOK:
		return
	case coroPanicBoundaryPopInvalidRecord:
		coroRuntimeAbort("invalid coroutine panic boundary pop record")
	case coroPanicBoundaryPopInvalidResume:
		coroRuntimeAbort("invalid coroutine panic boundary pop resume state")
	case coroPanicBoundaryPopActivePanic:
		coroRuntimeAbort("invalid coroutine panic boundary pop with active panic")
	default:
		coroRuntimeAbort("invalid coroutine panic boundary pop status")
	}
}

//export __llgo_coro_panic_boundary_stage_v1
func __llgo_coro_panic_boundary_stage_v1(g, handle, boundary unsafe.Pointer) {
	if !coroPanicBoundaryStage((*coro.G)(g), handle, (*Defer)(boundary)) {
		coroRuntimeAbort("invalid coroutine panic boundary landing")
	}
}

//export __llgo_coro_panic_boundary_enabled_v1
func __llgo_coro_panic_boundary_enabled_v1(g unsafe.Pointer) bool {
	return coroPanicBoundaryCapability((*coro.G)(g))
}

// __llgo_coro_panic_boundary_take_v1 is the first compiler-owned resume gate.
// Nil is the ordinary fast path. A non-nil token is detached from the legacy
// panic stack exactly once and remains valid until data_release consumes it.
//
//export __llgo_coro_panic_boundary_take_v1
func __llgo_coro_panic_boundary_take_v1(g, handle unsafe.Pointer) unsafe.Pointer {
	task := (*coro.G)(g)
	if task == nil || handle == nil || !coroPanicBoundaryCapability(task) ||
		!coro.ResumePanicBoundaryMayLand(task, handle) {
		return nil
	}
	gp := getg()
	if gp == nil || gp.panic_ == nil {
		return nil
	}
	node := (*panicNode)(gp.panic_)
	if node.defer_ != (*Defer)(handle) {
		return nil
	}
	gp.panic_ = node.prev
	node.prev = nil
	node.defer_ = (*Defer)(unsafe.Pointer(node))
	return unsafe.Pointer(node)
}

func coroDetachedBoundaryPanic(token unsafe.Pointer) *panicNode {
	if token == nil {
		return nil
	}
	node := (*panicNode)(token)
	if node.prev != nil || node.defer_ != (*Defer)(token) {
		return nil
	}
	return node
}

//export __llgo_coro_panic_boundary_type_v1
func __llgo_coro_panic_boundary_type_v1(token unsafe.Pointer) unsafe.Pointer {
	node := coroDetachedBoundaryPanic(token)
	if node == nil {
		coroRuntimeAbort("invalid coroutine panic boundary type token")
	}
	payload := efaceOf(&node.arg)
	if payload._type == nil {
		coroRuntimeAbort("coroutine panic boundary retained an untyped nil panic")
	}
	return unsafe.Pointer(payload._type)
}

// __llgo_coro_panic_boundary_data_release_v1 returns the second interface word
// and releases only the transport node. The dynamic payload is already rooted
// by the structured cleanup/frame or scheduler publication which immediately
// consumes the returned pair.
//
//export __llgo_coro_panic_boundary_data_release_v1
func __llgo_coro_panic_boundary_data_release_v1(token unsafe.Pointer) unsafe.Pointer {
	node := coroDetachedBoundaryPanic(token)
	if node == nil {
		coroRuntimeAbort("invalid coroutine panic boundary data token")
	}
	payload := efaceOf(&node.arg)
	if payload._type == nil {
		coroRuntimeAbort("coroutine panic boundary retained an untyped nil panic")
	}
	data := payload.data
	node.arg = nil
	node.defer_ = nil
	c.Free(token)
	return data
}
