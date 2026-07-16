//go:build !llgo
// +build !llgo

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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	"golang.org/x/tools/go/ssa"
)

const coroEntryTestSource = `package foo

var channel chan int

func Plain() {}
func Coroutine() { <-channel }
func Boxed() {}
func Box() any { return Boxed }
func External()
`

func buildCoroEntryTestPlan(t *testing.T) (*ssa.Package, *coro.SSAPlan) {
	t.Helper()
	pkg, _, _ := buildGoSSAPkg(t, coroEntryTestSource)
	plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{
		{Function: pkg.Func("Plain"), Demand: coro.SyncDemand},
		{Function: pkg.Func("Coroutine"), Demand: coro.AsyncDemand},
		{Function: pkg.Func("Box"), Demand: coro.AsyncDemand},
		{Function: pkg.Func("External"), Demand: coro.SyncDemand},
	}, coro.SSAConfig{
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == pkg.Func("External") {
				return coro.SSAFunctionPolicy{
					Effect:           coro.WaitHost,
					External:         coro.ExternalKnown,
					OverrideExternal: true,
				}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return pkg, plan
}

func newCoroEntryTestContext(t *testing.T, pkg *ssa.Package, compilation *Compilation) (*context, func()) {
	t.Helper()
	prog := newLLSSAProg(t)
	ctx := &context{
		prog:        prog,
		pkg:         prog.NewPackage(pkg.Pkg.Name(), pkg.Pkg.Path()),
		goProg:      pkg.Prog,
		goTyps:      pkg.Pkg,
		goPkg:       pkg,
		compilation: compilation,
	}
	return ctx, prog.Dispose
}

// coroEntryPreflightUniverse is a minimal exact universe for tests that are
// expected to stop in whole-plan preflight before package/codegen validation.
func coroEntryPreflightUniverse(plan *coro.SSAPlan) *EmissionUniverse {
	u := &EmissionUniverse{
		required: make(map[*ssa.Function]none),
		aliases:  make(map[*ssa.Function]*ssa.Function),
	}
	if plan == nil {
		return u
	}
	for _, planned := range plan.Functions() {
		u.functions = append(u.functions, planned.Function)
		u.required[planned.Function] = none{}
	}
	return u
}

func TestResolveFunctionSymbolUsesPrimaryAndExactPlan(t *testing.T) {
	pkg, plan := buildCoroEntryTestPlan(t)
	ctx, dispose := newCoroEntryTestContext(t, pkg, &Compilation{
		CoroPlan:                  plan,
		EnableCoroEntryResolution: true,
	})
	defer dispose()

	plain, err := ctx.resolveFunctionSymbol(pkg.Func("Plain"))
	if err != nil {
		t.Fatal(err)
	}
	if !plain.planned || plain.plan.Emission != coro.EmitPlain || plain.plan.Primary != coro.PrimaryPlain || strings.HasSuffix(plain.name, coroPrimarySuffix) {
		t.Fatalf("plain entry = %+v", plain)
	}
	if err := plain.checkSupported(); err != nil {
		t.Fatalf("plain entry rejected: %v", err)
	}

	coroutine, err := ctx.resolveFunctionSymbol(pkg.Func("Coroutine"))
	if err != nil {
		t.Fatal(err)
	}
	if !coroutine.planned || coroutine.plan.Emission != coro.EmitCoroutine || coroutine.plan.Primary != coro.PrimaryCoroutine || !strings.HasSuffix(coroutine.name, coroPrimarySuffix) {
		t.Fatalf("coroutine entry = %+v", coroutine)
	}
	if err := coroutine.checkSupported(); err == nil || !strings.Contains(err.Error(), "physical ABI") {
		t.Fatalf("coroutine support error = %v", err)
	}

	boxed, err := ctx.resolveFunctionSymbol(pkg.Func("Boxed"))
	if err != nil {
		t.Fatal(err)
	}
	if boxed.plan.Emission != coro.EmitPlain || boxed.plan.Primary != coro.PrimaryPlain || boxed.plan.FuncRep != coro.Dispatch || strings.HasSuffix(boxed.name, coroPrimarySuffix) {
		t.Fatalf("boxed entry = %+v, want one plain primary plus dispatch descriptor", boxed)
	}
	if err := boxed.checkSupported(); err == nil || !strings.Contains(err.Error(), "dispatch descriptor") {
		t.Fatalf("boxed support error = %v", err)
	}

	external, err := ctx.resolveFunctionSymbol(pkg.Func("External"))
	if err != nil {
		t.Fatal(err)
	}
	if external.plan.Emission != coro.EmitExternal || external.plan.Primary != coro.PrimaryExternal || external.plan.FuncRep != coro.DirectCoro {
		t.Fatalf("external entry = %+v, want coroutine external primary", external)
	}
	if err := external.checkSupported(); err == nil || !strings.Contains(err.Error(), "external coroutine") {
		t.Fatalf("external support error = %v", err)
	}

	reportOnlyCtx, reportOnlyDispose := newCoroEntryTestContext(t, pkg, &Compilation{CoroPlan: plan})
	defer reportOnlyDispose()
	reportOnly, err := reportOnlyCtx.resolveFunctionSymbol(pkg.Func("Coroutine"))
	if err != nil {
		t.Fatal(err)
	}
	if reportOnly.planned || strings.HasSuffix(reportOnly.name, coroPrimarySuffix) {
		t.Fatalf("report-only entry = %+v, want unchanged legacy entry", reportOnly)
	}

	otherPkg, _, _ := buildGoSSAPkg(t, coroEntryTestSource)
	otherCtx, otherDispose := newCoroEntryTestContext(t, otherPkg, &Compilation{
		CoroPlan:                  plan,
		EnableCoroEntryResolution: true,
	})
	defer otherDispose()
	if _, err := otherCtx.resolveFunctionSymbol(otherPkg.Func("Plain")); err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("other-program resolution error = %v, want exact-pointer plan miss", err)
	}
}

func TestCoroEntryOmitsUndemandedEffectfulComplexFunction(t *testing.T) {
	const source = `package foo
func Complex(ch chan int) int {
	value := <-ch
	if value == 0 {
		return 1
	}
	return value
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
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, nil, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	complexPlan, ok := plan.FunctionPlan(ssaPkg.Func("Complex"))
	if !ok || complexPlan.Demand != coro.NoDemand || complexPlan.Emission != coro.EmitNone || complexPlan.Primary != coro.PrimaryCoroutine || !complexPlan.Effect.MaySuspend() {
		t.Fatalf("Complex plan = %+v, present=%t; want undemanded, non-emitted logical coroutine", complexPlan, ok)
	}
	compilation := &Compilation{
		CoroPlan:         plan,
		EmissionUniverse: universe,
	}
	enableCoroChildAwaitCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("undemanded complex function blocked package emission: %v", err)
	}
	module := pkg.Module()
	if !module.NamedFunction("foo.Complex").IsNil() || !module.NamedFunction("foo.Complex"+coroPrimarySuffix).IsNil() {
		t.Fatalf("EmitNone function acquired an LLVM symbol:\n%s", module.String())
	}
	ir := module.String()
	for _, marker := range []string{
		coroPrimarySuffix,
		coroDescriptorPrefixV1,
		coroRootFactoryDescriptorPrefix,
		coroRootPackageAnchorPrefix,
	} {
		if strings.Contains(ir, marker) {
			t.Fatalf("EmitNone package unexpectedly contains coroutine marker %q:\n%s", marker, ir)
		}
	}
}

func TestCoroEntryDirectFunctionValueDemandsTargetAndOmitsDeadExternal(t *testing.T) {
	const source = `package foo
func External()
func Target() {}
func Owner() bool { return Target != nil }
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
	owner := ssaPkg.Func("Owner")
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: owner, Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          universe.FunctionIDConfig(),
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	targetPlan, ok := plan.FunctionPlan(ssaPkg.Func("Target"))
	if !ok || targetPlan.Demand != coro.SyncDemand || targetPlan.Emission != coro.EmitPlain || targetPlan.FuncRep != coro.DirectPlain {
		t.Fatalf("Target plan = %+v, present=%t; want demanded direct plain body", targetPlan, ok)
	}
	externalPlan, ok := plan.FunctionPlan(ssaPkg.Func("External"))
	if !ok || externalPlan.Demand != coro.NoDemand || externalPlan.Emission != coro.EmitNone || externalPlan.Primary != coro.PrimaryExternal {
		t.Fatalf("External plan = %+v, present=%t; want non-emitted logical external", externalPlan, ok)
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: &Compilation{
			CoroPlan:                  plan,
			EmissionUniverse:          universe,
			EnableCoroEntryResolution: true,
			EnableCoroPhysicalABI:     true,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	for _, name := range []string{"foo.Owner", "foo.Target"} {
		if module.NamedFunction(name).IsNil() {
			t.Fatalf("missing demanded function %q:\n%s", name, module.String())
		}
	}
	if !module.NamedFunction("foo.External").IsNil() {
		t.Fatalf("dead external acquired an LLVM declaration:\n%s", module.String())
	}
}

func TestCoroEntryDemandedEffectfulComplexFunctionStillFailsClosed(t *testing.T) {
	const source = `package foo
func Complex(ch chan int) int {
	value := <-ch
	if value == 0 {
		return 1
	}
	return value
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
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
	functionIDs.ArchiveReady = true
	complex := ssaPkg.Func("Complex")
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: complex, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	complexPlan, ok := plan.FunctionPlan(complex)
	if !ok || complexPlan.Demand != coro.AsyncDemand || complexPlan.Emission != coro.EmitCoroutine {
		t.Fatalf("Complex plan = %+v, present=%t; want demanded coroutine emission", complexPlan, ok)
	}
	compilation := &Compilation{
		CoroPlan:         plan,
		EmissionUniverse: universe,
	}
	enableCoroChildAwaitCompilation(compilation)
	got, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err == nil || !strings.Contains(err.Error(), "requires exactly one basic block") {
		t.Fatalf("demanded complex preflight = %v, %v; want fail-closed CFG diagnostic", got, err)
	}
	if got != nil {
		t.Fatal("demanded complex preflight returned a partial package")
	}
}

func TestCoroPhysicalConsumerRejectsReferenceToEmitNone(t *testing.T) {
	tests := []struct {
		name        string
		hiddenName  string
		selectInstr func(ssa.Instruction) bool
		want        string
	}{
		{
			name:       "call",
			hiddenName: "HiddenCall",
			selectInstr: func(instr ssa.Instruction) bool {
				_, ok := instr.(*ssa.Call)
				return ok
			},
			want: "non-emitted call target",
		},
		{
			name:       "function value",
			hiddenName: "HiddenValue",
			selectInstr: func(instr ssa.Instruction) bool {
				for _, operand := range instr.Operands(nil) {
					if operand != nil {
						if _, ok := (*operand).(*ssa.Function); ok {
							return true
						}
					}
				}
				return false
			},
			want: "non-emitted function value",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const source = `package foo
func Target() {}
func HiddenCall() { Target() }
func HiddenValue() any { return Target }
func Caller() {}
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
			caller := ssaPkg.Func("Caller")
			plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: caller, Demand: coro.SyncDemand}}, coro.SSAConfig{
				EmissionUniverse:     ssaUniverse,
				FunctionIDs:          universe.FunctionIDConfig(),
				MaxPlainInstructions: -1,
			})
			if err != nil {
				t.Fatal(err)
			}
			targetPlan, ok := plan.FunctionPlan(ssaPkg.Func("Target"))
			if !ok || targetPlan.Emission != coro.EmitNone {
				t.Fatalf("Target plan = %+v, present=%t; want EmitNone before injected inconsistent consumer", targetPlan, ok)
			}
			var injected ssa.Instruction
			for _, block := range ssaPkg.Func(test.hiddenName).Blocks {
				for _, instr := range block.Instrs {
					if test.selectInstr(instr) {
						injected = instr
						break
					}
				}
			}
			if injected == nil {
				t.Fatalf("%s has no instruction suitable for the test", test.hiddenName)
			}
			// Deliberately mutate SSA after the immutable plan was built. This
			// models a stale/mismatched consumer and proves cl will not create a
			// declaration for an EmitNone target.
			caller.Blocks[0].Instrs = append([]ssa.Instruction{injected}, caller.Blocks[0].Instrs...)
			got, _, err := NewPackageExWithEmbedOptions(
				prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
				PackageOptions{Compilation: &Compilation{
					CoroPlan:                  plan,
					EmissionUniverse:          universe,
					EnableCoroEntryResolution: true,
					EnableCoroPhysicalABI:     true,
				}},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("inconsistent emitted consumer = %v, %v; want error containing %q", got, err, test.want)
			}
			if got != nil {
				t.Fatal("consumer preflight returned a partial package")
			}
		})
	}
}

func TestCoroEntryRejectsUnsupportedBeforeCreatingSymbol(t *testing.T) {
	pkg, plan := buildCoroEntryTestPlan(t)
	for _, tt := range []struct {
		name string
		fn   string
		use  func(*context, *ssa.Function)
	}{
		{
			name: "definition",
			fn:   "Coroutine",
			use: func(ctx *context, fn *ssa.Function) {
				ctx.compileFuncDecl(ctx.pkg, fn)
			},
		},
		{
			name: "declaration",
			fn:   "Coroutine",
			use: func(ctx *context, fn *ssa.Function) {
				ctx.funcOf(fn)
			},
		},
		{
			name: "dispatch",
			fn:   "Boxed",
			use: func(ctx *context, fn *ssa.Function) {
				ctx.compileFuncDecl(ctx.pkg, fn)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, dispose := newCoroEntryTestContext(t, pkg, &Compilation{
				CoroPlan:                  plan,
				EnableCoroEntryResolution: true,
			})
			defer dispose()

			fn := pkg.Func(tt.fn)
			_, legacyName, _ := ctx.funcName(fn)
			func() {
				defer func() {
					if recover() == nil {
						t.Fatal("entry resolution unexpectedly succeeded")
					}
				}()
				tt.use(ctx, fn)
			}()
			if got := ctx.pkg.FuncOf(legacyName); got != nil {
				t.Fatalf("unsupported entry resolution created legacy symbol %q", legacyName)
			}
			if got := ctx.pkg.FuncOf(legacyName + coroPrimarySuffix); got != nil {
				t.Fatalf("unsupported entry resolution created coroutine symbol %q", legacyName+coroPrimarySuffix)
			}
		})
	}
}

func TestCoroEntryResolutionPreflightRejectsWholePlanBeforeCodegen(t *testing.T) {
	tests := []struct {
		name   string
		source string
		plan   func(*ssa.Package) (*coro.SSAPlan, error)
		want   string
	}{
		{
			name: "later coroutine body",
			source: `package foo
func APlain() {}
func ZCoroutine(ch chan int) { <-ch }
`,
			plan: func(pkg *ssa.Package) (*coro.SSAPlan, error) {
				return coro.AnalyzeSSA(pkg.Prog, coro.Roots{
					{Function: pkg.Func("APlain"), Demand: coro.SyncDemand},
					{Function: pkg.Func("ZCoroutine"), Demand: coro.AsyncDemand},
				}, coro.SSAConfig{})
			},
			want: "physical ABI",
		},
		{
			name: "dispatch descriptor",
			source: `package foo
func Target() {}
func Box() any { return Target }
`,
			plan: func(pkg *ssa.Package) (*coro.SSAPlan, error) {
				return coro.AnalyzeSSA(pkg.Prog, coro.Roots{
					{Function: pkg.Func("Box"), Demand: coro.AsyncDemand},
				}, coro.SSAConfig{})
			},
			want: "dispatch descriptor",
		},
		{
			name:   "external coroutine",
			source: `package foo; func External()`,
			plan: func(pkg *ssa.Package) (*coro.SSAPlan, error) {
				external := pkg.Func("External")
				return coro.AnalyzeSSA(pkg.Prog, coro.Roots{
					{Function: external, Demand: coro.SyncDemand},
				}, coro.SSAConfig{
					ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
						if fn == external {
							return coro.SSAFunctionPolicy{
								Effect:           coro.WaitHost,
								External:         coro.ExternalKnown,
								OverrideExternal: true,
							}, nil
						}
						return coro.SSAFunctionPolicy{}, nil
					},
				})
			},
			want: "external coroutine",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkg, _, files := buildGoSSAPkg(t, tt.source)
			plan, err := tt.plan(pkg)
			if err != nil {
				t.Fatal(err)
			}
			observerCalls := 0
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			got, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, pkg, files, goembed.VarMap{}, PackageOptions{
				Compilation: &Compilation{
					CoroPlan:         plan,
					EmissionUniverse: coroEntryPreflightUniverse(plan),
					CoroPlanObserver: func(*ssa.Package, *coro.SSAPlan) {
						observerCalls++
					},
					EnableCoroEntryResolution: true,
				},
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("preflight result = %v, %v; want error containing %q", got, err, tt.want)
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

func TestCoroEntryResolutionPreflightRejectsMissingPlanAndCache(t *testing.T) {
	pkg, _, files := buildGoSSAPkg(t, `package foo; func F() {}`)
	for _, tt := range []struct {
		name        string
		compilation *Compilation
		cacheHit    bool
		want        string
	}{
		{
			name:        "missing plan",
			compilation: &Compilation{EnableCoroEntryResolution: true},
			want:        "requires a compilation CoroPlan",
		},
		{
			name: "missing universe",
			compilation: &Compilation{
				CoroPlan:                  &coro.SSAPlan{},
				EnableCoroEntryResolution: true,
			},
			want: "prepared emission universe",
		},
		{
			name: "cache hit",
			compilation: &Compilation{
				CoroPlan:                  &coro.SSAPlan{},
				EmissionUniverse:          coroEntryPreflightUniverse(&coro.SSAPlan{}),
				EnableCoroEntryResolution: true,
			},
			cacheHit: true,
			want:     "CoroPlanDigest",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			got, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, pkg, files, goembed.VarMap{}, PackageOptions{
				Compilation: tt.compilation,
				CacheHit:    tt.cacheHit,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("preflight result = %v, %v; want error containing %q", got, err, tt.want)
			}
			if got != nil {
				t.Fatal("preflight failure returned a partial package")
			}
		})
	}
}
