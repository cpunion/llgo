//go:build !llgo

package ssa

import (
	"go/token"
	"go/types"
	"runtime"
	"strings"
	"testing"
)

func TestTypePointerFree(t *testing.T) {
	podStruct := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "a", types.Typ[types.Int64], false),
		types.NewField(token.NoPos, nil, "b", types.NewArray(types.Typ[types.Byte], 16), false),
		types.NewField(token.NoPos, nil, "c", types.Typ[types.Bool], false),
		types.NewField(token.NoPos, nil, "d", types.Typ[types.Float64], false),
		types.NewField(token.NoPos, nil, "e", types.Typ[types.Complex128], false),
	}, nil)
	ptrStruct := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "a", types.Typ[types.Int64], false),
		types.NewField(token.NoPos, nil, "p", types.NewPointer(types.Typ[types.Int]), false),
	}, nil)
	strStruct := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "s", types.Typ[types.String], false),
	}, nil)
	uintPtrStruct := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "u", types.Typ[types.Uintptr], false),
	}, nil)

	cases := []struct {
		name string
		typ  types.Type
		want bool
	}{
		{"nil", nil, false},
		{"bool", types.Typ[types.Bool], true},
		{"byte", types.Typ[types.Byte], true},
		{"int", types.Typ[types.Int], true},
		{"int64", types.Typ[types.Int64], true},
		{"uint64", types.Typ[types.Uint64], true},
		{"float32", types.Typ[types.Float32], true},
		{"complex64", types.Typ[types.Complex64], true},
		{"array-byte", types.NewArray(types.Typ[types.Byte], 64), true},
		{"pod-struct", podStruct, true},
		{"string", types.Typ[types.String], false},
		{"unsafe-pointer", types.Typ[types.UnsafePointer], false},
		{"uintptr", types.Typ[types.Uintptr], false},
		{"pointer", types.NewPointer(types.Typ[types.Byte]), false},
		{"slice-byte", types.NewSlice(types.Typ[types.Byte]), false},
		{"map", types.NewMap(types.Typ[types.Int], types.Typ[types.Int]), false},
		{"chan", types.NewChan(types.SendRecv, types.Typ[types.Int]), false},
		{"interface", types.NewInterfaceType(nil, nil).Complete(), false},
		{"func", types.NewSignatureType(nil, nil, nil, nil, nil, false), false},
		{"array-ptr", types.NewArray(types.NewPointer(types.Typ[types.Int]), 4), false},
		{"struct-with-ptr", ptrStruct, false},
		{"struct-with-string", strStruct, false},
		{"struct-with-uintptr", uintPtrStruct, false},
	}

	for _, tc := range cases {
		if got := TypePointerFree(tc.typ); got != tc.want {
			t.Errorf("TypePointerFree(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}

	if got := AllocZHelperName(podStruct); got != "AllocAtomicZ" {
		t.Fatalf("AllocZHelperName(podStruct) = %q, want AllocAtomicZ", got)
	}
	if got := AllocZHelperName(ptrStruct); got != "AllocZ" {
		t.Fatalf("AllocZHelperName(ptrStruct) = %q, want AllocZ", got)
	}
	if got := AllocUHelperName(podStruct); got != "AllocAtomicU" {
		t.Fatalf("AllocUHelperName(podStruct) = %q, want AllocAtomicU", got)
	}
	if got := AllocUHelperName(ptrStruct); got != "AllocU" {
		t.Fatalf("AllocUHelperName(ptrStruct) = %q, want AllocU", got)
	}
	if got := MakeSliceHelperName(types.NewSlice(types.Typ[types.Byte])); got != "MakeSliceAtomic" {
		t.Fatalf("MakeSliceHelperName([]byte) = %q, want MakeSliceAtomic", got)
	}
	if got := MakeSliceHelperName(types.NewSlice(types.Typ[types.String])); got != "MakeSlice" {
		t.Fatalf("MakeSliceHelperName([]string) = %q, want MakeSlice", got)
	}
	if got := SliceAppendHelperName(types.NewSlice(types.Typ[types.Int64])); got != "SliceAppendAtomic" {
		t.Fatalf("SliceAppendHelperName([]int64) = %q, want SliceAppendAtomic", got)
	}
	if got := SliceAppendHelperName(types.NewSlice(types.NewPointer(types.Typ[types.Int]))); got != "SliceAppend" {
		t.Fatalf("SliceAppendHelperName([]*int) = %q, want SliceAppend", got)
	}
}

func TestAtomicNoscanAllocationIR(t *testing.T) {
	prog := NewProgram(nil)
	defer prog.Dispose()
	prog.sizes = types.SizesFor("gc", runtime.GOARCH)
	prog.SetRuntime(RuntimeTypesForTest(t))

	pkg := prog.NewPackage("main", "main")
	b := pkg.NewFunc("allocs", NoArgsNoRet, InGo).MakeBody(1)

	podStruct := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "x", types.Typ[types.Int64], false),
		types.NewField(token.NoPos, nil, "y", types.Typ[types.Uint64], false),
	}, nil)
	ptrStruct := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "p", types.NewPointer(types.Typ[types.Int]), false),
		types.NewField(token.NoPos, nil, "n", types.Typ[types.Int64], false),
	}, nil)

	b.Alloc(prog.Type(podStruct, InGo), true)
	b.Alloc(prog.Type(ptrStruct, InGo), true)
	anyT := prog.Type(types.NewInterfaceType(nil, nil).Complete(), InGo)
	b.MakeInterface(anyT, prog.Zero(prog.Type(podStruct, InGo)))
	b.MakeInterface(anyT, prog.Zero(prog.Type(ptrStruct, InGo)))
	byteArr := b.Alloc(prog.Type(types.NewArray(types.Typ[types.Byte], 16), InGo), true)
	b.IndexAddr(byteArr, prog.IntVal(0, prog.Int()))

	byteSliceT := prog.Type(types.NewSlice(types.Typ[types.Byte]), InGo)
	ptrSliceT := prog.Type(types.NewSlice(types.NewPointer(types.Typ[types.Int])), InGo)
	n := prog.IntVal(16, prog.Int())
	bs := b.MakeSlice(byteSliceT, n, n)
	ps := b.MakeSlice(ptrSliceT, n, n)
	b.BuiltinCall("append", bs, bs)
	b.BuiltinCall("append", ps, ps)
	b.Return()
	b.EndBuild()

	ir := pkg.String()
	for _, want := range []string{
		`runtime.AllocAtomicZ"`,
		`runtime.AllocZ"`,
		`runtime.AllocAtomicU"`,
		`runtime.AllocU"`,
		`runtime.MakeSliceAtomic"`,
		`runtime.MakeSlice"`,
		`runtime.SliceAppendAtomic"`,
		`runtime.SliceAppend"`,
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("expected IR to contain %s:\n%s", want, ir)
		}
	}

	if strings.Contains(ir, "AssertNilDeref") {
		t.Fatalf("AllocAtomicZ array base unexpectedly emitted AssertNilDeref:\n%s", ir)
	}

	// Verify SetAllowRuntimeHelper gate cleanly falls back to pointer-scanned helpers.
	fallbackPkg := prog.NewPackage("fallback", "fallback")
	fallbackPkg.SetAllowRuntimeHelper(func(name string) bool {
		return !strings.Contains(name, "Atomic")
	})
	fb := fallbackPkg.NewFunc("allocs", NoArgsNoRet, InGo).MakeBody(1)
	fb.Alloc(prog.Type(podStruct, InGo), true)
	fb.MakeInterface(anyT, prog.Zero(prog.Type(podStruct, InGo)))
	fbs := fb.MakeSlice(byteSliceT, n, n)
	fb.BuiltinCall("append", fbs, fbs)
	fb.Return()
	fb.EndBuild()

	fir := fallbackPkg.String()
	for _, forbidden := range []string{"AllocAtomicZ", "AllocAtomicU", "MakeSliceAtomic", "SliceAppendAtomic"} {
		if strings.Contains(fir, forbidden) {
			t.Fatalf("fallback IR unexpectedly contained %s:\n%s", forbidden, fir)
		}
	}
	for _, want := range []string{`runtime.AllocZ"`, `runtime.AllocU"`, `runtime.MakeSlice"`, `runtime.SliceAppend"`} {
		if !strings.Contains(fir, want) {
			t.Fatalf("fallback IR missing %s:\n%s", want, fir)
		}
	}
}
