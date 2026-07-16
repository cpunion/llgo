//go:build !llgo

package ssa

import (
	"go/token"
	"go/types"
	"testing"
)

func TestTypeBackgroundUsesNamedTypeMetadata(t *testing.T) {
	prog := &aProgram{gocvt: newGoTypes()}
	pkg := types.NewPackage("example.com/ffi", "ffi")
	sig := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	cFunc := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "CFunc", nil), sig, nil)
	goFunc := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "GoFunc", nil), sig, nil)
	looksLikeC := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "CFunction", nil), sig, nil)
	cAlias := types.NewAlias(types.NewTypeName(token.NoPos, pkg, "CFuncAlias", nil), cFunc)

	prog.SetTypeBackground("example.com/ffi.CFunc", InC)
	prog.SetTypeBackground("example.com/ffi.GoFunc", InGo)

	tests := []struct {
		name string
		typ  types.Type
		want Background
	}{
		{name: "named C function", typ: cFunc, want: InC},
		{name: "named Go function", typ: goFunc, want: InGo},
		{name: "alias to C function", typ: cAlias, want: InC},
		{name: "unregistered name is not inferred", typ: looksLikeC, want: inUnknown},
		{name: "unnamed signature", typ: sig, want: inUnknown},
		{name: "nil type", typ: nil, want: inUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := prog.TypeBackground(test.typ); got != test.want {
				t.Fatalf("TypeBackground(%v) = %v, want %v", test.typ, got, test.want)
			}
		})
	}
}

func TestNilProgramTypeBackgroundIsUnknown(t *testing.T) {
	var prog Program
	if got := prog.TypeBackground(types.Typ[types.Int]); got != inUnknown {
		t.Fatalf("TypeBackground on nil Program = %v, want %v", got, inUnknown)
	}
}
