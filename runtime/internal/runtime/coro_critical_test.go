//go:build coro_critical_adapter_test

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
	goruntime "runtime"
	"testing"
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

func coroRuntimeAbort(message string) {
	panic(message)
}

type coroCriticalAdapterFixture struct {
	g          *coro.G
	header     *coro.HeaderV1
	memory     []uintptr
	descriptor *byte
}

func newCoroCriticalAdapterFixture(t *testing.T) *coroCriticalAdapterFixture {
	t.Helper()

	g := new(coro.G)
	if !coro.InitG(g) {
		t.Fatal("initialize critical adapter G")
	}
	const size = uintptr(16)
	align := unsafe.Alignof(uintptr(0))
	total, ok := coro.FrameAllocationSize(size, align)
	if !ok {
		t.Fatal("compute critical adapter frame allocation")
	}
	wordSize := unsafe.Sizeof(uintptr(0))
	memory := make([]uintptr, (total+wordSize-1)/wordSize)
	descriptor := new(byte)
	storage, ok := coro.RegisterFrame(
		g, unsafe.Pointer(&memory[0]), total, size, align, unsafe.Pointer(descriptor),
	)
	if !ok {
		t.Fatal("register critical adapter frame")
	}
	handle := unsafe.Pointer(new(byte))
	header := &coro.HeaderV1{
		G:             unsafe.Pointer(g),
		Descriptor:    unsafe.Pointer(descriptor),
		SuspendReason: uint16(coro.SuspendNone),
		Lifecycle:     uint16(coro.FrameInitialSuspended),
	}
	if !coro.PublishFrame(g, handle, header, storage) || !coro.AdoptRoot(g, handle) {
		t.Fatal("publish critical adapter root frame")
	}
	p := new(coro.P)
	action, ok := coro.BeginRunG(p, g)
	if !ok || action.Kind != coro.ActionCheckResume {
		t.Fatalf("begin critical adapter G = (%+v, %t)", action, ok)
	}
	action, ok = coro.Checked(p, g, action, false)
	if !ok || action.Kind != coro.ActionResume {
		t.Fatalf("activate critical adapter G = (%+v, %t)", action, ok)
	}
	if _, _, _, _, ok = coro.TakeRunDecision(g, coro.ParkTicket{}); !ok {
		t.Fatal("take critical adapter resume gate")
	}
	header.SuspendReason = uint16(coro.SuspendNone)
	header.Lifecycle = uint16(coro.FrameActive)
	return &coroCriticalAdapterFixture{
		g:          g,
		header:     header,
		memory:     memory,
		descriptor: descriptor,
	}
}

func expectCoroCriticalAdapterAbort(t *testing.T, call func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("critical adapter did not abort")
		}
	}()
	call()
}

func TestCoroCriticalAdapterNestedExitReturnsOutermostYield(t *testing.T) {
	fixture := newCoroCriticalAdapterFixture(t)
	__llgo_coro_critical_enter_v1(unsafe.Pointer(fixture.g))
	__llgo_coro_critical_enter_v1(unsafe.Pointer(fixture.g))
	if !coro.RequestPreempt(fixture.g) {
		t.Fatal("publish critical adapter preemption request")
	}
	if __llgo_coro_critical_exit_v1(unsafe.Pointer(fixture.g)) {
		t.Fatal("nested critical exit requested a yield")
	}
	if !__llgo_coro_critical_exit_v1(unsafe.Pointer(fixture.g)) {
		t.Fatal("outer critical exit did not return the sticky yield request")
	}
	goruntime.KeepAlive(fixture)
}

func TestCoroCriticalAdapterAbortsInvalidTransitions(t *testing.T) {
	expectCoroCriticalAdapterAbort(t, func() {
		__llgo_coro_critical_enter_v1(nil)
	})
	fixture := newCoroCriticalAdapterFixture(t)
	expectCoroCriticalAdapterAbort(t, func() {
		__llgo_coro_critical_exit_v1(unsafe.Pointer(fixture.g))
	})
	goruntime.KeepAlive(fixture)
}
