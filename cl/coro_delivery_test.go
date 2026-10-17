//go:build !llgo

package cl

import (
	"go/ast"
	"go/types"
	"strconv"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroDeliveryLargeFallbackAllocatesOnlyOnNilOutput(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
		size int
	}{
		{name: "native", size: 131073},
		{name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}, size: 131073},
		{name: "native-boundary", size: 131072},
		{name: "wasm32-boundary", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}, size: 131072},
	} {
		t.Run(target.name, func(t *testing.T) {
			source := newEmissionTestProgram()
			source.ssa.CreatePackage(types.Unsafe, nil, nil, true)
			runtimePkg := source.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func AllocZ(size uintptr) unsafe.Pointer { return nil }
func AllocU(size uintptr) unsafe.Pointer { return nil }
func FreeDeferNode(pointer unsafe.Pointer) {}
func AssertNilDeref(value bool) {}
`)
			foo := source.addPackage(t, "foo", strings.ReplaceAll(`package foo
import "unsafe"
type Result = struct { R0 [131073]byte }
func initialize(out *Result) { out.R0[0] = 1 }
func drop(out *Result, _ unsafe.Pointer) { out.R0[0] = 0 }
//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*Result), func(*Result, unsafe.Pointer)) *Result
func Acquire() [131073]byte { out := borrow(initialize, drop); return out.R0 }
func Root() byte { return Acquire()[0] }
func Deferred() { defer Acquire() }
func DynamicDeferred(f func() [131073]byte) { defer f() }
func DynamicLoopDeferred(f func() [131073]byte) { for i := 0; i < 2; i++ { defer f() } }
`, "131073", strconv.Itoa(target.size)))
			source.ssa.Build()
			prog := newLLSSAProgForTarget(t, target.arch)
			defer prog.Dispose()
			u, err := prepareStacklessEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
				{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
				{SSA: foo.ssa, Files: []*ast.File{foo.file}},
			}, EmissionUniverseOptions{CompleteRuntimeABI: true})
			if err != nil {
				t.Fatal(err)
			}
			sources, err := coro.NewSSAEmissionUniverse(foo.ssa.Prog, u.Functions())
			if err != nil {
				t.Fatal(err)
			}
			ids := u.FunctionIDConfig()
			ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = coro.PhysicalABIV1, coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0, true
			plan, err := coro.AnalyzeSSA(foo.ssa.Prog, coro.Roots{
				{Function: foo.ssa.Func("Root"), Demand: coro.AsyncDemand},
				{Function: foo.ssa.Func("Deferred"), Demand: coro.AsyncDemand},
				{Function: foo.ssa.Func("DynamicDeferred"), Demand: coro.AsyncDemand},
				{Function: foo.ssa.Func("DynamicLoopDeferred"), Demand: coro.AsyncDemand},
			}, coro.SSAConfig{
				EmissionUniverse: sources, FunctionIDs: ids, MaxPlainInstructions: -1,
				OutcomeMode: coro.OutcomeExplicitStatus, ClassifyLocalBody: u.CoroLocalBodyFacts,
				ClassifyLoweredCalls: u.CoroLoweredCalls,
				ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (coro.UnknownTarget, error) {
					return coro.UnknownManagedDispatch, nil
				},
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					if fn == foo.ssa.Func("Acquire") {
						return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
					}
					return coro.SSAFunctionPolicy{}, nil
				},
				ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
					fact, found, err := u.CoroCallSitePlan(call)
					return found && fact.ElidesCall(), err
				},
				ClassifyStaticFunctionReferenceCallArgument: func(_ *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
					return u.CoroStaticFunctionReferenceCallArgument(call, argument)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			report, err := u.BuildCoroLoweringFactsReport(plan)
			if err != nil {
				t.Fatal(err)
			}
			conditionalAllocators := 0
			for _, function := range report.Facts.Functions {
				for _, site := range function.Sites {
					for _, helper := range site.Helpers {
						if strings.HasPrefix(helper.LogicalName, coroManagedFrameSlotAllocZCall+":dynamic:") {
							conditionalAllocators++
							if helper.ManagedDispatch || helper.Target == "" {
								t.Fatalf("conditional allocator lost its exact real target: %+v", helper)
							}
						}
					}
				}
			}
			if conditionalAllocators != 2 {
				t.Fatalf("pointer-free exact conditional allocators=%d, want 2", conditionalAllocators)
			}
			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: u}
			enableCoroChildAwaitCompilation(compilation)
			compilation.PanicABI = coro.PanicExplicitStatusABIV0
			pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, foo.ssa, []*ast.File{foo.file}, goembed.VarMap{}, PackageOptions{Compilation: compilation})
			if err != nil {
				t.Fatal(err)
			}
			module := pkg.Module()
			defer module.Dispose()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("large delivery before split: %v\n%s", err, module.String())
			}
			producer := requireCoroPhysicalFunction(t, module, "foo.Acquire")
			if strings.Contains(producer.String(), "store ["+strconv.Itoa(target.size)+" x i8]") {
				t.Fatalf("borrowed output was copied back into its own physical result sink:\n%s", producer.String())
			}
			allocations := 0
			for _, block := range producer.BasicBlocks() {
				for call := block.FirstInstruction(); !call.IsNil(); call = llvm.NextInstruction(call) {
					if call.InstructionOpcode() != llvm.Call || call.CalledValue().Name() != llssa.PkgRuntime+".AllocZ" {
						continue
					}
					allocations++
					if block == producer.FirstBasicBlock() {
						t.Fatalf("fallback aggregate allocated unconditionally in the ramp:\n%s", producer.String())
					}
					// The allocation block must be selected only when the hidden
					// caller output is nil; a provided output bypasses it entirely.
					guarded := false
					for _, predecessor := range producer.BasicBlocks() {
						branch := predecessor.LastInstruction()
						if branch.InstructionOpcode() != llvm.Br || branch.SuccessorsCount() != 2 || branch.Successor(1) != block {
							continue
						}
						condition := branch.Operand(0)
						guarded = condition.InstructionOpcode() == llvm.ICmp && condition.IntPredicate() == llvm.IntNE && condition.Operand(0) == producer.Param(1) && condition.Operand(1).IsNull()
					}
					if !guarded {
						t.Fatalf("fallback AllocZ lacks exact nil-output branch:\n%s", producer.String())
					}
				}
			}
			if allocations != 1 {
				t.Fatalf("fallback AllocZ count=%d, want one conditional allocation:\n%s", allocations, producer.String())
			}
			for _, name := range []string{"foo.Root", "foo.Deferred"} {
				consumer := requireCoroPhysicalFunction(t, module, name)
				if count := countCoroIRDirectCalls(consumer, llssa.PkgRuntime+".AllocZ"); count != 1 {
					t.Fatalf("%s result allocations=%d, want one planned shared result slot:\n%s", name, count, consumer.String())
				}
			}
			for _, name := range []string{"foo.DynamicDeferred", "foo.DynamicLoopDeferred"} {
				consumer := requireCoroPhysicalFunction(t, module, name)
				withoutDelivery := coroDeliveryTestReachableWithoutCapability(t, consumer)
				allocations := 0
				for _, block := range consumer.BasicBlocks() {
					for call := block.FirstInstruction(); !call.IsNil(); call = llvm.NextInstruction(call) {
						if call.InstructionOpcode() != llvm.Call || call.CalledValue().Name() != llssa.PkgRuntime+".AllocZ" {
							continue
						}
						allocations++
						if block == consumer.FirstBasicBlock() {
							t.Fatalf("%s eagerly allocated a discarded large result in its ramp:\n%s", name, consumer)
						}
						if withoutDelivery[block] {
							t.Fatalf("%s allocation is reachable without the positive delivery capability:\n%s", name, consumer)
						}
					}
				}
				if allocations != 1 || !strings.Contains(consumer.String(), "coro.dispatch.delivery") {
					t.Fatalf("%s lacks one capability-gated custody allocation: allocations=%d\n%s", name, allocations, consumer)
				}
			}
			runCoroABITestPipeline(t, prog, module)
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			if size := coroFrameAllocationSize(t, module.NamedFunction("foo.Acquire$coro"), prog.PointerSize()*8); size >= 128*1024 {
				t.Fatalf("fallback aggregate inflated the coroutine frame to %d bytes", size)
			}
			for _, name := range []string{"foo.DynamicDeferred$coro", "foo.DynamicLoopDeferred$coro"} {
				if size := coroFrameAllocationSize(t, module.NamedFunction(name), prog.PointerSize()*8); size >= 128*1024 {
					t.Fatalf("%s retained a large discarded result in its frame: %d bytes", name, size)
				}
			}
		})
	}
}

func coroDeliveryTestReachableWithoutCapability(t *testing.T, function llvm.Value) map[llvm.BasicBlock]bool {
	t.Helper()
	var positive llvm.BasicBlock
	for _, block := range function.BasicBlocks() {
		branch := block.LastInstruction()
		if branch.InstructionOpcode() != llvm.Br || branch.SuccessorsCount() != 2 {
			continue
		}
		condition := branch.Operand(0)
		if condition.InstructionOpcode() != llvm.ICmp || condition.IntPredicate() != llvm.IntNE || !condition.Operand(1).IsNull() {
			continue
		}
		mask := condition.Operand(0)
		if mask.InstructionOpcode() == llvm.And && !mask.Operand(1).IsAConstantInt().IsNil() &&
			mask.Operand(1).ZExtValue() == uint64(llssa.CoroDispatchFlagHasDelivery) {
			positive = branch.Successor(0)
			break
		}
	}
	if positive.IsNil() {
		t.Fatal("consumer has no positive delivery-capability branch")
	}
	reachable := make(map[llvm.BasicBlock]bool)
	pending := []llvm.BasicBlock{function.FirstBasicBlock()}
	for len(pending) != 0 {
		block := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if reachable[block] || block == positive {
			continue
		}
		reachable[block] = true
		branch := block.LastInstruction()
		for index := 0; index < branch.SuccessorsCount(); index++ {
			pending = append(pending, branch.Successor(index))
		}
	}
	return reachable
}

const coroDeliverySourceFixture = `package foo
import "unsafe"
type Result = struct { R0 int; R1 error }
func initFD(out *Result) { out.R0 = -1; out.R1 = nil }
func dropFD(out *Result, _ unsafe.Pointer, _ string) { out.R0 = -1 }
//go:linkname borrow llgo.coroBorrowResult
func borrow(func(*Result), func(*Result, unsafe.Pointer, string)) *Result
func Acquire(path string) (int, error) {
	out := borrow(initFD, dropFD)
	return out.R0, out.R1
}
func Root() { Acquire("fixture") }
`

func TestCoroDeliveryRejectsBorrowedOutputEscape(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{{name: "native"}, {name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			for _, test := range []struct{ name, declarations, use string }{
				{"global", "var leaked *Result", "leaked = out"},
				{"interior", "var leaked *int", "leaked = &out.R0"},
				{"unsafe", "var leaked unsafe.Pointer", "leaked = unsafe.Pointer(out)"},
				{"interface", "var leaked any", "leaked = out"},
				{"closure", "var leaked func()", "field := &out.R0; leaked = func() { *field = 1 }"},
				{"transitive", "var leaked *Result; func escape(p *Result) { leaked = p }", "escape(out)"},
				{"goroutine", "func mutate(p *Result) { p.R0 = 1 }", "go mutate(out)"},
				{"dynamic", "var observer func(*Result)", "observer(out)"},
				{"staged", "var leaked *Result", "pair := struct { P *Result }{out}; leaked = pair.P"},
			} {
				t.Run(test.name, func(t *testing.T) {
					source := strings.Replace(coroDeliverySourceFixture, "return out.R0, out.R1", test.use+"; return out.R0, out.R1", 1) + "\n" + test.declarations
					pkg, _, files := buildGoSSAPkg(t, source)
					prog := newLLSSAProgForTarget(t, target.arch)
					defer prog.Dispose()
					_, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
					if err == nil || !strings.Contains(err.Error(), "borrowed output address may escape") {
						for _, block := range pkg.Func("Acquire").Blocks {
							for _, instruction := range block.Instrs {
								t.Logf("%T: %s", instruction, instruction)
							}
						}
						t.Fatalf("unproven output loan was accepted: %v", err)
					}
				})
			}
		})
	}
}

func TestCoroDeliveryRejectsHelperOutputEscape(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, helper := range []string{"initFD", "dropFD"} {
		t.Run(helper, func(t *testing.T) {
			source := coroDeliverySourceFixture
			if helper == "initFD" {
				source = strings.Replace(source, "out.R0 = -1; out.R1 = nil", "leaked = out; out.R0 = -1; out.R1 = nil", 1)
			} else {
				source = strings.Replace(source, "_ string) { out.R0 = -1 }", "_ string) { leaked = out; out.R0 = -1 }", 1)
			}
			source += "\nvar leaked *Result"
			pkg, _, files := buildGoSSAPkg(t, source)
			prog := newLLSSAProgForTarget(t, nil)
			defer prog.Dispose()
			_, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
			if err == nil || !strings.Contains(err.Error(), "delivery helper retains borrowed output") {
				t.Fatalf("escaping %s was accepted: %v", helper, err)
			}
		})
	}
}

func TestCoroDeliveryOutputLifetimeUsesFrozenCallSiteContracts(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name, declaration string
		accepted          bool
	}{
		{"managed", "func observe(p *Result) { p.R0 = 7 }", true},
		{"foreign-default", "//go:linkname observe C.delivery_loan_observer\nfunc observe(*Result)", true},
		{"foreign-retained", "//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=retained\n//go:linkname observe C.delivery_loan_observer\nfunc observe(*Result)", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := strings.Replace(coroDeliverySourceFixture, "return out.R0, out.R1", "observe(out); return out.R0, out.R1", 1) + "\n" + test.declaration
			pkg, _, files := buildGoSSAPkg(t, source)
			prog := newLLSSAProgForTarget(t, nil)
			defer prog.Dispose()
			u, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
			if !test.accepted {
				if err == nil || !strings.Contains(err.Error(), "borrowed output address may escape") {
					t.Fatalf("untrusted C lifetime was accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			recipe, present, err := u.coroProgramIR.sourceDelivery(pkg.Func("Acquire"))
			if err != nil || !present || recipe.loan.Value != recipe.borrow || recipe.loan.FunctionsVisited == 0 {
				t.Fatalf("closed logical call lost its output loan: %+v: %v", recipe, err)
			}
		})
	}
}

func TestCoroDeliverySourceFreezesExactTypedHelperIdentities(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			testCoroDeliveryTypedHelperIdentities(t, target.arch, false, false)
		})
	}
}

func TestCoroDeliveryRejectsUnboundDescriptorPublication(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{{name: "native"}, {name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			testCoroDeliveryTypedHelperIdentities(t, target.arch, true, false)
		})
	}
}

func TestCoroDeliveryPublishesGuardedDescriptors(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{{name: "native"}, {name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			testCoroDeliveryTypedHelperIdentities(t, target.arch, true, true)
		})
	}
}

func testCoroDeliveryTypedHelperIdentities(t *testing.T, target *llssa.Target, publish, bound bool) {
	source := coroDeliverySourceFixture
	if publish {
		source = strings.Replace(source, "func Root() { Acquire(\"fixture\") }", "var Published = Acquire\nfunc Root() { Published(\"fixture\") }", 1)
	}
	pkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProgForTarget(t, target)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	producer := pkg.Func("Acquire")
	recipe, found, err := universe.coroProgramIR.sourceDelivery(producer)
	if err != nil || !found || recipe.producer != producer || recipe.initializer != pkg.Func("initFD") ||
		recipe.drop != pkg.Func("dropFD") || recipe.borrow == nil || recipe.result.NumFields() != 2 {
		t.Fatalf("source recipe=%+v present=%t error=%v", recipe, found, err)
	}
	initializerResult := recipe.initializer.Signature.Params().At(0).Type().(*types.Pointer).Elem()
	if !types.Identical(recipe.result, initializerResult) ||
		universe.emissionTypeKeys.strictABI(recipe.result) != universe.emissionTypeKeys.strictABI(initializerResult) {
		t.Fatal("Go-identical canonical and source-expressible result tuples have different strict ABI keys")
	}
	site, frozen, err := universe.CoroCallSitePlan(recipe.borrow)
	if err != nil || !frozen || !site.Intrinsic || !site.ElidesCall() ||
		site.IntrinsicSemantics != CoroIntrinsicCallInlineWithLoweredCalls {
		t.Fatalf("borrow site=%+v frozen=%t error=%v", site, frozen, err)
	}
	for _, owner := range universe.sortedUseOwners(producer) {
		key := emissionFunctionOwnerKey{function: producer, owner: owner}
		frozen := universe.coroProgramIR.sitePlans[key][recipe.borrow]
		if source, cleanup := frozen.managedRuntimeHelpersAt(coroRuntimeHelperAtSource), frozen.managedRuntimeHelpersAt(coroRuntimeHelperAtResultDrop); len(source) != 1 || source[0] != coroDeliveryInitializeCall || len(cleanup) != 1 || cleanup[0] != coroDeliveryDropCall {
			t.Fatalf("borrow fallback helper placement source=%v cleanup=%v", source, cleanup)
		}
	}
	roots, err := universe.intrinsicCallValueRoots(llgoCoroBorrowResult, recipe.borrow.Common().Args, fnNormal)
	if err != nil || len(roots) != 2 || !roots[0].directFunction || !roots[1].directFunction ||
		roots[0].value != recipe.initializer || roots[1].value != recipe.drop {
		t.Fatalf("helper identities became runtime function values: roots=%+v error=%v", roots, err)
	}
	copy := recipe
	copy.drop = pkg.Func("Root")
	if again, _, _ := universe.coroProgramIR.sourceDelivery(producer); again.drop != recipe.drop {
		t.Fatal("source recipe lookup exposed mutable producer state")
	}
	if ordinary, found, err := universe.coroProgramIR.sourceDelivery(pkg.Func("Root")); err != nil || found || ordinary.producer != nil {
		t.Fatal("calling an acquisition inferred resource ownership for an ordinary wrapper")
	}
	if err := universe.coroProgramIR.freezeDeliveryProducer(recipe); err == nil {
		t.Fatal("source recipe remained mutable after source freeze")
	}
	lowered, err := universe.CoroLoweredCalls(producer)
	if err != nil || len(lowered) != 2 ||
		lowered[0].LogicalName != coroDeliveryDropCall || lowered[0].Target != recipe.drop ||
		lowered[1].LogicalName != coroDeliveryInitializeCall || lowered[1].Target != recipe.initializer {
		t.Fatalf("hidden helper calls were not frozen in the ordinary call graph: %+v error=%v", lowered, err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(pkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	var plan *coro.SSAPlan
	planRoots := coro.Roots{{Function: pkg.Func("Root"), Demand: coro.AsyncDemand}}
	if bound {
		planRoots = append(planRoots, coro.Root{Function: pkg.Func("init"), Demand: coro.AsyncDemand})
	}
	for _, asyncHelper := range []*ssa.Function{recipe.initializer, recipe.drop} {
		plan, err = coro.AnalyzeSSA(pkg.Prog, planRoots, coro.SSAConfig{
			EmissionUniverse: ssaUniverse, FunctionIDs: functionIDs, MaxPlainInstructions: -1,
			OutcomeMode:       coro.OutcomeExplicitStatus,
			ClassifyLocalBody: universe.CoroLocalBodyFacts,
			ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if function == asyncHelper {
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, Exec: coro.NeedsRuntimeContext}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
			ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
				site, found, err := universe.CoroCallSitePlan(call)
				return found && site.ElidesCall(), err
			},
			ClassifyStaticFunctionReferenceCallArgument: func(_ *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
				return universe.CoroStaticFunctionReferenceCallArgument(call, argument)
			},
			ClassifyLoweredCalls: universe.CoroLoweredCalls,
			ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (coro.UnknownTarget, error) {
				if bound {
					return coro.UnknownManagedDispatch, nil
				}
				return coro.UnknownManaged, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if publish && !bound {
			fact, found := plan.FunctionPlan(producer)
			if !found || fact.FuncRep != coro.Dispatch {
				t.Fatalf("publication fixture did not demand its producer descriptor: %+v", fact)
			}
			_, err := prepareCoroDeliveryFunctionPlan(producer, plan, universe.coroProgramIR, universe)
			if err == nil || !strings.Contains(err.Error(), "requires a bound physical resource-delivery guard for descriptor publication") {
				t.Fatalf("unbound producer descriptor escaped publication gate: %v", err)
			}
			return
		}
		for _, function := range []*ssa.Function{producer, pkg.Func("Root")} {
			fact, found := plan.FunctionPlan(function)
			if !found || !fact.Effect.Contains(coro.AwaitStructured) || !fact.Exec.Contains(coro.NeedsRuntimeContext) {
				t.Fatalf("hidden %s did not automatically color %s: %+v present=%t", asyncHelper, function, fact, found)
			}
		}
	}
	if recipe.loan.Value != recipe.borrow || recipe.loan.FunctionsVisited == 0 ||
		recipe.helperLoans[0].Function != recipe.initializer || recipe.helperLoans[1].Function != recipe.drop {
		t.Fatal("source recipe lost its exact output/helper lifetime proofs")
	}
	for _, missing := range []string{"producer", "initializer", "drop"} {
		corrupt := *universe.coroProgramIR
		broken := recipe
		switch missing {
		case "producer":
			broken.loan = coro.SSABorrowedValueProof{}
		case "initializer":
			broken.helperLoans[0] = coro.SSABorrowedParameterProof{}
		case "drop":
			broken.helperLoans[1] = coro.SSABorrowedParameterProof{}
		}
		corrupt.deliveryProducers = map[*ssa.Function]coroSourceDeliveryRecipe{producer: broken}
		if _, err := prepareCoroDeliveryFunctionPlan(producer, plan, &corrupt, universe); err == nil || !strings.Contains(err.Error(), "lifetime proof") {
			t.Fatalf("physical preflight accepted missing %s proof: %v", missing, err)
		}
	}
	for _, function := range []*ssa.Function{recipe.initializer, recipe.drop} {
		fact, found := plan.FunctionPlan(function)
		if !found || fact.FuncRep == coro.Dispatch {
			t.Fatalf("compile-time helper reference required a runtime descriptor for %s: %+v", function, fact)
		}
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	metadata := coro.LibraryEffectMetadata{
		FunctionIDSchema: coro.FunctionIDSchema,
		CoroABI:          compilation.CoroABI, SchedulerABI: compilation.SchedulerABI,
		PanicABI: compilation.PanicABI, FuncRepABI: compilation.FuncRepABI,
		TargetTriple: prog.TargetSpec().Triple, PointerBits: prog.PointerSize() * 8,
		Endianness: "little", DataLayout: prog.DataLayout(),
	}
	record, present, err := universe.CoroLibraryEffects().SourceDeliveryRecord(producer, metadata)
	if err != nil || !present || record.ResultSize != uint64(3*prog.PointerSize()) || record.ResultAlign != uint64(prog.PointerSize()) {
		t.Fatalf("source archive recipe=%+v present=%t error=%v", record, present, err)
	}
	for index, function := range []*ssa.Function{producer, recipe.initializer, recipe.drop} {
		fact, _ := plan.FunctionPlan(function)
		if ids := [3]coro.FunctionID{record.Producer, record.Initializer, record.Drop}; ids[index] != fact.ID {
			t.Fatal("archive recipe reconstructed another callable identity")
		}
	}
	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, pkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := compiled.Module()
	defer module.Dispose()
	if bound {
		published := 0
		for global := module.FirstGlobal(); !global.IsNil(); global = llvm.NextGlobal(global) {
			if !strings.HasPrefix(global.Name(), coroPlainDispatchDescriptorPrefix) {
				continue
			}
			initializer := global.Initializer()
			if initializer.OperandsCount() != 2 {
				continue
			}
			base, tail := initializer.Operand(0), initializer.Operand(1)
			if base.OperandsCount() != 9 || base.Operand(1).ZExtValue()&uint64(llssa.CoroDispatchFlagHasDelivery) == 0 ||
				tail.OperandsCount() != 8 || tail.Operand(0).ZExtValue() != uint64(llssa.CoroDispatchDeliveryVersionV2) ||
				tail.Operand(2).IsNull() || tail.Operand(3).IsNull() {
				t.Fatalf("published producer lost its complete guarded tail: %s", global)
			}
			published++
		}
		if published != 1 {
			t.Fatalf("resource descriptor tails=%d, want exactly one", published)
		}
	}
	// Cold delivery metadata reuses the already demanded primary; publishing
	// it must not fabricate a Dispatch value-flow fact or compile a second body.
	helperContext, err := universe.functionABIContext(producer, universe.ownerOf(producer))
	if err != nil {
		t.Fatal(err)
	}
	helperContext.pkg, helperContext.compilation = compiled, compilation
	_, initializeSignature, dropSignature, err := coro.DeliveryHelperSignatures(recipe.signature)
	if err != nil {
		t.Fatal(err)
	}
	for index, helper := range []*ssa.Function{recipe.initializer, recipe.drop} {
		before, _ := plan.FunctionPlan(helper)
		signature := initializeSignature
		if index == 1 {
			signature = dropSignature
		}
		publisherSignature := types.NewSignatureType(nil, nil, nil, nil,
			types.NewTuple(types.NewParam(0, nil, "", types.Typ[types.UnsafePointer])), false)
		publisher := compiled.NewFunc("foo.privateDeliveryHelper."+helper.Name(), publisherSignature, llssa.InGo)
		body := publisher.MakeBody(1)
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("private helper metadata accepted a different typed ABI")
				}
			}()
			wrong := dropSignature
			if index == 1 {
				wrong = initializeSignature
			}
			helperContext.emitCoroDeliveryHelper(body, helper, wrong)
		}()
		value := helperContext.emitCoroDeliveryHelper(body, helper, signature)
		body.Return(body.Field(value.value, 0))
		body.EndBuild()
		body.Dispose()
		if after, _ := plan.FunctionPlan(helper); after != before || after.FuncRep == coro.Dispatch {
			t.Fatalf("private helper metadata changed its static primary plan: before=%+v after=%+v", before, after)
		}
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify guarded source delivery: %v\n%s", err, module.String())
	}
	for _, name := range []string{"foo.Root", "foo.Acquire"} {
		fn := requireCoroPhysicalFunction(t, module, name)
		if countCoroIRDirectCalls(fn, coroRecoverAliasBeginHookV1) != 0 {
			t.Fatalf("resource guard gained source-defer recover permission: %s", fn.String())
		}
		if begins, ends := countCoroIRDirectCalls(fn, coroCleanupShieldBeginHookV1), countCoroIRDirectCalls(fn, coroCleanupShieldEndHookV1); begins != 1 || ends != 2 {
			t.Fatalf("%s finite drop scopes begin=%d end=%d, want one joined internal drop: %s", name, begins, ends, fn.String())
		}
	}
	runCoroABITestPipeline(t, prog, module)
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify split resource guards: %v\n%s", err, module.String())
	}
}

func TestCoroDeliverySourceRejectsUnboundOrInexactShapes(t *testing.T) {
	for _, test := range []struct {
		name, old, replacement, diagnostic string
	}{
		{"defined-result", "type Result = struct", "type Result struct", "exact typed result"},
		{"private-result", "R0", "r0", "exact typed result"},
		{"indirect-init", "borrow(initFD, dropFD)", "borrow(initializer, dropFD)", "exact context-free function"},
		{"captured-init", "borrow(initFD, dropFD)", "borrow(func(out *Result) { out.R0 = len(path) }, dropFD)", "exact context-free function"},
		{"non-entry", "out := borrow", "if path == \"\" { return 0, nil }; out := borrow", "source entry block"},
		{"duplicate", "out := borrow(initFD, dropFD)", "out := borrow(initFD, dropFD); _ = borrow(initFD, dropFD)", "conflicting or repeated"},
		{"foreign-drop", "func dropFD", "//go:linkname dropFD C.llgo_test_delivery_drop\nfunc dropFD", "managed Go helper"},
		{"invented-fd", "return out.R0, out.R1", "return 0, out.R1", "load each exact borrowed result field"},
		{"changed-fd", "return out.R0, out.R1", "return out.R0 + 1, out.R1", "load each exact borrowed result field"},
		{"invented-error", "return out.R0, out.R1", "return out.R0, nil", "load each exact borrowed result field"},
		{"stale-fd", "return out.R0, out.R1", "fd := out.R0; out.R0 = -1; return fd, out.R1", "followed only by the remaining projections"},
		{"await-after-load", "return out.R0, out.R1", "fd := out.R0; dropFD(out, nil, path); return fd, out.R1", "followed only by the remaining projections"},
		{"cross-block-load", "return out.R0, out.R1", "fd := out.R0; if path == \"\" { return fd, out.R1 }; return out.R0, out.R1", "load each exact borrowed result field"},
		{"call-before-borrow", "out := borrow", "initFD(nil); out := borrow", "borrow must precede source calls"},
		{"defer-before-borrow", "out := borrow", "defer initFD(nil); out := borrow", "borrow must precede source calls"},
		{"go-before-borrow", "out := borrow", "go initFD(nil); out := borrow", "borrow must precede source calls"},
		{"cleanup-after-load", "return out.R0, out.R1", "defer dropFD(out, nil, path); return out.R0, out.R1", "exact borrowed result field"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := strings.ReplaceAll(coroDeliverySourceFixture, test.old, test.replacement)
			if test.name == "indirect-init" {
				source += "\nvar initializer = initFD\n"
			}
			pkg, _, files := buildGoSSAPkg(t, source)
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
			if err == nil {
				// Shape diagnostics are frozen on the exact call rather than
				// rediscovered by a later analysis or physical emitter.
				for _, block := range pkg.Func("Acquire").Blocks {
					for _, instruction := range block.Instrs {
						call, ok := instruction.(*ssa.Call)
						if !ok || call.Common().StaticCallee() != pkg.Func("borrow") {
							continue
						}
						_, _, err = universe.CoroCallSitePlan(call)
						if err != nil {
							break
						}
					}
					if err != nil {
						break
					}
				}
			}
			if err == nil || !strings.Contains(err.Error(), test.diagnostic) {
				t.Fatalf("diagnostic=%v, want %q", err, test.diagnostic)
			}
		})
	}
}

func TestCoroDeliveryConsumerFactsAreLocalAndFrozen(t *testing.T) {
	source := coroDeliverySourceFixture + `
func Twice() { Acquire("first"); Acquire("second") }
func SpawnOnly() { go Acquire("spawn") }
func Deferred() { defer Acquire("defer") }
func OrdinaryWrapper() { Root() }
func Dead() { if false { Acquire("dead") } }
func Unrelated() {}
`
	pkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	u, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		cleanup bool
		helpers int
	}{
		{"Acquire", true, 2}, // Discarded-entry fallback is independently owned.
		{"Root", true, 2},
		{"Twice", true, 2}, // Helper identity, not one edge per source occurrence.
		{"Deferred", true, 2},
		{"SpawnOnly", false, 0},
		{"OrdinaryWrapper", false, 0}, // Local cleanup ownership never propagates.
		{"Dead", false, 0},
		{"Unrelated", false, 0},
	} {
		fn := pkg.Func(test.name)
		facts, err := u.CoroLocalBodyFacts(fn)
		if err != nil || facts.Exec.Contains(coro.NeedsCleanupFrame) != test.cleanup {
			t.Errorf("%s cleanup=%t, want %t; error=%v", test.name, facts.Exec.Contains(coro.NeedsCleanupFrame), test.cleanup, err)
		}
		if test.cleanup && (facts.OutcomePlainLeaf || facts.OutcomePlainDAG || facts.StaticOutcomeLocal) {
			t.Errorf("%s implicit cleanup retained a frame-free body candidate: %+v", test.name, facts)
		}
		edges, err := u.CoroLoweredCalls(fn)
		if err != nil || len(edges) != test.helpers {
			t.Errorf("%s hidden helper edges=%+v, want %d; error=%v", test.name, edges, test.helpers, err)
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common().StaticCallee() != pkg.Func("Acquire") {
					continue
				}
				frozen, present, err := u.coroProgramIR.callSitePlan(call)
				if test.name == "Dead" {
					if err != nil || !present || !frozen.plan.ElidesCall() || frozen.deliveryProducer != nil {
						t.Fatalf("dead acquisition acquired a delivery obligation: %+v error=%v", frozen, err)
					}
					continue
				}
				if err != nil || !present || frozen.deliveryProducer != pkg.Func("Acquire") {
					t.Fatalf("%s exact consumer producer=%v present=%t error=%v", test.name, frozen.deliveryProducer, present, err)
				}
				recipe, found, err := u.coroProgramIR.sourceDelivery(frozen.deliveryProducer)
				if err != nil || !found || recipe.initializer != pkg.Func("initFD") || recipe.drop != pkg.Func("dropFD") {
					t.Fatalf("%s consumer lost its shared producer recipe: %+v found=%t error=%v", test.name, recipe, found, err)
				}
				for _, owner := range u.sortedUseOwners(fn) {
					key := emissionFunctionOwnerKey{function: fn, owner: owner}
					site := u.coroProgramIR.sitePlans[key][instruction]
					if !sameCoroFrozenCallSitePlan(site.callPlan, frozen) {
						t.Fatal("consumer and owner SitePlan disagree about one delivery recipe")
					}
					source, cleanup := site.managedRuntimeHelpersAt(coroRuntimeHelperAtSource), site.managedRuntimeHelpersAt(coroRuntimeHelperAtResultDrop)
					switch test.name {
					case "SpawnOnly":
						if len(source) != 0 || len(cleanup) != 0 {
							t.Fatalf("spawn executed entry fallback on parent: source=%v cleanup=%v", source, cleanup)
						}
					case "Deferred":
						initializers := site.managedRuntimeHelpersAt(coroRuntimeHelperAtCleanup)
						if len(source) != 0 || len(cleanup) != 1 || cleanup[0] != recipe.consumerDrop || len(initializers) != 1 || initializers[0] != recipe.consumerInitialize {
							t.Fatalf("deferred delivery placement source=%v cleanup=%v", source, cleanup)
						}
					default:
						if len(source) != 1 || source[0] != recipe.consumerInitialize || len(cleanup) != 1 || cleanup[0] != recipe.consumerDrop {
							t.Fatalf("ordinary delivery placement source=%v cleanup=%v", source, cleanup)
						}
					}
				}
			}
		}
	}
	if err := (coroProgramIRBuilder{canonical: emissionCanonicalIndex{universe: u}}).freezeDeliveryConsumers(u.coroProgramIR); err == nil {
		t.Fatal("delivery consumers remained mutable after source freeze")
	}
}

func TestCoroDeliverySourceAcceptsFreshProjectionsAfterOrdinaryWork(t *testing.T) {
	for _, body := range []string{
		"dropFD(out, nil, path); return out.R0, out.R1",
		"if path == \"\" { out.R0 = 0; return out.R0, out.R1 }; out.R0 = 1; return out.R0, out.R1",
	} {
		pkg, _, files := buildGoSSAPkg(t, strings.Replace(coroDeliverySourceFixture, "return out.R0, out.R1", body, 1))
		prog := newLLSSAProg(t)
		universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
		if err == nil {
			_, present, lookupErr := universe.coroProgramIR.sourceDelivery(pkg.Func("Acquire"))
			if lookupErr != nil || !present {
				t.Errorf("fresh borrowed-result projection was not frozen: present=%t error=%v", present, lookupErr)
			}
		} else {
			t.Errorf("ordinary producer work rejected: %v", err)
		}
		prog.Dispose()
	}
}
