//go:build wasm && llgo.wasm.gc.linear && !llgo.wasm.workers && !llgo.wasi_threads

package memprofile

import (
	"bytes"
	"runtime"
	"runtime/pprof"
	"strings"
	"testing"
	"time"
	"unsafe"
)

var wasmProfileObjects [][]byte
var wasmProfileTimerObject []byte

func init() {
	tinyAllocationGranule = int64(4 * unsafe.Sizeof(uintptr(0)))
}

//go:noinline
func allocateWasmProfileTimerObject() {
	wasmProfileTimerObject = make([]byte, 96)
}

func TestWasmMemProfileTimerCallback(t *testing.T) {
	old := runtime.MemProfileRate
	defer func() { runtime.MemProfileRate = old; wasmProfileTimerObject = nil }()
	runtime.MemProfileRate = 1
	done := make(chan struct{})
	time.AfterFunc(time.Millisecond, func() {
		allocateWasmProfileTimerObject()
		close(done)
	})
	<-done
	runtime.MemProfileRate = 0
	for _, record := range readWasmProfile(false) {
		if record.AllocObjects > 0 && record.AllocBytes/record.AllocObjects == 96 && wasmProfileHasFrame(record, ".allocateWasmProfileTimerObject") {
			return
		}
	}
	t.Fatal("missing timer callback allocation stack")
}

//go:linkname freeWasmProfileObject github.com/xgo-dev/llgo/runtime/internal/runtime.FreeRoot
func freeWasmProfileObject(unsafe.Pointer)

//go:linkname pauseWasmMemProfile github.com/xgo-dev/llgo/runtime/internal/runtime.MemProfilePause
func pauseWasmMemProfile()

//go:linkname resumeWasmMemProfile github.com/xgo-dev/llgo/runtime/internal/runtime.MemProfileResume
func resumeWasmMemProfile()

//go:noinline
func allocateAndFreeWasmProfile() (reused bool) {
	// Keep the loop within one shadow-stack frame: entering a helper for each
	// allocation would let its frame metadata take the just-freed object head.
	var previous uintptr
	for i := 0; i < 152; i++ {
		if i == 25 {
			runtime.MemProfileRate = 0
		}
		if i == 75 {
			runtime.MemProfileRate = 1
		}
		if i == 150 {
			pauseWasmMemProfile()
			pauseWasmMemProfile()
		}
		if i == 151 {
			resumeWasmMemProfile()
		}
		object := make([]byte, 48)
		key := ^uintptr(unsafe.Pointer(unsafe.SliceData(object)))
		// Exercise the runtime-owned explicit-free path. Never access the
		// object again; compare only encoded addresses to keep the test weak.
		freeWasmProfileObject(unsafe.Pointer(unsafe.SliceData(object)))
		if key == previous {
			reused = true
		}
		previous = key
	}
	resumeWasmMemProfile()
	return
}

func TestWasmMemProfileExplicitFreeAndAddressReuse(t *testing.T) {
	old := runtime.MemProfileRate
	defer func() { runtime.MemProfileRate = old }()
	runtime.MemProfileRate = 0
	allocBefore, freeBefore := wasmExplicitProfileCounts()
	runtime.MemProfileRate = 1
	reused := allocateAndFreeWasmProfile()
	runtime.MemProfileRate = 0
	alloc, free := wasmExplicitProfileCounts()
	alloc -= allocBefore
	free -= freeBefore
	if alloc != 100 || free != alloc {
		t.Fatalf("reused object counts = alloc %d, free %d, want 100 each", alloc, free)
	}
	if !reused {
		t.Fatal("fixture did not exercise a reused allocation head")
	}
}

func wasmExplicitProfileCounts() (alloc, free int64) {
	for _, record := range readWasmProfile(true) {
		if wasmProfileHasFrame(record, ".allocateAndFreeWasmProfile") {
			alloc += record.AllocObjects
			free += record.FreeObjects
		}
	}
	return
}

//go:noinline
func allocateWasmProfileBuckets() {
	wasmProfileObjects = make([][]byte, 80)
	for i := range wasmProfileObjects {
		wasmProfileObjects[i] = make([]byte, 64*(i+1))
	}
}

func TestWasmMemProfileMoreThan64Buckets(t *testing.T) {
	old := runtime.MemProfileRate
	defer func() { runtime.MemProfileRate = old; wasmProfileObjects = nil }()
	runtime.MemProfileRate = 1
	allocateWasmProfileBuckets()
	runtime.MemProfileRate = 0
	records := readWasmProfile(true)
	sizes := make(map[int64]bool)
	for _, record := range records {
		if wasmProfileHasFrame(record, ".allocateWasmProfileBuckets") && record.AllocObjects != 0 {
			sizes[record.AllocBytes/record.AllocObjects] = true
		}
	}
	for i := int64(1); i <= 80; i++ {
		if !sizes[64*i] {
			t.Fatalf("missing size %d from %d stack-and-size buckets", 64*i, len(sizes))
		}
	}
	// Restore rate one while pprof allocates its conversion buffers: the
	// nested pause must exclude those allocations and allow an unbounded read.
	runtime.MemProfileRate = 1
	var output bytes.Buffer
	if err := pprof.Lookup("heap").WriteTo(&output, 1); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte("allocateWasmProfileBuckets")) {
		t.Fatalf("heap profile omitted the allocation stack: %s", output.String())
	}
}

type wasmProfileFinalized struct{ data [4096]byte }

//go:noinline
func allocateWasmProfileFinalized(done chan struct{}) {
	object := new(wasmProfileFinalized)
	object.data[0] = 1
	runtime.SetFinalizer(object, func(*wasmProfileFinalized) { close(done) })
	runtime.KeepAlive(object)
}

func TestWasmMemProfileWeakSamplesAndRateZeroFree(t *testing.T) {
	old := runtime.MemProfileRate
	defer func() { runtime.MemProfileRate = old }()
	runtime.MemProfileRate = 1
	done, exited := make(chan struct{}), make(chan struct{})
	go func() {
		allocateWasmProfileFinalized(done)
		close(exited)
	}()
	<-exited
	// Disable new samples before reclamation. Existing weak samples must
	// neither retain the finalized object nor lose its eventual free event.
	runtime.MemProfileRate = 0
	finalized := false
	for i := 0; i < 20; i++ {
		runtime.GC()
		runtime.Gosched()
		select {
		case <-done:
			finalized = true
		default:
		}
		if finalized {
			for _, record := range readWasmProfile(true) {
				if record.AllocObjects != 0 && record.AllocBytes/record.AllocObjects == int64(unsafe.Sizeof(wasmProfileFinalized{})) && wasmProfileHasFrame(record, ".allocateWasmProfileFinalized") && record.FreeObjects == record.AllocObjects {
					return
				}
			}
		}
	}
	t.Fatalf("sampled object was not fully reclaimed (finalized=%t)", finalized)
}

func readWasmProfile(inuseZero bool) []runtime.MemProfileRecord {
	var records []runtime.MemProfileRecord
	for {
		n, ok := runtime.MemProfile(records, inuseZero)
		if ok {
			return records[:n]
		}
		records = make([]runtime.MemProfileRecord, n+32)
	}
}

func wasmProfileHasFrame(record runtime.MemProfileRecord, suffix string) bool {
	frames := runtime.CallersFrames(record.Stack())
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, suffix) {
			return true
		}
		if !more {
			return false
		}
	}
}
