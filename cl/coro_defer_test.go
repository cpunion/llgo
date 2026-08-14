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
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroStaticCleanupIRFixture = `package foo
var Sink uint32
var PanicPayload uint32

type Guard struct{}

func First(value uint32) { Sink = Sink*10 + value }
func Second(value uint32) { Sink = Sink*10 + value }
func (*Guard) Third(value uint32) { Sink = Sink*10 + value }
func Variadic(values ...uint32) { Sink = Sink*10 + uint32(len(values)) + values[0] }

func Root(guard *Guard, mode uint32, values []uint32) {
	defer First(1)
	defer Second(mode + 2)
	defer guard.Third(mode + 3)
	defer Variadic(values...)
	if mode == 10 { defer panic(&PanicPayload) }
	if mode == 9 { panic(&PanicPayload) }
}
`

const coroCapturedStaticCleanupIRFixture = `package foo
var Sink uint32

func Root(value uint32) {
	defer func(add uint32) { Sink = value + add }(7)
}
`

const coroEscapingCapturedStaticCleanupIRFixture = `package foo
var Sink uint32

func Observe(callback func(uint32)) {
	if callback == nil {
		Sink++
	}
}

func Root(value uint32) {
	cleanup := func(add uint32) { Sink = value + add }
	if value != 0 {
		Observe(cleanup)
	}
	defer cleanup(7)
}
`

const coroDynamicCleanupIRFixture = `package foo
var Sink uint32

func Cleanup(value uint32) { Sink = Sink*10 + value }
type CleanupCallback func(uint32)
var CleanupFunc CleanupCallback = Cleanup

func Root(limit uint32) {
	defer Cleanup(99)
	for value := uint32(0); value < limit; value++ {
		defer CleanupFunc(value)
	}
}
`

func TestCoroStaticCleanupSharedReturnShape(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func Unlock() {}
func Root(locked, ok bool) (swapped bool) {
	if locked { defer Unlock() }
	if !ok { return false }
	return true
}
`)
	root := ssaPkg.Func("Root")
	found := 0
	for _, block := range root.Blocks {
		for index, instruction := range block.Instrs {
			if _, ok := instruction.(*ssa.RunDefers); !ok {
				continue
			}
			found++
			if !coroStaticRunDefersReturns(block, index) {
				t.Fatalf("RunDefers block=%d does not accept the exact named-result reload tail: instructions=%v successors=%v", block.Index, block.Instrs, block.Succs)
			}
		}
	}
	if found != 2 {
		t.Fatalf("shared-return fixture RunDefers count = %d, want 2", found)
	}
}

func TestCoroStaticCleanupPanicOnlyShapeNeedsNoRunDefers(t *testing.T) {
	const source = `package foo
func cleanup() {}
func Root() {
	defer cleanup()
	panic("sentinel")
}
`
	prog, universe, plan, root, _ := buildCoroStaticCleanupPlanFixture(t, source)
	defer prog.Dispose()

	runDefers := 0
	normalReturns := 0
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			switch instruction.(type) {
			case *ssa.RunDefers:
				runDefers++
			case *ssa.Return:
				if block != root.Recover {
					normalReturns++
				}
			}
		}
	}
	if runDefers != 0 || normalReturns != 0 || root.Recover == nil {
		t.Fatalf("panic-only SSA shape: RunDefers=%d normal Returns=%d Recover=%v", runDefers, normalReturns, root.Recover)
	}
	if coroStaticCleanupHasReachableNormalReturn(root) {
		t.Fatal("panic-only cleanup was classified as having a reachable normal Return")
	}
	cleanup, err := prepareCoroStaticCleanupPlan(root, plan, universe, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup == nil || len(cleanup.sites) != 1 {
		t.Fatalf("panic-only cleanup plan = %+v", cleanup)
	}
}

func TestCoroStaticCleanupOrderIgnoresConstantUnreachableDefer(t *testing.T) {
	const source = `package foo
func live() {}
func dead() {}
func Root() {
	defer live()
	if false {
		defer dead()
	}
}
`
	prog, universe, plan, root, _ := buildCoroStaticCleanupPlanFixture(t, source)
	defer prog.Dispose()

	reachable := coroPhysicalConstantReachableBlocks(root)
	allDefers, reachableDefers := 0, 0
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if _, ok := instruction.(*ssa.Defer); ok {
				allDefers++
				if reachable[block] {
					reachableDefers++
				}
			}
		}
	}
	if allDefers != 2 || reachableDefers != 1 {
		t.Fatalf("constant-unreachable defer shape = all:%d reachable:%d", allDefers, reachableDefers)
	}
	cleanup, err := prepareCoroStaticCleanupPlan(root, plan, universe, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup == nil || len(cleanup.sites) != 1 || cleanup.sites[0].target == nil || cleanup.sites[0].target.Name() != "live" {
		t.Fatalf("constant-unreachable cleanup plan = %+v", cleanup)
	}
}

func TestCoroStaticCleanupIRNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, root := compileCoroStaticCleanupIRFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			rootPlan, ok := plan.FunctionPlan(root)
			if !ok || rootPlan.Emission != coro.EmitCoroutine ||
				!rootPlan.Exec.Contains(coro.NeedsCleanupFrame) || !rootPlan.Effect.Contains(coro.AwaitStructured) {
				t.Fatalf("Root cleanup plan = %+v, present=%t", rootPlan, ok)
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify static cleanup before CoroSplit: %v\n%s", err, module.String())
			}
			body := requireCoroPhysicalFunction(t, module, "foo.Root").String()
			for _, symbol := range []string{"foo.First$coro", "foo.Second$coro", "Third$coro", "foo.Variadic$coro"} {
				if got := strings.Count(body, symbol); got != 1 {
					t.Fatalf("Root cleanup references %s = %d, want one shared guarded call site:\n%s", symbol, got, body)
				}
			}
			for _, forbidden := range []string{"Sigsetjmp", "SetThreadDefer", "GetThreadDefer", "runtime.RunDefers"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("stackless cleanup retained legacy defer machinery %q:\n%s", forbidden, body)
				}
			}
			if !strings.Contains(body, "switch i32") || strings.Count(body, "alloca i1") < 4 ||
				strings.Count(body, "store i1 false") < 4 || strings.Count(body, "store i1 true") < 4 {
				t.Fatalf("static cleanup frame/continuation state is incomplete:\n%s", body)
			}
			if strings.Count(body, "call void @"+coroPanicPrepareHookV1) != 1 ||
				strings.Count(body, "call void @"+coroCompletePrepareHookV2) != 1 {
				t.Fatalf("panic and completion do not share the cleanup drainer:\n%s", body)
			}
			if got := strings.Count(body, "call void @"+coroPanicTraceReplaceHookV1); got != 1 {
				t.Fatalf("cleanup-local panic trace replacement calls = %d, want one:\n%s", got, body)
			}

			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.Root$coro.resume")
			if resume.IsNil() {
				t.Fatalf("CoroSplit did not create Root cleanup resume entry:\n%s", module.String())
			}
			post := resume.String()
			for _, symbol := range []string{"foo.First$coro", "foo.Second$coro", "Third$coro", "foo.Variadic$coro"} {
				if got := strings.Count(post, symbol); got != 1 {
					t.Fatalf("post-split cleanup references %s = %d, want one:\n%s", symbol, got, post)
				}
			}
			if got := strings.Count(post, "call void @"+coroPanicTraceReplaceHookV1); got != 1 {
				t.Fatalf("post-split cleanup-local panic trace replacement calls = %d, want one:\n%s", got, post)
			}
		})
	}
}

func TestCoroCapturedStaticCleanupIRNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, root, closure, target := compileCoroCapturedStaticCleanupFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			rootPlan, rootOK := plan.FunctionPlan(root)
			targetPlan, targetOK := plan.FunctionPlan(target)
			if !rootOK || rootPlan.Emission != coro.EmitCoroutine ||
				!rootPlan.Exec.Contains(coro.NeedsCleanupFrame) || !rootPlan.Effect.Contains(coro.AwaitStructured) {
				t.Fatalf("captured cleanup root plan = %+v, present=%t", rootPlan, rootOK)
			}
			if !targetOK || targetPlan.Emission != coro.EmitCoroutine || targetPlan.FuncRep != coro.DirectCoro {
				t.Fatalf("captured cleanup target plan = %+v, present=%t", targetPlan, targetOK)
			}
			cleanup, err := prepareCoroStaticCleanupPlan(root, plan, nil, "", true)
			if err != nil {
				t.Fatal(err)
			}
			if cleanup == nil || len(cleanup.sites) != 1 || cleanup.sites[0].closure != closure ||
				cleanup.sites[0].target != target || cleanup.sites[0].kind != coroStaticCleanupCoroutine {
				t.Fatalf("captured static cleanup plan = %+v", cleanup)
			}

			assertCoroCapturedCleanupCall(t, requireCoroPhysicalFunction(t, module, "foo.Root"), target.String(), true)
			physicalTarget := requireCoroPhysicalFunction(t, module, target.String())
			if got := physicalTarget.ParamsCount(); got != 4 {
				t.Fatalf("captured cleanup physical parameters = %d, want (g,out,ctx,add)", got)
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify captured cleanup before CoroSplit: %v\n%s", err, module.String())
			}
			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.Root$coro.resume")
			if resume.IsNil() {
				t.Fatalf("CoroSplit did not create captured cleanup resume entry:\n%s", module.String())
			}
			assertCoroCapturedCleanupCall(t, resume, target.String(), false)
		})
	}
}

func TestCoroEscapingCapturedStaticCleanupRetainsDirectDeferIRNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, root, closure, target := compileCoroCapturedStaticCleanupFixtureSource(
				t, test.target, coroEscapingCapturedStaticCleanupIRFixture,
			)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			targetPlan, targetOK := plan.FunctionPlan(target)
			valuePlan, valueOK := plan.ValuePlan(closure)
			var deferred *ssa.Defer
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					candidate, ok := instruction.(*ssa.Defer)
					if ok && candidate.Common().Value == closure {
						deferred = candidate
						break
					}
				}
			}
			callPlan, callOK := plan.CallPlan(deferred)
			if !targetOK || targetPlan.Emission != coro.EmitCoroutine ||
				targetPlan.FuncRep != coro.Dispatch {
				t.Fatalf("escaping cleanup target plan = %+v, present=%t", targetPlan, targetOK)
			}
			if !valueOK || len(valuePlan.Funcs) != 1 ||
				valuePlan.Funcs[0].Rep != coro.Dispatch ||
				valuePlan.Funcs[0].Transport != coro.ManagedTransport {
				t.Fatalf("escaping cleanup value plan = %+v, present=%t", valuePlan, valueOK)
			}
			if deferred == nil || !callOK || callPlan.Rep != coro.DirectCoro ||
				callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 ||
				callPlan.Targets[0] != targetPlan.ID {
				t.Fatalf("escaping cleanup defer call plan = %+v, present=%t", callPlan, callOK)
			}
			cleanup, err := prepareCoroStaticCleanupPlan(root, plan, nil, "", true)
			if err != nil {
				t.Fatal(err)
			}
			if cleanup == nil || len(cleanup.sites) != 1 ||
				cleanup.sites[0].closure != closure ||
				cleanup.sites[0].kind != coroStaticCleanupCoroutine {
				t.Fatalf("escaping captured cleanup plan = %+v", cleanup)
			}
			if !strings.Contains(module.String(), coroPlainDispatchDescriptorPrefix) {
				t.Fatalf("escaping captured cleanup did not publish its descriptor:\n%s", module.String())
			}
			assertCoroCapturedCleanupCall(
				t, requireCoroPhysicalFunction(t, module, "foo.Root"), target.String(), true,
			)
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify escaping captured cleanup before CoroSplit: %v\n%s", err, module.String())
			}
			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.Root$coro.resume")
			if resume.IsNil() {
				t.Fatalf("CoroSplit did not create escaping cleanup resume entry:\n%s", module.String())
			}
			assertCoroCapturedCleanupCall(t, resume, target.String(), false)
		})
	}
}

func TestCoroDynamicCleanupLIFOIRNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, root := compileCoroDynamicCleanupFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			rootPlan, ok := plan.FunctionPlan(root)
			if !ok || rootPlan.Emission != coro.EmitCoroutine ||
				!rootPlan.Exec.Contains(coro.NeedsCleanupFrame) || !rootPlan.Exec.Contains(coro.NeedsPreempt) ||
				!rootPlan.Effect.Contains(coro.AwaitStructured) {
				t.Fatalf("Root dynamic cleanup plan = %+v, present=%t", rootPlan, ok)
			}
			cleanup, err := prepareCoroStaticCleanupPlan(root, plan, nil, "", true)
			if err != nil {
				t.Fatal(err)
			}
			if cleanup == nil || !cleanup.dynamic || cleanup.dynamicTrigger == nil || len(cleanup.sites) != 2 ||
				cleanup.dynamicAlloc == nil || cleanup.dynamicFree == nil {
				t.Fatalf("dynamic cleanup data model = %+v", cleanup)
			}
			for index, site := range cleanup.sites {
				if site == nil || site.tag != uint32(index+1) {
					t.Fatalf("dynamic cleanup site %d = %+v, want stable tag %d", index, site, index+1)
				}
			}
			if cleanup.sites[1].kind != coroStaticCleanupDispatch || cleanup.sites[1].descriptor == nil ||
				cleanup.sites[1].callPlan.Rep != coro.Dispatch || cleanup.sites[1].callPlan.Transport != coro.ManagedTransport {
				t.Fatalf("loop cleanup site is not one frozen managed descriptor record: %+v", cleanup.sites[1])
			}
			if err := validateCoroDynamicCleanupHelpers(cleanup, plan); err != nil {
				t.Fatalf("dynamic cleanup helper certificate: %v", err)
			}

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify dynamic cleanup before CoroSplit: %v\n%s", err, module.String())
			}
			body := requireCoroPhysicalFunction(t, module, "foo.Root").String()
			for _, required := range []string{
				"AllocU", "FreeDeferNode", "switch i32", "foo.Cleanup$coro",
				"llvm.coro.promise", coroAwaitPrepareHookV1, coroFaultPayloadHookV1,
			} {
				if !strings.Contains(body, required) {
					t.Fatalf("dynamic cleanup body lacks %q:\n%s", required, body)
				}
			}
			if !strings.Contains(module.String(), coroPlainDispatchDescriptorPrefix) {
				t.Fatalf("dynamic cleanup module lacks the descriptor producer:\n%s", module.String())
			}
			for _, forbidden := range []string{"Sigsetjmp", "SetThreadDefer", "GetThreadDefer", "runtime.RunDefers"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("dynamic stackless cleanup retained legacy defer machinery %q:\n%s", forbidden, body)
				}
			}
			if got := strings.Count(body, "AllocU"); got != 2 {
				t.Fatalf("dynamic cleanup AllocU sites = %d, want one per static defer site:\n%s", got, body)
			}
			if got := strings.Count(body, "FreeDeferNode"); got != 2 {
				t.Fatalf("dynamic cleanup FreeDeferNode sites = %d, want one per dispatch site:\n%s", got, body)
			}

			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.Root$coro.resume")
			if resume.IsNil() || !strings.Contains(resume.String(), "FreeDeferNode") ||
				!strings.Contains(resume.String(), "foo.Cleanup$coro") {
				t.Fatalf("post-split dynamic cleanup lost its pop/free/await loop:\n%s", module.String())
			}
		})
	}
}

func assertCoroCapturedCleanupCall(t *testing.T, function llvm.Value, target string, requireContextLoad bool) {
	t.Helper()
	var call llvm.Value
	for _, block := range function.BasicBlocks() {
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if instruction.InstructionOpcode() != llvm.Call || instruction.CalledValue().Name() != target+"$coro" {
				continue
			}
			if !call.IsNil() {
				t.Fatalf("%s invokes captured cleanup %q more than once:\n%s", function.Name(), target, function.String())
			}
			call = instruction
		}
	}
	if call.IsNil() {
		t.Fatalf("%s does not invoke captured cleanup %q:\n%s", function.Name(), target, function.String())
	}
	// (g, out, ctx, add) plus LLVM's called-value operand. The context is the
	// exact environment loaded from the registration record, never a nil marker
	// used by context-free static cleanup.
	if got := call.OperandsCount() - 1; got != 4 {
		t.Fatalf("captured cleanup call arguments = %d, want 4:\n%s", got, call.String())
	}
	context := call.Operand(2)
	if !context.IsAConstantPointerNull().IsNil() || context.IsUndef() {
		t.Fatalf("captured cleanup call received an absent context:\n%s", call.String())
	}
	if requireContextLoad && context.InstructionOpcode() != llvm.Load {
		t.Fatalf("captured cleanup context is not loaded from its registration slot:\n%s", call.String())
	}
	if got := countCoroIRDirectCalls(function, coroAwaitPrepareHookV1); got != 1 {
		t.Fatalf("%s captured cleanup await_prepare calls = %d, want 1:\n%s", function.Name(), got, function.String())
	}
}

const coroAwaitCompletionCleanupFixture = `package foo
var Sink uint32

func Cleanup(value uint32) { Sink = value }
func Child(value uint32) uint32 { return value + 1 }

func Parent(value uint32) {
	defer Cleanup(value)
	Sink = Child(value)
}
`

func TestCoroAwaitCompletionDrainsParentCleanupNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, parent, child := compileCoroAwaitCompletionCleanupFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			parentPlan, parentOK := plan.FunctionPlan(parent)
			childPlan, childOK := plan.FunctionPlan(child)
			if !parentOK || parentPlan.Emission != coro.EmitCoroutine ||
				!parentPlan.Exec.Contains(coro.NeedsCleanupFrame) || !parentPlan.Effect.Contains(coro.AwaitStructured) {
				t.Fatalf("Parent completion/cleanup plan = %+v, present=%t", parentPlan, parentOK)
			}
			if !childOK || childPlan.Emission != coro.EmitCoroutine || childPlan.FuncRep != coro.DirectCoro {
				t.Fatalf("Child completion plan = %+v, present=%t", childPlan, childOK)
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify parent-owned completion before CoroSplit: %v\n%s", err, module.String())
			}
			parentRamp := requireCoroPhysicalFunction(t, module, "foo.Parent")
			assertCoroAwaitCompletionCleanupControlFlow(t, parentRamp, true)

			runCoroABITestPipeline(t, prog, module)
			parentResume := module.NamedFunction("foo.Parent$coro.resume")
			if parentResume.IsNil() {
				t.Fatalf("CoroSplit did not create Parent completion/cleanup resume:\n%s", module.String())
			}
			assertCoroAwaitCompletionCleanupControlFlow(t, parentResume, false)
			for _, name := range []string{"foo.Parent$coro", "foo.Parent$coro.destroy"} {
				function := module.NamedFunction(name)
				if function.IsNil() {
					t.Fatalf("CoroSplit did not retain %q:\n%s", name, module.String())
				}
				if functionHasReachableDirectCall(function, coroAwaitConsumeHookV1) {
					t.Fatalf("parent completion is consumed outside the resume entry %q:\n%s", name, function.String())
				}
			}
		})
	}
}

func assertCoroAwaitCompletionCleanupControlFlow(t *testing.T, function llvm.Value, presplit bool) {
	t.Helper()
	if function.IsNil() {
		t.Fatal("cannot inspect nil parent completion function")
	}
	body := function.String()
	await := strings.Index(body, "call void @"+coroAwaitPrepareHookV1)
	if await < 0 {
		t.Fatalf("%s has no parent-owned child await preparation:\n%s", function.Name(), body)
	}
	if presplit && !strings.Contains(body[await:], "call i8 @llvm.coro.suspend") {
		t.Fatalf("%s consumes child completion before the await suspension/resume edge:\n%s", function.Name(), body)
	}
	for _, forbidden := range []string{"runtime.Panic", "runtime.RunDefers", "Sigsetjmp", "SetThreadDefer", "GetThreadDefer"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("%s child panic outcome retained legacy unwind %q:\n%s", function.Name(), forbidden, body)
		}
	}

	var normalConsume, canceledConsume, dispatch, canceledDispatch llvm.Value
	for _, block := range function.BasicBlocks() {
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if instruction.InstructionOpcode() != llvm.Call || instruction.CalledValue().Name() != coroAwaitConsumeHookV1 {
				continue
			}
			terminator := block.LastInstruction()
			if !terminator.IsNil() && terminator.InstructionOpcode() == llvm.Switch && terminator.Operand(0) == instruction &&
				!coroTestBlockStoresI32(block, coroStaticCleanupContinueComplete) {
				if !normalConsume.IsNil() {
					t.Fatalf("%s has multiple normal child completion dispatches:\n%s", function.Name(), body)
				}
				normalConsume, dispatch = instruction, terminator
				continue
			}
			if !terminator.IsNil() && terminator.InstructionOpcode() == llvm.Switch && terminator.Operand(0) == instruction &&
				coroTestBlockStoresI32(block, coroStaticCleanupContinueComplete) {
				if !canceledConsume.IsNil() {
					t.Fatalf("%s has multiple canceled child reconciliation paths:\n%s", function.Name(), body)
				}
				canceledConsume, canceledDispatch = instruction, terminator
				continue
			}
			t.Fatalf("%s child completion consume is not status-dispatched into cleanup:\n%s", function.Name(), body)
		}
	}
	if normalConsume.IsNil() || canceledConsume.IsNil() || dispatch.IsNil() || canceledDispatch.IsNil() {
		t.Fatalf("%s lacks distinct normal/canceled child completion reconciliation:\n%s", function.Name(), body)
	}
	gateFound := false
	inlineFound := false
	normalBlock, canceledBlock := normalConsume.InstructionParent(), canceledConsume.InstructionParent()
	for _, block := range function.BasicBlocks() {
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if instruction.InstructionOpcode() != llvm.Call {
				continue
			}
			terminator := block.LastInstruction()
			if terminator.IsNil() || terminator.InstructionOpcode() != llvm.Br || terminator.SuccessorsCount() != 2 {
				continue
			}
			first := coroTestFirstReachableAwaitConsumes(terminator.Successor(0), normalBlock, canceledBlock)
			second := coroTestFirstReachableAwaitConsumes(terminator.Successor(1), normalBlock, canceledBlock)
			switch instruction.CalledValue().Name() {
			case coroRunDecisionTakeZeroHookV1:
				if first == coroTestAwaitConsumeNormal && second == coroTestAwaitConsumeCanceled ||
					first == coroTestAwaitConsumeCanceled && second == coroTestAwaitConsumeNormal {
					gateFound = true
				}
			case coroAwaitInlineFinishHookV2:
				// The inline-complete edge reaches only normal consume. The
				// suspend edge re-enters the run-decision gate and can first
				// reach cancellation as well as the normal continuation.
				if first == coroTestAwaitConsumeNormal && second&coroTestAwaitConsumeCanceled != 0 ||
					second == coroTestAwaitConsumeNormal && first&coroTestAwaitConsumeCanceled != 0 {
					inlineFound = true
				}
			}
		}
	}
	if !gateFound {
		t.Fatalf("%s normal/canceled consumes are not mutually exclusive resumed run-decision successors:\n%s", function.Name(), body)
	}
	if !inlineFound {
		t.Fatalf("%s inline completion does not bypass only the resumed cancellation gate:\n%s", function.Name(), body)
	}
	var returned, panicked, aborted, shutdown, goexited llvm.BasicBlock
	for successor := 1; successor < dispatch.SuccessorsCount(); successor++ {
		switch dispatch.GetSwitchCaseValue(successor).ZExtValue() {
		case coroAwaitCompletionReturn:
			returned = dispatch.Successor(successor)
		case coroAwaitCompletionPanic:
			panicked = dispatch.Successor(successor)
		case coroAwaitCompletionAbort:
			aborted = dispatch.Successor(successor)
		case coroAwaitCompletionShutdown:
			shutdown = dispatch.Successor(successor)
		case coroAwaitCompletionGoexit:
			goexited = dispatch.Successor(successor)
		}
	}
	if returned.IsNil() || panicked.IsNil() || aborted.IsNil() || shutdown.IsNil() || goexited.IsNil() ||
		returned == panicked || returned == aborted || returned == shutdown || panicked == aborted ||
		returned == goexited || panicked == shutdown || panicked == goexited || aborted == shutdown ||
		aborted == goexited || shutdown == goexited {
		t.Fatalf("%s completion switch lacks distinct Return/Panic/Abort/Shutdown/Goexit cases:\n%s", function.Name(), body)
	}
	if !coroTestBlockLoadsI32(returned) || !coroTestBlockStoresGlobal(returned, "foo.Sink") {
		t.Fatalf("%s Return completion does not load and commit the child result:\n%s", function.Name(), returned.AsValue().String())
	}
	if coroTestBlockLoadsI32(panicked) || coroTestBlockStoresGlobal(panicked, "foo.Sink") {
		t.Fatalf("%s Panic completion incorrectly reads or commits the child result:\n%s", function.Name(), panicked.AsValue().String())
	}
	for _, terminal := range []struct {
		name   string
		block  llvm.BasicBlock
		status uint32
	}{
		{name: "Abort", block: aborted, status: uint32(coroAwaitCompletionAbort)},
		{name: "Shutdown", block: shutdown, status: uint32(coroAwaitCompletionShutdown)},
		{name: "Goexit", block: goexited, status: uint32(coroAwaitCompletionGoexit)},
	} {
		if coroTestBlockLoadsI32(terminal.block) || coroTestBlockStoresGlobal(terminal.block, "foo.Sink") ||
			!coroTestBlockStoresI32(terminal.block, terminal.status) ||
			!coroTestBlockStoresI32(terminal.block, coroStaticCleanupContinueComplete) ||
			!coroTestBlockCanReachDirectCall(terminal.block, "foo.Cleanup") {
			t.Fatalf("%s %s completion does not become a cleanup base without reading child results:\n%s",
				function.Name(), terminal.name, terminal.block.AsValue().String())
		}
	}
	if !coroTestBlockStoresI32(returned, coroStaticCleanupContinueFirstRun) ||
		!coroTestBlockStoresI32(panicked, coroStaticCleanupContinueRecover) {
		t.Fatalf("%s Return/Panic outcomes do not select RunDefers/Panic cleanup continuations:\nReturn:\n%s\nPanic:\n%s",
			function.Name(), returned.AsValue().String(), panicked.AsValue().String())
	}
	returnedTerminator, panickedTerminator := returned.LastInstruction(), panicked.LastInstruction()
	if returnedTerminator.InstructionOpcode() != llvm.Br || returnedTerminator.SuccessorsCount() != 1 ||
		panickedTerminator.InstructionOpcode() != llvm.Br || panickedTerminator.SuccessorsCount() != 1 ||
		returnedTerminator.Successor(0) != panickedTerminator.Successor(0) {
		t.Fatalf("%s Return/Panic completion cases do not enter the shared cleanup drainer:\nReturn:\n%s\nPanic:\n%s",
			function.Name(), returned.AsValue().String(), panicked.AsValue().String())
	}
	drainer := returnedTerminator.Successor(0)
	if !coroTestBlockCanReachDirectCall(drainer, "foo.Cleanup") {
		t.Fatalf("%s shared completion join cannot reach the static defer drainer:\n%s", function.Name(), body)
	}
	for successor := 1; successor < canceledDispatch.SuccessorsCount(); successor++ {
		if !coroTestBlockCanReachDirectCall(canceledDispatch.Successor(successor), "foo.Cleanup") {
			t.Fatalf("%s canceled child status case %d does not enter the static defer drainer:\n%s", function.Name(), successor, body)
		}
	}
	if coroTestBlockHasDirectCall(panicked, coroPanicPrepareHookV1) {
		t.Fatalf("%s Panic completion bypasses the parent cleanup drainer:\n%s", function.Name(), panicked.AsValue().String())
	}
	completeCalls := 0
	for _, block := range function.BasicBlocks() {
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if instruction.InstructionOpcode() != llvm.Call || instruction.CalledValue().Name() != coroCompletePrepareHookV2 {
				continue
			}
			completeCalls++
			if got := instruction.OperandsCount() - 1; got != 4 {
				t.Fatalf("%s terminal completion arguments = %d, want (g,handle,header,status):\n%s",
					function.Name(), got, instruction.String())
			}
			status := instruction.Operand(3)
			if status.InstructionOpcode() != llvm.Load || status.Type().TypeKind() != llvm.IntegerTypeKind ||
				status.Type().IntTypeWidth() != 32 {
				t.Fatalf("%s terminal completion does not load its frame-local status:\n%s", function.Name(), instruction.String())
			}
		}
	}
	if completeCalls != 1 {
		t.Fatalf("%s terminal completion calls = %d, want one shared publication:\n%s", function.Name(), completeCalls, body)
	}
}

func coroTestBlockLoadsI32(block llvm.BasicBlock) bool {
	for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
		if instruction.InstructionOpcode() == llvm.Load && instruction.Type().TypeKind() == llvm.IntegerTypeKind &&
			instruction.Type().IntTypeWidth() == 32 {
			return true
		}
	}
	return false
}

func coroTestBlockStoresGlobal(block llvm.BasicBlock, name string) bool {
	for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
		if instruction.InstructionOpcode() == llvm.Store && instruction.Operand(1).Name() == name {
			return true
		}
	}
	return false
}

func coroTestBlockStoresI32(block llvm.BasicBlock, value uint32) bool {
	for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
		if instruction.InstructionOpcode() != llvm.Store {
			continue
		}
		stored := instruction.Operand(0)
		if stored.Type().TypeKind() == llvm.IntegerTypeKind && stored.Type().IntTypeWidth() == 32 &&
			!stored.IsAConstantInt().IsNil() && stored.ZExtValue() == uint64(value) {
			return true
		}
	}
	return false
}

func coroTestBlockHasDirectCall(block llvm.BasicBlock, callee string) bool {
	for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
		if instruction.InstructionOpcode() == llvm.Call && instruction.CalledValue().Name() == callee {
			return true
		}
	}
	return false
}

const (
	coroTestAwaitConsumeNormal uint8 = 1 << iota
	coroTestAwaitConsumeCanceled
)

func coroTestFirstReachableAwaitConsumes(
	entry, normal, canceled llvm.BasicBlock,
) uint8 {
	type edge struct {
		block       llvm.BasicBlock
		predecessor llvm.BasicBlock
	}
	type state struct {
		edge
		constants map[llvm.Value]uint64
	}
	seen := make(map[edge][]map[llvm.Value]uint64)
	pending := []state{{edge: edge{block: entry}, constants: make(map[llvm.Value]uint64)}}
	var result uint8
	for len(pending) != 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if current.block.IsNil() {
			continue
		}
		alreadySeen := false
		for _, constants := range seen[current.edge] {
			if sameCoroCFGConstants(constants, current.constants) {
				alreadySeen = true
				break
			}
		}
		if alreadySeen {
			continue
		}
		seen[current.edge] = append(seen[current.edge], current.constants)
		switch current.block {
		case normal:
			result |= coroTestAwaitConsumeNormal
			continue
		case canceled:
			result |= coroTestAwaitConsumeCanceled
			continue
		}
		constants := copyCoroCFGConstants(current.constants)
		for instruction := current.block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if !instruction.IsAPHINode().IsNil() {
				value, ok := coroCFGPHIIncomingConstant(instruction, current.predecessor, constants)
				if ok {
					constants[instruction] = value
				} else {
					delete(constants, instruction)
				}
				continue
			}
			// Conditional stack cuts encode !completed as xor i1 %phi, true.
			// Preserve that exact path fact so the inline-complete predecessor
			// is not incorrectly considered able to enter the resume gate.
			if instruction.InstructionOpcode() == llvm.Xor {
				left, leftOK := coroCFGConstant(instruction.Operand(0), constants)
				right, rightOK := coroCFGConstant(instruction.Operand(1), constants)
				if leftOK && rightOK {
					constants[instruction] = left ^ right
				}
			}
		}
		terminator := current.block.LastInstruction()
		for _, successor := range executableTerminatorSuccessors(terminator, constants) {
			pending = append(pending, state{
				edge:      edge{block: successor, predecessor: current.block},
				constants: constants,
			})
		}
	}
	return result
}

func coroTestBlockCanReachDirectCall(entry llvm.BasicBlock, callee string) bool {
	seen := make(map[llvm.BasicBlock]bool)
	pending := []llvm.BasicBlock{entry}
	for len(pending) != 0 {
		block := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if block.IsNil() || seen[block] {
			continue
		}
		seen[block] = true
		if coroTestBlockHasDirectCall(block, callee) {
			return true
		}
		terminator := block.LastInstruction()
		for successor := 0; successor < terminator.SuccessorsCount(); successor++ {
			pending = append(pending, terminator.Successor(successor))
		}
	}
	return false
}

func compileCoroAwaitCompletionCleanupFixture(
	t *testing.T,
	target *llssa.Target,
) (llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.Function) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroAwaitCompletionCleanupFixture)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
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
	parent, child := ssaPkg.Func("Parent"), ssaPkg.Func("Child")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: parent, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == child {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, parent, child
}

func compileCoroStaticCleanupIRFixture(
	t *testing.T,
	target *llssa.Target,
) (llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroStaticCleanupIRFixture)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
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
	root, first, second := ssaPkg.Func("Root"), ssaPkg.Func("First"), ssaPkg.Func("Second")
	variadic := ssaPkg.Func("Variadic")
	var third *ssa.Function
	for _, function := range universe.Functions() {
		if function != nil && function.Name() == "Third" && function.Signature != nil && function.Signature.Recv() != nil {
			third = function
			break
		}
	}
	if third == nil {
		prog.Dispose()
		t.Fatal("Third method is absent from the emission universe")
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == first || function == second || function == third || function == variadic {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, root
}

func compileCoroCapturedStaticCleanupFixture(
	t *testing.T,
	targetMachine *llssa.Target,
) (llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.MakeClosure, *ssa.Function) {
	return compileCoroCapturedStaticCleanupFixtureSource(
		t, targetMachine, coroCapturedStaticCleanupIRFixture,
	)
}

func compileCoroCapturedStaticCleanupFixtureSource(
	t *testing.T,
	targetMachine *llssa.Target,
	source string,
) (llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.MakeClosure, *ssa.Function) {
	t.Helper()
	testProgram := newEmissionTestProgram()
	testProgram.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePackage := testProgram.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func AllocU(size uintptr) unsafe.Pointer {
	if size == 0 { return nil }
	return nil
}
func AllocZ(size uintptr) unsafe.Pointer {
	if size == 0 { return nil }
	return nil
}
`)
	fooPackage := testProgram.addPackage(t, "foo", source)
	testProgram.ssa.Build()
	ssaPkg := fooPackage.ssa
	files := []*ast.File{fooPackage.file}
	var prog llssa.Program
	if targetMachine == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, targetMachine)
	}
	universe, err := prepareStacklessEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
		{SSA: runtimePackage.ssa, Files: []*ast.File{runtimePackage.file}},
		{SSA: ssaPkg, Files: files},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	var closure *ssa.MakeClosure
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			deferred, ok := instruction.(*ssa.Defer)
			if !ok {
				continue
			}
			closure, _ = deferred.Call.Value.(*ssa.MakeClosure)
			break
		}
		if closure != nil {
			break
		}
	}
	if closure == nil {
		prog.Dispose()
		t.Fatal("captured cleanup fixture has no exact MakeClosure defer")
	}
	cleanupTarget, ok := closure.Fn.(*ssa.Function)
	if !ok || cleanupTarget == nil {
		prog.Dispose()
		t.Fatal("captured cleanup fixture MakeClosure has no exact function target")
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyLoweredCalls: universe.CoroLoweredCalls,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == cleanupTarget {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, root, closure, cleanupTarget
}

func compileCoroDynamicCleanupFixture(
	t *testing.T,
	targetMachine *llssa.Target,
) (llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function) {
	t.Helper()
	testProgram := newEmissionTestProgram()
	testProgram.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePackage := testProgram.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func AllocU(size uintptr) unsafe.Pointer {
	if size == 0 { return nil }
	return nil
}
func FreeDeferNode(pointer unsafe.Pointer) {
	if pointer == nil { return }
}
`)
	fooPackage := testProgram.addPackage(t, "foo", coroDynamicCleanupIRFixture)
	testProgram.ssa.Build()
	ssaPkg := fooPackage.ssa
	files := []*ast.File{fooPackage.file}
	var prog llssa.Program
	if targetMachine == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, targetMachine)
	}
	universe, err := prepareStacklessEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
		{SSA: runtimePackage.ssa, Files: []*ast.File{runtimePackage.file}},
		{SSA: ssaPkg, Files: files},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	root, cleanup := ssaPkg.Func("Root"), ssaPkg.Func("Cleanup")
	var descriptorDefer *ssa.Defer
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			deferred, ok := instruction.(*ssa.Defer)
			if ok && deferred.Call.StaticCallee() == nil {
				descriptorDefer = deferred
				break
			}
		}
		if descriptorDefer != nil {
			break
		}
	}
	if descriptorDefer == nil {
		prog.Dispose()
		t.Fatal("dynamic cleanup fixture has no function-value defer")
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: root, Demand: coro.AsyncDemand},
		{Function: ssaPkg.Func("init"), Demand: coro.SyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyLoweredCalls: universe.CoroLoweredCalls,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == cleanup {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, NeedsDispatch: true}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyClosedDynamicCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.SSAClosedDynamicCallCertificate, bool, error) {
			if call == descriptorDefer {
				return coro.SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{cleanup}}, true, nil
			}
			return coro.SSAClosedDynamicCallCertificate{}, false, nil
		},
		ClassifyUnknownCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.UnknownTarget, error) {
			if call == descriptorDefer {
				return coro.UnknownManagedDispatch, nil
			}
			return coro.UnknownManaged, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	compilation.FuncRepABI = coro.FuncRepABIV1
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, root
}

func TestCoroStaticCleanupPlainTargetQuery(t *testing.T) {
	const source = `package foo
type Guard struct{}
func (*Guard) release() {}
func Root(guard *Guard) { defer guard.release() }
`
	prog, universe, plan, root, target := buildCoroStaticCleanupPlanFixture(t, source)
	defer prog.Dispose()

	rootPlan, ok := plan.FunctionPlan(root)
	if !ok || rootPlan.Emission != coro.EmitCoroutine || !rootPlan.Exec.Contains(coro.NeedsCleanupFrame) {
		t.Fatalf("Root plan = %+v, present=%t; want cleanup coroutine", rootPlan, ok)
	}
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.Emission != coro.EmitPlain || targetPlan.FuncRep != coro.DirectPlain {
		t.Fatalf("release plan = %+v, present=%t; want DirectPlain", targetPlan, ok)
	}
	cleanup, err := prepareCoroStaticCleanupPlan(root, plan, universe, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup == nil || len(cleanup.sites) != 1 || cleanup.sites[0].target != target ||
		cleanup.sites[0].kind != coroStaticCleanupPlain || len(cleanup.sites[0].instruction.Call.Args) != 1 {
		t.Fatalf("static receiver cleanup = %+v", cleanup)
	}
	certified, err := universe.CoroStaticCleanupPlainTarget(plan, target, "")
	if err != nil || !certified {
		t.Fatalf("plain cleanup target certified=%t, err=%v", certified, err)
	}
}

func TestCoroStaticCleanupPlainTargetQueryRejectsOtherConsumers(t *testing.T) {
	const source = `package foo
func cleanup() {}
func Root() { defer cleanup(); cleanup() }
`
	prog, universe, plan, _, target := buildCoroStaticCleanupPlanFixture(t, source)
	defer prog.Dispose()
	certified, err := universe.CoroStaticCleanupPlainTarget(plan, target, "")
	if err != nil {
		t.Fatal(err)
	}
	if certified {
		t.Fatal("plain cleanup target with an ordinary call consumer was certified")
	}
}

func TestCoroStaticCleanupPlanFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		explicit bool
		want     string
	}{
		{
			name: "legacy panic ABI",
			source: `package foo
func cleanup() {}
func Root() { defer cleanup() }
`,
			want: "legacy panic",
		},
		{
			name: "captured plain closure",
			source: `package foo
func Root(value uint32) { defer func() { _ = value }() }
`,
			explicit: true,
			want:     "unsupported value representation direct-plain",
		},
		{
			name: "dynamic plain closure without no-unwind proof",
			source: `package foo
func Root(value uint32, first bool) {
	left := func() { _ = value }
	right := func() { _ = value + 1 }
	selected := left
	if !first { selected = right }
	defer selected()
}
`,
			explicit: true,
			want:     "no-unwind proof",
		},
		{
			name: "loop registration without frozen dynamic helpers",
			source: `package foo
func cleanup() {}
func Root() { for index := 0; index != 1; index++ { defer cleanup() } }
`,
			explicit: true,
			want:     "AllocU",
		},
		{
			name: "cleanup child panic",
			source: `package foo
var Payload uint32
func cleanup() { panic(&Payload) }
func Root() { defer cleanup() }
`,
			explicit: true,
			want:     "no-unwind proof",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog, universe, plan, root, _ := buildCoroStaticCleanupPlanFixture(t, test.source)
			defer prog.Dispose()
			_, err := prepareCoroStaticCleanupPlan(root, plan, universe, "", test.explicit)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("cleanup preflight error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCoroStaticCleanupPlanComposesNestedCoroutineTarget(t *testing.T) {
	const source = `package foo
var ready chan struct{}
func inner() {}
func cleanup() { defer inner(); <-ready }
func Root() { defer cleanup() }
`
	prog, universe, plan, root, target := buildCoroStaticCleanupPlanFixture(t, source)
	defer prog.Dispose()
	if target == nil {
		t.Fatal("Root defer target is absent")
	}
	var deferPlan coro.SSACallPlan
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if deferred, ok := instruction.(*ssa.Defer); ok {
				deferPlan, _ = plan.CallPlan(deferred)
			}
		}
	}
	targetPlan, planned := plan.FunctionPlan(target)
	if !planned || targetPlan.Emission != coro.EmitCoroutine ||
		!targetPlan.Exec.Contains(coro.NeedsCleanupFrame) {
		t.Fatalf("nested cleanup target plan = %+v, present=%t; defer=%+v", targetPlan, planned, deferPlan)
	}
	rootCleanup, err := prepareCoroStaticCleanupPlan(root, plan, universe, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if rootCleanup == nil || len(rootCleanup.sites) != 1 ||
		rootCleanup.sites[0].target != target ||
		rootCleanup.sites[0].kind != coroStaticCleanupCoroutine {
		t.Fatalf("Root nested cleanup plan = %+v", rootCleanup)
	}
	targetCleanup, err := prepareCoroStaticCleanupPlan(target, plan, universe, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if targetCleanup == nil || len(targetCleanup.sites) != 1 {
		t.Fatalf("deferred child cleanup plan = %+v", targetCleanup)
	}
}

func TestCoroStaticCleanupPlanCapturesFunctionValuedArgument(t *testing.T) {
	const source = `package foo
func cleanup(func(int)) {}
func Root() { defer cleanup(func(int) {}) }
`
	prog, universe, plan, root, target := buildCoroStaticCleanupPlanFixture(t, source)
	defer prog.Dispose()
	cleanup, err := prepareCoroStaticCleanupPlan(root, plan, universe, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup == nil || len(cleanup.sites) != 1 || cleanup.sites[0].target != target ||
		len(cleanup.sites[0].instruction.Call.Args) != 1 {
		t.Fatalf("function-valued cleanup plan = %+v", cleanup)
	}
	argument := cleanup.sites[0].instruction.Call.Args[0]
	if _, ok := types.Unalias(argument.Type()).Underlying().(*types.Signature); !ok {
		t.Fatalf("captured cleanup argument type = %s, want function", argument.Type())
	}
}

func TestCoroStaticCleanupPlanCanonicalizesPatchedTarget(t *testing.T) {
	universe, original, alternate, dispose := preparePatchedEmissionTest(t, `package p
type Guard struct{}
func (*Guard) Cleanup() {}
func Root(guard *Guard) { defer guard.Cleanup() }
`, `package p
type Guard struct{}
func (*Guard) Cleanup() {}
`)
	defer dispose()

	root := original.ssa.Func("Root")
	var rawTarget *ssa.Function
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if deferred, ok := instruction.(*ssa.Defer); ok {
				rawTarget = deferred.Call.StaticCallee()
			}
		}
	}
	if rawTarget == nil {
		t.Fatal("patched method defer has no static source target")
	}
	target, ok := universe.Resolve(rawTarget)
	if !ok || target == nil || target.Pkg != alternate.ssa {
		t.Fatalf("Resolve(original Cleanup) = %v, %t; want patched method in %v", target, ok, alternate.ssa)
	}
	if resolved, ok := universe.Resolve(rawTarget); !ok || resolved != target {
		t.Fatalf("Resolve(original Cleanup) = %v, %t; want patched target %v", resolved, ok, target)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(original.ssa.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(original.ssa.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ResolveFunction: func(function *ssa.Function) (*ssa.Function, bool, error) {
			resolved, ok := universe.Resolve(function)
			return resolved, ok, nil
		},
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == root {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := prepareCoroStaticCleanupPlan(root, plan, universe, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup == nil || len(cleanup.sites) != 1 || cleanup.sites[0].target != target {
		t.Fatalf("patched cleanup plan = %+v; want exact target %v", cleanup, target)
	}
}

func buildCoroStaticCleanupPlanFixture(
	t *testing.T,
	source string,
) (llssa.Program, *EmissionUniverse, *coro.SSAPlan, *ssa.Function, *ssa.Function) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
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
	root := ssaPkg.Func("Root")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == root {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	var target *ssa.Function
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if deferred, ok := instruction.(*ssa.Defer); ok {
				target = deferred.Call.StaticCallee()
				break
			}
		}
	}
	return prog, universe, plan, root, target
}
