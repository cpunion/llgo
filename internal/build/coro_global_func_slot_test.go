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

package build

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
)

func TestCoroGlobalFunctionSlotNilOnlyProof(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
var optional func()
func callOptional() { optional() }
func install() {}
`)
	call, certificate := onlyCoroGlobalFunctionSlotCertificate(t, fixture, "callOptional")
	if !certificate.MayBeNil || certificate.SyncDispatch || len(certificate.Targets) != 0 {
		t.Fatalf("nil-only global slot certificate = %+v", certificate)
	}
	proof := fixture.ctx.coroGlobalFunctionSlots[call]
	if proof.global == nil || proof.global.Name() != "optional" || len(proof.inactive) != 0 {
		t.Fatalf("nil-only global slot proof = %+v", proof)
	}
	plan, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("callOptional"))
	if err != nil {
		t.Fatal(err)
	}
	callPlan, ok := plan.CallPlan(call)
	if !ok || callPlan.Open || callPlan.Rep != coro.Dispatch || callPlan.SyncDispatch || !callPlan.MayBeNil || len(callPlan.Targets) != 0 {
		t.Fatalf("nil-only global slot CallPlan = %+v, present=%t", callPlan, ok)
	}
}

func TestCoroGlobalFunctionSlotNilOnlyTupleResultProof(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
var optional func(string, string) ([]byte, error)
func callOptional(left, right string) ([]byte, error) { return optional(left, right) }
func install() {}
`)
	call, certificate := onlyCoroGlobalFunctionSlotCertificate(t, fixture, "callOptional")
	if !certificate.MayBeNil || certificate.SyncDispatch || len(certificate.Targets) != 0 {
		t.Fatalf("nil-only tuple-result certificate = %+v", certificate)
	}
	plan, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("callOptional"))
	if err != nil {
		t.Fatal(err)
	}
	callPlan, ok := plan.CallPlan(call)
	owner := functionPlanForBuildTest(t, plan, fixture.pkg.Func("callOptional"))
	if !ok || callPlan.Open || !callPlan.MayBeNil || len(callPlan.Targets) != 0 || owner.Exec.IsOpaque() {
		t.Fatalf("nil-only tuple-result plans = call:%+v/%t owner:%+v", callPlan, ok, owner)
	}
}

func TestCoroGlobalFunctionSlotExactTargetAndParameterFlow(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{
			name: "direct store",
			body: `
var optional func()
func target() {}
func callOptional() { optional() }
func useOptional() { optional = target; callOptional() }
func install() {}
`,
		},
		{
			name: "closed parameter store",
			body: `
var optional func()
func target() {}
func register(callback func()) { optional = callback }
func callOptional() { optional() }
func useOptional() { register(target); callOptional() }
func install() {}
`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := buildRequiredCoroRuntimeFixture(t, test.body)
			call, certificate := onlyCoroGlobalFunctionSlotCertificate(t, fixture, "callOptional")
			target := fixture.pkg.Func("target")
			if !certificate.MayBeNil || certificate.SyncDispatch || len(certificate.Targets) != 1 || certificate.Targets[0] != target {
				t.Fatalf("exact global slot certificate = %+v, want target %v", certificate, target)
			}
			if proof := fixture.ctx.coroGlobalFunctionSlots[call]; len(proof.inactive) != 0 {
				t.Fatalf("exact global slot proof has conditional hazards: %+v", proof.inactive)
			}
			plan, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("useOptional"))
			if err != nil {
				t.Fatal(err)
			}
			callPlan, ok := plan.CallPlan(call)
			id, hasID := plan.FunctionID(target)
			if !ok || !hasID || callPlan.Open || callPlan.SyncDispatch || len(callPlan.Targets) != 1 || callPlan.Targets[0] != id {
				t.Fatalf("exact global slot CallPlan = %+v, target id=%q/%t", callPlan, id, hasID)
			}
		})
	}
}

func TestCoroGlobalFunctionSlotDeadParameterWriterIsPlanConditional(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
var optional func()
func register(callback func()) { optional = callback }
func callOptional() { optional() }
func install() {}
`)
	call, certificate := onlyCoroGlobalFunctionSlotCertificate(t, fixture, "callOptional")
	if len(certificate.Targets) != 0 || !certificate.MayBeNil {
		t.Fatalf("dead-writer certificate = %+v, want nil-only", certificate)
	}
	proof := fixture.ctx.coroGlobalFunctionSlots[call]
	if len(proof.inactive) != 1 || proof.inactive[0].owner != fixture.pkg.Func("register") ||
		!strings.Contains(proof.inactive[0].reason, "no frozen static incoming call") {
		t.Fatalf("dead-writer hazards = %+v", proof.inactive)
	}
	plan, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("callOptional"))
	if err != nil {
		t.Fatal(err)
	}
	if writer := functionPlanForBuildTest(t, plan, fixture.pkg.Func("register")); writer.Emission != coro.EmitNone {
		t.Fatalf("dead parameter writer plan = %+v, want EmitNone", writer)
	}
}

func TestCoroGlobalFunctionSlotProofFailsClosedForActiveUnknownFlow(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantReason string
		wantStores int
	}{
		{
			name: "unknown store",
			body: `
var optional func()
//go:linkname choose C.choose_optional
func choose() func()
func callOptional() { optional() }
func useOptional() { optional = choose(); callOptional() }
func install() {}
`,
			wantReason: "factory result",
		},
		{
			name: "cell alias escape",
			body: `
var optional func()
func mutate(cell *func()) { *cell = nil }
func callOptional() { optional() }
func useOptional() { mutate(&optional); callOptional() }
func install() {}
`,
			wantReason: "cell address escapes",
		},
		{
			name: "exact publication plus active cell escape",
			body: `
var optional func()
func target() {}
func publish() { optional = target }
func mutate(cell *func()) { *cell = nil }
func callOptional() { optional() }
func useOptional() { publish(); mutate(&optional); callOptional() }
func install() {}
`,
			wantReason: "cell address escapes",
			wantStores: 1,
		},
		{
			name: "active external writer",
			body: `
var optional func()
//go:linkname rawWrite C.raw_write_optional
func rawWrite(*func())
func callOptional() { optional() }
func useOptional() { rawWrite(&optional); callOptional() }
func install() {}
`,
			wantReason: "cell address escapes",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := buildRequiredCoroRuntimeFixture(t, test.body)
			call, _ := onlyCoroGlobalFunctionSlotCertificate(t, fixture, "callOptional")
			proof := fixture.ctx.coroGlobalFunctionSlots[call]
			if len(proof.stores) != test.wantStores {
				t.Fatalf("global slot Store proofs = %+v, want %d", proof.stores, test.wantStores)
			}
			found := false
			for _, hazard := range proof.inactive {
				if strings.Contains(hazard.reason, test.wantReason) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("global slot hazards = %+v, want reason containing %q", proof.inactive, test.wantReason)
			}
			_, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("useOptional"))
			if err == nil || (!strings.Contains(err.Error(), "omitted an active writer/escape") &&
				!strings.Contains(err.Error(), "unsupported execution constraint")) {
				t.Fatalf("active unknown global writer error = %v", err)
			}
		})
	}
}

func TestCoroGlobalFunctionSlotParameterIncomingUnknownFailsClosed(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
var optional func()
//go:linkname choose C.choose_optional_incoming
func choose() func()
func register(callback func()) { optional = callback }
func callOptional() { optional() }
func useOptional() { register(choose()); callOptional() }
func install() {}
`)
	call, certificate := onlyCoroGlobalFunctionSlotCertificate(t, fixture, "callOptional")
	if len(certificate.Targets) != 0 || !certificate.MayBeNil {
		t.Fatalf("unknown incoming certificate = %+v, want conditionally nil-only", certificate)
	}
	proof := fixture.ctx.coroGlobalFunctionSlots[call]
	if len(proof.inactive) == 0 || proof.inactive[0].owner != fixture.pkg.Func("useOptional") {
		t.Fatalf("unknown incoming hazards = %+v, want active caller useOptional", proof.inactive)
	}
	dormant, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("callOptional"))
	if err != nil {
		t.Fatalf("dormant unknown incoming edge: %v", err)
	}
	if caller := functionPlanForBuildTest(t, dormant, fixture.pkg.Func("useOptional")); caller.Emission != coro.EmitNone {
		t.Fatalf("dormant unknown incoming caller plan = %+v, want EmitNone", caller)
	}
	_, err = analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("useOptional"))
	if err == nil || !strings.Contains(err.Error(), "omitted an active writer/escape") {
		t.Fatalf("active unknown incoming error = %v", err)
	}
}

func TestCoroGlobalFunctionSlotStaticFactoryResultClosure(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
var optional func()
var sink int
func makeCallback(value int) func() {
	return func() { sink = value }
}
func useOptional() { optional = makeCallback(42); optional() }
func install() {}
`)
	call, certificate := onlyCoroGlobalFunctionSlotCertificate(t, fixture, "useOptional")
	factory := fixture.pkg.Func("makeCallback")
	if factory == nil || len(factory.AnonFuncs) != 1 {
		t.Fatalf("factory anonymous functions = %d, want one", len(factory.AnonFuncs))
	}
	target := factory.AnonFuncs[0]
	if len(target.FreeVars) != 1 || !certificate.MayBeNil || certificate.SyncDispatch ||
		len(certificate.Targets) != 1 || certificate.Targets[0] != target {
		t.Fatalf("factory-result certificate = %+v, target=%v free-vars=%d", certificate, target, len(target.FreeVars))
	}
	if proof := fixture.ctx.coroGlobalFunctionSlots[call]; len(proof.inactive) != 0 {
		t.Fatalf("factory-result proof has conditional hazards: %+v", proof.inactive)
	}
	plan, err := fixture.input.Analyze(coro.Roots{{Function: fixture.pkg.Func("useOptional"), Demand: coro.AsyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
		FunctionIDs:          fixture.functionIDs,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == target {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	callPlan, planned := plan.CallPlan(call)
	targetPlan := functionPlanForBuildTest(t, plan, target)
	if !planned || callPlan.Open || callPlan.Rep != coro.Dispatch || len(callPlan.Targets) != 1 ||
		targetPlan.FuncRep != coro.Dispatch || targetPlan.Emission != coro.EmitCoroutine ||
		targetPlan.Primary != coro.PrimaryCoroutine {
		t.Fatalf("captured factory result plans = call:%+v/%t target:%+v", callPlan, planned, targetPlan)
	}
}

func TestCoroGlobalFunctionSlotStaticFactoryExtractResult(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
var optional func()
var sink int
func makeCallback(value int) (int, func()) {
	return value, func() { sink = value }
}
func useOptional() { _, optional = makeCallback(7); optional() }
func install() {}
`)
	_, certificate := onlyCoroGlobalFunctionSlotCertificate(t, fixture, "useOptional")
	factory := fixture.pkg.Func("makeCallback")
	if factory == nil || len(factory.AnonFuncs) != 1 || len(certificate.Targets) != 1 ||
		certificate.Targets[0] != factory.AnonFuncs[0] {
		t.Fatalf("extracted factory-result certificate = %+v", certificate)
	}
}

func TestCoroGlobalFunctionSlotStaticFactoryUnknownReturnFailsClosed(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
var optional func()
func target() {}
//go:linkname rawFactory C.raw_factory
func rawFactory() func()
func makeCallback(flag bool) func() {
	if flag { return target }
	return rawFactory()
}
func useOptional() { optional = makeCallback(true); optional() }
func install() {}
`)
	call, certificate := onlyCoroGlobalFunctionSlotCertificate(t, fixture, "useOptional")
	if len(certificate.Targets) != 1 || certificate.Targets[0] != fixture.pkg.Func("target") {
		t.Fatalf("partially-known factory certificate = %+v", certificate)
	}
	proof := fixture.ctx.coroGlobalFunctionSlots[call]
	if len(proof.inactive) == 0 {
		t.Fatal("unknown factory return did not remain a conditional hazard")
	}
	found := false
	for _, hazard := range proof.inactive {
		if strings.Contains(hazard.reason, "factory result") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unknown factory hazards = %+v", proof.inactive)
	}
	_, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("useOptional"))
	if err == nil || !strings.Contains(err.Error(), "omitted an active writer/escape") {
		t.Fatalf("active unknown factory return error = %v", err)
	}
}

func TestCoroGlobalFunctionSlotRecursiveFactoryFailsClosed(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
var optional func()
func target() {}
func makeCallback(depth int) func() {
	if depth == 0 { return target }
	return makeCallback(depth - 1)
}
func useOptional() { optional = makeCallback(1); optional() }
func install() {}
`)
	call, certificate := onlyCoroGlobalFunctionSlotCertificate(t, fixture, "useOptional")
	if len(certificate.Targets) != 1 || certificate.Targets[0] != fixture.pkg.Func("target") {
		t.Fatalf("recursive factory certificate = %+v", certificate)
	}
	proof := fixture.ctx.coroGlobalFunctionSlots[call]
	found := false
	for _, hazard := range proof.inactive {
		if strings.Contains(hazard.reason, "cyclic return provenance") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("recursive factory hazards = %+v, want cyclic return provenance", proof.inactive)
	}
	_, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("useOptional"))
	if err == nil || !strings.Contains(err.Error(), "omitted an active writer/escape") {
		t.Fatalf("active recursive factory error = %v", err)
	}
}

func TestCoroGlobalFunctionSlotLinknamedCellRemainsOpen(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		root string
	}{
		{
			name: "apparently nil only",
			body: `
//go:linkname optional C.extern_optional_nil
var optional func()
func callOptional() { optional() }
func install() {}
`,
			root: "callOptional",
		},
		{
			name: "local exact store cannot close external cell",
			body: `
//go:linkname optional C.extern_optional_exact
var optional func()
func target() {}
func useOptional() { optional = target; optional() }
func install() {}
`,
			root: "useOptional",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := buildRequiredCoroRuntimeFixture(t, test.body)
			root := fixture.pkg.Func(test.root)
			call := onlyDynamicBuildTestCall(t, root)
			if _, certified := fixture.closedDynamic[call]; certified {
				t.Fatal("linknamed global function cell acquired a closed dynamic certificate")
			}
			if _, proved := fixture.ctx.coroGlobalFunctionSlots[call]; proved {
				t.Fatal("linknamed global function cell acquired a conditional flow proof")
			}
			plan, err := analyzeCoroGlobalFunctionSlotFixture(fixture, root)
			if err != nil {
				t.Fatal(err)
			}
			function := functionPlanForBuildTest(t, plan, root)
			callPlan, planned := plan.CallPlan(call)
			if function.Effect.IsOpaque() || !function.Effect.Contains(coro.AwaitStructured|coro.OutcomeStructured) ||
				!function.Exec.Contains(coro.MayUnwind) || !planned || !callPlan.Open ||
				callPlan.Rep != coro.Dispatch || callPlan.Unresolved != coro.UnknownManagedDispatch ||
				!callPlan.MayBeNil || len(callPlan.Targets) != 0 {
				t.Fatalf("linknamed global caller = function:%+v call:%+v/%t, want structured open descriptor dispatch", function, callPlan, planned)
			}
		})
	}
}

func TestCoroGlobalFunctionSlotConditionalPublicationWaitsForManagedReader(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
var optional func()
func target() {}
func other() {}
func publish() { optional = target }
func callOptional() { optional() }
func useOptional() { publish(); callOptional() }
func install() {}
`)
	call, _ := onlyCoroGlobalFunctionSlotCertificate(t, fixture, "callOptional")
	proof := fixture.ctx.coroGlobalFunctionSlots[call]
	target := fixture.pkg.Func("target")
	if len(proof.inactive) != 0 {
		t.Fatalf("direct global-slot Store hazards = %+v", proof.inactive)
	}
	if len(proof.stores) != 1 || proof.stores[0].owner != fixture.pkg.Func("publish") ||
		proof.stores[0].store == nil || proof.stores[0].target != target {
		t.Fatalf("direct global-slot Store proof = %+v", proof.stores)
	}

	inactive, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("install"))
	if err != nil {
		t.Fatal(err)
	}
	targetPlan := functionPlanForBuildTest(t, inactive, target)
	if targetPlan.ManagedDemand != coro.NoDemand || targetPlan.RawPlainDemand || targetPlan.Emission != coro.EmitNone ||
		targetPlan.RawPlainEntry || inactive.HasRawPlainVariant(target) {
		t.Fatalf("inactive publisher and reader target = %+v, raw-variant=%t", targetPlan, inactive.HasRawPlainVariant(target))
	}

	readerOnly, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("callOptional"))
	if err != nil {
		t.Fatal(err)
	}
	targetPlan = functionPlanForBuildTest(t, readerOnly, target)
	if targetPlan.ManagedDemand == coro.NoDemand || targetPlan.RawPlainDemand || targetPlan.RawPlainOnly ||
		targetPlan.Emission != coro.EmitPlain || targetPlan.FuncRep != coro.Dispatch ||
		targetPlan.RawPlainEntry || readerOnly.HasRawPlainVariant(target) {
		t.Fatalf("managed-reader-only target = %+v, raw-variant=%t", targetPlan, readerOnly.HasRawPlainVariant(target))
	}

	dormant, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("publish"))
	if err != nil {
		t.Fatal(err)
	}
	targetPlan = functionPlanForBuildTest(t, dormant, target)
	if targetPlan.ManagedDemand != coro.NoDemand || targetPlan.RawPlainDemand || targetPlan.RawPlainOnly ||
		targetPlan.Emission != coro.EmitNone || targetPlan.FuncRep != coro.Dispatch ||
		targetPlan.RawPlainEntry || dormant.HasRawPlainVariant(target) {
		t.Fatalf("dormant conditional descriptor Store target = %+v, raw-variant=%t", targetPlan, dormant.HasRawPlainVariant(target))
	}
	if plannedTarget, ok := dormant.ConditionalManagedStoreTarget(proof.stores[0].store); !ok || plannedTarget != target ||
		!dormant.ElidesConditionalManagedStore(proof.stores[0].store) {
		t.Fatalf("dormant conditional Store target/elision = %v, %t/%t", plannedTarget, ok, dormant.ElidesConditionalManagedStore(proof.stores[0].store))
	}
	if caller := functionPlanForBuildTest(t, dormant, fixture.pkg.Func("callOptional")); caller.Emission != coro.EmitNone {
		t.Fatalf("dormant closed-call owner = %+v, want EmitNone", caller)
	}
	valuePlan, planned := dormant.ValuePlan(target)
	if !planned || len(valuePlan.Funcs) != 1 || valuePlan.Funcs[0].Rep != coro.Dispatch {
		t.Fatalf("direct Store target ValuePlan = %+v/%t, want retained Dispatch boundary", valuePlan, planned)
	}

	active, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("useOptional"))
	if err != nil {
		t.Fatal(err)
	}
	targetPlan = functionPlanForBuildTest(t, active, target)
	if targetPlan.ManagedDemand == coro.NoDemand || targetPlan.RawPlainDemand || targetPlan.RawPlainOnly ||
		targetPlan.Emission != coro.EmitPlain || targetPlan.FuncRep != coro.Dispatch ||
		targetPlan.RawPlainEntry || active.HasRawPlainVariant(target) || active.ElidesConditionalManagedStore(proof.stores[0].store) {
		t.Fatalf("active managed descriptor Store target = %+v, raw-variant=%t", targetPlan, active.HasRawPlainVariant(target))
	}

	_, err = fixture.input.Analyze(coro.Roots{{Function: fixture.pkg.Func("publish"), Demand: coro.AsyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
		FunctionIDs:          fixture.functionIDs,
		ClassifyConditionalManagedStoreReference: func(owner *ssa.Function, store *ssa.Store) (*ssa.Function, bool, error) {
			return fixture.pkg.Func("other"), true, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "builder cannot authorize conditional managed Store references") {
		t.Fatalf("forged direct Store target error = %v", err)
	}
}

func TestCoroGlobalFunctionSlotDormantPublicationDoesNotActivateSuspendClosure(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
var optional func()
var channel chan struct{}
func target() { <-channel }
func publish() { optional = target }
func callOptional() { optional() }
func useOptional() { publish(); callOptional() }
func install() {}
`)
	call, _ := onlyCoroGlobalFunctionSlotCertificate(t, fixture, "callOptional")
	proof := fixture.ctx.coroGlobalFunctionSlots[call]
	if len(proof.stores) != 1 || proof.stores[0].target != fixture.pkg.Func("target") {
		t.Fatalf("suspending conditional Store proof = %+v", proof.stores)
	}
	dormant, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("publish"))
	if err != nil {
		t.Fatal(err)
	}
	target := fixture.pkg.Func("target")
	if got := functionPlanForBuildTest(t, dormant, target); got.Emission != coro.EmitNone || got.ManagedDemand != coro.NoDemand ||
		!dormant.ElidesConditionalManagedStore(proof.stores[0].store) {
		t.Fatalf("dormant suspending target = %+v, elided=%t", got, dormant.ElidesConditionalManagedStore(proof.stores[0].store))
	}
	active, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("useOptional"))
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanForBuildTest(t, active, target); got.Emission != coro.EmitCoroutine || got.ManagedDemand == coro.NoDemand ||
		active.ElidesConditionalManagedStore(proof.stores[0].store) {
		t.Fatalf("active suspending target = %+v, elided=%t", got, active.ElidesConditionalManagedStore(proof.stores[0].store))
	}
}

func TestCoroGlobalFunctionSlotAcceptsContextFreeSourceLiteral(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
var optional = func() {}
func callOptional() { optional() }
func install() {}
`)
	call, certificate := onlyCoroGlobalFunctionSlotCertificate(t, fixture, "callOptional")
	if len(certificate.Targets) != 1 || certificate.Targets[0] == nil ||
		certificate.Targets[0].Parent() == nil || len(certificate.Targets[0].FreeVars) != 0 {
		t.Fatalf("context-free literal certificate = %+v", certificate)
	}
	if proof := fixture.ctx.coroGlobalFunctionSlots[call]; len(proof.inactive) != 0 || len(proof.stores) != 1 ||
		proof.stores[0].target != certificate.Targets[0] {
		t.Fatalf("context-free literal proof = stores:%+v hazards:%+v", proof.stores, proof.inactive)
	}
	plan, err := analyzeCoroGlobalFunctionSlotFixture(fixture, fixture.pkg.Func("callOptional"))
	if err != nil {
		t.Fatal(err)
	}
	targetPlan := functionPlanForBuildTest(t, plan, certificate.Targets[0])
	if targetPlan.ManagedDemand == coro.NoDemand || targetPlan.RawPlainDemand || targetPlan.RawPlainEntry ||
		targetPlan.FuncRep != coro.Dispatch || targetPlan.Emission != coro.EmitPlain || plan.HasRawPlainVariant(certificate.Targets[0]) {
		t.Fatalf("context-free literal target plan = %+v", targetPlan)
	}
}

func TestCoroGlobalFunctionSlotUsesFrozenPhysicalIdentity(t *testing.T) {
	fixture := buildRequiredCoroRuntimeFixture(t, `
var optional func()
func target() {}
func useOptional() { optional = target; optional() }
func install() {}
`)
	fixture.ctx.patches = cl.Patches{llssa.PkgRuntime: {}}
	certificates, proofs, err := proveCoroGlobalFunctionSlotClosedDynamicCalls(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	call := onlyDynamicBuildTestCall(t, fixture.pkg.Func("useOptional"))
	if _, certified := certificates[call]; !certified {
		t.Fatal("mutable build patch map changed the frozen global physical identity")
	}
	proof, proved := proofs[call]
	if !proved || proof.identityID == "" || proof.physicalSymbol == "" || len(proof.members) != 1 {
		t.Fatalf("frozen global physical proof = %+v, present=%t", proof, proved)
	}
}

func analyzeCoroGlobalFunctionSlotFixture(fixture requiredCoroRuntimeFixture, root *ssa.Function) (*coro.SSAPlan, error) {
	config := coro.SSAConfig{MaxPlainInstructions: -1, FunctionIDs: fixture.functionIDs}
	return fixture.input.Analyze(coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, config)
}

func onlyCoroGlobalFunctionSlotCertificate(
	t *testing.T,
	fixture requiredCoroRuntimeFixture,
	ownerName string,
) (ssa.CallInstruction, coro.SSAClosedDynamicCallCertificate) {
	t.Helper()
	var foundCall ssa.CallInstruction
	var foundCertificate coro.SSAClosedDynamicCallCertificate
	for call, proof := range fixture.ctx.coroGlobalFunctionSlots {
		if call.Parent() == fixture.pkg.Func(ownerName) {
			if foundCall != nil {
				t.Fatalf("multiple global function-slot proofs in %q", ownerName)
			}
			foundCall = call
			foundCertificate = fixture.closedDynamic[call]
			if !sameCoroClosedDynamicCallCertificate(foundCertificate, proof.certificate) {
				t.Fatalf("global proof and closed certificate differ: proof=%+v certificate=%+v", proof.certificate, foundCertificate)
			}
		}
	}
	if foundCall == nil {
		t.Fatalf("missing global function-slot proof in %q; all closed calls=%d global proofs=%d",
			ownerName, len(fixture.closedDynamic), len(fixture.ctx.coroGlobalFunctionSlots))
	}
	return foundCall, foundCertificate
}

func onlyDynamicBuildTestCall(t *testing.T, fn *ssa.Function) ssa.CallInstruction {
	t.Helper()
	var result ssa.CallInstruction
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok || call.Common() == nil || call.Common().StaticCallee() != nil {
				continue
			}
			if result != nil {
				t.Fatalf("%s has multiple dynamic calls", fn)
			}
			result = call
		}
	}
	if result == nil {
		t.Fatalf("%s has no dynamic call", fn)
	}
	return result
}
