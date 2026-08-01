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
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestLibraryEffectSummaryAutomaticallyColorsImportedCallers(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "library.go", `package coroid
func imported()
func caller() { imported() }
`)
	imported := packageFunction(t, pkg, "imported")
	caller := packageFunction(t, pkg, "caller")
	functionIDs := FunctionIDConfig{}
	importedID, err := StableFunctionID(imported, functionIDs)
	if err != nil {
		t.Fatal(err)
	}
	summary := testLibraryEffectSummary(t, "example/library", false)
	summary.Functions = []LibraryEffectFunction{{
		ID:            importedID,
		ABIHash:       strings.Repeat("3", 64),
		Effect:        MayPark,
		Exec:          MayUnwind,
		FuncRep:       DirectCoro,
		Primary:       PrimaryCoroutine,
		ManagedEntry:  ManagedEntryCoroutine,
		PrimarySymbol: "example/library.imported$coro",
	}}
	summary.ForeignCallables = nil
	summary.ExportBindings = nil
	index, err := NewLibraryEffectIndex([]LibraryEffectSummary{summary}, testLibraryEffectMetadata())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: caller, Demand: AsyncDemand}}, SSAConfig{
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (SSAFunctionPolicy, error) {
			id, idErr := StableFunctionID(function, functionIDs)
			if idErr != nil {
				return SSAFunctionPolicy{}, idErr
			}
			fact, ok := index.Lookup(id)
			if !ok {
				return SSAFunctionPolicy{}, nil
			}
			return fact.ImportedPolicy()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	importedPlan := functionPlanFor(t, plan, imported)
	if importedPlan.External != ExternalKnown || importedPlan.Effect != MayPark ||
		importedPlan.Primary != PrimaryExternal {
		t.Fatalf("imported plan = %+v", importedPlan)
	}
	callerPlan := functionPlanFor(t, plan, caller)
	if !callerPlan.Effect.Contains(MayPark|AwaitStructured) || !callerPlan.Exec.Contains(MayUnwind) ||
		callerPlan.Primary != PrimaryCoroutine {
		t.Fatalf("caller was not automatically colored from library summary: %+v", callerPlan)
	}
}
