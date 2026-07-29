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

const coroCallableTransportFixtureSource = `package foo

//llgo:type C
type CFunc func(int) int

type Mixed struct {
	Raw CFunc
	Managed func(int) int
}

func BoxRaw(value CFunc) any { return value }
func AssertRaw(value any) (CFunc, bool) {
	result, ok := value.(CFunc)
	return result, ok
}

func BoxMixed(value Mixed) any { return value }
func AssertMixed(value any) (Mixed, bool) {
	result, ok := value.(Mixed)
	return result, ok
}

func BoxManaged(value func(int) int) any { return value }
func AssertManaged(value any) (func(int) int, bool) {
	result, ok := value.(func(int) int)
	return result, ok
}
`

type coroCallableTransportFixture struct {
	prog     llssa.Program
	pkg      *ssa.Package
	universe *EmissionUniverse
	plan     *coro.SSAPlan
}

func prepareCoroCallableTransportFixture(t *testing.T) coroCallableTransportFixture {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroCallableTransportFixtureSource)
	prog := newLLSSAProg(t)
	ParsePkgSyntax(prog, ssaPkg.Prog.Fset, ssaPkg.Pkg, files)
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	roots := make(coro.Roots, 0, 6)
	for _, name := range []string{"BoxRaw", "AssertRaw", "BoxMixed", "AssertMixed", "BoxManaged", "AssertManaged"} {
		roots = append(roots, coro.Root{Function: ssaPkg.Func(name), Demand: coro.AsyncDemand})
	}
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, roots, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyRawCFunctionType: func(typ types.Type) (bool, error) {
			_, signature := types.Unalias(typ).Underlying().(*types.Signature)
			return signature && prog.TypeBackground(typ) == llssa.InC, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return coroCallableTransportFixture{prog: prog, pkg: ssaPkg, universe: universe, plan: plan}
}

func TestCoroCallableTransportPreservesRawCAndManagedInterfaceLeaves(t *testing.T) {
	fixture := prepareCoroCallableTransportFixture(t)
	defer fixture.prog.Dispose()

	for _, name := range []string{"BoxRaw", "BoxMixed", "BoxManaged"} {
		fn := fixture.pkg.Func(name)
		box := coroCallableMakeInterface(t, fn)
		if err := validateCoroCallableTransportValue(fixture.plan, fn, box.X, fixture.universe); err != nil {
			t.Fatalf("%s callable transport: %v", name, err)
		}
	}
	for _, name := range []string{"AssertRaw", "AssertMixed", "AssertManaged"} {
		fn := fixture.pkg.Func(name)
		assertion := coroCallableTypeAssert(t, fn)
		if err := validateCoroCallableTransportValue(fixture.plan, fn, assertion, fixture.universe); err != nil {
			t.Fatalf("%s callable transport: %v", name, err)
		}
	}

	rawBox := coroCallableMakeInterface(t, fixture.pkg.Func("BoxRaw"))
	rawPlan, found := fixture.plan.ValuePlan(rawBox.X)
	if !found || len(rawPlan.Funcs) != 1 || rawPlan.Funcs[0].Transport != coro.RawCCodePointer || rawPlan.Funcs[0].Rep != coro.DirectPlain {
		t.Fatalf("raw box operand plan = %+v, present=%t; want one raw direct pointer", rawPlan, found)
	}
	rawBoxAudit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.pkg.Func("BoxRaw"), "")
	if err != nil {
		t.Fatal(err)
	}
	if reason := rawBoxAudit.validateMakeInterface(rawBox); reason != "" {
		t.Fatalf("raw C interface box physical validation: %s", reason)
	}
	mixedBox := coroCallableMakeInterface(t, fixture.pkg.Func("BoxMixed"))
	mixedPlan, found := fixture.plan.ValuePlan(mixedBox.X)
	if !found || len(mixedPlan.Funcs) != 2 ||
		mixedPlan.Funcs[0].Transport != coro.RawCCodePointer || mixedPlan.Funcs[0].Rep != coro.DirectPlain ||
		mixedPlan.Funcs[1].Transport != coro.ManagedTransport || mixedPlan.Funcs[1].Rep != coro.Dispatch {
		t.Fatalf("mixed box operand plan = %+v, present=%t; want raw direct plus managed descriptor", mixedPlan, found)
	}
}

func TestCoroCallableTransportTypeAssertUsesPhysicalHelperContract(t *testing.T) {
	fixture := prepareCoroCallableTransportFixture(t)
	defer fixture.prog.Dispose()

	rawFn := fixture.pkg.Func("AssertRaw")
	raw := coroCallableTypeAssert(t, rawFn)
	rawAudit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, rawFn, "")
	if err != nil {
		t.Fatal(err)
	}
	if coroTypeAssertUsesManagedClosure(rawAudit.ctx, raw) {
		t.Fatal("raw C type assertion was classified as a managed closure")
	}
	if reason := rawAudit.validateTypeAssert(raw); reason != "" {
		t.Fatalf("raw C type assertion physical validation: %s", reason)
	}

	mixedFn := fixture.pkg.Func("AssertMixed")
	mixed := coroCallableTypeAssert(t, mixedFn)
	mixedAudit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, mixedFn, "")
	if err != nil {
		t.Fatal(err)
	}
	if reason := mixedAudit.validateTypeAssert(mixed); reason != "" {
		t.Fatalf("mixed aggregate type assertion physical validation: %s", reason)
	}

	managedFn := fixture.pkg.Func("AssertManaged")
	managed := coroCallableTypeAssert(t, managedFn)
	managedAudit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, managedFn, "")
	if err != nil {
		t.Fatal(err)
	}
	if !coroTypeAssertUsesManagedClosure(managedAudit.ctx, managed) {
		t.Fatal("managed type assertion did not retain the closure descriptor contract")
	}
	if reason := managedAudit.validateTypeAssert(managed); !strings.Contains(reason, "MatchesClosure") {
		t.Fatalf("managed type assertion validation = %q; want MatchesClosure to remain in the frozen helper contract", reason)
	}
}

func TestCoroCallableTransportRejectsForgedDescriptorPlans(t *testing.T) {
	for _, test := range []struct {
		name string
		leaf coro.FuncRepLeaf
		want coro.FuncTransport
	}{
		{
			name: "raw C disguised as descriptor",
			leaf: coro.FuncRepLeaf{Rep: coro.Dispatch, Transport: coro.RawCCodePointer},
			want: coro.RawCCodePointer,
		},
		{
			name: "managed closure disguised as direct pointer",
			leaf: coro.FuncRepLeaf{Rep: coro.DirectPlain, Transport: coro.ManagedTransport},
			want: coro.ManagedTransport,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateCoroInterfaceCallableLeaf(test.leaf, test.want); err == nil {
				t.Fatalf("forged leaf %+v unexpectedly passed", test.leaf)
			}
		})
	}
}

func coroCallableMakeInterface(t *testing.T, fn *ssa.Function) *ssa.MakeInterface {
	t.Helper()
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if box, ok := instruction.(*ssa.MakeInterface); ok {
				return box
			}
		}
	}
	t.Fatalf("function %s has no MakeInterface", fn)
	return nil
}

func coroCallableTypeAssert(t *testing.T, fn *ssa.Function) *ssa.TypeAssert {
	t.Helper()
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if assertion, ok := instruction.(*ssa.TypeAssert); ok {
				return assertion
			}
		}
	}
	t.Fatalf("function %s has no TypeAssert", fn)
	return nil
}
