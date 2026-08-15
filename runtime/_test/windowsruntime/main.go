package main

import (
	"runtime"
	"unsafe"

	nativesync "github.com/goplus/llgo/runtime/internal/clite/sync"
	// The smoke package lives below the LLGo runtime root, whose packages are
	// excluded from the ordinary need-runtime signal. Import the core runtime
	// explicitly so its global state is initialized before the low-level test.
	_ "github.com/goplus/llgo/runtime/internal/runtime"
)

const LLGoFiles = "_wrap/fault.c"

//go:linkname windowsInvalidAddress C.llgo_windows_invalid_address
func windowsInvalidAddress() uintptr

//go:linkname windowsUnrecoveredFault C.llgo_windows_unrecovered_fault
func windowsUnrecoveredFault() int32

//go:noinline
func windowsNilFault() byte {
	return *(*byte)(unsafe.Pointer(windowsInvalidAddress()))
}

func hasSuffix(value, suffix string) bool {
	return len(value) >= len(suffix) && value[len(value)-len(suffix):] == suffix
}

func checkNilFault() {
	for attempt := 0; attempt < 2; attempt++ {
		deferred := false
		recovered := false
		func() {
			defer func() {
				value := recover()
				if value == nil {
					panic("Windows nil fault was not recoverable")
				}
				err, ok := value.(error)
				if !ok || err.Error() != "runtime error: invalid memory address or nil pointer dereference" {
					panic("Windows nil fault returned the wrong panic value")
				}
				if !deferred {
					panic("Windows nil fault skipped an earlier defer")
				}

				var pcs [32]uintptr
				n := runtime.Callers(0, pcs[:])
				frames := runtime.CallersFrames(pcs[:n])
				found := false
				for {
					frame, more := frames.Next()
					if hasSuffix(frame.Function, ".windowsNilFault") {
						found = true
					}
					if !more {
						break
					}
				}
				if !found {
					panic("Windows nil fault traceback lost the faulting frame")
				}
				recovered = true
			}()
			defer func() { deferred = true }()
			_ = windowsNilFault()
		}()
		if !recovered || !deferred {
			panic("Windows nil fault did not complete recovery")
		}
	}
}

func checkRecover() {
	defer func() {
		if value := recover(); value != "windows panic smoke" {
			panic("wrong recovered value")
		}
	}()
	panic("windows panic smoke")
}

func main() {
	values := make(chan int)
	go func() {
		values <- 42
	}()
	if value := <-values; value != 42 {
		panic("wrong channel value")
	}

	var once nativesync.Once
	done := make(chan struct{}, 4)
	onceValue := 0
	for i := 0; i < 4; i++ {
		go func() {
			if result := once.Do(func() { onceValue = 7 }); result != 0 {
				panic("native once failed")
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	if onceValue != 7 {
		panic("native once ran incorrectly")
	}

	checkRecover()
	checkNilFault()
	if windowsUnrecoveredFault() != 0 {
		_ = windowsNilFault()
		panic("unrecovered Windows fault returned")
	}
	println("windows runtime smoke: ok")
}
