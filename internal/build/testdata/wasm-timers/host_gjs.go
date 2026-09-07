//go:build js && wasm && !llgo.wasm.emscripten

package main

import "syscall/js"

// The existing browser matrix executes this before its completion marker,
// without adding another browser job or another module build.
func init() {
	const text = "Go\x00兼容"
	if js.ValueOf(text).String() != text {
		panic("GJS browser string round trip failed")
	}
	inner := js.FuncOf(func(js.Value, []js.Value) any {
		ch := make(chan int)
		go func() { ch <- 42 }()
		return <-ch
	})
	defer inner.Release()
	outer := js.FuncOf(func(js.Value, []js.Value) any { return inner.Invoke() })
	defer outer.Release()
	if got := outer.Invoke(); got.Type() != js.TypeNumber || got.Int() != 42 {
		panic("GJS browser nested blocking callback failed")
	}
}
