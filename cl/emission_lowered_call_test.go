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

package cl

import (
	"go/ast"
	"strings"
	"testing"
)

func TestEmissionUniverseCoroLoweredCallsAreExactSortedAndFailClosed(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/loweredcalls", `package loweredcalls
func Owner() {}
func First() {}
func Second() {}
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
	owner := pkg.ssa.Func("Owner")
	first := pkg.ssa.Func("First")
	second := pkg.ssa.Func("Second")
	if err := universe.recordCoroLoweredCall(owner, "runtime.second", second); err != nil {
		t.Fatal(err)
	}
	if err := universe.recordCoroLoweredCall(owner, "runtime.first", first); err != nil {
		t.Fatal(err)
	}
	if err := universe.recordCoroLoweredCall(owner, "runtime.first", first); err != nil {
		t.Fatalf("idempotent lowered call: %v", err)
	}

	calls, err := universe.CoroLoweredCalls(owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].LogicalName != "runtime.first" || calls[0].Target != first || calls[1].LogicalName != "runtime.second" || calls[1].Target != second {
		t.Fatalf("lowered calls = %+v, want sorted exact mappings", calls)
	}
	calls[0].Target = second
	target, ok, err := universe.ResolveCoroLoweredCall(owner, "runtime.first")
	if err != nil || !ok || target != first {
		t.Fatalf("ResolveCoroLoweredCall(runtime.first) = %v, %v, %v", target, ok, err)
	}
	if target, ok, err := universe.ResolveCoroLoweredCall(owner, "runtime.missing"); err != nil || ok || target != nil {
		t.Fatalf("ResolveCoroLoweredCall(runtime.missing) = %v, %v, %v", target, ok, err)
	}

	if err := universe.recordCoroLoweredCall(owner, "runtime.first", second); err == nil || !strings.Contains(err.Error(), "resolves to both") {
		t.Fatalf("conflicting logical helper error = %v", err)
	}
	if err := universe.recordCoroLoweredCall(owner, "", first); err == nil || !strings.Contains(err.Error(), "invalid logical name") {
		t.Fatalf("empty logical helper error = %v", err)
	}
	if err := universe.recordCoroLoweredCall(owner, "runtime.nil", nil); err == nil || !strings.Contains(err.Error(), "nil target") {
		t.Fatalf("nil lowered helper error = %v", err)
	}
	if _, err := universe.CoroLoweredCalls(nil); err == nil || !strings.Contains(err.Error(), "exact owner") {
		t.Fatalf("nil lowered-call owner error = %v", err)
	}
}
