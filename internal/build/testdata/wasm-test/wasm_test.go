package wasmtest

import (
	"crypto/sha256"
	"fmt"
	"math"
	"runtime"
	"testing"
	"time"
)

func TestStandardLibraryWasmAssembly(t *testing.T) {
	if got := math.Floor(3.75); got != 3 {
		t.Fatalf("math.Floor(3.75) = %v, want 3", got)
	}
	const wantSHA256 = "336154bf67f765f8f75d16a0accee61b5ee5f6a75b2a2905703df913bd550f3e"
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte("wasm"))); got != wantSHA256 {
		t.Fatalf("sha256.Sum256(wasm) = %s, want %s", got, wantSHA256)
	}
}

func TestScheduler(t *testing.T) {
	done := make(chan int, 1)
	go func() {
		done <- 42
	}()

	select {
	case got := <-done:
		if got != 42 {
			t.Fatalf("goroutine result = %d, want 42", got)
		}
	case <-time.After(time.Second):
		t.Fatal("goroutine did not make progress")
	}
}

func TestExplicitGCDoesNotStarveRunnableGoroutines(t *testing.T) {
	gcStarted := make(chan struct{})
	stopGC := make(chan struct{})
	gcDone := make(chan struct{})
	go func() {
		defer close(gcDone)
		close(gcStarted)
		for {
			select {
			case <-stopGC:
				return
			default:
				runtime.GC()
			}
		}
	}()
	<-gcStarted
	defer func() {
		close(stopGC)
		<-gcDone
	}()

	const goroutines = 20
	done := make(chan struct{}, goroutines)
	for range goroutines {
		go func() {
			done <- struct{}{}
		}()
	}
	for range goroutines {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("explicit GC starved runnable goroutines")
		}
	}
}

func TestPanicRecoverAndCaller(t *testing.T) {
	defer func() {
		got := recover()
		if got != "wasm-test-panic" {
			t.Fatalf("recover = %v, want wasm-test-panic", got)
		}
	}()

	if _, file, line, ok := runtime.Caller(0); !ok || file == "" || line == 0 {
		t.Fatalf("runtime.Caller = %q:%d, %v", file, line, ok)
	}
	panic("wasm-test-panic")
}

func TestCallerSeesPanicFrame(t *testing.T) {
	panicCallerLine := 0
	defer func() {
		_, file, line, ok := runtime.Caller(2)
		if !ok || file == "" || line != panicCallerLine {
			t.Errorf("runtime.Caller(2) during panic = %q:%d, %v; want line %d", file, line, ok, panicCallerLine)
		}
		var pcs [8]uintptr
		n := runtime.Callers(0, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		foundPanic := false
		for {
			frame, more := frames.Next()
			if frame.Function == "runtime.gopanic" {
				foundPanic = true
			}
			if !more {
				break
			}
		}
		if !foundPanic {
			t.Error("runtime.Callers omitted runtime.gopanic")
		}
		if got := recover(); got != "wasm-caller-panic" {
			t.Errorf("recover = %v, want wasm-caller-panic", got)
		}
		_, file, line, ok = runtime.Caller(2)
		if !ok || file == "" || line != panicCallerLine {
			t.Errorf("runtime.Caller(2) after recover = %q:%d, %v; want line %d", file, line, ok, panicCallerLine)
		}
	}()
	_, _, panicCallerLine, _ = runtime.Caller(0)
	panicCallerLine += 2
	panic("wasm-caller-panic")
}
