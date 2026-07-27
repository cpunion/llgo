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
	"go/ast"
	"strings"
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

type callableContractBuildFixture struct {
	program     llssa.Program
	pkg         *ssa.Package
	input       CoroPlanInput
	functionIDs coro.FunctionIDConfig
}

func newCallableContractBuildFixture(t *testing.T) *callableContractBuildFixture {
	t.Helper()
	ssaPkg, files := buildCoroPlanTestPackage(t, "example.com/callableplan", `package callableplan
//llgo:coro contract foreign.v1 progress=executor-safe affinity=any-thread reentry=none memory=borrow-until-return
//go:linkname Safe C.callable_safe
func Safe()

//llgo:coro contract foreign.v1 progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete inline-progress=executor-safe inline-affinity=any-thread inline-reentry=none inline-memory=borrow-until-return
//go:linkname Blocking C.callable_blocking
func Blocking()

//llgo:coro contract foreign.v1 progress=unknown affinity=unknown reentry=unknown memory=unknown inline-progress=executor-safe inline-affinity=any-thread inline-reentry=none inline-memory=borrow-until-return
//go:linkname Unknown C.callable_unknown
func Unknown()

//llgo:coro contract foreign.v1 progress=no-return affinity=any-thread reentry=none memory=by-value
//go:linkname Never C.callable_never
func Never()

//llgo:coro contract foreign.v1 scope=wrapper progress=may-block affinity=caller-thread reentry=none memory=borrow-until-return
func Wrapper() { Blocking() }

//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
func FastWrapper() { Blocking() }

//llgo:coro contract foreign.v1 scope=wrapper progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
func UnknownFastWrapper() { Unknown() }

func SafeCaller() { Safe() }
func NeverCaller() { Never() }
`, nil)
	program := llssa.NewProgram(nil)
	emission, err := cl.PrepareEmissionUniverse(program, nil, []cl.EmissionPackage{{
		SSA: ssaPkg, Files: []*ast.File{files[0]}, Identity: "example.com/callableplan",
	}})
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	ssaEmission, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, emission.Functions())
	if err != nil {
		program.Dispose()
		t.Fatal(err)
	}
	functionIDs := emission.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	return &callableContractBuildFixture{
		program: program,
		pkg:     ssaPkg,
		input: CoroPlanInput{
			Program:            ssaPkg.Prog,
			EmissionUniverse:   ssaEmission,
			resolveFunction:    emission.Resolve,
			functionBackground: emission.FunctionBackground,
			callableIdentity:   emission.CoroCallableIdentityCertificate,
			callableContract:   emission.CoroCallableContractCertificate,
			trustedInlineCall:  emission.CoroTrustedInlineCallCertificate,
		},
		functionIDs: functionIDs,
	}
}

func (fixture *callableContractBuildFixture) close() { fixture.program.Dispose() }

func (fixture *callableContractBuildFixture) analyze(input CoroPlanInput, classify func(*ssa.Function) (coro.SSAFunctionPolicy, error)) (*coro.SSAPlan, error) {
	return input.Analyze(coro.Roots{
		{Function: fixture.pkg.Func("SafeCaller"), Demand: coro.SyncDemand},
		{Function: fixture.pkg.Func("Wrapper"), Demand: coro.SyncDemand},
		{Function: fixture.pkg.Func("FastWrapper"), Demand: coro.SyncDemand},
		{Function: fixture.pkg.Func("UnknownFastWrapper"), Demand: coro.SyncDemand},
		{Function: fixture.pkg.Func("NeverCaller"), Demand: coro.SyncDemand},
	}, coro.SSAConfig{MaxPlainInstructions: -1, FunctionIDs: fixture.functionIDs, ClassifyFunction: classify})
}

func TestCoroPlanInputInjectsFrozenCallableContracts(t *testing.T) {
	fixture := newCallableContractBuildFixture(t)
	defer fixture.close()
	plan, err := fixture.analyze(fixture.input, nil)
	if err != nil {
		t.Fatal(err)
	}
	safe, blocking, unknown, never, wrapper, fastWrapper, unknownFastWrapper := fixture.pkg.Func("Safe"), fixture.pkg.Func("Blocking"), fixture.pkg.Func("Unknown"), fixture.pkg.Func("Never"), fixture.pkg.Func("Wrapper"), fixture.pkg.Func("FastWrapper"), fixture.pkg.Func("UnknownFastWrapper")
	safePlan, _ := plan.FunctionPlan(safe)
	blockingPlan, _ := plan.FunctionPlan(blocking)
	unknownPlan, _ := plan.FunctionPlan(unknown)
	neverPlan, _ := plan.FunctionPlan(never)
	wrapperPlan, _ := plan.FunctionPlan(wrapper)
	fastWrapperPlan, _ := plan.FunctionPlan(fastWrapper)
	unknownFastWrapperPlan, _ := plan.FunctionPlan(unknownFastWrapper)
	if safePlan.External != coro.ExternalKnown || safePlan.Exec != coro.IRQUnsafe || safePlan.Effect != coro.NoSuspend {
		t.Fatalf("executor-safe declaration plan = %+v", safePlan)
	}
	if blockingPlan.External != coro.ExternalUnknownForeign || blockingPlan.Exec != coro.BlockForeign|coro.IRQUnsafe ||
		!wrapperPlan.Effect.Contains(coro.WaitForeign) || plan.IgnoresBody(wrapper) {
		t.Fatalf("blocking/wrapper plans = blocking:%+v wrapper:%+v ignored=%t", blockingPlan, wrapperPlan, plan.IgnoresBody(wrapper))
	}
	if fastWrapperPlan.Effect != coro.NoSuspend || fastWrapperPlan.Exec.Contains(coro.BlockForeign|coro.NeedsPreempt|coro.OpaqueExec) || plan.IgnoresBody(fastWrapper) {
		t.Fatalf("trusted-inline wrapper plan = %+v, ignored=%t", fastWrapperPlan, plan.IgnoresBody(fastWrapper))
	}
	if unknownPlan.External != coro.ExternalUnknownForeign ||
		unknownPlan.Exec != coro.BlockForeign|coro.IRQUnsafe|coro.ThreadAffine|coro.OpaqueExec ||
		unknownFastWrapperPlan.Effect != coro.NoSuspend || unknownFastWrapperPlan.Exec != coro.IRQUnsafe ||
		plan.IgnoresBody(unknownFastWrapper) {
		t.Fatalf("unknown/trusted-inline plans = target:%+v wrapper:%+v ignored=%t", unknownPlan, unknownFastWrapperPlan, plan.IgnoresBody(unknownFastWrapper))
	}
	fastCall := onlyCallableContractStaticCall(t, fastWrapper, "Blocking")
	fastCallPlan, ok := plan.CallPlan(fastCall)
	if !ok || fastCallPlan.Kind != coro.CallTrustedInline || fastCallPlan.Rep != coro.DirectPlain ||
		fastCallPlan.InvocationPolicy != coro.InvocationTrustedInline || fastCallPlan.InvocationContract == "" ||
		fastCallPlan.InvocationABI == "" || fastCallPlan.InvocationCertificate == "" {
		t.Fatalf("FastWrapper call plan = %+v, %t", fastCallPlan, ok)
	}
	unknownFastCall := onlyCallableContractStaticCall(t, unknownFastWrapper, "Unknown")
	unknownFastCallPlan, ok := plan.CallPlan(unknownFastCall)
	if !ok || unknownFastCallPlan.Kind != coro.CallTrustedInline || unknownFastCallPlan.Rep != coro.DirectPlain ||
		unknownFastCallPlan.InvocationPolicy != coro.InvocationTrustedInline || unknownFastCallPlan.InvocationContract == "" ||
		unknownFastCallPlan.InvocationABI == "" || unknownFastCallPlan.InvocationCertificate == "" {
		t.Fatalf("UnknownFastWrapper call plan = %+v, %t", unknownFastCallPlan, ok)
	}
	if neverPlan.External != coro.ExternalUnknownForeign || neverPlan.Exec != coro.BlockForeign|coro.IRQUnsafe|coro.NoReturn {
		t.Fatalf("no-return declaration plan = %+v", neverPlan)
	}
	neverCaller, _ := plan.FunctionPlan(fixture.pkg.Func("NeverCaller"))
	if !neverCaller.Effect.Contains(coro.WaitForeign) {
		t.Fatalf("no-return Auto caller plan = %+v; want conservative WaitForeign", neverCaller)
	}
	for _, function := range []*ssa.Function{safe, blocking, unknown, never, wrapper, fastWrapper, unknownFastWrapper} {
		certificate, ok := plan.CallableContractCertificate(function)
		if !ok || certificate.IsZero() || certificate.Contract.ID == "foreign.v1" {
			t.Fatalf("planned callable contract for %q = %+v, %t", function.Name(), certificate, ok)
		}
	}
	for _, function := range []*ssa.Function{safe, blocking, unknown, never} {
		identity, ok := plan.CallableIdentityCertificate(function)
		frontend, frontendOK, frontendErr := fixture.input.callableIdentity(function)
		if !ok || frontendErr != nil || !frontendOK || identity != frontend {
			t.Fatalf("planned callable identity for %q = %+v, %t; frontend=%+v, %t, %v", function.Name(), identity, ok, frontend, frontendOK, frontendErr)
		}
	}
	for _, function := range []*ssa.Function{wrapper, fastWrapper, unknownFastWrapper} {
		if _, ok := plan.CallableIdentityCertificate(function); ok {
			t.Fatalf("Go wrapper %q acquired a C declaration identity", function.Name())
		}
	}
}

func onlyCallableContractStaticCall(t *testing.T, function *ssa.Function, targetName string) *ssa.Call {
	t.Helper()
	var found *ssa.Call
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil || call.Common().StaticCallee() == nil || call.Common().StaticCallee().Name() != targetName {
				continue
			}
			if found != nil {
				t.Fatalf("%s has multiple calls to %s", function.Name(), targetName)
			}
			found = call
		}
	}
	if found == nil {
		t.Fatalf("%s has no static call to %s", function.Name(), targetName)
	}
	return found
}

func TestCoroPlanInputCallableContractProofsAreFrontendOwned(t *testing.T) {
	fixture := newCallableContractBuildFixture(t)
	defer fixture.close()
	safe := fixture.pkg.Func("Safe")
	frontend, ok, err := fixture.input.callableContract(safe)
	if err != nil || !ok {
		t.Fatalf("frontend Safe contract = %+v, %t, %v", frontend, ok, err)
	}

	_, err = fixture.analyze(fixture.input, func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
		if fn == safe {
			forged := frontend
			forged.CallableABI = "forged.v1"
			return coro.SSAFunctionPolicy{CallableContractCertificate: forged}, nil
		}
		return coro.SSAFunctionPolicy{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with the frozen frontend certificate") {
		t.Fatalf("forged builder callable contract error = %v", err)
	}

	withoutFrontend := fixture.input
	withoutFrontend.callableContract = nil
	_, err = fixture.analyze(withoutFrontend, func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
		if fn == safe {
			return coro.SSAFunctionPolicy{CallableContractCertificate: frontend}, nil
		}
		return coro.SSAFunctionPolicy{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "without an exact frozen frontend contract") {
		t.Fatalf("unfrozen builder callable contract error = %v", err)
	}

	noncertifying := fixture.input
	noncertifying.callableContract = func(fn *ssa.Function) (cl.CoroCallableContractCertificate, bool, error) {
		if fn == safe {
			return frontend, false, nil
		}
		return cl.CoroCallableContractCertificate{}, false, nil
	}
	_, err = fixture.analyze(noncertifying, nil)
	if err == nil || !strings.Contains(err.Error(), "without certifying it") {
		t.Fatalf("non-certifying callback error = %v", err)
	}
}

func TestCoroPlanInputCallableIdentityIsFrontendOwned(t *testing.T) {
	fixture := newCallableContractBuildFixture(t)
	defer fixture.close()
	safe := fixture.pkg.Func("Safe")
	frontend, ok, err := fixture.input.callableIdentity(safe)
	if err != nil || !ok {
		t.Fatalf("frontend Safe identity = %+v, %t, %v", frontend, ok, err)
	}

	_, err = fixture.analyze(fixture.input, func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
		if fn == safe {
			forged := frontend
			forged.CallableABI = "forged.v1"
			return coro.SSAFunctionPolicy{CallableIdentityCertificate: forged}, nil
		}
		return coro.SSAFunctionPolicy{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "callable identity") || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("forged builder callable identity error = %v", err)
	}

	withoutFrontend := fixture.input
	withoutFrontend.callableIdentity = nil
	withoutFrontend.callableContract = nil
	withoutFrontend.trustedInlineCall = nil
	_, err = fixture.analyze(withoutFrontend, func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
		if fn == safe {
			return coro.SSAFunctionPolicy{CallableIdentityCertificate: frontend}, nil
		}
		return coro.SSAFunctionPolicy{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "without an exact frozen frontend identity") {
		t.Fatalf("unfrozen builder callable identity error = %v", err)
	}

	noncertifying := fixture.input
	noncertifying.callableContract = nil
	noncertifying.trustedInlineCall = nil
	noncertifying.callableIdentity = func(fn *ssa.Function) (cl.CoroCallableIdentityCertificate, bool, error) {
		if fn == safe {
			return frontend, false, nil
		}
		return fixture.input.callableIdentity(fn)
	}
	_, err = fixture.analyze(noncertifying, nil)
	if err == nil || !strings.Contains(err.Error(), "identity data") || !strings.Contains(err.Error(), "without certifying") {
		t.Fatalf("non-certifying identity callback error = %v", err)
	}
}

func TestCoroPlanInputTrustedInlineProofsAreFrontendOwned(t *testing.T) {
	fixture := newCallableContractBuildFixture(t)
	defer fixture.close()
	_, err := fixture.input.Analyze(
		coro.Roots{{Function: fixture.pkg.Func("SafeCaller"), Demand: coro.SyncDemand}},
		coro.SSAConfig{
			FunctionIDs: fixture.functionIDs,
			ClassifyTrustedInlineCall: func(*ssa.Function, ssa.CallInstruction) (coro.SSATrustedInlineCallCertificate, bool, error) {
				return coro.SSATrustedInlineCallCertificate{ID: "forged", Contract: "forged", ABI: "forged"}, true, nil
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "cannot manufacture trusted-inline") {
		t.Fatalf("builder trusted-inline proof error = %v", err)
	}
}

func TestCoroPlanInputExecutorSafeWrapperFailsClosedWithoutExactInvocationCertificate(t *testing.T) {
	fixture := newCallableContractBuildFixture(t)
	defer fixture.close()
	input := fixture.input
	trustedInlineCall := input.trustedInlineCall
	input.trustedInlineCall = func(caller *ssa.Function, call ssa.CallInstruction) (coro.SSATrustedInlineCallCertificate, bool, error) {
		if caller == fixture.pkg.Func("FastWrapper") {
			return coro.SSATrustedInlineCallCertificate{}, false, nil
		}
		return trustedInlineCall(caller, call)
	}
	_, err := fixture.analyze(input, nil)
	if err == nil || !strings.Contains(err.Error(), "claims executor-safe") {
		t.Fatalf("missing trusted-inline invocation certificate error = %v", err)
	}
}

func TestCoroPlanInputRejectsGenericAndLegacyCallableCertificates(t *testing.T) {
	fixture := newCallableContractBuildFixture(t)
	defer fixture.close()
	safe := fixture.pkg.Func("Safe")
	for _, test := range []struct {
		name   string
		mutate func(*CoroPlanInput)
	}{
		{"noblock", func(input *CoroPlanInput) {
			input.foreignNoBlock = func(fn *ssa.Function) (cl.CoroForeignNoBlockCertificate, bool, error) {
				return cl.CoroForeignNoBlockCertificate{ID: "legacy"}, fn == safe, nil
			}
		}},
		{"sync", func(input *CoroPlanInput) {
			input.foreignSync = func(fn *ssa.Function) (cl.CoroForeignSyncCertificate, bool, error) {
				return cl.CoroForeignSyncCertificate{ID: "legacy"}, fn == safe, nil
			}
		}},
		{"worker", func(input *CoroPlanInput) {
			input.foreignWorker = func(fn *ssa.Function) (cl.CoroForeignWorkerCertificate, bool, error) {
				return cl.CoroForeignWorkerCertificate{ID: "legacy"}, fn == safe, nil
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := fixture.input
			test.mutate(&input)
			_, err := fixture.analyze(input, nil)
			if err == nil || !strings.Contains(err.Error(), "mutually exclusive generic callable and legacy") {
				t.Fatalf("generic/legacy conflict error = %v", err)
			}
		})
	}
}
