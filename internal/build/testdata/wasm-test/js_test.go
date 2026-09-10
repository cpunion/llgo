//go:build js && wasm

package wasmtest

import (
	"syscall/js"
	"testing"
	"time"
)

func TestJSValueZeroIsUndefined(t *testing.T) {
	var value js.Value
	if !value.IsUndefined() || value.Type() != js.TypeUndefined {
		t.Fatalf("zero js.Value = undefined %v, type %v", value.IsUndefined(), value.Type())
	}
	if !value.Equal(js.Undefined()) {
		t.Fatal("zero js.Value does not equal js.Undefined()")
	}
}

func TestHostCallbackWakesScheduler(t *testing.T) {
	done := make(chan struct{}, 1)
	callback := js.FuncOf(func(js.Value, []js.Value) any {
		done <- struct{}{}
		return nil
	})
	defer callback.Release()

	js.Global().Call("setTimeout", callback, 0)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("JavaScript callback did not wake the scheduler")
	}
}

func TestJSFuncRunsSynchronouslyFromGoCall(t *testing.T) {
	ran := false
	callback := js.FuncOf(func(js.Value, []js.Value) any {
		ran = true
		return 7
	})
	defer callback.Release()

	got := callback.Invoke()
	if !ran {
		t.Fatal("js.FuncOf callback did not run before Invoke returned")
	}
	if got.Int() != 7 {
		t.Fatalf("Invoke() = %v, want 7", got)
	}
}

func TestJSFuncCompletesBufferedChannelBeforeCallReturns(t *testing.T) {
	c := make(chan int, 1)
	callback := js.FuncOf(func(js.Value, []js.Value) any {
		c <- 99
		return nil
	})
	defer callback.Release()

	obj := js.Global().Get("Object").New()
	obj.Set("write", callback)
	obj.Call("write")
	select {
	case got := <-c:
		if got != 99 {
			t.Fatalf("got %d, want 99", got)
		}
	default:
		t.Fatal("js.FuncOf callback did not send on the buffered channel before Call returned")
	}
}

func TestJSFuncReinstallPreservesNestedDispatch(t *testing.T) {
	var callback, replacement js.Func
	callback = js.FuncOf(func(js.Value, []js.Value) any {
		// Releasing the last callback temporarily empties the registry. Creating
		// its replacement must not reinstall the module bridge or reset the
		// active Go-to-JS call depth.
		callback.Release()
		replacement = js.FuncOf(func(_ js.Value, args []js.Value) any {
			return args[0].Int() + 1
		})
		return replacement.Invoke(40)
	})

	if got := callback.Invoke(); got.Int() != 41 {
		t.Fatalf("nested replacement callback = %v, want 41", got)
	}
	defer replacement.Release()
	if got := replacement.Invoke(41); got.Int() != 42 {
		t.Fatalf("replacement callback after return = %v, want 42", got)
	}
}

func TestHostCallbackCanBlock(t *testing.T) {
	done := make(chan int, 1)
	callback := js.FuncOf(func(js.Value, []js.Value) any {
		value := make(chan int)
		go func() {
			value <- 42
		}()
		got := <-value
		done <- got
		return got
	})
	defer callback.Release()

	js.Global().Call("setTimeout", callback, 0)
	select {
	case got := <-done:
		if got != 42 {
			t.Fatalf("callback result = %d, want 42", got)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked JavaScript callback prevented another goroutine from running")
	}
}
