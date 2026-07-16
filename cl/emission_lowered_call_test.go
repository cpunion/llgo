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

	"golang.org/x/tools/go/ssa"
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

func TestEmissionUniverseLoweredCallUnwindOnlyUsesCFGAndAllSites(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/loweredunwind", `package loweredunwind
func Owner(ok bool) int {
	if !ok { panic("bad") }
	return 1
}
	func Helper() {}
`)
	testProg.ssa.Build()
	owner := pkg.ssa.Func("Owner")
	helper := pkg.ssa.Func("Helper")
	// Keep normalReturnBlocks at its zero value: hand-built/report universes
	// must receive the same structural CFG answer as production universes whose
	// constructor eagerly initializes the cache.
	universe := &EmissionUniverse{
		required:     map[*ssa.Function]none{owner: {}, helper: {}},
		aliases:      make(map[*ssa.Function]*ssa.Function),
		loweredCalls: make(map[*ssa.Function]map[string]coroLoweredCallTarget),
	}
	universe.required[owner] = none{}
	universe.required[helper] = none{}
	var panicInstr, returnInstr ssa.Instruction
	for _, block := range owner.Blocks {
		for _, instr := range block.Instrs {
			switch instr.(type) {
			case *ssa.Panic:
				panicInstr = instr
			case *ssa.Return:
				returnInstr = instr
			}
		}
	}
	if panicInstr == nil || returnInstr == nil {
		t.Fatalf("fixture lacks panic/return instructions:\n%s", owner.String())
	}
	if !universe.loweredCallUnwindOnly(owner, panicInstr) {
		t.Fatal("panic-only CFG block was not classified unwind-only")
	}
	if universe.loweredCallUnwindOnly(owner, returnInstr) {
		t.Fatal("normal Return block was classified unwind-only")
	}
	if err := universe.recordCoroLoweredCallSite(owner, "runtime.Helper", helper, true); err != nil {
		t.Fatal(err)
	}
	if got, err := universe.CoroLoweredCalls(owner); err != nil || len(got) != 1 || !got[0].UnwindOnly {
		t.Fatalf("unwind-only call = %+v, err=%v", got, err)
	}
	if err := universe.recordCoroLoweredCallSite(owner, "runtime.Helper", helper, false); err != nil {
		t.Fatal(err)
	}
	if got, err := universe.CoroLoweredCalls(owner); err != nil || len(got) != 1 || got[0].UnwindOnly {
		t.Fatalf("mixed-site call = %+v, err=%v; normal-return-reachable site must win", got, err)
	}
}

func TestLoweredRuntimeHelpersIncludeMapKeyAndInvokeEdges(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/loweredruntime", `package loweredruntime
type I interface { M() }
func Use(m map[int]int, key int, value I) {
	_ = m[key]
	_, _ = m[key]
	m[key] = 1
	delete(m, key)
	value.M()
}

`)
	testProg.ssa.Build()
	universe, owner := newEmissionABIDemandTestUniverse(testProg, pkg)
	fn := pkg.ssa.Func("Use")
	ctx, err := universe.functionABIContext(fn, owner)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool)
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			for _, helper := range universe.loweredRuntimeHelpers(ctx, instruction) {
				got[helper] = true
			}
		}
	}
	for _, helper := range []string{"AllocU", "MapAccess1", "MapAccess2", "MapAssign", "MapDelete", "IfacePtrData"} {
		if !got[helper] {
			t.Errorf("lowered runtime helpers %v omit %q", got, helper)
		}
	}
}

func TestLoweredRuntimeHelpersMatchStaticIndexFastPath(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/loweredindex", `package loweredindex
func StaticArray(value [4]int) int { return value[1] }
func DynamicArray(value [4]int, index int) int { return value[index] }
func StaticPointer(value *[4]int) int { return value[1] }
func StaticSlice(value []int) int { return value[1] }
`)
	testProg.ssa.Build()
	universe, owner := newEmissionABIDemandTestUniverse(testProg, pkg)
	for _, test := range []struct {
		name      string
		wantRange bool
	}{
		{name: "StaticArray"},
		{name: "DynamicArray", wantRange: true},
		{name: "StaticPointer"},
		{name: "StaticSlice", wantRange: true},
	} {
		fn := pkg.ssa.Func(test.name)
		ctx, err := universe.functionABIContext(fn, owner)
		if err != nil {
			t.Fatal(err)
		}
		hasRange := false
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				for _, helper := range universe.loweredRuntimeHelpers(ctx, instruction) {
					if helper == "CheckIndexRange" {
						hasRange = true
					}
				}
			}
		}
		if hasRange != test.wantRange {
			t.Errorf("%s CheckIndexRange edge = %v, want %v", test.name, hasRange, test.wantRange)
		}
	}
}

func TestLoweredRuntimeHelpersMatchPointerArraySliceFastPath(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/loweredslice", `package loweredslice
func Whole(value *[4]int) []int { return value[:] }
func Partial(value *[4]int) []int { return value[1:] }
func Dynamic(value []int) []int { return value[:] }
`)
	testProg.ssa.Build()
	universe, owner := newEmissionABIDemandTestUniverse(testProg, pkg)
	for _, test := range []struct {
		name       string
		wantHelper bool
	}{
		{name: "Whole"},
		{name: "Partial", wantHelper: true},
		{name: "Dynamic", wantHelper: true},
	} {
		fn := pkg.ssa.Func(test.name)
		ctx, err := universe.functionABIContext(fn, owner)
		if err != nil {
			t.Fatal(err)
		}
		hasSliceHelper := false
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				for _, helper := range universe.loweredRuntimeHelpers(ctx, instruction) {
					if helper == "NewSlice2" || helper == "NewSlice3Bounds" {
						hasSliceHelper = true
					}
				}
			}
		}
		if hasSliceHelper != test.wantHelper {
			t.Errorf("%s slice helper edge = %v, want %v", test.name, hasSliceHelper, test.wantHelper)
		}
	}
}

func TestLoweredRuntimeHelpersIncludeValueReceiverNilCheck(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/loweredreceiver", `package loweredreceiver
type Value struct { N int }
func (Value) Method() {}
func Call(value *Value) { value.Method() }
`)
	testProg.ssa.Build()
	universe, owner := newEmissionABIDemandTestUniverse(testProg, pkg)
	fn := pkg.ssa.Func("Call")
	ctx, err := universe.functionABIContext(fn, owner)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			for _, helper := range universe.loweredRuntimeHelpers(ctx, instruction) {
				if helper == "AssertNilDerefPtr" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("value-receiver lowering omitted AssertNilDerefPtr")
	}
}

func TestLoweredRuntimeHelpersIncludeAddressOfFieldNilCheck(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/loweredfieldaddr", `package loweredfieldaddr
type Value struct { N int }
func Addr(value *Value) *int { return &value.N }
`)
	testProg.ssa.Build()
	universe, owner := newEmissionABIDemandTestUniverse(testProg, pkg)
	fn := pkg.ssa.Func("Addr")
	ctx, err := universe.functionABIContext(fn, owner)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			for _, helper := range universe.loweredRuntimeHelpers(ctx, instruction) {
				if helper == "AssertNilDeref" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("address-of field lowering omitted AssertNilDeref")
	}
}
