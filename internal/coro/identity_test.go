//go:build !llgo

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

package coro

import (
	"fmt"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

const identityTestSource = `package coroid

type Value struct{}
func (Value) M() {}

type Pointer struct{}
func (*Pointer) M() {}

type Embedded struct{}
func (Embedded) hidden() {}
type Wrapper struct{ Embedded }

func init() {}
func init() {}

func Generic[T any](T) {}
func Outer[T any](value T) func() T {
	return func() T { return value }
}

func A() {
	type Local int
	var value Local
	Generic(value)
	_ = func() { _ = func() {} }
}

func B() {
	type Local int
	var value Local
	Generic(value)
}

func instantiate() {
	Generic(1)
	Generic("x")
	Generic[interface{ M() }](nil)
	_ = Outer(1)
	_ = Outer("x")
}

func wrappers(value Wrapper) {
	value.hidden()
	_ = Wrapper.hidden
	_ = value.hidden
}
`

func TestStableFunctionIDIndependentOfCheckoutPath(t *testing.T) {
	progA, _ := buildCoroTestSSA(t, "/first/checkout/source.go", identityTestSource)
	progB, _ := buildCoroTestSSA(t, "/other/checkout/source.go", identityTestSource)

	idsA := stableIDSet(t, progA, FunctionIDConfig{})
	idsB := stableIDSet(t, progB, FunctionIDConfig{})
	if len(idsA) != len(idsB) {
		t.Fatalf("function count differs: %d vs %d", len(idsA), len(idsB))
	}
	for i := range idsA {
		if idsA[i] != idsB[i] {
			t.Fatalf("FunctionID differs across checkout paths at %d:\nA: %s\nB: %s", i, idsA[i], idsB[i])
		}
	}
}

func TestStableFunctionIDDistinguishesFunctionsAndInstances(t *testing.T) {
	prog, _ := buildCoroTestSSA(t, "source.go", identityTestSource)
	functions := matchingFunctions(prog, func(*ssa.Function) bool { return true })
	seen := make(map[FunctionID]*ssa.Function, len(functions))
	for _, fn := range functions {
		id, err := StableFunctionID(fn, FunctionIDConfig{})
		if err != nil {
			t.Fatalf("StableFunctionID(%s): %v", fn, err)
		}
		if previous := seen[id]; previous != nil && previous != fn {
			t.Fatalf("FunctionID collision: %s and %s", previous, fn)
		}
		seen[id] = fn
	}

	instances := matchingFunctions(prog, func(fn *ssa.Function) bool {
		origin := fn.Origin()
		return origin != nil && origin.Name() == "Generic"
	})
	if len(instances) != 5 {
		t.Fatalf("got %d Generic instances, want 5: %v", len(instances), instances)
	}
	instanceIDs := make(map[FunctionID]bool)
	for _, fn := range instances {
		id, err := StableFunctionID(fn, FunctionIDConfig{})
		if err != nil {
			t.Fatal(err)
		}
		instanceIDs[id] = true
	}
	if len(instanceIDs) != len(instances) {
		t.Fatalf("generic instances collided: %v", instances)
	}

	closures := matchingFunctions(prog, func(fn *ssa.Function) bool {
		origin := fn.Origin()
		return origin != nil && origin.Parent() != nil && origin.Parent().Name() == "Outer"
	})
	if len(closures) != 2 {
		t.Fatalf("got %d instantiated generic closures, want 2: %v", len(closures), closures)
	}
	closureIDs := make(map[FunctionID]bool)
	for _, fn := range closures {
		id, err := StableFunctionID(fn, FunctionIDConfig{})
		if err != nil {
			t.Fatal(err)
		}
		closureIDs[id] = true
	}
	if len(closureIDs) != len(closures) {
		t.Fatalf("instantiated generic closures collided: %v", closures)
	}
}

func TestStableFunctionIDIncludesABIAndResolvedIdentity(t *testing.T) {
	_, pkg := buildCoroTestSSA(t, "source.go", "package coroid; func F() {}")
	fn := packageFunction(t, pkg, "F")
	base, err := StableFunctionID(fn, FunctionIDConfig{})
	if err != nil {
		t.Fatal(err)
	}
	changedABI, err := StableFunctionID(fn, FunctionIDConfig{CoroABI: "next"})
	if err != nil {
		t.Fatal(err)
	}
	if base == changedABI {
		t.Fatal("CoroABI did not affect FunctionID")
	}
	resolved, err := StableFunctionID(fn, FunctionIDConfig{
		ResolveLinkIdentity: func(*ssa.Function) (string, error) { return "final.symbol", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if base == resolved {
		t.Fatalf("resolved link identity did not affect digest: %s", resolved)
	}
	if got, want := len(resolved), len(FunctionIDSchema)+1+64; got != want {
		t.Fatalf("FunctionID length = %d, want %d", got, want)
	}
}

func TestStableFunctionIDArchiveReadyRequirements(t *testing.T) {
	_, pkg := buildCoroTestSSA(t, "source.go", "package coroid; func F() {}")
	fn := packageFunction(t, pkg, "F")
	if _, err := StableFunctionID(fn, FunctionIDConfig{ArchiveReady: true}); err == nil || !strings.Contains(err.Error(), "coroutine ABI") {
		t.Fatalf("missing coroutine ABI error = %v", err)
	}
	if _, err := StableFunctionID(fn, FunctionIDConfig{
		ArchiveReady: true, CoroABI: "coro-v1",
	}); err == nil || !strings.Contains(err.Error(), "scheduler ABI") {
		t.Fatalf("missing scheduler ABI error = %v", err)
	}
	if _, err := StableFunctionID(fn, FunctionIDConfig{
		ArchiveReady: true, CoroABI: "coro-v1", SchedulerABI: "sched-v1",
	}); err == nil || !strings.Contains(err.Error(), "link identity") {
		t.Fatalf("missing link resolver error = %v", err)
	}
	if _, err := StableFunctionID(fn, FunctionIDConfig{
		ArchiveReady: true, CoroABI: "coro-v1", SchedulerABI: "sched-v1",
		ResolveLinkIdentity: func(*ssa.Function) (string, error) {
			return "final.symbol", nil
		},
	}); err == nil || !strings.Contains(err.Error(), "package key") {
		t.Fatalf("missing package resolver error = %v", err)
	}
	_, err := StableFunctionID(fn, FunctionIDConfig{
		ArchiveReady: true, CoroABI: "coro-v1", SchedulerABI: "sched-v1",
		ResolveLinkIdentity: func(*ssa.Function) (string, error) {
			return "final.symbol", nil
		},
		CanonicalPackageKey: func(pkg *types.Package) (string, error) {
			return "variant:" + pkg.Path(), nil
		},
	})
	if err != nil {
		t.Fatalf("archive-ready identity: %v", err)
	}
}

func TestStableFunctionIDRejectsUnknownSynthetic(t *testing.T) {
	prog, _ := buildCoroTestSSA(t, "source.go", "package coroid")
	sig := types.NewSignatureType(nil, nil, nil, types.NewTuple(), types.NewTuple(), false)
	fn := prog.NewFunction("mystery", sig, "unstable human description")
	if _, err := StableFunctionID(fn, FunctionIDConfig{}); err == nil || !strings.Contains(err.Error(), "unsupported synthetic") {
		t.Fatalf("unknown synthetic error = %v", err)
	}
	id, err := StableFunctionID(fn, FunctionIDConfig{
		ResolveSynthetic: func(got *ssa.Function) (string, bool, error) {
			if got != fn {
				return "", false, fmt.Errorf("unexpected function")
			}
			return "llgo.custom.mystery.v0", true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(id), len(FunctionIDSchema)+1+64; got != want {
		t.Fatalf("custom synthetic FunctionID length = %d, want %d", got, want)
	}
}

func TestStableFunctionIDImportedGenericMethods(t *testing.T) {
	_, pkg := buildCoroTestSSA(t, "source.go", `package coroid

import "sync/atomic"

func use(pointer *atomic.Pointer[int]) {
	_ = pointer.Load()
	pointer.Store(nil)
}
	`)

	var methods []*ssa.Function
	for _, block := range packageFunction(t, pkg, "use").Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			callee := call.Common().StaticCallee()
			if callee == nil {
				continue
			}
			declared := callee
			if origin := callee.Origin(); origin != nil {
				declared = origin
			}
			if declared.Name() == "Load" || declared.Name() == "Store" {
				methods = append(methods, callee)
			}
		}
	}
	if len(methods) != 2 {
		t.Fatalf("got %d imported generic methods, want Load and Store: %v", len(methods), methods)
	}
	seen := make(map[FunctionID]*ssa.Function, len(methods))
	for _, method := range methods {
		origin := method.Origin()
		if origin == nil {
			t.Fatalf("imported generic method %s has no origin", method)
		}
		receiverParams := origin.Signature.RecvTypeParams()
		if receiverParams == nil || receiverParams.Len() != 1 {
			t.Fatalf("origin %s has receiver type parameters %v, want one", origin, receiverParams)
		}
		if parent := receiverParams.At(0).Obj().Parent(); parent != nil {
			t.Fatalf("origin %s receiver type parameter unexpectedly has lexical parent %s", origin, parent)
		}
		id, err := StableFunctionID(method, FunctionIDConfig{})
		if err != nil {
			t.Fatalf("StableFunctionID(%s): %v", method, err)
		}
		if previous := seen[id]; previous != nil {
			t.Fatalf("FunctionID collision: %s and %s", previous, method)
		}
		seen[id] = method
	}
}

func TestStableFunctionIDGenericLocalNamedTypes(t *testing.T) {
	source := `package coroid

func Generic[T any]() {}

func Outer[T any]() {
	type Local struct { Value T }
	Generic[Local]()
}

func OuterUnused[T any]() {
	type Local struct{}
	Generic[Local]()
}

func OuterClosure[T any]() {
	func() {
		type Local struct{}
		Generic[Local]()
	}()
}

func OuterScopes[T any](flag bool) {
	if flag {
		type Local struct{}
		Generic[Local]()
	}
	if !flag {
		type Local struct{}
		Generic[Local]()
	}
}

func instantiate() {
	Outer[int]()
	Outer[string]()
	OuterUnused[int]()
	OuterUnused[string]()
	OuterClosure[int]()
	OuterClosure[string]()
	OuterScopes[int](true)
}
`
	prog, pkg := buildCoroTestSSA(t, "/first/checkout/source.go", source)

	instances := matchingFunctions(prog, func(fn *ssa.Function) bool {
		origin := fn.Origin()
		if origin == nil || origin.Name() != "Generic" || len(fn.TypeArgs()) != 1 {
			return false
		}
		named, ok := types.Unalias(fn.TypeArgs()[0]).(*types.Named)
		return ok && named.Obj().Parent() == nil
	})
	if len(instances) != 8 {
		t.Fatalf("got %d Generic instances over fresh local types, want 8: %v", len(instances), instances)
	}
	ids := make(map[FunctionID]*ssa.Function, len(instances))
	emptyStructIDs := make(map[FunctionID]bool)
	for _, instance := range instances {
		id, err := StableFunctionID(instance, FunctionIDConfig{})
		if err != nil {
			t.Fatalf("StableFunctionID(%s): %v", instance, err)
		}
		if previous := ids[id]; previous != nil {
			t.Fatalf("FunctionID collision: %s and %s", previous, instance)
		}
		ids[id] = instance
		named := types.Unalias(instance.TypeArgs()[0]).(*types.Named)
		underlying, ok := named.Underlying().(*types.Struct)
		if ok && underlying.NumFields() == 0 {
			emptyStructIDs[id] = true
		}
	}
	if len(emptyStructIDs) != 6 {
		t.Fatalf("identical-underlying local types produced %d distinct IDs, want 6", len(emptyStructIDs))
	}
	if _, err := AnalyzeSSA(prog, Roots{{Function: packageFunction(t, pkg, "instantiate"), Demand: AsyncDemand}}, SSAConfig{}); err != nil {
		t.Fatalf("AnalyzeSSA with instantiated local named types: %v", err)
	}

	// Distinct generic instances over fresh local named types may have the same
	// raw presentation key even though their structural FunctionIDs differ. The
	// emission universe must preserve both exact pointers, and AnalyzeSSA must
	// produce the same stable plan regardless of frontend input order.
	rawOwners := make(map[string]*ssa.Function)
	hasRawTie := false
	for _, instance := range instances {
		key := rawSSAFunctionKey(instance)
		if previous := rawOwners[key]; previous != nil && previous != instance {
			hasRawTie = true
		}
		rawOwners[key] = instance
	}
	if !hasRawTie {
		t.Fatal("generic instances over fresh local named types did not exercise an equal raw sort key")
	}

	allFunctions := matchingFunctions(prog, func(*ssa.Function) bool { return true })
	reversedFunctions := append([]*ssa.Function(nil), allFunctions...)
	for left, right := 0, len(reversedFunctions)-1; left < right; left, right = left+1, right-1 {
		reversedFunctions[left], reversedFunctions[right] = reversedFunctions[right], reversedFunctions[left]
	}
	universeA, err := NewSSAEmissionUniverse(prog, allFunctions)
	if err != nil {
		t.Fatal(err)
	}
	universeB, err := NewSSAEmissionUniverse(prog, reversedFunctions)
	if err != nil {
		t.Fatal(err)
	}
	root := packageFunction(t, pkg, "instantiate")
	planA, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, SSAConfig{EmissionUniverse: universeA})
	if err != nil {
		t.Fatal(err)
	}
	planB, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: AsyncDemand}}, SSAConfig{EmissionUniverse: universeB})
	if err != nil {
		t.Fatal(err)
	}
	functionsA, functionsB := planA.Functions(), planB.Functions()
	if len(functionsA) != len(functionsB) {
		t.Fatalf("plan function counts differ by universe input order: %d and %d", len(functionsA), len(functionsB))
	}
	for i := range functionsA {
		if functionsA[i] != functionsB[i] {
			t.Fatalf("plan function %d differs by universe input order:\nA: %+v\nB: %+v", i, functionsA[i], functionsB[i])
		}
	}

	otherProg, _ := buildCoroTestSSA(t, "/other/checkout/source.go", source)
	firstIDs := stableIDSet(t, prog, FunctionIDConfig{})
	otherIDs := stableIDSet(t, otherProg, FunctionIDConfig{})
	if len(firstIDs) != len(otherIDs) {
		t.Fatalf("function count differs across checkout paths: %d vs %d", len(firstIDs), len(otherIDs))
	}
	for i := range firstIDs {
		if firstIDs[i] != otherIDs[i] {
			t.Fatalf("generic-local FunctionID differs across checkout paths at %d: %s vs %s", i, firstIDs[i], otherIDs[i])
		}
	}
}

func TestStableFunctionIDResolvesUnreachableLocalTypeOwner(t *testing.T) {
	_, pkg := buildCoroTestSSA(t, "source.go", `package coroid
func Generic[T any]() {}
func Outer[T any]() {
	type Local struct{}
	Generic[Local]()
}
func instantiate() { Outer[int]() }
`)
	findCallee := func(fn *ssa.Function, originName string) *ssa.Function {
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				callee := call.Common().StaticCallee()
				if callee != nil && callee.Origin() != nil && callee.Origin().Name() == originName {
					return callee
				}
			}
		}
		t.Fatalf("%s has no call to an instance of %s", fn, originName)
		return nil
	}
	outer := findCallee(packageFunction(t, pkg, "instantiate"), "Outer")
	generic := findCallee(outer, "Generic")
	local := types.Unalias(generic.TypeArgs()[0]).(*types.Named)
	canonicalObject := types.NewTypeName(local.Obj().Pos(), local.Obj().Pkg(), "Local[int]", nil)
	canonicalLocal := types.NewNamed(canonicalObject, local.Underlying(), nil)
	if declaration, err := lexicalTypeDeclaration(canonicalLocal.Obj()); err != nil || declaration.Name() != "Local" || declaration.Pos() != local.Obj().Pos() {
		t.Fatalf("canonical local lexical declaration = %v, %v; want source Local at %v", declaration, err, local.Obj().Pos())
	}

	// Model a tool that retains an isolated instance after removing the source
	// roots that carried its reverse provenance. x/tools exposes no owner link
	// on the fresh local *types.Named itself.
	delete(pkg.Members, "instantiate")
	delete(pkg.Members, "Outer")
	delete(pkg.Members, "Generic")
	if _, err := StableFunctionID(generic, FunctionIDConfig{}); err == nil || !strings.Contains(err.Error(), "cannot find SSA owner") {
		t.Fatalf("unreachable local owner error = %v", err)
	}

	id, err := StableFunctionID(generic, FunctionIDConfig{
		ResolveLocalTypeOwner: func(got *types.Named) (*ssa.Function, bool, error) {
			if got != local {
				return nil, false, fmt.Errorf("unexpected local type %v", got)
			}
			return outer, true, nil
		},
	})
	if err != nil {
		t.Fatalf("StableFunctionID with recorded local owner: %v", err)
	}
	if got, want := len(id), len(FunctionIDSchema)+1+64; got != want {
		t.Fatalf("FunctionID length = %d, want %d", got, want)
	}
}

func TestFunctionIDTypeKeysAreStructuralAndScopeAware(t *testing.T) {
	pkg := types.NewPackage("example.test/types", "typespkg")
	localScopeA := types.NewScope(pkg.Scope(), token.NoPos, token.NoPos, "A")
	localScopeB := types.NewScope(pkg.Scope(), token.NoPos, token.NoPos, "B")
	localObjectA := types.NewTypeName(token.NoPos, pkg, "Local", nil)
	localObjectB := types.NewTypeName(token.NoPos, pkg, "Local", nil)
	localScopeA.Insert(localObjectA)
	localScopeB.Insert(localObjectB)
	localA := types.NewNamed(localObjectA, types.Typ[types.Int], nil)
	localB := types.NewNamed(localObjectB, types.Typ[types.Int], nil)

	paramScope := types.NewScope(pkg.Scope(), token.NoPos, token.NoPos, "type-parameters")
	paramObject := types.NewTypeName(token.NoPos, pkg, "T", nil)
	paramScope.Insert(paramObject)
	param := types.NewTypeParam(paramObject, types.Universe.Lookup("comparable").Type())
	params := types.NewTuple(types.NewVar(token.NoPos, pkg, "value", localA))
	results := types.NewTuple(types.NewVar(token.NoPos, pkg, "result", types.Typ[types.String]))
	signature := types.NewSignatureType(nil, nil, nil, params, results, false)
	method := types.NewFunc(token.NoPos, pkg, "method", types.NewSignatureType(
		nil, nil, nil, types.NewTuple(), types.NewTuple(), false,
	))
	iface := types.NewInterfaceType([]*types.Func{method}, nil).Complete()
	union := types.NewUnion([]*types.Term{
		types.NewTerm(true, types.Typ[types.Int]),
		types.NewTerm(false, localA),
	})
	structType := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, pkg, "field", localA, false),
	}, []string{`json:"field"`})

	typesToCheck := []types.Type{
		types.Typ[types.Int],
		types.NewPointer(types.Typ[types.Bool]),
		types.NewSlice(types.Typ[types.String]),
		types.NewArray(types.Typ[types.Uint8], 7),
		types.NewMap(types.Typ[types.String], localA),
		types.NewChan(types.RecvOnly, localA),
		localA,
		localB,
		signature,
		params,
		structType,
		iface,
		param,
		union,
	}
	builder := functionIDBuilder{config: FunctionIDConfig{}}
	seen := make(map[string]types.Type)
	for _, typ := range typesToCheck {
		key, err := builder.typeKey(typ)
		if err != nil {
			t.Fatalf("typeKey(%s): %v", typ, err)
		}
		if key == "" || !strings.Contains(key, "kind=") {
			t.Fatalf("typeKey(%s) = %q", typ, key)
		}
		if previous := seen[key]; previous != nil {
			t.Fatalf("type identity collision: %s and %s", previous, typ)
		}
		seen[key] = typ
	}

	embeddedField := types.NewField(token.NoPos, pkg, "Local", localA, true)
	namedField := types.NewField(token.NoPos, pkg, "Local", localA, false)
	embeddedKey, err := builder.typeKey(types.NewStruct([]*types.Var{embeddedField}, nil))
	if err != nil {
		t.Fatal(err)
	}
	namedKey, err := builder.typeKey(types.NewStruct([]*types.Var{namedField}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if embeddedKey == namedKey {
		t.Fatal("embedded and named struct fields have the same type identity")
	}

	embeddedInterface := types.NewInterfaceType(nil, []types.Type{iface}).Complete()
	ifaceKey, err := builder.typeKey(iface)
	if err != nil {
		t.Fatal(err)
	}
	embeddedInterfaceKey, err := builder.typeKey(embeddedInterface)
	if err != nil {
		t.Fatal(err)
	}
	if ifaceKey != embeddedInterfaceKey {
		t.Fatal("equivalent explicit and embedded interfaces have different identities")
	}

	reversedUnion := types.NewUnion([]*types.Term{
		types.NewTerm(false, localA),
		types.NewTerm(true, types.Typ[types.Int]),
	})
	unionKey, err := builder.typeKey(union)
	if err != nil {
		t.Fatal(err)
	}
	reversedUnionKey, err := builder.typeKey(reversedUnion)
	if err != nil {
		t.Fatal(err)
	}
	if unionKey != reversedUnionKey {
		t.Fatal("union source order affected type identity")
	}

	parentless := types.NewTypeParam(types.NewTypeName(token.NoPos, pkg, "P", nil), types.Universe.Lookup("any").Type())
	if _, err := builder.typeKey(parentless); err == nil || !strings.Contains(err.Error(), "no lexical owner") {
		t.Fatalf("parentless type parameter error = %v", err)
	}
	genericSignature := types.NewSignatureType(nil, nil, []*types.TypeParam{param}, params, results, false)
	if _, err := builder.typeKey(genericSignature); err == nil || !strings.Contains(err.Error(), "generic function type") {
		t.Fatalf("generic signature error = %v", err)
	}
}

func stableIDSet(t *testing.T, prog *ssa.Program, config FunctionIDConfig) []FunctionID {
	t.Helper()
	functions := matchingFunctions(prog, func(*ssa.Function) bool { return true })
	ids := make([]FunctionID, 0, len(functions))
	for _, fn := range functions {
		id, err := StableFunctionID(fn, config)
		if err != nil {
			t.Fatalf("StableFunctionID(%s): %v", fn, err)
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
