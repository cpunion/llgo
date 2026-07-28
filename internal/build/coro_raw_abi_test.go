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

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestCoroRawPlainDirectForeignContractCompatibility(t *testing.T) {
	base := coro.CallableContract{
		ID:       "foreign.v1/test",
		Progress: coro.ProgressMayBlock,
		Affinity: coro.AffinityAnyThread,
		Reentry:  coro.ReentryNone,
		Memory:   coro.MemoryBorrowUntilComplete,
	}
	if !coroRawPlainDirectForeignContractCompatible(base) {
		t.Fatal("default may-block foreign contract was rejected on a raw caller stack")
	}
	callerThread := base
	callerThread.Affinity = coro.AffinityCallerThread
	if !coroRawPlainDirectForeignContractCompatible(callerThread) {
		t.Fatal("caller-thread contract was rejected on its exact raw caller stack")
	}
	for name, mutate := range map[string]func(*coro.CallableContract){
		"executor-safe": func(contract *coro.CallableContract) {
			contract.Progress = coro.ProgressExecutorSafe
		},
		"owner-thread": func(contract *coro.CallableContract) {
			contract.Affinity = coro.AffinityOwnerThread
		},
		"managed-reentry": func(contract *coro.CallableContract) {
			contract.Reentry = coro.ReentryManagedCallback
		},
		"retained-memory": func(contract *coro.CallableContract) {
			contract.Memory = coro.MemoryRetained
		},
	} {
		t.Run(name, func(t *testing.T) {
			contract := base
			mutate(&contract)
			if coroRawPlainDirectForeignContractCompatible(contract) {
				t.Fatalf("raw caller accepted incompatible contract %+v", contract)
			}
		})
	}
}

// A raw ABI address demand may suppress scanner-only preemption in an owned Go
// closure, but it is not evidence that a C leaf is nonblocking. In particular,
// following the exact static call below must not turn Foreign into
// ExternalKnown merely because Raw is embedded as a receiver-less runtime ABI
// helper.
func TestCoroRawABIClosureDoesNotCertifyUnknownForeign(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
func Foreign()
func Raw(depth uint) {
	if depth > 0 { Raw(depth - 1) }
	Foreign()
}
func Owner() {}
`, nil)
	owner := ssaPkg.Func("Owner")
	foreign := ssaPkg.Func("Foreign")
	raw := ssaPkg.Func("Raw")
	if raw == nil || len(raw.Blocks) == 0 {
		t.Fatalf("Raw SSA function = %v; want an owned body", raw)
	}
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{owner, raw, foreign})
	if err != nil {
		t.Fatal(err)
	}
	input := CoroPlanInput{
		Program:          ssaPkg.Prog,
		EmissionUniverse: universe,
		functionBackground: func(fn *ssa.Function) (llssa.Background, bool, error) {
			if fn == foreign {
				return llssa.InC, true, nil
			}
			return llssa.InGo, true, nil
		},
		demandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
			if fn == owner {
				return []*ssa.Function{raw}, nil
			}
			return nil, nil
		},
		syncDemandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
			if fn == owner {
				return []*ssa.Function{raw}, nil
			}
			return nil, nil
		},
	}
	_, err = input.Analyze(coro.Roots{{Function: owner, Demand: coro.SyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
	})
	if err == nil || !strings.Contains(err.Error(), "reaches uncertified external target") {
		t.Fatalf("raw unknown-foreign closure error = %v; want fail-closed external rejection", err)
	}

	// Replacing the conservative effect/exec shape with ExternalKnown is not
	// itself a physical raw-call proof. Only an immutable generic direct-executor,
	// legacy C noblock/sync, or translated-assembly certificate may admit an
	// ordinary raw path.
	_, err = input.Analyze(coro.Roots{{Function: owner, Demand: coro.SyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == foreign {
				return coro.SSAFunctionPolicy{
					IgnoreBody: true, External: coro.ExternalKnown, OverrideExternal: true,
				}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "without an exact direct executor, foreign-noblock, foreign-sync, or assembly-no-suspend certificate") {
		t.Fatalf("raw ExternalKnown/no-suspend closure error = %v; want exact-certificate rejection", err)
	}
}

func TestCoroRawABITerminalOnlyClosureMayFinishThroughUnknownForeign(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
func Foreign()
func FatalTail() { Foreign() }
func RawTerminal() {}
func RawStaticTerminal(ok bool) {
	if ok { return }
	FatalTail()
	panic("terminal")
}
func RawNormal() { FatalTail() }
func TerminalOwner() {}
func StaticTerminalOwner() {}
func NormalOwner() {}
`, nil)
	foreign := ssaPkg.Func("Foreign")
	fatalTail := ssaPkg.Func("FatalTail")
	rawTerminal := ssaPkg.Func("RawTerminal")
	rawStaticTerminal := ssaPkg.Func("RawStaticTerminal")
	rawNormal := ssaPkg.Func("RawNormal")
	terminalOwner := ssaPkg.Func("TerminalOwner")
	staticTerminalOwner := ssaPkg.Func("StaticTerminalOwner")
	normalOwner := ssaPkg.Func("NormalOwner")
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{
		foreign, fatalTail, rawTerminal, rawStaticTerminal, rawNormal,
		terminalOwner, staticTerminalOwner, normalOwner,
	})
	if err != nil {
		t.Fatal(err)
	}
	newInput := func(owner, raw *ssa.Function, unwind bool) CoroPlanInput {
		return CoroPlanInput{
			Program:          ssaPkg.Prog,
			EmissionUniverse: universe,
			functionBackground: func(fn *ssa.Function) (llssa.Background, bool, error) {
				if fn == foreign {
					return llssa.InC, true, nil
				}
				return llssa.InGo, true, nil
			},
			demandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
				if fn == owner {
					return []*ssa.Function{raw}, nil
				}
				return nil, nil
			},
			syncDemandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
				if fn == owner {
					return []*ssa.Function{raw}, nil
				}
				return nil, nil
			},
			loweredCalls: func(fn *ssa.Function) ([]coro.SSALoweredCall, error) {
				if fn == raw && unwind {
					return []coro.SSALoweredCall{{
						LogicalName: "runtime.FatalTail", Target: fatalTail, UnwindOnly: true,
					}}, nil
				}
				return nil, nil
			},
		}
	}

	terminalPlan, err := newInput(terminalOwner, rawTerminal, true).Analyze(
		coro.Roots{{Function: terminalOwner, Demand: coro.SyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1},
	)
	if err != nil {
		t.Fatalf("exact terminal-only foreign tail rejected: %v", err)
	}
	if got := functionPlanForBuildTest(t, terminalPlan, fatalTail); got.ManagedDemand != coro.NoDemand || !got.RawPlainDemand ||
		!got.RawPlainOnly || got.RawPlainEntry || !terminalPlan.HasRawPlainVariant(fatalTail) ||
		!got.Effect.Contains(coro.WaitForeign) || got.Emission != coro.EmitRawPlain {
		t.Fatalf("terminal fatal-tail plan = %+v; want one raw-only body with preserved foreign-wait facts", got)
	}
	if got := functionPlanForBuildTest(t, terminalPlan, foreign); got.External != coro.ExternalUnknownForeign ||
		!got.Exec.Contains(coro.BlockForeign) {
		t.Fatalf("terminal foreign plan = %+v; terminal reachability must not globally certify it as nonblocking", got)
	}

	staticTerminalPlan, err := newInput(staticTerminalOwner, rawStaticTerminal, false).Analyze(
		coro.Roots{{Function: staticTerminalOwner, Demand: coro.SyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1},
	)
	if err != nil {
		t.Fatalf("explicit static call in a no-normal-return block rejected: %v", err)
	}
	if got := functionPlanForBuildTest(t, staticTerminalPlan, rawStaticTerminal); got.ManagedDemand != coro.NoDemand || !got.RawPlainDemand ||
		!got.RawPlainOnly || !got.RawPlainEntry || !staticTerminalPlan.HasRawPlainVariant(rawStaticTerminal) || got.Emission != coro.EmitRawPlain ||
		!got.Effect.Contains(coro.WaitForeign) || got.LocalEffect.Contains(coro.WaitForeign) {
		t.Fatalf("static-terminal raw plan = %+v; want raw-only emission with conservative terminal effect facts", got)
	}
	if got := functionPlanForBuildTest(t, staticTerminalPlan, fatalTail); got.ManagedDemand != coro.NoDemand || !got.RawPlainDemand ||
		!got.RawPlainOnly || got.RawPlainEntry || !staticTerminalPlan.HasRawPlainVariant(fatalTail) || got.Emission != coro.EmitRawPlain {
		t.Fatalf("static-terminal fatal-tail plan = %+v; want internal raw-only body without address capability", got)
	}

	_, err = newInput(normalOwner, rawNormal, false).Analyze(
		coro.Roots{{Function: normalOwner, Demand: coro.SyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1},
	)
	if err == nil || !strings.Contains(err.Error(), "reaches uncertified external target") {
		t.Fatalf("ordinary raw path to the same foreign tail error = %v; want fail-closed external rejection", err)
	}
}

func TestCoroRequiredPlainSyncRootGetsProvenRawEntry(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
func OutcomeRoot(depth int) int {
	base := depth + 40
	callback := func(delta int) int {
		if delta < 0 { panic(delta) }
		return base + delta
	}
	if depth > 0 { return OutcomeRoot(depth - 1) }
	return callback(2)
}
`, nil)
	root := ssaPkg.Func("OutcomeRoot")
	if len(root.AnonFuncs) != 1 || len(root.AnonFuncs[0].FreeVars) != 1 {
		t.Fatalf("OutcomeRoot captured closure = %+v", root.AnonFuncs)
	}
	captured := root.AnonFuncs[0]
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{root, captured})
	if err != nil {
		t.Fatal(err)
	}
	input := CoroPlanInput{
		Program:          ssaPkg.Prog,
		EmissionUniverse: universe,
		functionBackground: func(*ssa.Function) (llssa.Background, bool, error) {
			return llssa.InGo, true, nil
		},
		requiredRoots: coro.Roots{{Function: root, Demand: coro.SyncDemand}},
		requiredPlain: map[*ssa.Function]struct{}{root: {}},
	}
	plan, err := input.Analyze(nil, coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}
	got := functionPlanForBuildTest(t, plan, root)
	if got.Demand != coro.SyncDemand || got.ManagedDemand != coro.NoDemand || !got.RawPlainDemand || !got.RawPlainOnly ||
		got.Emission != coro.EmitRawPlain || got.Primary != coro.PrimaryPlain || !got.RawPlainEntry ||
		!got.Effect.Contains(coro.YieldOnly|coro.AwaitStructured) {
		t.Fatalf("required synchronous runtime root plan = %+v; want raw-only entry with preserved explicit-status facts", got)
	}
	capturedPlan := functionPlanForBuildTest(t, plan, captured)
	if capturedPlan.ManagedDemand != coro.SyncDemand || !capturedPlan.RawPlainDemand || capturedPlan.RawPlainOnly ||
		capturedPlan.RawPlainEntry || !plan.HasRawPlainVariant(captured) || capturedPlan.Emission != coro.EmitPlain ||
		capturedPlan.Primary != coro.PrimaryPlain {
		t.Fatalf("captured raw closure plan = %+v, variant=%t; want one shared no-suspend plain body without address publication", capturedPlan, plan.HasRawPlainVariant(captured))
	}
}

func TestCoroRequiredPlainSyncRootRejectsRealPark(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
var blocked chan int
func ParkRoot() { <-blocked }
`, nil)
	root := ssaPkg.Func("ParkRoot")
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{root})
	if err != nil {
		t.Fatal(err)
	}
	input := CoroPlanInput{
		Program:          ssaPkg.Prog,
		EmissionUniverse: universe,
		functionBackground: func(*ssa.Function) (llssa.Background, bool, error) {
			return llssa.InGo, true, nil
		},
		requiredRoots: coro.Roots{{Function: root, Demand: coro.SyncDemand}},
		requiredPlain: map[*ssa.Function]struct{}{root: {}},
	}
	_, err = input.Analyze(nil, coro.SSAConfig{MaxPlainInstructions: -1})
	if err == nil ||
		!strings.Contains(err.Error(), "real local suspend effect may-park") ||
		!strings.Contains(err.Error(), "provenance:") ||
		!strings.Contains(err.Error(), "compiler/runtime raw ABI entry") {
		t.Fatalf("required synchronous runtime park error = %v; want exact raw-ABI feasibility rejection", err)
	}
}

func TestCoroGeneratedWorkerElisionPublishesExactRawAdapter(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/cgoworkerraw", `package cgoworkerraw
func Adapter(value uint64) uint64 { return value + 1 }
func Root(value uint64) uint64 { return Adapter(value) }
`, nil)
	root := ssaPkg.Func("Root")
	adapter := ssaPkg.Func("Adapter")
	calls := coroPlanTestCalls(root)
	if len(calls) != 1 {
		t.Fatalf("Root calls = %d, want one generated-adapter call", len(calls))
	}
	workerCall := calls[0]
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{root, adapter})
	if err != nil {
		t.Fatal(err)
	}
	input := CoroPlanInput{
		Program:          ssaPkg.Prog,
		EmissionUniverse: universe,
		functionBackground: func(*ssa.Function) (llssa.Background, bool, error) {
			return llssa.InGo, true, nil
		},
		syncDemandReferences: func(*ssa.Function) ([]*ssa.Function, error) {
			return nil, nil
		},
		callSitePlan: func(call ssa.CallInstruction) (cl.CoroCallSitePlan, bool, error) {
			if call != workerCall {
				return cl.CoroCallSitePlan{}, false, nil
			}
			return cl.CoroCallSitePlan{
				Elision:            cl.CoroCallElidedCgoWorker,
				ElisionCertificate: "exact-cgo-worker",
				CgoWorkerTarget:    adapter,
			}, true, nil
		},
	}
	plan, err := input.Analyze(
		coro.Roots{{Function: root, Demand: coro.SyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1},
	)
	if err != nil {
		t.Fatal(err)
	}
	rootPlan := functionPlanForBuildTest(t, plan, root)
	if !rootPlan.Effect.Contains(coro.WaitForeign) || rootPlan.Emission != coro.EmitCoroutine ||
		!plan.ElidesCall(workerCall) {
		t.Fatalf("managed cgo caller plan = %+v, elided=%t; want coroutine worker wait", rootPlan, plan.ElidesCall(workerCall))
	}
	adapterPlan := functionPlanForBuildTest(t, plan, adapter)
	if adapterPlan.ManagedDemand != coro.NoDemand || !adapterPlan.RawPlainDemand ||
		!adapterPlan.RawPlainOnly || !adapterPlan.RawPlainEntry ||
		adapterPlan.Emission != coro.EmitRawPlain || adapterPlan.Primary != coro.PrimaryPlain ||
		!plan.HasRawPlainVariant(adapter) {
		t.Fatalf(
			"generated cgo adapter plan = %+v, variant=%t; want exact raw-only worker entry",
			adapterPlan, plan.HasRawPlainVariant(adapter),
		)
	}
}

func TestCoroDeferredGeneratedWorkerElisionPublishesExactRawAdapter(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/cgoworkerdeferraw", `package cgoworkerdeferraw
func Adapter(value uint64) uint64 { return value + 1 }
func Root(value uint64) { defer Adapter(value) }
`, nil)
	root := ssaPkg.Func("Root")
	adapter := ssaPkg.Func("Adapter")
	calls := coroPlanTestCalls(root)
	if len(calls) != 1 {
		t.Fatalf("Root calls = %d, want one deferred generated-adapter call", len(calls))
	}
	workerDefer, deferred := calls[0].(*ssa.Defer)
	if !deferred {
		t.Fatalf("Root call = %T, want *ssa.Defer", calls[0])
	}
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{root, adapter})
	if err != nil {
		t.Fatal(err)
	}
	input := CoroPlanInput{
		Program:          ssaPkg.Prog,
		EmissionUniverse: universe,
		functionBackground: func(*ssa.Function) (llssa.Background, bool, error) {
			return llssa.InGo, true, nil
		},
		syncDemandReferences: func(*ssa.Function) ([]*ssa.Function, error) {
			return nil, nil
		},
		callSitePlan: func(call ssa.CallInstruction) (cl.CoroCallSitePlan, bool, error) {
			if call != workerDefer {
				return cl.CoroCallSitePlan{}, false, nil
			}
			return cl.CoroCallSitePlan{
				Elision:            cl.CoroCallElidedCgoWorker,
				ElisionCertificate: "exact-deferred-cgo-worker",
				CgoWorkerTarget:    adapter,
			}, true, nil
		},
	}
	plan, err := input.Analyze(
		coro.Roots{{Function: root, Demand: coro.SyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1},
	)
	if err != nil {
		t.Fatal(err)
	}
	rootPlan := functionPlanForBuildTest(t, plan, root)
	if !rootPlan.Effect.Contains(coro.WaitForeign) || rootPlan.Emission != coro.EmitCoroutine ||
		!plan.ElidesCall(workerDefer) {
		t.Fatalf(
			"deferred managed cgo caller plan = %+v, elided=%t; want coroutine worker wait",
			rootPlan, plan.ElidesCall(workerDefer),
		)
	}
	adapterPlan := functionPlanForBuildTest(t, plan, adapter)
	if adapterPlan.ManagedDemand != coro.NoDemand || !adapterPlan.RawPlainDemand ||
		!adapterPlan.RawPlainOnly || !adapterPlan.RawPlainEntry ||
		adapterPlan.Emission != coro.EmitRawPlain || adapterPlan.Primary != coro.PrimaryPlain ||
		!plan.HasRawPlainVariant(adapter) {
		t.Fatalf(
			"deferred generated cgo adapter plan = %+v, variant=%t; want exact raw-only worker entry",
			adapterPlan, plan.HasRawPlainVariant(adapter),
		)
	}
}

func TestCoroRawABIPlainVariantAttributesOnlyCertifiedWorkerPark(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
var blocked chan int
func Intrinsic(uintptr)
func Owner() {}
func RawWorker() { Intrinsic(1) }
func RawChannel() { Intrinsic(2); <-blocked }
func RawUncertified() { Intrinsic(3) }
func RawSynchronous() { Intrinsic(4) }
`, nil)
	owner := ssaPkg.Func("Owner")
	intrinsic := ssaPkg.Func("Intrinsic")
	rawWorker := ssaPkg.Func("RawWorker")
	rawChannel := ssaPkg.Func("RawChannel")
	rawUncertified := ssaPkg.Func("RawUncertified")
	rawSynchronous := ssaPkg.Func("RawSynchronous")
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{
		owner, intrinsic, rawWorker, rawChannel, rawUncertified, rawSynchronous,
	})
	if err != nil {
		t.Fatal(err)
	}

	analyze := func(raw *ssa.Function, policyPark bool) (*coro.SSAPlan, error) {
		input := CoroPlanInput{
			Program:          ssaPkg.Prog,
			EmissionUniverse: universe,
			functionBackground: func(*ssa.Function) (llssa.Background, bool, error) {
				return llssa.InGo, true, nil
			},
			demandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
				if fn == owner {
					return []*ssa.Function{raw}, nil
				}
				return nil, nil
			},
			syncDemandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
				if fn == owner {
					return []*ssa.Function{raw}, nil
				}
				return nil, nil
			},
			callSitePlan: func(call ssa.CallInstruction) (cl.CoroCallSitePlan, bool, error) {
				if call != nil && call.Common() != nil && call.Common().StaticCallee() == intrinsic {
					if call.Parent() == rawSynchronous {
						return cl.CoroCallSitePlan{
							IntrinsicSemantics:           cl.CoroIntrinsicCallUnsupported,
							Intrinsic:                    true,
							RawPlainSynchronousIntrinsic: true,
						}, true, nil
					}
					certificate := ""
					if call.Parent() != rawUncertified {
						certificate = "worker-exact"
					}
					return cl.CoroCallSitePlan{
						IntrinsicSemantics:           cl.CoroIntrinsicCallInlineSuspend,
						Intrinsic:                    true,
						RawPlainSynchronousIntrinsic: true,
						Elision:                      cl.CoroCallElidedIntrinsic,
						ElisionCertificate:           certificate,
					}, true, nil
				}
				return cl.CoroCallSitePlan{}, false, nil
			},
		}
		return input.Analyze(coro.Roots{{Function: owner, Demand: coro.SyncDemand}}, coro.SSAConfig{
			MaxPlainInstructions: -1,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if policyPark && fn == raw {
					return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		})
	}

	plan, err := analyze(rawWorker, false)
	if err != nil {
		t.Fatalf("certified worker park did not receive its synchronous raw interpretation: %v", err)
	}
	workerPlan := functionPlanForBuildTest(t, plan, rawWorker)
	if !workerPlan.LocalEffect.Contains(coro.MayPark) || !workerPlan.RawPlainOnly ||
		workerPlan.Emission != coro.EmitRawPlain || !plan.HasRawPlainVariant(rawWorker) {
		t.Fatalf("certified worker raw plan = %+v, variant=%t", workerPlan, plan.HasRawPlainVariant(rawWorker))
	}

	plan, err = analyze(rawSynchronous, false)
	if err != nil {
		t.Fatalf("exact retained syscall did not receive its synchronous raw interpretation: %v", err)
	}
	synchronousPlan := functionPlanForBuildTest(t, plan, rawSynchronous)
	if !synchronousPlan.RawPlainOnly || synchronousPlan.Emission != coro.EmitRawPlain ||
		!plan.HasRawPlainVariant(rawSynchronous) {
		t.Fatalf("retained synchronous raw plan = %+v, variant=%t", synchronousPlan, plan.HasRawPlainVariant(rawSynchronous))
	}

	for name, test := range map[string]struct {
		raw        *ssa.Function
		policyPark bool
		want       string
	}{
		"real-channel":        {raw: rawChannel, want: "real local suspend effect may-park"},
		"uncertified-park":    {raw: rawUncertified, want: "has no exact worker elision certificate"},
		"builder-policy-park": {raw: rawWorker, policyPark: true, want: "unsupported declared raw-stack effect may-park"},
	} {
		_, err := analyze(test.raw, test.policyPark)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s raw park error = %v; want source-attributed rejection %q", name, err, test.want)
		}
	}
}

func TestCoroExplicitStatusElidedPanicIsRawOnlyUnlessSeparatelyManaged(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
func Panic(depth int) {
	if depth > 0 { Panic(depth - 1) }
}
func RawRoot() {}
`, nil)
	panicHelper := ssaPkg.Func("Panic")
	rawRoot := ssaPkg.Func("RawRoot")
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{panicHelper, rawRoot})
	if err != nil {
		t.Fatal(err)
	}
	input := CoroPlanInput{
		Program:          ssaPkg.Prog,
		EmissionUniverse: universe,
		functionBackground: func(*ssa.Function) (llssa.Background, bool, error) {
			return llssa.InGo, true, nil
		},
		requiredRoots:     coro.Roots{{Function: rawRoot, Demand: coro.SyncDemand}},
		requiredPlain:     map[*ssa.Function]struct{}{rawRoot: {}, panicHelper: {}},
		requiredHostPlain: map[*ssa.Function]struct{}{rawRoot: {}, panicHelper: {}},
		loweredCalls: func(fn *ssa.Function) ([]coro.SSALoweredCall, error) {
			if fn != rawRoot {
				return nil, nil
			}
			return []coro.SSALoweredCall{{
				LogicalName: "runtime.Panic", Target: panicHelper,
				UnwindOnly: true, ExplicitStatusElided: true,
			}}, nil
		},
	}

	rawPlan, err := input.Analyze(nil, coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatalf("raw-only ExplicitStatus panic plan: %v", err)
	}
	rawPanic := functionPlanForBuildTest(t, rawPlan, panicHelper)
	if rawPanic.ManagedDemand != coro.NoDemand || !rawPanic.RawPlainDemand || !rawPanic.RawPlainOnly ||
		rawPanic.Emission != coro.EmitRawPlain || rawPanic.Primary != coro.PrimaryPlain ||
		rawPanic.RawPlainEntry || !rawPlan.HasRawPlainVariant(panicHelper) ||
		!rawPanic.Effect.Contains(coro.YieldOnly) || !rawPanic.Exec.Contains(coro.NeedsPreempt) {
		t.Fatalf("raw-only runtime.Panic plan = %+v, variant=%t", rawPanic, rawPlan.HasRawPlainVariant(panicHelper))
	}

	mixedPlan, err := input.Analyze(
		coro.Roots{{Function: panicHelper, ManagedDemand: coro.AsyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1},
	)
	if err != nil {
		t.Fatalf("mixed ExplicitStatus panic plan: %v", err)
	}
	mixedPanic := functionPlanForBuildTest(t, mixedPlan, panicHelper)
	if mixedPanic.ManagedDemand != coro.AsyncDemand || !mixedPanic.RawPlainDemand || mixedPanic.RawPlainOnly ||
		mixedPanic.Emission != coro.EmitCoroutine || mixedPanic.Primary != coro.PrimaryCoroutine ||
		mixedPanic.RawPlainEntry || !mixedPlan.HasRawPlainVariant(panicHelper) {
		t.Fatalf("mixed runtime.Panic plan = %+v, variant=%t; want managed coroutine plus raw variant", mixedPanic, mixedPlan.HasRawPlainVariant(panicHelper))
	}
}

func TestCoroRequiredPlainRawRootRejectsOpenManagedCall(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
func OpenRoot(callback func()) { callback() }
`, nil)
	root := ssaPkg.Func("OpenRoot")
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{root})
	if err != nil {
		t.Fatal(err)
	}
	input := CoroPlanInput{
		Program:          ssaPkg.Prog,
		EmissionUniverse: universe,
		functionBackground: func(*ssa.Function) (llssa.Background, bool, error) {
			return llssa.InGo, true, nil
		},
		requiredRoots: coro.Roots{{Function: root, Demand: coro.SyncDemand}},
		requiredPlain: map[*ssa.Function]struct{}{root: {}},
	}
	_, err = input.Analyze(nil, coro.SSAConfig{MaxPlainInstructions: -1})
	if err == nil || !strings.Contains(err.Error(), "dynamic/open call") {
		t.Fatalf("required raw root open-call error = %v; want fail-closed dynamic/open rejection", err)
	}
}
