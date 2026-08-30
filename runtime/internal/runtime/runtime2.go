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

import "unsafe"

// goroutineFunc is the target-independent entry ABI between compiler-generated
// goroutine wrappers and the runtime scheduler.
//
//llgo:type C
type goroutineFunc func(unsafe.Pointer) unsafe.Pointer

// These G and P states intentionally keep the values used by the Go runtime.
// Only states reachable by the current 1:1 backend are defined here.
const (
	_Grunnable = 1
	_Grunning  = 2
	_Gdead     = 6
)

const (
	_Pidle    = 0
	_Prunning = 1
	_Pdead    = 4
)

// panicPCStore is the bounded traceback snapshot of a panic. G keeps only a
// lazy pointer because ordinary logical Gs never need this 64-PC payload. The
// legacy signal handler has a process-global, allocation-free emergency store;
// its fault snapshot was already process-global and deliberately best-effort.
type panicPCStore struct {
	n     int32
	armed int32
	fault int32
	// native is the exact leading prefix captured by a coroutine C worker.
	// The remaining PCs are compiler-maintained logical Go frames; retaining
	// this boundary avoids address-shape guesses in the terminal reporter.
	native int32
	recFP1 uintptr
	recFP2 uintptr
	pcs    [64]uintptr
}

// g holds state owned by one LLGo goroutine.
//
// The current host-thread backend gives every G its own M and P. Fields that only
// make sense once LLGo can suspend and resume a G (saved registers, wait state,
// and stack roots) belong here when those facilities are added.
type g struct {
	defer_       *Defer
	panic_       unsafe.Pointer
	panicPCs     *panicPCStore
	recoverFrame unsafe.Pointer
	recoverPanic unsafe.Pointer
	m            *m

	atomicstatus uint32
	goid         uint64
	parentGoid   uint64

	startfn goroutineFunc
	// startarg belongs to the pthread entry only until mstart consumes and
	// clears it. A stackless logical G has no such entry, so its runtime sidecar
	// phase-overlays this otherwise idle pointer with the scheduler G identity.
	startarg unsafe.Pointer

	context *coroRuntimeContext
	// localContext follows the logical G. Native pthread execution installs a
	// stack-owned context at its outer Go entry; the stackless scheduler points
	// this field at the context embedded in the task's runtime sidecar.
	localContext *LocalContext

	goexit       bool
	isMain       bool
	paniconfault bool
	// coroState reuses the original one-byte coroEmbedded field. Packing the
	// two foreign-ingress flags and its three-bit terminal status here keeps g
	// unchanged on 32-bit targets, where that original byte ended the struct
	// with no spare tail padding. Logical coroutine Gs and physical foreign-
	// thread placeholders use disjoint bits and mutate them only on their owner.
	coroState uint8
}

const (
	gCoroEmbeddedFlag uint8 = 1 << iota
	gCoroForeignIngressFlag
	gForeignThreadGCRegistrationOwnedFlag
)

const (
	gCoroTerminalStatusShift = 3
	gCoroTerminalStatusMask  = uint8(7 << gCoroTerminalStatusShift)
)

// Prove that the packed state still occupies exactly the old coroEmbedded byte
// and leaves g at its pre-feature aligned size on every target architecture.
const (
	gBeforePackedCoroStateEnd = unsafe.Offsetof(g{}.coroState) +
		unsafe.Sizeof(bool(false))
	gBeforePackedCoroStateSize = (gBeforePackedCoroStateEnd +
		unsafe.Alignof(g{}) - 1) &^ (unsafe.Alignof(g{}) - 1)
)

var (
	_ [unsafe.Sizeof(g{}.coroState) - unsafe.Sizeof(bool(false))]byte
	_ [unsafe.Sizeof(bool(false)) - unsafe.Sizeof(g{}.coroState)]byte
	_ [unsafe.Sizeof(g{}) - gBeforePackedCoroStateSize]byte
	_ [gBeforePackedCoroStateSize - unsafe.Sizeof(g{})]byte
)

// m represents the host execution resource running Go code. Platform-specific
// scheduler state is confined to mOS so it does not leak into the common core.
type m struct {
	curg *g
	p    *p
	id   int64
	os   mOS

	// signalFaultPC is the one allocation-free fault-site word captured while
	// a stackless logical G is installed on this physical executor. It is
	// promoted to the G's lazy full panic store after longjmp/recover leaves the
	// signal callback. Keeping it on M supports concurrent recoverable faults
	// without adding the 64-PC store to every coroutine allocation.
	signalFaultPC [1]uintptr
	// signalFaultState packs native boundary depth, fault presence, the
	// first-panic capture gate, and the paniconfault policy observed at the
	// interrupted instruction. One word preserves the M layout on both 32- and
	// 64-bit targets and survives synchronous defer execution until the boundary
	// stages this exact fault.
	signalFaultState uint32
}

// p represents the scheduling resources attached to an M. The host-thread backend
// currently binds one P to one M; a later M:N scheduler can retain this object
// while replacing that fixed binding with a P pool and run queues.
type p struct {
	id     int32
	status uint32
	m      *m

	// goidcache amortizes the global atomic used to assign logical G ids. A P
	// is owned by one M while running, so its current G can consume this range
	// without additional synchronization.
	goidcache    uint64
	goidcacheend uint64
}
