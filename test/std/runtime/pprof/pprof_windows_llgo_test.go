//go:build llgo && windows && (amd64 || arm64)

package pprof_test

import (
	"testing"
	"time"
	_ "unsafe"
)

//go:linkname testCPUProfileWindowsFaultRecovery C.llgo_cpu_profile_test_fault_recovery
func testCPUProfileWindowsFaultRecovery() int32

//go:linkname currentThreadCPUProfileSamples C.llgo_cpu_profile_test_current_thread_samples
func currentThreadCPUProfileSamples() uint64

//go:linkname clearCPUProfileTestThread C.llgo_cpu_profile_test_clear_thread
func clearCPUProfileTestThread()

func init() {
	waitForCPUProfileSample = func() {
		before := currentThreadCPUProfileSamples()
		deadline := time.Now().Add(time.Second)
		for currentThreadCPUProfileSamples() == before {
			if time.Now().After(deadline) {
				clearCPUProfileTestThread()
				panic("Windows CPU profiler did not sample the current thread within 1s")
			}
		}
		clearCPUProfileTestThread()
	}
}

func TestCPUProfileWindowsFaultRecovery(t *testing.T) {
	if got := testCPUProfileWindowsFaultRecovery(); got != 1 {
		t.Fatalf("guarded frame walk returned %d frames, want interrupted PC only", got)
	}
}
