//go:build !llgo

package cl

import (
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestCoroFrozenFrontendProjectionsDoNotRediscoverBuilderMetadata(t *testing.T) {
	pkg, _, files := buildGoSSAPkg(t, `package foo
import _ "unsafe"
//go:linkname Enabled llgo.coroExecutionDomainEnabled
func Enabled() bool
type Value int
func (v Value) Method() int { return int(v) }
type I interface { Method() int }
func Box(v Value) any { return v }
func Root(i I) int { if Enabled() { return i.Method() }; return 0 }
`)
	program := newLLSSAProg(t)
	defer program.Dispose()
	universe, err := prepareStacklessEmissionUniverse(program, nil, []EmissionPackage{{SSA: pkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ir := universe.coroProgramIR
	classification := ir.callableClassifications[pkg.Func("Enabled")]
	if !classification.intrinsic || classification.opcode != llgoCoroExecutionDomainEnabled {
		t.Fatalf("predicate classification = %+v", classification)
	}
	var invoke ssa.CallInstruction
	for _, block := range pkg.Func("Root").Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(ssa.CallInstruction); ok && call.Common().IsInvoke() {
				invoke = call
			}
		}
	}
	if invoke == nil {
		t.Fatal("fixture has no interface invoke")
	}
	key, err := ir.coroManagedInterfaceInvokeMethodKey(pkg.Func("Root"), invoke)
	if err != nil || key == "" {
		t.Fatalf("frozen method key = %q, %v", key, err)
	}
	references := ir.managedValueReferences[pkg.Func("Box")]
	if len(references) == 0 {
		t.Fatal("Box has no frozen method publications")
	}
	// These are builder-side stores, deliberately corrupted after freeze. The
	// physical consumers must use the same immutable facts, not classify again.
	universe.functionKinds = nil
	universe.intrinsicOps = nil
	universe.managedValueReferences = nil
	universe.emissionTypeKeys = emissionTypeKeyCache{}
	if got := ir.callableClassifications[pkg.Func("Enabled")]; got != classification {
		t.Fatalf("callable classification changed after freeze: %+v", got)
	}
	if got, err := ir.coroManagedInterfaceInvokeMethodKey(pkg.Func("Root"), invoke); err != nil || got != key {
		t.Fatalf("method key rediscovered mutable metadata: %q, %v", got, err)
	}
	if _, err := ir.coroManagedInterfaceInvokeMethodKey(pkg.Func("Box"), invoke); err == nil {
		t.Fatal("method key accepted the wrong SSA owner")
	}
	for _, source := range references {
		if source.Signature == nil || source.Signature.Recv() == nil {
			continue
		}
		if target, err := ir.resolveCoroManagedABIMethodTarget(source); err != nil || target != source {
			t.Fatalf("ordinary receiver target = %v, %v; want %v", target, err, source)
		}
	}
	if _, err := newCoroProgramIR().resolveCoroManagedABIMethodTarget(references[0]); err == nil {
		t.Fatal("missing managed method projection did not fail closed")
	}
}

func TestCoroWorkerOpaquePointerFacadeNeedsNoGlobalClassification(t *testing.T) {
	pointer := types.NewPointer(types.Typ[types.Int])
	namedPointer := types.NewNamed(types.NewTypeName(token.NoPos, nil, "Pointer", nil), pointer, nil)
	for _, typ := range []types.Type{pointer, namedPointer, types.NewPointer(types.Typ[types.String]), types.Typ[types.UnsafePointer]} {
		if !coroWorkerForeignOpaquePointerFacadeCompatible(pointer, typ) ||
			!coroWorkerForeignOpaquePointerFacadeCompatible(typ, pointer) {
			t.Fatalf("opaque pointer facade rejected %v", typ)
		}
	}
	for _, typ := range []types.Type{nil, types.Typ[types.Uintptr], types.NewMap(types.Typ[types.Int], types.Typ[types.Int]),
		types.NewSignatureType(nil, nil, nil, nil, nil, false), types.NewStruct(nil, nil)} {
		if coroWorkerForeignOpaquePointerFacadeCompatible(pointer, typ) ||
			coroWorkerForeignOpaquePointerFacadeCompatible(typ, pointer) {
			t.Fatalf("opaque pointer facade admitted non-pointer %v", typ)
		}
	}
}
