package main

import (
	"os"
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

//go:noinline
func panicMarker() {
	var pointer *struct{ ready chan int }
	select {
	case <-pointer.ready:
	}
}

//go:noinline
func checkRecoveredPC() {
	defer func() {
		if recover() == nil {
			panic("caller PC probe did not recover its nil fault")
		}
		for skip := 0; skip < 32; skip++ {
			pc, file, line, ok := runtime.Caller(skip)
			if !ok {
				break
			}
			fn := runtime.FuncForPC(pc)
			if fn != nil && strings.HasSuffix(fn.Name(), ".panicMarker") && line > 0 && strings.HasSuffix(file, "main.go") {
				return
			}
		}
		panic("recovered caller PC identity missing")
	}()
	panicMarker()
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
	for _, frame := range []struct {
		pc   uintptr
		name string
	}{{first, "firstMarker"}, {second, "secondMarker"}} {
		fn := runtime.FuncForPC(frame.pc - 1)
		if fn == nil || !strings.Contains(fn.Name(), frame.name) {
			panic("adjusted caller PC identity changed: " + frame.name)
		}
	}
	checkRecoveredPC()
	const marker = "shared caller PC identity passed\n"
	if count, err := os.Stdout.WriteString(marker); err != nil || count != len(marker) {
		panic("caller PC completion marker was not written")
	}
}
