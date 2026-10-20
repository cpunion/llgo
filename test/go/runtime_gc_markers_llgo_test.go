//go:build llgo && !nogc && !baremetal && !llgo_wasm_gc && !wasm && !tinygo.wasm && (darwin || linux)

package gotest

import (
	"os"
	"runtime"
	"strconv"
	"testing"
	_ "unsafe"
)

//go:linkname getBDWGCParallel C.GC_get_parallel
func getBDWGCParallel() int32

// Check the collector initialized by the actual compiler entry, rather than
// calling the init wrapper in isolation. Creating the runtime TLS key used to
// allocate through GC before the allocator bootstrap could set its policy.
func TestRuntimeGCInitialMarkerQuota(t *testing.T) {
	if _, set := os.LookupEnv("GC_MARKERS"); set {
		t.Skip("explicit collector marker count")
	}
	if _, set := os.LookupEnv("GC_NPROCS"); set {
		t.Skip("explicit collector CPU count")
	}
	quota, err := strconv.Atoi(os.Getenv("GOMAXPROCS"))
	if err != nil || quota <= 0 {
		t.Skip("no explicit initial Go execution quota")
	}
	before := int(getBDWGCParallel()) + 1
	if before < 1 || before > quota {
		t.Fatalf("collector started %d markers with GOMAXPROCS=%d", before, quota)
	}
	runtime.GC()
	if after := int(getBDWGCParallel()) + 1; after != before {
		t.Fatalf("collector marker count changed after GC: %d -> %d", before, after)
	}
}
