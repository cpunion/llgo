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
	"regexp"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroManagedDispatchAwaitEmitsCapabilityBranchesAndChildHandoff(t *testing.T) {
	const source = `package foo

func Plain(value int) int { return value + 1 }
func Async(value int) int { return value + 2 }

func Apply(callback func(int) int, value int) int {
	return callback(value)
}
`
	for _, test := range []struct {
		name string
		open bool
	}{
		{name: "open managed fallback", open: true},
		{name: "closed coroutine singleton"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, _, files := buildGoSSAPkg(t, source)
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
			if err != nil {
				t.Fatal(err)
			}
			apply := ssaPkg.Func("Apply")
			plain := ssaPkg.Func("Plain")
			async := ssaPkg.Func("Async")
			dynamicCall := onlyCoroManagedDispatchValidationCall(t, apply)
			functionIDs := universe.FunctionIDConfig()
			functionIDs.CoroABI = coro.PhysicalABIV1
			functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
			functionIDs.ArchiveReady = true
			plan, err := coro.AnalyzeSSA(
				ssaPkg.Prog,
				coro.Roots{{Function: apply, Demand: coro.AsyncDemand}},
				coro.SSAConfig{
					EmissionUniverse:     ssaUniverse,
					FunctionIDs:          functionIDs,
					MaxPlainInstructions: -1,
					ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
						switch fn {
						case plain:
							return coro.SSAFunctionPolicy{NeedsDispatch: true}, nil
						case async:
							return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, NeedsDispatch: true}, nil
						default:
							return coro.SSAFunctionPolicy{}, nil
						}
					},
					ClassifyUnknownCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.UnknownTarget, error) {
						if test.open && call == dynamicCall {
							return coro.UnknownManagedDispatch, nil
						}
						return coro.UnknownManaged, nil
					},
					ClassifyClosedDynamicCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.SSAClosedDynamicCallCertificate, bool, error) {
						if !test.open && call == dynamicCall {
							return coro.SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{async}}, true, nil
						}
						return coro.SSAClosedDynamicCallCertificate{}, false, nil
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			callPlan, ok := plan.CallPlan(dynamicCall)
			if !ok || callPlan.Rep != coro.Dispatch {
				t.Fatalf("Apply callback CallPlan = %+v, present=%t; want Dispatch", callPlan, ok)
			}
			if test.open {
				if !callPlan.Open || callPlan.Unresolved != coro.UnknownManagedDispatch {
					t.Fatalf("Apply callback CallPlan = %+v; want open managed fallback", callPlan)
				}
			} else if callPlan.Open || len(callPlan.Targets) != 1 {
				t.Fatalf("Apply callback CallPlan = %+v; want one closed coroutine target", callPlan)
			}
			if !test.open {
				functionPlan, present := plan.FunctionPlan(async)
				if !present || functionPlan.FuncRep != coro.Dispatch || functionPlan.Emission != coro.EmitCoroutine {
					t.Fatalf("Async plan = %+v, present=%t; want coroutine Dispatch target", functionPlan, present)
				}
			}

			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
			enableCoroPreemptCompilation(compilation)
			compilation.PanicABI = coro.PanicExplicitStatusABIV0
			compilation.FuncRepABI = coro.FuncRepABIV1
			compiled, _, err := NewPackageExWithEmbedOptions(
				prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatalf("compile managed descriptor await: %v", err)
			}
			module := compiled.Module()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify managed descriptor await: %v\n%s", err, module.String())
			}

			applyIR := requireCoroPhysicalFunction(t, module, "foo.Apply").String()
			if strings.Contains(applyIR, "AssertNilDeref") ||
				!strings.Contains(applyIR, "call void @"+coroFaultPrepareHookV1) {
				t.Fatalf("Apply did not lower the nullable descriptor through its structured coroutine fault edge:\n%s", applyIR)
			}
			// The capability probe validates the shared descriptor and branches on the
			// HasCoro bit (2) before either capability-specific indirect call.
			probe := regexp.MustCompile(`(?s)and i32 [^\n]+, 2.*icmp ne i32 [^\n]+, 0.*br i1`).FindStringIndex(applyIR)
			if probe == nil {
				t.Fatalf("Apply has no HasCoro capability probe and branch:\n%s", applyIR)
			}
			plainCall := regexp.MustCompile(`call i64 %[-a-zA-Z$._0-9]+\(ptr [^,]+, i64 [^)]+\)`).FindStringIndex(applyIR)
			coroCall := regexp.MustCompile(`call ptr %[-a-zA-Z$._0-9]+\(ptr [^,]+, ptr [^,]+, ptr [^,]+, i64 [^)]+\)`).FindStringIndex(applyIR)
			if plainCall == nil || coroCall == nil {
				t.Fatalf("Apply is missing plain/coroutine descriptor branches (plain=%v coro=%v):\n%s", plainCall, coroCall, applyIR)
			}
			if !strings.Contains(applyIR, "@llvm.coro.promise") ||
				!strings.Contains(applyIR, "call void @"+coroAwaitPrepareHookV1) ||
				!strings.Contains(applyIR, "call i1 @"+coroAwaitInlineBeginHookV2) {
				t.Fatalf("Apply coroutine descriptor branch does not enter the shared child-await handoff:\n%s", applyIR)
			}
			await := strings.Index(applyIR, "call void @"+coroAwaitPrepareHookV1)
			inline := strings.Index(applyIR, "call i1 @"+coroAwaitInlineBeginHookV2)
			if await < coroCall[0] || inline < await || strings.Index(applyIR[inline:], "call i8 @llvm.coro.suspend") < 0 {
				t.Fatalf("Apply does not publish, try inline completion, and retain its dynamic-child slow suspend:\n%s", applyIR)
			}
			if !regexp.MustCompile(`store i64 [^,]+, ptr `).MatchString(applyIR[plainCall[0]:]) {
				t.Fatalf("Apply plain branch does not merge its result through the shared result slot:\n%s", applyIR)
			}

			runCoroABITestPipeline(t, prog, module)
			applyResume := module.NamedFunction("foo.Apply$coro.resume")
			if applyResume.IsNil() {
				t.Fatalf("CoroSplit did not create managed descriptor await resume:\n%s", module.String())
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify managed descriptor %s branch after CoroSplit: %v\n%s", test.name, err, module.String())
			}
		})
	}
}

func TestCoroManagedDispatchAwaitClosedMixedCertificateRemainsFailClosed(t *testing.T) {
	const source = `package foo
func Plain(value int) int { return value + 1 }
func Async(value int) int { return value + 2 }
func Apply(callback func(int) int, value int) int { return callback(value) }
`
	ssaPkg, _, _ := buildGoSSAPkg(t, source)
	apply := ssaPkg.Func("Apply")
	plain := ssaPkg.Func("Plain")
	async := ssaPkg.Func("Async")
	dynamicCall := onlyCoroManagedDispatchValidationCall(t, apply)
	_, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{{Function: apply, Demand: coro.AsyncDemand}},
		coro.SSAConfig{
			MaxPlainInstructions: -1,
			ClassifyClosedDynamicCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.SSAClosedDynamicCallCertificate, bool, error) {
				if call == dynamicCall {
					return coro.SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{plain, async}}, true, nil
				}
				return coro.SSAClosedDynamicCallCertificate{}, false, nil
			},
		},
	)
	// TODO: replace this negative gate with the same end-to-end IR assertions
	// above once whole-program function flow can certify more than one exact
	// target. Dynamic codegen is already capability-aware; only the closed-flow
	// certificate remains singleton in this slice.
	if err == nil || !strings.Contains(err.Error(), "only nil or one exact target is supported") {
		t.Fatalf("closed mixed certificate result = %v; want the current singleton fail-closed boundary", err)
	}
}

func TestCoroManagedDispatchAwaitSupportsStdlibAggregateABI(t *testing.T) {
	const source = `package foo

func Apply(
	callback func(int, []byte, string, any, *byte) (int, error, string, []byte, any, *byte),
	fd int, data []byte, label string, value any, pointer *byte,
) (int, error, string, []byte, any, *byte) {
	return callback(fd, data, label, value, pointer)
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	apply := ssaPkg.Func("Apply")
	dynamicCall := onlyCoroManagedDispatchValidationCall(t, apply)
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{{Function: apply, Demand: coro.AsyncDemand}},
		coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          functionIDs,
			MaxPlainInstructions: -1,
			ClassifyUnknownCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.UnknownTarget, error) {
				if call == dynamicCall {
					return coro.UnknownManagedDispatch, nil
				}
				return coro.UnknownManaged, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	callPlan, ok := plan.CallPlan(dynamicCall)
	if !ok || callPlan.Rep != coro.Dispatch || !callPlan.Open || callPlan.Unresolved != coro.UnknownManagedDispatch {
		t.Fatalf("aggregate Apply CallPlan = %+v, present=%t; want open managed Dispatch", callPlan, ok)
	}
	if err := validateCoroManagedDispatchCall(plan, apply, dynamicCall, callPlan); err != nil {
		t.Fatalf("aggregate managed descriptor call rejected: %v", err)
	}
	audit, err := newCoroPhysicalPureSSAAudit(universe, plan, apply, "")
	if err != nil {
		t.Fatal(err)
	}
	proof := audit.currentFrameRetentionProof()
	if got := strings.Join(rootNames(proof.exactCallKeepaliveRoots(dynamicCall)), ","); got != "data,pointer" {
		t.Fatalf("managed descriptor child keepalive roots = %q, want data,pointer", got)
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	compilation.FuncRepABI = coro.FuncRepABIV1
	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("compile aggregate managed descriptor await: %v", err)
	}
	module := compiled.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify aggregate managed descriptor await: %v\n%s", err, module.String())
	}
	applyIR := requireCoroPhysicalFunction(t, module, "foo.Apply").String()
	if !strings.Contains(applyIR, "call void @"+coroAwaitPrepareHookV1) ||
		!strings.Contains(applyIR, "call i1 @"+coroAwaitInlineBeginHookV2) ||
		strings.Count(applyIR, "extractvalue") < 6 {
		t.Fatalf("aggregate descriptor branches did not hand off child and merge six typed results:\n%s", applyIR)
	}
	runCoroABITestPipeline(t, prog, module)
}
