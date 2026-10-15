//go:build !llgo

package cl

import (
	"go/token"
	"go/types"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	llabi "github.com/xgo-dev/llgo/ssa/abi"
)

func TestEmissionABIDemandPhysicalDescriptorUsesSourceMethodSignature(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/methodsource", `package methodsource
type Box struct { F func(int) int }
func (b Box) Method() func(int) int { return b.F }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	source := pkg.types.Scope().Lookup("Box").Type()
	physical := prog.PhysicalType(source, llssa.InGo)
	if types.Identical(source, physical) {
		t.Fatal("fixture lacks distinct physical receiver")
	}
	seenDescriptor, seenMethod := false, false
	methodSignature := func(typ types.Type) types.Type {
		sig := typ.(*types.Signature)
		if sig.Recv() != nil {
			seenMethod = true
			recv := sig.Recv().Type()
			if pointer, ok := recv.(*types.Pointer); ok {
				recv = pointer.Elem()
			}
			if recv != source {
				t.Fatal("descriptor method signature retained physical receiver")
			}
		}
		return llabi.PublicType(prog.PhysicalType(typ, llssa.InGo))
	}
	if err := walkEmissionABITypeDemandEx(physical, nil, prog.SourceType, methodSignature, prog.ABITypeImplicitDescriptorTypes, func(typ types.Type) error {
		seenDescriptor = seenDescriptor || typ == physical
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !seenDescriptor || !seenMethod {
		t.Fatalf("physical descriptor=%v, source method=%v", seenDescriptor, seenMethod)
	}
}

func TestEmissionABIInterfaceMethodRestoresSourceResultBeforeLowering(t *testing.T) {
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	result := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	signature := types.NewSignatureType(nil, nil, nil, nil, types.NewTuple(types.NewVar(token.NoPos, nil, "", result)), false)
	source := types.NewInterfaceType([]*types.Func{types.NewFunc(token.NoPos, nil, "Method", signature)}, nil).Complete()
	physical := prog.PhysicalType(source, llssa.InGo)
	seen := false
	methodSignature := func(typ types.Type) types.Type {
		seen = true
		if typ != signature || typ.(*types.Signature).Results().At(0).Type() != result {
			t.Fatal("interface method signature retained its physical closure result")
		}
		return llabi.PublicType(prog.PhysicalType(typ, llssa.InGo))
	}
	if err := walkEmissionABITypeDemandEx(physical, nil, prog.SourceType, methodSignature, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("physical interface method was not traversed")
	}
}
