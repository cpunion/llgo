//go:build !llgo

package ssa

import (
	"fmt"
	"go/types"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestWasmTypedLocalZero(t *testing.T) {
	Initialize(InitAllTargets | InitAllTargetInfos | InitAllTargetMCs | InitAllAsmPrinters)
	structOf := func(fields ...types.Type) *types.Struct {
		vars := make([]*types.Var, len(fields))
		for i, typ := range fields {
			vars[i] = types.NewVar(0, nil, fmt.Sprintf("F%d", i), typ)
		}
		return types.NewStruct(vars, nil)
	}
	padded := structOf(types.Typ[types.Byte], types.Typ[types.Int32])
	cases := []struct {
		name string
		typ  types.Type
		want bool
	}{
		{"float", types.Typ[types.Float64], true},
		{"complex", types.Typ[types.Complex128], true},
		{"table_row", structOf(types.Typ[types.Complex128], types.Typ[types.Complex128], types.Typ[types.Complex128]), true},
		{"pointer", types.NewPointer(types.Typ[types.Int]), true},
		{"array", types.NewArray(types.Typ[types.Float32], 8), true},
		{"padded", padded, false},
		{"nested_padding", structOf(padded), false},
		{"large", types.NewArray(types.Typ[types.Complex128], 5), false},
		{"bool", types.Typ[types.Bool], false},
		{"empty", types.NewStruct(nil, nil), false},
	}
	for _, target := range []*Target{
		{GOOS: "wasip1", GOARCH: "wasm"},
		{GOOS: "js", GOARCH: "wasm", LLVMTarget: "wasm64-unknown-emscripten"},
		{GOOS: "linux", GOARCH: "amd64"},
	} {
		for _, tc := range cases {
			t.Run(target.GOARCH+"/"+target.LLVMTarget+"/"+tc.name, func(t *testing.T) {
				prog := NewProgram(target)
				defer prog.Dispose()
				pkg := prog.NewPackage("zero", "example.com/zero")
				fn := pkg.NewFunc("loop", NoArgsNoRet, InGo)
				body := fn.MakeBody(2)
				defer body.Dispose()
				body.Jump(fn.Block(1))
				body.SetBlock(fn.Block(1))
				typ := prog.Type(tc.typ, InGo)
				local := body.Alloc(typ, false)
				body.Jump(fn.Block(1))
				body.EndBuild()
				stores := 0
				for block := fn.impl.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
					for inst := block.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
						if inst.IsAStoreInst().IsNil() || inst.Operand(1) != local.impl {
							continue
						}
						stores++
						if block != fn.Block(1).first || inst.Operand(0).Type() != typ.ll || !inst.Operand(0).IsNull() {
							t.Fatalf("local zero must retain its type and execute on each iteration:\n%s", pkg.String())
						}
					}
				}
				want := 0
				if target.GOARCH == "wasm" && tc.want {
					want = 1
				}
				if stores != want {
					t.Fatalf("typed zero stores = %d, want %d:\n%s", stores, want, pkg.String())
				}
				if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestWasmTypedLocalZeroTypeBudget(t *testing.T) {
	prog := NewProgram(&Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	ctx := prog.ctx
	packed := ctx.StructType([]llvm.Type{ctx.Int8Type(), ctx.Int32Type()}, true)
	implicitPadding := ctx.StructType([]llvm.Type{ctx.Int8Type(), ctx.Int32Type()}, false)
	tailPadding := ctx.StructType([]llvm.Type{ctx.Int32Type(), ctx.Int8Type()}, false)
	explicitPadding := ctx.StructType([]llvm.Type{ctx.Int8Type(), llvm.ArrayType(ctx.Int8Type(), 3), ctx.Int32Type()}, false)
	cases := []struct {
		name   string
		typ    llvm.Type
		budget int
		want   bool
	}{
		{"empty_budget", ctx.Int8Type(), 0, false},
		{"integer", ctx.Int32Type(), 1, true},
		{"sub_byte", ctx.Int1Type(), 1, false},
		{"padded_integer", ctx.IntType(24), 1, false},
		{"linear_pointer", llvm.PointerType(ctx.Int8Type(), 0), 1, true},
		{"nonintegral_pointer", llvm.PointerType(ctx.Int8Type(), 1), 1, false},
		{"packed", packed, 3, true},
		{"explicit_padding", explicitPadding, 8, true},
		{"implicit_padding", implicitPadding, 3, false},
		{"tail_padding", tailPadding, 3, false},
		{"struct_budget", packed, 2, false},
		{"nested_budget", ctx.StructType([]llvm.Type{packed}, false), 3, false},
		{"array_budget", llvm.ArrayType(ctx.Int8Type(), 32), 32, false},
		{"array_padding", llvm.ArrayType(implicitPadding, 2), 10, false},
		{"array_nested_budget", llvm.ArrayType(packed, 2), 6, false},
		{"empty_array", llvm.ArrayType(ctx.Int32Type(), 0), 1, true},
		{"vector", llvm.VectorType(ctx.Int32Type(), 2), 3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			budget := tc.budget
			if got := denseLocalZeroType(prog.td, tc.typ, &budget); got != tc.want {
				t.Fatalf("denseLocalZeroType = %v, want %v", got, tc.want)
			}
		})
	}
}
