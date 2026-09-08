//go:build !llgo || !wasm || !wasip1

package abi

type FuncType struct {
	Type
	In  []*Type
	Out []*Type
}
