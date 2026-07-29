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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	"golang.org/x/tools/go/ssa"
)

// loweredRuntimeHelpers keeps older focused tests source-compatible while
// making them exercise the frozen SitePlan rather than the removed raw-SSA
// production classifier.
func (u *EmissionUniverse) loweredRuntimeHelpers(ctx *context, instruction ssa.Instruction) []string {
	if u.coroProgramIR == nil {
		shape, _ := prepareCoroEmissionFunctionShape(instruction.Parent())
		helpers, err := u.classifyCoroRuntimeHelpers(ctx, shape, instruction)
		if err != nil {
			panic(err)
		}
		return helpers
	}
	helpers, err := u.coroProgramIR.plannedRuntimeHelpers(ctx, instruction)
	if err != nil {
		shape, _ := prepareCoroEmissionFunctionShape(instruction.Parent())
		helpers, classifyErr := u.classifyCoroRuntimeHelpers(ctx, shape, instruction)
		if classifyErr != nil {
			panic(classifyErr)
		}
		return helpers
	}
	return helpers
}

func (u *EmissionUniverse) plainRepresentationRuntimeHelpers(ctx *context, instruction ssa.Instruction) []string {
	if u.coroProgramIR == nil {
		shape, _ := prepareCoroEmissionFunctionShape(instruction.Parent())
		managed, err := u.classifyCoroRuntimeHelpers(ctx, shape, instruction)
		if err != nil {
			panic(err)
		}
		return u.classifyPlainRuntimeHelpers(ctx, instruction, managed)
	}
	plan, err := u.coroProgramIR.sitePlan(ctx, instruction)
	if err != nil {
		shape, _ := prepareCoroEmissionFunctionShape(instruction.Parent())
		managed, classifyErr := u.classifyCoroRuntimeHelpers(ctx, shape, instruction)
		if classifyErr != nil {
			panic(classifyErr)
		}
		return u.classifyPlainRuntimeHelpers(ctx, instruction, managed)
	}
	return plan.plainRuntimeHelpers
}

func TestCoroSitePlanConsumersFailClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(coroEmissionSitePlan) coroEmissionSitePlan
		want   string
	}{
		{
			name: "unexpected actual helper",
			mutate: func(plan coroEmissionSitePlan) coroEmissionSitePlan {
				plan.managedRuntimeHelpers = nil
				return plan
			},
			want: "runtime helper capability validation found no lowered helper",
		},
		{
			name: "missing planned helper",
			mutate: func(plan coroEmissionSitePlan) coroEmissionSitePlan {
				plan.managedRuntimeHelpers = append(plan.managedRuntimeHelpers, coroPlannedRuntimeHelper{
					name:      "UnemittedHelper",
					placement: coroRuntimeHelperAtSource,
				})
				return plan
			},
			want: "operation lowers through unapproved runtime helper UnemittedHelper",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := prepareCoroStringConcatTestPlan(t, nil, true)
			defer fixture.prog.Dispose()
			owners := fixture.universe.sortedUseOwners(fixture.root)
			if len(owners) != 1 {
				t.Fatalf("Root owners = %d, want 1", len(owners))
			}
			key := emissionFunctionOwnerKey{function: fixture.root, owner: owners[0]}
			instruction := fixture.concats[0]
			plan, ok := fixture.universe.coroProgramIR.sitePlans[key][instruction]
			if !ok {
				t.Fatal("string concat has no frozen SitePlan")
			}
			fixture.universe.coroProgramIR.sitePlans[key][instruction] = test.mutate(cloneCoroEmissionSitePlan(plan))

			message := compileCoroSitePlanFailure(fixture)
			if !strings.Contains(message, test.want) {
				t.Fatalf("compile failure = %q, want %q", message, test.want)
			}
		})
	}
}

func TestCoroSitePlanEmissionObserverRejectsUnexpectedAndMissing(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*context, ssa.Instruction)
		want string
	}{
		{
			name: "unexpected",
			run: func(ctx *context, instruction ssa.Instruction) {
				finish := ctx.beginCoroSiteEmission(instruction)
				defer finish()
				ctx.observeCoroSiteRuntimeHelper("UnplannedHelper")
			},
			want: "emitted runtime helper \"UnplannedHelper\" absent from its frozen SitePlan",
		},
		{
			name: "missing",
			run: func(ctx *context, instruction ssa.Instruction) {
				ctx.beginCoroSiteEmission(instruction)()
			},
			want: "omitted frozen runtime helper(s) StringCat",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := prepareCoroStringConcatTestPlan(t, nil, true)
			defer fixture.prog.Dispose()
			owners := fixture.universe.sortedUseOwners(fixture.root)
			if len(owners) != 1 {
				t.Fatalf("Root owners = %d, want 1", len(owners))
			}
			ctx, err := fixture.universe.functionABIContext(fixture.root, owners[0])
			if err != nil {
				t.Fatal(err)
			}
			ctx.compilation = &Compilation{CoroPlan: fixture.plan, EmissionUniverse: fixture.universe}
			ctx.coroEmission = &coroPhysicalEmissionSession{phase: coroPhysicalEmissionPrologue}
			message := captureCoroSitePlanPanic(func() { test.run(ctx, fixture.concats[0]) })
			if !strings.Contains(message, test.want) {
				t.Fatalf("observer panic = %q, want %q", message, test.want)
			}
		})
	}
}

func TestCoroPhysicalPhiObservesFrozenSemanticSitePlan(t *testing.T) {
	prog, ssaPkg, files, universe, plan := prepareCoroRootFactoryTestPlan(
		t,
		`package foo
func Leaf(flag bool, left, right uint32) uint32 {
	value := left
	if flag {
		value = right
	}
	return value
}
`,
		[]coroRootFactoryTestRoot{{name: "Leaf", demand: coro.AsyncDemand}},
		[]string{"Leaf"},
	)
	defer prog.Dispose()
	leaf := ssaPkg.Func("Leaf")
	var phi *ssa.Phi
	for _, block := range leaf.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.Phi); ok {
				phi = candidate
				break
			}
		}
	}
	if phi == nil {
		t.Fatal("Leaf fixture has no Phi instruction")
	}

	compilation := &Compilation{
		CoroPlan:         plan,
		EmissionUniverse: universe,
		CoroPlanObserver: func(*ssa.Package, *coro.SSAPlan) {
			owners := universe.sortedUseOwners(leaf)
			if len(owners) != 1 {
				t.Fatalf("Leaf physical owners = %d, want 1", len(owners))
			}
			key := emissionFunctionOwnerKey{function: leaf, owner: owners[0]}
			physical := universe.coroProgramIR.physicalPlans[key]
			if physical == nil {
				t.Fatal("Leaf has no committed physical plan")
			}
			instruction, ok := physical.instructions[phi]
			if !ok {
				t.Fatal("Phi has no frozen physical SitePlan")
			}
			// Deliberately corrupt the immutable test projection after
			// preflight. Codegen must still consume this exact Phi SitePlan and
			// fail closed; bypassing the deferred Phi observer would silently
			// accept it.
			instruction.semantic.recipe = ""
			physical.instructions[phi] = instruction
		},
	}
	message := captureCoroSitePlanPanic(func() {
		compiled, _, compileErr := NewPackageExWithEmbedOptions(
			prog,
			nil,
			nil,
			nil,
			ssaPkg,
			files,
			goembed.VarMap{},
			PackageOptions{Compilation: compilation},
		)
		if compileErr != nil {
			panic(compileErr)
		}
		compiled.Module().Dispose()
	})
	if !strings.Contains(message, "semantic recipe emission has no exact physical SitePlan") {
		t.Fatalf("corrupt Phi SitePlan failure = %q", message)
	}
}

func captureCoroSitePlanPanic(run func()) (message string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			message = fmt.Sprint(recovered)
		}
	}()
	run()
	return ""
}

func compileCoroSitePlanFailure(fixture coroStringConcatTestPlan) (message string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			message = fmt.Sprint(recovered)
		}
	}()
	compilation := &Compilation{CoroPlan: fixture.plan, EmissionUniverse: fixture.universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	for _, pkg := range []emissionTestPackage{fixture.runtimePkg, fixture.fooPkg} {
		compiled, _, err := NewPackageExWithEmbedOptions(
			fixture.prog, nil, nil, nil, pkg.ssa, []*ast.File{pkg.file}, goembed.VarMap{},
			PackageOptions{Compilation: compilation},
		)
		if err != nil {
			return err.Error()
		}
		compiled.Module().Dispose()
	}
	return ""
}
