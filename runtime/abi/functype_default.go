//go:build !llgo || !wasm

package abi

type FuncType struct {
	Type
	In  []*Type
	Out []*Type
}
