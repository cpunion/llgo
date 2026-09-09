package cl

import (
	"go/constant"
	"go/token"
	"go/types"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// guardPanicSite records a checked operation only on its failure path on Wasm.
// Call after compiling its operands, and defer the returned restore function.
// Native fault/PC-line attribution retains the existing eager update.
func (p *context) guardPanicSite(b llssa.Builder, pos token.Pos) (restore func()) {
	if p.prog.Target().GOARCH != "wasm" {
		p.recordPanicSite(b, pos)
		return func() {}
	}
	previous := b.SetPanicLocation(func() { p.recordPanicSite(b, pos) })
	return func() { b.SetPanicLocation(previous) }
}

// Known-safe initializer addresses need no panic-location runtime update.
// Keep dynamic indexes, slices, and possibly nil array pointers unchanged.
// Otherwise each field of a generated table introduces a suspendable call
// that Asyncify must expand despite the address being statically valid.
func isKnownSafeArrayIndexAddr(address *ssa.IndexAddr) bool {
	pointer, ok := types.Unalias(address.X.Type()).Underlying().(*types.Pointer)
	if !ok || !isKnownNonNilAddr(address.X) {
		return false
	}
	array, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Array)
	if !ok {
		return false
	}
	index, ok := address.Index.(*ssa.Const)
	return ok && index.Value.Kind() == constant.Int &&
		constant.Compare(index.Value, token.GEQ, constant.MakeInt64(0)) &&
		constant.Compare(index.Value, token.LSS, constant.MakeInt64(array.Len()))
}
