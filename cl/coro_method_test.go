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

const coroStaticMethodSource = `package foo

var gate chan uint32

type Counter struct { value uint32 }

func (counter Counter) Plain(delta uint32) uint32 {
	return counter.value + delta
}

func (counter *Counter) PlainPointer(delta uint32) uint32 {
	return delta + 2
}

func (counter Counter) WaitValue(delta uint32) uint32 {
	received := <-gate
	return counter.value + delta + received
}

func (counter *Counter) WaitPointer(delta uint32) uint32 {
	received := <-gate
	return delta + received
}

func Root(counter Counter, pointer *Counter) uint32 {
	received := <-gate
	first := counter.Plain(received)
	second := pointer.PlainPointer(first)
	third := counter.WaitValue(second)
	return pointer.WaitPointer(third)
}
`

func TestCoroStaticMethodReceiverABIPlainAndAwaitCoroSplit(t *testing.T) {
	prog, pkg, universe, plan, ssaPkg, methods := compileCoroStaticMethodFixture(t, coroStaticMethodSource, coro.DynamicCHAOpen)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	root := ssaPkg.Func("Root")
	rootPlan, ok := plan.FunctionPlan(root)
	if !ok || rootPlan.Emission != coro.EmitCoroutine || rootPlan.FuncRep != coro.DirectCoro ||
		!rootPlan.Effect.Contains(coro.MayPark|coro.AwaitStructured) {
		t.Fatalf("Root plan = %+v, present=%t; want parking child-await coroutine", rootPlan, ok)
	}
	for _, spec := range []struct {
		name       string
		emission   coro.BodyEmission
		represent  coro.FuncRep
		coroutine  bool
		pointerRec bool
	}{
		{name: "Plain", emission: coro.EmitPlain, represent: coro.DirectPlain},
		{name: "PlainPointer", emission: coro.EmitPlain, represent: coro.DirectPlain, pointerRec: true},
		{name: "WaitValue", emission: coro.EmitCoroutine, represent: coro.DirectCoro, coroutine: true},
		{name: "WaitPointer", emission: coro.EmitCoroutine, represent: coro.DirectCoro, coroutine: true, pointerRec: true},
	} {
		method := methods[spec.name]
		if method == nil || method.Signature == nil || method.Signature.Recv() == nil {
			t.Fatalf("method %s is absent or has no declared receiver", spec.name)
		}
		_, isPointer := types.Unalias(method.Signature.Recv().Type()).(*types.Pointer)
		if isPointer != spec.pointerRec {
			t.Fatalf("method %s pointer receiver=%t, want %t", spec.name, isPointer, spec.pointerRec)
		}
		methodPlan, found := plan.FunctionPlan(method)
		if !found || methodPlan.Emission != spec.emission || methodPlan.FuncRep != spec.represent {
			t.Fatalf("method %s plan = %+v, present=%t", spec.name, methodPlan, found)
		}
		sourceSig, err := universe.coroPhysicalSourceSignature(method)
		if err != nil {
			t.Fatalf("method %s effective physical signature: %v", spec.name, err)
		}
		if sourceSig.Recv() != nil || sourceSig.Params().Len() != len(method.Params) {
			t.Fatalf("method %s normalized signature = %v, SSA params=%d", spec.name, sourceSig, len(method.Params))
		}
		for index, parameter := range method.Params {
			if !types.Identical(sourceSig.Params().At(index).Type(), parameter.Type()) {
				t.Fatalf("method %s normalized parameter %d %s != SSA parameter %s", spec.name, index, sourceSig.Params().At(index).Type(), parameter.Type())
			}
		}

		name := funcName(ssaPkg.Pkg, method, false)
		if spec.coroutine {
			entry := plannedFunctionSymbol{function: method, plan: methodPlan, planned: true}
			abiContext := &context{prog: prog, compilation: coroStaticMethodCompilation(plan, universe)}
			fromDeclared := newCoroPhysicalABI(abiContext, entry, method.Signature)
			fromNormalized := newCoroPhysicalABI(abiContext, entry, sourceSig)
			if fromDeclared.hash != fromNormalized.hash || fromDeclared.descriptorName != fromNormalized.descriptorName ||
				!types.Identical(fromDeclared.physicalSig, fromNormalized.physicalSig) ||
				!types.Identical(fromDeclared.resultSlotType, fromNormalized.resultSlotType) {
				t.Fatalf("method %s declared and normalized physical ABI/hash disagree", spec.name)
			}
			name += coroPrimarySuffix
			ramp := module.NamedFunction(name)
			if ramp.IsNil() {
				t.Fatalf("method %s has no physical coroutine ramp %q:\n%s", spec.name, name, module.String())
			}
			if got, want := ramp.ParamsCount(), sourceSig.Params().Len()+2; got != want {
				t.Fatalf("method %s physical params=%d, want hidden+normalized=%d", spec.name, got, want)
			}
		} else if fn := module.NamedFunction(name); fn.IsNil() || fn.ParamsCount() != sourceSig.Params().Len() {
			t.Fatalf("plain method %s did not keep the ordinary receiver-first declaration ABI", spec.name)
		}
	}

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify static method coroutine before CoroSplit: %v\n%s", err, module.String())
	}
	rootIR := requireCoroPhysicalFunction(t, module, "foo.Root").String()
	for _, name := range []string{"Plain", "PlainPointer", "WaitValue", "WaitPointer"} {
		methodName := funcName(ssaPkg.Pkg, methods[name], false)
		if strings.HasPrefix(name, "Wait") {
			methodName += coroPrimarySuffix
		}
		if !strings.Contains(rootIR, methodName) {
			t.Fatalf("Root does not call method entry %q:\n%s", methodName, rootIR)
		}
	}

	runCoroABITestPipeline(t, prog, module)
	for _, name := range []string{"WaitValue", "WaitPointer"} {
		resumeName := funcName(ssaPkg.Pkg, methods[name], false) + coroPrimarySuffix + ".resume"
		if resume := module.NamedFunction(resumeName); resume.IsNil() {
			t.Fatalf("CoroSplit did not create method resume %q:\n%s", resumeName, module.String())
		}
	}
	if rootResume := module.NamedFunction("foo.Root$coro.resume"); rootResume.IsNil() {
		t.Fatalf("CoroSplit did not preserve Root method awaits:\n%s", module.String())
	}
}

func TestCoroPointerReceiverInterfaceAwaitCoroSplit(t *testing.T) {
	const source = `package foo
var gate chan uint32
type Waiter interface { Wait() uint32 }
type Counter struct{}
func (*Counter) Wait() uint32 { return <-gate }
func Root(waiter Waiter) uint32 { <-gate; return waiter.Wait() }
`
	prog, pkg, _, plan, ssaPkg, methods := compileCoroStaticMethodFixture(t, source, coro.DynamicCHAClosed)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	root := ssaPkg.Func("Root")
	rootPlan, ok := plan.FunctionPlan(root)
	if !ok || rootPlan.Emission != coro.EmitCoroutine || rootPlan.Primary != coro.PrimaryCoroutine ||
		!rootPlan.Effect.Contains(coro.MayPark|coro.AwaitStructured) {
		t.Fatalf("Root plan = %+v, present=%t; want parking interface-await coroutine", rootPlan, ok)
	}
	wait := methods["Wait"]
	waitPlan, ok := plan.FunctionPlan(wait)
	if wait == nil || !ok || waitPlan.Emission != coro.EmitCoroutine || waitPlan.Primary != coro.PrimaryCoroutine ||
		waitPlan.FuncRep != coro.Dispatch {
		t.Fatalf("Wait plan = %+v, present=%t; want coroutine Dispatch target", waitPlan, ok)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify pointer-receiver interface await before CoroSplit: %v\n%s", err, module.String())
	}
	rootIR := requireCoroPhysicalFunction(t, module, "foo.Root").String()
	waitName := funcName(ssaPkg.Pkg, wait, false) + coroPrimarySuffix
	for _, required := range []string{"coro.dispatch", "call void @" + coroAwaitPrepareHookV1} {
		if !strings.Contains(rootIR, required) {
			t.Fatalf("pointer-receiver interface await lacks %q:\n%s", required, rootIR)
		}
	}

	runCoroABITestPipeline(t, prog, module)
	if resume := module.NamedFunction("foo.Root$coro.resume"); resume.IsNil() {
		t.Fatalf("CoroSplit did not create pointer-receiver interface await resume:\n%s", module.String())
	}
	if waitResume := module.NamedFunction(waitName + ".resume"); waitResume.IsNil() {
		t.Fatalf("CoroSplit did not create pointer-receiver method resume %q:\n%s", waitName+".resume", module.String())
	}
}

func TestCoroStaticMethodReceiverABICompatibility(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		resolution coro.DynamicResolution
		want       string
	}{
		{
			name: "bound method value",
			source: `package foo
var gate chan uint32
type Counter struct{}
func (Counter) Wait() uint32 { return <-gate }
func Root(counter Counter) uint32 {
	<-gate
	wait := counter.Wait
	return wait()
}
`,
			resolution: coro.DynamicCHAClosed,
			want:       "approved runtime helper(s) lack an exact coroutine-safe lowered-call plan: AllocU",
		},
		{
			name: "dynamic suspending interface",
			source: `package foo
var gate chan uint32
type Waiter interface { Wait() uint32 }
type Counter struct{}
func (Counter) Wait() uint32 { return <-gate }
func Root(waiter Waiter) uint32 { <-gate; return waiter.Wait() }
`,
			resolution: coro.DynamicCHAClosed,
			want:       "",
		},
		{
			name: "variadic method",
			source: `package foo
var gate chan uint32
type Counter struct{}
func (Counter) Wait(values ...uint32) uint32 { <-gate; return uint32(len(values)) }
func Root(counter Counter) uint32 { <-gate; return counter.Wait(nil...) }
`,
			resolution: coro.DynamicCHAOpen,
			want:       "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, _, files := buildGoSSAPkg(t, test.source)
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, plan, methods, err := prepareCoroStaticMethodPlan(prog, ssaPkg, files, test.resolution)
			var pkg llssa.Package
			if err == nil {
				pkg, _, err = NewPackageExWithEmbedOptions(
					prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
					PackageOptions{Compilation: coroStaticMethodCompilation(plan, universe)},
				)
			}
			if test.want == "" {
				if err != nil {
					t.Fatalf("compile supported static method ABI: %v", err)
				}
				if test.name == "variadic method" {
					method := methods["Wait"]
					if method == nil || method.Signature == nil || !method.Signature.Variadic() {
						t.Fatalf("variadic method fixture lost its source signature: %v", method)
					}
					effective, err := universe.coroPhysicalSourceSignature(method)
					if err != nil || effective == nil || effective.Variadic() {
						t.Fatalf("variadic method effective signature = %v, %v; want packed non-variadic slice ABI", effective, err)
					}
				}
				module := pkg.Module()
				defer module.Dispose()
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatalf("verify supported static/interface method: %v\n%s", err, module.String())
				}
				return
			}
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("compile error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func compileCoroStaticMethodFixture(t *testing.T, source string, resolution coro.DynamicResolution) (
	llssa.Program, llssa.Package, *EmissionUniverse, *coro.SSAPlan, *ssa.Package, map[string]*ssa.Function,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	universe, plan, methods, err := prepareCoroStaticMethodPlan(prog, ssaPkg, files, resolution)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: coroStaticMethodCompilation(plan, universe)},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, universe, plan, ssaPkg, methods
}

func prepareCoroStaticMethodPlan(prog llssa.Program, ssaPkg *ssa.Package, files []*ast.File, resolution coro.DynamicResolution) (
	*EmissionUniverse, *coro.SSAPlan, map[string]*ssa.Function, error,
) {
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}}, EmissionUniverseOptions{CoroProfile: CoroProfileStackless},
	)
	if err != nil {
		return nil, nil, nil, err
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		return nil, nil, nil, err
	}
	methods := make(map[string]*ssa.Function)
	for _, function := range universe.Functions() {
		if function != nil && function.Signature != nil && function.Signature.Recv() != nil && function.Synthetic == "" {
			methods[function.Name()] = function
		}
	}
	root := ssaPkg.Func("Root")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		DynamicResolution:    resolution,
		MaxPlainInstructions: -1,
		OutcomeMode:          coro.OutcomeExplicitStatus,
	})
	if err != nil {
		return universe, nil, methods, err
	}
	return universe, plan, methods, nil
}

func coroStaticMethodCompilation(plan *coro.SSAPlan, universe *EmissionUniverse) *Compilation {
	return &Compilation{
		CoroPlan:         plan,
		EmissionUniverse: universe,

		CoroABI:      coro.PhysicalABIV1,
		SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
		PanicABI:     coro.PanicExplicitStatusABIV0,
		FuncRepABI:   coro.FuncRepABIV1, CoroProfile: CoroProfileStackless,
	}
}
