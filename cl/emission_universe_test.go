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

func TestEmissionFunctionSortKeyIgnoresFileSetBaseAndCheckoutRoot(t *testing.T) {
	build := func(filename string, leadingBytes int) *ssa.Function {
		t.Helper()
		fset := token.NewFileSet()
		if leadingBytes != 0 {
			fset.AddFile("unrelated.go", -1, leadingBytes)
		}
		file, err := parser.ParseFile(fset, filename, `package sortstable
func Target(value int) int { return value + 1 }
`, 0)
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
		pkg := types.NewPackage("example.com/emission/sortstable", "sortstable")
		if err := types.NewChecker(&types.Config{Importer: importer.Default()}, fset, pkg, info).Files([]*ast.File{file}); err != nil {
			t.Fatal(err)
		}
		prog := ssa.NewProgram(fset, ssa.SanityCheckFunctions|ssa.InstantiateGenerics)
		ssaPkg := prog.CreatePackage(pkg, []*ast.File{file}, info, true)
		ssaPkg.Build()
		return ssaPkg.Func("Target")
	}

	left := build("/checkout/one/p.go", 0)
	right := build("/different/root/p.go", 8192)
	if left.Pos() == right.Pos() {
		t.Fatalf("test setup produced equal raw token positions %d", left.Pos())
	}
	if got, want := emissionFunctionSortKey(left), emissionFunctionSortKey(right); got != want {
		t.Fatalf("stable function sort keys differ across FileSet/root: %q != %q", got, want)
	}
}

func TestEmissionAddResolvedRequiredCanonicalizesAliasChains(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/aliaschain", `package aliaschain
func A() {}
func B() {}
func C() {}
`)
	testProg.ssa.Build()
	owner := &preparedEmissionPackage{
		identity: pkg.types.Path(),
		ssa:      pkg.ssa,
		pkgPath:  pkg.types.Path(),
		oldTypes: pkg.types,
		pkgTypes: pkg.types,
	}
	a, b, c := pkg.ssa.Func("A"), pkg.ssa.Func("B"), pkg.ssa.Func("C")
	universe := &EmissionUniverse{
		goProg:   testProg.ssa,
		packages: map[*ssa.Package]*preparedEmissionPackage{pkg.ssa: owner},
		aliases:  map[*ssa.Function]*ssa.Function{a: b, b: c},
		excluded: make(map[*ssa.Function]none),
		required: make(map[*ssa.Function]none),
		fnOwners: make(map[*ssa.Function]*preparedEmissionPackage),
		fnStates: make(map[*ssa.Function]emissionFunctionState),
		functionKinds: map[emissionFunctionOwnerKey]int{
			{function: c, owner: owner}: goFunc,
		},
		finalKeys:   map[emissionFunctionOwnerKey]string{{function: c, owner: owner}: "canonical-c"},
		useOwners:   make(map[*ssa.Function]map[*preparedEmissionPackage]none),
		ownerStates: make(map[*ssa.Function]map[*preparedEmissionPackage]emissionFunctionState),
	}
	got, err := universe.addResolvedRequired(a, owner, c, emissionFunctionState{state: pkgNormal})
	if err != nil {
		t.Fatal(err)
	}
	if got != c {
		t.Fatalf("resolved alias chain = %v; want exact %v", got, c)
	}
	if _, ok := universe.required[c]; !ok {
		t.Fatalf("canonical alias target %v was not required", c)
	}
	if _, ok := universe.required[b]; ok {
		t.Fatalf("intermediate alias %v was incorrectly required", b)
	}
	if resolved, ok := universe.Resolve(a); !ok || resolved != c {
		t.Fatalf("Resolve(alias chain) = %v, %v; want exact %v, true", resolved, ok, c)
	}
	if got := universe.finalIdentity(a); got != universe.finalIdentity(c) {
		t.Fatalf("alias-chain final identity = %q; want canonical %q", got, universe.finalIdentity(c))
	}
	cOwnerKey := emissionFunctionOwnerKey{function: c, owner: owner}
	delete(universe.functionKinds, cOwnerKey)
	if _, err := universe.addResolvedRequired(a, owner, c, emissionFunctionState{state: pkgNormal}); err == nil || !strings.Contains(err.Error(), "partially frozen frontend metadata") {
		t.Fatalf("half-frozen alias metadata error = %v; want construction-time rejection", err)
	}
	universe.functionKinds[cOwnerKey] = goFunc

	universe.aliases[c] = a
	if _, err := universe.addResolvedRequired(a, owner, c, emissionFunctionState{state: pkgNormal}); err == nil || !strings.Contains(err.Error(), "cyclic canonical aliases") {
		t.Fatalf("cyclic alias error = %v; want explicit cycle diagnostic", err)
	}
	if resolved, ok := universe.Resolve(a); ok || resolved != nil {
		t.Fatalf("Resolve(alias cycle) = %v, %v; want nil, false", resolved, ok)
	}
	if got := universe.finalIdentity(a); got != "<cyclic-alias>" {
		t.Fatalf("cyclic alias final identity = %q; want cycle diagnostic", got)
	}
}

func TestEmissionUniverseReplaceManagedWinnerTransfersAllOwnerMetadata(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/replacemultiowner", `package replacemultiowner
func Old() {}
func Replacement() {}
`)
	testProg.ssa.Build()
	old := pkg.ssa.Func("Old")
	replacement := pkg.ssa.Func("Replacement")
	ownerA := &preparedEmissionPackage{
		order:     0,
		identity:  "owner-a",
		pkgPath:   "example.com/emission/owner-a",
		winners:   make(map[string]*ssa.Function),
		fromPatch: make(map[*ssa.Function]bool),
	}
	ownerB := &preparedEmissionPackage{
		order:    1,
		identity: "owner-b",
		pkgPath:  "example.com/emission/owner-b",
	}
	ownerAKey := managedSymbolKey(goFunc, "same", "signature")
	ownerBKey := managedSymbolKey(goFunc, "owner-b-same", "signature")
	newUniverse := func() *EmissionUniverse {
		ownerA.winners = map[string]*ssa.Function{ownerAKey: old}
		ownerA.fromPatch = map[*ssa.Function]bool{old: false}
		return &EmissionUniverse{
			aliases:  make(map[*ssa.Function]*ssa.Function),
			required: map[*ssa.Function]none{old: {}},
			useOwners: map[*ssa.Function]map[*preparedEmissionPackage]none{
				old: {ownerA: {}, ownerB: {}},
			},
			ownerStates: map[*ssa.Function]map[*preparedEmissionPackage]emissionFunctionState{
				old: {
					ownerA: {state: pkgHasPatch},
					ownerB: {state: pkgNormal},
				},
			},
			functionKinds: map[emissionFunctionOwnerKey]int{
				{function: old, owner: ownerA}: goFunc,
				{function: old, owner: ownerB}: goFunc,
			},
			finalKeys: map[emissionFunctionOwnerKey]string{
				{function: old, owner: ownerA}: ownerAKey,
				{function: old, owner: ownerB}: ownerBKey,
			},
		}
	}

	t.Run("transfer", func(t *testing.T) {
		universe := newUniverse()
		if err := universe.replaceManagedWinner(ownerA, ownerAKey, old, replacement); err != nil {
			t.Fatal(err)
		}
		if got := ownerA.winners[ownerAKey]; got != replacement {
			t.Fatalf("managed winner = %v; want replacement %v", got, replacement)
		}
		if got := universe.aliases[old]; got != replacement {
			t.Fatalf("old canonical alias = %v; want replacement %v", got, replacement)
		}
		if len(universe.useOwners[replacement]) != 2 {
			t.Fatalf("replacement use owners = %v; want both frozen owners", universe.useOwners[replacement])
		}
		for owner, wantKey := range map[*preparedEmissionPackage]string{ownerA: ownerAKey, ownerB: ownerBKey} {
			oldKey := emissionFunctionOwnerKey{function: old, owner: owner}
			if _, ok := universe.functionKinds[oldKey]; ok {
				t.Fatalf("old function kind remains for owner %q", owner.identity)
			}
			if _, ok := universe.finalKeys[oldKey]; ok {
				t.Fatalf("old managed key remains for owner %q", owner.identity)
			}
			replacementKey := emissionFunctionOwnerKey{function: replacement, owner: owner}
			if got := universe.functionKinds[replacementKey]; got != goFunc {
				t.Fatalf("replacement function kind for owner %q = %d; want goFunc", owner.identity, got)
			}
			if got := universe.finalKeys[replacementKey]; got != wantKey {
				t.Fatalf("replacement managed key for owner %q = %q; want %q", owner.identity, got, wantKey)
			}
		}
	})

	t.Run("conflict", func(t *testing.T) {
		universe := newUniverse()
		universe.functionKinds[emissionFunctionOwnerKey{function: replacement, owner: ownerB}] = cFunc
		err := universe.replaceManagedWinner(ownerA, ownerAKey, old, replacement)
		if err == nil || !strings.Contains(err.Error(), "conflicting frozen frontend kinds") {
			t.Fatalf("replaceManagedWinner conflict error = %v; want frozen-kind conflict", err)
		}
		if got := ownerA.winners[ownerAKey]; got != old {
			t.Fatalf("managed winner mutated after conflict = %v; want old %v", got, old)
		}
		if _, aliased := universe.aliases[old]; aliased {
			t.Fatal("old function was aliased after rejected metadata conflict")
		}
	})

	t.Run("provenance conflict is atomic", func(t *testing.T) {
		universe := newUniverse()
		universe.ownerStates[old][ownerB] = emissionFunctionState{state: pkgHasPatch}
		universe.useOwners[replacement] = map[*preparedEmissionPackage]none{ownerB: {}}
		universe.ownerStates[replacement] = map[*preparedEmissionPackage]emissionFunctionState{
			ownerB: {state: pkgInPatch},
		}
		err := universe.replaceManagedWinner(ownerA, ownerAKey, old, replacement)
		if err == nil || !strings.Contains(err.Error(), "conflicting emission provenance") {
			t.Fatalf("replaceManagedWinner provenance error = %v; want conflict", err)
		}
		if got := ownerA.winners[ownerAKey]; got != old {
			t.Fatalf("managed winner mutated after provenance conflict = %v; want old %v", got, old)
		}
		if ownerA.fromPatch[replacement] {
			t.Fatal("replacement patch provenance mutated after rejected merge")
		}
		if _, aliased := universe.aliases[old]; aliased {
			t.Fatal("old function was aliased after rejected provenance conflict")
		}
		if _, required := universe.required[old]; !required {
			t.Fatal("old function requirement was deleted after rejected provenance conflict")
		}
		if len(universe.useOwners[old]) != 2 || len(universe.ownerStates[old]) != 2 {
			t.Fatal("old owner metadata was mutated after rejected provenance conflict")
		}
		if got := universe.ownerStates[replacement][ownerB]; got.state != pkgInPatch || got.fromPatch {
			t.Fatalf("replacement provenance mutated after conflict: %+v", got)
		}
		if universe.ownerStateErr != nil {
			t.Fatalf("atomic preflight leaked global ownerStateErr: %v", universe.ownerStateErr)
		}
	})

	newIntrinsicUniverse := func() (*EmissionUniverse, string, string) {
		ownerAKey := managedSymbolKey(llgoInstr, "cstr", "signature")
		ownerBKey := managedSymbolKey(llgoInstr, "cstr", "owner-b-signature")
		ownerA.winners = map[string]*ssa.Function{ownerAKey: old}
		ownerA.fromPatch = map[*ssa.Function]bool{old: false}
		universe := &EmissionUniverse{
			aliases:  make(map[*ssa.Function]*ssa.Function),
			required: map[*ssa.Function]none{old: {}},
			useOwners: map[*ssa.Function]map[*preparedEmissionPackage]none{
				old: {ownerA: {}, ownerB: {}},
			},
			ownerStates: map[*ssa.Function]map[*preparedEmissionPackage]emissionFunctionState{
				old: {
					ownerA: {state: pkgHasPatch},
					ownerB: {state: pkgNormal},
				},
			},
			functionKinds: map[emissionFunctionOwnerKey]int{
				{function: old, owner: ownerA}: llgoInstr,
				{function: old, owner: ownerB}: llgoInstr,
			},
			intrinsicOps: map[emissionFunctionOwnerKey]int{
				{function: old, owner: ownerA}: llgoCstr,
				{function: old, owner: ownerB}: llgoCstr,
			},
			finalKeys: map[emissionFunctionOwnerKey]string{
				{function: old, owner: ownerA}: ownerAKey,
				{function: old, owner: ownerB}: ownerBKey,
			},
		}
		return universe, ownerAKey, ownerBKey
	}

	t.Run("intrinsic opcode transfer", func(t *testing.T) {
		universe, ownerAKey, _ := newIntrinsicUniverse()
		if err := universe.replaceManagedWinner(ownerA, ownerAKey, old, replacement); err != nil {
			t.Fatal(err)
		}
		for _, owner := range []*preparedEmissionPackage{ownerA, ownerB} {
			oldKey := emissionFunctionOwnerKey{function: old, owner: owner}
			if _, exists := universe.intrinsicOps[oldKey]; exists {
				t.Fatalf("old intrinsic opcode remains for owner %q", owner.identity)
			}
			replacementKey := emissionFunctionOwnerKey{function: replacement, owner: owner}
			if opcode, exists := universe.intrinsicOps[replacementKey]; !exists || opcode != llgoCstr {
				t.Fatalf("replacement intrinsic opcode for owner %q = %d, %v; want llgoCstr, true", owner.identity, opcode, exists)
			}
		}
	})

	t.Run("intrinsic opcode conflict is atomic", func(t *testing.T) {
		universe, ownerAKey, _ := newIntrinsicUniverse()
		replacementKey := emissionFunctionOwnerKey{function: replacement, owner: ownerB}
		universe.intrinsicOps[replacementKey] = llgoUnreachable
		err := universe.replaceManagedWinner(ownerA, ownerAKey, old, replacement)
		if err == nil || !strings.Contains(err.Error(), "conflicting frozen llgo intrinsic opcode") {
			t.Fatalf("replaceManagedWinner intrinsic conflict error = %v; want opcode conflict", err)
		}
		if got := ownerA.winners[ownerAKey]; got != old {
			t.Fatalf("managed winner mutated after intrinsic conflict = %v; want old %v", got, old)
		}
		if _, aliased := universe.aliases[old]; aliased {
			t.Fatal("old intrinsic was aliased after rejected opcode conflict")
		}
		for _, owner := range []*preparedEmissionPackage{ownerA, ownerB} {
			oldKey := emissionFunctionOwnerKey{function: old, owner: owner}
			if opcode, exists := universe.intrinsicOps[oldKey]; !exists || opcode != llgoCstr {
				t.Fatalf("old intrinsic opcode mutated for owner %q: %d, %v", owner.identity, opcode, exists)
			}
		}
	})
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
	const publicInit = "example.com/emission/p.init"
	publicInitName, err := universe.physicalName(original.ssa, alt.ssa.Func("init"), publicInit)
	if err != nil || publicInitName != publicInit {
		t.Fatalf("patch public init physical name = %q, %v; want %s", publicInitName, err, publicInit)
	}
	originalInitName, err := universe.physicalName(original.ssa, original.ssa.Func("init"), publicInit)
	if err != nil || originalInitName != publicInit {
		t.Fatalf("ordinary original-init physical name = %q, %v; want public %s", originalInitName, err, publicInit)
	}
	hiddenInitName, err := universe.patchOriginalInitPhysicalName(original.ssa.Func("init"))
	if err != nil || hiddenInitName != publicInit+"$hasPatch" {
		t.Fatalf("private patch-original physical name = %q, %v; want %s$hasPatch", hiddenInitName, err, publicInit)
	}
	originalState, frozen, err := universe.frozenFunctionState(original.ssa, original.ssa.Func("init"))
	if err != nil || !frozen || originalState.state != pkgHasPatch || originalState.fromPatch {
		t.Fatalf("patch original init frozen state = %+v, %v, %v; want pkgHasPatch original", originalState, frozen, err)
	}
	entries, err := universe.CoroPatchInitEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0] != alt.ssa.Func("init") {
		t.Fatalf("patch init entries = %v; want exact alternate init", entries)
	}
	lowered, err := universe.CoroLoweredCalls(alt.ssa.Func("init"))
	if err != nil {
		t.Fatal(err)
	}
	foundOriginalInit := false
	for _, call := range lowered {
		if call.LogicalName == coroPatchOriginalInitCall {
			foundOriginalInit = call.Target == original.ssa.Func("init")
		}
	}
	if !foundOriginalInit {
		t.Fatalf("patch init lowered calls = %v; want exact original-init edge", lowered)
	}

	copyOfFunctions := universe.Functions()
	copyOfFunctions[0] = nil
	if universe.Functions()[0] == nil {
		t.Fatal("Functions exposed mutable storage")
	}
}

func TestEmissionUniversePatchCanonicalizesGenericMethodInstances(t *testing.T) {
	testProg := newEmissionTestProgram()
	const originalPath = "example.com/emission/genericpatch"
	original := testProg.addPackage(t, originalPath, `package genericpatch
type Handle[T any] struct { value T }
func (h Handle[T]) Value() T { return h.value }
`)
	alt := testProg.addPackage(t, abi.PatchPathPrefix+originalPath, `package genericpatch
//llgo:skipall
type PatchControl struct{}
type Handle[T any] struct { value T }
func (h Handle[T]) Value() T { return h.value }
`)
	caller := testProg.addPackage(t, "example.com/emission/genericpatchcaller", `package genericpatchcaller
import "example.com/emission/genericpatch"
func Use(h genericpatch.Handle[int]) int { return h.Value() }
func Box(h genericpatch.Handle[int]) interface{ Value() int } { return h }
`)
	testProg.ssa.Build()

	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, Patches{
		originalPath: {Alt: alt.ssa, Types: typepatch.Clone(alt.types)},
	}, []EmissionPackage{
		{SSA: original.ssa, Files: []*ast.File{original.file, alt.file}},
		{SSA: caller.ssa, Files: []*ast.File{caller.file}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var instances []*ssa.Function
	for _, fn := range universe.Functions() {
		if origin := fn.Origin(); origin != nil && origin.Name() == "Value" && len(fn.TypeArgs()) == 1 {
			instances = append(instances, fn)
		}
	}
	if len(instances) != 1 {
		var diagnostics []string
		for _, fn := range instances {
			origin, _ := universe.Resolve(fn.Origin())
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s origin=%s canonical-origin=%v identity=%s",
				emissionFunctionDiagnostic(fn), emissionFunctionDiagnostic(fn.Origin()), origin, universe.finalIdentity(fn),
			))
		}
		t.Fatalf("canonical generic Value[int] instances = %d, want 1: %v", len(instances), diagnostics)
	}
	if instances[0].Origin() == original.ssa.Prog.MethodValue(
		original.ssa.Prog.MethodSets.MethodSet(original.types.Scope().Lookup("Handle").Type()).At(0),
	) {
		t.Fatal("canonical generic method instance retained the skipped original origin")
	}
	var raw *ssa.Function
	for _, block := range caller.ssa.Func("Use").Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(*ssa.Call); ok && call.Common().StaticCallee() != nil {
				raw = call.Common().StaticCallee()
			}
		}
	}
	canonical, ok := universe.Resolve(raw)
	if raw == nil || !ok || canonical != instances[0] || canonical == raw {
		t.Fatalf("generic method call alias = raw %v canonical %v exact %t; want distinct patch instance", raw, canonical, ok)
	}
	rawSignature, err := universe.coroPhysicalSourceSignature(raw)
	if err != nil {
		t.Fatalf("raw generic patch source signature: %v", err)
	}
	canonicalSignature, err := universe.coroPhysicalSourceSignature(canonical)
	if err != nil {
		t.Fatalf("canonical generic patch source signature: %v", err)
	}
	if !coroInterfaceDispatchSignaturesIdentical(rawSignature, canonicalSignature) {
		t.Fatalf("generic patch source signatures differ: raw %s canonical %s", rawSignature, canonicalSignature)
	}
}

func TestEmissionUniversePatchInitRedirectsImportedOccurrenceToPublicInit(t *testing.T) {
	testProg := newEmissionTestProgram()
	original := testProg.addPackage(t, "example.com/emission/redirected", `package redirected
var Original = 1
`)
	alt := testProg.addPackage(t, abi.PatchPathPrefix+"example.com/emission/redirected", `package redirected
var Patched = 2
`)
	importer := testProg.addPackage(t, "example.com/emission/importer", `package importer
import _ "example.com/emission/redirected"
var Ready = true
`)
	testProg.ssa.Build()

	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, Patches{
		"example.com/emission/redirected": {
			Alt:   alt.ssa,
			Types: typepatch.Clone(alt.types),
		},
	}, []EmissionPackage{
		{SSA: original.ssa, Files: []*ast.File{original.file, alt.file}},
		{SSA: importer.ssa, Files: []*ast.File{importer.file}},
	})
	if err != nil {
		t.Fatal(err)
	}

	owner := importer.ssa.Func("init")
	originalInit := original.ssa.Func("init")
	publicInit := alt.ssa.Func("init")
	var occurrence *ssa.Call
	for _, block := range owner.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common().StaticCallee() == originalInit {
				occurrence = call
			}
		}
	}
	if occurrence == nil {
		t.Fatal("importer SSA has no exact call to the original package initializer")
	}
	logicalName, target, redirected, err := universe.CoroPatchInitRedirect(occurrence)
	if err != nil || !redirected || logicalName == "" || target != publicInit {
		t.Fatalf("patch init redirect = %q, %v, %v, %v; want exact public init", logicalName, target, redirected, err)
	}
	record, frozen, err := universe.ResolveCoroLoweredCallRecord(owner, logicalName)
	if err != nil || !frozen || record.Target != publicInit || record.RawPlain || record.UnwindOnly || record.ExplicitStatusElided {
		t.Fatalf("patch init lowered occurrence = %+v, %v, %v; want ordinary exact public-init call", record, frozen, err)
	}
	if _, _, redirected, err := universe.CoroPatchInitRedirect(nil); err == nil || redirected {
		t.Fatalf("nil patch init occurrence = redirected %v, error %v; want fail closed", redirected, err)
	}
}

func TestEmissionUniversePatchIntrinsicWinnerTransfersFrozenOpcode(t *testing.T) {
	universe, original, alt, dispose := preparePatchedEmissionTest(t, `package p
//llgo:link Intrinsic llgo.cstr
func Intrinsic(string) *byte
`, `package p
//llgo:link Intrinsic llgo.cstr
func Intrinsic(string) *byte
`)
	defer dispose()

	originalIntrinsic, replacement := original.ssa.Func("Intrinsic"), alt.ssa.Func("Intrinsic")
	if got, ok := universe.Resolve(originalIntrinsic); !ok || got != replacement {
		t.Fatalf("Resolve(original intrinsic) = %v, %v; want exact patch winner", got, ok)
	}
	for _, fn := range []*ssa.Function{originalIntrinsic, replacement} {
		semantics, intrinsic, err := universe.CoroIntrinsicSemantics(fn)
		if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
			t.Fatalf("CoroIntrinsicSemantics(%s) = %v, %v, %v; want inline-no-suspend, true, nil", fn.Name(), semantics, intrinsic, err)
		}
	}
	owner := universe.packages[original.ssa]
	if opcode, ok := universe.intrinsicOps[emissionFunctionOwnerKey{function: replacement, owner: owner}]; !ok || opcode != llgoCstr {
		t.Fatalf("patch intrinsic opcode = %d, %v; want llgoCstr, true", opcode, ok)
	}
	if opcode, ok := universe.intrinsicOps[emissionFunctionOwnerKey{function: originalIntrinsic, owner: owner}]; !ok || opcode != llgoCstr {
		t.Fatalf("patch intrinsic alias opcode = %d, %v; want llgoCstr, true", opcode, ok)
	}
}

func TestEmissionUniversePatchInitFreezesOmittedDependencyInitMetadata(t *testing.T) {
	testProg := newEmissionTestProgram()
	dependency := testProg.addPackage(t, "example.com/emission/patchinitdep", `package patchinitdep
var Ready = initialize()
func initialize() int { return 1 }
`)
	original := testProg.addPackage(t, "example.com/emission/patchinitowner", `package patchinitowner
var Original = 1
`)
	alt := testProg.addPackage(t, abi.PatchPathPrefix+"example.com/emission/patchinitowner", `package patchinitowner
import _ "example.com/emission/patchinitdep"
var Alternate = 2
`)
	testProg.ssa.Build()

	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, Patches{
		"example.com/emission/patchinitowner": {
			Alt:   alt.ssa,
			Types: typepatch.Clone(alt.types),
		},
	}, []EmissionPackage{{
		SSA:   original.ssa,
		Files: []*ast.File{original.file, alt.file},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if universe.packages[dependency.ssa] != nil {
		t.Fatal("test dependency unexpectedly has an explicit emission package owner")
	}
	dependencyInit := dependency.ssa.Func("init")
	if dependencyInit == nil || !universe.Contains(dependencyInit) {
		t.Fatalf("patch dependency init = %v; want materialized canonical function", dependencyInit)
	}
	if got, classified, err := universe.FunctionBackground(dependencyInit); err != nil || got != llssa.InGo || !classified {
		t.Fatalf("FunctionBackground(patch dependency init) = %v, %v, %v; want InGo, true, nil", got, classified, err)
	}
	owner := universe.packages[original.ssa]
	ownerKey := emissionFunctionOwnerKey{function: dependencyInit, owner: owner}
	if kind, ok := universe.functionKinds[ownerKey]; !ok || kind != goFunc {
		t.Fatalf("patch dependency init frozen kind = %d, %v; want goFunc, true", kind, ok)
	}
	if key := universe.finalKeys[ownerKey]; key == "" || managedKeyFunctionType(key) != goFunc {
		t.Fatalf("patch dependency init frozen managed key = %q; want Go managed provenance", key)
	}
}

func TestEmissionUniverseMetadataOnlyDeclarationFreezesReachedCFunction(t *testing.T) {
	testProg := newEmissionTestProgram()
	declaration := testProg.addPackage(t, "example.com/emission/decldep", `package decldep
const LLGoPackage = "decl"
//go:linkname Exit C.exit
func Exit(int)
//go:linkname Unused C.unused
func Unused()
`)
	owner := testProg.addPackage(t, "example.com/emission/declowner", `package declowner
import "example.com/emission/decldep"
func Call() { decldep.Exit(0) }
`)
	testProg.ssa.Build()

	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: owner.ssa, Files: []*ast.File{owner.file}},
		{SSA: declaration.ssa, Files: []*ast.File{declaration.file}, MetadataOnly: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared := universe.packages[declaration.ssa]
	if prepared == nil || !prepared.metadataOnly {
		t.Fatal("declaration package has no exact metadata-only frontend owner")
	}
	exit := declaration.ssa.Func("Exit")
	if resolved, ok := universe.Resolve(exit); !ok || resolved != exit || !universe.Contains(exit) {
		t.Fatalf("Resolve(Exit) = %v, %v (contained=%v); want exact reached canonical declaration", resolved, ok, universe.Contains(exit))
	}
	if universe.Contains(declaration.ssa.Func("Unused")) {
		t.Fatal("metadata-only package eagerly selected an unused declaration")
	}
	declarationInit := declaration.ssa.Func("init")
	if !universe.Contains(declarationInit) {
		t.Fatal("decl package synthetic init is absent from the exact retained universe")
	}
	if got, classified, err := universe.FunctionBackground(exit); err != nil || got != llssa.InC || !classified {
		t.Fatalf("FunctionBackground(Exit) = %v, %v, %v; want InC, true, nil", got, classified, err)
	}

	// The public query is frozen construction metadata, not a late lookup in
	// the mutable llssa linkname table.
	prog.SetLinkname("example.com/emission/decldep.Exit", "example.com/other.GoExit")
	if got, classified, err := universe.FunctionBackground(exit); err != nil || got != llssa.InC || !classified {
		t.Fatalf("FunctionBackground(Exit) after linkname mutation = %v, %v, %v; want frozen InC", got, classified, err)
	}

	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := coro.AnalyzeSSA(testProg.ssa, nil, coro.SSAConfig{
		EmissionUniverse: ssaUniverse,
		FunctionIDs:      universe.FunctionIDConfig(),
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			return FrontendElidesNoInitCall(call), nil
		},
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			background, classified, err := universe.FunctionBackground(fn)
			if err != nil || !classified || background != llssa.InC {
				return coro.SSAFunctionPolicy{}, err
			}
			return coro.SSAFunctionPolicy{
				External:         coro.ExternalUnknownForeign,
				OverrideExternal: true,
				IgnoreBody:       true,
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.IgnoresBody(exit) {
		t.Fatal("reached frozen C declaration did not receive physical IgnoreBody policy")
	}
	var declarationInitCall ssa.CallInstruction
	for _, block := range owner.ssa.Func("init").Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if ok && call.Common().StaticCallee() == declarationInit {
				declarationInitCall = call
			}
		}
	}
	if declarationInitCall == nil || !plan.ElidesCall(declarationInitCall) {
		t.Fatalf("decl init call = %v; want exact retained frontend-elided call", declarationInitCall)
	}
	if _, ok := plan.CallPlan(declarationInitCall); ok {
		t.Fatal("frontend-elided decl init call unexpectedly has a CallPlan")
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
	if !universe.generatedWrapperDefinitionNeedsLinkOnce(shared) {
		t.Fatal("frozen Pkg-nil wrapper is not protected against cross-module materialization")
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
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
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
		CoroPlan:         plan,
		EmissionUniverse: universe, CoroProfile: CoroProfileStackless,
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
//llgo:link CAlias C.same
func CAlias()
//llgo:link Py py.same
func Py()
//llgo:link Instr llgo.unreachable
func Instr()
//llgo:link CStr llgo.cstr
func CStr(string) *byte
//llgo:link CStrAlias llgo.cstr
func CStrAlias(string) *byte
func _cgoexp_Ignored()
func Closure() func() { return func() {} }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	owner := universe.packages[pkg.ssa]
	for name, want := range map[string]int{"Go": goFunc, "C": cFunc, "CAlias": cFunc, "Py": pyFunc, "Instr": llgoInstr, "CStr": llgoInstr, "CStrAlias": llgoInstr} {
		key, managed, err := universe.managedSymbolKey(owner, pkg.ssa.Func(name), pkgNormal)
		if err != nil || !managed || managedKeyFunctionType(key) != want {
			t.Fatalf("managedSymbolKey(%s) = %q, %v, %v; want ftype %d", name, key, managed, err, want)
		}
	}
	if key, managed, err := universe.managedSymbolKey(owner, pkg.ssa.Func("_cgoexp_Ignored"), pkgNormal); err != nil || managed || key != "" {
		t.Fatalf("ignored managedSymbolKey = %q, %v, %v", key, managed, err)
	}

	for _, test := range []struct {
		name       string
		background llssa.Background
		classified bool
	}{
		{name: "Go", background: llssa.InGo, classified: true},
		{name: "C", background: llssa.InC, classified: true},
		{name: "CAlias", background: llssa.InC, classified: true},
		{name: "Py", background: llssa.InPython, classified: true},
		{name: "Instr"},
		{name: "CStr"},
		{name: "CStrAlias"},
		{name: "_cgoexp_Ignored"},
	} {
		got, classified, err := universe.FunctionBackground(pkg.ssa.Func(test.name))
		if err != nil || got != test.background || classified != test.classified {
			t.Errorf("FunctionBackground(%s) = %v, %v, %v; want %v, %v, nil", test.name, got, classified, err, test.background, test.classified)
		}
	}
	if semantics, intrinsic, err := universe.CoroIntrinsicSemantics(pkg.ssa.Func("Instr")); err != nil || !intrinsic || semantics != CoroIntrinsicCallUnsupported {
		t.Fatalf("unreachable intrinsic semantics = %v, %v, %v; want unsupported, true, nil", semantics, intrinsic, err)
	}
	cstr, cstrOK := universe.Resolve(pkg.ssa.Func("CStr"))
	cstrAlias, cstrAliasOK := universe.Resolve(pkg.ssa.Func("CStrAlias"))
	if !cstrOK || !cstrAliasOK || cstr == nil || cstrAlias != cstr {
		t.Fatalf("canonical cstr aliases = %v/%v and %v/%v; want one exact canonical intrinsic", cstr, cstrOK, cstrAlias, cstrAliasOK)
	}
	for _, fn := range []*ssa.Function{pkg.ssa.Func("CStr"), pkg.ssa.Func("CStrAlias")} {
		semantics, intrinsic, err := universe.CoroIntrinsicSemantics(fn)
		if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
			t.Fatalf("cstr alias semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
		}
	}
	cstrOwner := universe.packages[pkg.ssa]
	cstrOwnerKey := emissionFunctionOwnerKey{function: cstr, owner: cstrOwner}
	if opcode, ok := universe.intrinsicOps[cstrOwnerKey]; !ok || opcode != llgoCstr {
		t.Fatalf("canonical cstr opcode = %d, %v; want llgoCstr, true", opcode, ok)
	}
	conflictingOwner := &preparedEmissionPackage{order: cstrOwner.order + 1, identity: "conflicting-intrinsic-owner", pkgPath: cstrOwner.pkgPath}
	conflictingKey := emissionFunctionOwnerKey{function: cstr, owner: conflictingOwner}
	universe.useOwners[cstr][conflictingOwner] = none{}
	universe.ownerStates[cstr][conflictingOwner] = emissionFunctionState{state: pkgNormal}
	universe.functionKinds[conflictingKey] = llgoInstr
	universe.finalKeys[conflictingKey] = universe.finalKeys[cstrOwnerKey]
	universe.intrinsicOps[conflictingKey] = llgoUnreachable
	if _, intrinsic, err := universe.CoroIntrinsicSemantics(cstr); err == nil || intrinsic || !strings.Contains(err.Error(), "inconsistent compiler opcodes") {
		t.Fatalf("conflicting cstr owner semantics = _, %v, %v; want deterministic opcode conflict", intrinsic, err)
	}
	delete(universe.useOwners[cstr], conflictingOwner)
	delete(universe.ownerStates[cstr], conflictingOwner)
	delete(universe.functionKinds, conflictingKey)
	delete(universe.finalKeys, conflictingKey)
	delete(universe.intrinsicOps, conflictingKey)
	closureParent := pkg.ssa.Func("Closure")
	if closureParent == nil || len(closureParent.AnonFuncs) != 1 {
		t.Fatalf("Closure anonymous functions = %v; want exactly one", closureParent)
	}
	closure := closureParent.AnonFuncs[0]
	if got, classified, err := universe.FunctionBackground(closure); err != nil || got != llssa.InGo || !classified {
		t.Fatalf("FunctionBackground(Closure$1) = %v, %v, %v; want InGo, true, nil", got, classified, err)
	}
	closureOwnerKey := emissionFunctionOwnerKey{function: closure, owner: owner}
	if kind, ok := universe.functionKinds[closureOwnerKey]; !ok || kind != goFunc {
		t.Fatalf("Closure$1 frozen function kind = %d, %v; want goFunc, true", kind, ok)
	}
	if key := universe.finalKeys[closureOwnerKey]; key == "" || managedKeyFunctionType(key) != goFunc {
		t.Fatalf("Closure$1 frozen managed key = %q; want Go managed provenance", key)
	}
	c, cOK := universe.Resolve(pkg.ssa.Func("C"))
	cAlias, cAliasOK := universe.Resolve(pkg.ssa.Func("CAlias"))
	if !cOK || !cAliasOK || c == nil || cAlias != c {
		t.Fatalf("canonical C aliases = %v/%v and %v/%v; want one exact canonical function", c, cOK, cAlias, cAliasOK)
	}
	cOwner := universe.packages[pkg.ssa]
	cOwnerKey := emissionFunctionOwnerKey{function: c, owner: cOwner}
	cKind := universe.functionKinds[cOwnerKey]
	delete(universe.functionKinds, cOwnerKey)
	if _, classified, err := universe.FunctionBackground(pkg.ssa.Func("CAlias")); err == nil || classified || !strings.Contains(err.Error(), "no frozen frontend function kind") {
		t.Fatalf("FunctionBackground(alias with missing kind) = _, %v, %v; want fail-closed metadata error", classified, err)
	}
	universe.functionKinds[cOwnerKey] = goFunc
	if _, classified, err := universe.FunctionBackground(pkg.ssa.Func("C")); err == nil || classified || !strings.Contains(err.Error(), "inconsistent frozen frontend kinds") {
		t.Fatalf("FunctionBackground(inconsistent kind) = _, %v, %v; want fail-closed metadata error", classified, err)
	}
	universe.functionKinds[cOwnerKey] = cKind
	cState := universe.ownerStates[c][cOwner]
	delete(universe.ownerStates[c], cOwner)
	if _, classified, err := universe.FunctionBackground(pkg.ssa.Func("C")); err == nil || classified || !strings.Contains(err.Error(), "no frozen provenance") {
		t.Fatalf("FunctionBackground(missing provenance) = _, %v, %v; want fail-closed metadata error", classified, err)
	}
	universe.ownerStates[c][cOwner] = cState

	corruptFirst := &preparedEmissionPackage{order: -1, identity: "corrupt", pkgPath: "a"}
	corruptSecond := &preparedEmissionPackage{order: -1, identity: "corrupt", pkgPath: "z"}
	universe.useOwners[c][corruptFirst] = none{}
	universe.useOwners[c][corruptSecond] = none{}
	universe.ownerStates[c][corruptFirst] = emissionFunctionState{state: pkgNormal}
	var firstError string
	for attempt := 0; attempt < 64; attempt++ {
		_, classified, err := universe.FunctionBackground(pkg.ssa.Func("CAlias"))
		if err == nil || classified || !strings.Contains(err.Error(), "no frozen frontend function kind") {
			t.Fatalf("FunctionBackground(multi-owner corruption) attempt %d = _, %v, %v; want deterministic first-owner kind error", attempt, classified, err)
		}
		if attempt == 0 {
			firstError = err.Error()
		} else if err.Error() != firstError {
			t.Fatalf("FunctionBackground(multi-owner corruption) attempt %d error = %q; want %q", attempt, err, firstError)
		}
	}
	universe.useOwners[c][nil] = none{}
	if _, classified, err := universe.FunctionBackground(pkg.ssa.Func("C")); err == nil || classified || !strings.Contains(err.Error(), "nil frozen use owner") {
		t.Fatalf("FunctionBackground(nil owner) = _, %v, %v; want independently detected nil-owner error", classified, err)
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
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
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
			CoroPlan:         plan,
			EmissionUniverse: universe, CoroProfile: CoroProfileStackless,
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
