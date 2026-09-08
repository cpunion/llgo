//go:build llgo && wasm

package abi

import "unsafe"

// The compiler supplies a typed indirect-call bridge for each statically
// described WebAssembly signature. Calls stay inside Wasm so GC and stack
// suspension do not cross an untracked JavaScript libffi continuation.
// Native targets retain their existing descriptor layout and libffi backend.
type FuncType struct {
	Type
	In    []*Type
	Out   []*Type
	Call_ unsafe.Pointer
	Make_ unsafe.Pointer
}
