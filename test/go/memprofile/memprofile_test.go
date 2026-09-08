package memprofile

import (
	"bytes"
	"fmt"
	"runtime"
	"runtime/pprof"
	"testing"
	"unsafe"
)

var tinySink []*int32

func TestRuntimeMemProfileBufferContract(t *testing.T) {
	oldRate := runtime.MemProfileRate
	defer func() { runtime.MemProfileRate = oldRate }()
	runtime.MemProfileRate = 1
	allocateTinyObjects(256)
	runtime.GC()
	runtime.GC()
	runtime.MemProfileRate = 0
	n, _ := runtime.MemProfile(nil, true)
	if n == 0 {
		t.Fatal("missing allocation records")
	}
	// Extra capacity must not be overwritten, and a short buffer must be
	// rejected without exposing a partial snapshot.
	records := make([]runtime.MemProfileRecord, n+256)
	for i := range records {
		records[i].AllocBytes = -1
	}
	for _, length := range []int{0, n - 1} {
		if got, ok := runtime.MemProfile(records[:length], true); ok || got < n {
			t.Fatalf("buffer length %d = %d, %t, want at least %d, false", length, got, ok, n)
		}
		for i := range records {
			if records[i].AllocBytes != -1 {
				t.Fatalf("snapshot with buffer length %d modified record %d", length, i)
			}
		}
	}
	got, ok := runtime.MemProfile(records, true)
	if !ok || got < n {
		t.Fatalf("full buffer = %d, %t, want at least %d, true", got, ok, n)
	}
	for i := got; i < len(records); i++ {
		if records[i].AllocBytes != -1 {
			t.Fatalf("snapshot modified trailing record %d", i)
		}
	}
}

func TestRuntimeMemProfileReportsTinyAllocations(t *testing.T) {
	oldRate := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() {
		runtime.MemProfileRate = oldRate
	}()

	const n = 4096
	tinySink = make([]*int32, 0, n)
	for i := 0; i < n; i++ {
		p := new(int32)
		*p = int32(i)
		tinySink = append(tinySink, p)
	}
	runtime.GC()
	runtime.GC()

	records := readMemProfile(t)
	wantBytes := int64(n * 4)
	wantGranule := int64(16)
	// Go's tiny allocator uses 16-byte blocks even on its 64-bit-pointer
	// wasm profile. Only LLGo's linear collector scales its four-word blocks.
	if runtime.Compiler == "llgo" && runtime.GOARCH == "wasm" {
		wantGranule = int64(4 * unsafe.Sizeof(uintptr(0)))
	}
	for _, r := range records {
		inUseObjects := r.InUseObjects()
		inUseBytes := r.InUseBytes()
		if inUseObjects <= 0 || inUseBytes <= 0 {
			continue
		}
		if got := len(r.Stack()); got > len(r.Stack0) {
			t.Fatalf("MemProfileRecord.Stack length = %d, want <= %d", got, len(r.Stack0))
		}
		if inUseBytes/inUseObjects == wantGranule && inUseBytes >= wantBytes {
			return
		}
	}
	t.Fatalf("MemProfile did not report tiny allocations totaling at least %d bytes: %#v", wantBytes, records)
}

func TestRuntimePprofHeapProfileReportsTinyAllocations(t *testing.T) {
	oldRate := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() {
		runtime.MemProfileRate = oldRate
	}()

	const n = 4096
	allocateTinyObjects(n)
	runtime.GC()
	runtime.GC()

	var buf bytes.Buffer
	if err := pprof.Lookup("heap").WriteTo(&buf, 1); err != nil {
		t.Fatalf("heap profile WriteTo failed: %v", err)
	}

	var inUseObjects, inUseBytes, allocObjects, allocBytes, rate int64
	if _, err := fmt.Fscanf(bytes.NewReader(buf.Bytes()), "heap profile: %d: %d [%d: %d] @ heap/%d",
		&inUseObjects, &inUseBytes, &allocObjects, &allocBytes, &rate); err != nil {
		t.Fatalf("failed to parse heap profile header: %v\n%s", err, buf.String())
	}
	wantBytes := int64(n * 4)
	if inUseObjects <= 0 || allocObjects <= 0 || inUseBytes < wantBytes || allocBytes < wantBytes {
		t.Fatalf("heap profile totals = %d: %d [%d: %d], want live allocation bytes >= %d\n%s",
			inUseObjects, inUseBytes, allocObjects, allocBytes, wantBytes, buf.String())
	}
}

func readMemProfile(t *testing.T) []runtime.MemProfileRecord {
	t.Helper()
	var records []runtime.MemProfileRecord
	for {
		n, ok := runtime.MemProfile(records, false)
		if ok {
			return records[:n]
		}
		records = make([]runtime.MemProfileRecord, n+10)
	}
}

func allocateTinyObjects(n int) {
	tinySink = make([]*int32, 0, n)
	for i := 0; i < n; i++ {
		p := new(int32)
		*p = int32(i)
		tinySink = append(tinySink, p)
	}
}
