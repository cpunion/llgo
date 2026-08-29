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

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func TestCoroManagedDispatchAwaitEmitsCapabilityBranchesAndChildHandoff(t *testing.T) {
	const source = `package foo

func Plain(value int) int { return value + 1 }
func Async(value int) int { return value + 2 }
var outcomePanic any
func Outcome(value int) int {
	if value < 0 { panic(outcomePanic) }
	return value + 3
}

func Apply(callback func(int) int, value int) int {
	return callback(value)
}
`
	for _, test := range []struct {
		name          string
		open          bool
		outcomeTarget bool
	}{
		{name: "open managed fallback", open: true},
		{name: "closed coroutine singleton"},
		{name: "closed outcome singleton", outcomeTarget: true},
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
			outcome := ssaPkg.Func("Outcome")
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
					OutcomeMode:          coro.OutcomeExplicitStatus,
					MaxPlainInstructions: 64,
					ClassifyLocalBody:    universe.CoroLocalBodyFacts,
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
							target := async
							if test.outcomeTarget {
								target = outcome
							}
							return coro.SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{target}}, true, nil
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
				target, wantEmission := async, coro.EmitCoroutine
				if test.outcomeTarget {
					target, wantEmission = outcome, coro.EmitOutcomePlain
				}
				functionPlan, present := plan.FunctionPlan(target)
				if !present || functionPlan.FuncRep != coro.Dispatch || functionPlan.Emission != wantEmission {
					t.Fatalf("%s plan = %+v, present=%t; want %s Dispatch target", target.Name(), functionPlan, present, wantEmission)
				}
			}

			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
			enableCoroPreemptCompilation(compilation)
			compilation.PanicABI = coro.PanicExplicitStatusABIV0
			compilation.FuncRepABI = coro.FuncRepABIV2
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
			// An open call retains universal descriptor selection. A closed family
			// with one published structured capability loads only that entry word:
			// regenerating impossible branches would make every statically closed
			// funcval pay for open-world dispatch.
			outcomeProbe := regexp.MustCompile(`(?s)and i32 [^\n]+, 2.*icmp ne i32 [^\n]+, 0`).FindStringIndex(applyIR)
			coroProbe := regexp.MustCompile(`(?s)and i32 [^\n]+, 4.*icmp ne i32 [^\n]+, 0`).FindStringIndex(applyIR)
			plainCall := regexp.MustCompile(`call i64 %[-a-zA-Z$._0-9]+\(ptr [^,]+, i64 [^)]+\)`).FindStringIndex(applyIR)
			outcomeCall := regexp.MustCompile(`call void %[-a-zA-Z$._0-9]+\(ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^,]+, i64 [^)]+\)`).FindStringIndex(applyIR)
			coroCall := regexp.MustCompile(`call ptr %[-a-zA-Z$._0-9]+\(ptr [^,]+, ptr [^,]+, ptr [^,]+, i64 [^)]+\)`).FindStringIndex(applyIR)
			if test.outcomeTarget {
				if outcomeProbe != nil || coroProbe != nil || plainCall != nil || coroCall != nil || outcomeCall == nil {
					t.Fatalf("closed outcome Apply retained impossible descriptor paths (outcomeProbe=%v coroProbe=%v plain=%v outcome=%v coro=%v):\n%s",
						outcomeProbe, coroProbe, plainCall, outcomeCall, coroCall, applyIR)
				}
				if strings.Contains(applyIR, "load { i32, i32, i64, i64, ptr, ptr, i64, i64, ptr }") ||
					strings.Contains(applyIR, "call i1 @"+coroAwaitPrepareInlineHookV4) ||
					strings.Contains(applyIR, "call i32 @"+coroAwaitInlineDestroyConsumeHookV4) {
					t.Fatalf("closed outcome Apply retained full descriptor or child-handoff machinery:\n%s", applyIR)
				}
				descriptorFields := regexp.MustCompile(
					`getelementptr inbounds[^\n]+\{ i32, i32, i64, i64, ptr, ptr, i64, i64, ptr \}[^\n]+i32 0, i32 ([0-9]+)`,
				).FindAllStringSubmatch(applyIR, -1)
				if len(descriptorFields) != 1 || descriptorFields[0][1] != "5" {
					t.Fatalf("closed outcome Apply descriptor fields = %v, want only structured entry 5:\n%s", descriptorFields, applyIR)
				}
				outcomeBody := module.NamedFunction("foo.Outcome" + coroOutcomePlainPrimarySuffix)
				if outcomeBody.IsNil() || strings.Contains(outcomeBody.String(), "llvm.coro.") ||
					!module.NamedFunction("foo.Outcome"+coroPrimarySuffix).IsNil() {
					t.Fatalf("Outcome descriptor did not emit exactly one stackless synchronous body:\n%s", module.String())
				}
			} else if !test.open {
				if outcomeProbe != nil || coroProbe != nil || plainCall != nil || outcomeCall != nil || coroCall == nil {
					t.Fatalf("closed coroutine Apply retained impossible descriptor paths (outcomeProbe=%v coroProbe=%v plain=%v outcome=%v coro=%v):\n%s",
						outcomeProbe, coroProbe, plainCall, outcomeCall, coroCall, applyIR)
				}
				if strings.Contains(applyIR, "load { i32, i32, i64, i64, ptr, ptr, i64, i64, ptr }") ||
					!strings.Contains(applyIR, ", i32 0, i32 5") ||
					!strings.Contains(applyIR, "call i1 @"+coroAwaitPrepareInlineHookV4) ||
					!strings.Contains(applyIR, "call i32 @"+coroAwaitInlineDestroyConsumeHookV4) {
					t.Fatalf("closed coroutine Apply did not use one narrow structured entry plus child handoff:\n%s", applyIR)
				}
				await := strings.Index(applyIR, "call i1 @"+coroAwaitPrepareInlineHookV4)
				if await < coroCall[0] || strings.Index(applyIR[await:], "call i8 @llvm.coro.suspend") < 0 {
					t.Fatalf("closed coroutine Apply does not publish and retain its dynamic-child slow suspend:\n%s", applyIR)
				}
			} else {
				if outcomeProbe == nil || coroProbe == nil {
					t.Fatalf("Apply has no outcome/coroutine capability probes (outcome=%v coro=%v):\n%s", outcomeProbe, coroProbe, applyIR)
				}
				if loads := strings.Count(applyIR, "load { i32, i32, i64, i64, ptr, ptr, i64, i64, ptr }"); loads != 1 {
					t.Fatalf("Apply descriptor aggregate loads = %d, want one validated selection shared by all branches:\n%s", loads, applyIR)
				}
				if plainCall == nil || outcomeCall == nil || coroCall == nil {
					t.Fatalf("Apply is missing plain/outcome/coroutine descriptor branches (plain=%v outcome=%v coro=%v):\n%s", plainCall, outcomeCall, coroCall, applyIR)
				}
				if !strings.Contains(applyIR, "@llvm.coro.promise") ||
					!strings.Contains(applyIR, "call i1 @"+coroAwaitPrepareInlineHookV4) ||
					!strings.Contains(applyIR, "call i32 @"+coroAwaitInlineDestroyConsumeHookV4) {
					t.Fatalf("Apply coroutine descriptor branch does not enter the shared child-await handoff:\n%s", applyIR)
				}
				await := strings.Index(applyIR, "call i1 @"+coroAwaitPrepareInlineHookV4)
				if await < coroCall[0] || strings.Index(applyIR[await:], "call i8 @llvm.coro.suspend") < 0 {
					t.Fatalf("Apply does not publish, try inline completion, and retain its dynamic-child slow suspend:\n%s", applyIR)
				}
				if !regexp.MustCompile(`store i64 [^,]+, ptr `).MatchString(applyIR[plainCall[0]:]) {
					t.Fatalf("Apply plain branch does not merge its result through the shared result slot:\n%s", applyIR)
				}
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

func TestCoroManagedDispatchDoesNotInterpretCoroutineDescriptorAsOutcomeTwin(t *testing.T) {
	const source = `package foo

var panicValue any

func Target() {
	if panicValue != nil { panic(panicValue) }
}

func Root(callback func()) {
	Target()
	callback()
}

func Block(callback func()) { defer callback() }

func Seed() {
	Block(Target)
	Root(Target)
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(
		prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}},
	)
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	root, block := ssaPkg.Func("Root"), ssaPkg.Func("Block")
	target, seed := ssaPkg.Func("Target"), ssaPkg.Func("Seed")
	var dynamicCall *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common() != nil && call.Common().StaticCallee() == nil {
				if dynamicCall != nil {
					t.Fatal("Root has more than one dynamic call")
				}
				dynamicCall = call
			}
		}
	}
	if dynamicCall == nil {
		t.Fatal("Root has no dynamic call")
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{{Function: seed, ManagedDemand: coro.AsyncDemand}},
		coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          functionIDs,
			OutcomeMode:          coro.OutcomeExplicitStatus,
			MaxPlainInstructions: 64,
			ClassifyLocalBody:    universe.CoroLocalBodyFacts,
			ClassifyClosedDynamicCall: func(owner *ssa.Function, call ssa.CallInstruction) (coro.SSAClosedDynamicCallCertificate, bool, error) {
				if (owner == root || owner == block) && call.Common() != nil && call.Common().StaticCallee() == nil {
					return coro.SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{target}}, true, nil
				}
				return coro.SSAClosedDynamicCallCertificate{}, false, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	targetPlan, found := plan.FunctionPlan(target)
	if !found || targetPlan.Emission != coro.EmitCoroutine ||
		targetPlan.ManagedEntry != coro.ManagedEntryCoroutine ||
		!targetPlan.HasStaticOutcome() {
		t.Fatalf("Target plan = %+v, present=%t; want coroutine primary with an exact-call outcome twin", targetPlan, found)
	}
	callPlan, found := plan.CallPlan(dynamicCall)
	if !found || callPlan.Open || callPlan.Rep != coro.Dispatch || len(callPlan.Targets) != 1 {
		t.Fatalf("dynamic CallPlan = %+v, present=%t; want one closed descriptor target", callPlan, found)
	}
	if coroManagedDispatchPublishedOutcomeOnly(plan, callPlan) {
		t.Fatal("coroutine-primary descriptor was accepted as an outcome-only publication")
	}
	if !coroManagedDispatchPublishedCoroutineOnly(plan, callPlan) {
		t.Fatal("closed coroutine-primary descriptor did not select its narrow publication")
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	compilation.FuncRepABI = coro.FuncRepABIV2
	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("compile dual-capability descriptor target: %v", err)
	}
	module := compiled.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify dual-capability descriptor target: %v\n%s", err, module.String())
	}
	rootIR := requireCoroPhysicalFunction(t, module, "foo.Root").String()
	if strings.Contains(rootIR, " and i32 ") ||
		strings.Contains(rootIR, "load { i32, i32, i64, i64, ptr, ptr") ||
		!strings.Contains(rootIR, ", i32 0, i32 5") ||
		!regexp.MustCompile(`call ptr %[-a-zA-Z$._0-9]+\(ptr [^,]+, ptr [^,]+, ptr [^)]+\)`).MatchString(rootIR) ||
		!strings.Contains(rootIR, "call i1 @"+coroAwaitPrepareInlineHookV4) {
		t.Fatalf("Root did not lower the closed descriptor to one narrow coroutine path:\n%s", rootIR)
	}
	blockIR := requireCoroPhysicalFunction(t, module, "foo.Block").String()
	if strings.Contains(blockIR, "load { i32, i32, i64, i64, ptr, ptr") ||
		!strings.Contains(blockIR, ", i32 0, i32 5") ||
		!regexp.MustCompile(`call ptr %[-a-zA-Z$._0-9]+\(ptr [^,]+, ptr [^,]+, ptr [^)]+\)`).MatchString(blockIR) {
		t.Fatalf("deferred closed descriptor did not use one narrow coroutine entry:\n%s", blockIR)
	}
	runCoroABITestPipeline(t, prog, module)
	if resume := module.NamedFunction("foo.Root$coro.resume"); resume.IsNil() {
		t.Fatalf("CoroSplit did not retain Root's dynamic child handoff:\n%s", module.String())
	}
}

func TestCoroInstantiatedBoundMethodsUseOneConcreteDescriptorEach(t *testing.T) {
	const source = `package foo
type Foo[T any] struct { value T }
func Seed() {}
func (foo Foo[T]) Get() T { Seed(); return foo.value }
var NewInt = Foo[int]{value: 1}.Get
var NewString = Foo[string]{value: "x"}.Get
func Root() { _ = NewInt(); _ = NewString() }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	initFn, root, seed := ssaPkg.Func("init"), ssaPkg.Func("Root"), ssaPkg.Func("Seed")
	wrappers := make(map[string]*ssa.Function)
	for function := range ssautil.AllFunctions(ssaPkg.Prog) {
		if function == nil || !strings.HasPrefix(function.Synthetic, "bound method wrapper for ") ||
			len(function.FreeVars) != 1 {
			continue
		}
		receiver := function.FreeVars[0].Type().String()
		switch {
		case strings.Contains(receiver, "Foo[int]"):
			wrappers["int"] = function
		case strings.Contains(receiver, "Foo[string]"):
			wrappers["string"] = function
		}
	}
	calls := make(map[string]*ssa.Call)
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common().StaticCallee() != nil {
				continue
			}
			switch call.Type().String() {
			case "int":
				calls["int"] = call
			case "string":
				calls["string"] = call
			}
		}
	}
	if len(wrappers) != 2 || len(calls) != 2 {
		t.Fatalf("fixture wrappers=%v calls=%v, want int and string", wrappers, calls)
	}

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
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{{Function: initFn, Demand: coro.SyncDemand}, {Function: root, Demand: coro.AsyncDemand}},
		coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          functionIDs,
			MaxPlainInstructions: -1,
			ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if function == seed {
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
			ClassifyClosedDynamicCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.SSAClosedDynamicCallCertificate, bool, error) {
				for kind, dynamic := range calls {
					if call == dynamic {
						return coro.SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{wrappers[kind]}}, true, nil
					}
				}
				return coro.SSAClosedDynamicCallCertificate{}, false, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for kind, wrapper := range wrappers {
		functionPlan, found := plan.FunctionPlan(wrapper)
		if !found || functionPlan.FuncRep != coro.Dispatch || functionPlan.Emission != coro.EmitCoroutine ||
			functionPlan.Primary != coro.PrimaryCoroutine || plan.HasRawPlainVariant(wrapper) {
			t.Fatalf("%s bound wrapper plan = %+v, present=%t raw-plain=%t; want one coroutine descriptor primary",
				kind, functionPlan, found, plan.HasRawPlainVariant(wrapper))
		}
		callPlan, found := plan.CallPlan(calls[kind])
		if !found || callPlan.Rep != coro.Dispatch || callPlan.Open || len(callPlan.Targets) != 1 {
			t.Fatalf("%s dynamic CallPlan = %+v, present=%t; want one closed descriptor target", kind, callPlan, found)
		}
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	compilation.FuncRepABI = coro.FuncRepABIV2
	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("compile instantiated bound methods: %v", err)
	}
	module := compiled.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify instantiated bound methods: %v\n%s", err, module.String())
	}

	descriptors, hashes := 0, make(map[[2]uint64]struct{})
	for global := module.FirstGlobal(); !global.IsNil(); global = llvm.NextGlobal(global) {
		if !strings.HasPrefix(global.Name(), coroPlainDispatchDescriptorPrefix) {
			continue
		}
		descriptors++
		initializer := global.Initializer()
		if got := initializer.Operand(1).ZExtValue(); got != uint64(llssa.CoroDispatchFlagHasCoro) {
			t.Fatalf("instantiated bound descriptor flags = %#x, want captured HasCoro only", got)
		}
		if initializer.Operand(4).IsAConstantPointerNull().IsNil() ||
			!initializer.Operand(5).IsAConstantPointerNull().IsNil() {
			t.Fatalf("instantiated bound descriptor publishes a redundant plain entry or lacks its coroutine entry: %v", initializer)
		}
		hashes[[2]uint64{initializer.Operand(2).ZExtValue(), initializer.Operand(3).ZExtValue()}] = struct{}{}
	}
	coroThunks, plainThunks := 0, 0
	for function := module.FirstFunction(); !function.IsNil(); function = llvm.NextFunction(function) {
		switch {
		case strings.HasPrefix(function.Name(), coroCoroDispatchThunkPrefix):
			coroThunks++
		case strings.HasPrefix(function.Name(), coroPlainDispatchThunkPrefix):
			plainThunks++
		}
	}
	if descriptors != 2 || len(hashes) != 2 || coroThunks != 2 || plainThunks != 0 {
		t.Fatalf("instantiated bound emission descriptors=%d unique-hashes=%d coro-thunks=%d plain-thunks=%d; want 2,2,2,0\n%s",
			descriptors, len(hashes), coroThunks, plainThunks, module.String())
	}
	for kind, wrapper := range wrappers {
		name, err := universe.physicalName(ssaPkg, wrapper, funcName(ssaPkg.Pkg, wrapper, false))
		if err != nil {
			t.Fatal(err)
		}
		if !module.NamedFunction(name).IsNil() || module.NamedFunction(name+coroPrimarySuffix).IsNil() {
			t.Fatalf("%s bound wrapper did not emit exactly its coroutine primary %q\n%s", kind, name+coroPrimarySuffix, module.String())
		}
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
	compilation.FuncRepABI = coro.FuncRepABIV2
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
	if !strings.Contains(applyIR, "call i1 @"+coroAwaitPrepareInlineHookV4) ||
		!strings.Contains(applyIR, "call i32 @"+coroAwaitInlineDestroyConsumeHookV4) ||
		strings.Count(applyIR, "extractvalue") < 6 {
		t.Fatalf("aggregate descriptor branches did not hand off child and merge six typed results:\n%s", applyIR)
	}
	runCoroABITestPipeline(t, prog, module)
}
