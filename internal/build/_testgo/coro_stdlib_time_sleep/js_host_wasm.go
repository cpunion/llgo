//go:build js && wasm

package main

import (
	"syscall/js"
)

func verifyJSHost() {
	global := js.Global()
	if got := global.Get("Math").Call("max", 3, 7).Int(); got != 7 {
		panic("syscall/js method call returned the wrong number")
	}

	object := js.ValueOf(map[string]any{"name": "llgo", "count": 2})
	if got := object.Get("name").String(); got != "llgo" {
		panic("syscall/js object returned the wrong string")
	}
	object.Set("ready", true)
	if !object.Get("ready").Bool() {
		panic("syscall/js object property was not set")
	}
	object.Delete("count")
	if !object.Get("count").IsUndefined() {
		panic("syscall/js object property was not deleted")
	}

	array := js.ValueOf([]any{1, "two"})
	array.SetIndex(0, 3)
	if array.Length() != 2 || array.Index(0).Int() != 3 ||
		array.Index(1).String() != "two" {
		panic("syscall/js array round trip failed")
	}

	uint8Array := global.Get("Uint8Array")
	bytes := uint8Array.New(3)
	if !bytes.InstanceOf(uint8Array) {
		panic("syscall/js constructor returned the wrong value")
	}
	if copied := js.CopyBytesToJS(bytes, []byte{4, 5, 6}); copied != 3 {
		panic("syscall/js CopyBytesToJS returned the wrong length")
	}
	dst := make([]byte, 3)
	if copied := js.CopyBytesToGo(dst, bytes); copied != 3 ||
		dst[0] != 4 || dst[1] != 5 || dst[2] != 6 {
		panic("syscall/js CopyBytesToGo round trip failed")
	}
}
