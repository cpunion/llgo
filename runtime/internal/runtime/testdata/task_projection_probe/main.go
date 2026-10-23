package main

import (
	rt "github.com/xgo-dev/llgo/runtime/internal/runtime"
	"runtime"
	"sync/atomic"
)

var result atomic.Int32

func main() {
	go func() {
		if rt.CoroNativeTaskProjectionProbeV1() {
			result.Store(1)
		} else {
			result.Store(-1)
		}
	}()
	for result.Load() == 0 {
		runtime.Gosched()
	}
	if result.Load() != 1 {
		panic("projection still needs ambient runtime G")
	}
	println("ok managed-task projection without ambient G install")
}
