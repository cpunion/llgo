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
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroUniqueAsyncWriterSource = `package foo

var gate chan struct{}

type Writer interface { Write([]byte) (int, error) }
type AsyncWriter struct{}

func (*AsyncWriter) Write(buffer []byte) (int, error) {
	<-gate
	return len(buffer), nil
}

func Root(writer Writer) (int, error) {
	return writer.Write([]byte("payload"))
}
`

func TestResolveCoroInterfaceDispatchPlanUniqueAsyncWriter(t *testing.T) {
	fixture := buildCoroInterfaceDispatchFixture(t, coroUniqueAsyncWriterSource, coro.DynamicCHAClosed)
	defer fixture.program.Dispose()

	resolved, err := resolveCoroInterfaceDispatchPlan(fixture.plan, nil, fixture.invoke)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.call != fixture.invoke || resolved.receiver != fixture.invoke.Common().Value || resolved.method.Id() != "Write" {
		t.Fatalf("resolved call facts do not preserve the exact invoke: %+v", resolved)
	}
	if !resolved.mayBeNil {
		t.Fatal("interface invoke lost its required nil-interface panic check")
	}
	if resolved.sourceCallSignature == nil || resolved.sourceCallSignature.Recv() != nil || resolved.sourceCallSignature.Variadic() ||
		resolved.sourceCallSignature.Params().Len() != 1 || resolved.sourceCallSignature.Results().Len() != 2 {
		t.Fatalf("source call signature = %v", resolved.sourceCallSignature)
	}
	if len(resolved.candidates) != 1 {
		t.Fatalf("candidates = %d, want one: %+v", len(resolved.candidates), resolved.candidates)
	}
	candidate := resolved.candidates[0]
	if candidate.function == nil || candidate.function.Name() != "Write" || candidate.plan.ID != candidate.id ||
		candidate.plan.External != coro.Defined || candidate.plan.Emission != coro.EmitCoroutine ||
		candidate.plan.Primary != coro.PrimaryCoroutine || candidate.plan.Demand != coro.AsyncDemand ||
		candidate.plan.FuncRep != coro.Dispatch || !candidate.plan.Effect.MaySuspend() {
		t.Fatalf("async Writer.Write candidate = %+v", candidate)
	}

	again, err := resolveCoroInterfaceDispatchPlan(fixture.plan, nil, fixture.invoke)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.candidates) != 1 || again.candidates[0].id != candidate.id || again.candidates[0].function != candidate.function ||
		!types.Identical(again.sourceCallSignature, resolved.sourceCallSignature) {
		t.Fatalf("repeated resolution is not stable: first=%+v again=%+v", resolved, again)
	}
}

func TestResolveCoroInterfaceDispatchPlanAcceptsPromotedGenericMethodWrappers(t *testing.T) {
	const source = `package foo
type Interface interface { M() }
func Call[P Interface](p P) { p.M() }
type inner[T any] struct{}
func (*inner[T]) M() {}
type Outer struct { *inner[int] }
func KeepPromotedWrapperLive() {
	var p Interface = &Outer{inner: &inner[int]{}}
	p.M()
}
func Root() { Call[Interface](nil) }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	program := newLLSSAProg(t)
	defer program.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		program, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}}, EmissionUniverseOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: ssaPkg.Func("Root"), Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		DynamicResolution:    coro.DynamicCHAClosed,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var invoke *ssa.Call
	for _, function := range universe.Functions() {
		if function == nil || function.Origin() == nil || function.Origin().Name() != "Call" {
			continue
		}
		invoke = coroInterfaceDispatchFindInvoke(t, function)
		break
	}
	if invoke == nil {
		t.Fatal("materialized generic Call instance is absent")
	}
	resolved, err := resolveCoroInterfaceDispatchPlan(plan, universe, invoke)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.mayBeNil || len(resolved.candidates) != 3 {
		t.Fatalf("generic nil invoke plan: may-be-nil=%t candidates=%d, want true and three", resolved.mayBeNil, len(resolved.candidates))
	}
	wrappers, instances := 0, 0
	for _, candidate := range resolved.candidates {
		switch {
		case strings.HasPrefix(candidate.function.Synthetic, "wrapper for "):
			wrappers++
			if !coroMaterializedGenericMethodWrapper(candidate.function) {
				t.Fatalf("promoted generic wrapper was not recognized:\n%s", candidate.function)
			}
		case coroMaterializedGenericInstance(candidate.function):
			instances++
		}
	}
	if wrappers != 2 || instances != 1 {
		t.Fatalf("generic dispatch candidates: wrappers=%d instances=%d, want 2 and 1", wrappers, instances)
	}
}

func TestCoroManagedOpenAnonymousInterfaceUsesUniversalMethodDescriptor(t *testing.T) {
	const source = `package foo
var gate chan struct{}
type plainMatcher struct{}
type asyncMatcher struct{}
type promotedBase struct{}
type deadPromotedMatcher struct{ promotedBase }
func (plainMatcher) As(any) bool { return true }
func (*asyncMatcher) As(any) bool { <-gate; return true }
func (promotedBase) As(any) bool { return true }
func keep(flag bool) interface{ As(any) bool } {
	if flag { return plainMatcher{} }
	return &asyncMatcher{}
}
func Root(value interface{ As(any) bool }, target any, flag bool) bool {
	if flag {
		_, _ = target.(*plainMatcher)
		_, _ = target.(*asyncMatcher)
	}
	return value.As(target)
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	program := newLLSSAProg(t)
	defer program.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		program, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}},
		EmissionUniverseOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	invoke := coroInterfaceDispatchFindInvoke(t, root)
	methodTargets := make(map[*ssa.Function]struct{})
	for _, function := range universe.Functions() {
		if function != nil && function.Name() == "As" && function.Signature != nil && function.Signature.Recv() != nil {
			methodTargets[function] = struct{}{}
		}
	}
	if len(methodTargets) < 2 {
		t.Fatalf("managed interface fixture has %d As method entries, want at least two", len(methodTargets))
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		DynamicResolution:    coro.DynamicCHAOpen,
		MaxPlainInstructions: -1,
		ClassifyUnknownCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.UnknownTarget, error) {
			if call == invoke {
				return coro.UnknownManagedInterfaceDispatch, nil
			}
			return coro.UnknownManaged, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	callPlan, ok := plan.CallPlan(invoke)
	if !ok || callPlan.Rep != coro.Dispatch || !callPlan.Open ||
		callPlan.Unresolved != coro.UnknownManagedInterfaceDispatch || len(callPlan.Targets) == 0 {
		t.Fatalf("anonymous As invoke CallPlan = %+v, present=%t", callPlan, ok)
	}
	var deadPromoted *ssa.Function
	for target := range methodTargets {
		if strings.Contains(target.Synthetic, "wrapper") && strings.Contains(target.String(), "deadPromotedMatcher") {
			deadPromoted = target
			break
		}
	}
	if deadPromoted == nil {
		t.Fatal("managed interface fixture has no dead promoted method wrapper")
	}
	deadPlan, ok := plan.FunctionPlan(deadPromoted)
	if !ok || !coroInterfaceTargetContains(callPlan.Targets, deadPlan.ID) {
		t.Fatalf("dead promoted target plan = %+v, present=%t; open targets=%v", deadPlan, ok, callPlan.Targets)
	}
	materializedByTypeData := false
	for _, owner := range plan.Functions() {
		if owner.Function == nil || owner.Plan.Emission == coro.EmitNone {
			continue
		}
		references, err := universe.CoroDemandReferences(owner.Function)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range references {
			materializedByTypeData = materializedByTypeData || target == deadPromoted
		}
	}
	if materializedByTypeData {
		t.Fatal("dead promoted wrapper unexpectedly has an ABI type-data owner")
	}
	managedMethods, err := analyzeCoroManagedInterfaceDispatchPlan(plan, universe, true)
	if err != nil {
		t.Fatal(err)
	}
	if !managedMethods.acceptsTarget(deadPromoted, deadPlan) {
		t.Fatalf("managed method plan did not freeze exact dead promoted target %q", deadPlan.ID)
	}
	closedMethods, err := analyzeCoroClosedInterfacePlainPlan(plan, universe, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if closedMethods.acceptsTarget(deadPromoted, deadPlan) {
		t.Fatal("dead promoted target acquired an unrelated closed/raw method-token capability")
	}
	if err := validateCoroDynamicDispatchTarget(deadPromoted, deadPlan); err == nil ||
		!strings.Contains(err.Error(), "methods require receiver-aware dispatch lowering") {
		t.Fatalf("receiver-free function-value validator accepted managed method target: %v", err)
	}
	rootPlan, ok := plan.FunctionPlan(root)
	if !ok || rootPlan.Emission != coro.EmitCoroutine || rootPlan.Effect.IsOpaque() ||
		!rootPlan.Effect.Contains(coro.AwaitStructured) {
		t.Fatalf("Root plan = %+v, present=%t", rootPlan, ok)
	}

	compilation := coroClosedInterfacePlainCompilation(plan, universe)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	pkg, _, err := NewPackageExWithEmbedOptions(
		program, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("compile managed anonymous interface invoke: %v", err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify managed anonymous interface invoke: %v\n%s", err, module.String())
	}
	ir := module.String()
	if !strings.Contains(ir, coroPlainDispatchDescriptorPrefix+"method.") ||
		!strings.Contains(ir, coroPlainDispatchThunkPrefix+"method.") ||
		!strings.Contains(ir, coroCoroDispatchThunkPrefix+"method.") {
		t.Fatalf("plain/coroutine method capabilities were not materialized:\n%s", ir)
	}
	rootIR := requireCoroPhysicalFunction(t, module, "foo.Root").String()
	if !strings.Contains(rootIR, "coro.dispatch.version.invalid") ||
		!strings.Contains(rootIR, "coro.dispatch.flags.unknown") ||
		!strings.Contains(rootIR, "call void @"+coroAwaitPrepareHookV1) ||
		!strings.Contains(rootIR, "call i1 @"+coroAwaitInlineBeginHookV2) {
		t.Fatalf("open interface invoke did not enter validated descriptor child-await lowering:\n%s", rootIR)
	}
	if strings.Contains(rootIR, "call i1 %") && !strings.Contains(rootIR, "coro.dispatch") {
		t.Fatalf("open interface invoke fell back to an unvalidated raw itab call:\n%s", rootIR)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify managed anonymous interface invoke before split: %v\n%s", err, ir)
	}
}

func TestCoroRawPlainTypeDataPublishesManagedMethodPrimary(t *testing.T) {
	const source = `package foo
var gate chan struct{}
type Writer interface { Write() int }
type writer struct{}
func (writer) Write() int { <-gate; return 1 }
func ManagedRoot(value Writer) int { return value.Write() }
func RawRoot() any { return writer{} }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	program := newLLSSAProg(t)
	defer program.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		program, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}},
		EmissionUniverseOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	managedRoot, rawRoot := ssaPkg.Func("ManagedRoot"), ssaPkg.Func("RawRoot")
	invoke := coroInterfaceDispatchFindInvoke(t, managedRoot)
	var method *ssa.Function
	for _, function := range universe.Functions() {
		if function != nil && function.Name() == "Write" && function.Signature != nil && function.Signature.Recv() != nil {
			method = function
			break
		}
	}
	if method == nil {
		t.Fatal("writer.Write is absent from the emission universe")
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: managedRoot, Demand: coro.AsyncDemand},
		{Function: rawRoot, RawPlainDemand: true},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		DynamicResolution:    coro.DynamicCHAClosed,
		MaxPlainInstructions: -1,
		OutcomeMode:          coro.OutcomeExplicitStatus,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == rawRoot {
				return coro.SSAFunctionPolicy{RawPlainEntry: true}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	callPlan, ok := plan.CallPlan(invoke)
	if !ok || callPlan.Rep != coro.Dispatch || len(callPlan.Targets) == 0 {
		t.Fatalf("managed invoke plan = %+v, present=%t", callPlan, ok)
	}
	methodPlan, ok := plan.FunctionPlan(method)
	if !ok || methodPlan.Emission != coro.EmitCoroutine || plan.HasRawPlainVariant(method) {
		t.Fatalf("writer.Write plan = %+v, present=%t raw-variant=%t", methodPlan, ok, plan.HasRawPlainVariant(method))
	}

	compilation := coroClosedInterfacePlainCompilation(plan, universe)
	pkg, _, err := NewPackageExWithEmbedOptions(
		program, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("compile raw type-data producer: %v", err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify raw type-data producer: %v\n%s", err, module.String())
	}
	ir := module.String()
	if !strings.Contains(ir, coroPlainDispatchDescriptorPrefix+"method.") ||
		!strings.Contains(ir, "foo.writer.Write$coro") {
		t.Fatalf("raw type data did not publish the managed method primary:\n%s", ir)
	}
	if !strings.Contains(ir, "define linkonce ptr @"+coroCoroDispatchThunkPrefix+"method.") ||
		!strings.Contains(ir, "define linkonce i64 @"+coroManagedInterfaceRawTrapPrefix) {
		t.Fatalf("cross-package method capability helpers are not coalescible:\n%s", ir)
	}
}

func TestValidateCoroManagedInterfaceDescriptorTargetSelectsCoroutinePrimaryWithRawAlternate(t *testing.T) {
	fixture := buildCoroInterfaceDispatchFixture(t, coroUniqueAsyncWriterSource, coro.DynamicCHAClosed)
	defer fixture.program.Dispose()
	resolved, err := resolveCoroInterfaceDispatchPlan(fixture.plan, nil, fixture.invoke)
	if err != nil {
		t.Fatal(err)
	}
	candidate := resolved.candidates[0]
	candidate.plan.Demand = coro.BothDemand
	candidate.plan.RawPlainEntry = true
	for _, rep := range []coro.FuncRep{coro.Dispatch, coro.DirectCoro} {
		plan := candidate.plan
		plan.FuncRep = rep
		if err := validateCoroManagedInterfaceDescriptorTarget(
			candidate.function, plan, nil, resolved.sourceCallSignature,
		); err == nil || !strings.Contains(err.Error(), "prepared emission universe") {
			// The nil universe must remain fail-closed after accepting the managed
			// coroutine primary shape. A receiver-aware ABI method descriptor may
			// wrap DirectCoro without creating a receiver-free function descriptor
			// or a second body.
			t.Fatalf("%s BothDemand/raw-alternate descriptor validation stopped at %v", rep, err)
		}
	}
}

func TestValidateCoroInterfaceDispatchCandidateAcceptsManagedPrimaryWithRawAlternate(t *testing.T) {
	fixture := buildCoroInterfaceDispatchFixture(t, coroUniqueAsyncWriterSource, coro.DynamicCHAClosed)
	defer fixture.program.Dispose()

	resolved, err := resolveCoroInterfaceDispatchPlan(fixture.plan, nil, fixture.invoke)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.candidates) != 1 {
		t.Fatalf("candidates = %d, want one", len(resolved.candidates))
	}
	candidate := resolved.candidates[0]
	candidate.plan.Demand = coro.BothDemand
	candidate.plan.RawPlainEntry = true
	receiver, targetReceiver, methodEntry, err := validateCoroInterfaceDispatchCandidate(
		fixture.invoke.Common(), resolved.iface, resolved.sourceCallSignature, nil,
		fixture.invoke.Parent(), candidate.id, candidate.function, candidate.plan,
	)
	if err != nil {
		t.Fatalf("BothDemand managed interface candidate rejected: %v", err)
	}
	if !types.Identical(receiver, candidate.receiver) || !types.Identical(targetReceiver, candidate.targetReceiver) || methodEntry != candidate.methodEntry {
		t.Fatalf("validated candidate changed: receiver=%s target=%s entry=%v", receiver, targetReceiver, methodEntry)
	}
}

func TestResolveCoroInterfaceDispatchPlanMixedPlainAndCoroutine(t *testing.T) {
	const source = `package foo
var gate chan struct{}
type Writer interface { Write([]byte) (int, error) }
type AsyncWriter struct{}
type PlainWriter struct{}
func (*AsyncWriter) Write(buffer []byte) (int, error) { <-gate; return len(buffer), nil }
func (*PlainWriter) Write(buffer []byte) (int, error) { return len(buffer), nil }
func KeepBoth(flag bool) Writer {
	if flag { return &AsyncWriter{} }
	return &PlainWriter{}
}
func Root(writer Writer) (int, error) { return writer.Write([]byte("payload")) }
`
	fixture := buildCoroInterfaceDispatchFixture(t, source, coro.DynamicCHAClosed)
	defer fixture.program.Dispose()

	resolved, err := resolveCoroInterfaceDispatchPlan(fixture.plan, nil, fixture.invoke)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.candidates) != 2 {
		t.Fatalf("candidates = %d, want mixed pair: %+v", len(resolved.candidates), resolved.candidates)
	}
	plain, asynchronous := 0, 0
	for index, candidate := range resolved.candidates {
		if index != 0 && resolved.candidates[index-1].id >= candidate.id {
			t.Fatalf("candidates are not in strict FunctionID order: %+v", resolved.candidates)
		}
		switch candidate.plan.Emission {
		case coro.EmitPlain:
			plain++
			if candidate.plan.Primary != coro.PrimaryPlain || candidate.plan.Effect != coro.NoSuspend {
				t.Fatalf("plain candidate = %+v", candidate)
			}
		case coro.EmitCoroutine:
			asynchronous++
			if candidate.plan.Primary != coro.PrimaryCoroutine || candidate.plan.Demand != coro.AsyncDemand || !candidate.plan.Effect.MaySuspend() {
				t.Fatalf("coroutine candidate = %+v", candidate)
			}
		default:
			t.Fatalf("unexpected candidate emission: %+v", candidate)
		}
	}
	if plain != 1 || asynchronous != 1 {
		t.Fatalf("candidate classes: plain=%d coroutine=%d", plain, asynchronous)
	}
}

func TestResolveCoroInterfaceDispatchPlanPointerPromotedMethodEntry(t *testing.T) {
	const source = `package foo
var gate chan struct{}
type Writer interface {
	Write([]byte) (int, error)
	Close() error
}
type PointerOnlyWriter struct{}
func (PointerOnlyWriter) Write(buffer []byte) (int, error) { <-gate; return len(buffer), nil }
func (*PointerOnlyWriter) Close() error { return nil }
func Keep() Writer { return &PointerOnlyWriter{} }
func Root(writer Writer) (int, error) { return writer.Write([]byte("payload")) }
`
	fixture := buildCoroPointerPromotedInterfaceDispatchFixture(t, source)
	defer fixture.program.Dispose()

	resolved, err := resolveCoroInterfaceDispatchPlan(fixture.plan, nil, fixture.invoke)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.candidates) != 1 {
		t.Fatalf("candidates = %d, want one pointer-promoted method: %+v", len(resolved.candidates), resolved.candidates)
	}
	candidate := resolved.candidates[0]
	dynamicPointer, dynamicIsPointer := types.Unalias(candidate.receiver).Underlying().(*types.Pointer)
	if !dynamicIsPointer || !types.Identical(dynamicPointer.Elem(), candidate.targetReceiver) {
		t.Fatalf("dynamic receiver %s does not promote declared receiver %s", candidate.receiver, candidate.targetReceiver)
	}
	if candidate.methodEntry == nil || candidate.methodEntry == candidate.function || candidate.methodEntry.Signature == nil ||
		candidate.methodEntry.Signature.Recv() == nil || !types.Identical(candidate.methodEntry.Signature.Recv().Type(), candidate.receiver) {
		t.Fatalf("pointer-promoted method entry = %v; target=%v dynamic receiver=%s", candidate.methodEntry, candidate.function, candidate.receiver)
	}
	if !strings.Contains(candidate.methodEntry.Synthetic, "wrapper") {
		t.Fatalf("method entry %s is not the exact pointer method-set wrapper: synthetic=%q", candidate.methodEntry, candidate.methodEntry.Synthetic)
	}
}

func TestResolveCoroInterfaceDispatchPlanFailsClosed(t *testing.T) {
	closed := buildCoroInterfaceDispatchFixture(t, coroUniqueAsyncWriterSource, coro.DynamicCHAClosed)
	defer closed.program.Dispose()
	open := buildCoroInterfaceDispatchFixture(t, coroUniqueAsyncWriterSource, coro.DynamicCHAOpen)
	defer open.program.Dispose()
	other := buildCoroInterfaceDispatchFixture(t, coroUniqueAsyncWriterSource, coro.DynamicCHAClosed)
	defer other.program.Dispose()

	tests := []struct {
		name string
		plan *coro.SSAPlan
		call *ssa.Call
		want string
	}{
		{name: "nil plan", call: closed.invoke, want: "exact call and compilation plan"},
		{name: "nil call", plan: closed.plan, want: "exact call and compilation plan"},
		{name: "open", plan: open.plan, call: open.invoke, want: "closed nonempty Dispatch CallPlan"},
		{name: "missing exact call plan", plan: closed.plan, call: other.invoke, want: "no exact compilation CallPlan"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolveCoroInterfaceDispatchPlan(test.plan, nil, test.call)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}

	resolved, err := resolveCoroInterfaceDispatchPlan(closed.plan, nil, closed.invoke)
	if err != nil {
		t.Fatal(err)
	}
	target := resolved.candidates[0].function
	original := target.Signature
	recv := original.Recv()
	badParam := types.NewVar(0, target.Pkg.Pkg, "buffer", types.Typ[types.Int])
	target.Signature = types.NewSignatureType(recv, nil, nil, types.NewTuple(badParam), original.Results(), false)
	_, err = resolveCoroInterfaceDispatchPlan(closed.plan, nil, closed.invoke)
	target.Signature = original
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("signature conflict error = %v", err)
	}

	originalFreeVars := target.FreeVars
	target.FreeVars = []*ssa.FreeVar{nil}
	_, err = resolveCoroInterfaceDispatchPlan(closed.plan, nil, closed.invoke)
	target.FreeVars = originalFreeVars
	if err == nil || !strings.Contains(err.Error(), "captured or nested methods") {
		t.Fatalf("free-variable error = %v", err)
	}

	constraint := types.NewInterfaceType(nil, nil)
	constraint.Complete()
	typeParam := types.NewTypeParam(types.NewTypeName(0, target.Pkg.Pkg, "T", nil), constraint)
	named := types.NewNamed(types.NewTypeName(0, target.Pkg.Pkg, "GenericReceiver", nil), types.NewStruct(nil, nil), nil)
	named.SetTypeParams([]*types.TypeParam{typeParam})
	receiverTypeParam := types.NewTypeParam(types.NewTypeName(0, target.Pkg.Pkg, "T", nil), constraint)
	instantiated, instantiateErr := types.Instantiate(nil, named, []types.Type{receiverTypeParam}, false)
	if instantiateErr != nil {
		t.Fatal(instantiateErr)
	}
	genericRecv := types.NewVar(0, target.Pkg.Pkg, "writer", types.NewPointer(instantiated))
	target.Signature = types.NewSignatureType(genericRecv, []*types.TypeParam{receiverTypeParam}, nil, original.Params(), original.Results(), false)
	_, err = resolveCoroInterfaceDispatchPlan(closed.plan, nil, closed.invoke)
	target.Signature = original
	if err == nil || !strings.Contains(err.Error(), "generic") {
		t.Fatalf("generic receiver error = %v", err)
	}

	badRecv := types.NewVar(0, target.Pkg.Pkg, "writer", types.Typ[types.Int])
	target.Signature = types.NewSignatureType(badRecv, nil, nil, original.Params(), original.Results(), false)
	_, err = resolveCoroInterfaceDispatchPlan(closed.plan, nil, closed.invoke)
	target.Signature = original
	if err == nil || !strings.Contains(err.Error(), "implement invoke interface") {
		t.Fatalf("receiver conflict error = %v", err)
	}
}

func TestResolveCoroInterfaceDispatchPlanRejectsVariadicAndABIDirective(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "variadic",
			source: `package foo
type Writer interface { Write(...byte) int }
type Concrete struct{}
func (Concrete) Write(buffer ...byte) int { return len(buffer) }
func Root(writer Writer) int { return writer.Write(1, 2) }
`,
			want: "variadic method",
		},
		{
			name: "ABI directive",
			source: `package foo
import _ "unsafe"
type Writer interface { Write([]byte) int }
type Concrete struct{}
//go:linkname redirectedWrite example.com/redirectedWrite
func (Concrete) Write(buffer []byte) int { return len(buffer) }
func Root(writer Writer) int { return writer.Write(nil) }
`,
			want: "ABI directive",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := buildCoroInterfaceDispatchFixture(t, test.source, coro.DynamicCHAClosed)
			defer fixture.program.Dispose()
			_, err := resolveCoroInterfaceDispatchPlan(fixture.plan, nil, fixture.invoke)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

type coroInterfaceDispatchFixture struct {
	program llssa.Program
	plan    *coro.SSAPlan
	invoke  *ssa.Call
}

func buildCoroInterfaceDispatchFixture(t *testing.T, source string, resolution coro.DynamicResolution) coroInterfaceDispatchFixture {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	program := newLLSSAProg(t)
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		program, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}}, EmissionUniverseOptions{},
	)
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	invoke := coroInterfaceDispatchFindInvoke(t, root)
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		DynamicResolution:    resolution,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	return coroInterfaceDispatchFixture{program: program, plan: plan, invoke: invoke}
}

func buildCoroPointerPromotedInterfaceDispatchFixture(t *testing.T, source string) coroInterfaceDispatchFixture {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	program := newLLSSAProg(t)
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		program, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}}, EmissionUniverseOptions{},
	)
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	invoke := coroInterfaceDispatchFindInvoke(t, root)
	var declared, wrapper *ssa.Function
	for _, function := range universe.Functions() {
		if function == nil || function.Name() != "Write" || function.Signature == nil || function.Signature.Recv() == nil {
			continue
		}
		_, pointer := types.Unalias(function.Signature.Recv().Type()).Underlying().(*types.Pointer)
		switch {
		case !pointer && function.Synthetic == "":
			declared = function
		case pointer && strings.Contains(function.Synthetic, "wrapper"):
			wrapper = function
		}
	}
	if declared == nil || wrapper == nil {
		program.Dispose()
		t.Fatalf("pointer promotion fixture methods: declared=%v wrapper=%v", declared, wrapper)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		FunctionIDs:          functionIDs,
		DynamicResolution:    coro.DynamicCHAClosed,
		MaxPlainInstructions: -1,
		ResolveFunction: func(function *ssa.Function) (*ssa.Function, bool, error) {
			if function == wrapper {
				return declared, true, nil
			}
			return function, true, nil
		},
	})
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	return coroInterfaceDispatchFixture{program: program, plan: plan, invoke: invoke}
}

func coroInterfaceDispatchFindInvoke(t *testing.T, function *ssa.Function) *ssa.Call {
	t.Helper()
	if function == nil {
		t.Fatal("missing Root function")
	}
	var result *ssa.Call
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || !call.Common().IsInvoke() {
				continue
			}
			if result != nil {
				t.Fatal("Root has more than one interface invoke")
			}
			result = call
		}
	}
	if result == nil {
		t.Fatal("Root has no interface invoke")
	}
	return result
}
