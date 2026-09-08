//go:build llgo && wasm && wasip1

package abi

import "unsafe"

// WASI cannot manufacture executable libffi stubs at runtime. The compiler
// supplies a typed indirect-call bridge for each statically described signature.
// Other targets retain their existing descriptor layout and libffi backend.
type FuncType struct {
	Type
	In    []*Type
	Out   []*Type
	Call_ unsafe.Pointer
	Make_ unsafe.Pointer
}
