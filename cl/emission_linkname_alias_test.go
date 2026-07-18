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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
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
