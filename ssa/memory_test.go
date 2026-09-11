package ssa

import (
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestAssertNilDerefZeroExprNoPanic(t *testing.T) {
	var b Builder
	b.AssertNilDeref(Expr{})
}

func TestPhiIncomingControlFlowTracksPredecessor(t *testing.T) {
	prog := NewProgram(&Target{
		GOOS:       "js",
		GOARCH:     "wasm",
		LLVMTarget: "wasm32-unknown-emscripten",
	})
	defer prog.Dispose()
	prog.SetRuntime(func() *types.Package {
		rt := types.NewPackage(PkgRuntime, "runtime")
		params := types.NewTuple(types.NewVar(token.NoPos, nil, "nil", types.Typ[types.Bool]))
		sig := types.NewSignatureType(nil, nil, nil, params, nil, false)
		rt.Scope().Insert(types.NewFunc(token.NoPos, rt, "AssertNilDeref", sig))
		return rt
	})

	empty := types.NewStruct(nil, nil)
	params := types.NewTuple(
		types.NewVar(token.NoPos, nil, "choose", types.Typ[types.Bool]),
		types.NewVar(token.NoPos, nil, "value", types.NewPointer(empty)),
	)
	results := types.NewTuple(types.NewVar(token.NoPos, nil, "", empty))
	sig := types.NewSignatureType(nil, nil, nil, params, results, false)
	pkg := prog.NewPackage("p", "example.com/phi")
	fn := pkg.NewFunc("selectValue", sig, InGo)
	b := fn.MakeBody(4)
	predWithCheck, predWithoutCheck, merge := fn.Block(1), fn.Block(2), fn.Block(3)
	b.If(fn.Param(0), predWithCheck, predWithoutCheck)
	b.SetBlock(predWithCheck).Jump(merge)
	b.SetBlock(predWithoutCheck).Jump(merge)
	b.SetBlock(merge)
	phi := b.Phi(prog.Type(empty, InGo))
	b.Return(phi.Expr)
	phi.AddIncoming(b, []BasicBlock{predWithCheck, predWithoutCheck}, func(i int, blk BasicBlock) Expr {
		b.SetBlockEx(blk, BeforeLast, false)
		if i == 0 {
			// A zero-sized load still requires a nil check. The check splits this
			// incoming edge, so the phi must use the resulting success block.
			return b.Load(fn.Param(1))
		}
		return prog.Zero(prog.Type(empty, InGo))
	})
	b.EndBuild()

	if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("phi did not follow the split incoming edge: %v\n%s", err, pkg.Module().String())
	}
}

func TestAssertNilDerefColdCall(t *testing.T) {
	Initialize(InitAllTargets | InitAllTargetInfos | InitAllTargetMCs | InitAllAsmPrinters)
	for _, target := range []*Target{
		{GOOS: "linux", GOARCH: "amd64", LLVMTarget: "x86_64-unknown-linux"},
		{GOOS: "js", GOARCH: "wasm", LLVMTarget: "wasm32-unknown-emscripten"},
		{GOOS: "js", GOARCH: "wasm", LLVMTarget: "wasm64-unknown-emscripten"},
		{GOOS: "wasip1", GOARCH: "wasm", LLVMTarget: "wasm32-unknown-wasip1"},
	} {
		t.Run(target.LLVMTarget, func(t *testing.T) {
			prog := NewProgram(target)
			defer prog.Dispose()
			prog.SetRuntime(func() *types.Package {
				rt := types.NewPackage(PkgRuntime, "runtime")
				params := types.NewTuple(types.NewVar(token.NoPos, nil, "nil", types.Typ[types.Bool]))
				sig := types.NewSignatureType(nil, nil, nil, params, nil, false)
				rt.Scope().Insert(types.NewFunc(token.NoPos, rt, "AssertNilDeref", sig))
				return rt
			})
			pkg := prog.NewPackage("p", "example.com/nilcheck")
			params := types.NewTuple(types.NewVar(token.NoPos, nil, "p", types.Typ[types.UnsafePointer]))
			sig := types.NewSignatureType(nil, nil, nil, params, nil, false)
			for _, name := range []string{"dynamic", "nil", "allocated", "constant"} {
				fn := pkg.NewFunc(name, sig, InGo)
				b := fn.MakeBody(1)
				ptr := fn.Param(0)
				switch name {
				case "nil":
					ptr = prog.Nil(prog.VoidPtr())
				case "allocated":
					allocator := pkg.NewFunc(PkgRuntime+".AllocU", prog.tyMalloc(), InGo)
					ptr = b.Call(allocator.Expr, prog.IntVal(8, prog.Uintptr()))
				case "constant":
					ptr = b.Convert(prog.VoidPtr(), prog.IntVal(1, prog.Uintptr()))
				}
				b.AssertNilDeref(ptr)
				b.Return()
				b.EndBuild()
				if name == "dynamic" {
					entry := fn.impl.EntryBasicBlock()
					branch := entry.LastInstruction()
					if branch.InstructionOpcode() != llvm.Br || branch.SuccessorsCount() != 2 {
						t.Fatalf("dynamic nil check must branch before calling runtime:\n%s", fn.impl.String())
					}
					for inst := entry.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
						if inst.InstructionOpcode() == llvm.Call {
							t.Fatalf("nil check calls runtime unconditionally:\n%s", fn.impl.String())
						}
					}
					failure := branch.Successor(0)
					back := failure.LastInstruction()
					if back.InstructionOpcode() != llvm.Br || back.SuccessorsCount() != 1 || back.Successor(0) != failure {
						t.Fatalf("nil failure must not return to the successful path:\n%s", fn.impl.String())
					}
				}
			}
			mod := pkg.Module()
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			opts := llvm.NewPassBuilderOptions()
			defer opts.Dispose()
			if err := mod.RunPasses("function(instcombine,simplifycfg)", prog.TargetMachine(), opts); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"dynamic", "nil", "allocated", "constant"} {
				ir := mod.NamedFunction(name).String()
				if got := strings.Contains(ir, "AssertNilDeref"); got != (name == "dynamic" || name == "nil") {
					t.Fatalf("unexpected optimized %s check:\n%s", name, ir)
				}
			}
			if err := llvm.VerifyModule(mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
		})
	}
}
