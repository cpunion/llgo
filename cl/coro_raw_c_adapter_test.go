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
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroRawCAdapterFixtureSource = `package adapter

//llgo:type C
type RawCallback func(int) int

func sink(RawCallback) {}

func target(value int) int { return value + 1 }

func RawOnly() {
	sink(RawCallback(target))
}

func Mixed(value int) int {
	sink(RawCallback(target))
	return target(value)
}

func DynamicToRaw(fn func(int) int) RawCallback {
	return RawCallback(fn)
}

func RawToManaged(fn RawCallback) func(int) int {
	return (func(int) int)(fn)
}
`

type coroRawCAdapterFixture struct {
	prog     llssa.Program
	pkg      *ssa.Package
	universe *EmissionUniverse
}

func prepareCoroRawCAdapterFixture(t *testing.T) coroRawCAdapterFixture {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroRawCAdapterFixtureSource)
	prog := newLLSSAProg(t)
	ParsePkgSyntax(prog, ssaPkg.Prog.Fset, ssaPkg.Pkg, files)
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return coroRawCAdapterFixture{prog: prog, pkg: ssaPkg, universe: universe}
}

func (fixture coroRawCAdapterFixture) analyze(
	t *testing.T,
	root *ssa.Function,
	rawCallbackOwner *ssa.Function,
) (*coro.SSAPlan, error) {
	t.Helper()
	ssaUniverse, err := coro.NewSSAEmissionUniverse(fixture.pkg.Prog, fixture.universe.Functions())
	if err != nil {
		return nil, err
	}
	functionIDs := fixture.universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	target := fixture.pkg.Func("target")
	sink := fixture.pkg.Func("sink")
	return coro.AnalyzeSSA(fixture.pkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyRawCFunctionType: func(typ types.Type) (bool, error) {
			_, signature := types.Unalias(typ).Underlying().(*types.Signature)
			return signature && fixture.prog.TypeBackground(typ) == llssa.InC, nil
		},
		ClassifyRawDirectPlainCallArgument: func(owner *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
			return rawCallbackOwner != nil && owner == rawCallbackOwner && call.Common() != nil &&
				call.Common().StaticCallee() == sink && argument == 0, nil
		},
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == target {
				// Force a distinct managed coroutine primary in the mixed-demand
				// test. Raw-only demand still emits just its validated legacy body.
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, RawPlainEntry: true}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
}

func TestCoroRawCAdapterResolvesRawOnlyExactStaticTarget(t *testing.T) {
	fixture := prepareCoroRawCAdapterFixture(t)
	defer fixture.prog.Dispose()

	owner := fixture.pkg.Func("RawOnly")
	plan, err := fixture.analyze(t, owner, owner)
	if err != nil {
		t.Fatal(err)
	}
	target := fixture.pkg.Func("target")
	targetPlan, found := plan.FunctionPlan(target)
	if !found || !targetPlan.RawPlainOnly || targetPlan.ManagedDemand != coro.NoDemand ||
		!targetPlan.RawPlainDemand || !targetPlan.RawPlainEntry || targetPlan.Emission != coro.EmitRawPlain ||
		!plan.HasRawPlainVariant(target) {
		t.Fatalf("raw-only target plan = %+v, present=%t variant=%t", targetPlan, found, plan.HasRawPlainVariant(target))
	}

	change := coroRawCAdapterChangeType(t, owner)
	adapter, recognized, err := resolveCoroRawCChangeType(plan, fixture.universe, owner, change)
	if err != nil {
		t.Fatal(err)
	}
	if !recognized || adapter.target != target || adapter.rawRetag || adapter.resultType == nil {
		t.Fatalf("raw-only adapter = %+v, recognized=%t; want exact target raw entry", adapter, recognized)
	}
	coroAssertRawCAdapterValuePlans(t, plan, change, targetPlan.ID, coro.DirectPlain)
}

func TestCoroRawCAdapterSelectionIsOccurrenceSpecific(t *testing.T) {
	fixture := prepareCoroRawCAdapterFixture(t)
	defer fixture.prog.Dispose()

	owner := fixture.pkg.Func("Mixed")
	plan, err := fixture.analyze(t, owner, owner)
	if err != nil {
		t.Fatal(err)
	}
	target := fixture.pkg.Func("target")
	targetPlan, found := plan.FunctionPlan(target)
	if !found || targetPlan.RawPlainOnly || targetPlan.ManagedDemand == coro.NoDemand ||
		!targetPlan.RawPlainDemand || !targetPlan.RawPlainEntry || targetPlan.Emission != coro.EmitCoroutine ||
		targetPlan.Primary != coro.PrimaryCoroutine || !plan.HasRawPlainVariant(target) {
		t.Fatalf("mixed target plan = %+v, present=%t variant=%t", targetPlan, found, plan.HasRawPlainVariant(target))
	}

	change := coroRawCAdapterChangeType(t, owner)
	adapter, recognized, err := resolveCoroRawCChangeType(plan, fixture.universe, owner, change)
	if err != nil {
		t.Fatal(err)
	}
	if !recognized || adapter.target != target || adapter.rawRetag {
		t.Fatalf("mixed raw occurrence adapter = %+v, recognized=%t", adapter, recognized)
	}
	coroAssertRawCAdapterValuePlans(t, plan, change, targetPlan.ID, coro.DirectCoro)

	managedCall := coroRawCAdapterStaticCall(t, owner, target)
	callPlan, found := plan.CallPlan(managedCall)
	if !found || callPlan.Transport != coro.ManagedTransport || callPlan.Rep != coro.DirectCoro ||
		len(callPlan.Targets) != 1 || callPlan.Targets[0] != targetPlan.ID {
		t.Fatalf("unrelated managed call plan = %+v, present=%t; want managed coroutine entry", callPlan, found)
	}
}

func TestCoroRawCAdapterDynamicCrossingsFailClosed(t *testing.T) {
	fixture := prepareCoroRawCAdapterFixture(t)
	defer fixture.prog.Dispose()

	t.Run("ManagedToRawC", func(t *testing.T) {
		owner := fixture.pkg.Func("DynamicToRaw")
		plan, err := fixture.analyze(t, owner, nil)
		if err != nil {
			t.Fatal(err)
		}
		change := coroRawCAdapterChangeType(t, owner)
		_, recognized, err := resolveCoroRawCChangeType(plan, fixture.universe, owner, change)
		if !recognized || err == nil ||
			!strings.Contains(err.Error(), "Go-to-RawC source requires an exact managed direct entry, got managed/dispatch") {
			t.Fatalf("dynamic Managed-to-RawC adapter = recognized %t, error %v", recognized, err)
		}
	})

	t.Run("RawCToManaged", func(t *testing.T) {
		owner := fixture.pkg.Func("RawToManaged")
		plan, err := fixture.analyze(t, owner, nil)
		if err != nil {
			t.Fatal(err)
		}
		change := coroRawCAdapterChangeType(t, owner)
		_, recognized, err := resolveCoroRawCChangeType(plan, fixture.universe, owner, change)
		if !recognized || err == nil || !strings.Contains(err.Error(), "RawC-to-Managed ChangeType has no descriptor construction recipe") {
			t.Fatalf("RawC-to-Managed adapter = recognized %t, error %v", recognized, err)
		}
	})
}

func coroRawCAdapterChangeType(t *testing.T, owner *ssa.Function) *ssa.ChangeType {
	t.Helper()
	var found *ssa.ChangeType
	for _, block := range owner.Blocks {
		for _, instruction := range block.Instrs {
			change, ok := instruction.(*ssa.ChangeType)
			if !ok {
				continue
			}
			if found != nil {
				t.Fatalf("function %s has multiple ChangeType instructions", owner)
			}
			found = change
		}
	}
	if found == nil {
		t.Fatalf("function %s has no ChangeType instruction", owner)
	}
	return found
}

func coroRawCAdapterStaticCall(t *testing.T, owner, target *ssa.Function) *ssa.Call {
	t.Helper()
	for _, block := range owner.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common() != nil && call.Common().StaticCallee() == target {
				return call
			}
		}
	}
	t.Fatalf("function %s has no static call to %s", owner, target)
	return nil
}

func coroAssertRawCAdapterValuePlans(
	t *testing.T,
	plan *coro.SSAPlan,
	change *ssa.ChangeType,
	target coro.FunctionID,
	wantSourceRep coro.FuncRep,
) {
	t.Helper()
	source, sourceFound := plan.ValuePlan(change.X)
	result, resultFound := plan.ValuePlan(change)
	if !sourceFound || len(source.Funcs) != 1 || source.Funcs[0].Transport != coro.ManagedTransport ||
		source.Funcs[0].Rep != wantSourceRep || source.Funcs[0].MayBeNil ||
		len(source.Funcs[0].Targets) != 1 || source.Funcs[0].Targets[0] != target {
		t.Fatalf("raw adapter source plan = %+v, present=%t", source, sourceFound)
	}
	if !resultFound || len(result.Funcs) != 1 || result.Funcs[0].Transport != coro.RawCCodePointer ||
		result.Funcs[0].Rep != coro.DirectPlain || result.Funcs[0].MayBeNil ||
		len(result.Funcs[0].Targets) != 1 || result.Funcs[0].Targets[0] != target {
		t.Fatalf("raw adapter result plan = %+v, present=%t", result, resultFound)
	}
}
