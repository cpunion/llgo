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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroMaterializedGenericReceiverInstanceNativeAndWasm(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	const source = `package foo
type Pointer[T any] struct { value *T }
func (p *Pointer[T]) Load() *T { return nil }
func Root(p *Pointer[int]) *int { return p.Load() }
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
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(*ssa.Call)
					if ok && call.Common().StaticCallee() != nil {
						instance = call.Common().StaticCallee()
					}
				}
			}
			if instance == nil {
				t.Fatal("generic receiver call has no static instance target")
			}
			if !coroMaterializedGenericInstance(instance) || typeParamCount(instance.Signature.RecvTypeParams()) != 1 {
				t.Fatalf("generic receiver target = %v, materialized=%t recv-type-params=%d",
					instance, coroMaterializedGenericInstance(instance), typeParamCount(instance.Signature.RecvTypeParams()))
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
			sourceSig, err := universe.coroPhysicalSourceSignature(instance)
			if err != nil {
				t.Fatal(err)
			}
			if sourceSig.Recv() != nil || sourceSig.RecvTypeParams().Len() != 0 ||
				sourceSig.Params().Len() != 1 || !strings.Contains(sourceSig.Params().At(0).Type().String(), "Pointer[int]") {
				t.Fatalf("normalized generic receiver signature = %v", sourceSig)
			}

			ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
			if err != nil {
				t.Fatal(err)
			}
			functionIDs := universe.FunctionIDConfig()
			functionIDs.CoroABI = coro.PhysicalABIV1
			functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
			functionIDs.ArchiveReady = true
			plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse:     ssaUniverse,
				FunctionIDs:          functionIDs,
				MaxPlainInstructions: -1,
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					if fn == instance {
						return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
					}
					return coro.SSAFunctionPolicy{}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			instancePlan, ok := plan.FunctionPlan(instance)
			if !ok || instancePlan.Emission != coro.EmitCoroutine || instancePlan.Primary != coro.PrimaryCoroutine ||
				!instancePlan.Demand.Contains(coro.AsyncDemand) {
				t.Fatalf("generic receiver instance plan = %+v, present=%t", instancePlan, ok)
			}
			compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
			enableCoroChildAwaitCompilation(compilation)
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
				t.Fatalf("verify generic receiver instance before CoroSplit: %v\n%s", err, module.String())
			}
			rootIR := requireCoroPhysicalFunction(t, module, "foo.Root").String()
			if !strings.Contains(rootIR, "$coro") || !strings.Contains(rootIR, "call i1 @"+coroAwaitPrepareInlineHookV4) {
				t.Fatalf("generic receiver call did not use child await:\n%s", rootIR)
			}
			runCoroABITestPipeline(t, prog, module)
			if module.NamedFunction("foo.Root$coro.resume").IsNil() {
				t.Fatalf("CoroSplit lost generic receiver caller resume:\n%s", module.String())
			}
		})
	}
}

func TestCoroMaterializedGenericPointerMethodWrapper(t *testing.T) {
	const source = `package foo
type Pointer[T any] struct { value *T }
func (p Pointer[T]) Value() *T { return p.value }
func Root(p *Pointer[int]) *int { return p.Value() }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	root := ssaPkg.Func("Root")
	selection := ssaPkg.Prog.MethodSets.MethodSet(root.Params[0].Type()).Lookup(ssaPkg.Pkg, "Value")
	if selection == nil {
		t.Fatal("generic pointer method selection is absent")
	}
	wrapper := ssaPkg.Prog.MethodValue(selection)
	if wrapper == nil || !strings.HasPrefix(wrapper.Synthetic, "wrapper for ") ||
		wrapper.Pkg != nil || typeParamCount(wrapper.Signature.RecvTypeParams()) != 1 {
		t.Fatalf("generic pointer method wrapper has unexpected shape: %v synthetic=%q", wrapper, func() string {
			if wrapper == nil {
				return ""
			}
			return wrapper.Synthetic
		}())
	}
	if !coroMaterializedGenericMethodWrapper(wrapper) || !coroMaterializedGenericCallable(wrapper) {
		t.Fatalf("exact generated generic method wrapper was not recognized:\n%s", wrapper)
	}

	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	sig, err := universe.coroPhysicalSourceSignature(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if sig.Recv() != nil || typeParamCount(sig.RecvTypeParams()) != 0 || sig.Params().Len() != 1 ||
		!strings.Contains(sig.Params().At(0).Type().String(), "*foo.Pointer[int]") {
		t.Fatalf("generic pointer wrapper physical signature = %v", sig)
	}

	originalSynthetic := wrapper.Synthetic
	wrapper.Synthetic = "wrapper for forged generic method"
	if coroMaterializedGenericMethodWrapper(wrapper) {
		t.Fatal("forged generic method wrapper identity was accepted")
	}
	wrapper.Synthetic = originalSynthetic
}
