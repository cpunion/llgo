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
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/typepatch"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/goplus/llgo/ssa/abi"
	"golang.org/x/tools/go/ssa"
)

func TestEmissionUniverseAliasesOrdinaryBodylessRuntimeHookToExactLinknameDefinition(t *testing.T) {
	testProg := newEmissionTestProgram()
	declaration := testProg.addPackage(t, "example.com/emission/hookdecl", `package hookdecl
func runtimeHook(cleanup func())
func Call(cleanup func()) { runtimeHook(cleanup) }
`)
	definition := testProg.addPackage(t, "example.com/emission/hookdef", `package hookdef
//go:linkname llgoHook example.com/emission/hookdecl.runtimeHook
func llgoHook(cleanup func()) { cleanup() }
`)
	testProg.ssa.Build()

	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: declaration.ssa, Files: []*ast.File{declaration.file}},
		{SSA: definition.ssa, Files: []*ast.File{definition.file}},
	})
	if err != nil {
		t.Fatal(err)
	}
	declared := declaration.ssa.Func("runtimeHook")
	defined := definition.ssa.Func("llgoHook")
	resolved, ok := universe.Resolve(declared)
	if !ok || resolved != defined {
		t.Fatalf("Resolve(ordinary bodyless runtime hook) = %v, %t; want %v, true", resolved, ok, defined)
	}
}

func TestEmissionUniverseAliasesBodylessGoLinknameToExactDefinition(t *testing.T) {
	testProg := newEmissionTestProgram()
	declaration := testProg.addPackage(t, "example.com/emission/linkdecl", `package linkdecl
//go:linkname upstreamRuntimeHook
func upstreamRuntimeHook(int) int
func Call(value int) int { return upstreamRuntimeHook(value) }
`)
	definition := testProg.addPackage(t, "example.com/emission/linkdef", `package linkdef
//go:linkname llgoRuntimeHook example.com/emission/linkdecl.upstreamRuntimeHook
func llgoRuntimeHook(value int) int { return value + 1 }
`)
	testProg.ssa.Build()

	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: declaration.ssa, Files: []*ast.File{declaration.file}},
		{SSA: definition.ssa, Files: []*ast.File{definition.file}},
	})
	if err != nil {
		t.Fatal(err)
	}

	declFn := declaration.ssa.Func("upstreamRuntimeHook")
	definitionFn := definition.ssa.Func("llgoRuntimeHook")
	if len(declFn.Blocks) != 0 || len(definitionFn.Blocks) == 0 {
		t.Fatalf("fixture body shapes = declaration %d, definition %d; want bodyless/bodyful", len(declFn.Blocks), len(definitionFn.Blocks))
	}
	if resolved, ok := universe.Resolve(declFn); !ok || resolved != definitionFn {
		t.Fatalf("Resolve(bodyless go:linkname) = %v, %v; want exact definition %v, true", resolved, ok, definitionFn)
	}
	if universe.Contains(declFn) || !universe.Contains(definitionFn) {
		t.Fatalf("canonical membership = declaration %t, definition %t; want false, true", universe.Contains(declFn), universe.Contains(definitionFn))
	}
	if _, required := universe.required[declFn]; required {
		t.Fatal("bodyless go:linkname declaration remains required")
	}
	if _, owned := universe.fnOwners[declFn]; owned {
		t.Fatal("bodyless go:linkname declaration retains a frozen function owner")
	}
	if _, stated := universe.fnStates[declFn]; stated {
		t.Fatal("bodyless go:linkname declaration retains frozen provenance")
	}
	if len(universe.useOwners[declFn]) != 0 || len(universe.ownerStates[declFn]) != 0 {
		t.Fatal("bodyless go:linkname declaration retains use-owner metadata")
	}
	for key := range universe.functionKinds {
		if key.function == declFn {
			t.Fatal("bodyless go:linkname declaration retains frontend-kind metadata")
		}
	}
	for key := range universe.finalKeys {
		if key.function == declFn {
			t.Fatal("bodyless go:linkname declaration retains final managed-key metadata")
		}
	}

	declOwner := universe.packages[declaration.ssa]
	definitionOwner := universe.packages[definition.ssa]
	owners := universe.sortedUseOwners(definitionFn)
	if len(owners) != 1 || owners[0] != definitionOwner {
		t.Fatalf("canonical definition owners = %v; want only exact definition owner %q", owners, definitionOwner.identity)
	}
	definitionOwnerKey := emissionFunctionOwnerKey{function: definitionFn, owner: definitionOwner}
	if kind, ok := universe.functionKinds[definitionOwnerKey]; !ok || kind != goFunc {
		t.Fatalf("canonical definition kind = %d, %v; want goFunc, true", kind, ok)
	}
	finalKey := universe.finalKeys[definitionOwnerKey]
	if finalKey == "" {
		t.Fatal("canonical definition has no final managed key")
	}
	if _, leaked := universe.functionKinds[emissionFunctionOwnerKey{function: definitionFn, owner: declOwner}]; leaked {
		t.Fatal("canonical definition inherited the declaration owner")
	}
	if winner := declOwner.winners[finalKey]; winner != definitionFn {
		t.Fatalf("declaration-owner managed winner = %v; want exact definition %v", winner, definitionFn)
	}

	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	callOwner := declaration.ssa.Func("Call")
	plan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{{Function: callOwner, Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse: ssaUniverse,
		ResolveFunction: func(fn *ssa.Function) (*ssa.Function, bool, error) {
			canonical, ok := universe.Resolve(fn)
			return canonical, ok, nil
		},
		FunctionIDs: universe.FunctionIDConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := universe.ValidatePlanCoverage(plan); err != nil {
		t.Fatal(err)
	}
	if _, ok := plan.FunctionPlan(declFn); ok {
		t.Fatal("bodyless go:linkname declaration entered the coroutine plan")
	}
	definitionID, ok := plan.FunctionID(definitionFn)
	if !ok {
		t.Fatal("canonical definition has no coroutine FunctionID")
	}
	var directCall ssa.CallInstruction
	for _, block := range callOwner.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if ok && call.Common().StaticCallee() == declFn {
				directCall = call
			}
		}
	}
	callPlan, ok := plan.CallPlan(directCall)
	if directCall == nil || !ok || callPlan.Open || len(callPlan.Targets) != 1 || callPlan.Targets[0] != definitionID {
		t.Fatalf("canonical declaration call plan = %+v, %v; want one exact definition target %q", callPlan, ok, definitionID)
	}

	ctx, err := universe.functionABIContext(definitionFn, declOwner)
	if err != nil {
		t.Fatal(err)
	}
	ctx.compilation = &Compilation{
		EnableCoroEntryResolution: true,
		CoroPlan:                  plan,
		EmissionUniverse:          universe,
	}
	entry, err := ctx.resolveFunctionSymbol(declFn)
	if err != nil {
		t.Fatal(err)
	}
	const physical = "example.com/emission/linkdecl.upstreamRuntimeHook"
	if entry.function != definitionFn || entry.name != physical || entry.ftype != goFunc {
		t.Fatalf("resolved codegen entry = function %v, name %q, kind %d; want %v, %q, goFunc", entry.function, entry.name, entry.ftype, definitionFn, physical)
	}
}

func TestEmissionUniverseAliasesPatchedBodylessGoLinknameToExactDefinition(t *testing.T) {
	testProg := newEmissionTestProgram()
	original := testProg.addPackage(t, "example.com/emission/patchedlinkdecl", `package patchedlinkdecl
func runtimeHook(int) int
func Call(value int) int { return runtimeHook(value) }
`)
	alternate := testProg.addPackage(t, abi.PatchPathPrefix+"example.com/emission/patchedlinkdecl", `package patchedlinkdecl
//go:linkname runtimeHook example.com/emission/patchedlinkdef.runtimeHook
func runtimeHook(int) int
`)
	definition := testProg.addPackage(t, "example.com/emission/patchedlinkdef", `package patchedlinkdef
func runtimeHook(value int) int { return value + 1 }
`)
	testProg.ssa.Build()

	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, Patches{
		"example.com/emission/patchedlinkdecl": {
			Alt:   alternate.ssa,
			Types: typepatch.Clone(alternate.types),
		},
	}, []EmissionPackage{
		{SSA: original.ssa, Files: []*ast.File{original.file, alternate.file}},
		{SSA: definition.ssa, Files: []*ast.File{definition.file}},
	})
	if err != nil {
		t.Fatal(err)
	}

	declaration := alternate.ssa.Func("runtimeHook")
	defined := definition.ssa.Func("runtimeHook")
	if resolved, ok := universe.Resolve(declaration); !ok || resolved != defined {
		t.Fatalf("Resolve(patched bodyless go:linkname) = %v, %v; want exact definition %v, true", resolved, ok, defined)
	}
	if universe.Contains(declaration) || !universe.Contains(defined) {
		t.Fatalf("patched linkname membership = declaration %t, definition %t; want false, true", universe.Contains(declaration), universe.Contains(defined))
	}
}

func TestEmissionUniverseGoLinknamePendingDefinitionPolicy(t *testing.T) {
	for _, test := range []struct {
		name         string
		declaration  string
		metadataOnly bool
		wantManaged  bool
	}{
		{
			name: "ordinary skipped declaration",
			declaration: `package linkpending
//llgo:skipall
type PatchControl struct{}
func RuntimeHook(int) int
`,
			wantManaged: true,
		},
		{
			name: "metadata-only declaration",
			declaration: `package linkpending
func RuntimeHook(int) int
`,
			metadataOnly: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			declaration := testProg.addPackage(t, "example.com/emission/linkpending", test.declaration)
			definition := testProg.addPackage(t, "example.com/emission/linkpendingdef", `package linkpendingdef
//go:linkname implementation example.com/emission/linkpending.RuntimeHook
func implementation(value int) int { return value + 1 }
`)
			testProg.ssa.Build()

			prog := llssa.NewProgram(nil)
			defer prog.Dispose()
			universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
				{SSA: declaration.ssa, Files: []*ast.File{declaration.file}, MetadataOnly: test.metadataOnly},
				{SSA: definition.ssa, Files: []*ast.File{definition.file}},
			})
			if err != nil {
				t.Fatal(err)
			}

			declFn := declaration.ssa.Func("RuntimeHook")
			definitionFn := definition.ssa.Func("implementation")
			if resolved, ok := universe.Resolve(declFn); ok || resolved != declFn || universe.Contains(declFn) {
				t.Fatalf("Resolve(pending declaration) = %v, %t (contained=%t); want original, false, false", resolved, ok, universe.Contains(declFn))
			}
			pair, paired := universe.goLinknameDefinitions[declFn]
			declarationOwner := universe.packages[declaration.ssa]
			if !paired || pair.definition != definitionFn || pair.key == "" || pair.declarationOwner != declarationOwner {
				t.Fatalf("frozen pending pair = %+v, %t; want exact definition, key, and declaration owner", pair, paired)
			}
			if pair.declarationOwner.metadataOnly != test.metadataOnly {
				t.Fatalf("frozen declaration owner metadata-only = %t; want %t", pair.declarationOwner.metadataOnly, test.metadataOnly)
			}
			definitionOwner := universe.packages[definition.ssa]
			if key := universe.finalKeys[emissionFunctionOwnerKey{function: definitionFn, owner: definitionOwner}]; key != pair.key {
				t.Fatalf("definition frozen key = %q; want pair key %q", key, pair.key)
			}

			managed, err := universe.exactManagedGoLinknameDefinition(definitionFn)
			if err != nil || managed != test.wantManaged {
				t.Fatalf("managed pending definition = %t, %v; want %t, nil", managed, err, test.wantManaged)
			}
			directive, err := coroRawABIDirective(definitionFn, universe)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantManaged && directive != "" {
				t.Fatalf("managed pending raw ABI directive = %q; want empty", directive)
			}
			if !test.wantManaged && directive != "//go:linkname implementation example.com/emission/linkpending.RuntimeHook" {
				t.Fatalf("untrusted pending raw ABI directive = %q; want original linkname", directive)
			}

			// The decision is based on the exact pair frozen during construction,
			// not a late lookup in the mutable frontend linkname table.
			prog.SetLinkname("example.com/emission/linkpendingdef.implementation", "example.com/emission/other.RuntimeHook")
			managedAfterMutation, err := universe.exactManagedGoLinknameDefinition(definitionFn)
			if err != nil || managedAfterMutation != test.wantManaged {
				t.Fatalf("managed pending definition after linkname mutation = %t, %v; want %t, nil", managedAfterMutation, err, test.wantManaged)
			}
		})
	}
}

func TestEmissionUniverseGoLinknameReachedMetadataOnlyDefinitionIsManaged(t *testing.T) {
	testProg := newEmissionTestProgram()
	declaration := testProg.addPackage(t, "example.com/emission/linkmetadecl", `package linkmetadecl
func RuntimeHook(int) int
`)
	owner := testProg.addPackage(t, "example.com/emission/linkmetaowner", `package linkmetaowner
import "example.com/emission/linkmetadecl"
func Call(value int) int { return linkmetadecl.RuntimeHook(value) }
`)
	definition := testProg.addPackage(t, "example.com/emission/linkmetadef", `package linkmetadef
//go:linkname implementation example.com/emission/linkmetadecl.RuntimeHook
func implementation(value int) int { return value + 1 }
`)
	testProg.ssa.Build()

	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: owner.ssa, Files: []*ast.File{owner.file}},
		{SSA: declaration.ssa, Files: []*ast.File{declaration.file}, MetadataOnly: true},
		{SSA: definition.ssa, Files: []*ast.File{definition.file}},
	})
	if err != nil {
		t.Fatal(err)
	}

	declFn := declaration.ssa.Func("RuntimeHook")
	definitionFn := definition.ssa.Func("implementation")
	if resolved, ok := universe.Resolve(declFn); !ok || resolved != definitionFn {
		t.Fatalf("Resolve(reached metadata-only declaration) = %v, %t; want %v, true", resolved, ok, definitionFn)
	}
	pair, paired := universe.goLinknameDefinitions[declFn]
	if !paired || pair.definition != definitionFn || pair.declarationOwner != universe.packages[declaration.ssa] || !pair.declarationOwner.metadataOnly {
		t.Fatalf("reached metadata-only pair = %+v, %t; want exact activated pair with metadata-only owner", pair, paired)
	}
	managed, err := universe.exactManagedGoLinknameDefinition(definitionFn)
	if err != nil || !managed {
		t.Fatalf("managed reached metadata-only definition = %t, %v; want true, nil", managed, err)
	}
	if directive, err := coroRawABIDirective(definitionFn, universe); err != nil || directive != "" {
		t.Fatalf("reached metadata-only raw ABI directive = %q, %v; want empty, nil", directive, err)
	}
}

func TestEmissionUniverseGoLinknamePrivateMirrorABI(t *testing.T) {
	const matchingDefinition = `package linkmirrorimpl
import "unsafe"
type notifyList struct {
	wait uint32
	notify uint32
	lock uintptr
	head unsafe.Pointer
	tail unsafe.Pointer
}
//go:linkname implementation example.com/emission/linkmirror.runtimeNotifyAll
func implementation(list *notifyList) { _ = list.wait }
`
	for _, test := range []struct {
		name             string
		definition       string
		metadataOnly     bool
		wantPair         bool
		wantResolvedBody bool
	}{
		{
			name:             "reached exact layout",
			definition:       matchingDefinition,
			wantPair:         true,
			wantResolvedBody: true,
		},
		{
			name:         "metadata-only pending exact layout",
			definition:   matchingDefinition,
			metadataOnly: true,
			wantPair:     true,
		},
		{
			name: "different field type",
			definition: `package linkmirrorimpl
import "unsafe"
type notifyList struct {
	wait uint32
	notify uint32
	lock uintptr
	head unsafe.Pointer
	tail uintptr
}
//go:linkname implementation example.com/emission/linkmirror.runtimeNotifyAll
func implementation(list *notifyList) { _ = list.wait }
`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
			declaration := testProg.addPackage(t, "example.com/emission/linkmirror", `package linkmirror
import "unsafe"
type notifyList struct {
	wait uint32
	notify uint32
	lock uintptr
	head unsafe.Pointer
	tail unsafe.Pointer
}
func runtimeNotifyAll(*notifyList)
func Call(list *notifyList) { runtimeNotifyAll(list) }
`)
			definition := testProg.addPackage(t, "example.com/emission/linkmirrorimpl", test.definition)
			testProg.ssa.Build()

			prog := llssa.NewProgram(nil)
			defer prog.Dispose()
			universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
				{SSA: declaration.ssa, Files: []*ast.File{declaration.file}, MetadataOnly: test.metadataOnly},
				{SSA: definition.ssa, Files: []*ast.File{definition.file}},
			})
			if err != nil {
				t.Fatal(err)
			}

			declFn := declaration.ssa.Func("runtimeNotifyAll")
			definitionFn := definition.ssa.Func("implementation")
			strictDeclaration := structuralEmissionABITypeKey(declFn.Signature)
			strictDefinition := structuralEmissionABITypeKey(definitionFn.Signature)
			if strictDeclaration == strictDefinition {
				t.Fatal("ordinary managed signatures unexpectedly erased private named-type identity")
			}
			linknameDeclaration := structuralGoLinknameABITypeKey(declFn.Signature)
			linknameDefinition := structuralGoLinknameABITypeKey(definitionFn.Signature)
			if got := linknameDeclaration == linknameDefinition; got != test.wantPair {
				t.Fatalf("private-mirror linkname ABI match = %t; want %t", got, test.wantPair)
			}

			pair, paired := universe.goLinknameDefinitions[declFn]
			if paired != test.wantPair {
				t.Fatalf("private-mirror exact pair = %+v, %t; want paired=%t", pair, paired, test.wantPair)
			}
			if paired {
				kind, symbol, signature, valid := splitManagedSymbolKey(pair.key)
				if pair.definition != definitionFn || pair.declarationOwner != universe.packages[declaration.ssa] ||
					!valid || kind != goFunc || symbol != "example.com/emission/linkmirror.runtimeNotifyAll" || signature != linknameDefinition {
					t.Fatalf("private-mirror frozen pair = %+v, key=(%d,%q,%q,%t); want exact physical/linkname ABI pair", pair, kind, symbol, signature, valid)
				}
			}

			resolved, resolvedBody := universe.Resolve(declFn)
			if test.wantResolvedBody {
				if !resolvedBody || resolved != definitionFn {
					t.Fatalf("Resolve(private mirror declaration) = %v, %t; want %v, true", resolved, resolvedBody, definitionFn)
				}
			} else if test.metadataOnly {
				if resolvedBody || resolved != declFn {
					t.Fatalf("Resolve(metadata-only pending private mirror) = %v, %t; want original, false", resolved, resolvedBody)
				}
			} else if !resolvedBody || resolved != declFn {
				t.Fatalf("Resolve(mismatched private mirror) = %v, %t; want original, true", resolved, resolvedBody)
			}

			managed, err := universe.exactManagedGoLinknameDefinition(definitionFn)
			if err != nil || managed != test.wantResolvedBody {
				t.Fatalf("managed private-mirror definition = %t, %v; want %t, nil", managed, err, test.wantResolvedBody)
			}
			directive, err := coroRawABIDirective(definitionFn, universe)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantResolvedBody && directive != "" {
				t.Fatalf("managed private-mirror raw ABI directive = %q; want empty", directive)
			}
			if !test.wantResolvedBody && directive == "" {
				t.Fatal("unmanaged private-mirror definition lost its raw ABI directive")
			}
		})
	}
}

func TestEmissionUniverseGoLinknameExactDefinitionZeroKeepsDeclaration(t *testing.T) {
	tests := []struct {
		name       string
		definition string
	}{
		{
			name: "no definition",
		},
		{
			name: "structural signature mismatch",
			definition: `package linkmismatchdef
//go:linkname llgoRuntimeHook example.com/emission/linkmismatch.runtimeHook
func llgoRuntimeHook(string) int { return 1 }
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			declaration := testProg.addPackage(t, "example.com/emission/linkmismatch", `package linkmismatch
//go:linkname runtimeHook
func runtimeHook(int) int
`)
			inputs := []EmissionPackage{{SSA: declaration.ssa, Files: []*ast.File{declaration.file}}}
			var definition emissionTestPackage
			if test.definition != "" {
				definition = testProg.addPackage(t, "example.com/emission/linkmismatchdef", test.definition)
				inputs = append(inputs, EmissionPackage{SSA: definition.ssa, Files: []*ast.File{definition.file}})
			}
			testProg.ssa.Build()
			prog := llssa.NewProgram(nil)
			defer prog.Dispose()
			universe, err := PrepareEmissionUniverse(prog, nil, inputs)
			if err != nil {
				t.Fatal(err)
			}
			declFn := declaration.ssa.Func("runtimeHook")
			if resolved, ok := universe.Resolve(declFn); !ok || resolved != declFn || !universe.Contains(declFn) {
				t.Fatalf("Resolve(unmatched declaration) = %v, %v (contained=%t); want original, true, true", resolved, ok, universe.Contains(declFn))
			}
			if test.definition == "" {
				return
			}
			definitionFn := definition.ssa.Func("llgoRuntimeHook")
			declKey := universe.finalKeys[emissionFunctionOwnerKey{function: declFn, owner: universe.packages[declaration.ssa]}]
			definitionKey := universe.finalKeys[emissionFunctionOwnerKey{function: definitionFn, owner: universe.packages[definition.ssa]}]
			declKind, declName, declSignature, declOK := splitManagedSymbolKey(declKey)
			definitionKind, definitionName, definitionSignature, definitionOK := splitManagedSymbolKey(definitionKey)
			if !declOK || !definitionOK || declKind != goFunc || definitionKind != goFunc || declName != definitionName || declSignature == definitionSignature {
				t.Fatalf("mismatch keys = (%d,%q,%q,%t), (%d,%q,%q,%t); want same Go symbol and different structural signatures", declKind, declName, declSignature, declOK, definitionKind, definitionName, definitionSignature, definitionOK)
			}
		})
	}
}

func TestEmissionUniverseGoLinknameCopiedSamePathDefinitions(t *testing.T) {
	const declarationSource = `package linkvariantdecl
//go:linkname runtimeHook
func runtimeHook(int) int
`
	const definitionSource = `package linkvariantdef
//go:linkname implementation example.com/emission/linkvariantdecl.runtimeHook
func implementation(value int) int { return value + 1 }
`

	t.Run("identical copies select first exact variant", func(t *testing.T) {
		testProg := newEmissionTestProgram()
		declaration := testProg.addPackage(t, "example.com/emission/linkvariantdecl", declarationSource)
		first := testProg.addPackage(t, "example.com/emission/linkvariantdef", definitionSource)
		second := testProg.addPackage(t, "example.com/emission/linkvariantdef", definitionSource)
		testProg.ssa.Build()

		prog := llssa.NewProgram(nil)
		defer prog.Dispose()
		universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
			{SSA: declaration.ssa, Files: []*ast.File{declaration.file}},
			{SSA: first.ssa, Files: []*ast.File{first.file}, Identity: "definition ordinary"},
			{SSA: second.ssa, Files: []*ast.File{second.file}, Identity: "definition test"},
		})
		if err != nil {
			t.Fatal(err)
		}
		declFn := declaration.ssa.Func("runtimeHook")
		firstFn := first.ssa.Func("implementation")
		secondFn := second.ssa.Func("implementation")
		if resolved, ok := universe.Resolve(declFn); !ok || resolved != firstFn {
			t.Fatalf("Resolve(copied same-path definition) = %v, %t; want first exact variant %v, true", resolved, ok, firstFn)
		}
		if !universe.Contains(firstFn) || !universe.Contains(secondFn) {
			t.Fatal("copied same-path definitions lost their exact variant membership")
		}
	})

	t.Run("distinct copy remains ambiguous", func(t *testing.T) {
		testProg := newEmissionTestProgram()
		declaration := testProg.addPackage(t, "example.com/emission/linkvariantdecl", declarationSource)
		first := testProg.addPackage(t, "example.com/emission/linkvariantdef", definitionSource)
		second := testProg.addPackage(t, "example.com/emission/linkvariantdef", `package linkvariantdef
//go:linkname implementation example.com/emission/linkvariantdecl.runtimeHook
func implementation(value int) int { return value + 2 }
`)
		testProg.ssa.Build()

		prog := llssa.NewProgram(nil)
		defer prog.Dispose()
		_, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
			{SSA: declaration.ssa, Files: []*ast.File{declaration.file}},
			{SSA: first.ssa, Files: []*ast.File{first.file}, Identity: "definition ordinary"},
			{SSA: second.ssa, Files: []*ast.File{second.file}, Identity: "definition test"},
		})
		if err == nil || !strings.Contains(err.Error(), "multiple emitted Go definitions") {
			t.Fatalf("PrepareEmissionUniverse error = %v; want distinct same-path definition rejection", err)
		}
	})
}

func TestEmissionUniverseGoLinknameMultipleExactDefinitionsFailClosed(t *testing.T) {
	testProg := newEmissionTestProgram()
	declaration := testProg.addPackage(t, "example.com/emission/linkambiguous", `package linkambiguous
//go:linkname runtimeHook
func runtimeHook(int) int
`)
	first := testProg.addPackage(t, "example.com/emission/linkambiguousfirst", `package linkambiguousfirst
//go:linkname firstHook example.com/emission/linkambiguous.runtimeHook
func firstHook(value int) int { return value + 1 }
`)
	second := testProg.addPackage(t, "example.com/emission/linkambiguoussecond", `package linkambiguoussecond
//go:linkname secondHook example.com/emission/linkambiguous.runtimeHook
func secondHook(value int) int { return value + 2 }
`)
	testProg.ssa.Build()
	prog := llssa.NewProgram(nil)
	defer prog.Dispose()
	_, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{
		{SSA: declaration.ssa, Files: []*ast.File{declaration.file}},
		{SSA: first.ssa, Files: []*ast.File{first.file}},
		{SSA: second.ssa, Files: []*ast.File{second.file}},
	})
	if err == nil || !strings.Contains(err.Error(), "multiple emitted Go definitions") ||
		!strings.Contains(err.Error(), "example.com/emission/linkambiguous.runtimeHook") {
		t.Fatalf("PrepareEmissionUniverse error = %v; want exact managed-symbol multiple-definition rejection", err)
	}
}
