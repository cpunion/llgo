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

func TestEmissionUniverseCoalescesEquivalentNilCheckThunks(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "main", `package main
type S string
type T string
func (S) val() int { return 1 }
func (T) val() int { return 2 }
func Use() int {
	var s S
	var value T
	method := (*S).val
	other := (*T).val
	return method(&s) + other(&value)
}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var thunk, other *ssa.Function
	for _, function := range universe.Functions() {
		params := function.Signature.Params()
		if wrapperKind(function) != "thunk" || function.Name() != "val$thunk" || params == nil || params.Len() == 0 {
			continue
		}
		pointer, ok := types.Unalias(params.At(0).Type()).(*types.Pointer)
		if !ok {
			continue
		}
		named, ok := types.Unalias(pointer.Elem()).(*types.Named)
		if !ok || named.Obj() == nil {
			continue
		}
		switch named.Obj().Name() {
		case "S":
			if thunk != nil {
				t.Fatal("prepared fixture retained duplicate val nil-check thunks")
			}
			thunk = function
		case "T":
			other = function
		}
	}
	if thunk == nil || other == nil {
		t.Fatal("prepared fixture has no exact val and other nil-check thunk pair")
	}
	owner := universe.packages[pkg.ssa]
	callIdentity, callStatic, err := universe.wrapperCallIdentity(owner, thunk, pkgNormal)
	if err != nil || callIdentity == "" || !callStatic {
		t.Fatalf("nil-check thunk semantic call identity = %q, %t, %v; want exact static callee", callIdentity, callStatic, err)
	}
	otherIdentity, otherStatic, err := universe.wrapperCallIdentity(owner, other, pkgNormal)
	if err != nil || otherIdentity == "" || !otherStatic || otherIdentity == callIdentity {
		t.Fatalf("distinct nil-check thunk semantic call identity = %q, %t, %v; want a distinct exact static callee", otherIdentity, otherStatic, err)
	}
	if universe.samePromotedWrapperLinkIdentity(owner, thunk, other) {
		t.Fatal("distinct nil-check thunks shared one link identity")
	}
	copyThunk := new(ssa.Function)
	*copyThunk = *thunk
	if err := universe.selectFunction(owner, copyThunk, pkgNormal, false); err != nil {
		t.Fatalf("select equivalent nil-check thunk: %v", err)
	}
	canonical, resolved := universe.Resolve(copyThunk)
	if !resolved || canonical != thunk {
		t.Fatalf("equivalent nil-check thunk resolved to %v, %t; want canonical %v", canonical, resolved, thunk)
	}
}
