package runtime_test

import (
	"runtime"
	"strings"
	"testing"
)

//go:noinline
func callerPanicSite() {
	panic("caller panic site")
}

func TestCallerDuringPanicAndRecovery(t *testing.T) {
	defer func() {
		for phase := 0; phase < 2; phase++ {
			if phase == 1 {
				if got := recover(); got != "caller panic site" {
					t.Fatalf("recover() = %v", got)
				}
			}
			pc, _, _, ok := runtime.Caller(2)
			fn := runtime.FuncForPC(pc)
			if !ok || fn == nil || !strings.HasSuffix(fn.Name(), ".callerPanicSite") {
				t.Fatalf("phase %d Caller(2) = %v, %v; want callerPanicSite", phase, fn, ok)
			}
			var pcs [16]uintptr
			n := runtime.Callers(1, pcs[:])
			frames := runtime.CallersFrames(pcs[:n])
			// Deferred function, runtime panic dispatcher, then the panic site.
			frames.Next()
			frames.Next()
			frame, _ := frames.Next()
			if !strings.HasSuffix(frame.Function, ".callerPanicSite") {
				t.Fatalf("phase %d Callers frame 2 = %s; want callerPanicSite", phase, frame.Function)
			}
		}
	}()
	callerPanicSite()
}
