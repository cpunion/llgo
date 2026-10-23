//go:build !llgo

package ssa

import (
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestLocalAggregateEntryStoragePreservesBranchZeroing(t *testing.T) {
	for _, target := range []*Target{nil, {GOOS: "wasip1", GOARCH: "wasm"}} {
		prog := NewProgram(target)
		defer prog.Dispose()
		prog.SetRuntime(RuntimeTypesForTest(t))
		pkg := prog.NewPackage("entrylocal", "test/entrylocal")
		word := types.Typ[types.Uintptr]
		sig := types.NewSignatureType(nil, nil, nil,
			types.NewTuple(types.NewVar(0, nil, "branch", types.Typ[types.Bool]), types.NewVar(0, nil, "value", word)),
			types.NewTuple(types.NewVar(0, nil, "", word)), false)
		fn := pkg.NewFunc("branchLocal", sig, InGo)
		b := fn.MakeBody(3)
		defer b.Dispose()
		b.If(fn.Param(0), fn.Block(1), fn.Block(2))
		b.SetBlock(fn.Block(1))
		aggregate := prog.Struct(prog.Uintptr(), prog.Uintptr())
		local := b.Alloc(aggregate, false)
		if local.impl.InstructionParent() != fn.impl.EntryBasicBlock() {
			t.Fatalf("local allocation is outside entry:\n%s", fn.impl.String())
		}
		zeroAtSource := false
		for inst := fn.Block(1).first.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
			if strings.Contains(inst.String(), "llvm.memset") {
				zeroAtSource = true
			}
		}
		if !zeroAtSource {
			t.Fatal("local zero initialization moved away from its source instruction")
		}
		b.StoreKnownNonNil(b.FieldAddrKnownNonNil(local, 0), fn.Param(1))
		b.Return(b.LoadKnownNonNil(b.FieldAddrKnownNonNil(local, 1)))
		b.SetBlock(fn.Block(2))
		b.Return(fn.Param(1))
		b.EndBuild()
		if err := llvm.VerifyModule(pkg.mod, llvm.ReturnStatusAction); err != nil {
			t.Fatal(err, pkg.String())
		}
		options := llvm.NewPassBuilderOptions()
		err := pkg.mod.RunPasses("function(sroa,instcombine)", prog.TargetMachine(), options)
		options.Dispose()
		if err != nil {
			t.Fatal(err)
		}
		body := fn.impl.String()
		if strings.Contains(body, "alloca ") || strings.Contains(body, "llvm.memset") {
			t.Fatalf("branch local remained a dynamic temporary after scalar replacement:\n%s", body)
		}
	}
}

func TestExplicitAndHeapAllocationsRemainAtSource(t *testing.T) {
	prog := NewProgram(nil)
	defer prog.Dispose()
	prog.SetRuntime(RuntimeTypesForTest(t))
	pkg := prog.NewPackage("entrylocal", "test/entrylocal")
	fn := pkg.NewFunc("explicitStorage", types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewVar(0, nil, "size", types.Typ[types.Uintptr])), nil, false), InGo)
	b := fn.MakeBody(2)
	defer b.Dispose()
	b.Jump(fn.Block(1))
	b.SetBlock(fn.Block(1))
	for _, allocation := range []Expr{b.Alloca(fn.Param(0)), b.AllocaT(prog.Uintptr()), b.Alloc(prog.Uintptr(), true)} {
		if allocation.impl.InstructionParent() != fn.Block(1).first {
			t.Fatal("explicit or heap allocation moved away from its source instruction")
		}
	}
	b.Return()
	b.EndBuild()
	if err := llvm.VerifyModule(pkg.mod, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
}

func TestLocalEntryStorageHonorsFunctionBudget(t *testing.T) {
	prog := NewProgram(nil)
	defer prog.Dispose()
	prog.SetRuntime(RuntimeTypesForTest(t))
	pkg := prog.NewPackage("entrylocal", "test/entrylocal")
	fn := pkg.NewFunc("boundedStorage", NoArgsNoRet, InGo)
	b := fn.MakeBody(2)
	defer b.Dispose()
	b.Jump(fn.Block(1))
	b.SetBlock(fn.Block(1))
	large := prog.Type(types.NewArray(types.Typ[types.Byte], 65536), InGo)
	for range 2 {
		if allocation := b.Alloc(large, false); allocation.impl.InstructionParent() != fn.impl.EntryBasicBlock() {
			t.Fatal("local within the bounded entry budget was not hoisted")
		}
	}
	if allocation := b.Alloc(prog.Uintptr(), false); allocation.impl.InstructionParent() != fn.Block(1).first {
		t.Fatal("entry budget overflow did not retain source-position allocation")
	}
	b.Return()
	b.EndBuild()
	if err := llvm.VerifyModule(pkg.mod, llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
}
