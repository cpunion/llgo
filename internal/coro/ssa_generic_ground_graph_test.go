//go:build !llgo

package coro

import (
	"go/token"
	"go/types"
	"testing"
)

func TestGroundTypeSharedGraph(t *testing.T) {
	// Only 65 type nodes, but a path-only recursion guard expands more than
	// 2^32 paths. Groundness is existential over nodes, not over all paths.
	var typ types.Type = types.Typ[types.Int]
	for level := 0; level < 32; level++ {
		pointer := types.NewPointer(typ)
		typ = types.NewStruct([]*types.Var{
			types.NewField(token.NoPos, nil, "Left", pointer, false),
			types.NewField(token.NoPos, nil, "Right", pointer, false),
		}, nil)
	}
	if TypeContainsUnresolvedTypeParam(typ, make(map[types.Type]bool)) {
		t.Fatal("ground shared graph was rejected")
	}
}

func TestGroundTypeGraphCyclesAndQueryIsolation(t *testing.T) {
	ground := types.NewNamed(types.NewTypeName(token.NoPos, nil, "Ground", nil), nil, nil)
	ground.SetUnderlying(types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "Next", types.NewPointer(ground), false),
	}, nil))
	parameter := types.NewTypeParam(types.NewTypeName(token.NoPos, nil, "T", nil), types.NewInterfaceType(nil, nil).Complete())
	unresolved := types.NewNamed(types.NewTypeName(token.NoPos, nil, "Unresolved", nil), nil, nil)
	unresolved.SetUnderlying(types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "Cycle", types.NewPointer(unresolved), false),
		types.NewField(token.NoPos, nil, "Shared", types.NewPointer(ground), false),
		types.NewField(token.NoPos, nil, "Value", parameter, false),
	}, nil))
	seed := map[types.Type]bool{types.Typ[types.String]: true}
	for round := 0; round < 3; round++ {
		if TypeContainsUnresolvedTypeParam(ground, seed) {
			t.Fatal("ground cycle rejected")
		}
		if !TypeContainsUnresolvedTypeParam(unresolved, seed) {
			t.Fatal("type parameter behind cycle omitted")
		}
		if !TypeContainsUnresolvedTypeParam(types.NewSlice(parameter), seed) {
			t.Fatal("reused query state hid a type parameter")
		}
		if len(seed) != 1 || !seed[types.Typ[types.String]] {
			t.Fatal("query mutated caller seed")
		}
	}
	// Caller-provided active nodes keep their original cycle-cut meaning.
	if TypeContainsUnresolvedTypeParam(unresolved, map[types.Type]bool{unresolved: true}) {
		t.Fatal("caller active-node seed ignored")
	}
}
