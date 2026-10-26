//go:build llgo && (darwin || linux) && !baremetal

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package llgoext

import (
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// Ordinary go statements exercise stackless tasks. Call the native entry ABI
// explicitly so changes to the native-only startup storage are covered too.
//
//llgo:type C
type nativeStartEntry func(unsafe.Pointer) unsafe.Pointer

//go:linkname newNativeProcForTest github.com/xgo-dev/llgo/runtime/internal/runtime.NewProc
func newNativeProcForTest(nativeStartEntry, unsafe.Pointer, uintptr)

//go:linkname allocNativeStartRootForTest github.com/xgo-dev/llgo/runtime/internal/runtime.AllocRoot
func allocNativeStartRootForTest(uintptr) unsafe.Pointer

type nativeStartResult struct {
	ready   uint32
	release uint32
	done    uint32
	valid   bool
	state   runtimeGMPState
}

type nativeStartPayload struct {
	words [128]uint64
}

type nativeStartRecord struct {
	result  *nativeStartResult
	payload *nativeStartPayload
}

func nativeStartCallback(raw unsafe.Pointer) unsafe.Pointer {
	record := (*nativeStartRecord)(raw)
	result := record.result
	result.state = currentRuntimeGMPState()
	atomic.StoreUint32(&result.ready, 1)
	for atomic.LoadUint32(&result.release) == 0 {
		// This is a native C entry, so it must not suspend a coroutine.
	}
	valid := true
	for index, word := range record.payload.words {
		if word != uint64(index)*19+7 {
			valid = false
		}
	}
	result.valid = valid
	atomic.StoreUint32(&result.done, 1)
	return nil
}

//go:noinline
func startNativeRecord(result *nativeStartResult) {
	// Ownership of the scanned root transfers to mstart/mexit. Only the result
	// stays in the parent's graph while GC runs with the native entry parked.
	raw := allocNativeStartRootForTest(unsafe.Sizeof(nativeStartRecord{}))
	if raw == nil {
		panic("native startup root allocation failed")
	}
	record := (*nativeStartRecord)(raw)
	record.result = result
	record.payload = new(nativeStartPayload)
	for index := range record.payload.words {
		record.payload.words[index] = uint64(index)*19 + 7
	}
	newNativeProcForTest(nativeStartEntry(nativeStartCallback), raw, 0)
}

func TestRuntimeNativeStartStorageAndLifetime(t *testing.T) {
	// The acceptance harness runs this test alone in a fresh process. A
	// global count cannot distinguish this native child's retirement from an
	// unrelated test goroutine finishing concurrently.
	if os.Getenv("LLGO_NATIVE_START_ACCEPTANCE_CHILD") != "1" {
		t.Skip("run through TestCoroContextLayoutAcceptance for isolated native lifetime accounting")
	}
	parent := currentRuntimeGMPState()
	checkRunningRuntimeGMP(t, parent)
	baseline := runtime.NumGoroutine()
	for iteration := 0; iteration < 4; iteration++ {
		result := new(nativeStartResult)
		// Also release the native entry if a test assertion fails.
		defer atomic.StoreUint32(&result.release, 1)
		startNativeRecord(result)
		deadline := time.Now().Add(5 * time.Second)
		for atomic.LoadUint32(&result.ready) == 0 && time.Now().Before(deadline) {
			runtime.Gosched()
		}
		if atomic.LoadUint32(&result.ready) == 0 {
			t.Fatal("native entry did not start")
		}
		checkRunningRuntimeGMP(t, result.state)
		if result.state.parentGoid != parent.goid || result.state.goid == parent.goid ||
			result.state.mid == parent.mid || runtime.NumGoroutine() != baseline+1 {
			t.Fatalf("native child ownership: parent=%+v child=%+v count=%d baseline=%d",
				parent, result.state, runtime.NumGoroutine(), baseline)
		}
		runtime.GC()
		atomic.StoreUint32(&result.release, 1)
		deadline = time.Now().Add(5 * time.Second)
		for (atomic.LoadUint32(&result.done) == 0 || runtime.NumGoroutine() != baseline) && time.Now().Before(deadline) {
			runtime.Gosched()
		}
		if atomic.LoadUint32(&result.done) == 0 {
			t.Fatal("native entry did not return")
		}
		if !result.valid || runtime.NumGoroutine() != baseline {
			t.Fatalf("native entry did not retain its payload and retire: payload=%t count=%d baseline=%d",
				result.valid, runtime.NumGoroutine(), baseline)
		}
	}
}

func TestRuntimeNativeStartRejectsNilEntry(t *testing.T) {
	if os.Getenv("LLGO_NATIVE_START_ACCEPTANCE_CHILD") != "1" {
		t.Skip("run through TestCoroContextLayoutAcceptance for isolated native lifetime accounting")
	}
	baseline := runtime.NumGoroutine()
	defer func() {
		if got := recover(); got != "go of nil func value" {
			t.Fatalf("nil native entry panic = %v", got)
		}
		if runtime.NumGoroutine() != baseline {
			t.Fatal("nil native entry published a runtime context")
		}
	}()
	newNativeProcForTest(nil, nil, 0)
}
