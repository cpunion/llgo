//go:build !llgo

/*
 * Copyright (c) 2026 The XGo Authors. All rights reserved.
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
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroMaterializedGenericClosureInstance(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	const source = `package foo
type Box[T any] struct { value T }
func (b *Box[T]) All() func(func(T) bool) {
	return func(yield func(T) bool) { var zero T; yield(zero) }
}
func Yield(value int) bool { return value != 0 }
func Root(b *Box[int]) { b.All()(Yield) }
`
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, _, files := buildGoSSAPkg(t, source)
			root := ssaPkg.Func("Root")
			var instance *ssa.Function
			var outerCall *ssa.Call
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(*ssa.Call)
					if !ok {
						continue
					}
					if call.Common().StaticCallee() != nil {
						instance = call.Common().StaticCallee()
					} else {
						outerCall = call
					}
				}
			}
			anonCount := 0
			if instance != nil {
				anonCount = len(instance.AnonFuncs)
			}
			if instance == nil || anonCount != 1 || outerCall == nil {
				t.Fatalf("generic receiver instance = %v, anonymous functions = %d, outer call = %v",
					instance, anonCount, outerCall)
			}
			closure := instance.AnonFuncs[0]
			if !coroMaterializedGenericInstance(instance) || !coroMaterializedGenericInstance(closure) {
				t.Fatalf("materialized generic instance=%t closure=%t", coroMaterializedGenericInstance(instance), coroMaterializedGenericInstance(closure))
			}
			if typeParamCount(closure.TypeParams()) == 0 || typeParamCount(closure.Signature.TypeParams()) != 0 ||
				closure.Parent() != instance || closure.Origin() == nil || len(closure.TypeArgs()) != 1 {
				t.Fatalf("generic closure metadata is not the expected stale-declaration/concrete-signature shape: %+v", closure)
			}
			var innerCall *ssa.Call
			for _, block := range closure.Blocks {
				for _, instruction := range block.Instrs {
					if call, ok := instruction.(*ssa.Call); ok && call.Common().StaticCallee() == nil {
						innerCall = call
					}
				}
			}
			if innerCall == nil {
				t.Fatal("materialized closure has no dynamic callback call")
			}

			var prog llssa.Program
			if test.target == nil {
				prog = newLLSSAProg(t)
			} else {
				prog = newLLSSAProgForTarget(t, test.target)
			}
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
			yield := ssaPkg.Func("Yield")
			plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse:     ssaUniverse,
				FunctionIDs:          functionIDs,
				MaxPlainInstructions: -1,
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					if fn == instance {
						return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
					}
					if fn == closure || fn == yield {
						return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, NeedsDispatch: true}, nil
					}
					return coro.SSAFunctionPolicy{}, nil
				},
				ClassifyUnknownCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.UnknownTarget, error) {
					if call == outerCall || call == innerCall {
						return coro.UnknownManagedDispatch, nil
					}
					return coro.UnknownManaged, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			instancePlan, ok := plan.FunctionPlan(instance)
			if !ok || instancePlan.FuncRep != coro.DirectCoro || instancePlan.Emission != coro.EmitCoroutine {
				t.Fatalf("generic receiver instance plan = %+v, present=%t; want direct coroutine", instancePlan, ok)
			}
			for name, fn := range map[string]*ssa.Function{"closure": closure, "yield": yield} {
				functionPlan, ok := plan.FunctionPlan(fn)
				if !ok || functionPlan.FuncRep != coro.Dispatch || functionPlan.Emission != coro.EmitCoroutine {
					t.Fatalf("%s plan = %+v, present=%t; want coroutine Dispatch", name, functionPlan, ok)
				}
			}
			callbackPlan, ok := plan.ValuePlan(closure.Params[0])
			if !ok || len(callbackPlan.Funcs) != 1 || callbackPlan.Funcs[0].Rep != coro.Dispatch ||
				len(callbackPlan.Funcs[0].Path) != 0 || !callbackPlan.Funcs[0].MayBeNil {
				t.Fatalf("nested callback ValuePlan = %+v, present=%t; want nullable scalar Dispatch", callbackPlan, ok)
			}

			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
			enableCoroPreemptCompilation(compilation)
			compilation.PanicABI = coro.PanicExplicitStatusABIV0
			compilation.FuncRepABI = coro.FuncRepABIV1
			compiled, _, err := NewPackageExWithEmbedOptions(
				prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatal(err)
			}
			module := compiled.Module()
			defer module.Dispose()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify recursive dispatch before CoroSplit: %v\n%s", err, module.String())
			}
			ir := module.String()
			if strings.Count(ir, coroPlainDispatchDescriptorPrefix) < 2 ||
				!strings.Contains(ir, "{ ptr, ptr }") || !strings.Contains(ir, "call i1 @"+coroAwaitPrepareInlineHookV4) {
				t.Fatalf("recursive descriptor transport is incomplete:\n%s", ir)
			}
			runCoroABITestPipeline(t, prog, module)
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify recursive dispatch after CoroSplit: %v\n%s", err, module.String())
			}
		})
	}
}
