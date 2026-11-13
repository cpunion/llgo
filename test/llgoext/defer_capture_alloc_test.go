//go:build llgo

package llgoext

import "testing"

var deferCaptureAllocSink int

//go:noinline
func deferCaptureAllocation(value int, callback func()) {
	returned := false
	defer func() {
		if !returned {
			panic("callback did not return")
		}
		deferCaptureAllocSink += value
	}()
	callback()
	returned = true
}

func TestDeferCaptureNoAllocation(t *testing.T) {
	deferCaptureAllocSink = 0
	callback := func() {}
	i := 0
	// Use the standard per-call measurement rather than a process-wide zero
	// delta: an unrelated background allocation must not fail this regression.
	allocs := testing.AllocsPerRun(100, func() {
		deferCaptureAllocation(i, callback)
		i++
	})
	// AllocsPerRun includes one warm-up call, so values 0 through 100 run.
	if allocs != 0 || i != 101 || deferCaptureAllocSink != 5050 {
		t.Fatalf("allocations/call=%v, calls=%d, checksum=%d; want 0, 101, 5050", allocs, i, deferCaptureAllocSink)
	}
}
