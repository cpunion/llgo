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
	"regexp"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroExplicitStatusPanicFixture = `package foo

var FirstPayload uint32
var SecondPayload uint32
var InterfacePayload any = &FirstPayload

func Root(mode uint32) uint32 {
	if mode == 0 {
		return 11
	}
	if mode == 1 {
		panic(&FirstPayload)
	}
	if mode == 2 {
		return 13
	}
	if mode == 3 {
		panic(InterfacePayload)
	}
	panic(&SecondPayload)
}
`

func TestCoroExplicitStatusPanicNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, root := compileCoroExplicitStatusPanicFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			rootPlan, ok := plan.FunctionPlan(root)
			if !ok || rootPlan.Emission != coro.EmitCoroutine || rootPlan.FuncRep != coro.DirectCoro ||
				rootPlan.Demand != coro.AsyncDemand || !rootPlan.Exec.Contains(coro.MayUnwind) {
				t.Fatalf("Root plan = %+v, present=%t; want may-unwind direct coroutine", rootPlan, ok)
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify explicit-status panic before CoroSplit: %v\n%s", err, module.String())
			}
			body := requireCoroPhysicalFunction(t, module, "foo.Root").String()
			assertCoroExplicitStatusPanicBody(t, body, 3)
			assertNoLegacyCoroPanicSymbol(t, module.String())

			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.Root$coro.resume")
			if resume.IsNil() {
				t.Fatalf("CoroSplit did not create Root resume entry:\n%s", module.String())
			}
			if got := strings.Count(resume.String(), "call void @"+coroPanicPrepareHookV1); got != 3 {
				t.Fatalf("Root.resume panic prepare calls = %d, want 3:\n%s", got, resume.String())
			}
			assertNoLegacyCoroPanicSymbol(t, module.String())
			for _, intrinsic := range []string{"llvm.coro.id", "llvm.coro.begin", "llvm.coro.suspend", "llvm.coro.end"} {
				if hasLLVMCall(module.String(), intrinsic) {
					t.Fatalf("post-split panic module still calls %s:\n%s", intrinsic, module.String())
				}
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit post-CoroSplit panic object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(coroPanicPrepareHookV1)) ||
				!bytes.Contains(object.Bytes(), []byte("foo.Root$coro")) {
				t.Fatal("post-CoroSplit object lost the panic hook or physical coroutine symbol")
			}
		})
	}
}

func assertCoroExplicitStatusPanicBody(t *testing.T, body string, panicSites int) {
	t.Helper()
	if got := strings.Count(body, "call void @"+coroPanicPrepareHookV1); got != panicSites {
		t.Fatalf("panic prepare calls = %d, want %d:\n%s", got, panicSites, body)
	}
	if got := strings.Count(body, "call void @"+coroCompletePrepareHookV2); got != 1 {
		t.Fatalf("completion prepare calls = %d, want one shared normal completion:\n%s", got, body)
	}
	if got := strings.Count(body, "call i8 @llvm.coro.suspend"); got != 2 {
		t.Fatalf("coro.suspend calls = %d, want initial + one shared final:\n%s", got, body)
	}
	if got := strings.Count(body, "@llvm.coro.suspend(token none, i1 true)"); got != 1 {
		t.Fatalf("final coro.suspend calls = %d, want exactly one shared final suspend:\n%s", got, body)
	}
	stateAndHook := regexp.MustCompile(
		`(?s)store i16 5,.*?store i16 4,.*?store i32 [1-9][0-9]*,.*?call void @` + regexp.QuoteMeta(coroPanicPrepareHookV1) +
			`\(ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^)]+\)`,
	)
	if got := len(stateAndHook.FindAllStringIndex(body, -1)); got != panicSites {
		t.Fatalf("Panic/FinalSuspended/stateID publication followed by the five-pointer hook = %d, want %d:\n%s", got, panicSites, body)
	}
	hookBranch := regexp.MustCompile(
		`call void @`+regexp.QuoteMeta(coroPanicPrepareHookV1)+`\([^\n]+\)\n\s+br label (%[-a-zA-Z$._0-9]+)`,
	).FindAllStringSubmatch(body, -1)
	if len(hookBranch) != panicSites {
		t.Fatalf("panic hooks followed immediately by an ordinary branch = %d, want %d (no source panic/unreachable path):\n%s", len(hookBranch), panicSites, body)
	}
	completeBranch := regexp.MustCompile(
		`call void @` + regexp.QuoteMeta(coroCompletePrepareHookV2) + `\([^\n]+\)\n\s+br label (%[-a-zA-Z$._0-9]+)`,
	).FindStringSubmatch(body)
	if len(completeBranch) != 2 {
		t.Fatalf("normal completion does not branch to the shared terminal block:\n%s", body)
	}
	for _, branch := range hookBranch {
		if branch[1] != completeBranch[1] {
			t.Fatalf("panic branch target %s differs from normal completion target %s:\n%s", branch[1], completeBranch[1], body)
		}
	}
	finalSuspend := strings.Index(body, "@llvm.coro.suspend(token none, i1 true)")
	if finalSuspend < 0 {
		t.Fatalf("shared final suspend is absent:\n%s", body)
	}
	for offset := 0; ; {
		relative := strings.Index(body[offset:], "call void @"+coroPanicPrepareHookV1)
		if relative < 0 {
			break
		}
		hook := offset + relative
		if hook >= finalSuspend {
			t.Fatalf("panic hook does not precede the shared final suspend:\n%s", body)
		}
		offset = hook + len(coroPanicPrepareHookV1)
	}
}

func assertNoLegacyCoroPanicSymbol(t *testing.T, ir string) {
	t.Helper()
	for _, forbidden := range []string{"runtime.Panic", "runtime.Rethrow"} {
		if strings.Contains(ir, forbidden) {
			t.Fatalf("explicit-status coroutine retained legacy panic symbol %q:\n%s", forbidden, ir)
		}
	}
}

func compileCoroExplicitStatusPanicFixture(t *testing.T, target *llssa.Target) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroExplicitStatusPanicFixture)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
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
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == root {
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
	compilation.EnableCoroExplicitStatusPanicABI = true
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

func TestCoroExplicitStatusPanicPreflightRemainsFailClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
		exec   coro.ExecFlags
	}{
		{
			name: "dynamic interface operand",
			source: `package foo
func Root(value any, trigger bool) { if trigger { panic(value) } }
`,
			want: "has no post-destroy lifetime proof",
		},
		{
			name: "unproven interface load address",
			source: `package foo
import "unsafe"
func Root(address uintptr, trigger bool) { if trigger { panic(*(*any)(unsafe.Pointer(address))) } }
`,
			want: "uintptr-to-pointer conversion has no traceable exact pointer provenance",
		},
		{
			name: "untyped nil",
			source: `package foo
func Root(trigger bool) { if trigger { panic(nil) } }
`,
			want: "explicit-status panic",
		},
		{
			name: "boxed scalar",
			source: `package foo
func Root(trigger bool) { if trigger { panic(uint32(7)) } }
`,
			want: "structured runtime helper validation requires a frozen emission universe",
		},
		{
			name: "frame local pointer",
			source: `package foo
func Root(trigger bool) { value := uint32(7); if trigger { panic(&value) }; _ = value }
`,
			want: "heap allocation requires managed allocation",
		},
		{
			name: "parameter pointer",
			source: `package foo
func Root(value *uint32, trigger bool) { if trigger { panic(value) } }
`,
			want: "may outlive its coroutine frame",
		},
		{
			name: "cleanup frame",
			source: `package foo
var Payload uint32
func cleanup() {}
func Root(trigger bool) { defer cleanup(); if trigger { panic(&Payload) } }
`,
			want: "execution flags",
			exec: coro.NeedsCleanupFrame,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, _, files := buildGoSSAPkg(t, test.source)
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			root := ssaPkg.Func("Root")
			plan := coro.FunctionPlan{
				ID:            coro.FunctionID("foo.Root"),
				External:      coro.Defined,
				Demand:        coro.AsyncDemand,
				ManagedDemand: coro.AsyncDemand,
				Emission:      coro.EmitCoroutine,
				Primary:       coro.PrimaryCoroutine,
				FuncRep:       coro.DirectCoro,
				Effect:        coro.YieldOnly,
				Exec:          coro.MayUnwind | test.exec,
			}
			err = validateCoroPhysicalABIWithUniverseCapabilities(root, plan, nil, universe, true, false, false, true)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("preflight error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCoroExplicitStatusPanicAcceptsStableClosureInterfaceLoad(t *testing.T) {
	const source = `package foo
type state struct { payload any }
func Root(value *state) {
	inner := func() { panic(value.payload) }
	inner()
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	root := ssaPkg.Func("Root")
	if root == nil {
		t.Fatal("Root function is absent")
	}
	if len(root.AnonFuncs) != 1 {
		t.Fatalf("Root anonymous functions = %d, want one", len(root.AnonFuncs))
	}
	inner := root.AnonFuncs[0]
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == root || function == inner {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	innerPlan, ok := plan.FunctionPlan(inner)
	if !ok || innerPlan.Emission != coro.EmitCoroutine || !innerPlan.Exec.Contains(coro.MayUnwind) {
		t.Fatalf("inner plan = %+v, present=%t; want may-unwind coroutine", innerPlan, ok)
	}
	if err := validateCoroPhysicalABIWithUniverseCapabilities(
		inner, innerPlan, plan, universe, true, false, false, true,
	); err != nil {
		t.Fatalf("stable closure interface load rejected: %v", err)
	}
}

func TestCoroExplicitStatusPanicRejectsPlainCallFromPhysicalBody(t *testing.T) {
	const source = `package foo
var Payload uint32
func Plain(value, divisor uint32) uint32 { return value / divisor }
func Root(value, divisor uint32, trigger bool) uint32 {
	result := Plain(value, divisor)
	if trigger { panic(&Payload) }
	return result
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == root {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plainPlan, ok := plan.FunctionPlan(ssaPkg.Func("Plain"))
	if !ok || !plainPlan.Exec.Contains(coro.MayUnwind) {
		t.Fatalf("Plain plan = %+v, present=%t; want exact unknown-divisor unwind fact", plainPlan, ok)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.EnableCoroExplicitStatusPanicABI = true
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	got, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err == nil || !strings.Contains(err.Error(), "direct plain target") || !strings.Contains(err.Error(), "hidden-outcome/unwind contract") {
		t.Fatalf("plain-call preflight result = %v, %v; want exact hidden-outcome rejection", got, err)
	}
	if got != nil {
		t.Fatal("plain-body preflight failure returned a partial package")
	}
}

func TestCoroExplicitStatusPanicAcceptsExactNoUnwindPlainCall(t *testing.T) {
	const source = `package foo
var Payload uint32
func Plain(value uint32) uint32 { return value + 1 }
func Root(value uint32, trigger bool) uint32 {
	result := Plain(value)
	if trigger { panic(&Payload) }
	return result
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	plain := ssaPkg.Func("Plain")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == root {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plainPlan, ok := plan.FunctionPlan(plain)
	if !ok || plainPlan.Exec.Contains(coro.MayUnwind) {
		t.Fatalf("Plain plan = %+v, present=%t; want exact no-unwind proof", plainPlan, ok)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.EnableCoroExplicitStatusPanicABI = true
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	got, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("exact no-unwind plain call rejected: %v", err)
	}
	if got == nil {
		t.Fatal("exact no-unwind plain call returned no package")
	}
}
