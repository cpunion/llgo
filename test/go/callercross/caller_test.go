package callercross

import (
	"reflect"
	"runtime"
	"testing"
)

const functionPrefix = "github.com/xgo-dev/llgo/test/go/callercross."

type capturedPCs struct {
	wantName string
	caller   uintptr
	file     string
	line     int
	pcs      [8]uintptr
	n        int
	rawEntry uintptr
}

//go:noinline
func captureOne(out chan<- capturedPCs, done chan<- struct{}) {
	defer close(done)
	got := capturedPCs{wantName: functionPrefix + "captureOne"}
	// Keep these calls on adjacent lines: the Callers frame must name line+1.
	got.caller, got.file, got.line, _ = runtime.Caller(0)
	got.n = runtime.Callers(1, got.pcs[:])
	got.rawEntry = reflect.ValueOf(captureOne).Pointer()
	out <- got
}

//go:noinline
func captureTwo(out chan<- capturedPCs, done chan<- struct{}) {
	defer close(done)
	got := capturedPCs{wantName: functionPrefix + "captureTwo"}
	// Both fresh goroutines used to assign the same small local PC indexes.
	got.caller, got.file, got.line, _ = runtime.Caller(0)
	got.n = runtime.Callers(1, got.pcs[:])
	got.rawEntry = reflect.ValueOf(captureTwo).Pointer()
	out <- got
}

func checkCapturedPCs(t *testing.T, got capturedPCs) {
	t.Helper()
	if got.caller == 0 || got.n == 0 {
		t.Fatalf("%s captured no PCs", got.wantName)
	}
	f := runtime.FuncForPC(got.caller)
	if f == nil || f.Name() != got.wantName {
		t.Fatalf("Caller PC %#x resolves to %v, want %s", got.caller, f, got.wantName)
	}
	if file, line := f.FileLine(got.caller); file != got.file || line != got.line {
		t.Fatalf("Caller PC resolves to %s:%d, want %s:%d", file, line, got.file, got.line)
	}
	frame, _ := runtime.CallersFrames(got.pcs[:got.n]).Next()
	if frame.Function != got.wantName || frame.File != got.file || frame.Line != got.line+1 {
		t.Fatalf("Callers frame = %+v, want %s at %s:%d", frame, got.wantName, got.file, got.line+1)
	}
	if frame.Func == nil || frame.Func.Name() != got.wantName || frame.Entry != f.Entry() || frame.Func.Entry() != f.Entry() {
		t.Fatalf("Caller and Callers disagree on function/entry: caller=%s/%#x, frame=%+v", f.Name(), f.Entry(), frame)
	}
	if file, line := frame.Func.FileLine(got.caller); file != got.file || line != got.line {
		t.Fatalf("Callers Func.FileLine(Caller PC) = %s:%d, want %s:%d", file, line, got.file, got.line)
	}
	// Include synthetic runtime frames, whose stable Entry may not name a
	// physical function. CallersFrames adjusts return PCs before lookup.
	frames := runtime.CallersFrames(got.pcs[:got.n])
	for {
		frame, more := frames.Next()
		if frame.Func != nil {
			fn := runtime.FuncForPC(frame.PC)
			if fn == nil || fn.Name() != frame.Function || fn.Entry() != frame.Entry || frame.Func.Entry() != frame.Entry {
				t.Fatalf("frame and FuncForPC disagree: frame=%+v, function=%v", frame, fn)
			}
			if file, line := fn.FileLine(frame.PC); file != frame.File || line != frame.Line {
				t.Fatalf("frame and Func.FileLine disagree: frame=%+v, file/line=%s:%d", frame, file, line)
			}
		}
		if !more {
			break
		}
	}
	// Real function-value PCs occupy WebAssembly table indexes, not the
	// synthetic namespace. Resolve them after warming the synthetic caches.
	raw := runtime.FuncForPC(got.rawEntry)
	if raw == nil || raw.Name() != got.wantName || raw.Entry() != got.rawEntry || f.Entry() != got.rawEntry {
		t.Fatalf("function-value PC %#x aliases a synthetic frame: raw=%v, caller entry=%#x", got.rawEntry, raw, f.Entry())
	}
}

func TestPCsSurviveGoroutineExit(t *testing.T) {
	out := make(chan capturedPCs, 1)
	done := make(chan struct{})
	go captureOne(out, done)
	one := <-out
	<-done
	done = make(chan struct{})
	go captureTwo(out, done)
	two := <-out
	<-done
	if one.caller == two.caller || one.pcs[0] == two.pcs[0] {
		t.Fatalf("distinct functions received identical PCs: Caller=%#x/%#x, Callers=%#x/%#x", one.caller, two.caller, one.pcs[0], two.pcs[0])
	}
	runtime.Gosched()
	runtime.GC()
	runtime.Gosched()
	runtime.GC()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		// Populate an unrelated consumer frame before resolving saved PCs.
		_, _, _, _ = runtime.Caller(0)
		checkCapturedPCs(t, one)
		checkCapturedPCs(t, two)
	}()
	<-finished
	// A third goroutine (the test itself) must get the same immutable results,
	// including after the first consumer's symbolization caches were warmed.
	checkCapturedPCs(t, two)
	checkCapturedPCs(t, one)
}
