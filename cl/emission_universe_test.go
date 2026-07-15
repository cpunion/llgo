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
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	"github.com/goplus/llgo/internal/typepatch"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/goplus/llgo/ssa/abi"
	"golang.org/x/tools/go/ssa"
)

type emissionTestImporter struct {
	packages map[string]*types.Package
	fallback types.Importer
}

func (p *emissionTestImporter) Import(path string) (*types.Package, error) {
	if pkg := p.packages[path]; pkg != nil {
		return pkg, nil
	}
	return p.fallback.Import(path)
}

type emissionTestPackage struct {
	ssa   *ssa.Package
	file  *ast.File
	types *types.Package
}

type emissionTestProgram struct {
	fset     *token.FileSet
	ssa      *ssa.Program
	importer *emissionTestImporter
}

func newEmissionTestProgram() *emissionTestProgram {
	fset := token.NewFileSet()
	return &emissionTestProgram{
		fset: fset,
		ssa:  ssa.NewProgram(fset, ssa.SanityCheckFunctions|ssa.InstantiateGenerics),
		importer: &emissionTestImporter{
			packages: make(map[string]*types.Package),
			fallback: importer.Default(),
		},
	}
}

func (p *emissionTestProgram) addPackage(t *testing.T, path, src string) emissionTestPackage {
	t.Helper()
	file, err := parser.ParseFile(p.fset, path+".go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Implicits:  make(map[ast.Node]types.Object),
		Scopes:     make(map[ast.Node]*types.Scope),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Instances:  make(map[*ast.Ident]types.Instance),
	}
	pkg := types.NewPackage(path, file.Name.Name)
	conf := types.Config{Importer: p.importer}
	if err := types.NewChecker(&conf, p.fset, pkg, info).Files([]*ast.File{file}); err != nil {
		t.Fatal(err)
	}
	p.importer.packages[path] = pkg
	ssaPkg := p.ssa.CreatePackage(pkg, []*ast.File{file}, info, true)
	return emissionTestPackage{ssa: ssaPkg, file: file, types: pkg}
}

func preparePatchedEmissionTest(t *testing.T, originalSource, altSource string) (*EmissionUniverse, emissionTestPackage, emissionTestPackage, func()) {
	t.Helper()
	testProg := newEmissionTestProgram()
	original := testProg.addPackage(t, "example.com/emission/p", originalSource)
	alt := testProg.addPackage(t, abi.PatchPathPrefix+"example.com/emission/p", altSource)
	testProg.ssa.Build()

	prog := llssa.NewProgram(nil)
	patches := Patches{
		"example.com/emission/p": {
			Alt:   alt.ssa,
			Types: typepatch.Clone(alt.types),
		},
	}
	universe, err := PrepareEmissionUniverse(prog, patches, []EmissionPackage{{
		SSA:   original.ssa,
		Files: []*ast.File{original.file, alt.file},
	}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return universe, original, alt, prog.Dispose
}

func TestEmissionUniversePatchCanonicalizationAndInitRoles(t *testing.T) {
	universe, original, alt, dispose := preparePatchedEmissionTest(t, `package p
func F() int { return 1 }
func Keep() int { return F() }
`, `package p
func F() int { return 2 }
`)
	defer dispose()

	originalF, altF := original.ssa.Func("F"), alt.ssa.Func("F")
	if got, ok := universe.Resolve(originalF); !ok || got != altF {
		t.Fatalf("Resolve(original F) = %v, %v; want exact alt F", got, ok)
	}
	if universe.Contains(originalF) {
		t.Fatal("replaced original F remains canonical")
	}
	if !universe.Contains(altF) || !universe.Contains(original.ssa.Func("Keep")) {
		t.Fatal("effective alt F or original Keep is absent")
	}

	// Patch and original package initializers intentionally have distinct final
	// symbols: p.init and p.init$hasPatch.
	if !universe.Contains(alt.ssa.Func("init")) || !universe.Contains(original.ssa.Func("init")) {
		t.Fatal("patch/original init dual role was collapsed")
	}
	if universe.finalIdentity(alt.ssa.Func("init")) == universe.finalIdentity(original.ssa.Func("init")) {
		t.Fatal("patch/original init final identities collide")
	}

	copyOfFunctions := universe.Functions()
	copyOfFunctions[0] = nil
	if universe.Functions()[0] == nil {
		t.Fatal("Functions exposed mutable storage")
	}
}

func TestEmissionUniversePatchSignatureIgnoresParameterAndResultNames(t *testing.T) {
	universe, original, alt, dispose := preparePatchedEmissionTest(t, `package p
func ReadTrace(input []byte) (buf []byte) { return input }
func Use(input []byte) []byte { return ReadTrace(input) }
`, `package p
func ReadTrace(value []byte) []byte { return value }
`)
	defer dispose()

	originalFn, replacement := original.ssa.Func("ReadTrace"), alt.ssa.Func("ReadTrace")
	if got, ok := universe.Resolve(originalFn); !ok || got != replacement {
		t.Fatalf("Resolve(named-signature original) = %v, %v; want replacement %v", got, ok, replacement)
	}
	if universe.Contains(originalFn) || !universe.Contains(replacement) {
		t.Fatal("parameter/result names prevented ABI-equivalent patch canonicalization")
	}
}

func TestEmissionUniversePatchCanSuppressOldInit(t *testing.T) {
	universe, original, alt, dispose := preparePatchedEmissionTest(t, `package p
var Original = sideEffect()
func sideEffect() int { return 1 }
`, `package p
//llgo:skip init
type PatchControl struct{}
var Alternate = sideEffect()
func sideEffect() int { return 2 }
`)
	defer dispose()
	if universe.Contains(original.ssa.Func("init")) {
		t.Fatal("skipped original init remains in the canonical universe")
	}
	if resolved, ok := universe.Resolve(original.ssa.Func("init")); !ok || resolved != alt.ssa.Func("init") {
		t.Fatalf("Resolve(skipped original init) = %v, %v; want alternate public init", resolved, ok)
	}
	if !universe.Contains(alt.ssa.Func("init")) {
		t.Fatal("alternate init is absent")
	}
}

func TestEmissionUniverseRejectsReachableSkippedOriginalWithoutReplacement(t *testing.T) {
	testProg := newEmissionTestProgram()
	original := testProg.addPackage(t, "example.com/emission/p", `package p
func Victim() {}
func Keep() { Victim() }
`)
	alt := testProg.addPackage(t, abi.PatchPathPrefix+"example.com/emission/p", `package p
//llgo:skip Victim
type PatchControl struct{}
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	_, err := PrepareEmissionUniverse(prog, Patches{
		"example.com/emission/p": {Alt: alt.ssa, Types: typepatch.Clone(alt.types)},
	}, []EmissionPackage{{SSA: original.ssa, Files: []*ast.File{original.file, alt.file}}})
	if err == nil || !strings.Contains(err.Error(), "excluded original") || !strings.Contains(err.Error(), "Victim") {
		t.Fatalf("PrepareEmissionUniverse error = %v; want reachable excluded-original failure", err)
	}
}

func TestEmissionUniverseMaterializesClosuresMethodsAndGenericInstances(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/features", `package features
type hidden struct{}
func (hidden) M() {}
type Outer struct{ hidden }
func Closure() func() { return func() { var value hidden; value.M() } }
func Bound() func() { var value hidden; return value.M }
func Structural() any { return struct{ hidden }{} }
func Generic[T any](value T) T { return value }
func UseGeneric() int { return Generic[int](1) }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}

	closure := pkg.ssa.Func("Closure")
	if len(closure.AnonFuncs) != 1 || !universe.Contains(closure.AnonFuncs[0]) {
		t.Fatal("nested closure was not pre-materialized")
	}
	method := pkg.ssa.Prog.MethodValue(pkg.ssa.Prog.MethodSets.MethodSet(pkg.types.Scope().Lookup("hidden").Type()).At(0))
	if !universe.Contains(method) {
		t.Fatal("unexported value-receiver method was not selected")
	}

	foundBound, foundInstance := false, false
	foundPromoted, foundStructural := false, false
	for _, fn := range universe.Functions() {
		foundBound = foundBound || strings.HasSuffix(fn.Name(), "$bound")
		foundInstance = foundInstance || fn.Origin() == pkg.ssa.Func("Generic")
		if !strings.HasPrefix(fn.Synthetic, "wrapper for ") || fn.Signature.Recv() == nil {
			continue
		}
		recv := types.Unalias(fn.Signature.Recv().Type())
		if named, ok := recv.(*types.Named); ok && named.Obj().Name() == "Outer" {
			foundPromoted = true
		}
		if _, ok := recv.(*types.Struct); ok {
			foundStructural = true
		}
	}
	if !foundBound {
		t.Fatal("bound-method wrapper was not pre-materialized")
	}
	if !foundInstance {
		t.Fatal("generic instance was not pre-materialized")
	}
	if !foundPromoted {
		t.Fatal("promoted named-type method wrapper was not pre-materialized")
	}
	if !foundStructural {
		t.Fatal("anonymous structural-type method wrapper was not pre-materialized")
	}
	if universe.Contains(pkg.ssa.Func("Generic")) {
		t.Fatal("uninstantiated generic origin should not be emitted")
	}
}

func TestEmissionUniverseCoalescesEquivalentLocalPromotedWrappers(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/localwrapper", `package localwrapper
type Base struct{}
func (Base) M() {}
func Value(first bool) any {
	if first {
		type local struct{ Base }
		return local{}
	}
	{
		type local struct{ Base }
		return local{}
	}
}
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	identities := make(map[string]*ssa.Function)
	for _, fn := range universe.Functions() {
		if isLocallyMergedPromotedWrapper(fn) && fn.Name() == "M" {
			identity := universe.finalIdentity(fn)
			if previous := identities[identity]; previous != nil && previous != fn {
				t.Fatalf("distinct local wrappers share final identity %q", identity)
			}
			identities[identity] = fn
		}
	}
	// Both lexical types have the same local name, receiver layout, callee, and
	// wrapper body. Their ABI descriptors therefore share one exact structural
	// value wrapper and one PtrToThis wrapper; the differing-layout/callee tests
	// below ensure that only genuinely equivalent wrappers coalesce.
	if len(identities) != 2 {
		t.Fatalf("equivalent local promoted wrapper identities = %d; want one value and one pointer symbol", len(identities))
	}
}

func TestEmissionUniverseSeparatesLocalPromotedWrappersWithDifferentCallee(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/localwrapperbad", `package localwrapperbad
type Left struct{}
func (Left) M() {}
type Right struct{}
func (Right) M() {}
func Value(first bool) any {
	if first {
		type local struct{ Left }
		return local{}
	}
	{
		type local struct{ Right }
		return local{}
	}
}
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	identities := make(map[string]none)
	for _, fn := range universe.Functions() {
		if isLocallyMergedPromotedWrapper(fn) && fn.Name() == "M" {
			identities[universe.finalIdentity(fn)] = none{}
		}
	}
	if len(identities) < 4 {
		t.Fatalf("different-callee local wrapper identities = %d; want distinct physical symbols", len(identities))
	}
}

func TestEmissionUniverseWrapperSymbolIncludesEmbeddedFieldStructure(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/localwrapperoffset", `package localwrapperoffset
type Base struct{}
func (Base) M() {}
type Pad struct{ value uintptr }
func Value(first bool) any {
	if first {
		type local struct{ Base }
		return local{}
	}
	{
		type local struct { Pad; Base }
		return local{}
	}
}

`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	identities := make(map[string]none)
	for _, fn := range universe.Functions() {
		if isLocallyMergedPromotedWrapper(fn) && fn.Name() == "M" {
			identities[universe.finalIdentity(fn)] = none{}
		}
	}
	if len(identities) < 4 {
		t.Fatalf("field-offset local wrapper identities = %d; field #0/#1 wrappers collided", len(identities))
	}
}

func TestEmissionUniverseWrapperSymbolIncludesLocalReceiverLayout(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/localwrapperlayout", `package localwrapperlayout
type Base struct{}
func (Base) M() {}
func Value(first bool) any {
	if first {
		type local struct { Base; X int }
		return local{}
	}
	{
		type local struct { Base; Y string }
		return local{}
	}
}
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	identities := make(map[string]none)
	for _, fn := range universe.Functions() {
		if isLocallyMergedPromotedWrapper(fn) && fn.Name() == "M" {
			identities[universe.finalIdentity(fn)] = none{}
		}
	}
	if len(identities) < 4 {
		t.Fatalf("same-field-index local wrapper identities = %d; receiver layouts collided", len(identities))
	}
}

func TestEmissionUniverseMaterializesCrossPackageDirectCallsAndClosures(t *testing.T) {
	testProg := newEmissionTestProgram()
	callee := testProg.addPackage(t, "example.com/emission/callee", `package callee
func Direct() {}
func Closure() func() { return func() { Direct() } }
`)
	caller := testProg.addPackage(t, "example.com/emission/caller", `package caller
import "example.com/emission/callee"
func Call() func() { callee.Direct(); return callee.Closure() }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: caller.ssa, Files: []*ast.File{caller.file}},
		{SSA: callee.ssa, Files: []*ast.File{callee.file}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !universe.Contains(callee.ssa.Func("Direct")) || !universe.Contains(callee.ssa.Func("Closure")) {
		t.Fatal("cross-package static callees are absent")
	}
	closure := callee.ssa.Func("Closure")
	if len(closure.AnonFuncs) != 1 || !universe.Contains(closure.AnonFuncs[0]) {
		t.Fatal("cross-package callee closure is absent")
	}
	if owner := universe.ownerOf(closure.AnonFuncs[0]); owner == nil || owner.pkgPath != "example.com/emission/callee" {
		t.Fatalf("closure owner = %+v; want callee package", owner)
	}
}

func TestEmissionUniverseCrossPackageGlobalKeepsMethodOwner(t *testing.T) {
	testProg := newEmissionTestProgram()
	intrinsics := testProg.addPackage(t, "example.com/emission/ownerintrinsics", `package ownerintrinsics
//llgo:link Intrinsic llgo.unreachable
func Intrinsic()
`)
	callee := testProg.addPackage(t, "example.com/emission/ownercallee", `package ownercallee
import "example.com/emission/ownerintrinsics"
type T struct{}
func (*T) M() func() { return ownerintrinsics.Intrinsic }
`)
	caller := testProg.addPackage(t, "example.com/emission/ownercaller", `package ownercaller
import "example.com/emission/ownercallee"
var Global ownercallee.T
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()

	// Keep the use-site first: its global's *ownercallee.T type can lazily
	// materialize the pointer-receiver method before ownercallee is selected.
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: caller.ssa, Files: []*ast.File{caller.file}},
		{SSA: callee.ssa, Files: []*ast.File{callee.file}},
		{SSA: intrinsics.ssa, Files: []*ast.File{intrinsics.file}},
	})
	if err != nil {
		t.Fatal(err)
	}
	method := callee.ssa.Prog.MethodValue(callee.ssa.Prog.MethodSets.MethodSet(types.NewPointer(callee.types.Scope().Lookup("T").Type())).At(0))
	if owner := universe.ownerOf(method); owner == nil || owner.ssa != callee.ssa {
		t.Fatalf("callee method owner = %+v; want exact callee package", owner)
	}
	intrinsic := intrinsics.ssa.Func("Intrinsic")
	if _, ok := universe.intrinsicWrapper(callee.ssa, intrinsic); !ok {
		t.Fatal("callee-scoped intrinsic function-value wrapper was not prepared")
	}
}

func TestEmissionUniverseMaterializesOwnerScopedWrappersForEveryLinkOnceUseSite(t *testing.T) {
	testProg := newEmissionTestProgram()
	intrinsics := testProg.addPackage(t, "example.com/emission/linkonceintrinsics", `package linkonceintrinsics
//llgo:link Intrinsic llgo.unreachable
func Intrinsic()
`)
	generic := testProg.addPackage(t, "example.com/emission/linkoncegeneric", `package linkoncegeneric
import "example.com/emission/linkonceintrinsics"
type Box[T any] struct{}
func (Box[T]) M() func() { return linkonceintrinsics.Intrinsic }
`)
	one := testProg.addPackage(t, "example.com/emission/linkonceone", `package linkonceone
import "example.com/emission/linkoncegeneric"
func Use() any { return linkoncegeneric.Box[int]{} }
`)
	two := testProg.addPackage(t, "example.com/emission/linkoncetwo", `package linkoncetwo
import "example.com/emission/linkoncegeneric"
func Use() any { return linkoncegeneric.Box[int]{} }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: one.ssa, Files: []*ast.File{one.file}},
		{SSA: two.ssa, Files: []*ast.File{two.file}},
		{SSA: generic.ssa, Files: []*ast.File{generic.file}},
		{SSA: intrinsics.ssa, Files: []*ast.File{intrinsics.file}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var instance *ssa.Function
	for _, fn := range universe.Functions() {
		if origin := fn.Origin(); origin != nil && origin.Name() == "M" {
			if instance != nil && instance != fn {
				t.Fatalf("found multiple Box[int].M instances: %v and %v", instance, fn)
			}
			instance = fn
		}
	}
	if instance == nil {
		t.Fatal("Box[int].M instance was not materialized")
	}
	intrinsic := intrinsics.ssa.Func("Intrinsic")
	for _, owner := range []*ssa.Package{one.ssa, two.ssa} {
		if _, ok := universe.intrinsicWrapper(owner, intrinsic); !ok {
			t.Fatalf("owner-scoped intrinsic wrapper is absent for linkonce use-site %s", owner.Pkg.Path())
		}
	}
}

func TestEmissionUniverseFreezesExactStructuralWrapperForEveryOwner(t *testing.T) {
	testProg := newEmissionTestProgram()
	base := testProg.addPackage(t, "example.com/emission/sharedwrapperbase", `package sharedwrapperbase
type Base struct{}
func (Base) M() {}
`)
	one := testProg.addPackage(t, "example.com/emission/sharedwrapperone", `package sharedwrapperone
import "example.com/emission/sharedwrapperbase"
var Value any = struct{ sharedwrapperbase.Base }{}
`)
	two := testProg.addPackage(t, "example.com/emission/sharedwrappertwo", `package sharedwrappertwo
import "example.com/emission/sharedwrapperbase"
var Value any = struct{ sharedwrapperbase.Base }{}
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: one.ssa, Files: []*ast.File{one.file}},
		{SSA: two.ssa, Files: []*ast.File{two.file}},
		{SSA: base.ssa, Files: []*ast.File{base.file}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var shared *ssa.Function
	for _, fn := range universe.Functions() {
		if fn.Pkg == nil && fn.Name() == "M" && fn.Signature.Recv() != nil {
			recv := types.Unalias(fn.Signature.Recv().Type())
			if pointer, ok := recv.(*types.Pointer); ok {
				recv = types.Unalias(pointer.Elem())
			}
			if _, ok := recv.(*types.Struct); ok {
				shared = fn
				break
			}
		}
	}
	if shared == nil {
		t.Fatal("shared structural promoted wrapper is absent")
	}
	oneOwner, twoOwner := universe.packages[one.ssa], universe.packages[two.ssa]
	if _, ok := universe.useOwners[shared][oneOwner]; !ok {
		t.Fatal("first use-site owner is absent")
	}
	if _, ok := universe.useOwners[shared][twoOwner]; !ok {
		t.Fatal("second use-site owner is absent")
	}
	oneName := universe.physicalNames[emissionFunctionOwnerKey{function: shared, owner: oneOwner}]
	twoName := universe.physicalNames[emissionFunctionOwnerKey{function: shared, owner: twoOwner}]
	if oneName == "" || twoName == "" || oneName == twoName {
		t.Fatalf("owner-scoped physical names = %q, %q; want two frozen names", oneName, twoName)
	}
	linkIdentity := universe.linkIdentities[shared]
	if !strings.Contains(linkIdentity, oneOwner.identity) || !strings.Contains(linkIdentity, twoOwner.identity) {
		t.Fatalf("multi-owner link identity %q omits an owner", linkIdentity)
	}
}

func TestEmissionUniverseKeepsSamePathTestVariantsExact(t *testing.T) {
	testProg := newEmissionTestProgram()
	first := testProg.addPackage(t, "example.com/emission/variant", `package variant; func F() int { return 1 }`)
	second := testProg.addPackage(t, "example.com/emission/variant", `package variant; func F() int { return 2 }`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: first.ssa, Files: []*ast.File{first.file}, Identity: "variant ordinary"},
		{SSA: second.ssa, Files: []*ast.File{second.file}, Identity: "variant test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !universe.Contains(first.ssa.Func("F")) || !universe.Contains(second.ssa.Func("F")) {
		t.Fatal("same-path variant exact functions were collapsed")
	}
	if got := universe.ownerOf(first.ssa.Func("F")); got == nil || got.ssa != first.ssa {
		t.Fatalf("first variant owner = %+v", got)
	}
	if got := universe.ownerOf(second.ssa.Func("F")); got == nil || got.ssa != second.ssa {
		t.Fatalf("second variant owner = %+v", got)
	}
	if universe.byPath["example.com/emission/variant"] != nil {
		t.Fatal("ambiguous package path remained an ownership fallback")
	}
	config := universe.FunctionIDConfig()
	firstID, err := coro.StableFunctionID(first.ssa.Func("F"), config)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := coro.StableFunctionID(second.ssa.Func("F"), config)
	if err != nil {
		t.Fatal(err)
	}
	if firstID == secondID {
		t.Fatal("same-path variants share a frozen FunctionID")
	}
}

func TestEmissionUniverseIntrinsicWrappersAreOwnerScopedAndIdentifiable(t *testing.T) {
	testProg := newEmissionTestProgram()
	intrinsics := testProg.addPackage(t, "example.com/emission/intrinsics", `package intrinsics
//llgo:link Intrinsic llgo.unreachable
func Intrinsic()
`)
	one := testProg.addPackage(t, "example.com/emission/one", `package one
import "example.com/emission/intrinsics"
var Value = intrinsics.Intrinsic
`)
	two := testProg.addPackage(t, "example.com/emission/two", `package two
import "example.com/emission/intrinsics"
var Value = intrinsics.Intrinsic
`)
	testProg.ssa.Build()

	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: intrinsics.ssa, Files: []*ast.File{intrinsics.file}},
		{SSA: one.ssa, Files: []*ast.File{one.file}},
		{SSA: two.ssa, Files: []*ast.File{two.file}},
	})
	if err != nil {
		t.Fatal(err)
	}
	intrinsic := intrinsics.ssa.Func("Intrinsic")
	if owner := universe.packages[intrinsics.ssa]; !universe.isIntrinsic(intrinsic, owner) {
		t.Fatal("//llgo:link intrinsic classification was not frozen before selection")
	}
	oneWrapper, oneOK := universe.intrinsicWrapper(one.ssa, intrinsic)
	twoWrapper, twoOK := universe.intrinsicWrapper(two.ssa, intrinsic)
	if !oneOK || !twoOK || oneWrapper == twoWrapper {
		t.Fatalf("owner wrappers = (%v, %v), (%v, %v); want two exact wrappers", oneWrapper, oneOK, twoWrapper, twoOK)
	}
	if repeated, ok := universe.intrinsicWrapper(one.ssa, intrinsic); !ok || repeated != oneWrapper {
		t.Fatal("intrinsic wrapper memo did not return the prepared exact pointer")
	}

	config := universe.FunctionIDConfig()
	oneKey, ok, err := config.ResolveSynthetic(oneWrapper)
	if err != nil || !ok || !strings.Contains(oneKey, "example.com/emission/one") {
		t.Fatalf("one wrapper key = %q, %v, %v", oneKey, ok, err)
	}
	twoKey, ok, err := config.ResolveSynthetic(twoWrapper)
	if err != nil || !ok || !strings.Contains(twoKey, "example.com/emission/two") || oneKey == twoKey {
		t.Fatalf("two wrapper key = %q, %v, %v", twoKey, ok, err)
	}
	if _, err := coro.StableFunctionID(oneWrapper, config); err != nil {
		t.Fatalf("StableFunctionID(prepared wrapper): %v", err)
	}

	ssaUniverse, err := coro.NewSSAEmissionUniverse(universe.SSAProgram(), universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := coro.AnalyzeSSA(universe.SSAProgram(), nil, coro.SSAConfig{
		EmissionUniverse: ssaUniverse,
		FunctionIDs:      config,
	})
	if err != nil {
		t.Fatalf("AnalyzeSSA with prepared wrappers: %v", err)
	}
	if err := universe.ValidateCoroPlan(plan); err != nil {
		t.Fatal(err)
	}

	emptyPlan, err := coro.AnalyzeSSA(universe.SSAProgram(), nil, coro.SSAConfig{
		EmissionUniverse: func() *coro.SSAEmissionUniverse {
			empty, createErr := coro.NewSSAEmissionUniverse(universe.SSAProgram(), nil)
			if createErr != nil {
				t.Fatal(createErr)
			}
			return empty
		}(),
		FunctionIDs: config,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := universe.ValidateCoroPlan(emptyPlan); err == nil || !strings.Contains(err.Error(), "required final function") {
		t.Fatalf("coverage error = %v", err)
	}
}

func TestEmissionUniverseCoveragePrecedesPhysicalABIPreflight(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/preflight", `package preflight
func Plain() {}
func Coroutine(channel chan int) { <-channel }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	coroutine := pkg.ssa.Func("Coroutine")
	incomplete, err := coro.NewSSAEmissionUniverse(testProg.ssa, []*ssa.Function{coroutine})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{{Function: coroutine, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse: incomplete,
	})
	if err != nil {
		t.Fatal(err)
	}
	compilation := &Compilation{
		CoroPlan:                  plan,
		EmissionUniverse:          universe,
		EnableCoroEntryResolution: true,
	}
	err = compilation.preflightCoroPlan()
	if err == nil || !strings.Contains(err.Error(), "plan coverage") {
		t.Fatalf("preflight error = %v; want coverage miss", err)
	}
	if strings.Contains(err.Error(), "physical ABI") {
		t.Fatalf("physical ABI error won before coverage: %v", err)
	}
}

func TestEmissionUniverseCoverageRejectsPlanExtras(t *testing.T) {
	testProg := newEmissionTestProgram()
	included := testProg.addPackage(t, "example.com/emission/coverageincluded", `package coverageincluded; func Included() {}`)
	outside := testProg.addPackage(t, "example.com/emission/coverageoutside", `package coverageoutside; func Outside() {}`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: included.ssa, Files: []*ast.File{included.file}}})
	if err != nil {
		t.Fatal(err)
	}
	functions := append(universe.Functions(), outside.ssa.Func("Outside"))
	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, functions)
	if err != nil {
		t.Fatal(err)
	}
	config := universe.AugmentFunctionIDConfig(coro.FunctionIDConfig{
		ResolveLinkIdentity: func(fn *ssa.Function) (string, error) {
			return "outside:" + fn.Name(), nil
		},
	})
	plan, err := coro.AnalyzeSSA(testProg.ssa, nil, coro.SSAConfig{EmissionUniverse: ssaUniverse, FunctionIDs: config})
	if err != nil {
		t.Fatal(err)
	}
	if err := universe.ValidatePlanCoverage(plan); err == nil || !strings.Contains(err.Error(), "extra function") {
		t.Fatalf("ValidatePlanCoverage error = %v; want exact-set extra rejection", err)
	}
}

func TestEmissionUniverseSkipAllOnlyAllowsBodylessOriginal(t *testing.T) {
	for _, test := range []struct {
		name      string
		original  string
		wantError bool
	}{
		{name: "bodyful", original: `package skipped; func Target() {}`, wantError: true},
		{name: "bodyless", original: `package skipped; func Target()`},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			original := testProg.addPackage(t, "example.com/emission/skipped", test.original)
			alt := testProg.addPackage(t, abi.PatchPathPrefix+"example.com/emission/skipped", `package skipped
//llgo:skipall
type PatchControl struct{}
`)
			caller := testProg.addPackage(t, "example.com/emission/skipcaller", `package skipcaller
import "example.com/emission/skipped"
func Call() { skipped.Target() }
`)
			testProg.ssa.Build()
			prog := llssa.NewProgram(nil)
			defer prog.Dispose()
			universe, err := PrepareEmissionUniverse(prog, Patches{
				"example.com/emission/skipped": {Alt: alt.ssa, Types: typepatch.Clone(alt.types)},
			}, []EmissionPackage{
				{SSA: original.ssa, Files: []*ast.File{original.file, alt.file}},
				{SSA: caller.ssa, Files: []*ast.File{caller.file}},
			})
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "excluded original") {
					t.Fatalf("PrepareEmissionUniverse error = %v; want reached bodyful skipall rejection", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !universe.Contains(original.ssa.Func("Target")) {
				t.Fatal("reached bodyless skipall declaration is absent")
			}
		})
	}
}

func TestEmissionUniverseManagedKeysIncludeFrontendFunctionKind(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/functionkinds", `package functionkinds
func Go()
//llgo:link C C.same
func C()
//llgo:link Py py.same
func Py()
//llgo:link Instr llgo.unreachable
func Instr()
func _cgoexp_Ignored()
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	owner := universe.packages[pkg.ssa]
	for name, want := range map[string]int{"Go": goFunc, "C": cFunc, "Py": pyFunc, "Instr": llgoInstr} {
		key, managed, err := universe.managedSymbolKey(owner, pkg.ssa.Func(name), pkgNormal)
		if err != nil || !managed || managedKeyFunctionType(key) != want {
			t.Fatalf("managedSymbolKey(%s) = %q, %v, %v; want ftype %d", name, key, managed, err, want)
		}
	}
	if key, managed, err := universe.managedSymbolKey(owner, pkg.ssa.Func("_cgoexp_Ignored"), pkgNormal); err != nil || managed || key != "" {
		t.Fatalf("ignored managedSymbolKey = %q, %v, %v", key, managed, err)
	}
}

func TestEmissionUniverseIntrinsicWrapperNamesIncludeCanonicalCallee(t *testing.T) {
	testProg := newEmissionTestProgram()
	one := testProg.addPackage(t, "example.com/emission/intrinsicnameone", `package intrinsicnameone
//llgo:link X llgo.unreachable
func X()
`)
	two := testProg.addPackage(t, "example.com/emission/intrinsicnametwo", `package intrinsicnametwo
//llgo:link X llgo.skip
func X()
`)
	owner := testProg.addPackage(t, "example.com/emission/intrinsicnameowner", `package intrinsicnameowner
import one "example.com/emission/intrinsicnameone"
import two "example.com/emission/intrinsicnametwo"
var One = one.X
var Two = two.X
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: owner.ssa, Files: []*ast.File{owner.file}},
		{SSA: one.ssa, Files: []*ast.File{one.file}},
		{SSA: two.ssa, Files: []*ast.File{two.file}},
	})
	if err != nil {
		t.Fatal(err)
	}
	oneWrapper, oneOK := universe.intrinsicWrapper(owner.ssa, one.ssa.Func("X"))
	twoWrapper, twoOK := universe.intrinsicWrapper(owner.ssa, two.ssa.Func("X"))
	if !oneOK || !twoOK || oneWrapper == nil || twoWrapper == nil {
		t.Fatalf("same-owner intrinsic wrappers = %v/%v, %v/%v", oneWrapper, oneOK, twoWrapper, twoOK)
	}
	if oneWrapper == twoWrapper || oneWrapper.Name() == twoWrapper.Name() {
		t.Fatalf("same-owner intrinsic wrapper names = %q, %q", oneWrapper.Name(), twoWrapper.Name())
	}
}

func TestEmissionUniverseMaterializesPtrToThisMethods(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/ptrtothis", `package ptrtothis
type Box[T any] struct{}
func (*Box[T]) P() {}
type E struct{}
func (*E) M() {}
func Generic() any { return Box[int]{} }
func Structural() any { return struct{ E }{} }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	foundGenericPointer, foundStructuralPointer := false, false
	for _, fn := range universe.Functions() {
		if origin := fn.Origin(); origin != nil && origin.Name() == "P" {
			foundGenericPointer = true
		}
		if fn.Pkg == nil && fn.Name() == "M" && fn.Signature.Recv() != nil {
			recv := types.Unalias(fn.Signature.Recv().Type())
			if pointer, ok := recv.(*types.Pointer); ok {
				if _, structural := types.Unalias(pointer.Elem()).(*types.Struct); structural {
					foundStructuralPointer = true
				}
			}
		}
	}
	if !foundGenericPointer || !foundStructuralPointer {
		t.Fatalf("PtrToThis methods: generic=%v structural=%v", foundGenericPointer, foundStructuralPointer)
	}
}

func TestEmissionUniverseActiveCodegenHasNoLateTypeFunctions(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/codegen", `package codegen
type Base struct{}
func (*Base) M() {}
type Outer struct{ *Base }
var Global struct{ *Base }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := coro.AnalyzeSSA(testProg.ssa, nil, coro.SSAConfig{
		EmissionUniverse: ssaUniverse,
		FunctionIDs:      universe.FunctionIDConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, pkg.ssa, []*ast.File{pkg.file}, goembed.VarMap{},
		PackageOptions{Compilation: &Compilation{
			CoroPlan:                  plan,
			EmissionUniverse:          universe,
			EnableCoroEntryResolution: true,
		}},
	)
	if err != nil {
		t.Fatalf("active codegen found a late function: %v", err)
	}
	if compiled == nil {
		t.Fatal("active codegen returned nil package")
	}
}

func TestEmissionUniverseRejectsPackageMutation(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/frozen", `package frozen; func F() {}`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := universe.checkPackage(pkg.ssa, nil, nil); err == nil || !strings.Contains(err.Error(), "syntax changed") {
		t.Fatalf("checkPackage mutation error = %v", err)
	}
	if got := fmt.Sprint(universe.Functions()); got == "" {
		t.Fatal("unexpected empty diagnostic")
	}
}
