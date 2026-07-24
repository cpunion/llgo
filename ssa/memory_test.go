package ssa

import (
	"go/importer"
	"go/types"
	"runtime"
	"strings"
	"testing"
)

func TestAssertNilDerefZeroExprNoPanic(t *testing.T) {
	var b Builder
	b.AssertNilDeref(Expr{})
}

func TestLoadKnownNonNilZeroSizedSkipsNilDerefGuard(t *testing.T) {
	prog := NewProgram(nil)
	prog.sizes = types.SizesFor("gc", runtime.GOARCH)
	prog.SetRuntime(func() *types.Package {
		pkg, err := importer.For("source", nil).Import(PkgRuntime)
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	})
	pkg := prog.NewPackage("memory", "test/memory")
	empty := types.NewStruct(nil, nil)
	params := types.NewTuple(types.NewVar(0, nil, "p", types.NewPointer(empty)))
	results := types.NewTuple(types.NewVar(0, nil, "", empty))
	sig := types.NewSignatureType(nil, nil, nil, params, results, false)

	build := func(name string, knownNonNil bool) string {
		fn := pkg.NewFunc(name, sig, InGo)
		b := fn.MakeBody(1)
		if knownNonNil {
			b.Return(b.LoadKnownNonNil(fn.Param(0)))
		} else {
			b.Return(b.Load(fn.Param(0)))
		}
		b.EndBuild()
		return fn.impl.String()
	}

	knownBody := build("loadKnownNonNilZero", true)
	if strings.Contains(knownBody, "AssertNilDeref") {
		t.Fatalf("known-non-nil zero-sized load emitted nil-deref helper:\n%s", knownBody)
	}

	ordinaryBody := build("loadZero", false)
	if !strings.Contains(ordinaryBody, "AssertNilDeref") {
		t.Fatalf("ordinary zero-sized load lost nil-deref helper:\n%s", ordinaryBody)
	}
}

func TestFieldAddrKnownNonNilSkipsStaticNilDerefGuard(t *testing.T) {
	prog := NewProgram(nil)
	prog.sizes = types.SizesFor("gc", runtime.GOARCH)
	prog.SetRuntime(func() *types.Package {
		pkg, err := importer.For("source", nil).Import(PkgRuntime)
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	})
	pkg := prog.NewPackage("memory", "test/memory")
	structure := types.NewStruct([]*types.Var{
		types.NewField(0, nil, "Value", types.Typ[types.Int], false),
	}, nil)
	resultType := types.NewPointer(types.Typ[types.Int])
	results := types.NewTuple(types.NewVar(0, nil, "", resultType))
	sig := types.NewSignatureType(nil, nil, nil, nil, results, false)

	build := func(name string, knownNonNil bool) string {
		fn := pkg.NewFunc(name, sig, InGo)
		b := fn.MakeBody(1)
		base := prog.Nil(prog.rawType(types.NewPointer(structure)))
		if knownNonNil {
			b.Return(b.FieldAddrKnownNonNil(base, 0))
		} else {
			b.Return(b.FieldAddr(base, 0))
		}
		b.EndBuild()
		return fn.impl.String()
	}

	knownBody := build("fieldAddrKnownNonNil", true)
	if strings.Contains(knownBody, "AssertNilDeref") {
		t.Fatalf("known-non-nil field address emitted nil-deref helper:\n%s", knownBody)
	}
	ordinaryBody := build("fieldAddrOrdinary", false)
	if !strings.Contains(ordinaryBody, "AssertNilDeref") {
		t.Fatalf("ordinary static-nil field address lost nil-deref helper:\n%s", ordinaryBody)
	}
}
