// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package cl

import (
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroNativeFaultProjectionPreservesGlobalClosure(t *testing.T) {
	pkg, _, files := buildGoSSAPkg(t, `package foo
func Leaf() int { return 1 }
func Root() int { return Leaf() + 1 }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	whole, err := coro.NewSSAEmissionUniverse(pkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{{Function: pkg.Func("Root"), Demand: coro.AsyncDemand}}, coro.SSAConfig{EmissionUniverse: whole, FunctionIDs: universe.FunctionIDConfig(), MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}
	var occurrence ssa.CallInstruction
	for _, block := range pkg.Func("Root").Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(*ssa.Call); ok && call.Common().StaticCallee() == pkg.Func("Leaf") {
				occurrence = call
			}
		}
	}
	if occurrence == nil {
		t.Fatal("missing exact source call")
	}
	for _, isolated := range []bool{false, true} {
		faults := &coroNativeFaultProjection{required: make(map[*ssa.Function]bool), isolatedCalls: map[ssa.CallInstruction]bool{occurrence: isolated}}
		caps, err := deriveCoroFunctionProgramCapabilitiesWithFaultProjection(plan, map[*ssa.Function]coro.ProgramCapabilities{pkg.Func("Leaf"): coro.NativeDefaultFaultBoundaryProgramCapability() | coro.NewProgramCapabilities(true, false)}, nil, faults)
		if err != nil {
			t.Fatal(err)
		}
		if root := caps[pkg.Func("Root")]; !root.NativeDefaultFaultBoundary() || !root.Worker() {
			t.Fatalf("global closure changed: %#x", root)
		}
		if !faults.required[pkg.Func("Leaf")] || faults.required[pkg.Func("Root")] == isolated {
			t.Fatalf("isolated=%t projection=%v", isolated, faults.required)
		}
	}
}

func TestCoroNativeFaultIsolationRequiresLocalPhysicalChild(t *testing.T) {
	pkg, _, _ := buildGoSSAPkg(t, `package foo
func Child() {}
func Imported()
func Parent() { Child(); Imported() }
`)
	parent, child, imported := pkg.Func("Parent"), pkg.Func("Child"), pkg.Func("Imported")
	var childCall, importCall *ssa.Call
	for _, block := range parent.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(*ssa.Call); ok {
				switch call.Common().StaticCallee() {
				case child:
					childCall = call
				case imported:
					importCall = call
				}
			}
		}
	}
	if childCall == nil || importCall == nil {
		t.Fatal("missing exact source occurrences")
	}
	for _, test := range []struct {
		name           string
		defined, mixed bool
		control        coroPhysicalControlRecipe
		want           bool
	}{
		{"local-child", true, false, coroPhysicalControlDirectAwait, true},
		{"missing-child-definition", false, false, coroPhysicalControlDirectAwait, false},
		{"outcome-child", true, false, coroPhysicalControlDirectOutcome, false},
		{"raw-child", true, false, coroPhysicalControlRawPlainCall, false},
		{"owner-disagreement", true, true, coroPhysicalControlDirectAwait, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ir := newCoroProgramIR()
			ir.physicalPlansSealed = true
			owner := &preparedEmissionPackage{identity: "first"}
			ir.physicalPlans[emissionFunctionOwnerKey{function: parent, owner: owner}] = &coroPhysicalFunctionPlan{function: parent, instructions: map[ssa.Instruction]coroPhysicalInstructionPlan{childCall: {control: test.control, controlTarget: child}, importCall: {control: coroPhysicalControlDirectAwait, controlTarget: imported}}}
			// Even a declaration carrying a physical placeholder cannot certify a
			// producer's native landing. Imported archive bodies stay conservative.
			ir.physicalPlans[emissionFunctionOwnerKey{function: imported, owner: owner}] = &coroPhysicalFunctionPlan{function: imported}
			if test.defined {
				ir.physicalPlans[emissionFunctionOwnerKey{function: child, owner: owner}] = &coroPhysicalFunctionPlan{function: child}
			}
			if test.mixed {
				other := &preparedEmissionPackage{identity: "second"}
				ir.physicalPlans[emissionFunctionOwnerKey{function: parent, owner: other}] = &coroPhysicalFunctionPlan{function: parent, instructions: map[ssa.Instruction]coroPhysicalInstructionPlan{childCall: {control: coroPhysicalControlDirectOutcome, controlTarget: child}}}
			}
			got := ir.nativeFaultIsolatedCalls()
			if got[childCall] != test.want || got[importCall] {
				t.Fatalf("local isolated=%t want %t; imported isolated=%t", got[childCall], test.want, got[importCall])
			}
		})
	}
}
