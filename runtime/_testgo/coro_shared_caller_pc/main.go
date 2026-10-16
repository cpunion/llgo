package main

import (
	"runtime"
	"strings"
)

//go:noinline
func firstMarker() uintptr {
	var pcs [1]uintptr
	if runtime.Callers(1, pcs[:]) != 1 {
		panic("first caller PC missing")
	}
	return pcs[0]
}

//go:noinline
func secondMarker() uintptr {
	var pcs [1]uintptr
	if runtime.Callers(1, pcs[:]) != 1 {
		panic("second caller PC missing")
	}
	return pcs[0]
}

func check(pc uintptr, name string) {
	fn := runtime.FuncForPC(pc)
	if fn == nil || !strings.Contains(fn.Name(), name) {
		panic("cross-goroutine FuncForPC identity changed: " + name)
	}
	frame, _ := runtime.CallersFrames([]uintptr{pc}).Next()
	if frame.Function != fn.Name() || frame.Line <= 0 || !strings.HasSuffix(frame.File, "main.go") {
		panic("cross-goroutine CallersFrames identity changed: " + name)
	}
}

func main() {
	channel := make(chan uintptr, 1)
	go func() { channel <- firstMarker() }()
	first := <-channel
	check(first, "firstMarker")
	go func() { channel <- secondMarker() }()
	second := <-channel
	if first == second {
		panic("different logical caller frames reused one PC")
	}
	check(second, "secondMarker")
	check(first, "firstMarker")
	println("shared caller PC identity passed")
}
