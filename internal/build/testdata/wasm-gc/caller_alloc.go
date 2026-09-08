package main

import "runtime"

//go:noinline
func callerAllocationRecursion(depth int) int {
	if depth == 0 {
		return 0
	}
	return callerAllocationRecursion(depth-1) + 1
}

func testCallerStackReuse() {
	// Grow beyond the measured depth first. Reusing shadow-stack capacity
	// must not allocate the synthetic one-element array of an append call.
	if callerAllocationRecursion(128) != 128 {
		panic("caller allocation warmup failed")
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < 128; i++ {
		if callerAllocationRecursion(64) != 64 {
			panic("caller allocation recursion failed")
		}
	}
	runtime.ReadMemStats(&after)
	if after.TotalAlloc != before.TotalAlloc || after.Mallocs != before.Mallocs {
		println("caller stack allocated after warmup:", after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs)
		panic("caller stack reuse allocated")
	}
}
