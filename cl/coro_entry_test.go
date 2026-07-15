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
	if !plain.planned || plain.plan.Primary != coro.PrimaryPlain || strings.HasSuffix(plain.name, coroPrimarySuffix) {
		t.Fatalf("plain entry = %+v", plain)
	}
	if err := plain.checkSupported(); err != nil {
		t.Fatalf("plain entry rejected: %v", err)
	}

	coroutine, err := ctx.resolveFunctionSymbol(pkg.Func("Coroutine"))
	if err != nil {
		t.Fatal(err)
	}
	if !coroutine.planned || coroutine.plan.Primary != coro.PrimaryCoroutine || !strings.HasSuffix(coroutine.name, coroPrimarySuffix) {
		t.Fatalf("coroutine entry = %+v", coroutine)
	}
	if err := coroutine.checkSupported(); err == nil || !strings.Contains(err.Error(), "physical ABI") {
		t.Fatalf("coroutine support error = %v", err)
	}

	boxed, err := ctx.resolveFunctionSymbol(pkg.Func("Boxed"))
	if err != nil {
		t.Fatal(err)
	}
	if boxed.plan.Primary != coro.PrimaryPlain || boxed.plan.FuncRep != coro.Dispatch || strings.HasSuffix(boxed.name, coroPrimarySuffix) {
		t.Fatalf("boxed entry = %+v, want one plain primary plus dispatch descriptor", boxed)
	}
	if err := boxed.checkSupported(); err == nil || !strings.Contains(err.Error(), "dispatch descriptor") {
		t.Fatalf("boxed support error = %v", err)
	}

	external, err := ctx.resolveFunctionSymbol(pkg.Func("External"))
	if err != nil {
		t.Fatal(err)
	}
	if external.plan.Primary != coro.PrimaryExternal || external.plan.FuncRep != coro.DirectCoro {
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
					CoroPlan: plan,
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
			name: "cache hit",
			compilation: &Compilation{
				CoroPlan:                  &coro.SSAPlan{},
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
