//go:build llgo && ((wasm && wasip1) || (goexperiment.simd && (amd64 || arm64)))

package abi

import "unsafe"

// FuncType carries typed entries for WASI indirect calls and native SIMD
// signatures, whose vector ABI cannot be described by libffi struct types.
type FuncType struct {
	Type
	In    []*Type
	Out   []*Type
	Call_ unsafe.Pointer
	Make_ unsafe.Pointer
}
