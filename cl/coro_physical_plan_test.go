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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	"golang.org/x/tools/go/ssa"
)

func TestCoroPhysicalPlanStageIsAtomicExactAndSingleCommit(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func Root(value int) int { return value + 1 }
`)
	root := ssaPkg.Func("Root")
	owner := &preparedEmissionPackage{identity: "foo"}
	physical := &coroPhysicalFunctionPlan{
		function:     root,
		owner:        owner,
		instructions: make(map[ssa.Instruction]coroPhysicalInstructionPlan),
	}
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			physical.instructions[instruction] = coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionOrdinary}
		}
	}
	key := emissionFunctionOwnerKey{function: root, owner: owner}
	expected := map[emissionFunctionOwnerKey]none{key: {}}

	stage := newCoroPhysicalPlanStage()
	if err := stage.freezePhysicalFunctionPlan(physical); err != nil {
		t.Fatal(err)
	}
	if err := stage.freezePhysicalFunctionPlan(physical); err == nil || !strings.Contains(err.Error(), "frozen more than once") {
		t.Fatalf("duplicate physical freeze = %v", err)
	}

	missing := newCoroProgramIR()
	missing.callsFrozen = true
	if err := missing.commitPhysicalFunctionPlans(newCoroPhysicalPlanStage(), expected); err == nil ||
		!strings.Contains(err.Error(), "has 0 function owners, want 1") {
		t.Fatalf("incomplete physical commit = %v", err)
	}
	if missing.physicalPlansSealed || len(missing.physicalPlans) != 0 {
		t.Fatal("failed physical commit mutated ProgramIR")
	}

	ir := newCoroProgramIR()
	ir.callsFrozen = true
	if err := ir.commitPhysicalFunctionPlans(stage, expected); err != nil {
		t.Fatal(err)
	}
	if loaded, err := ir.physicalFunctionPlan(root, owner); err != nil || loaded != physical {
		t.Fatalf("frozen physical lookup = %p, %v; want %p", loaded, err, physical)
	}
	if err := ir.commitPhysicalFunctionPlans(stage, expected); err == nil || !strings.Contains(err.Error(), "committed more than once") {
		t.Fatalf("second physical commit = %v", err)
	}
}

func TestCoroPhysicalRecipeObserverRejectsMissingAndMismatch(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func Root(value int) int { return value + 1 }
`)
	root := ssaPkg.Func("Root")
	instruction := root.Blocks[0].Instrs[0]
	owner := &preparedEmissionPackage{identity: "foo"}
	physical := &coroPhysicalFunctionPlan{
		function: root,
		owner:    owner,
		instructions: map[ssa.Instruction]coroPhysicalInstructionPlan{
			instruction: {recipe: coroPhysicalInstructionDeref, nilGuard: true},
		},
	}
	ctx := &context{
		compilation:          &Compilation{},
		emissionUniverse:     &EmissionUniverse{coroProgramIR: newCoroProgramIR()},
		coroPhysicalEmission: true,
		coroPhysicalPlan:     physical,
	}

	missing := captureCoroSitePlanPanic(func() { ctx.beginCoroSiteEmission(instruction)() })
	if !strings.Contains(missing, "omitted frozen physical recipe deref") {
		t.Fatalf("missing physical observation = %q", missing)
	}
	mismatch := captureCoroSitePlanPanic(func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroPhysicalInstruction(instruction, coroPhysicalInstructionIndex)
	})
	if !strings.Contains(mismatch, "emitted physical recipe index, frozen SitePlan requires deref") {
		t.Fatalf("mismatched physical observation = %q", mismatch)
	}
	missingGuard := captureCoroSitePlanPanic(func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroPhysicalInstruction(instruction, coroPhysicalInstructionDeref)
	})
	if !strings.Contains(missingGuard, "physical nil-guard emission=false, frozen SitePlan requires true") {
		t.Fatalf("missing physical guard observation = %q", missingGuard)
	}
	func() {
		finish := ctx.beginCoroSiteEmission(instruction)
		defer finish()
		ctx.observeCoroPhysicalInstruction(instruction, coroPhysicalInstructionDeref)
		ctx.observeCoroPhysicalNilGuard(instruction)
	}()
}

func TestCoroPhysicalCodegenRejectsMissingCommittedPlan(t *testing.T) {
	prog, ssaPkg, files, universe, plan := prepareCoroPhysicalValueTransportABI(t, nil)
	defer prog.Dispose()
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.EnableCoroPlainDispatch = true
	compilation.FuncRepABI = coro.FuncRepABIV1
	if err := compilation.preflightCoroPlan(); err != nil {
		t.Fatal(err)
	}
	for key := range universe.coroProgramIR.physicalPlans {
		delete(universe.coroProgramIR.physicalPlans, key)
	}
	message := captureCoroSitePlanPanic(func() {
		pkg, _, err := NewPackageExWithEmbedOptions(
			prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
			PackageOptions{Compilation: compilation},
		)
		if pkg != nil {
			pkg.Module().Dispose()
		}
		if err != nil {
			panic(err)
		}
	})
	if !strings.Contains(message, "has no frozen physical plan") {
		t.Fatalf("missing physical plan codegen failure = %q", message)
	}
}
