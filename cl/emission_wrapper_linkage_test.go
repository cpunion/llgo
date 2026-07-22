//go:build !llgo

package cl

import (
	"go/ast"
	"go/types"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func emissionWrapperLinkageFixture(t *testing.T) (*EmissionUniverse, *ssa.Function, *preparedEmissionPackage) {
	t.Helper()
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/wrapperlinkage", `package wrapperlinkage
type Inner struct{}
func (Inner) M() {}
type Outer struct{ Inner }
func Value() any { return Outer{} }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	t.Cleanup(prog.Dispose)
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file},
	}})
	if err != nil {
		t.Fatal(err)
	}
	owner := universe.packages[pkg.ssa]
	for _, function := range universe.functions {
		if wrapperKind(function) == "promoted" && function.Name() == "M" && len(function.Blocks) != 0 {
			return universe, function, owner
		}
	}
	t.Fatal("promoted wrapper is absent")
	return nil, nil, nil
}

func TestEmissionUniverseFreezesPackageNamedProvenanceAcrossTypepatchClone(t *testing.T) {
	universe, original, _, dispose := preparePatchedEmissionTest(t, `package p
type Inner struct{}
func (Inner) M() {}
type Outer struct{ Inner }
func LocalValue() any {
	type Local struct{ Inner }
	return Local{}
}
`, `package p
type Inner struct{}
func (Inner) M() {}
type Outer struct{ Inner }
`)
	defer dispose()
	owner := universe.packages[original.ssa]
	object, ok := owner.pkgTypes.Scope().Lookup("Outer").(*types.TypeName)
	if !ok {
		t.Fatal("merged patch scope has no Outer type")
	}
	// typepatch.Clone/Merge installs a cloned package scope while preserving
	// the alternate declaration's canonical TypeName and parent scope.
	if object.Parent() == owner.pkgTypes.Scope() {
		t.Fatal("test setup did not produce the cloned-scope mismatch")
	}
	named, ok := types.Unalias(object.Type()).(*types.Named)
	if !ok {
		t.Fatalf("Outer type = %T; want named", object.Type())
	}
	owners, frozen := universe.frozenPackageNamedType(named)
	if !frozen || len(owners) != 1 || owners[0] != owner {
		t.Fatalf("cloned-scope Outer provenance = %v, %t; want exact declaring owner", owners, frozen)
	}

	localValue := original.ssa.Func("LocalValue")
	var local *types.Named
	for _, block := range localValue.Blocks {
		for _, instruction := range block.Instrs {
			makeInterface, ok := instruction.(*ssa.MakeInterface)
			if !ok {
				continue
			}
			candidate, ok := types.Unalias(makeInterface.X.Type()).(*types.Named)
			if ok && candidate.Obj() != nil && candidate.Obj().Name() == "Local" {
				local = candidate
			}
		}
	}
	if local == nil {
		t.Fatal("LocalValue SSA has no local named concrete type")
	}
	if _, frozen := universe.frozenPackageNamedType(local); frozen {
		t.Fatal("true function-local named type received package-level provenance")
	}
}

func addEquivalentWrapperEmissionCopy(
	t *testing.T,
	universe *EmissionUniverse,
	original *ssa.Function,
	originalOwner *preparedEmissionPackage,
) (*ssa.Function, *preparedEmissionPackage) {
	t.Helper()
	copyFunction := new(ssa.Function)
	*copyFunction = *original
	copyOwnerValue := *originalOwner
	copyOwnerValue.order++
	copyOwner := &copyOwnerValue
	originalKey := emissionFunctionOwnerKey{function: original, owner: originalOwner}
	copyKey := emissionFunctionOwnerKey{function: copyFunction, owner: copyOwner}
	universe.functions = append(universe.functions, copyFunction)
	universe.required[copyFunction] = none{}
	universe.useOwners[copyFunction] = map[*preparedEmissionPackage]none{copyOwner: {}}
	universe.ownerStates[copyFunction] = map[*preparedEmissionPackage]emissionFunctionState{
		copyOwner: universe.ownerStates[original][originalOwner],
	}
	universe.finalKeys[copyKey] = universe.finalKeys[originalKey]
	universe.physicalNames[copyKey] = universe.physicalNames[originalKey]
	return copyFunction, copyOwner
}

func TestEmissionUniverseLinkOnceGroupsDistinctEquivalentWrapperObjects(t *testing.T) {
	universe, original, owner := emissionWrapperLinkageFixture(t)
	if !universe.generatedWrapperDefinitionNeedsLinkOnce(original) {
		t.Fatal("generated wrapper must be linkonce across independently compiled package archives")
	}
	copyFunction, _ := addEquivalentWrapperEmissionCopy(t, universe, original, owner)
	if err := universe.validateGeneratedWrapperPhysicalCollisions(); err != nil {
		t.Fatal(err)
	}
	for _, function := range []*ssa.Function{original, copyFunction} {
		if !universe.generatedWrapperDefinitionNeedsLinkOnce(function) {
			t.Fatalf("equivalent wrapper object %p did not receive frozen linkonce linkage", function)
		}
		ctx := &context{emissionUniverse: universe, linkOnceFns: make(map[*ssa.Function]none)}
		if !ctx.needsLinkOnce(function) {
			t.Fatalf("codegen did not consume frozen linkonce linkage for wrapper object %p", function)
		}
	}
}

func TestEmissionUniverseRejectsConflictingWrapperObjectsWithOnePhysicalSymbol(t *testing.T) {
	universe, original, owner := emissionWrapperLinkageFixture(t)
	copyFunction, copyOwner := addEquivalentWrapperEmissionCopy(t, universe, original, owner)
	copyKey := emissionFunctionOwnerKey{function: copyFunction, owner: copyOwner}
	_, physical, _, ok := splitManagedSymbolKey(universe.finalKeys[copyKey])
	if !ok {
		t.Fatal("copied wrapper has no managed key")
	}
	universe.finalKeys[copyKey] = managedSymbolKey(goFunc, physical, "conflicting-callable-abi")
	if err := universe.validateGeneratedWrapperPhysicalCollisions(); err == nil {
		t.Fatal("conflicting wrapper bodies/ABIs sharing one physical symbol were accepted")
	}
}
