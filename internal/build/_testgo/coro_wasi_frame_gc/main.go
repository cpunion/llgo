package main

import (
	"runtime"
	"time"
	_ "unsafe"
	"weak"
)

const (
	LLGoFiles      = "stack_scrub.c"
	headMarker     = uint64(0x13579bdf2468ace0)
	tailMarker     = uint64(0xfedcba9876543210)
	wasmNonPointer = uintptr(1 << 31)
)

type payload struct {
	padding [14]uint64
	next    *payload
	marker  uint64
}

var (
	garbage    *payload
	pressure   []*payload
	weakGlobal uint64
)

//go:noinline
func publishWeak(result chan<- weak.Pointer[uint64], done chan<- struct{}) {
	object := new([128]uint64)
	object[37] = headMarker
	w := weak.Make(&object[37])
	if w != weak.Make(&object[37]) || w == weak.Make(&object[38]) || w.Value() != &object[37] {
		failWASIFrameGC(104)
	}
	runtime.GC()
	if w.Value() != &object[37] || object[37] != headMarker {
		failWASIFrameGC(105)
	}
	result <- w
	close(done)
}

func checkWeakLifecycle() {
	global := weak.Make(&weakGlobal)
	result := make(chan weak.Pointer[uint64], 1)
	done := make(chan struct{})
	go publishWeak(result, done)
	w := <-result
	<-done
	// Let the producer return and release its coroutine frame. The C helper
	// then overwrites stale resume-stack pointers, just as for the frame test.
	time.Sleep(time.Millisecond)
	for i := 0; i < 8; i++ {
		if overwriteNativeResumeStack(uintptr(i))&wasmNonPointer == 0 {
			failWASIFrameGC(106)
		}
		runtime.GC()
	}
	if w.Value() != nil || global.Value() != &weakGlobal {
		failWASIFrameGC(107)
	}
	// Reusing freed allocation slots must never revive an earlier weak handle.
	allocatePressure()
	runtime.GC()
	if w.Value() != nil {
		failWASIFrameGC(108)
	}
}

//go:noinline
func retainAcrossSuspend(started chan<- uint64, result chan<- uint64) {
	seed := uint64(time.Now().UnixNano())
	tail := &payload{marker: seed ^ tailMarker}
	head := &payload{next: tail, marker: seed<<1 ^ headMarker}
	for index := range head.padding {
		head.padding[index] = seed + uint64(index+1)
	}

	started <- payloadChecksum(head)
	time.Sleep(500 * time.Millisecond)
	result <- payloadChecksum(head)
}

//go:noinline
func allocatePressure() {
	const count = 8192
	pressure = make([]*payload, count)
	for index := range pressure {
		pressure[index] = &payload{marker: uint64(index) + 1}
	}
}

//go:noinline
func allocateGarbage() {
	for index := 0; index < 2048; index++ {
		garbage = &payload{marker: uint64(index) + 1}
	}
	garbage = nil
}

//go:noinline
func payloadChecksum(head *payload) uint64 {
	checksum := head.marker ^ head.next.marker
	for _, word := range head.padding {
		checksum ^= word
	}
	return checksum
}

// This test-only foreign leaf has no calls, callbacks, allocation, or waits.
//
//llgo:coro noblock
//go:linkname overwriteNativeResumeStack C.llgo_coro_wasi_frame_gc_scrub_stack
func overwriteNativeResumeStack(seed uintptr) uintptr

// This test-only C leaf terminates the WASI command through libc's proc_exit
// binding and gives each invariant a stable process status.
//
//llgo:coro noblock
//go:linkname wasiFrameGCExit C.llgo_coro_wasi_frame_gc_exit
func wasiFrameGCExit(status int32)

func failWASIFrameGC(status int32) {
	wasiFrameGCExit(status)
	panic("WASI proc_exit unexpectedly returned")
}

func main() {
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	started := make(chan uint64, 1)
	result := make(chan uint64, 1)
	go retainAcrossSuspend(started, result)
	expected := <-started

	// Yield the executor once so the child parks on its longer timer. At both
	// collections head/tail are reachable only from the child's LLVM coroutine
	// frame. Overwrite more native resume-stack space than this fixture uses so
	// stale words from the child's last resume cannot make the test pass.
	time.Sleep(10 * time.Millisecond)
	if overwriteNativeResumeStack(uintptr(expected))&wasmNonPointer == 0 {
		failWASIFrameGC(101)
	}
	allocateGarbage()
	runtime.GC()
	allocatePressure()
	runtime.GC()

	if got := <-result; got != expected {
		failWASIFrameGC(102)
	}

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	if stats.NumGC < before.NumGC+2 || stats.Mallocs < before.Mallocs+8192 ||
		stats.Frees <= before.Frees || stats.HeapSys == 0 || stats.GCSys == 0 {
		failWASIFrameGC(103)
	}
	checkWeakLifecycle()
}
