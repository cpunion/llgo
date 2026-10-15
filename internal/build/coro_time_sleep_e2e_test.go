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
	"fmt"
	"go/constant"
	"go/token"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroTimeSleepParkSymbolV2   = "__llgo_coro_timer_park_v2"
	coroTimeSleepResumeSymbolV2 = "__llgo_coro_timer_resume_v2"
	coroTimeSleepPreemptPollV1  = "__llgo_coro_preempt_poll_v1"
	coroTimeSleepAwaitHookV1    = "__llgo_coro_await_prepare_inline_v4"
)

// TestCoroNativeTimeSleepProductionPlanAndCodegen compiles ordinary synchronous
// Go through the complete production pipeline, including the synctest bridge.
// No hand-written runtime types, effect seeds, or partial SSA universe stand in
// for the production implementation. Hooks observe the final plan and frontend
// IR without replacing the default analysis policy.
func TestCoroNativeTimeSleepProductionPlanAndCodegen(t *testing.T) {
	capability := &Config{BuildMode: BuildModeExe, Goos: runtime.GOOS, Goarch: runtime.GOARCH}
	if !nativeCoroTimerRuntimeABI(capability) {
		t.Skipf("native coroutine time.Sleep compilation is unavailable on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	const callerPath = "github.com/xgo-dev/llgo/internal/build/_testgo/coro_time_sleep"
	var input CoroPlanInput
	var plan *coro.SSAPlan
	bodies := make(map[string]string)
	var emitted []llssa.Package
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.CoroPlanBuilder = func(next CoroPlanInput) (*coro.SSAPlan, error) {
		analyzed, err := defaultCoroPlanBuilder(next)
		if err == nil {
			input, plan = next, analyzed
		}
		return analyzed, err
	}
	conf.ModuleHook = func(pkg Package) {
		if pkg.LPkg == nil || pkg.PkgPath != "time" && pkg.PkgPath != callerPath {
			return
		}
		emitted = append(emitted, pkg.LPkg)
		for _, name := range []string{"Sleep", "sleepOnce", "sleepZero", "sleepNegative", "main"} {
			symbol := llssa.FullName(pkg.Types, name) + "$coro"
			physical := pkg.LPkg.Module().NamedFunction(symbol)
			if !physical.IsNil() && physical.BasicBlocksCount() != 0 {
				bodies[symbol] = physical.String()
			}
		}
	}
	packages, err := Do([]string{"./_testgo/coro_time_sleep"}, conf)
	if err != nil {
		t.Fatalf("compile production time.Sleep program: %v", err)
	}
	if len(packages) != 1 || packages[0].LPkg == nil {
		t.Fatal("production pipeline did not return one owned LLVM program")
	}
	defer packages[0].LPkg.Prog.Dispose()
	if input.Program == nil || plan == nil {
		t.Fatal("production pipeline did not publish its final plan")
	}

	find := func(path, name string) *ssa.Function {
		t.Helper()
		fn, err := findCoroTimeSleepFunction(input.Program, path, name)
		if err != nil {
			t.Fatal(err)
		}
		return fn
	}
	sleep := find("time", "Sleep")
	position := input.Program.Fset.PositionFor(sleep.Pos(), true)
	wantPatch := filepath.ToSlash(filepath.Join("runtime", "_patch", "time", "sleep_coro_native_llgo.go"))
	if got := filepath.ToSlash(position.Filename); !strings.HasSuffix(got, wantPatch) {
		t.Fatalf("time.Sleep source = %q, want production source patch suffix %q", got, wantPatch)
	}
	intrinsic := find("time", "llgoCoroTimerSleep")
	intrinsicCall, err := findCoroTimeSleepDirectCall(sleep, intrinsic)
	if err != nil {
		t.Fatal(err)
	}
	assertCoroTimeSleepNonpositiveFastPath(t, sleep, intrinsicCall)
	site, found, err := input.callSitePlan(intrinsicCall)
	if err != nil || !found || !site.Intrinsic || !site.IntrinsicSemantics.SuspendsCurrentFrame() || !plan.ElidesCall(intrinsicCall) {
		t.Fatalf("production time.Sleep timer site = %+v, found=%t, err=%v, elided=%t; want exact suspending intrinsic",
			site, found, err, plan.ElidesCall(intrinsicCall))
	}
	if err := assertCoroTimeSleepFunctionPlan(plan, sleep, true, "time.Sleep"); err != nil {
		t.Fatal(err)
	}
	main := find("main", "main")
	if err := assertCoroTimeSleepFunctionPlan(plan, main, false, "main"); err != nil {
		t.Fatal(err)
	}
	sleepSymbol := llssa.FullName(sleep.Pkg.Pkg, sleep.Name()) + "$coro"
	body := func(fn *ssa.Function) string {
		t.Helper()
		symbol := llssa.FullName(fn.Pkg.Pkg, fn.Name()) + "$coro"
		ir, found := bodies[symbol]
		if !found {
			t.Fatalf("production pipeline emitted no physical body for %s", symbol)
		}
		return ir
	}
	sleepPlan, _ := plan.FunctionPlan(sleep)
	assertCoroTimerSleepV2FrameIR(t, "production time.Sleep", body(sleep), sleepPlan.Exec.Contains(coro.NeedsPreempt))
	for _, name := range []string{"sleepOnce", "sleepZero", "sleepNegative"} {
		caller := find("main", name)
		if err := assertCoroTimeSleepFunctionPlan(plan, caller, false, name); err != nil {
			t.Fatal(err)
		}
		for _, edge := range [][2]*ssa.Function{{caller, sleep}, {main, caller}} {
			call, err := findCoroTimeSleepDirectCall(edge[0], edge[1])
			if err != nil {
				t.Fatal(err)
			}
			if err := assertCoroTimeSleepDirectCoroCall(plan, call, edge[1], edge[0].Name()+" -> "+edge[1].Name()); err != nil {
				t.Fatal(err)
			}
		}
		assertCoroTimeSleepAwaitsIR(t, name, body(caller), sleepSymbol)
	}
	// Static tail-forward calls use the final ramp, not a redundant wrapper.
	assertCoroTimeSleepAwaitsIR(t, "main", body(main), sleepSymbol, sleepSymbol, sleepSymbol)
	for _, pkg := range emitted {
		runCoroSpawnNativeE2EPasses(t, pkg.Prog, pkg.Module())
	}
}

func findCoroTimeSleepFunction(prog *ssa.Program, path, name string) (*ssa.Function, error) {
	if prog == nil {
		return nil, fmt.Errorf("find %s.%s: nil SSA program", path, name)
	}
	var found *ssa.Function
	for _, pkg := range prog.AllPackages() {
		if pkg == nil || pkg.Pkg == nil || llssa.PathOf(pkg.Pkg) != path {
			continue
		}
		member, ok := pkg.Members[name].(*ssa.Function)
		if !ok || member == nil {
			continue
		}
		if found != nil && found != member {
			return nil, fmt.Errorf("production source has ambiguous %s.%s SSA bodies", path, name)
		}
		found = member
	}
	if found == nil {
		return nil, fmt.Errorf("production source has no %s.%s SSA function", path, name)
	}
	return found, nil
}

func assertCoroTimeSleepFunctionPlan(plan *coro.SSAPlan, function *ssa.Function, intrinsicOwner bool, label string) error {
	got, ok := plan.FunctionPlan(function)
	if !ok || got.External != coro.Defined || got.Emission != coro.EmitCoroutine ||
		got.Primary != coro.PrimaryCoroutine || got.FuncRep != coro.DirectCoro ||
		!got.Effect.Contains(coro.MayPark) {
		return fmt.Errorf("%s production plan = %+v, present=%t; want one defined DirectCoro MayPark body", label, got, ok)
	}
	if intrinsicOwner {
		if !got.DeclaredEffect.Contains(coro.MayPark) || !got.LocalEffect.Contains(coro.MayPark) {
			return fmt.Errorf("%s production local effect = declared:%s local:%s, want intrinsic MayPark seed", label, got.DeclaredEffect, got.LocalEffect)
		}
		return nil
	}
	// The complete production plan also records demand-sensitive terminal
	// outcome transport. It is not a local timer/wait seed; reject every other
	// local effect so MayPark must still be inferred through the call chain.
	if got.DeclaredEffect != coro.NoSuspend || got.LocalEffect&^coro.OutcomeStructured != coro.NoSuspend || !got.Effect.Contains(coro.AwaitStructured) {
		return fmt.Errorf("%s production effects = declared:%s local:%s total:%s, want unseeded synchronous source automatically tainted by MayPark+AwaitStructured", label, got.DeclaredEffect, got.LocalEffect, got.Effect)
	}
	return nil
}

func findCoroTimeSleepDirectCall(owner, target *ssa.Function) (*ssa.Call, error) {
	if owner == nil || target == nil {
		return nil, fmt.Errorf("find production time.Sleep call: nil owner or target")
	}
	var found *ssa.Call
	for _, block := range owner.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil || call.Common().StaticCallee() != target {
				continue
			}
			if found != nil {
				return nil, fmt.Errorf("%s has duplicate direct calls to %s", owner.Name(), target.Name())
			}
			found = call
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%s has no direct call to %s", owner.Name(), target.Name())
	}
	return found, nil
}

// The whole-program plan is intentionally value-insensitive, so Sleep(0) and
// Sleep(-1) callers still use one DirectCoro child await. This CFG proof is
// narrower: the nonpositive body returns before entering the dedicated timer
// intrinsic. Avoiding the caller-side coroutine
// handoff as required by Go's immediate-return behavior still needs a
// conditional-effect or call-site fast-path ABI.
func assertCoroTimeSleepNonpositiveFastPath(t *testing.T, sleep *ssa.Function, intrinsic *ssa.Call) {
	t.Helper()
	if sleep == nil || len(sleep.Params) != 1 || len(sleep.Blocks) == 0 || intrinsic == nil {
		t.Fatal("production time.Sleep fast-path proof requires one parameter, body, and timer intrinsic call")
	}
	entry := sleep.Blocks[0]
	if len(entry.Instrs) == 0 || len(entry.Succs) != 2 {
		t.Fatalf("production time.Sleep entry CFG = instructions:%d successors:%d, want one binary duration guard", len(entry.Instrs), len(entry.Succs))
	}
	branch, ok := entry.Instrs[len(entry.Instrs)-1].(*ssa.If)
	if !ok {
		t.Fatalf("production time.Sleep entry terminator = %T, want duration guard", entry.Instrs[len(entry.Instrs)-1])
	}
	comparison, ok := branch.Cond.(*ssa.BinOp)
	if !ok || comparison.Op != token.LEQ {
		t.Fatalf("production time.Sleep guard = %T %v, want d <= 0", branch.Cond, branch.Cond)
	}
	parameterAndZero := func(parameter, zero ssa.Value) bool {
		value, ok := zero.(*ssa.Const)
		return parameter == sleep.Params[0] && ok && value.Value != nil && constant.Sign(value.Value) == 0
	}
	if !parameterAndZero(comparison.X, comparison.Y) && !parameterAndZero(comparison.Y, comparison.X) {
		t.Fatalf("production time.Sleep guard = %s, want its exact duration parameter and zero", comparison)
	}
	fast, positive := entry.Succs[0], entry.Succs[1]
	if coroTimeSleepBlockReachesInstruction(fast, intrinsic) || !coroTimeSleepBlockReachesInstruction(positive, intrinsic) {
		t.Fatalf("production time.Sleep d<=0/positive intrinsic reachability = %t/%t, want false/true",
			coroTimeSleepBlockReachesInstruction(fast, intrinsic), coroTimeSleepBlockReachesInstruction(positive, intrinsic))
	}
	if len(fast.Instrs) == 0 {
		t.Fatal("production time.Sleep d<=0 branch is empty")
	}
	if _, ok := fast.Instrs[len(fast.Instrs)-1].(*ssa.Return); !ok {
		t.Fatalf("production time.Sleep d<=0 terminator = %T, want immediate return", fast.Instrs[len(fast.Instrs)-1])
	}
	for _, block := range []*ssa.BasicBlock{entry, fast} {
		for _, instruction := range block.Instrs {
			if alloc, ok := instruction.(*ssa.Alloc); ok {
				t.Fatalf("production time.Sleep d<=0 path allocates retained state before return: %s", alloc)
			}
		}
	}
}

// assertCoroTimerSleepV2FrameIR checks the production source patch after its
// single intrinsic has become the compiler-owned source-aware park recipe.
// The opaque state must be a coroutine-frame local, with exactly one park /
// suspend / resume sequence and no ordinary heap allocation or V1 token span.
func assertCoroTimerSleepV2FrameIR(t *testing.T, label, body string, needsPreempt bool) {
	t.Helper()
	for _, forbidden := range []string{
		"runtime.AllocZ",
		"__llgo_coro_timer_prepare_after_or_abort_v1",
		"__llgo_coro_timer_retire_completed_or_abort_v1",
		"llgo.coroTimerSleep",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("%s contains obsolete or escaping Timer storage %q:\n%s", label, forbidden, body)
		}
	}
	park := strings.Index(body, "call void @"+coroTimeSleepParkSymbolV2)
	resume := strings.Index(body, "call i32 @"+coroTimeSleepResumeSymbolV2)
	if park < 0 || resume <= park || strings.Count(body, coroTimeSleepParkSymbolV2) != 1 ||
		strings.Count(body, coroTimeSleepResumeSymbolV2) != 1 {
		t.Fatalf("%s does not contain one ordered Timer V2 park/resume pair:\n%s", label, body)
	}
	span := body[park:resume]
	if got := strings.Count(span, "@llvm.coro.suspend"); got != 1 {
		t.Fatalf("%s Timer V2 park/resume span has %d suspends, want 1:\n%s", label, got, body)
	}
	if !regexp.MustCompile(
		`(?s)store i16 4,.*store i16 3,.*call void @` + regexp.QuoteMeta(coroTimeSleepParkSymbolV2) +
			`\(ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^,]+, i64 [^)]+\)`,
	).MatchString(body) {
		t.Fatalf("%s does not publish SuspendPark before the exact Timer V2 park ABI:\n%s", label, body)
	}
	for _, forbidden := range []string{coroTimeSleepPreemptPollV1, coroTimeSleepAwaitHookV1, "__llgo_coro_yield_prepare_v1"} {
		if strings.Contains(span, forbidden) {
			t.Fatalf("%s Timer V2 park/resume span contains forbidden handoff %q:\n%s", label, forbidden, body)
		}
	}
	if needsPreempt && !strings.Contains(body[:park], coroTimeSleepPreemptPollV1) {
		t.Fatalf("%s has no preemption poll before its Timer V2 park:\n%s", label, body)
	}
}

func coroTimeSleepBlockReachesInstruction(start *ssa.BasicBlock, want ssa.Instruction) bool {
	if start == nil || want == nil {
		return false
	}
	seen := make(map[*ssa.BasicBlock]bool)
	queue := []*ssa.BasicBlock{start}
	for len(queue) != 0 {
		block := queue[0]
		queue = queue[1:]
		if block == nil || seen[block] {
			continue
		}
		seen[block] = true
		for _, instruction := range block.Instrs {
			if instruction == want {
				return true
			}
		}
		queue = append(queue, block.Succs...)
	}
	return false
}

func assertCoroTimeSleepDirectCoroCall(plan *coro.SSAPlan, call ssa.CallInstruction, target *ssa.Function, label string) error {
	targetID, ok := plan.FunctionID(target)
	if !ok {
		return fmt.Errorf("%s target is absent from production plan", label)
	}
	got, ok := plan.CallPlan(call)
	if !ok || got.Kind != coro.CallDirect || got.Rep != coro.DirectCoro || got.Open || got.MayBeNil ||
		len(got.Targets) != 1 || got.Targets[0] != targetID {
		return fmt.Errorf("%s production CallPlan = %+v, present=%t; want one closed DirectCoro target %q", label, got, ok, targetID)
	}
	return nil
}

func assertCoroTimeSleepAwaitsIR(t *testing.T, label, body string, childSymbols ...string) {
	t.Helper()
	if len(childSymbols) == 0 {
		t.Fatalf("%s has no expected coroutine child", label)
	}
	handoffs := strings.Count(body, coroTimeSleepAwaitHookV1)
	tailForward := len(childSymbols) == 1 && handoffs == 0 &&
		strings.Contains(body, "ret ptr") && !strings.Contains(body, "llvm.coro.")
	if handoffs != len(childSymbols) && !tailForward {
		t.Fatalf("%s await handoffs = %d, want %d or one frame-free tail forward:\n%s", label, handoffs, len(childSymbols), body)
	}
	expectedCounts := make(map[string]int)
	for _, childSymbol := range childSymbols {
		expectedCounts[childSymbol]++
	}
	for childSymbol, expected := range expectedCounts {
		if got := strings.Count(body, childSymbol); got != expected {
			t.Fatalf("%s calls coroutine child %q %d times, want %d:\n%s", label, childSymbol, got, expected, body)
		}
	}
	searchFrom := 0
	for _, childSymbol := range childSymbols {
		relativeChild := strings.Index(body[searchFrom:], childSymbol)
		if relativeChild < 0 {
			t.Fatalf("%s does not call coroutine child %q in source order:\n%s", label, childSymbol, body)
		}
		child := searchFrom + relativeChild
		await := strings.Index(body[child+len(childSymbol):], coroTimeSleepAwaitHookV1)
		if await < 0 && !tailForward {
			t.Fatalf("%s child %q is not followed by a structured await:\n%s", label, childSymbol, body)
		}
		if await >= 0 {
			searchFrom = child + len(childSymbol) + await + len(coroTimeSleepAwaitHookV1)
		} else {
			searchFrom = child + len(childSymbol)
		}
	}
}
