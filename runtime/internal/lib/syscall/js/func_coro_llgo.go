// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm && llgo && llgo_coro

package js

// Func is a wrapped Go function to be called by JavaScript.
//
// The host-pull coroutine profile does not yet expose the separate managed
// JS-to-Go callback admission ABI required by FuncOf. Keeping the API shape
// here lets packages which only use ordinary syscall/js values compile without
// accidentally publishing the legacy synchronous raw callback.
type Func struct {
	Value
}

// FuncOf currently fails explicitly on the coroutine JS/WASM profile. A
// callback can suspend or start work dynamically, so it cannot be called
// through the legacy raw //export entry or inferred as executor-safe.
func FuncOf(func(this Value, args []Value) any) Func {
	panic("syscall/js: FuncOf requires the LLGo coroutine JS callback reactor")
}

// Release frees the resources associated with f.
func (f Func) Release() {}
