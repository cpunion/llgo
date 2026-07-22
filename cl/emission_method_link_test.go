/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cl

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestEmissionUniverseActiveABIMethodTablesUseFrozenWrapperSymbols(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/methodlink", `package methodlink
type Base struct{}
func (Base) M() {}
type hasM interface { M() }
func dead(value hasM) { value.M() }
func Value() any { return struct{ Base }{} }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()

	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	value := pkg.ssa.Func("Value")
	references, err := universe.CoroDemandReferences(value)
	if err != nil {
		t.Fatal(err)
	}
	foundPromoted := false
	for _, fn := range references {
		if wrapperKind(fn) == "promoted" && fn.Name() == "M" {
			foundPromoted = true
		}
	}
	if !foundPromoted {
		t.Fatal("Value has no frozen promoted M method-table reference")
	}
	synchronous, err := universe.CoroSyncDemandReferences(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range synchronous {
		if wrapperKind(fn) == "promoted" && fn.Name() == "M" {
			t.Fatalf("method-table target %v was misclassified as a synchronous raw callback", fn)
		}
	}
	plan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{{Function: value, Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse:             ssaUniverse,
		FunctionIDs:                  universe.FunctionIDConfig(),
		ClassifyDemandReferences:     universe.CoroDemandReferences,
		ClassifySyncDemandReferences: universe.CoroSyncDemandReferences,
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, pkg.ssa, []*ast.File{pkg.file}, nil,
		PackageOptions{Compilation: &Compilation{
			CoroPlan:         plan,
			EmissionUniverse: universe, CoroProfile: CoroProfileStackless,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}

	owner := universe.packages[pkg.ssa]
	found := 0
	ir := compiled.String()
	for key, physical := range universe.physicalNames {
		fn := key.function
		if key.owner != owner || wrapperKind(fn) != "promoted" || fn.Name() != "M" || fn.Signature.Recv() == nil {
			continue
		}
		receiver := types.Unalias(fn.Signature.Recv().Type())
		if pointer, ok := receiver.(*types.Pointer); ok {
			receiver = types.Unalias(pointer.Elem())
		}
		if _, ok := receiver.(*types.Struct); !ok {
			continue
		}
		state := universe.ownerStates[fn][owner]
		legacy, _, _, managed, classifyErr := universe.classifiedManagedSymbol(owner, fn, state.state)
		if classifyErr != nil || !managed {
			t.Fatalf("classify wrapper %s: managed=%v, err=%v", fn, managed, classifyErr)
		}
		if physical == legacy {
			t.Fatalf("wrapper %s retained colliding legacy symbol %q", fn, legacy)
		}
		definition := compiled.FuncOf(physical)
		if definition == nil || !definition.HasBody() {
			t.Fatalf("frozen wrapper %q has no emitted body", physical)
		}
		if old := compiled.FuncOf(legacy); old != nil {
			t.Fatalf("ABI method table retained legacy wrapper declaration %q", legacy)
		}
		if count := strings.Count(ir, physical); count < 2 {
			t.Fatalf("frozen wrapper %q occurs %d time(s) in IR; want definition and ABI method-table reference", physical, count)
		}
		found++
	}
	if found == 0 {
		t.Fatal("test did not materialize an anonymous promoted wrapper")
	}
}

func TestEmissionUniverseCrossPackageABIMethodTableUsesDeclaringWrapperSymbol(t *testing.T) {
	testProg := newEmissionTestProgram()
	declaring := testProg.addPackage(t, "example.com/emission/methoddecl", `package methoddecl
type Error string
func (e Error) Error() string { return string(e) }
`)
	consumer := testProg.addPackage(t, "example.com/emission/methodconsumer", `package methodconsumer
import "example.com/emission/methoddecl"
func Value() any { return methoddecl.Error("value") }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()

	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: declaring.ssa, Files: []*ast.File{declaring.file}},
		{SSA: consumer.ssa, Files: []*ast.File{consumer.file}},
	})
	if err != nil {
		t.Fatal(err)
	}
	errorType := declaring.types.Scope().Lookup("Error").Type()
	selection := testProg.ssa.MethodSets.MethodSet(types.NewPointer(errorType)).Lookup(declaring.types, "Error")
	if selection == nil {
		t.Fatal("pointer Error method selection is nil")
	}
	wrapper := testProg.ssa.MethodValue(selection)
	wrapper, ok := universe.Resolve(wrapper)
	if !ok || wrapper == nil || wrapperKind(wrapper) != "promoted" {
		t.Fatalf("pointer Error wrapper = %v, %t; want frozen promoted wrapper", wrapper, ok)
	}
	declaringOwner := universe.packages[declaring.ssa]
	consumerOwner := universe.packages[consumer.ssa]
	physical := universe.physicalNames[emissionFunctionOwnerKey{function: wrapper, owner: declaringOwner}]
	if physical == "" {
		t.Fatal("declaring package did not freeze the pointer Error wrapper symbol")
	}
	if unexpected := universe.physicalNames[emissionFunctionOwnerKey{function: wrapper, owner: consumerOwner}]; unexpected != "" {
		t.Fatalf("consumer unexpectedly owns wrapper symbol %q", unexpected)
	}
	ctx, err := universe.functionABIContext(wrapper, declaringOwner)
	if err != nil {
		t.Fatal(err)
	}
	_, legacy, _ := ctx.funcName(wrapper)
	if got, err := universe.physicalName(consumer.ssa, wrapper, legacy); err != nil || got != physical {
		t.Fatalf("consumer wrapper symbol = %q, %v; want declaring symbol %q", got, err, physical)
	}

	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	value := consumer.ssa.Func("Value")
	plan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{{Function: value, Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse:             ssaUniverse,
		FunctionIDs:                  universe.FunctionIDConfig(),
		ClassifyDemandReferences:     universe.CoroDemandReferences,
		ClassifySyncDemandReferences: universe.CoroSyncDemandReferences,
	})
	if err != nil {
		t.Fatal(err)
	}
	compilation := &Compilation{
		CoroPlan:         plan,
		EmissionUniverse: universe, CoroProfile: CoroProfileStackless,
	}
	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, consumer.ssa, []*ast.File{consumer.file}, nil,
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	declaration := compiled.FuncOf(physical)
	if declaration == nil || !declaration.HasBody() {
		t.Fatalf("consumer wrapper %q = %v; want a linkonce use-site definition", physical, declaration)
	}
	if legacy != physical && compiled.FuncOf(legacy) != nil {
		t.Fatalf("consumer retained legacy wrapper declaration %q", legacy)
	}
	declared, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, declaring.ssa, []*ast.File{declaring.file}, nil,
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	definitions := 0
	if function := declared.FuncOf(physical); function != nil && function.HasBody() {
		definitions++
	}
	if function := compiled.FuncOf(physical); function != nil && function.HasBody() {
		definitions++
	}
	if definitions != 2 {
		t.Fatalf("wrapper %q definition count across declaring/consumer modules = %d; want one coalescible definition per use site", physical, definitions)
	}
	for moduleName, ir := range map[string]string{
		"declaring": declared.String(),
		"consumer":  compiled.String(),
	} {
		linkOnceDefinition := false
		for _, line := range strings.Split(ir, "\n") {
			if strings.Contains(line, "define ") && strings.Contains(line, physical) && strings.Contains(line, "linkonce") {
				linkOnceDefinition = true
				break
			}
		}
		if !linkOnceDefinition {
			t.Fatalf("%s wrapper %q is not emitted with linkonce linkage", moduleName, physical)
		}
	}
}

func TestEmissionUniverseABIMethodDemandReferencesAreExactRecursiveAndOwnerScoped(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/methoddemand", `package methoddemand
var channel chan int
type Base struct{}
func (Base) Suspend() { <-channel }
type Leaf struct{}
func (Leaf) Plain() {}
type Outer struct { Base; Child Leaf; Next *Outer }
type Dead struct{}
func (Dead) Method() {}
func Demanded() any { return Outer{} }
func Unreachable() any { return Dead{} }
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
	demanded := pkg.ssa.Func("Demanded")
	unreachable := pkg.ssa.Func("Unreachable")
	references, err := universe.CoroDemandReferences(demanded)
	if err != nil {
		t.Fatal(err)
	}
	if len(references) == 0 {
		t.Fatal("Demanded has no frozen ABI method references")
	}
	for index := 1; index < len(references); index++ {
		if universe.functionSortKey(references[index-1]) > universe.functionSortKey(references[index]) {
			t.Fatalf("ABI method references are not deterministically sorted at %d", index)
		}
	}
	repeated, err := universe.CoroDemandReferences(demanded)
	if err != nil {
		t.Fatal(err)
	}
	if len(repeated) != len(references) {
		t.Fatalf("repeated reference count = %d; want %d", len(repeated), len(references))
	}
	for index := range references {
		if repeated[index] != references[index] {
			t.Fatalf("repeated reference %d = %v; want exact %v", index, repeated[index], references[index])
		}
	}
	references[0] = nil
	defensive, err := universe.CoroDemandReferences(demanded)
	if err != nil {
		t.Fatal(err)
	}
	if len(defensive) == 0 || defensive[0] == nil {
		t.Fatal("caller mutation changed frozen ABI method references")
	}

	memberType := func(name string) types.Type {
		member, ok := pkg.ssa.Members[name].(*ssa.Type)
		if !ok {
			t.Fatalf("SSA member %q is not a type", name)
		}
		return member.Type()
	}
	exactMethod := func(typ types.Type, name string) *ssa.Function {
		selection := emissionABIDemandMethodSelection(t, testProg.ssa, typ, name)
		method := testProg.ssa.MethodValue(selection)
		if method == nil {
			t.Fatalf("method %s.%s has no SSA value", typ, name)
		}
		canonical, ok := universe.Resolve(method)
		if !ok {
			t.Fatalf("method %s.%s is outside the frozen universe", typ, name)
		}
		return canonical
	}
	hasReference := func(list []*ssa.Function, target *ssa.Function) bool {
		for _, candidate := range list {
			if candidate == target {
				return true
			}
		}
		return false
	}
	outer := memberType("Outer")
	valueTFN := exactMethod(outer, "Suspend")
	pointerIFN := exactMethod(types.NewPointer(outer), "Suspend")
	if valueTFN == pointerIFN {
		t.Fatal("promoted value tfn and pointer ifn unexpectedly share one SSA wrapper")
	}
	leafPlain := exactMethod(memberType("Leaf"), "Plain")
	for label, target := range map[string]*ssa.Function{
		"value tfn": valueTFN, "pointer ifn": pointerIFN, "recursive field method": leafPlain,
	} {
		if !hasReference(defensive, target) {
			t.Fatalf("Demanded references omit exact %s %v", label, target)
		}
	}

	deadReferences, err := universe.CoroDemandReferences(unreachable)
	if err != nil {
		t.Fatal(err)
	}
	deadMethod := exactMethod(memberType("Dead"), "Method")
	if !hasReference(deadReferences, deadMethod) {
		t.Fatalf("Unreachable references omit exact Dead.Method %v", deadMethod)
	}

	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{{Function: demanded, Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse:             ssaUniverse,
		FunctionIDs:                  universe.FunctionIDConfig(),
		ClassifyDemandReferences:     universe.CoroDemandReferences,
		ClassifySyncDemandReferences: universe.CoroSyncDemandReferences,
	})
	if err != nil {
		t.Fatal(err)
	}
	demandedPlan, _ := plan.FunctionPlan(demanded)
	if demandedPlan.Effect != coro.NoSuspend || demandedPlan.Emission != coro.EmitPlain {
		t.Fatalf("Demanded plan = %+v, method addresses must not propagate effects", demandedPlan)
	}
	for _, target := range []*ssa.Function{valueTFN, pointerIFN} {
		methodPlan, ok := plan.FunctionPlan(target)
		if !ok || methodPlan.Demand != coro.AsyncDemand || methodPlan.Emission != coro.EmitCoroutine || methodPlan.Primary != coro.PrimaryCoroutine {
			t.Fatalf("suspending method %v plan = %+v, present=%v; want demanded coroutine entry", target, methodPlan, ok)
		}
	}
	deadPlan, ok := plan.FunctionPlan(deadMethod)
	if !ok || deadPlan.Demand != coro.NoDemand || deadPlan.Emission != coro.EmitNone {
		t.Fatalf("unreachable method plan = %+v, present=%v; want no over-emission", deadPlan, ok)
	}

	delete(universe.required, leafPlain)
	if _, err := universe.CoroDemandReferences(demanded); err == nil || !strings.Contains(err.Error(), "outside the frozen emission universe") {
		t.Fatalf("missing frozen ABI method error = %v", err)
	}
}

func TestEmissionUniverseActiveGenericLocalMethodFormsUseFrozenSymbols(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/genericmethodlink", `package genericmethodlink
type Box[X any] struct{ Value X }
func (Box[X]) M() int { return 1 }
func Generic[T any]() int {
	type Local struct{ Value T }
	var value Box[Local]
	direct := value.M()
	expression := Box[Local].M
	bound := value.M
	return direct + expression(value) + bound()
}
func UseInt() int { return Generic[int]() }
func UseString() int { return Generic[string]() }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()

	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{
		{Function: pkg.ssa.Func("UseInt"), Demand: coro.SyncDemand},
		{Function: pkg.ssa.Func("UseString"), Demand: coro.SyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse: ssaUniverse,
		FunctionIDs:      universe.FunctionIDConfig(),
		ResolveFunction: func(fn *ssa.Function) (*ssa.Function, bool, error) {
			canonical, ok := universe.Resolve(fn)
			return canonical, ok, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, pkg.ssa, []*ast.File{pkg.file}, nil,
		PackageOptions{Compilation: &Compilation{
			CoroPlan:         plan,
			EmissionUniverse: universe, CoroProfile: CoroProfileStackless,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}

	variantOf := func(fnType types.Type) string {
		seen := make(map[types.Type]bool)
		var visit func(types.Type) string
		visit = func(typ types.Type) string {
			if typ == nil {
				return ""
			}
			typ = types.Unalias(typ)
			if seen[typ] {
				return ""
			}
			seen[typ] = true
			switch typ := typ.(type) {
			case *types.Named:
				if object := typ.Obj(); object != nil && strings.HasPrefix(object.Name(), "Local") {
					if structure, ok := typ.Underlying().(*types.Struct); ok {
						for index := 0; index < structure.NumFields(); index++ {
							field := structure.Field(index)
							if field.Name() != "Value" {
								continue
							}
							switch {
							case types.Identical(field.Type(), types.Typ[types.Int]):
								return "int"
							case types.Identical(field.Type(), types.Typ[types.String]):
								return "string"
							}
						}
					}
				}
				for index := 0; index < typ.TypeArgs().Len(); index++ {
					if variant := visit(typ.TypeArgs().At(index)); variant != "" {
						return variant
					}
				}
				return visit(typ.Underlying())
			case *types.Pointer:
				return visit(typ.Elem())
			case *types.Signature:
				if recv := typ.Recv(); recv != nil {
					if variant := visit(recv.Type()); variant != "" {
						return variant
					}
				}
				for _, tuple := range []*types.Tuple{typ.Params(), typ.Results()} {
					for index := 0; index < tuple.Len(); index++ {
						if variant := visit(tuple.At(index).Type()); variant != "" {
							return variant
						}
					}
				}
			case *types.Struct:
				for index := 0; index < typ.NumFields(); index++ {
					if variant := visit(typ.Field(index).Type()); variant != "" {
						return variant
					}
				}
			}
			return ""
		}
		return visit(fnType)
	}

	owner := universe.packages[pkg.ssa]
	found := make(map[string]map[string]string)
	legacies := make(map[string]map[string]string)
	ir := compiled.String()
	for _, fn := range universe.Functions() {
		kind := wrapperKind(fn)
		if kind == "" && fn.Origin() != nil && fn.Origin().Name() == "M" {
			kind = "direct"
		}
		if kind != "direct" && fn.Name() != "M" && !strings.HasPrefix(fn.Name(), "M$") ||
			(kind != "direct" && kind != "thunk" && kind != "bound") {
			continue
		}
		owned := false
		for _, candidate := range universe.sortedUseOwners(fn) {
			owned = owned || candidate == owner
		}
		if !owned {
			continue
		}
		variant := variantOf(universe.effectiveType(owner, fn, fn.Signature))
		if variant == "" {
			for _, free := range fn.FreeVars {
				variant = variantOf(universe.effectiveType(owner, fn, free.Type()))
				if variant != "" {
					break
				}
			}
		}
		if variant == "" {
			continue
		}
		state := universe.ownerStates[fn][owner]
		legacy, _, _, managed, classifyErr := universe.classifiedManagedSymbol(owner, fn, state.state)
		if classifyErr != nil || !managed {
			t.Fatalf("classify %s/%s %s: managed=%v, err=%v", kind, variant, fn, managed, classifyErr)
		}
		physical, err := universe.physicalName(owner.ssa, fn, legacy)
		if err != nil {
			t.Fatal(err)
		}
		if found[kind] == nil {
			found[kind] = make(map[string]string)
			legacies[kind] = make(map[string]string)
		}
		if previous := found[kind][variant]; previous != "" && previous != physical {
			t.Fatalf("%s/%s has multiple physical symbols %q and %q", kind, variant, previous, physical)
		}
		found[kind][variant] = physical
		legacies[kind][variant] = legacy
		definition := compiled.FuncOf(physical)
		if definition == nil || !definition.HasBody() {
			t.Fatalf("%s/%s frozen symbol %q has no emitted body", kind, variant, physical)
		}
		if physical != legacy {
			if old := compiled.FuncOf(legacy); old != nil {
				t.Fatalf("%s/%s retained legacy declaration %q", kind, variant, legacy)
			}
		}
		if count := strings.Count(ir, physical); count < 2 {
			t.Fatalf("%s/%s frozen symbol %q occurs %d time(s); want definition and reference", kind, variant, physical, count)
		}
	}
	for _, kind := range []string{"direct", "thunk", "bound"} {
		if found[kind]["int"] == "" || found[kind]["string"] == "" {
			t.Fatalf("generic local %s symbols = %v; want int and string", kind, found[kind])
		}
		if found[kind]["int"] == found[kind]["string"] {
			t.Fatalf("generic local %s int/string symbols collide at %q", kind, found[kind]["int"])
		}
		if legacies[kind]["int"] == legacies[kind]["string"] &&
			(found[kind]["int"] == legacies[kind]["int"] || found[kind]["string"] == legacies[kind]["string"]) {
			t.Fatalf("generic local %s legacy collision %q was not fully frozen: %v", kind, legacies[kind]["int"], found[kind])
		}
	}
}
