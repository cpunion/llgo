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

	llssa "github.com/goplus/llgo/ssa"
)

func TestEmissionUniverseCompleteRuntimeABIGate(t *testing.T) {
	testProg := newEmissionTestProgram()
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
func Present() {}
`)
	callerPkg := testProg.addPackage(t, "example.com/emission/runtimeabigate", `package runtimeabigate
func Allocate() *int { return new(int) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	inputs := []EmissionPackage{
		{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
		{SSA: callerPkg.ssa, Files: []*ast.File{callerPkg.file}},
	}

	incomplete, err := PrepareEmissionUniverse(prog, nil, inputs)
	if err != nil {
		t.Fatalf("prepare incomplete/report universe: %v", err)
	}
	if incomplete.CompleteRuntimeABI() {
		t.Fatal("compatibility PrepareEmissionUniverse unexpectedly claims a complete runtime ABI")
	}
	lowered, err := incomplete.CoroLoweredCalls(callerPkg.ssa.Func("Allocate"))
	if err != nil {
		t.Fatal(err)
	}
	if len(lowered) != 0 {
		t.Fatalf("incomplete/report universe lowered calls = %+v; want legacy unresolved runtime markers", lowered)
	}

	_, err = PrepareEmissionUniverseWithOptions(prog, nil, inputs, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err == nil || !strings.Contains(err.Error(), `missing runtime helper "AllocZ"`) {
		t.Fatalf("complete runtime ABI error = %v; want missing AllocZ failure", err)
	}
}

func TestEmissionUniverseCompleteRuntimeABIFreezesExactHelper(t *testing.T) {
	testProg := newEmissionTestProgram()
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
func AllocZ(size uintptr) uintptr { return 0 }
`)
	callerPkg := testProg.addPackage(t, "example.com/emission/runtimeabiexact", `package runtimeabiexact
func Allocate() *int { return new(int) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
		{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
		{SSA: callerPkg.ssa, Files: []*ast.File{callerPkg.file}},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		t.Fatal(err)
	}
	if !universe.CompleteRuntimeABI() {
		t.Fatal("complete construction did not retain its runtime ABI contract")
	}
	owner := callerPkg.ssa.Func("Allocate")
	target := runtimePkg.ssa.Func("AllocZ")
	lowered, err := universe.CoroLoweredCalls(owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(lowered) != 1 || lowered[0].LogicalName != "AllocZ" || lowered[0].Target != target {
		t.Fatalf("complete runtime ABI lowered calls = %+v; want exact AllocZ target", lowered)
	}
}

func TestEmissionUniverseCompleteRuntimeABIRequiresRuntimePackage(t *testing.T) {
	testProg := newEmissionTestProgram()
	callerPkg := testProg.addPackage(t, "example.com/emission/runtimeabimissing", `package runtimeabimissing
func Use() {}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	_, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{{
		SSA: callerPkg.ssa, Files: []*ast.File{callerPkg.file},
	}}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err == nil || !strings.Contains(err.Error(), "complete runtime ABI requires package") {
		t.Fatalf("complete runtime ABI without runtime error = %v", err)
	}
}
