//go:build llgo && (darwin || linux) && !baremetal

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package main

import (
	"os"
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"
)

// This standalone entry exercises native startup without linking the testing
// package. Each lifetime/nil-entry invocation owns a fresh registered-G count.

//llgo:type C
type nativeEntry func(unsafe.Pointer) unsafe.Pointer

//go:linkname newNativeProc github.com/xgo-dev/llgo/runtime/internal/runtime.NewProc
func newNativeProc(nativeEntry, unsafe.Pointer, uintptr)

//go:linkname allocRoot github.com/xgo-dev/llgo/runtime/internal/runtime.AllocRoot
func allocRoot(uintptr) unsafe.Pointer

//go:linkname getg github.com/xgo-dev/llgo/runtime/internal/runtime.getg
func getg() unsafe.Pointer

//go:linkname runtimeGMP github.com/xgo-dev/llgo/runtime/internal/runtime.GMPForTesting
func runtimeGMP() (goid, parentGoid uint64, mid int64, pid int32, gstatus, pstatus uint32, linked bool)

type gmpState struct {
	goid, parentGoid uint64
	mid              int64
	pid              int32
	gstatus, pstatus uint32
	linked           bool
}

func currentGMP() gmpState {
	g, parent, m, p, gs, ps, linked := runtimeGMP()
	return gmpState{g, parent, m, p, gs, ps, linked}
}

func require(ok bool, reason string) {
	if !ok {
		panic(reason)
	}
}

func checkRunning(state gmpState) {
	require(state.goid != 0 && state.goid <= 1<<63, "invalid G id")
	require(state.mid != 0 && state.pid >= 0, "missing M/P")
	require(state.gstatus == 2 && state.pstatus == 1 && state.linked, "G/M/P ownership")
}

type startResult struct {
	ready, release, done uint32
	valid                bool
	state                gmpState
}

type startPayload struct{ words [128]uint64 }

type startRecord struct {
	result  *startResult
	payload *startPayload
}

func callback(raw unsafe.Pointer) unsafe.Pointer {
	record := (*startRecord)(raw)
	result := record.result
	result.state = currentGMP()
	atomic.StoreUint32(&result.ready, 1)
	for atomic.LoadUint32(&result.release) == 0 {
		// A native C entry must not suspend a coroutine.
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
func start(result *startResult) {
	// The parent retains only result across GC. mstart/mexit own the scanned
	// startup record and its payload until the native callback returns.
	raw := allocRoot(unsafe.Sizeof(startRecord{}))
	require(raw != nil, "startup root allocation")
	record := (*startRecord)(raw)
	record.result = result
	record.payload = new(startPayload)
	for index := range record.payload.words {
		record.payload.words[index] = uint64(index)*19 + 7
	}
	newNativeProc(nativeEntry(callback), raw, 0)
}

func lifetime() {
	parent := currentGMP()
	checkRunning(parent)
	baseline := runtime.NumGoroutine()
	for iteration := 0; iteration < 4; iteration++ {
		result := new(startResult)
		defer atomic.StoreUint32(&result.release, 1)
		start(result)
		deadline := time.Now().Add(5 * time.Second)
		for atomic.LoadUint32(&result.ready) == 0 && time.Now().Before(deadline) {
			runtime.Gosched()
		}
		require(atomic.LoadUint32(&result.ready) != 0, "native entry did not start")
		checkRunning(result.state)
		require(result.state.parentGoid == parent.goid && result.state.goid != parent.goid &&
			result.state.mid != parent.mid && runtime.NumGoroutine() == baseline+1, "native child identity")
		runtime.GC()
		atomic.StoreUint32(&result.release, 1)
		deadline = time.Now().Add(5 * time.Second)
		for (atomic.LoadUint32(&result.done) == 0 || runtime.NumGoroutine() != baseline) && time.Now().Before(deadline) {
			runtime.Gosched()
		}
		require(atomic.LoadUint32(&result.done) != 0, "native entry did not return")
		require(result.valid && runtime.NumGoroutine() == baseline, "payload root or native retirement")
	}
}

func nilEntry() {
	baseline := runtime.NumGoroutine()
	defer func() {
		require(recover() == "go of nil func value", "nil native entry panic")
		require(runtime.NumGoroutine() == baseline, "nil entry published a context")
	}()
	newNativeProc(nil, nil, 0)
}

func gmpLinks() {
	parent := currentGMP()
	checkRunning(parent)
	results := make(chan gmpState, 2)
	for i := 0; i < cap(results); i++ {
		go func() { results <- currentGMP() }()
	}
	seen := map[uint64]bool{parent.goid: true}
	for i := 0; i < cap(results); i++ {
		state := <-results
		checkRunning(state)
		require(state.parentGoid == parent.goid && !seen[state.goid], "child parent or duplicate G id")
		seen[state.goid] = true
	}
}

func gmpIDs() {
	parent := currentGMP()
	checkRunning(parent)
	const count = 65
	results := make(chan gmpState, 2*count)
	for i := 0; i < count; i++ {
		go func() {
			results <- currentGMP()
			go func() { results <- currentGMP() }()
		}()
	}
	seen := map[uint64]bool{parent.goid: true}
	for i := 0; i < 2*count; i++ {
		state := <-results
		checkRunning(state)
		require(!seen[state.goid], "duplicate batched G id")
		seen[state.goid] = true
	}
}

type gPointers struct{ first, second unsafe.Pointer }

func getgIsolation() {
	mainG := getg()
	require(mainG != nil && getg() == mainG, "unstable main getg")
	ready := make(chan unsafe.Pointer, 2)
	release := make(chan struct{})
	results := make(chan gPointers, 2)
	for i := 0; i < 2; i++ {
		go func() {
			first := getg()
			ready <- first
			<-release
			results <- gPointers{first, getg()}
		}()
	}
	first, second := <-ready, <-ready
	require(first != second, "shared child g")
	close(release)
	for i := 0; i < 2; i++ {
		pointers := <-results
		require(pointers.first != nil && pointers.first == pointers.second && pointers.first != mainG, "unstable child getg")
	}
}

func main() {
	require(len(os.Args) == 2, "expected native startup case")
	switch os.Args[1] {
	case "lifetime":
		lifetime()
	case "nil-entry":
		nilEntry()
	case "gmp":
		for repeat := 0; repeat < 3; repeat++ {
			gmpLinks()
			gmpIDs()
			getgIsolation()
		}
	default:
		panic("unknown native startup case")
	}
	println("native startup ok", os.Args[1])
}
