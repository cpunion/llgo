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
	"regexp"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroLeafPhysicalABIPresplit(t *testing.T) {
	prog, pkg := compileCoroLeafPhysicalABI(t, nil)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify coroutine leaf: %v\n%s", err, module.String())
	}
	ir := module.String()
	if module.NamedFunction("foo.Leaf").IsNil() == false {
		t.Fatalf("physical coroutine retained legacy source ABI symbol:\n%s", ir)
	}
	leaf := module.NamedFunction("foo.Leaf$coro")
	if leaf.IsNil() {
		t.Fatalf("physical coroutine symbol is absent:\n%s", ir)
	}
	leafIR := leaf.String()
	if !regexp.MustCompile(`define ptr @"?foo\.Leaf\$coro"?\(ptr [^,]+, ptr [^,]+, i32 `).MatchString(leafIR) {
		t.Fatalf("coroutine leaf does not use (g, out, args...) -> handle ABI:\n%s", leafIR)
	}
	if got := strings.Count(leafIR, "call i8 @llvm.coro.suspend"); got != 2 {
		t.Fatalf("coro.suspend calls = %d, want initial + final:\n%s", got, leafIR)
	}
	begin := strings.Index(leafIR, "call ptr @llvm.coro.begin")
	firstStore := strings.Index(leafIR, "store ")
	initialSuspend := strings.Index(leafIR, "call i8 @llvm.coro.suspend")
	if begin < 0 || firstStore < 0 || initialSuspend < 0 || !(begin < firstStore && firstStore < initialSuspend) {
		t.Fatalf("promise/header was not published after coro.begin and before initial suspend:\n%s", leafIR)
	}
	if !strings.Contains(leafIR, "store i32") {
		t.Fatalf("coroutine result was not copied to the external result slot:\n%s", leafIR)
	}
	for _, symbol := range []string{coroFrameAllocHook, coroFrameFreeHook, coroDescriptorPrefix} {
		if !strings.Contains(ir, symbol) {
			t.Fatalf("coroutine module is missing versioned ABI symbol %q:\n%s", symbol, ir)
		}
	}
	for _, forbidden := range []string{"@malloc", "@free(", "stacksave", "stackrestore", "pthread"} {
		if strings.Contains(ir, forbidden) {
			t.Fatalf("coroutine leaf introduced forbidden stack/runtime coupling %q:\n%s", forbidden, ir)
		}
	}
}

func TestCoroLeafPhysicalABIZeroResult(t *testing.T) {
	prog, pkg := compileCoroLeafPhysicalABISource(t, nil, `package foo
func Leaf() {}
`)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify zero-result coroutine leaf: %v\n%s", err, module.String())
	}
	leaf := module.NamedFunction("foo.Leaf$coro")
	if leaf.IsNil() || !regexp.MustCompile(`define ptr @"?foo\.Leaf\$coro"?\(ptr [^,]+, ptr [^)]+\)`).MatchString(leaf.String()) {
		t.Fatalf("zero-result coroutine has the wrong physical ABI:\n%s", module.String())
	}
}

func TestCoroLeafPhysicalABICoroSplit(t *testing.T) {
	prog, pkg := compileCoroLeafPhysicalABI(t, nil)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify before CoroSplit: %v\n%s", err, module.String())
	}
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	const pipeline = "coro-early,cgscc(coro-split),coro-cleanup"
	if err := module.RunPasses(pipeline, prog.TargetMachine(), options); err != nil {
		t.Fatalf("run %s: %v\n%s", pipeline, err, module.String())
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify after CoroSplit: %v\n%s", err, module.String())
	}
	ir := module.String()
	for _, suffix := range []string{".resume", ".destroy"} {
		if module.NamedFunction("foo.Leaf$coro" + suffix).IsNil() {
			t.Fatalf("CoroSplit did not create coroutine %s entry:\n%s", suffix, ir)
		}
	}
	for _, intrinsic := range []string{"llvm.coro.id", "llvm.coro.begin", "llvm.coro.suspend", "llvm.coro.end"} {
		if regexp.MustCompile(`call [^\n]*@` + regexp.QuoteMeta(intrinsic) + `\b`).MatchString(ir) {
			t.Fatalf("post-split module still calls %s:\n%s", intrinsic, ir)
		}
	}
	if !strings.Contains(module.NamedFunction("foo.Leaf$coro.resume").String(), "store i32") {
		t.Fatalf("result-slot store did not move to the resume function:\n%s", ir)
	}
}

func TestCoroLeafPhysicalABIGlobalDebug(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkgWithMode(t, `package foo
func Leaf(value uint32) uint32 {
	next := value + 1
	return next
}
`, ssa.SanityCheckFunctions|ssa.InstantiateGenerics|ssa.GlobalDebug)
	leafSSA := ssaPkg.Func("Leaf")
	foundDebugRef := false
	for _, block := range leafSSA.Blocks {
		for _, instruction := range block.Instrs {
			if _, ok := instruction.(*ssa.DebugRef); ok {
				foundDebugRef = true
			}
		}
	}
	if !foundDebugRef {
		t.Fatal("GlobalDebug SSA did not contain a DebugRef")
	}

	oldDebug, oldDebugSyms := enableDbg, enableDbgSyms
	EnableDebug(true)
	EnableDbgSyms(true)
	defer func() {
		EnableDebug(oldDebug)
		EnableDbgSyms(oldDebugSyms)
	}()
	prog, pkg := compileCoroLeafPhysicalABIPackage(t, nil, ssaPkg, files)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify debug coroutine before CoroSplit: %v\n%s", err, module.String())
	}
	if !strings.Contains(module.String(), "!dbg") {
		t.Fatalf("debug coroutine omitted function/parameter metadata:\n%s", module.String())
	}
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	if err := module.RunPasses("coro-early,cgscc(coro-split),coro-cleanup", prog.TargetMachine(), options); err != nil {
		t.Fatalf("CoroSplit debug coroutine: %v\n%s", err, module.String())
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify debug coroutine after CoroSplit: %v\n%s", err, module.String())
	}
}

func TestCoroLeafPhysicalABIUsesTargetPointerWidth(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog, pkg := compileCoroLeafPhysicalABI(t, &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	if got := prog.PointerSize(); got != 4 {
		t.Fatalf("wasm pointer size = %d, want 4", got)
	}
	ir := module.String()
	for _, intrinsic := range []string{"size", "align"} {
		if !strings.Contains(ir, "@llvm.coro."+intrinsic+".i32") {
			t.Fatalf("wasm coroutine uses non-i32 %s intrinsic:\n%s", intrinsic, ir)
		}
	}
	if !regexp.MustCompile(`@__llgo_coro_frame_descriptor_v0\.[0-9a-f]+ = linkonce_odr unnamed_addr constant \{ i32, i32, i64, i64, i32, i32 \}`).MatchString(ir) {
		t.Fatalf("wasm descriptor does not use target-width size/alignment fields:\n%s", ir)
	}
}

func TestCoroLeafPhysicalABIPreflightRejectsUnsupported(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "pointer parameter",
			source: `package foo; func Leaf(value *int) {}`,
			want:   "parameter 0 has unsupported type *int",
		},
		{
			name: "control flow",
			source: `package foo
func Leaf(value uint32) uint32 {
	if value == 0 { return 1 }
	return value
}`,
			want: "requires exactly one basic block",
		},
		{
			name: "call",
			source: `package foo
func Plain(value uint32) uint32 { return value }
func Leaf(value uint32) uint32 { return Plain(value) }`,
			want: "outside the ABI-only leaf allowlist",
		},
		{
			name: "spawn consumer",
			source: `package foo
func Leaf(value uint32) uint32 { return value + 1 }
func Launch() { go Leaf(1) }`,
			want: "goroutine spawn requires scheduler root lowering",
		},
		{
			name: "channel operation",
			source: `package foo
func Leaf(channel chan uint32) uint32 { return <-channel }`,
			want: "requires an explicit, isolated yield-only effect",
		},
		{
			name: "foreign ABI directive",
			source: `package foo
//export Leaf
func Leaf(value uint32) uint32 { return value + 1 }`,
			want: "ABI directive",
		},
		{
			name: "multiple results",
			source: `package foo
func Leaf(value uint32) (uint32, uint32) { return value, value }`,
			want: "supports at most one result",
		},
		{
			name: "shift requires hidden panic check",
			source: `package foo
func Leaf(value uint32, shift int) uint32 { return value << shift }`,
			want: "potentially panicking or non-scalar binary operation",
		},
		{
			name: "nested function literal",
			source: `package foo
func Leaf(value uint32) uint32 {
	_ = func() {}
	return value + 1
}`,
			want: "nested function literals require closure body lowering",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			ssaPkg, _, files := buildGoSSAPkg(t, test.source)
			universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
			if err != nil {
				t.Fatal(err)
			}
			leaf := ssaPkg.Func("Leaf")
			plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: leaf, Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse:     ssaUniverse,
				FunctionIDs:          universe.FunctionIDConfig(),
				MaxPlainInstructions: -1,
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					if fn == leaf {
						return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
					}
					return coro.SSAFunctionPolicy{}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			observerCalls := 0
			got, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{
				Compilation: &Compilation{
					CoroPlan:                  plan,
					EmissionUniverse:          universe,
					CoroPlanObserver:          func(*ssa.Package, *coro.SSAPlan) { observerCalls++ },
					EnableCoroEntryResolution: true,
					EnableCoroPhysicalABI:     true,
				},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("preflight result = %v, %v; want error containing %q", got, err, test.want)
			}
			if got != nil {
				t.Fatal("preflight failure returned a partial package")
			}
			if observerCalls != 0 {
				t.Fatalf("observer calls = %d, want pre-codegen rejection", observerCalls)
			}
		})
	}
}

func TestCoroPhysicalABIRequiresEntryResolution(t *testing.T) {
	err := (&Compilation{EnableCoroPhysicalABI: true}).preflightCoroPlan()
	if err == nil || !strings.Contains(err.Error(), "requires coroutine entry resolution") {
		t.Fatalf("preflight error = %v, want entry-resolution requirement", err)
	}
}

func compileCoroLeafPhysicalABI(t *testing.T, target *llssa.Target) (llssa.Program, llssa.Package) {
	t.Helper()
	return compileCoroLeafPhysicalABISource(t, target, `package foo
func Leaf(value uint32) uint32 { return value + 1 }
`)
}

func compileCoroLeafPhysicalABISource(t *testing.T, target *llssa.Target, source string) (llssa.Program, llssa.Package) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	return compileCoroLeafPhysicalABIPackage(t, target, ssaPkg, files)
}

func compileCoroLeafPhysicalABIPackage(t *testing.T, target *llssa.Target, ssaPkg *ssa.Package, files []*ast.File) (llssa.Program, llssa.Package) {
	t.Helper()
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
	leaf := ssaPkg.Func("Leaf")
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: leaf, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          universe.FunctionIDConfig(),
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == leaf {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	pkg, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{
		Compilation: &Compilation{
			CoroPlan:                  plan,
			EmissionUniverse:          universe,
			EnableCoroEntryResolution: true,
			EnableCoroPhysicalABI:     true,
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg
}
