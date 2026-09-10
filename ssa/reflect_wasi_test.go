//go:build !llgo

package ssa

import (
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestWASIReflectCallBridge(t *testing.T) {
	Initialize(InitAllTargets | InitAllTargetInfos | InitAllTargetMCs)
	for _, nout := range []int{0, 1, 2} {
		t.Run(string(rune('0'+nout))+" results", func(t *testing.T) {
			prog := NewProgram(&Target{GOOS: "wasip1", GOARCH: "wasm"})
			defer prog.Dispose()
			setTestRuntime(t, prog)
			pkg := prog.NewPackage("p", "example.com/p")
			input := types.NewTuple(types.NewParam(token.NoPos, nil, "x", types.Typ[types.Int64]))
			outs := make([]*types.Var, nout)
			for i := range outs {
				outs[i] = types.NewParam(token.NoPos, nil, "", types.Typ[types.Float64])
			}
			sig := types.NewSignatureType(nil, nil, nil, input, types.NewTuple(outs...), false)
			fn := pkg.wasiReflectCallBridge(sig, "test.signature")
			if pkg.wasiReflectCallBridge(sig, "test.signature") != fn {
				t.Fatal("duplicate bridge for the same signature")
			}
			prog.EnableGCRoots(true)
			makeFn := pkg.wasiReflectMakeBridge(sig, "test.signature")
			if pkg.wasiReflectMakeBridge(sig, "test.signature") != makeFn {
				t.Fatal("duplicate MakeFunc entry")
			}
			if err := llvm.VerifyModule(pkg.mod, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			ir := pkg.String()
			for _, want := range []string{"comdat any", "linkonce_odr", "load i64", "br i1"} {
				if !strings.Contains(ir, want) {
					t.Errorf("missing %q in bridge:\n%s", want, ir)
				}
			}
			calls := map[int]int{}
			for block := fn.impl.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
				for inst := block.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
					if !inst.IsACallInst().IsNil() && inst.CalledValue().IsAFunction().IsNil() {
						calls[inst.CalledFunctionType().ParamTypesCount()]++
					}
				}
			}
			if calls[1] != 1 || calls[2] != 1 {
				t.Fatalf("call signatures = %v, want plain and env-bearing edges", calls)
			}
		})
	}
}
