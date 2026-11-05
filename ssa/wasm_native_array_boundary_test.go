package ssa

import (
	"go/token"
	"go/types"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestWasm32NativeArrayBoundaryConversion(t *testing.T) {
	for _, test := range []struct {
		name string
		elem types.Type
	}{
		{"uintptr", types.Typ[types.Uintptr]},
		{"pointer", types.NewPointer(types.Typ[types.Uint64])},
		{"nested-uintptr", types.NewArray(types.Typ[types.Uintptr], 2)},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog := newJ32Program(t)
			setTestRuntime(t, prog)
			pkg := prog.NewPackage("array", "example.com/nativearray")
			array := types.NewArray(test.elem, 2)
			param := types.NewParam(token.NoPos, nil, "value", array)
			sig := types.NewSignatureType(nil, nil, nil, types.NewTuple(param), types.NewTuple(param), false)
			callee := pkg.NewFunc("example.com/nativearray.native", sig, CDecl)
			cb := callee.MakeBody(1)
			cb.Return(cb.Param(0))
			cb.EndBuild()
			callerSig := types.NewSignatureType(nil, nil, nil, types.NewTuple(param), nil, false)
			caller := pkg.NewFunc("example.com/nativearray.caller", callerSig, GoDecl)
			b := caller.MakeBody(1)
			result := b.Call(callee.Expr, b.Param(0))
			b.Return()
			b.EndBuild()
			native := callee.impl.GlobalValueType().ParamTypes()[0]
			if got := result.impl.Type(); got != result.Type.ll {
				t.Errorf("native result was not converted to logical storage: %s, want %s", got, result.Type.ll)
			}
			for block := caller.impl.FirstBasicBlock(); !block.IsNil(); block = llvm.NextBasicBlock(block) {
				for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
					if call := instruction.IsACallInst(); !call.IsNil() && call.CalledValue() == callee.impl {
						if got := call.Operand(0).Type(); got != native {
							t.Errorf("native argument was not converted to physical storage: %s, want %s", got, native)
						}
					}
				}
			}
			if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
				t.Errorf("invalid native array call: %v", err)
			}
			if t.Failed() {
				t.Log(pkg.Module().String())
			}
		})
	}
}
