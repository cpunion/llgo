//go:build js && wasm && !llgo.wasm.emscripten

// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license.
// See LICENSES/Go-BSD-3-Clause.txt at the repository root for license terms.

package wasmtest

import (
	"syscall/js"
	"testing"
)

// From Go's syscall/js TestInterleavedFunctions, with explicit Release calls.
func TestGJSInterleavedFunctions(t *testing.T) {
	c1 := make(chan struct{})
	c2 := make(chan struct{})
	callback := js.FuncOf(func(this js.Value, args []js.Value) any {
		c1 <- struct{}{}
		<-c2
		return nil
	})
	defer callback.Release()
	js.Global().Get("setTimeout").Invoke(callback, 0)
	<-c1
	c2 <- struct{}{}
	// The setTimeout callback has not returned yet; invoke another function.
	f := js.FuncOf(func(this js.Value, args []js.Value) any { return nil })
	defer f.Release()
	f.Invoke()
}
