//go:build !llgo

package ssa

import (
	"go/importer"
	"go/token"
	"go/types"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestWasmUnsafeRuntimeErrorGuards(t *testing.T) {
	Initialize(InitAllTargets | InitAllTargetInfos | InitAllTargetMCs | InitAllAsmPrinters)
	for _, target := range []*Target{
		{GOOS: "wasip1", GOARCH: "wasm"},
		{GOOS: "js", GOARCH: "wasm", LLVMTarget: "wasm64-unknown-emscripten"},
		{GOOS: "linux", GOARCH: "amd64"},
	} {
		for _, mode := range []string{"dynamic", "false", "true"} {
			t.Run(target.GOOS+"/"+target.LLVMTarget+"/"+mode, func(t *testing.T) {
				prog := NewProgram(target)
				defer prog.Dispose()
				prog.SetRuntime(func() *types.Package {
					pkg, err := importer.For("source", nil).Import(PkgRuntime)
					if err != nil {
						t.Fatal(err)
					}
					return pkg
				})
				pkg := prog.NewPackage("guard", "example.com/guard")
				params := types.NewTuple(types.NewVar(token.NoPos, nil, "invalid", types.Typ[types.Bool]))
				fn := pkg.NewFunc("guard", types.NewSignatureType(nil, nil, nil, params, nil, false), InGo)
				b := fn.MakeBody(1)
				check := fn.Param(0)
				if mode != "dynamic" {
					check = prog.BoolVal(mode == "true")
				}
				b.assertRuntimeError(check.impl, "unsafe.String: len out of range")
				b.Return()
				b.EndBuild()
				if err := llvm.VerifyModule(pkg.Module(), llvm.ReturnStatusAction); err != nil {
					t.Fatal(err)
				}
				calls := 0
				entry := fn.impl.FirstBasicBlock()
				for block := entry; !block.IsNil(); block = llvm.NextBasicBlock(block) {
					for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
						if call := instruction.IsACallInst(); !call.IsNil() {
							calls++
							if call.CalledValue().Name() != PkgRuntime+".AssertRuntimeError" {
								t.Fatalf("unexpected helper %s", call.CalledValue().Name())
							}
							if target.GOARCH == "wasm" && (block == entry || !call.Operand(0).IsConstant() || call.Operand(0).IsNull()) {
								t.Fatalf("successful path calls a runtime helper:\n%s", fn.impl.String())
							}
						}
					}
				}
				want := 1
				if target.GOARCH == "wasm" && mode == "false" {
					want = 0
				}
				if calls != want {
					t.Fatalf("calls=%d, want %d", calls, want)
				}
				if target.GOARCH == "wasm" && mode != "false" && fn.Block(0).last == entry {
					t.Fatal("logical block tail was not updated after splitting the check")
				}
			})
		}
	}
}
