//go:build !llgo
// +build !llgo

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
	"go/constant"
	"go/token"
	"go/types"
	"strings"
	"testing"

	llssa "github.com/goplus/llgo/ssa"
	llabi "github.com/goplus/llgo/ssa/abi"
	"github.com/goplus/llgo/ssa/ssatest"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/go/types/typeutil"
)

func newEmissionABIDemandTestUniverse(testProg *emissionTestProgram, pkg emissionTestPackage) (*EmissionUniverse, *preparedEmissionPackage) {
	owner := &preparedEmissionPackage{
		identity: pkg.types.Path(),
		ssa:      pkg.ssa,
		files:    []*ast.File{pkg.file},
		pkgPath:  pkg.types.Path(),
		oldTypes: pkg.types,
		pkgTypes: pkg.types,
	}
	u := &EmissionUniverse{
		goProg:   testProg.ssa,
		packages: map[*ssa.Package]*preparedEmissionPackage{pkg.ssa: owner},
		byTypes:  map[*types.Package]*preparedEmissionPackage{pkg.types: owner},
		required: make(map[*ssa.Function]none),
		aliases:  make(map[*ssa.Function]*ssa.Function),
	}
	return u, owner
}

func emissionABIDemandContains(typesList []types.Type, want types.Type) bool {
	for _, got := range typesList {
		if types.Identical(got, want) {
			return true
		}
	}
	return false
}

func emissionABIDemandNamed(typ types.Type, name string) *types.Named {
	switch typ := types.Unalias(typ).(type) {
	case *types.Pointer:
		return emissionABIDemandNamed(typ.Elem(), name)
	case *types.Named:
		if typ.Obj() != nil && typ.Obj().Name() == name {
			return typ
		}
	}
	return nil
}

func emissionABIDemandMethodSelection(t *testing.T, prog *ssa.Program, typ types.Type, name string) *types.Selection {
	t.Helper()
	mset := prog.MethodSets.MethodSet(typ)
	for index := 0; index < mset.Len(); index++ {
		if mset.At(index).Obj().Name() == name {
			return mset.At(index)
		}
	}
	t.Fatalf("method %q is absent from method set of %v", name, typ)
	return nil
}

func TestEmissionABIDemandPureFieldAccessDoesNotMaterializeLocalWrapper(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/fieldonly", `package fieldonly
type Type struct{}
func (*Type) Align() int { return 1 }
type PtrType struct{ Type }
type FuncType struct{ Type }
func Field(kind bool) *Type {
	if kind {
		type u struct{ PtrType }
		value := new(u)
		return &value.Type
	}
	type u struct{ FuncType }
	value := new(u)
	return &value.Type
}
`)
	testProg.ssa.Build()
	u, owner := newEmissionABIDemandTestUniverse(testProg, pkg)
	fn := pkg.ssa.Func("Field")
	demands, err := u.functionABITypeDemands(fn, owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(demands) != 0 {
		t.Fatalf("pure field access ABI roots = %v; want none", demands)
	}
	if err := u.materializeABITypeDemandsOfFunction(fn, owner, emissionFunctionState{state: pkgNormal}); err != nil {
		t.Fatal(err)
	}

	locals := make(map[*types.Named]none)
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if value, ok := instruction.(ssa.Value); ok {
				if named := emissionABIDemandNamed(value.Type(), "u"); named != nil {
					locals[named] = none{}
				}
			}
		}
	}
	if len(locals) != 2 {
		t.Fatalf("found %d local u types, want the two runtime/abi-style declarations", len(locals))
	}
	for local := range locals {
		selection := emissionABIDemandMethodSelection(t, testProg.ssa, types.NewPointer(local), "Align")
		wrapper := testProg.ssa.MethodValue(selection)
		if wrapper == nil {
			t.Fatalf("promoted Align wrapper for %v was not constructible", local)
		}
		if u.Contains(wrapper) {
			t.Fatalf("field-only local wrapper %v was materialized", wrapper)
		}
	}
}

func TestEmissionABIDemandIgnoresBodiesNotLoweredAsGoFunctions(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/nonloweredbody", `package nonloweredbody
type Marker struct{}
func (Marker) M() {}
type I interface{ M() }
func Go() I { func() { var _ I = Marker{} }(); return Marker{} }
//llgo:link C C.fake
func C() I { func() { var _ I = Marker{} }(); return Marker{} }
//llgo:link Python py.fake
func Python() I { func() { var _ I = Marker{} }(); return Marker{} }
//llgo:link Intrinsic llgo.skip
func Intrinsic() I { func() { var _ I = Marker{} }(); return Marker{} }
`)
	testProg.ssa.Build()

	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	owner := universe.ownerOf(pkg.ssa.Func("Go"))
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "Go", want: true},
		{name: "C"},
		{name: "Python"},
		{name: "Intrinsic"},
	} {
		demands, err := universe.functionABITypeDemands(pkg.ssa.Func(test.name), owner)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if got := len(demands) != 0; got != test.want {
			t.Fatalf("%s body ABI roots = %v; nonempty=%v, want %v", test.name, demands, got, test.want)
		}
		fn := pkg.ssa.Func(test.name)
		if len(fn.AnonFuncs) != 1 {
			t.Fatalf("%s anonymous functions = %d; want 1", test.name, len(fn.AnonFuncs))
		}
		if got := universe.Contains(fn.AnonFuncs[0]); got != test.want {
			t.Fatalf("%s fallback child retained=%v, want %v", test.name, got, test.want)
		}
	}
}

func TestEmissionABIDemandCollectsOnlyLoweringRoots(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/roots", `package roots
type T struct{}
func (T) M() {}
type I interface{ M() }
type J interface { I; N() }
func Roots(input map[T]int, value any, wider J) (any, I) {
	local := make(map[T]int)
	local[T{}] = input[T{}]
	for range local { break }
	delete(local, T{})
	clear(local)
	slice := []T{T{}}
	clear(slice)
	_ = value.(T)
	return T{}, wider
}
`)
	testProg.ssa.Build()
	u, owner := newEmissionABIDemandTestUniverse(testProg, pkg)
	fn := pkg.ssa.Func("Roots")

	var sawMakeInterface, sawMap, sawTypeAssert, sawChangeInterface bool
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			switch instruction.(type) {
			case *ssa.MakeInterface:
				sawMakeInterface = true
			case *ssa.MakeMap, *ssa.Lookup, *ssa.MapUpdate, *ssa.Range:
				sawMap = true
			case *ssa.TypeAssert:
				sawTypeAssert = true
			case *ssa.ChangeInterface:
				sawChangeInterface = true
			}
		}
	}
	if !sawMakeInterface || !sawMap || !sawTypeAssert || !sawChangeInterface {
		t.Fatalf("test SSA lacks required operations: makeInterface=%v map=%v typeAssert=%v changeInterface=%v", sawMakeInterface, sawMap, sawTypeAssert, sawChangeInterface)
	}

	demands, err := u.functionABITypeDemands(fn, owner)
	if err != nil {
		t.Fatal(err)
	}
	tType := pkg.types.Scope().Lookup("T").Type()
	iType := pkg.types.Scope().Lookup("I").Type()
	wants := []types.Type{
		tType,
		types.NewMap(tType, types.Typ[types.Int]),
		types.NewSlice(tType),
		iType.Underlying(),
	}
	for _, want := range wants {
		if !emissionABIDemandContains(demands, want) {
			t.Errorf("ABI roots %v do not contain %v", demands, want)
		}
	}
	if emissionABIDemandContains(demands, pkg.types.Scope().Lookup("J").Type()) {
		t.Fatalf("source interface J became a root even though ChangeInterface only emits its target descriptor: %v", demands)
	}
}

func TestEmissionABIDemandWalksPtrToThisAndMethods(t *testing.T) {
	pkg := types.NewPackage("example.com/emission/walk", "walk")
	obj := types.NewTypeName(token.NoPos, pkg, "T", nil)
	named := types.NewNamed(obj, types.NewStruct(nil, nil), nil)
	receiver := types.NewVar(token.NoPos, pkg, "", types.NewPointer(named))
	signature := types.NewSignature(receiver, nil, nil, false)
	named.AddMethod(types.NewFunc(token.NoPos, pkg, "PointerMethod", signature))

	var visited typeutil.Map
	if err := walkEmissionABITypeDemand(named, nil, func(typ types.Type) error {
		visited.Set(typ, true)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pointer := types.NewPointer(named)
	if visited.At(named) == nil || visited.At(pointer) == nil {
		t.Fatalf("visited descriptors omit root or PtrToThis: root=%v pointer=%v", visited.At(named), visited.At(pointer))
	}
	if types.NewMethodSet(pointer).Len() != 1 {
		t.Fatalf("PtrToThis method set was not reachable for %v", pointer)
	}
}

func TestEmissionABIDemandNamedUnderlyingIsNotDescriptorRoot(t *testing.T) {
	field := types.NewField(token.NoPos, nil, "Value", types.Typ[types.Int], false)
	underlying := types.NewStruct([]*types.Var{field}, nil)
	named := types.NewNamed(types.NewTypeName(token.NoPos, nil, "N", nil), underlying, nil)

	var visited typeutil.Map
	if err := walkEmissionABITypeDemand(named, nil, func(typ types.Type) error {
		visited.Set(typ, true)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if visited.At(named) == nil || visited.At(types.NewPointer(named)) == nil {
		t.Fatal("named root or its PtrToThis descriptor was not visited")
	}
	if visited.At(underlying) != nil || visited.At(types.NewPointer(underlying)) != nil {
		t.Fatalf("named underlying container became an independent descriptor: %v", underlying)
	}
	if visited.At(types.Typ[types.Int]) == nil {
		t.Fatal("named underlying fields were not recursively visited")
	}
}

func TestEmissionABIDemandGenericLocalUsesFunctionExactPatchedType(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/genericlocal", `package genericlocal
type Box[T any] struct{ Value T }
func (Box[T]) M() {}
type I interface{ M() }
func Generic[T any](value T) I {
	type Payload struct { Box[T] }
	type Local struct {
		Next *Local
		Payload
	}
	closure := func(inner bool) I {
		type Inner struct {
			Next *Inner
			Payload
		}
		if inner { return Inner{} }
		return Local{}
	}
	_ = closure
	return Local{Payload: Payload{Box: Box[T]{Value: value}}}
}
func Use() I { return Generic(1) }
func UseString() I { return Generic("value") }
`)
	testProg.ssa.Build()

	origin := pkg.ssa.Func("Generic")
	var instance, stringInstance *ssa.Function
	var makeInterface, stringMakeInterface *ssa.MakeInterface
	for fn := range ssautil.AllFunctions(testProg.ssa) {
		if fn == nil || fn.Origin() != origin || len(fn.TypeArgs()) != 1 {
			continue
		}
		var candidate *ssa.MakeInterface
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if makeInterface, ok := instruction.(*ssa.MakeInterface); ok {
					candidate = makeInterface
				}
			}
		}
		switch {
		case types.Identical(fn.TypeArgs()[0], types.Typ[types.Int]):
			instance, makeInterface = fn, candidate
		case types.Identical(fn.TypeArgs()[0], types.Typ[types.String]):
			stringInstance, stringMakeInterface = fn, candidate
		}
	}
	if instance == nil || makeInterface == nil || stringInstance == nil || stringMakeInterface == nil {
		t.Fatal("instantiated Generic[int]/Generic[string] MakeInterface was not found")
	}

	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	owner := universe.ownerOf(instance)
	demands, err := universe.functionABITypeDemands(instance, owner)
	if err != nil {
		t.Fatal(err)
	}
	var preparedLocal types.Type
	for _, demand := range demands {
		if named := emissionABIDemandNamed(demand, "Local[int]"); named != nil {
			preparedLocal = demand
			break
		}
	}
	if preparedLocal == nil {
		t.Fatalf("scanner roots %v omit the patched generic local type", demands)
	}

	// Simulate the later context.type_ call. The prepared root must be the
	// exact canonical raw type that active codegen gets, not merely an
	// identically printed fresh *types.Named.
	ctx, err := universe.functionABIContext(instance, owner)
	if err != nil {
		t.Fatal(err)
	}
	rawLocal := makeInterface.X.Type()
	activeLocal := universe.physicalFunctionABIType(ctx, rawLocal)
	if types.Identical(rawLocal, activeLocal) || !strings.Contains(activeLocal.String(), "[int]") {
		t.Fatalf("generic local type patch = %v from %v; want a distinct type carrying [int]", activeLocal, rawLocal)
	}
	if preparedLocal != activeLocal {
		t.Fatalf("scanner local type %p differs from active-codegen canonical type %p", preparedLocal, activeLocal)
	}
	rawNamed := emissionABIDemandNamed(rawLocal, "Local")
	if rawNamed == nil {
		t.Fatalf("raw generic local = %v; want Local", rawLocal)
	}
	parallelUniverse := &EmissionUniverse{
		goProg:             testProg.ssa,
		localGenericTypes:  make(map[*types.Named]emissionLocalGenericType),
		localGenericOwners: make(map[*types.Named]*ssa.Function),
	}
	const parallel = 32
	results := make(chan *types.Named, parallel)
	for range parallel {
		go func() {
			results <- parallelUniverse.canonicalLocalGenericNamed(ctx, rawNamed)
		}()
	}
	var parallelCanonical *types.Named
	for range parallel {
		candidate := <-results
		if candidate == nil || candidate.Underlying() == nil {
			t.Fatal("parallel canonicalization returned an incomplete type")
		}
		if parallelCanonical == nil {
			parallelCanonical = candidate
		} else if candidate != parallelCanonical {
			t.Fatalf("parallel canonicalization returned %p and %p", parallelCanonical, candidate)
		}
	}
	if len(instance.AnonFuncs) != 1 {
		t.Fatalf("Generic[int] anonymous functions = %d; want 1", len(instance.AnonFuncs))
	}
	closure := instance.AnonFuncs[0]
	var closureMakeInterface, closureInnerMakeInterface *ssa.MakeInterface
	for _, block := range closure.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.MakeInterface); ok {
				switch {
				case emissionABIDemandNamed(candidate.X.Type(), "Local") != nil:
					closureMakeInterface = candidate
				case emissionABIDemandNamed(candidate.X.Type(), "Inner") != nil:
					closureInnerMakeInterface = candidate
				}
			}
		}
	}
	if closureMakeInterface == nil || closureInnerMakeInterface == nil {
		t.Fatal("Generic[int] closure Local/Inner MakeInterface was not found")
	}
	closureOwner := universe.ownerOf(closure)
	closureCtx, err := universe.functionABIContext(closure, closureOwner)
	if err != nil {
		t.Fatal(err)
	}
	closureLocal := universe.physicalFunctionABIType(closureCtx, closureMakeInterface.X.Type())
	if closureLocal != activeLocal {
		t.Fatalf("outer Local[int] = %p, closure Local[int] = %p; want one canonical type", activeLocal, closureLocal)
	}
	closureInnerLocal := universe.physicalFunctionABIType(closureCtx, closureInnerMakeInterface.X.Type())
	checkCanonicalGraph := func(typ types.Type, name, payloadName string) *types.Named {
		t.Helper()
		named := emissionABIDemandNamed(typ, name)
		if named == nil {
			t.Fatalf("%v does not contain named type %q", typ, name)
		}
		underlying, ok := named.Underlying().(*types.Struct)
		if !ok || underlying.NumFields() == 0 || underlying.Field(0).Name() != "Next" {
			t.Fatalf("%v underlying = %T %v; want struct beginning with Next", named, named.Underlying(), named.Underlying())
		}
		pointer, ok := types.Unalias(underlying.Field(0).Type()).(*types.Pointer)
		if !ok || pointer.Elem() != named {
			t.Fatalf("%v.Next = %v; want pointer back to exact canonical named type %p", named, underlying.Field(0).Type(), named)
		}
		if underlying.NumFields() != 2 {
			t.Fatalf("%v fields = %d; want Next and Payload", named, underlying.NumFields())
		}
		payload, ok := types.Unalias(underlying.Field(1).Type()).(*types.Named)
		if !ok || payload.Obj().Name() != payloadName {
			t.Fatalf("%v.Payload = %v; want canonical named %q", named, underlying.Field(1).Type(), payloadName)
		}
		return payload
	}
	intPayload := checkCanonicalGraph(activeLocal, "Local[int]", "Payload[int]")
	closurePayload := checkCanonicalGraph(closureInnerLocal, "Inner[int]", "Payload[int]")
	if closurePayload != intPayload {
		t.Fatalf("outer and closure-inner canonical Payload[int] differ: %p and %p", intPayload, closurePayload)
	}

	stringOwner := universe.ownerOf(stringInstance)
	stringCtx, err := universe.functionABIContext(stringInstance, stringOwner)
	if err != nil {
		t.Fatal(err)
	}
	stringActiveLocal := universe.physicalFunctionABIType(stringCtx, stringMakeInterface.X.Type())
	if stringActiveLocal == activeLocal || !strings.Contains(stringActiveLocal.String(), "[string]") {
		t.Fatalf("Generic[string] local = %v (%p); want a distinct [string] canonical type from %v (%p)", stringActiveLocal, stringActiveLocal, activeLocal, activeLocal)
	}
	stringPayload := checkCanonicalGraph(stringActiveLocal, "Local[string]", "Payload[string]")
	if intPayload == stringPayload {
		t.Fatalf("Generic[int] and Generic[string] share canonical Payload %p", intPayload)
	}
	stringDemands, err := universe.functionABITypeDemands(stringInstance, stringOwner)
	if err != nil {
		t.Fatal(err)
	}
	if !emissionABIDemandContains(stringDemands, stringActiveLocal) {
		t.Fatalf("Generic[string] scanner roots %v omit exact active-codegen type %p", stringDemands, stringActiveLocal)
	}
	selection := emissionABIDemandMethodSelection(t, testProg.ssa, activeLocal, "M")
	wantWrapper := testProg.ssa.MethodValue(selection)
	if wantWrapper == nil {
		t.Fatalf("active-codegen MethodValue for %v.M is nil", activeLocal)
	}
	state := universe.ownerStates[instance][owner]
	if err := universe.materializeABITypeDemandsOfFunction(instance, owner, state); err != nil {
		t.Fatal(err)
	}
	if resolved, ok := universe.Resolve(wantWrapper); !ok || resolved != wantWrapper {
		t.Fatalf("prepared wrapper = %v, %v; want exact active-codegen MethodValue %v (demands=%v, synthetic=%q, receiver=%v)", resolved, ok, wantWrapper, demands, wantWrapper.Synthetic, wantWrapper.Signature.Recv().Type())
	}
}

func TestEmissionABIDemandGenericLocalTypeArgumentUsesDefinitionRegistry(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/genericlocalarg", `package genericlocalarg
type Box[X any] struct{ Item X }
func Helper[X any]() any { var value X; return value }
func Generic[T any](value T) {
	type Local struct{ Value T }
	_ = Helper[Box[Local]]()
}
func Use() { Generic(1) }
func UseString() { Generic("value") }
`)
	testProg.ssa.Build()

	origin := pkg.ssa.Func("Helper")
	type helperInstance struct {
		fn            *ssa.Function
		makeInterface *ssa.MakeInterface
		suffix        string
	}
	instances := make(map[string]helperInstance)
	for fn := range ssautil.AllFunctions(testProg.ssa) {
		if fn == nil || fn.Origin() != origin || len(fn.TypeArgs()) != 1 {
			continue
		}
		box, ok := types.Unalias(fn.TypeArgs()[0]).(*types.Named)
		if !ok || box.Obj().Name() != "Box" || box.TypeArgs().Len() != 1 {
			continue
		}
		named, ok := types.Unalias(box.TypeArgs().At(0)).(*types.Named)
		if !ok {
			continue
		}
		underlying, ok := named.Underlying().(*types.Struct)
		if !ok || underlying.NumFields() != 1 {
			continue
		}
		suffix := ""
		switch {
		case types.Identical(underlying.Field(0).Type(), types.Typ[types.Int]):
			suffix = "int"
		case types.Identical(underlying.Field(0).Type(), types.Typ[types.String]):
			suffix = "string"
		default:
			continue
		}
		var makeInterface *ssa.MakeInterface
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if candidate, ok := instruction.(*ssa.MakeInterface); ok {
					makeInterface = candidate
				}
			}
		}
		instances[suffix] = helperInstance{fn: fn, makeInterface: makeInterface, suffix: suffix}
	}
	if len(instances) != 2 || instances["int"].makeInterface == nil || instances["string"].makeInterface == nil {
		t.Fatalf("Helper local-type instances = %#v; want int and string MakeInterface bodies", instances)
	}

	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	canonical := make(map[string]types.Type)
	physicalNames := make(map[string]string)
	for suffix, instance := range instances {
		owner := universe.ownerOf(instance.fn)
		ctx, err := universe.functionABIContext(instance.fn, owner)
		if err != nil {
			t.Fatal(err)
		}
		active := universe.physicalFunctionABIType(ctx, instance.makeInterface.X.Type())
		name := "Local[" + suffix + "]"
		activeBox, ok := types.Unalias(active).(*types.Named)
		if !ok || activeBox.Obj().Name() != "Box" || activeBox.TypeArgs().Len() != 1 {
			t.Fatalf("Helper[%s] active root = %v; want canonical Box", suffix, active)
		}
		activeLocal := emissionABIDemandNamed(activeBox.TypeArgs().At(0), name)
		if activeLocal == nil {
			t.Fatalf("Helper[%s] active Box arg = %v; want %q from definition registry", suffix, activeBox.TypeArgs().At(0), name)
		}
		demands, err := universe.functionABITypeDemands(instance.fn, owner)
		if err != nil {
			t.Fatal(err)
		}
		if !emissionABIDemandContains(demands, active) {
			t.Fatalf("Helper[%s] ABI roots %v omit exact canonical local %p", suffix, demands, active)
		}
		canonical[suffix] = activeLocal
		_, legacy, _ := ctx.funcName(instance.fn)
		physical, err := universe.physicalName(owner.ssa, instance.fn, legacy)
		if err != nil {
			t.Fatal(err)
		}
		physicalNames[suffix] = physical
	}
	if canonical["int"] == canonical["string"] {
		t.Fatalf("Helper Local[int]/Local[string] share canonical type %p", canonical["int"])
	}
	if universe.finalIdentity(instances["int"].fn) == universe.finalIdentity(instances["string"].fn) {
		t.Fatal("Helper[Local[int]] and Helper[Local[string]] final symbols collide")
	}
	if physicalNames["int"] == physicalNames["string"] {
		t.Fatalf("Helper[Box[Local[int/string]]] physical symbols collide at %q", physicalNames["int"])
	}
}

func TestEmissionABIDemandAnonymousInterfaceMethodPatchesGenericLocal(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/genericlocalinterface", `package genericlocalinterface
func Generic[T any](input any) {
	type Local struct{ Value T }
	_, _ = input.(interface{ M(Local) })
}
func Use(input any) { Generic[int](input) }
func UseString(input any) { Generic[string](input) }
`)
	testProg.ssa.Build()
	origin := pkg.ssa.Func("Generic")
	type assertionInstance struct {
		fn        *ssa.Function
		assertion *ssa.TypeAssert
	}
	instances := make(map[string]assertionInstance)
	for fn := range ssautil.AllFunctions(testProg.ssa) {
		if fn == nil || fn.Origin() != origin || len(fn.TypeArgs()) != 1 {
			continue
		}
		suffix := fn.TypeArgs()[0].String()
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if assertion, ok := instruction.(*ssa.TypeAssert); ok {
					instances[suffix] = assertionInstance{fn: fn, assertion: assertion}
				}
			}
		}
	}
	if len(instances) != 2 || instances["int"].assertion == nil || instances["string"].assertion == nil {
		t.Fatalf("generic interface instances = %#v; want int and string", instances)
	}
	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	patchedLocals := make(map[string]*types.Named)
	for suffix, instance := range instances {
		owner := universe.ownerOf(instance.fn)
		ctx, err := universe.functionABIContext(instance.fn, owner)
		if err != nil {
			t.Fatal(err)
		}
		patched := ctx.patchType(instance.assertion.AssertedType)
		iface, ok := types.Unalias(patched).Underlying().(*types.Interface)
		if !ok || iface.NumMethods() != 1 {
			t.Fatalf("Generic[%s] asserted type = %v; want one-method interface", suffix, patched)
		}
		param := iface.Method(0).Type().(*types.Signature).Params().At(0).Type()
		name := "Local[" + suffix + "]"
		local := emissionABIDemandNamed(param, name)
		if local == nil {
			t.Fatalf("Generic[%s] interface method param = %v; want %q", suffix, param, name)
		}
		patchedLocals[suffix] = local
	}
	if patchedLocals["int"] == patchedLocals["string"] {
		t.Fatalf("anonymous interface methods share local canonical type %p", patchedLocals["int"])
	}
}

func TestEmissionABIDemandNestedGenericLocalTypeArgumentNamesRemainDistinct(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/nestedlocalarg", `package nestedlocalarg
func G[X any](value X) any {
	type M struct{ Value X }
	return M{Value: value}
}
func F[T any](value T) any {
	type L struct{ Value T }
	return G(L{Value: value})
}
func Use() any { return F(1) }
func UseString() any { return F("value") }
`)
	testProg.ssa.Build()
	origin := pkg.ssa.Func("G")
	type nestedInstance struct {
		fn            *ssa.Function
		makeInterface *ssa.MakeInterface
	}
	instances := make(map[string]nestedInstance)
	for fn := range ssautil.AllFunctions(testProg.ssa) {
		if fn == nil || fn.Origin() != origin || len(fn.TypeArgs()) != 1 {
			continue
		}
		local, ok := types.Unalias(fn.TypeArgs()[0]).(*types.Named)
		if !ok {
			continue
		}
		underlying := local.Underlying().(*types.Struct)
		suffix := underlying.Field(0).Type().String()
		if suffix != "int" && suffix != "string" {
			continue
		}
		var makeInterface *ssa.MakeInterface
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if candidate, ok := instruction.(*ssa.MakeInterface); ok {
					makeInterface = candidate
				}
			}
		}
		instances[suffix] = nestedInstance{fn: fn, makeInterface: makeInterface}
	}
	if len(instances) != 2 || instances["int"].makeInterface == nil || instances["string"].makeInterface == nil {
		t.Fatalf("nested G instances = %#v; want int and string", instances)
	}
	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]string)
	for suffix, instance := range instances {
		owner := universe.ownerOf(instance.fn)
		ctx, err := universe.functionABIContext(instance.fn, owner)
		if err != nil {
			t.Fatal(err)
		}
		active := universe.physicalFunctionABIType(ctx, instance.makeInterface.X.Type())
		named, ok := types.Unalias(active).(*types.Named)
		if !ok || !strings.HasPrefix(named.Obj().Name(), "M[") || !strings.Contains(named.Obj().Name(), suffix) {
			t.Fatalf("G[%s] local M canonical name = %v", suffix, active)
		}
		names[suffix] = named.Obj().Name()
	}
	if names["int"] == names["string"] {
		t.Fatalf("nested G local names collide at %q", names["int"])
	}
}

func TestEmissionABIDemandRecursiveConstrainedGenericLocalGraph(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/constrainedlocal", `package constrainedlocal
type Base struct{}
func (*Base) M() {}
type Box[X interface{ M() }] struct{ Value X }
func Generic[T any]() any {
	type Local struct {
		Base
		Next Box[*Local]
		Value T
	}
	return Local{}
}
func Use() any { return Generic[int]() }
`)
	testProg.ssa.Build()
	origin := pkg.ssa.Func("Generic")
	var instance *ssa.Function
	var makeInterface *ssa.MakeInterface
	for fn := range ssautil.AllFunctions(testProg.ssa) {
		if fn == nil || fn.Origin() != origin || len(fn.TypeArgs()) != 1 {
			continue
		}
		instance = fn
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if candidate, ok := instruction.(*ssa.MakeInterface); ok {
					makeInterface = candidate
				}
			}
		}
	}
	if instance == nil || makeInterface == nil {
		t.Fatal("constrained Generic[int] instance was not found")
	}
	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	owner := universe.ownerOf(instance)
	ctx, err := universe.functionABIContext(instance, owner)
	if err != nil {
		t.Fatal(err)
	}
	active := universe.physicalFunctionABIType(ctx, makeInterface.X.Type())
	local := emissionABIDemandNamed(active, "Local[int]")
	if local == nil {
		t.Fatalf("constrained active local = %v; want Local[int]", active)
	}
	underlying := local.Underlying().(*types.Struct)
	box, ok := types.Unalias(underlying.Field(1).Type()).(*types.Named)
	if !ok || box.TypeArgs().Len() != 1 {
		t.Fatalf("Local[int].Next = %v; want Box[*Local[int]]", underlying.Field(1).Type())
	}
	pointer, ok := types.Unalias(box.TypeArgs().At(0)).(*types.Pointer)
	if !ok || pointer.Elem() != local {
		t.Fatalf("Box type arg = %v; want exact *Local[int] %p", box.TypeArgs().At(0), local)
	}
}

func TestEmissionUniverseGenericLocalRegistrySupportsMultipleUseOwners(t *testing.T) {
	testProg := newEmissionTestProgram()
	gen := testProg.addPackage(t, "example.com/emission/multiownergen", `package multiownergen
func F[T any](value T) any {
	type Local struct{ Value T }
	return Local{Value: value}
}
`)
	use1 := testProg.addPackage(t, "example.com/emission/multiownerone", `package multiownerone
import "example.com/emission/multiownergen"
func Use() any { return multiownergen.F(1) }
`)
	use2 := testProg.addPackage(t, "example.com/emission/multiownertwo", `package multiownertwo
import "example.com/emission/multiownergen"
func Use() any { return multiownergen.F(1) }
`)
	testProg.ssa.Build()

	origin := gen.ssa.Func("F")
	var instance *ssa.Function
	for fn := range ssautil.AllFunctions(testProg.ssa) {
		if fn != nil && fn.Origin() == origin && len(fn.TypeArgs()) == 1 && types.Identical(fn.TypeArgs()[0], types.Typ[types.Int]) {
			instance = fn
			break
		}
	}
	if instance == nil {
		t.Fatal("shared F[int] instance was not found")
	}

	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: gen.ssa, Files: []*ast.File{gen.file}},
		{SSA: use1.ssa, Files: []*ast.File{use1.file}},
		{SSA: use2.ssa, Files: []*ast.File{use2.file}},
	})
	if err != nil {
		t.Fatal(err)
	}
	owners := universe.useOwners[instance]
	if len(owners) != 2 {
		t.Fatalf("F[int] use owners = %d; want the two consuming packages", len(owners))
	}
	if len(universe.materializedOwners[instance]) != 2 {
		t.Fatalf("F[int] materialized owners = %d; want 2", len(universe.materializedOwners[instance]))
	}
	var physical string
	for owner := range owners {
		ctx, err := universe.functionABIContext(instance, owner)
		if err != nil {
			t.Fatal(err)
		}
		_, legacy, _ := ctx.funcName(instance)
		name, err := universe.physicalName(owner.ssa, instance, legacy)
		if err != nil {
			t.Fatal(err)
		}
		if physical == "" {
			physical = name
		} else if physical != name {
			t.Fatalf("same exact F[int] has owner-dependent physical names %q and %q", physical, name)
		}
	}
}

func TestEmissionUniverseDisambiguatesLinkOnceInstancesAcrossUseOwners(t *testing.T) {
	testProg := newEmissionTestProgram()
	gen := testProg.addPackage(t, "example.com/emission/crossownergen", `package crossownergen
func Helper[X any]() any { var value X; return value }
func Outer[T any]() any {
	type Local struct{ Value T }
	return Helper[Local]()
}
`)
	useInt := testProg.addPackage(t, "example.com/emission/crossownerint", `package crossownerint
import "example.com/emission/crossownergen"
func Use() any { return crossownergen.Outer[int]() }
`)
	useString := testProg.addPackage(t, "example.com/emission/crossownerstring", `package crossownerstring
import "example.com/emission/crossownergen"
func Use() any { return crossownergen.Outer[string]() }
`)
	testProg.ssa.Build()

	origin := gen.ssa.Func("Helper")
	instances := make(map[string]*ssa.Function)
	for fn := range ssautil.AllFunctions(testProg.ssa) {
		if fn == nil || fn.Origin() != origin || len(fn.TypeArgs()) != 1 {
			continue
		}
		local, ok := types.Unalias(fn.TypeArgs()[0]).(*types.Named)
		if !ok {
			continue
		}
		underlying, ok := local.Underlying().(*types.Struct)
		if !ok || underlying.NumFields() != 1 {
			continue
		}
		switch field := underlying.Field(0).Type(); {
		case types.Identical(field, types.Typ[types.Int]):
			instances["int"] = fn
		case types.Identical(field, types.Typ[types.String]):
			instances["string"] = fn
		}
	}
	if instances["int"] == nil || instances["string"] == nil {
		t.Fatalf("cross-owner Helper instances = %#v; want int and string", instances)
	}

	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: gen.ssa, Files: []*ast.File{gen.file}},
		{SSA: useInt.ssa, Files: []*ast.File{useInt.file}},
		{SSA: useString.ssa, Files: []*ast.File{useString.file}},
	})
	if err != nil {
		t.Fatal(err)
	}

	physical := make(map[string]string)
	legacy := make(map[string]string)
	for suffix, fn := range instances {
		owners := universe.sortedUseOwners(fn)
		if len(owners) != 1 {
			t.Fatalf("Helper[%s] owners = %d; want one exact consumer", suffix, len(owners))
		}
		ctx, err := universe.functionABIContext(fn, owners[0])
		if err != nil {
			t.Fatal(err)
		}
		_, legacy[suffix], _ = ctx.funcName(fn)
		physical[suffix], err = universe.physicalName(owners[0].ssa, fn, legacy[suffix])
		if err != nil {
			t.Fatal(err)
		}
	}
	if physical["int"] == physical["string"] {
		t.Fatalf("cross-owner Helper physical symbols collide at %q (legacy %q, %q)", physical["int"], legacy["int"], legacy["string"])
	}
	if legacy["int"] == legacy["string"] && (physical["int"] == legacy["int"] || physical["string"] == legacy["string"]) {
		t.Fatalf("legacy collision %q was not disambiguated for every owner: %q, %q", legacy["int"], physical["int"], physical["string"])
	}
}

func TestEmissionABIDemandMethodSignatureUsesPhysicalClosureFields(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/methodsignature", `package methodsignature
type Base struct{}
func (Base) M() {}
type Host struct{}
func (Host) Accept(value struct {
	Base
	Callback func()
}) {}
func Root() any { return Host{} }
`)
	testProg.ssa.Build()

	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	host := pkg.types.Scope().Lookup("Host").Type().(*types.Named)
	method := host.Method(0)
	sourceParam := method.Type().(*types.Signature).Params().At(0).Type()
	rawMethod := llabi.PublicType(prog.PhysicalType(method.Type(), llssa.InGo)).(*types.Signature)
	physicalParam := rawMethod.Params().At(0).Type()
	if types.Identical(sourceParam, physicalParam) {
		t.Fatalf("method parameter was not converted to its physical closure shape: %v", physicalParam)
	}
	selection := emissionABIDemandMethodSelection(t, testProg.ssa, physicalParam, "M")
	wantWrapper := testProg.ssa.MethodValue(selection)
	if wantWrapper == nil {
		t.Fatalf("physical promoted wrapper for %v.M is nil", physicalParam)
	}
	if resolved, ok := universe.Resolve(wantWrapper); !ok || resolved != wantWrapper {
		t.Fatalf("physical method-parameter wrapper = %v, %v; want exact %v", resolved, ok, wantWrapper)
	}
}

func TestEmissionABIDemandCgoC2AddsGeneratedErrnoInterface(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/cgoc2", `package cgoc2
var _cgo_demo uintptr
func _cgo_runtime_cgocall(fn uintptr, arg uintptr) int
func _C2func_demo() (int, error) {
	_cgo_runtime_cgocall(_cgo_demo, 0)
	return 0, nil
}
`)
	testProg.ssa.Build()
	universe, owner := newEmissionABIDemandTestUniverse(testProg, pkg)
	demands, err := universe.functionABITypeDemands(pkg.ssa.Func("_C2func_demo"), owner)
	if err != nil {
		t.Fatal(err)
	}
	if !emissionABIDemandContains(demands, types.Typ[types.Int32]) {
		t.Fatalf("C2 ABI roots %v omit fallback errno int32", demands)
	}
	errorInterface := types.Universe.Lookup("error").Type().Underlying()
	if !emissionABIDemandContains(demands, errorInterface) {
		t.Fatalf("C2 ABI roots %v omit non-empty error interface", demands)
	}
}

func TestEmissionABIDemandCgoC2WithoutCgocallDoesNotAddErrnoInterface(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/cgoc2nil", `package cgoc2nil
func _C2func_demo() (int, error) { return 0, nil }
`)
	testProg.ssa.Build()
	universe, owner := newEmissionABIDemandTestUniverse(testProg, pkg)
	demands, err := universe.functionABITypeDemands(pkg.ssa.Func("_C2func_demo"), owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(demands) != 0 {
		t.Fatalf("C2 without cgocall ABI roots = %v; want none", demands)
	}
}

func TestEmissionUniverseCgoFirstBlockIgnoresFallbackOnlyFunctionValues(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/cgoexact", `package cgoexact
var Sink any
var _cgo_demo uintptr
func _cgo_runtime_cgocall(fn uintptr, arg uintptr) int
//llgo:link Intrinsic llgo.skip
func Intrinsic()
func _Cfunc_demo() {
	Sink = Intrinsic
	_cgo_runtime_cgocall(_cgo_demo, 0)
}
`)
	testProg.ssa.Build()
	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	if wrapper, ok := universe.intrinsicWrapper(pkg.ssa, pkg.ssa.Func("Intrinsic")); ok {
		t.Fatalf("cgo fallback-only function value materialized intrinsic wrapper %v", wrapper)
	}
}

func TestEmissionUniverseCgoIgnoredIntrinsicArgumentsDoNotMaterializeFunctionValues(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/cgoignoredargs", `package cgoignoredargs
var _cgo_demo uintptr
func _cgo_runtime_cgocall(fn uintptr, arg any) int
//llgo:link Check llgo._cgoCheckPointer
func Check(any, any)
//llgo:link Intrinsic llgo.unreachable
func Intrinsic()
func _Cfunc_demo() {
	Check(Intrinsic, Intrinsic)
	_cgo_runtime_cgocall(_cgo_demo, Intrinsic)
}
`)
	testProg.ssa.Build()
	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	if wrapper, ok := universe.intrinsicWrapper(pkg.ssa, pkg.ssa.Func("Intrinsic")); ok {
		t.Fatalf("cgo ignored checkPointer/cgocall arguments materialized intrinsic wrapper %v", wrapper)
	}
}

func TestEmissionUniverseCgoRejectsUnavailableConsumedProducer(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/cgounavailable", `package cgounavailable
func _cgo_runtime_cgocall(fn any, arg uintptr) int
//llgo:link Intrinsic llgo.unreachable
func Intrinsic()
func _Cfunc_demo() { _cgo_runtime_cgocall(Intrinsic, 0) }
`)
	testProg.ssa.Build()
	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	_, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err == nil || !strings.Contains(err.Error(), "consumes unavailable SSA producer") {
		t.Fatalf("PrepareEmissionUniverse error = %v; want unavailable cgo producer diagnostic", err)
	}
}

func TestEmissionUniverseCgoRejectsNonEmptyVarargs(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/cgovarargs", `package cgovarargs
func Variadic(__llgo_va_list ...any) {}
func _Cfunc_demo() { Variadic(1) }
`)
	testProg.ssa.Build()
	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	_, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err == nil || !strings.Contains(err.Error(), "non-empty varargs slots") {
		t.Fatalf("PrepareEmissionUniverse error = %v; want non-empty cgo varargs diagnostic", err)
	}
}

func TestEmissionUniverseCgoAcceptsEmptyVarargs(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/cgoemptyvarargs", `package cgoemptyvarargs
func Variadic(__llgo_va_list ...any) {}
func _Cfunc_demo() { Variadic() }
`)
	testProg.ssa.Build()
	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	if _, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}}); err != nil {
		t.Fatal(err)
	}
}

func TestEmissionCgoSyntheticMakeSlicePredicateNeedsNoLLVMProgram(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/cgomakeslice", `package cgomakeslice
func _Cfunc_demo(length int) { _ = make([]int, length, 4) }
func _Cfunc_static() { _ = make([]int, 2, 4) }
`)
	testProg.ssa.Build()
	fn := pkg.ssa.Func("_Cfunc_demo")
	var synthetic *ssa.Alloc
	for _, instruction := range fn.Blocks[0].Instrs {
		if alloc, ok := instruction.(*ssa.Alloc); ok && alloc.Comment == "makeslice" {
			synthetic = alloc
			break
		}
	}
	if synthetic == nil {
		t.Fatal("dynamic make with constant capacity has no synthetic makeslice allocation")
	}
	if !emissionSkipsSyntheticMakeSliceAlloc(synthetic) {
		t.Fatal("dynamic make with constant capacity was not recognized as synthetic")
	}
	static := pkg.ssa.Func("_Cfunc_static")
	var staticAlloc *ssa.Alloc
	for _, instruction := range static.Blocks[0].Instrs {
		if alloc, ok := instruction.(*ssa.Alloc); ok && alloc.Comment == "makeslice" {
			staticAlloc = alloc
			break
		}
	}
	if staticAlloc == nil {
		t.Fatal("constant make with constant capacity has no makeslice allocation")
	}
	if emissionSkipsSyntheticMakeSliceAlloc(staticAlloc) {
		t.Fatal("constant in-bounds make was incorrectly classified as synthetic")
	}

	// The ABI-demand frontend intentionally has no LLVM program. This used to
	// call context.syntheticMakeSliceCap and dereference that nil program.
	universe, owner := newEmissionABIDemandTestUniverse(testProg, pkg)
	if _, err := universe.functionABITypeDemands(fn, owner); err != nil {
		t.Fatal(err)
	}
}

func TestEmissionUniverseCgoMacroConsumesOnlyFirstArgument(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/cgomacroexact", `package cgomacroexact
//llgo:link Intrinsic llgo.unreachable
func Intrinsic()
func Callee[T any](result *int, ignored func()) {}
func _Cmacro_demo() int {
	var result int
	Callee[int](&result, Intrinsic)
	return result
}
`)
	testProg.ssa.Build()
	origin := pkg.ssa.Func("Callee")
	var instance *ssa.Function
	for fn := range ssautil.AllFunctions(testProg.ssa) {
		if fn != nil && fn.Origin() == origin && len(fn.TypeArgs()) == 1 && types.Identical(fn.TypeArgs()[0], types.Typ[types.Int]) {
			instance = fn
			break
		}
	}
	if instance == nil {
		t.Fatal("Callee[int] instance was not found")
	}

	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	if universe.Contains(instance) {
		t.Fatalf("C macro materialized ignored static callee %v", instance)
	}
	if wrapper, ok := universe.intrinsicWrapper(pkg.ssa, pkg.ssa.Func("Intrinsic")); ok {
		t.Fatalf("C macro materialized ignored second-argument wrapper %v", wrapper)
	}
}

func TestEmissionABIDemandCgoBuiltinCallUsesFirstBlockLowering(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/cgobuiltinabi", `package cgobuiltinabi
type Base struct{}
func (Base) M() {}
func _Cfunc_demo(values map[struct{ Base }]int, key struct{ Base }) {
	delete(values, key)
}
`)
	testProg.ssa.Build()
	fn := pkg.ssa.Func("_Cfunc_demo")
	mapType := fn.Signature.Params().At(0).Type()
	keyType := types.Unalias(mapType).Underlying().(*types.Map).Key()
	selection := emissionABIDemandMethodSelection(t, testProg.ssa, keyType, "M")
	wrapper := testProg.ssa.MethodValue(selection)
	if wrapper == nil {
		t.Fatalf("anonymous cgo map-key wrapper for %v.M is nil", keyType)
	}

	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	owner := universe.ownerOf(fn)
	demands, err := universe.functionABITypeDemands(fn, owner)
	if err != nil {
		t.Fatal(err)
	}
	if !emissionABIDemandContains(demands, mapType) {
		t.Fatalf("cgo builtin delete ABI roots %v omit map type %v", demands, mapType)
	}
	if resolved, ok := universe.Resolve(wrapper); !ok || resolved != wrapper {
		t.Fatalf("cgo builtin delete wrapper = %v, %v; want exact %v", resolved, ok, wrapper)
	}
}

func TestEmissionUniverseOrdinaryIgnoredDirectFunctionArgumentNeedsNoWrapper(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/ignoredfunctionarg", `package ignoredfunctionarg
//llgo:link Skip llgo.skip
func Skip(func())
//llgo:link Intrinsic llgo.unreachable
func Intrinsic()
func Use() { Skip(Intrinsic) }
`)
	testProg.ssa.Build()
	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	if wrapper, ok := universe.intrinsicWrapper(pkg.ssa, pkg.ssa.Func("Intrinsic")); ok {
		t.Fatalf("ordinary llgo.skip direct function argument materialized wrapper %v", wrapper)
	}
}

func TestEmissionIntrinsicOperandPolicyCoversRegistry(t *testing.T) {
	want := make(map[string]emissionIntrinsicOperandPolicy)
	add := func(policy emissionIntrinsicOperandPolicy, names ...string) {
		for _, name := range names {
			if _, exists := want[name]; exists {
				t.Fatalf("duplicate expected operand policy for %q", name)
			}
			want[name] = policy
		}
	}
	add(emissionIntrinsicNoValues,
		"cstr", "pystr", "skip", "_cgoCheckPointer", "sigjmpbuf",
		"deferData", "unreachable", "stackSave", "coroYield",
		"coroCriticalEnter", "coroCriticalExit", "coroGoexit",
		"coroOSThreadLock", "coroOSThreadUnlock")
	add(emissionIntrinsicRawAllValues, "syscall")
	add(emissionIntrinsicCompileValues,
		"boolToUint8", "atomicLoad", "atomicStore", "atomicCmpXchg",
		"atomicCmpXchgOK", "atomicAddReturnNew", "atomicLoadUnsafe", "atomicStoreUnsafe",
		"coroPark", "coroTimerSleep", "coroPollWait", "coroControlledTimerWait",
		"coroFFICall", "coroHostOperation",
		"atomicXchg", "atomicAdd",
		"atomicSub", "atomicAnd", "atomicNand", "atomicOr", "atomicXor",
		"atomicMax", "atomicMin", "atomicUMax", "atomicUMin")
	add(emissionIntrinsicFirstValue,
		"alloca", "allocCStr", "allocaCStr", "allocaCStrs", "string",
		"stringData", "_Cfunc_CString", "_Cfunc_CBytes", "_Cfunc_GoString",
		"_Cfunc__CMalloc", "_cgo_runtime_cgocall")
	add(emissionIntrinsicFirstTwoValues,
		"advance", "index", "sigsetjmp", "siglongjmp", "_Cfunc_GoStringN",
		"_Cfunc_GoBytes")
	add(emissionIntrinsicFixedBeforeVArg, "pyList", "pyTuple")
	add(emissionIntrinsicFuncAddr, "funcAddr")
	add(emissionIntrinsicFuncPCABI0, "funcPCABI0", "funcPCABIInternal")
	add(emissionIntrinsicAsm, "asm")
	add(emissionIntrinsicRawAllValues, "syscall32", "syscallPtr")

	for name, instruction := range llgoInstrs {
		expected, ok := want[name]
		if !ok {
			t.Errorf("llgo intrinsic %q (%d) has no expected operand policy", name, instruction)
			continue
		}
		got, err := emissionIntrinsicPolicy(instruction)
		if err != nil {
			t.Errorf("llgo intrinsic %q (%d) has no operand policy: %v", name, instruction, err)
		} else if got != expected {
			t.Errorf("llgo intrinsic %q (%d) operand policy = %d; want %d", name, instruction, got, expected)
		}
	}
	for name := range want {
		if _, ok := llgoInstrs[name]; !ok {
			t.Errorf("expected operand policy names unregistered intrinsic %q", name)
		}
	}

	first := ssa.NewConst(constant.MakeInt64(1), types.Typ[types.Int])
	trailing := ssa.NewConst(constant.MakeInt64(2), types.Typ[types.Int])
	values := []ssa.Value{first, trailing}
	for _, test := range []struct {
		name        string
		instruction int
		wantRoots   []ssa.Value
	}{
		{name: "raw-all keeps trailing varargs", instruction: llgoSyscall, wantRoots: []ssa.Value{first, trailing}},
		{name: "compileValues delegates trailing varargs", instruction: llgoAtomicStore, wantRoots: []ssa.Value{first}},
	} {
		t.Run(test.name, func(t *testing.T) {
			roots, err := new(EmissionUniverse).intrinsicCallValueRoots(test.instruction, values, fnHasVArg)
			if err != nil {
				t.Fatal(err)
			}
			if len(roots) != len(test.wantRoots) {
				t.Fatalf("operand roots = %d; want %d", len(roots), len(test.wantRoots))
			}
			for index, root := range roots {
				if root.value != test.wantRoots[index] {
					t.Fatalf("operand root %d = %v; want exact %v", index, root.value, test.wantRoots[index])
				}
				if root.directFunction {
					t.Fatal("ordinary intrinsic operand was classified as a direct function")
				}
			}
		})
	}
}

func TestEmissionABIDemandDoesNotElideEagerIntrinsicArguments(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/eagerintrinsic", `package eagerintrinsic
type Base struct{}
func (Base) M() {}
//llgo:link Check llgo._cgoCheckPointer
func Check(any, any)
//llgo:link Skip llgo.skip
func Skip(any)
func Use() {
	type checked struct{ Base }
	Check(checked{}, nil)
	type skipped struct{ Base }
	Skip(skipped{})
}
`)
	testProg.ssa.Build()
	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}

	seen := make(map[string]bool)
	for _, block := range pkg.ssa.Func("Use").Blocks {
		for _, instruction := range block.Instrs {
			makeInterface, ok := instruction.(*ssa.MakeInterface)
			if !ok {
				continue
			}
			named := emissionABIDemandNamed(makeInterface.X.Type(), "checked")
			if named == nil {
				named = emissionABIDemandNamed(makeInterface.X.Type(), "skipped")
			}
			if named == nil {
				continue
			}
			selection := emissionABIDemandMethodSelection(t, testProg.ssa, named, "M")
			wrapper := testProg.ssa.MethodValue(selection)
			if resolved, ok := universe.Resolve(wrapper); !ok || resolved != wrapper {
				t.Fatalf("eager intrinsic argument wrapper = %v, %v; want exact %v", resolved, ok, wrapper)
			}
			seen[named.Obj().Name()] = true
		}
	}
	if !seen["checked"] || !seen["skipped"] {
		t.Fatalf("eager intrinsic MakeInterface arguments seen = %v; want checked and skipped", seen)
	}
}

func TestEmissionABIDemandInvokeSignatureDropsReceiverBeforeArguments(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/invoke", `package invoke
type I interface { Take(first int, second any) }
func Call(value I, first int, second any) { value.Take(first, second) }
`)
	testProg.ssa.Build()
	prog := ssatest.NewProgram(t, nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	fn := pkg.ssa.Func("Call")
	owner := universe.ownerOf(fn)
	ctx, err := universe.functionABIContext(fn, owner)
	if err != nil {
		t.Fatal(err)
	}
	var invoke *ssa.CallCommon
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(ssa.CallInstruction); ok && call.Common().IsInvoke() {
				invoke = call.Common()
			}
		}
	}
	if invoke == nil || invoke.Signature().Recv() == nil {
		t.Fatal("SSA invoke with retained method receiver was not found")
	}
	physical, ok := universe.physicalInvokeCallSignature(ctx, invoke)
	if !ok {
		t.Fatal("physical invoke signature was not derived")
	}
	if physical.Recv() != nil || physical.Params().Len() != len(invoke.Args) {
		t.Fatalf("physical invoke signature = %v, args=%d; receiver must be closure data, not Params[0]", physical, len(invoke.Args))
	}
	if !types.Identical(physical.Params().At(0).Type(), types.Typ[types.Int]) {
		t.Fatalf("physical invoke first parameter = %v; want int", physical.Params().At(0).Type())
	}
	if _, ok := physical.Params().At(1).Type().Underlying().(*types.Interface); !ok {
		t.Fatalf("physical invoke second parameter = %v; want interface", physical.Params().At(1).Type())
	}
}
