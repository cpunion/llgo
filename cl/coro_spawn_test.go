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
	"bytes"
	"go/types"
	"regexp"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroClosedStaticSpawnTestSource = `package foo

var Sink uint32

func ArgFirst(value uint32) uint32 { return value + 1 }
func ArgSecond(value uint32) uint32 { return value + 2 }
func Plain(first, second uint32) { Sink = first + second }
func Async(value uint32) { Sink = value }

func Parent(value uint32) {
	Plain(value, value)
	go Plain(ArgFirst(value), ArgSecond(value))
	go Async(value)
}
`

const coroManagedDispatchSpawnTestSource = `package foo

var Sink int

func MakeCallback(seed int) func(int) {
	return func(value int) { Sink = seed + value }
}

func MakeLauncher(callback func(int), base int) func(int) {
	return func(value int) {
		go callback(base + value)
	}
}
`

const coroClosedStaticMethodSpawnTestSource = `package foo

var Sink int

type Worker int

func (receiver Worker) Run(callback func(int), value int) {
	Sink = int(receiver) + value
	_ = callback
}

func Receiver(value int) Worker { return Worker(value + 1) }
func Argument(value int) int { return value + 2 }

func Parent(callback func(int), value int) {
	go Receiver(value).Run(callback, Argument(value))
}
`

const coroStaticSpawnTransportTestSource = `package foo

//llgo:type C
type CFunc func(int) int

type Mixed struct {
	Raw CFunc
	Managed func(int)
}

func RawTarget(raw CFunc) { _ = raw }
func MixedTarget(raw CFunc, managed func(int), mixed Mixed) {
	_, _, _ = raw, managed, mixed
}

func Parent(raw CFunc, managed func(int), mixed Mixed) {
	go RawTarget(raw)
	go MixedTarget(raw, managed, mixed)
}

func RawCallee(raw CFunc) { go raw(1) }
`

func TestCoroClosedStaticSpawnNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, ssaPkg := compileCoroClosedStaticSpawnFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify closed static spawn before CoroSplit: %v\n%s", err, module.String())
			}
			parentPlan, _ := plan.FunctionPlan(ssaPkg.Func("Parent"))
			if parentPlan.DeclaredEffect != coro.YieldOnly || !parentPlan.LocalEffect.Contains(coro.YieldOnly) ||
				!parentPlan.Effect.Contains(coro.YieldOnly) || parentPlan.Emission != coro.EmitCoroutine ||
				parentPlan.Primary != coro.PrimaryCoroutine || parentPlan.FuncRep != coro.DirectCoro || parentPlan.Demand != coro.AsyncDemand {
				t.Fatalf("Parent plan = %+v", parentPlan)
			}
			plainPlan, _ := plan.FunctionPlan(ssaPkg.Func("Plain"))
			if plainPlan.Emission != coro.EmitCoroutine || plainPlan.Primary != coro.PrimaryCoroutine || plainPlan.FuncRep != coro.DirectCoro ||
				!plainPlan.Effect.Contains(coro.YieldOnly) || plainPlan.Demand != coro.AsyncDemand {
				t.Fatalf("Plain sync+spawn plan = %+v", plainPlan)
			}
			asyncPlan, _ := plan.FunctionPlan(ssaPkg.Func("Async"))
			if asyncPlan.Emission != coro.EmitCoroutine || asyncPlan.Primary != coro.PrimaryCoroutine ||
				asyncPlan.FuncRep != coro.DirectCoro || asyncPlan.Demand != coro.AsyncDemand {
				t.Fatalf("Async spawn plan = %+v", asyncPlan)
			}

			ir := module.String()
			parent := requireCoroPhysicalFunction(t, module, "foo.Parent").String()
			if !module.NamedFunction("foo.Plain").IsNil() || module.NamedFunction("foo.Plain"+coroPrimarySuffix).IsNil() {
				t.Fatalf("bounded sync+spawn target did not retain exactly one preemptible coroutine primary:\n%s", ir)
			}
			if !module.NamedFunction("foo.Async").IsNil() || module.NamedFunction("foo.Async"+coroPrimarySuffix).IsNil() {
				t.Fatalf("Async did not retain exactly one coroutine primary:\n%s", ir)
			}
			if strings.Contains(ir, "__llgo_coro_spawn_plain_adapter") {
				t.Fatalf("spawn target incorrectly gained a second plain-root adapter body:\n%s", ir)
			}

			index := func(pattern string) int {
				match := regexp.MustCompile(pattern).FindStringIndex(parent)
				if match == nil {
					return -1
				}
				return match[0]
			}
			first := index(`call i32 @"?foo\.ArgFirst"?`)
			second := index(`call i32 @"?foo\.ArgSecond"?`)
			begin := strings.Index(parent, "call ptr @"+coroSpawnBeginHookV1)
			plainRoot := -1
			if begin >= 0 {
				if relative := regexp.MustCompile(`call ptr @"?foo\.Plain\$coro"?\(`).FindStringIndex(parent[begin:]); relative != nil {
					plainRoot = begin + relative[0]
				}
			}
			commit := strings.Index(parent, "call void @"+coroSpawnCommitHookV1)
			poll := strings.Index(parent, "call i1 @"+coroPreemptPollHookV1)
			if first < 0 || second < 0 || begin < 0 || plainRoot < 0 || commit < 0 || poll < 0 ||
				!(first < second && second < begin && begin < plainRoot && plainRoot < commit && commit < poll) {
				t.Fatalf("argument/begin/root/commit/safepoint order is invalid:\n%s", parent)
			}
			if got := strings.Count(parent, "call ptr @"+coroSpawnBeginHookV1); got != 2 {
				t.Fatalf("spawn begin calls = %d, want two:\n%s", got, parent)
			}
			if got := strings.Count(parent, "call void @"+coroSpawnCommitHookV1); got != 2 {
				t.Fatalf("spawn commit calls = %d, want two:\n%s", got, parent)
			}
			if got := strings.Count(parent, "call i1 @"+coroPreemptPollHookV1); got != 2 {
				t.Fatalf("post-commit explicit preempt polls = %d, want two:\n%s", got, parent)
			}
			if got := strings.Count(parent, "call void @"+coroYieldPrepareHookV1); got != 2 {
				t.Fatalf("post-commit parent yield handoffs = %d, want two:\n%s", got, parent)
			}
			if !regexp.MustCompile(`call ptr @"?foo\.Async\$coro"?\(`).MatchString(parent) {
				t.Fatalf("suspendable target is not called through its unique physical root:\n%s", parent)
			}
			if strings.Contains(parent[begin:commit], "@llvm.coro.promise") {
				t.Fatalf("independent spawned G incorrectly received an await parent-handle link:\n%s", parent[begin:commit])
			}
			if got := len(regexp.MustCompile(`call ptr @"?foo\.Plain\$coro"?\(`).FindAllStringIndex(parent, -1)); got != 2 {
				t.Fatalf("sync await + spawn calls to the one Plain primary = %d, want two:\n%s", got, parent)
			}
			for _, forbidden := range []string{"CreateThread", "InitThreadAttr", "DestroyThreadAttr", "._llgo_routine$", "pthread", "AllocRoot"} {
				if strings.Contains(ir, forbidden) {
					t.Fatalf("closed static spawn leaked legacy native-stack lowering %q:\n%s", forbidden, ir)
				}
			}

			runCoroABITestPipeline(t, prog, module)
			for _, name := range []string{"foo.Parent$coro", "foo.Plain$coro", "foo.Async$coro"} {
				for _, suffix := range []string{".resume", ".destroy"} {
					if module.NamedFunction(name + suffix).IsNil() {
						t.Fatalf("CoroSplit did not create %s%s:\n%s", name, suffix, module.String())
					}
				}
			}
			for _, intrinsic := range []string{"llvm.coro.id", "llvm.coro.begin", "llvm.coro.suspend", "llvm.coro.end"} {
				if hasLLVMCall(module.String(), intrinsic) {
					t.Fatalf("post-split spawn module still calls %s:\n%s", intrinsic, module.String())
				}
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit post-CoroSplit spawn object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			for _, symbol := range []string{coroSpawnBeginHookV1, coroSpawnCommitHookV1, "foo.Plain$coro"} {
				if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(symbol)) {
					t.Fatalf("post-CoroSplit object lost spawn symbol %q", symbol)
				}
			}
		})
	}
}

func TestCoroManagedDispatchSpawnNativeAndWasm32CoroSplit(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, _, launcherTarget, callbackTarget := compileCoroManagedDispatchSpawnFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify managed descriptor spawn before CoroSplit: %v\n%s", err, module.String())
			}
			for _, target := range []*ssa.Function{launcherTarget, callbackTarget} {
				targetPlan, _ := plan.FunctionPlan(target)
				if targetPlan.Emission != coro.EmitCoroutine || targetPlan.Primary != coro.PrimaryCoroutine ||
					targetPlan.FuncRep != coro.Dispatch || targetPlan.Demand != coro.AsyncDemand ||
					!targetPlan.Effect.Contains(coro.YieldOnly) {
					t.Fatalf("captured descriptor target %s plan = %+v", target, targetPlan)
				}
			}

			launcherIR := requireCoroPhysicalFunction(t, module, launcherTarget.String()).String()
			indirectCoro := regexp.MustCompile(`call ptr %[-a-zA-Z$._0-9]+\(ptr [^,]+, ptr null, ptr [^,]+, i(?:32|64) [^)]+\)`)
			argumentMatch := regexp.MustCompile(`add i(?:32|64)`).FindStringIndex(launcherIR)
			argument := -1
			if argumentMatch != nil {
				argument = argumentMatch[0]
			}
			begin := strings.Index(launcherIR, "call ptr @"+coroSpawnBeginHookV1)
			indirect := indirectCoro.FindStringIndex(launcherIR)
			commit := strings.Index(launcherIR, "call void @"+coroSpawnCommitHookV1)
			poll := strings.Index(launcherIR, "call i1 @"+coroPreemptPollHookV1)
			if argument < 0 || begin < 0 || indirect == nil || commit < 0 || poll < 0 ||
				!(argument < begin && begin < indirect[0] && indirect[0] < commit && commit < poll) {
				t.Fatalf("captured launcher callee/argument/begin/descriptor/commit/poll order is invalid:\n%s", launcherIR)
			}
			if got := strings.Count(launcherIR, "call ptr @"+coroSpawnBeginHookV1); got != 1 {
				t.Fatalf("captured launcher spawn begin calls = %d, want one:\n%s", got, launcherIR)
			}
			if got := strings.Count(launcherIR, "call void @"+coroSpawnCommitHookV1); got != 1 {
				t.Fatalf("captured launcher spawn commit calls = %d, want one:\n%s", got, launcherIR)
			}
			if strings.Count(launcherIR, "call void @"+coroFaultPrepareHookV1) < 2 {
				t.Fatalf("captured launcher FreeVar cell loads lack explicit nil-fault edges:\n%s", launcherIR)
			}
			if !strings.Contains(launcherIR, "coro.dispatch.capability.missing") ||
				!strings.Contains(launcherIR, "call void @llvm.trap()") {
				t.Fatalf("managed spawn does not fail closed on a plain-only/corrupt descriptor:\n%s", launcherIR)
			}
			for _, forbidden := range []string{"CreateThread", "pthread", "._llgo_routine$", "@llvm.coro.promise"} {
				if strings.Contains(launcherIR, forbidden) {
					t.Fatalf("captured launcher managed spawn leaked forbidden path %q:\n%s", forbidden, launcherIR)
				}
			}
			if !strings.Contains(module.String(), coroCoroDispatchThunkPrefix) {
				t.Fatalf("captured goroutine descriptor has no coroutine thunk:\n%s", module.String())
			}

			runCoroABITestPipeline(t, prog, module)
			for _, name := range []string{launcherTarget.String() + coroPrimarySuffix, callbackTarget.String() + coroPrimarySuffix} {
				for _, suffix := range []string{".resume", ".destroy"} {
					if module.NamedFunction(name + suffix).IsNil() {
						t.Fatalf("CoroSplit did not create %s%s:\n%s", name, suffix, module.String())
					}
				}
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify managed descriptor spawn after CoroSplit: %v\n%s", err, module.String())
			}
		})
	}
}

func TestCoroClosedStaticMethodSpawnNativeAndWasm32CoroSplit(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, ssaPkg, method, spawn := compileCoroClosedStaticMethodSpawnFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify closed static method spawn before CoroSplit: %v\n%s", err, module.String())
			}
			parentPlan, _ := plan.FunctionPlan(ssaPkg.Func("Parent"))
			methodPlan, _ := plan.FunctionPlan(method)
			for name, function := range map[string]coro.FunctionPlan{"Parent": parentPlan, "Run": methodPlan} {
				if function.Emission != coro.EmitCoroutine || function.Primary != coro.PrimaryCoroutine ||
					function.FuncRep != coro.DirectCoro || function.Demand != coro.AsyncDemand ||
					!function.Effect.Contains(coro.YieldOnly) {
					t.Fatalf("%s plan = %+v", name, function)
				}
			}
			if _, _, err := resolveCoroDirectStaticSpawn(plan, spawn, false); err == nil ||
				!strings.Contains(err.Error(), "universal descriptor transport") {
				t.Fatalf("method callback gate-off error = %v", err)
			}
			if resolved, _, err := resolveCoroDirectStaticSpawn(plan, spawn, true); err != nil || resolved != method {
				t.Fatalf("resolve method spawn with descriptor transport = %v, %v", resolved, err)
			}
			callbackPlan, found := plan.ValuePlan(spawn.Common().Args[1])
			if !found || len(callbackPlan.Funcs) != 1 || len(callbackPlan.Funcs[0].Path) != 0 ||
				callbackPlan.Funcs[0].Rep != coro.Dispatch {
				t.Fatalf("method callback ValuePlan = %+v, present=%t", callbackPlan, found)
			}

			parentIR := requireCoroPhysicalFunction(t, module, "foo.Parent").String()
			methodName := funcName(ssaPkg.Pkg, method, false) + coroPrimarySuffix
			methodIR := module.NamedFunction(methodName)
			if methodIR.IsNil() {
				t.Fatalf("method spawn target %q is absent:\n%s", methodName, module.String())
			}
			if !regexp.MustCompile(`define ptr @"?` + regexp.QuoteMeta(methodName) + `"?\(ptr [^,]+, ptr [^,]+, i(?:32|64) [^,]+, \{ ptr, ptr \} [^,]+, i(?:32|64) `).MatchString(methodIR.String()) {
				t.Fatalf("method physical receiver/callback/argument ABI is not normalized descriptor transport:\n%s", methodIR.String())
			}
			index := func(pattern string) int {
				match := regexp.MustCompile(pattern).FindStringIndex(parentIR)
				if match == nil {
					return -1
				}
				return match[0]
			}
			receiver := index(`call i(?:32|64) @"?foo\.Receiver"?`)
			argument := index(`call i(?:32|64) @"?foo\.Argument"?`)
			begin := strings.Index(parentIR, "call ptr @"+coroSpawnBeginHookV1)
			methodCall := index(`call ptr @"?` + regexp.QuoteMeta(methodName) + `"?\([^\n]*\{ ptr, ptr \}`)
			commit := strings.Index(parentIR, "call void @"+coroSpawnCommitHookV1)
			poll := strings.Index(parentIR, "call i1 @"+coroPreemptPollHookV1)
			if receiver < 0 || argument < 0 || begin < 0 || methodCall < 0 || commit < 0 || poll < 0 ||
				!(receiver < argument && argument < begin && begin < methodCall && methodCall < commit && commit < poll) {
				t.Fatalf("receiver/arguments/begin/method/commit/poll order is invalid:\n%s", parentIR)
			}
			for _, forbidden := range []string{"CreateThread", "pthread", "._llgo_routine$", "@llvm.coro.promise"} {
				if strings.Contains(parentIR, forbidden) {
					t.Fatalf("method spawn leaked forbidden path %q:\n%s", forbidden, parentIR)
				}
			}

			runCoroABITestPipeline(t, prog, module)
			for _, name := range []string{"foo.Parent" + coroPrimarySuffix, methodName} {
				for _, suffix := range []string{".resume", ".destroy"} {
					if module.NamedFunction(name + suffix).IsNil() {
						t.Fatalf("CoroSplit did not create %s%s:\n%s", name, suffix, module.String())
					}
				}
			}
		})
	}
}

func TestCoroClosedStaticSpawnFunctionArgumentsAreTransportAware(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, ssaPkg, rawSpawn, mixedSpawn := compileCoroStaticSpawnTransportFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			if resolved, _, err := resolveCoroDirectStaticSpawn(plan, rawSpawn, false); err != nil || resolved != ssaPkg.Func("RawTarget") {
				t.Fatalf("raw-only static spawn without descriptor capability = %v, %v", resolved, err)
			}
			if _, _, err := resolveCoroDirectStaticSpawn(plan, mixedSpawn, false); err == nil ||
				!strings.Contains(err.Error(), "managed function leaf") {
				t.Fatalf("mixed static spawn gate-off error = %v", err)
			}
			if resolved, _, err := resolveCoroDirectStaticSpawn(plan, mixedSpawn, true); err != nil || resolved != ssaPkg.Func("MixedTarget") {
				t.Fatalf("mixed static spawn with descriptor capability = %v, %v", resolved, err)
			}

			rawArgumentPlan, found := plan.ValuePlan(rawSpawn.Common().Args[0])
			if !found || len(rawArgumentPlan.Funcs) != 1 ||
				rawArgumentPlan.Funcs[0].Transport != coro.RawCCodePointer ||
				rawArgumentPlan.Funcs[0].Rep != coro.DirectPlain {
				t.Fatalf("raw spawn argument ValuePlan = %+v, present=%t", rawArgumentPlan, found)
			}
			mixedArgumentPlan, found := plan.ValuePlan(mixedSpawn.Common().Args[2])
			if !found || len(mixedArgumentPlan.Funcs) != 2 ||
				mixedArgumentPlan.Funcs[0].Transport != coro.RawCCodePointer ||
				mixedArgumentPlan.Funcs[0].Rep != coro.DirectPlain ||
				mixedArgumentPlan.Funcs[1].Transport != coro.ManagedTransport ||
				mixedArgumentPlan.Funcs[1].Rep != coro.Dispatch {
				t.Fatalf("mixed spawn argument ValuePlan = %+v, present=%t", mixedArgumentPlan, found)
			}

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify transport-aware static spawn before CoroSplit: %v\n%s", err, module.String())
			}
			rawTargetIR := requireCoroPhysicalFunction(t, module, "foo.RawTarget").String()
			mixedTargetIR := requireCoroPhysicalFunction(t, module, "foo.MixedTarget").String()
			parentIR := requireCoroPhysicalFunction(t, module, "foo.Parent").String()
			if !regexp.MustCompile(`define ptr @"?foo\.RawTarget\$coro"?\(ptr [^,]+, ptr [^,]+, ptr `).MatchString(rawTargetIR) {
				t.Fatalf("raw C spawn parameter is not one physical code pointer:\n%s", rawTargetIR)
			}
			if !regexp.MustCompile(`%"?foo\.Mixed"? = type \{ ptr, \{ ptr, ptr \} \}`).MatchString(module.String()) ||
				!regexp.MustCompile(`define ptr @"?foo\.MixedTarget\$coro"?\(ptr [^,]+, ptr [^,]+, ptr [^,]+, \{ ptr, ptr \} [^,]+, %"?foo\.Mixed"? `).MatchString(mixedTargetIR) {
				t.Fatalf("mixed spawn target did not preserve raw/managed leaf layout:\n%s", mixedTargetIR)
			}
			if !regexp.MustCompile(`call ptr @"?foo\.RawTarget\$coro"?\(ptr [^,]+, ptr null, ptr `).MatchString(parentIR) ||
				!regexp.MustCompile(`call ptr @"?foo\.MixedTarget\$coro"?\(ptr [^,]+, ptr null, ptr [^,]+, \{ ptr, ptr \} [^,]+, %"?foo\.Mixed"? `).MatchString(parentIR) {
				t.Fatalf("static spawn calls did not pass the planned physical layouts:\n%s", parentIR)
			}

			runCoroABITestPipeline(t, prog, module)
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify transport-aware static spawn after CoroSplit: %v\n%s", err, module.String())
			}
		})
	}
}

func compileCoroStaticSpawnTransportFixture(t *testing.T, target *llssa.Target) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Package, *ssa.Go, *ssa.Go,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroStaticSpawnTransportTestSource)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	// Mirror production import ordering: //llgo:type metadata must be installed
	// before the emission universe freezes the C function-value transport.
	ParsePkgSyntax(prog, ssaPkg.Pkg, files)
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
	parent := ssaPkg.Func("Parent")
	rawTarget := ssaPkg.Func("RawTarget")
	mixedTarget := ssaPkg.Func("MixedTarget")
	rawCallee := ssaPkg.Func("RawCallee")
	var rawSpawn, mixedSpawn, rawCalleeSpawn *ssa.Go
	for _, block := range parent.Blocks {
		for _, instruction := range block.Instrs {
			spawn, ok := instruction.(*ssa.Go)
			if !ok || spawn.Common() == nil || spawn.Common().StaticCallee() == nil {
				continue
			}
			switch spawn.Common().StaticCallee() {
			case rawTarget:
				rawSpawn = spawn
			case mixedTarget:
				mixedSpawn = spawn
			}
		}
	}
	for _, block := range rawCallee.Blocks {
		for _, instruction := range block.Instrs {
			if spawn, ok := instruction.(*ssa.Go); ok {
				rawCalleeSpawn = spawn
			}
		}
	}
	if rawSpawn == nil || mixedSpawn == nil || rawCalleeSpawn == nil {
		prog.Dispose()
		t.Fatal("transport-aware static spawn fixture is incomplete")
	}

	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	config := coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == parent || fn == rawTarget || fn == mixedTarget || fn == rawCallee {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyUnknownCall: func(caller *ssa.Function, call ssa.CallInstruction) (coro.UnknownTarget, error) {
			if caller == rawCallee && call == rawCalleeSpawn {
				return coro.UnknownForeign, nil
			}
			return coro.UnknownManaged, nil
		},
		ClassifyRawCFunctionType: func(typ types.Type) (bool, error) {
			return prog.TypeBackground(typ) == llssa.InC, nil
		},
	}
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: parent, Demand: coro.AsyncDemand}}, config)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	rawCalleePlan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: rawCallee, Demand: coro.AsyncDemand}}, config)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	if callPlan, found := rawCalleePlan.CallPlan(rawCalleeSpawn); !found ||
		callPlan.Transport != coro.RawCCodePointer || callPlan.Rep != coro.DirectPlain {
		prog.Dispose()
		t.Fatalf("raw C callee spawn CallPlan = %+v, present=%t", callPlan, found)
	}
	if _, _, err := resolveCoroDirectStaticSpawn(rawCalleePlan, rawCalleeSpawn, true); err == nil ||
		!strings.Contains(err.Error(), "raw C code-pointer callee") {
		prog.Dispose()
		t.Fatalf("raw C callee spawn rejection = %v", err)
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	compilation.CoroProfile = CoroProfileStackless
	compilation.CoroProfile = CoroProfileStackless
	compilation.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	compilation.FuncRepABI = coro.FuncRepABIV1
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, ssaPkg, rawSpawn, mixedSpawn
}

func compileCoroClosedStaticMethodSpawnFixture(t *testing.T, target *llssa.Target) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Package, *ssa.Function, *ssa.Go,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroClosedStaticMethodSpawnTestSource)
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
	parent := ssaPkg.Func("Parent")
	var spawn *ssa.Go
	for _, block := range parent.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.Go); ok {
				spawn = candidate
			}
		}
	}
	if spawn == nil || spawn.Common() == nil {
		prog.Dispose()
		t.Fatal("method spawn fixture has no goroutine call")
	}
	method := spawn.Common().StaticCallee()
	if method == nil || method.Signature == nil || method.Signature.Recv() == nil {
		prog.Dispose()
		t.Fatalf("method spawn target = %v", method)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: parent, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == parent || fn == method {
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
	enableCoroPreemptCompilation(compilation)
	compilation.CoroProfile = CoroProfileStackless
	compilation.CoroProfile = CoroProfileStackless
	compilation.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	compilation.FuncRepABI = coro.FuncRepABIV1
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, ssaPkg, method, spawn
}

func compileCoroManagedDispatchSpawnFixture(t *testing.T, target *llssa.Target) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Package, *ssa.Function, *ssa.Function,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroManagedDispatchSpawnTestSource)
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
	makeCallback, makeLauncher := ssaPkg.Func("MakeCallback"), ssaPkg.Func("MakeLauncher")
	var launcherTarget, callbackTarget *ssa.Function
	for _, owner := range []*ssa.Function{makeCallback, makeLauncher} {
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				closure, ok := instruction.(*ssa.MakeClosure)
				if !ok {
					continue
				}
				closureTarget, ok := closure.Fn.(*ssa.Function)
				if !ok {
					prog.Dispose()
					t.Fatalf("captured descriptor target = %T", closure.Fn)
				}
				if owner == makeLauncher {
					launcherTarget = closureTarget
				} else {
					callbackTarget = closureTarget
				}
			}
		}
	}
	var launcherSpawn *ssa.Go
	if launcherTarget != nil {
		for _, block := range launcherTarget.Blocks {
			for _, instruction := range block.Instrs {
				if spawn, ok := instruction.(*ssa.Go); ok {
					launcherSpawn = spawn
				}
			}
		}
	}
	if launcherSpawn == nil || launcherTarget == nil || callbackTarget == nil {
		prog.Dispose()
		t.Fatal("managed descriptor spawn fixture is incomplete")
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: makeCallback, Demand: coro.SyncDemand},
		{Function: makeLauncher, Demand: coro.SyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == launcherTarget || fn == callbackTarget {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyUnknownCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.UnknownTarget, error) {
			if call == launcherSpawn {
				return coro.UnknownManagedDispatch, nil
			}
			return coro.UnknownManaged, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	if _, err := plan.ResolveManagedDispatchSpawn(launcherSpawn); err != nil {
		prog.Dispose()
		t.Fatalf("resolve managed descriptor spawn: %v", err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	compilation.CoroProfile = CoroProfileStackless
	compilation.CoroProfile = CoroProfileStackless
	compilation.CoroProfile = CoroProfileStackless
	compilation.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
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
	return prog, pkg, plan, ssaPkg, launcherTarget, callbackTarget
}

func compileCoroClosedStaticSpawnFixture(t *testing.T, target *llssa.Target) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Package,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroClosedStaticSpawnTestSource)
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
	parent, plain, async := ssaPkg.Func("Parent"), ssaPkg.Func("Plain"), ssaPkg.Func("Async")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: parent, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == parent || fn == plain || fn == async {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{
		CoroPlan:         plan,
		EmissionUniverse: universe,

		CoroABI:      coro.PhysicalABIV1,
		SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
		PanicABI:     coro.PanicExplicitStatusABIV0,
		FuncRepABI:   coro.FuncRepABIV1, CoroProfile: CoroProfileStackless,
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, ssaPkg
}

func TestCoroClosedStaticSpawnCompilationCapabilityFailsClosed(t *testing.T) {
	compilation := &Compilation{CoroProfile: CoroProfileStackless}
	if !compilation.CoroClosedStaticSpawnActive() || !compilation.CoroProgramBootstrapActive() || !compilation.CoroChildAwaitActive() {
		t.Fatal("stackless profile did not activate spawn, bootstrap, and child-await as one contract")
	}
}
