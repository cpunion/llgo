package gotest

import (
	"runtime"
	"strings"
	"testing"
)

//go:noinline
func inspectDeferredPanicCallers(t *testing.T, owner string) {
	t.Helper()
	// Caller starts at this helper, then the direct deferred function. gc's
	// gopanic remains observable both before and after that function recovers.
	for attempt := 0; attempt < 3; attempt++ {
		pc, _, _, ok := runtime.Caller(2)
		fn := runtime.FuncForPC(pc)
		if !ok || fn == nil || fn.Name() != "runtime.gopanic" {
			t.Fatalf("deferred Caller(2) has no gopanic frame: pc=%x ok=%t", pc, ok)
		}
		pc, _, _, ok = runtime.Caller(3)
		fn = runtime.FuncForPC(pc)
		if !ok || fn == nil || !strings.HasSuffix(fn.Name(), owner) {
			t.Fatalf("deferred Caller(3) lost panic owner %s: pc=%x ok=%t", owner, pc, ok)
		}
		var pcs [32]uintptr
		n := runtime.Callers(1, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		for index := 0; index <= 3; index++ {
			frame, more := frames.Next()
			if index == 2 && frame.Function != "runtime.gopanic" ||
				index == 3 && !strings.HasSuffix(frame.Function, owner) || index < 3 && !more {
				t.Fatalf("deferred Callers frame %d = %s (more=%t)", index, frame.Function, more)
			}
		}
	}
}

//go:noinline
func deferredPanicCallerDirect(t *testing.T) {
	defer func() {
		inspectDeferredPanicCallers(t, ".deferredPanicCallerDirect")
		if recover() != "direct" {
			t.Fatal("direct panic was not recovered")
		}
		inspectDeferredPanicCallers(t, ".deferredPanicCallerDirect")
	}()
	panic("direct")
}

//go:noinline
func deferredPanicCallerLeaf() {
	runtime.Gosched()
	panic("retained")
}

//go:noinline
func deferredPanicCallerRetained(t *testing.T) {
	defer func() {
		inspectDeferredPanicCallers(t, ".deferredPanicCallerLeaf")
		if recover() != "retained" {
			t.Fatal("retained panic was not recovered")
		}
		runtime.Gosched()
		inspectDeferredPanicCallers(t, ".deferredPanicCallerLeaf")
	}()
	deferredPanicCallerLeaf()
}

func TestRuntimeDeferredPanicCallerFrames(t *testing.T) {
	deferredPanicCallerDirect(t)
	deferredPanicCallerRetained(t)
}
