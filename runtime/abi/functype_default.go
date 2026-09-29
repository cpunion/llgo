//go:build !llgo || ((!wasm || !wasip1) && (!goexperiment.simd || (!amd64 && !arm64)))

package abi

// FuncType represents a function type on targets that use libffi.
type FuncType struct {
	Type
	In  []*Type
	Out []*Type
}
