package ssa

import (
	"go/token"
	"go/types"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestSliceChecksSplitPhiPredecessor(t *testing.T) {
	Initialize(InitAllTargets | InitAllTargetInfos | InitAllTargetMCs | InitAllAsmPrinters)
	for _, target := range []*Target{
		{GOOS: "linux", GOARCH: "amd64", LLVMTarget: "x86_64-unknown-linux"},
		{GOOS: "darwin", GOARCH: "arm64", LLVMTarget: "aarch64-apple-darwin"},
		{GOOS: "windows", GOARCH: "amd64", LLVMTarget: "x86_64-pc-windows-msvc"},
		{GOOS: "windows", GOARCH: "386", LLVMTarget: "i686-pc-windows-msvc"},
		{GOOS: "windows", GOARCH: "arm64", LLVMTarget: "aarch64-pc-windows-msvc"},
		{GOOS: "js", GOARCH: "wasm", LLVMTarget: "wasm32-unknown-emscripten"},
		{GOOS: "js", GOARCH: "wasm", LLVMTarget: "wasm64-unknown-emscripten"},
		{GOOS: "wasip1", GOARCH: "wasm", LLVMTarget: "wasm32-unknown-wasip1"},
	} {
		t.Run(target.LLVMTarget, func(t *testing.T) {
			prog := NewProgram(target)
			defer prog.Dispose()
			prog.SetRuntime(func() *types.Package {
				rt := types.NewPackage(PkgRuntime, "runtime")
				fields := []*types.Var{
					types.NewField(0, rt, "data", types.Typ[types.UnsafePointer], false),
					types.NewField(0, rt, "len", types.Typ[types.Int], false),
				}
				str := types.NewTypeName(0, rt, "String", nil)
				types.NewNamed(str, types.NewStruct(fields, nil), nil)
				rt.Scope().Insert(str)
				params := make([]*types.Var, 4)
				for i, typ := range []types.Type{types.Typ[types.Int64], types.Typ[types.Bool], types.Typ[types.Int], types.Typ[types.Uint8]} {
					params[i] = types.NewVar(token.NoPos, nil, "", typ)
				}
				sig := types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), nil, false)
				rt.Scope().Insert(types.NewFunc(token.NoPos, rt, "PanicBounds", sig))
				return rt
			})
			pkg := prog.NewPackage("p", "example.com/slice-phi")
			params := types.NewTuple(
				types.NewVar(0, nil, "choose", types.Typ[types.Bool]),
				types.NewVar(0, nil, "s", types.Typ[types.String]),
				types.NewVar(0, nil, "low", types.Typ[types.Uint64]),
				types.NewVar(0, nil, "high", types.Typ[types.Int64]),
			)
			results := types.NewTuple(types.NewVar(0, nil, "", types.Typ[types.String]))
			sig := types.NewSignatureType(nil, nil, nil, params, results, false)
			fn := pkg.NewFunc("chooseSlice", sig, GoDecl)
			b := fn.MakeBody(4)
			left, right, merge := fn.Block(1), fn.Block(2), fn.Block(3)
			b.If(fn.Param(0), left, right)
			b.SetBlock(left).Jump(merge)
			b.SetBlock(right).Jump(merge)
			b.SetBlock(merge)
			phi := b.Phi(prog.String())
			b.Return(phi.Expr)
			phi.AddIncoming(b, []BasicBlock{left, right}, func(i int, blk BasicBlock) Expr {
				b.SetBlockEx(blk, BeforeLast, false)
				if i == 0 {
					return b.Slice(fn.Param(1), fn.Param(2), fn.Param(3), Expr{})
				}
				return fn.Param(1)
			})
			b.EndBuild()
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Fatalf("invalid split predecessor: %v\n%s", err, pkg.String())
			}
			obj, err := prog.TargetMachine().EmitToMemoryBuffer(pkg.Module(), llvm.ObjectFile)
			if err != nil {
				t.Fatal(err)
			}
			obj.Dispose()
		})
	}
}
