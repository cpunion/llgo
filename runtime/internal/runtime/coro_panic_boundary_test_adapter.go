//go:build coro_panic_boundary_adapter_test

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

	"github.com/xgo-dev/llgo/runtime/internal/coro"
)

// This named-source test island retains the exact prefix consumed by
// coro_panic_boundary.go without linking the complete LLGo runtime into the
// host Go test runtime.
type Defer struct {
	Addr unsafe.Pointer
	Bits uintptr
	Link *Defer
	Reth unsafe.Pointer
	Rund unsafe.Pointer
	Args unsafe.Pointer
}

type panicNode struct {
	prev   unsafe.Pointer
	arg    any
	defer_ *Defer
}

type g struct {
	defer_       *Defer
	panic_       unsafe.Pointer
	recoverPanic unsafe.Pointer
}

var coroPanicBoundaryTestG g

func getg() *g { return &coroPanicBoundaryTestG }

type _type struct{}

type eface struct {
	_type *_type
	data  unsafe.Pointer
}

func efaceOf(value *any) *eface { return (*eface)(unsafe.Pointer(value)) }

func promoteSignalFaultPC() {}

var coroPanicBoundaryTestSignalDepth uint32

func signalFaultBoundaryEnter() bool {
	coroPanicBoundaryTestSignalDepth++
	return true
}

func signalFaultBoundaryExit() bool {
	if coroPanicBoundaryTestSignalDepth == 0 {
		return false
	}
	coroPanicBoundaryTestSignalDepth--
	return true
}

func signalFaultBoundaryActive() bool { return coroPanicBoundaryTestSignalDepth != 0 }

var coroPanicBoundaryTestSignalAdmitted = true

func signalFaultPanicAdmitted() bool { return coroPanicBoundaryTestSignalAdmitted }

func coroPanicBoundaryCapability(*coro.G) bool { return true }

func coroRuntimeAbort(message string) { panic(message) }
