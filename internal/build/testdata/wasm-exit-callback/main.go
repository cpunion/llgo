//go:build js && wasm

package main

import (
	"os"
	"syscall/js"
)

func main() {
	callback := js.FuncOf(func(js.Value, []js.Value) any {
		os.Exit(7)
		return nil
	})
	defer callback.Release()

	js.Global().Get("Function").New("callback", "callback();").Invoke(callback)
	panic("os.Exit returned")
}
