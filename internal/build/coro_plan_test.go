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

package build

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroPlanBuilderRunsBeforeCodegenWithoutChangingIR(t *testing.T) {
	var (
		builderCalls int
		builderDone  bool
		planned      *coro.SSAPlan
		mainFn       *ssa.Function
	)
	builder := func(prog *ssa.Program) (*coro.SSAPlan, error) {
		builderCalls++
		var err error
		mainFn, err = findSingleSSAMain(prog)
		if err != nil {
			return nil, err
		}
		planned, err = coro.AnalyzeSSA(prog, coro.Roots{{Function: mainFn, Demand: coro.AsyncDemand}}, coro.SSAConfig{})
		if err == nil {
			builderDone = true
		}
		return planned, err
	}

	baselineIR, baselineModules := buildModeGenIR(t, "../../cl/_testgo/chan", nil, nil)
	plannedIR, plannedModules := buildModeGenIR(t, "../../cl/_testgo/chan", builder, func(Package) {
		if !builderDone {
			t.Error("ModuleHook ran before CoroPlanBuilder completed")
		}
	})
	if builderCalls != 1 {
		t.Fatalf("CoroPlanBuilder calls = %d, want 1", builderCalls)
	}
	if planned == nil || mainFn == nil {
		t.Fatal("CoroPlanBuilder did not publish a plan for main")
	}
	id, ok := planned.FunctionID(mainFn)
	if !ok {
		t.Fatal("main function is absent from coroutine plan")
	}
	mainPlan, ok := planned.BasePlan().Lookup(id)
	if !ok || !mainPlan.Effect.Contains(coro.MayPark) || mainPlan.Demand != coro.AsyncDemand {
		t.Fatalf("main coroutine plan = %+v, %v", mainPlan, ok)
	}

	if plannedIR != baselineIR {
		t.Fatal("report-only CoroPlanBuilder changed emitted LLVM IR")
	}
	if len(plannedModules) == 0 || !reflect.DeepEqual(plannedModules, baselineModules) {
		t.Fatalf("report-only CoroPlanBuilder changed generated package modules:\nbaseline: %x\nplanned: %x", baselineModules, plannedModules)
	}
}

func TestBuildCoroPlanErrors(t *testing.T) {
	t.Run("builder error", func(t *testing.T) {
		sentinel := errors.New("sentinel")
		ctx := &context{
			buildConf: &Config{CoroPlanBuilder: func(*ssa.Program) (*coro.SSAPlan, error) {
				return nil, sentinel
			}},
		}
		err := buildCoroPlan(ctx)
		if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "build coroutine plan") {
			t.Fatalf("buildCoroPlan error = %v", err)
		}
		if ctx.coroPlan != nil {
			t.Fatal("failed builder installed a coroutine plan")
		}
	})

	t.Run("nil plan", func(t *testing.T) {
		ctx := &context{
			buildConf: &Config{CoroPlanBuilder: func(*ssa.Program) (*coro.SSAPlan, error) {
				return nil, nil
			}},
		}
		if err := buildCoroPlan(ctx); err == nil || !strings.Contains(err.Error(), "nil plan") {
			t.Fatalf("buildCoroPlan error = %v, want nil-plan rejection", err)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		ctx := &context{buildConf: &Config{}}
		if err := buildCoroPlan(ctx); err != nil || ctx.coroPlan != nil {
			t.Fatalf("disabled buildCoroPlan = %v, plan %v", err, ctx.coroPlan)
		}
	})

	t.Run("Do stops before codegen", func(t *testing.T) {
		sentinel := errors.New("sentinel")
		conf := NewDefaultConf(ModeGen)
		conf.CoroPlanBuilder = func(*ssa.Program) (*coro.SSAPlan, error) {
			return nil, sentinel
		}
		moduleCalls := 0
		conf.ModuleHook = func(Package) {
			moduleCalls++
		}

		pkgs, err := Do([]string{"../../cl/_testgo/print"}, conf)
		if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "build coroutine plan") {
			t.Fatalf("Do error = %v", err)
		}
		if len(pkgs) != 0 {
			t.Fatalf("Do packages = %+v, want none", pkgs)
		}
		if moduleCalls != 0 {
			t.Fatalf("ModuleHook calls = %d, want 0", moduleCalls)
		}
	})
}

func buildModeGenIR(t *testing.T, pattern string, builder CoroPlanBuilder, moduleHook ModuleHook) (string, map[string][sha256.Size]byte) {
	t.Helper()
	conf := NewDefaultConf(ModeGen)
	conf.CoroPlanBuilder = builder
	modules := make(map[string][sha256.Size]byte)
	conf.ModuleHook = func(pkg Package) {
		key := pkg.ID
		if _, exists := modules[key]; exists {
			t.Errorf("ModuleHook ran more than once for %s", key)
		}
		modules[key] = sha256.Sum256([]byte(pkg.LPkg.String()))
		if moduleHook != nil {
			moduleHook(pkg)
		}
	}
	pkgs, err := Do([]string{pattern}, conf)
	if err != nil {
		t.Fatalf("Do(%q): %v", pattern, err)
	}
	if len(pkgs) != 1 || pkgs[0].LPkg == nil {
		t.Fatalf("Do(%q) packages = %+v, want one generated package", pattern, pkgs)
	}
	ir := pkgs[0].LPkg.String()
	pkgs[0].LPkg.Prog.Dispose()
	return ir, modules
}

func findSingleSSAMain(prog *ssa.Program) (*ssa.Function, error) {
	if prog == nil {
		return nil, fmt.Errorf("nil SSA program")
	}
	var found *ssa.Function
	for _, pkg := range prog.AllPackages() {
		if pkg == nil || pkg.Pkg == nil || pkg.Pkg.Name() != "main" {
			continue
		}
		fn := pkg.Func("main")
		if fn == nil {
			continue
		}
		if found != nil && found != fn {
			return nil, fmt.Errorf("multiple SSA main functions: %s and %s", found, fn)
		}
		found = fn
	}
	if found == nil {
		return nil, fmt.Errorf("SSA main function not found")
	}
	return found, nil
}
