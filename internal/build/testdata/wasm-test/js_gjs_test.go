//go:build js && wasm && !llgo.wasm.emscripten

package wasmtest

import (
	"math"
	"runtime"
	"syscall/js"
	"testing"
	"time"
)

// Run this file with both compilers. These assertions cover the public Go
// contract independently of LLGo's host frame and reference implementation.
func TestGJSValues(t *testing.T) {
	for _, s := range []string{"", "hello", "a\x00b", "你好 🌍"} {
		v := js.ValueOf(s)
		if v.Type() != js.TypeString || v.String() != s || !v.Equal(js.ValueOf(s)) {
			t.Fatalf("string round trip %q: %v", s, v)
		}
	}
	for _, n := range []float64{0, math.Copysign(0, -1), 42.5, -5, math.Inf(1), math.Inf(-1)} {
		v := js.ValueOf(n)
		if v.Float() != n || !v.Equal(js.ValueOf(n)) {
			t.Fatalf("number round trip %v: %v", n, v)
		}
	}
	nan := js.ValueOf(math.NaN())
	if !nan.IsNaN() || nan.Equal(nan) || nan.Truthy() {
		t.Fatal("NaN semantics differ from Go")
	}
	obj := js.Global().Get("Object").New()
	obj.Set("self", obj)
	if !obj.Equal(obj.Get("self")) || obj.Equal(js.Global().Get("Object").New()) {
		t.Fatal("object identity was not preserved")
	}
	obj.Set("a\x00b", 17)
	if obj.Get("a\x00b").Int() != 17 {
		t.Fatal("property name was truncated")
	}
	for _, length := range []any{js.Undefined(), math.NaN(), math.Inf(1), "not-a-number"} {
		obj.Set("length", length)
		if got := obj.Length(); got != 0 {
			t.Fatalf("non-numeric length %v = %d", length, got)
		}
	}
	obj.Set("length", -3)
	if got := obj.Length(); got != -3 {
		t.Fatalf("signed length = %d", got)
	}
}

func TestGJSExceptions(t *testing.T) {
	fn := js.Global().Get("Function").New("throw new Error('host-error')")
	defer func() {
		err, ok := recover().(js.Error)
		if !ok || err.Get("message").String() != "host-error" {
			t.Errorf("JS exception was not preserved: %v", err)
		}
	}()
	fn.Invoke()
}

func TestGJSByteCopies(t *testing.T) {
	for _, name := range []string{"Uint8Array", "Uint8ClampedArray"} {
		array := js.Global().Get(name).New(3)
		if n := js.CopyBytesToJS(array, []byte{1, 2, 255, 4}); n != 3 {
			t.Fatalf("%s copied %d bytes", name, n)
		}
		dst := make([]byte, 5)
		if n := js.CopyBytesToGo(dst, array); n != 3 || dst[2] != 255 || dst[3] != 0 {
			t.Fatalf("%s result: %d %v", name, n, dst)
		}
	}
	for _, name := range []string{"Array", "Uint16Array", "DataView"} {
		var array js.Value
		if name == "DataView" {
			array = js.Global().Get(name).New(js.Global().Get("ArrayBuffer").New(4))
		} else {
			array = js.Global().Get(name).New(4)
		}
		for _, toGo := range []bool{true, false} {
			func() {
				defer func() {
					if recover() == nil {
						t.Errorf("accepted %s for byte copy", name)
					}
				}()
				if toGo {
					js.CopyBytesToGo(make([]byte, 2), array)
				} else {
					js.CopyBytesToJS(array, []byte{1})
				}
			}()
		}
	}
}

func TestGJSSynchronousNestedCallbacks(t *testing.T) {
	inner := js.FuncOf(func(this js.Value, args []js.Value) any {
		return args[0].Int() + 1
	})
	defer inner.Release()
	outer := js.FuncOf(func(this js.Value, args []js.Value) any {
		return inner.Invoke(args[0])
	})
	defer outer.Release()
	if got := outer.Invoke(41).Int(); got != 42 {
		t.Fatalf("callback result = %d", got)
	}
}

func TestGJSExternalCallbackResult(t *testing.T) {
	obj := js.Global().Get("Object").New()
	callback := js.FuncOf(func(js.Value, []js.Value) any { return 42 })
	defer callback.Release()
	schedule := js.Global().Get("Function").New("cb", "obj", "setTimeout(() => { obj.result = cb(); }, 0)")
	schedule.Invoke(callback, obj)
	deadline := time.Now().Add(time.Second)
	for obj.Get("result").IsUndefined() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := obj.Get("result"); got.Type() != js.TypeNumber || got.Int() != 42 {
		t.Fatalf("external callback returned %v", got)
	}
}

func TestGJSCallbackGoroutineSwitch(t *testing.T) {
	callback := js.FuncOf(func(js.Value, []js.Value) any {
		ch := make(chan int)
		go func() { ch <- 42 }()
		return <-ch
	})
	defer callback.Release()
	obj := js.ValueOf(map[string]any{"calls": 0})
	invoke := js.Global().Get("Function").New("cb", "obj", "obj.calls++; const result = cb(); obj.after = result; return result")
	outer := js.FuncOf(func(js.Value, []js.Value) any {
		return invoke.Invoke(callback, obj)
	})
	defer outer.Release()
	for i := 1; i <= 20; i++ {
		if got := outer.Invoke(); got.Type() != js.TypeNumber || got.Int() != 42 {
			t.Fatalf("blocking nested callback returned %v", got)
		}
		if got := obj.Get("calls").Int(); got != i {
			t.Fatalf("JavaScript invocation replayed: calls = %d, want %d", got, i)
		}
		if got := obj.Get("after"); got.Type() != js.TypeNumber || got.Int() != 42 {
			t.Fatalf("JavaScript resumed before the callback completed: %v", got)
		}
		// Exercise the enclosing Fiber again after returning through both JS
		// boundaries: its continuation must no longer point to the callback.
		runtime.Gosched()
	}
}

func TestGJSCallbackTimer(t *testing.T) {
	callback := js.FuncOf(func(js.Value, []js.Value) any {
		time.Sleep(time.Millisecond)
		return 42
	})
	defer callback.Release()
	if got := callback.Invoke(); got.Type() != js.TypeNumber || got.Int() != 42 {
		t.Fatalf("sleeping callback returned %v", got)
	}
}

func TestGJSCallbackDrainsRunnableWork(t *testing.T) {
	obj := js.ValueOf(map[string]any{"done": false})
	callback := js.FuncOf(func(js.Value, []js.Value) any {
		go func() { obj.Set("done", true) }()
		return nil
	})
	defer callback.Release()
	invoke := js.Global().Get("Function").New("cb", "obj", "cb(); return obj.done")
	if !invoke.Invoke(callback, obj).Bool() {
		t.Fatal("callback returned to JS before runnable work completed")
	}
}

func TestGJSCallbackPanic(t *testing.T) {
	// Recover inside the callback. Recovering across the JS boundary leaves
	// an incomplete runtime event even in the official Go implementation.
	callback := js.FuncOf(func(js.Value, []js.Value) (result any) {
		defer func() {
			if got := recover(); got != "callback-panic" {
				t.Errorf("callback panic = %v", got)
			}
			result = 42
		}()
		panic("callback-panic")
	})
	defer callback.Release()
	if got := callback.Invoke().Int(); got != 42 {
		t.Fatalf("recovered callback result = %d", got)
	}
	next := js.FuncOf(func(js.Value, []js.Value) any { return 42 })
	defer next.Release()
	if got := next.Invoke().Int(); got != 42 {
		t.Fatalf("callback after recovery = %d", got)
	}
	runtime.Gosched()
}
