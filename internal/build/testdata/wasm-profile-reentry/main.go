package main

import (
	"os"
	"runtime"
)

var keep []byte

// Capture the first application stack with allocation sampling already enabled.
// A testing.Test would warm the synthetic-PC registry before reaching this call
// and hide reentrancy while the registry's initial slices grow.
//
//go:noinline
func capture() {
	var pcs [32]uintptr
	n := runtime.Callers(0, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	found := false
	for {
		frame, more := frames.Next()
		const suffix = ".capture"
		if len(frame.Function) >= len(suffix) && frame.Function[len(frame.Function)-len(suffix):] == suffix {
			found = true
		}
		if !more {
			break
		}
	}
	if !found {
		frames = runtime.CallersFrames(pcs[:n])
		for {
			frame, more := frames.Next()
			println("captured", frame.PC, frame.Function, frame.Line)
			if !more {
				break
			}
		}
		panic("allocation profiling corrupted captured PCs")
	}
}

func main() {
	runtime.MemProfileRate = 1
	if len(os.Args) > 1 && os.Args[1] == "off" {
		runtime.MemProfileRate = 0
	}
	before, _ := runtime.MemProfile(nil, true)
	for i := 0; i < 64; i++ {
		capture()
		keep = make([]byte, 256+i)
	}
	if n, _ := runtime.MemProfile(nil, true); runtime.MemProfileRate != 0 && n <= before {
		panic("memory profiling did not resume after stack capture")
	}
}
