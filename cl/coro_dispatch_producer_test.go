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
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func TestCoroExactBoundMethodWrapperShape(t *testing.T) {
	const source = `package foo
type Reader interface { Read() int }
type Counter struct { value int }
func (counter *Counter) Read() int { return counter.value }
func Concrete(counter *Counter) func() int { return counter.Read }
func Interface(reader Reader) func() int { return reader.Read }
`
	ssaPkg, _, _ := buildGoSSAPkg(t, source)
	wrappers := make(map[string]*ssa.Function)
	for _, name := range []string{"Concrete", "Interface"} {
		function := ssaPkg.Func(name)
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				closure, ok := instruction.(*ssa.MakeClosure)
				if !ok {
					continue
				}
				wrapper, ok := closure.Fn.(*ssa.Function)
				if ok {
					wrappers[name] = wrapper
				}
			}
		}
	}
	for _, name := range []string{"Concrete", "Interface"} {
		wrapper := wrappers[name]
		if wrapper == nil {
			t.Fatalf("%s has no bound method wrapper", name)
		}
		if err := validateCoroExactBoundMethodWrapper(wrapper); err != nil {
			t.Fatalf("%s bound method wrapper rejected: %v\n%s", name, err, wrapper.String())
		}
	}
	concrete := wrappers["Concrete"]
	original := concrete.Synthetic
	concrete.Synthetic += " forged"
	if err := validateCoroExactBoundMethodWrapper(concrete); err == nil {
		t.Fatal("forged bound method identity was accepted")
	}
	concrete.Synthetic = original
}

func TestCoroDirectBoundMethodWrapperWithGenericResultPhysicalABI(t *testing.T) {
	const source = `package foo
type Seq[T any] func(func(T) bool)
type Value struct{}
func (Value) Seq() Seq[Value] { return nil }
func Capture(value Value) { _ = value.Seq }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	var wrapper *ssa.Function
	for function := range ssautil.AllFunctions(ssaPkg.Prog) {
		if function != nil && strings.HasPrefix(function.Synthetic, "bound method wrapper for ") {
			wrapper = function
			break
		}
	}
	if wrapper == nil {
		t.Fatal("Capture has no bound method wrapper")
	}
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(
		prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}},
	)
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
	plan, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{{Function: wrapper, Demand: coro.AsyncDemand}},
		coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          functionIDs,
			MaxPlainInstructions: -1,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == wrapper {
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	functionPlan, ok := plan.FunctionPlan(wrapper)
	if !ok {
		t.Fatal("bound method wrapper has no FunctionPlan")
	}
	if functionPlan.FuncRep != coro.DirectCoro || functionPlan.Emission != coro.EmitCoroutine {
		t.Fatalf("bound method wrapper plan = %+v, want direct coroutine", functionPlan)
	}
	if err := validateCoroPhysicalABIWithUniverseCapabilitiesFrameRetentionAndChannel(
		wrapper, functionPlan, plan, universe,
		true, true, false, false, "", false, false, false,
	); err != nil {
		t.Fatalf("direct bound method wrapper with generic result rejected: %v", err)
	}
}

func TestCoroExactMethodExpressionThunkShape(t *testing.T) {
	const source = `package foo
type Counter struct{}
func (*Counter) Release() {}
var Cleanup = (*Counter).Release
`
	ssaPkg, _, _ := buildGoSSAPkg(t, source)
	var thunk *ssa.Function
	for function := range ssautil.AllFunctions(ssaPkg.Prog) {
		if function != nil && function.Synthetic == "thunk for func (*foo.Counter).Release()" {
			thunk = function
			break
		}
	}
	if thunk == nil {
		t.Fatal("fixture has no method-expression thunk")
	}
	if err := validateCoroExactMethodExpressionThunk(thunk); err != nil {
		t.Fatalf("canonical method-expression thunk rejected: %v\n%s", err, thunk.String())
	}
	original := thunk.Synthetic
	thunk.Synthetic += " forged"
	if err := validateCoroExactMethodExpressionThunk(thunk); err == nil {
		t.Fatal("forged method-expression identity was accepted")
	}
	thunk.Synthetic = original
}

func TestCoroExactMethodExpressionThunkImplicitIndirectionShape(t *testing.T) {
	const source = `package foo
type Number int
func (number Number) Twice() int { return int(number) * 2 }
var Twice = (*Number).Twice
`
	ssaPkg, _, _ := buildGoSSAPkg(t, source)
	var thunk *ssa.Function
	for function := range ssautil.AllFunctions(ssaPkg.Prog) {
		if function != nil && function.Name() == "Twice$thunk" {
			thunk = function
			break
		}
	}
	if thunk == nil {
		t.Fatal("fixture has no implicit-indirection method-expression thunk")
	}
	if err := validateCoroExactMethodExpressionThunk(thunk); err != nil {
		var dump bytes.Buffer
		ssa.WriteFunction(&dump, thunk)
		t.Fatalf("canonical implicit-indirection method-expression thunk rejected: %v\n%s", err, dump.String())
	}
}

func TestCoroDynamicDispatchProducerCapturedPlainClosure(t *testing.T) {
	const source = `package foo
func Root(seed int) func(int) int {
	return func(value int) int { return seed + value }
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	root := ssaPkg.Func("Root")
	if len(root.AnonFuncs) != 1 || len(root.AnonFuncs[0].FreeVars) != 1 {
		t.Fatalf("Root anonymous functions = %+v; want one closure with one free variable", root.AnonFuncs)
	}
	target := root.AnonFuncs[0]
	prog := newLLSSAProg(t)
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
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.FuncRep != coro.Dispatch || targetPlan.Emission != coro.EmitPlain {
		t.Fatalf("captured target plan = %+v, present=%t; want a descriptor-backed plain primary", targetPlan, ok)
	}

	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: &Compilation{
			CoroPlan:         plan,
			EmissionUniverse: universe,

			CoroABI:      coro.PhysicalABIV1,
			SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
			PanicABI:     coro.PanicExplicitStatusABIV0,
			FuncRepABI:   coro.FuncRepABIV1}},
	)
	if err != nil {
		t.Fatalf("compile captured descriptor producer: %v", err)
	}
	module := compiled.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify captured descriptor producer: %v\n%s", err, module.String())
	}
	descriptor := coroDispatchProducerOnlyGlobalWithPrefix(t, module, coroPlainDispatchDescriptorPrefix)
	if got := descriptor.Initializer().Operand(1).ZExtValue(); got != uint64(llssa.CoroDispatchFlagHasPlain) {
		t.Fatalf("captured plain descriptor flags = %#x, want HasPlain without NoCapture", got)
	}
	thunk := coroDispatchProducerOnlyFunctionWithPrefix(t, module, coroPlainDispatchThunkPrefix)
	call := coroDispatchProducerOnlyCallTo(t, thunk, "")
	if got := call.OperandsCount() - 1; got != 2 {
		t.Fatalf("captured plain thunk target arguments = %d, want ctx+source argument", got)
	}
	if call.Operand(0).C != thunk.Param(0).C || call.Operand(1).C != thunk.Param(1).C {
		t.Fatalf("captured plain thunk did not reorder descriptor (env,arg) to target (ctx,arg):\n%s", module.String())
	}
	ir := module.String()
	if !strings.Contains(ir, "runtime/internal/runtime.AllocU") {
		t.Fatalf("captured descriptor producer did not reuse MakeClosure environment allocation:\n%s", ir)
	}
	if !strings.Contains(ir, "insertvalue { ptr, ptr }") || !strings.Contains(ir, ", ptr %") {
		t.Fatalf("captured descriptor producer did not materialize a non-nil descriptor environment:\n%s", ir)
	}
}

func TestCoroDynamicDispatchProducerElidesZeroSizedClosureEnv(t *testing.T) {
	const source = `package foo
type marker struct{}
var Sink marker
func Root() func() {
	value := marker{}
	return func() { Sink = value }
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	root := ssaPkg.Func("Root")
	if len(root.AnonFuncs) != 1 || len(root.AnonFuncs[0].FreeVars) != 1 {
		t.Fatalf("Root anonymous functions = %+v; want one closure with one free variable", root.AnonFuncs)
	}
	target := root.AnonFuncs[0]
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	if !universe.closureEnvironments.canElideZeroSizedEnvironment(target) {
		t.Fatal("fixture closure environment is not physically elidable")
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.FuncRep != coro.Dispatch || targetPlan.Emission != coro.EmitPlain {
		t.Fatalf("zero-sized target plan = %+v, present=%t; want a descriptor-backed plain primary", targetPlan, ok)
	}

	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: &Compilation{
			CoroPlan:         plan,
			EmissionUniverse: universe,

			CoroABI:      coro.PhysicalABIV1,
			SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
			PanicABI:     coro.PanicExplicitStatusABIV0,
			FuncRepABI:   coro.FuncRepABIV1}},
	)
	if err != nil {
		t.Fatalf("compile zero-sized descriptor producer: %v", err)
	}
	module := compiled.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify zero-sized descriptor producer: %v\n%s", err, module.String())
	}
	descriptor := coroDispatchProducerOnlyGlobalWithPrefix(t, module, coroPlainDispatchDescriptorPrefix)
	wantFlags := uint64(llssa.CoroDispatchFlagHasPlain | llssa.CoroDispatchFlagNoCapture)
	if got := descriptor.Initializer().Operand(1).ZExtValue(); got != wantFlags {
		t.Fatalf("zero-sized plain descriptor flags = %#x, want %#x", got, wantFlags)
	}
	thunk := coroDispatchProducerOnlyFunctionWithPrefix(t, module, coroPlainDispatchThunkPrefix)
	call := coroDispatchProducerOnlyCallTo(t, thunk, "")
	if got := call.OperandsCount() - 1; got != 0 {
		t.Fatalf("zero-sized plain thunk target arguments = %d, want no physical environment", got)
	}
}

func TestCoroBoundMethodElidesZeroSizedValueReceiverEnvironment(t *testing.T) {
	const source = `package foo
type marker struct{}
func (marker) Method() int { return 1 }
var Bound = marker{}.Method
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	initFn := ssaPkg.Func("init")
	var closure *ssa.MakeClosure
	for _, block := range initFn.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.MakeClosure); ok {
				closure = candidate
				break
			}
		}
	}
	if closure == nil {
		t.Fatal("package initializer has no bound-method closure")
	}
	target, ok := closure.Fn.(*ssa.Function)
	if !ok || target == nil || len(target.FreeVars) != 1 || len(closure.Bindings) != 1 {
		t.Fatalf("bound-method closure = %+v, target=%v", closure, target)
	}
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	if !universe.closureEnvironments.canElideZeroSizedEnvironment(target) {
		t.Fatalf(
			"zero-sized bound-method receiver environment was retained (synthetic=%q parent=%v free=%v binding=%v fact=%+v)",
			target.Synthetic, target.Parent(), target.FreeVars[0].Type(), closure.Bindings[0].Type(),
			universe.closureEnvironments.facts[target],
		)
	}
}

func TestCoroDynamicDispatchProducerElidesDormantConditionalPublication(t *testing.T) {
	const source = `package foo
var slot func()
func Target() {}
func Root() { slot = Target }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	root := ssaPkg.Func("Root")
	target := ssaPkg.Func("Target")
	var publication *ssa.Store
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			store, ok := instruction.(*ssa.Store)
			if ok && store.Val == target {
				publication = store
			}
		}
	}
	if publication == nil {
		t.Fatal("Root has no direct Target Store")
	}
	prog := newLLSSAProg(t)
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
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyConditionalManagedStoreReference: func(owner *ssa.Function, store *ssa.Store) (*ssa.Function, bool, error) {
			if owner == root && store == publication {
				return target, true, nil
			}
			return nil, false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	targetPlan, planned := plan.FunctionPlan(target)
	if !planned || targetPlan.ManagedDemand != coro.NoDemand || targetPlan.RawPlainDemand ||
		targetPlan.Emission != coro.EmitNone || targetPlan.FuncRep != coro.Dispatch || plan.HasRawPlainVariant(target) ||
		!plan.ElidesConditionalManagedStore(publication) {
		t.Fatalf("dormant conditional descriptor target = %+v/%t, variant=%t elided=%t", targetPlan, planned, plan.HasRawPlainVariant(target), plan.ElidesConditionalManagedStore(publication))
	}
	valuePlan, planned := plan.ValuePlan(target)
	if !planned || len(valuePlan.Funcs) != 1 || valuePlan.Funcs[0].Rep != coro.Dispatch {
		t.Fatalf("raw-only Store ValuePlan = %+v/%t", valuePlan, planned)
	}

	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: &Compilation{
			CoroPlan:         plan,
			EmissionUniverse: universe,

			CoroABI:      coro.PhysicalABIV1,
			SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
			PanicABI:     coro.PanicExplicitStatusABIV0,
			FuncRepABI:   coro.FuncRepABIV1}},
	)
	if err != nil {
		t.Fatalf("compile dormant conditional descriptor producer: %v", err)
	}
	module := compiled.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify dormant conditional descriptor producer: %v\n%s", err, module.String())
	}
	if strings.Contains(module.String(), coroPlainDispatchDescriptorPrefix) || strings.Contains(module.String(), "foo.Target") {
		t.Fatalf("dormant conditional publication materialized a descriptor or target:\n%s", module.String())
	}
}

func TestCoroDynamicDispatchProducerPublishesMixedPlainAndCoroTarget(t *testing.T) {
	const source = `package foo
var slot func()
func Target() {}
func Root() { slot = Target }
func Managed() { Target() }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	root := ssaPkg.Func("Root")
	target := ssaPkg.Func("Target")
	managed := ssaPkg.Func("Managed")
	var publication *ssa.Store
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			store, ok := instruction.(*ssa.Store)
			if ok && store.Val == target {
				publication = store
			}
		}
	}
	if publication == nil {
		t.Fatal("Root has no direct Target Store")
	}
	prog := newLLSSAProg(t)
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
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: root, Demand: coro.SyncDemand},
		{Function: managed, Demand: coro.AsyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == target {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyConditionalManagedStoreReference: func(owner *ssa.Function, store *ssa.Store) (*ssa.Function, bool, error) {
			if owner == root && store == publication {
				return target, true, nil
			}
			return nil, false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	targetPlan, planned := plan.FunctionPlan(target)
	if !planned || targetPlan.ManagedDemand == coro.NoDemand || targetPlan.RawPlainDemand ||
		targetPlan.RawPlainOnly || targetPlan.Emission != coro.EmitCoroutine || targetPlan.FuncRep != coro.Dispatch ||
		plan.HasRawPlainVariant(target) || plan.ElidesConditionalManagedStore(publication) {
		t.Fatalf("managed descriptor target = %+v/%t, variant=%t", targetPlan, planned, plan.HasRawPlainVariant(target))
	}
	valuePlan, planned := plan.ValuePlan(target)
	if !planned || len(valuePlan.Funcs) != 1 || valuePlan.Funcs[0].Rep != coro.Dispatch {
		t.Fatalf("mixed Store ValuePlan = %+v/%t", valuePlan, planned)
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	compilation.FuncRepABI = coro.FuncRepABIV1
	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("compile mixed descriptor producer: %v", err)
	}
	module := compiled.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify mixed descriptor producer: %v\n%s", err, module.String())
	}
	descriptor := coroDispatchProducerOnlyGlobalWithPrefix(t, module, coroPlainDispatchDescriptorPrefix)
	wantFlags := uint64(llssa.CoroDispatchFlagHasCoro | llssa.CoroDispatchFlagNoCapture)
	if got := descriptor.Initializer().Operand(1).ZExtValue(); got != wantFlags {
		t.Fatalf("mixed descriptor flags = %#x, want %#x", got, wantFlags)
	}
	coroThunk := coroDispatchProducerOnlyFunctionWithPrefix(t, module, coroCoroDispatchThunkPrefix)
	coroCall := coroDispatchProducerOnlyCallTo(t, coroThunk, "")
	if got := coroCall.CalledValue().Name(); got != "foo.Target"+coroPrimarySuffix {
		t.Fatalf("mixed coroutine thunk target = %q, want managed primary foo.Target%s", got, coroPrimarySuffix)
	}
}

func TestCoroDynamicDispatchProducerCoroThunkDropsNilEnvironment(t *testing.T) {
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	pkg := prog.NewPackage("dispatchproducer", "example.com/dispatchproducer")
	logical := types.NewSignatureType(
		nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "value", types.Typ[types.Int])),
		types.NewTuple(types.NewParam(token.NoPos, nil, "result", types.Typ[types.Int])),
		false,
	)
	hidden := []*types.Var{
		types.NewParam(token.NoPos, nil, "__llgo_g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "__llgo_out", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "value", types.Typ[types.Int]),
	}
	physical := types.NewSignatureType(
		nil, nil, nil, types.NewTuple(hidden...),
		types.NewTuple(types.NewParam(token.NoPos, nil, "handle", types.Typ[types.UnsafePointer])), false,
	)
	target := pkg.NewFunc("target$coro", physical, llssa.InC)
	targetBody := target.MakeBody(1)
	targetBody.Return(prog.Nil(prog.VoidPtr()))
	targetBody.EndBuild()
	targetBody.Dispose()
	resultSlot := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "r0", types.Typ[types.Int], false),
	}, nil)
	abi := coroPlainDispatchABI{signature: logical, resultSlotType: resultSlot}
	ctx := &context{prog: prog, pkg: pkg}
	thunkName := coroCoroDispatchThunkPrefix + "focused"
	thunkExpr := ctx.newCoroDynamicDispatchEntryThunk(thunkName, target.Expr, abi, coro.EmitCoroutine, nil)
	descriptorName := coroPlainDispatchDescriptorPrefix + "focused"
	descriptor := pkg.NewCoroDispatchDescriptor(descriptorName, llssa.CoroDispatchDescriptorOptions{
		Version:   llssa.CoroDispatchVersionV1,
		Flags:     llssa.CoroDispatchFlagHasCoro | llssa.CoroDispatchFlagNoCapture,
		Signature: logical,
		CoroEntry: thunkExpr,
		CodeEntry: target.Expr,
		Result:    prog.Type(resultSlot, llssa.InC),
	})
	producerSig := types.NewSignatureType(
		nil, nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "", logical)), false,
	)
	producer := pkg.NewFunc("producer", producerSig, llssa.InGo)
	producerBody := producer.MakeBody(1)
	producerBody.Return(producerBody.MakeCoroDispatchValue(logical, descriptor, llssa.Nil))
	producerBody.EndBuild()
	producerBody.Dispose()

	module := pkg.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify coroutine descriptor producer: %v\n%s", err, module.String())
	}
	global := module.NamedGlobal(descriptorName)
	if global.IsNil() {
		t.Fatal("coroutine descriptor global is absent")
	}
	if got := global.Initializer().Operand(1).ZExtValue(); got != uint64(llssa.CoroDispatchFlagHasCoro|llssa.CoroDispatchFlagNoCapture) {
		t.Fatalf("coroutine descriptor flags = %#x, want HasCoro|NoCapture", got)
	}
	thunk := module.NamedFunction(thunkName)
	call := coroDispatchProducerOnlyCallTo(t, thunk, target.Name())
	if got := call.OperandsCount() - 1; got != 3 {
		t.Fatalf("coroutine thunk target arguments = %d, want g+out+source argument", got)
	}
	if call.Operand(0).C != thunk.Param(0).C || call.Operand(1).C != thunk.Param(1).C || call.Operand(2).C != thunk.Param(3).C {
		t.Fatalf("coroutine thunk did not drop descriptor env and preserve (g,out,args) order:\n%s", module.String())
	}
}

func TestCoroDynamicDispatchProducerCapturedCoroThunkInsertsEnvironment(t *testing.T) {
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	pkg := prog.NewPackage("captureddispatchproducer", "example.com/captureddispatchproducer")
	logical := types.NewSignatureType(
		nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "value", types.Typ[types.Int])),
		types.NewTuple(types.NewParam(token.NoPos, nil, "result", types.Typ[types.Int])),
		false,
	)
	// Package loading can materialize equivalent imported named types as
	// distinct go/types objects. Reproduce that identity split while retaining
	// one frozen structural ABI for the closure environment.
	newImportedMap := func() *types.Named {
		imported := types.NewPackage("internal/sync", "sync")
		name := types.NewTypeName(token.NoPos, imported, "HashTrieMap", nil)
		return types.NewNamed(name, types.NewStruct(nil, nil), nil)
	}
	closureContext := func(mapType *types.Named) types.Type {
		return types.NewPointer(types.NewStruct([]*types.Var{
			types.NewField(token.NoPos, nil, "ht", types.NewPointer(types.NewPointer(mapType)), false),
		}, nil))
	}
	targetClosureCtx := closureContext(newImportedMap())
	producerClosureCtx := closureContext(newImportedMap())
	if types.Identical(targetClosureCtx, producerClosureCtx) {
		t.Fatal("fixture closure contexts unexpectedly share go/types identity")
	}
	if structuralEmissionABITypeKey(targetClosureCtx) != structuralEmissionABITypeKey(producerClosureCtx) {
		t.Fatal("fixture closure contexts do not share one structural ABI")
	}
	hidden := []*types.Var{
		types.NewParam(token.NoPos, nil, "__llgo_g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "__llgo_out", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "__llgo_ctx", targetClosureCtx),
		types.NewParam(token.NoPos, nil, "value", types.Typ[types.Int]),
	}
	physical := types.NewSignatureType(
		nil, nil, nil, types.NewTuple(hidden...),
		types.NewTuple(types.NewParam(token.NoPos, nil, "handle", types.Typ[types.UnsafePointer])), false,
	)
	target := pkg.NewFunc("target$coro", physical, llssa.InC)
	targetBody := target.MakeBody(1)
	targetBody.Return(prog.Nil(prog.VoidPtr()))
	targetBody.EndBuild()
	targetBody.Dispose()
	resultSlot := types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, nil, "r0", types.Typ[types.Int], false),
	}, nil)
	abi := coroPlainDispatchABI{signature: logical, resultSlotType: resultSlot}
	ctx := &context{prog: prog, pkg: pkg}
	thunkName := coroCoroDispatchThunkPrefix + "captured"
	thunkExpr := ctx.newCoroDynamicDispatchEntryThunk(thunkName, target.Expr, abi, coro.EmitCoroutine, producerClosureCtx)
	descriptorName := coroPlainDispatchDescriptorPrefix + "captured"
	pkg.NewCoroDispatchDescriptor(descriptorName, llssa.CoroDispatchDescriptorOptions{
		Version:   llssa.CoroDispatchVersionV1,
		Flags:     llssa.CoroDispatchFlagHasCoro,
		Signature: logical,
		CoroEntry: thunkExpr,
		CodeEntry: target.Expr,
		Result:    prog.Type(resultSlot, llssa.InC),
	})

	module := pkg.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify captured coroutine descriptor producer: %v\n%s", err, module.String())
	}
	global := module.NamedGlobal(descriptorName)
	if global.IsNil() {
		t.Fatal("captured coroutine descriptor global is absent")
	}
	if got := global.Initializer().Operand(1).ZExtValue(); got != uint64(llssa.CoroDispatchFlagHasCoro) {
		t.Fatalf("captured coroutine descriptor flags = %#x, want HasCoro without NoCapture", got)
	}
	thunk := module.NamedFunction(thunkName)
	call := coroDispatchProducerOnlyCallTo(t, thunk, target.Name())
	hiddenCallAttrs := 0
	for _, name := range []string{"nest", "swiftself"} {
		kind := llvm.AttributeKindID(name)
		if kind == 0 {
			continue
		}
		if attr := thunk.GetEnumAttributeAtIndex(3, kind); !attr.IsNil() {
			t.Fatalf("descriptor thunk env unexpectedly uses %s instead of the ordinary C ABI:\n%s", name, module.String())
		}
		if attr := call.GetCallSiteEnumAttribute(3, kind); !attr.IsNil() {
			hiddenCallAttrs++
		}
	}
	if hiddenCallAttrs != 1 {
		t.Fatalf("descriptor thunk final call has %d hidden context attributes, want exactly one:\n%s", hiddenCallAttrs, module.String())
	}
	if got := call.OperandsCount() - 1; got != 4 {
		t.Fatalf("captured coroutine thunk target arguments = %d, want g+out+ctx+source argument", got)
	}
	for i := 0; i < 4; i++ {
		if call.Operand(i).C != thunk.Param(i).C {
			t.Fatalf("captured coroutine thunk target argument %d does not preserve descriptor (g,out,env,arg) order:\n%s", i, module.String())
		}
	}
}

func TestCoroDynamicDispatchProducerAcceptsMultiTargetScalarValue(t *testing.T) {
	const source = `package foo
func A(value int) int { return value + 1 }
func B(value int) int { return value + 2 }
func Root(which bool) func(int) int {
	var fn func(int) int
	if which { fn = A } else { fn = B }
	return fn
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	root := ssaPkg.Func("Root")
	prog := newLLSSAProg(t)
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
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	foundMultiTarget := false
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			value, ok := instruction.(ssa.Value)
			if !ok {
				continue
			}
			valuePlan, planned := plan.ValuePlan(value)
			if planned && len(valuePlan.Funcs) == 1 && valuePlan.Funcs[0].Rep == coro.Dispatch && len(valuePlan.Funcs[0].Targets) == 2 {
				foundMultiTarget = true
			}
		}
	}
	if !foundMultiTarget {
		t.Fatal("Root has no scalar Dispatch value carrying both A and B")
	}
	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: &Compilation{
			CoroPlan:         plan,
			EmissionUniverse: universe,

			CoroABI:      coro.PhysicalABIV1,
			SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
			PanicABI:     coro.PanicExplicitStatusABIV0,
			FuncRepABI:   coro.FuncRepABIV1}},
	)
	if err != nil {
		t.Fatalf("compile multi-target descriptor producer: %v", err)
	}
	if err := llvm.VerifyModule(compiled.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify multi-target descriptor producer: %v\n%s", err, compiled.Module().String())
	}
	descriptors := 0
	for global := compiled.Module().FirstGlobal(); !global.IsNil(); global = llvm.NextGlobal(global) {
		if strings.HasPrefix(global.Name(), coroPlainDispatchDescriptorPrefix) {
			descriptors++
		}
	}
	if descriptors != 2 {
		t.Fatalf("multi-target descriptor globals = %d, want one each for A and B\n%s", descriptors, compiled.Module().String())
	}
}

func TestCoroDynamicDispatchProducerUsesTypedMultiResultABI(t *testing.T) {
	const source = `package foo
func Target(fd int, data []byte) (int, error) { return fd + len(data), nil }
func Root() func(int, []byte) (int, error) { return Target }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	root := ssaPkg.Func("Root")
	target := ssaPkg.Func("Target")
	prog := newLLSSAProg(t)
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
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.SyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.FuncRep != coro.Dispatch || targetPlan.Emission != coro.EmitPlain {
		t.Fatalf("multi-result Target plan = %+v, present=%t; want plain Dispatch producer", targetPlan, ok)
	}
	if err := validateCoroDynamicDispatchTarget(target, targetPlan); err != nil {
		t.Fatalf("multi-result slice/error descriptor target rejected: %v", err)
	}

	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: &Compilation{
			CoroPlan:         plan,
			EmissionUniverse: universe,

			CoroABI:      coro.PhysicalABIV1,
			SchedulerABI: coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
			PanicABI:     coro.PanicExplicitStatusABIV0,
			FuncRepABI:   coro.FuncRepABIV1}},
	)
	if err != nil {
		t.Fatalf("compile multi-result descriptor producer: %v", err)
	}
	module := compiled.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify multi-result descriptor producer: %v\n%s", err, module.String())
	}
	descriptor := coroDispatchProducerOnlyGlobalWithPrefix(t, module, coroPlainDispatchDescriptorPrefix)
	if got := descriptor.Initializer().Operand(1).ZExtValue(); got != uint64(llssa.CoroDispatchFlagHasPlain|llssa.CoroDispatchFlagNoCapture) {
		t.Fatalf("multi-result descriptor flags = %#x, want HasPlain|NoCapture", got)
	}
	if descriptor.Initializer().Operand(6).ZExtValue() == 0 || descriptor.Initializer().Operand(7).ZExtValue() == 0 {
		t.Fatal("multi-result descriptor did not publish its typed result-slot layout")
	}
	thunk := coroDispatchProducerOnlyFunctionWithPrefix(t, module, coroPlainDispatchThunkPrefix)
	call := coroDispatchProducerOnlyCallTo(t, thunk, "")
	if call.Type().TypeKind() != llvm.StructTypeKind {
		t.Fatalf("multi-result thunk target call type = %v, want tuple struct", call.Type().TypeKind())
	}
}

func coroDispatchProducerOnlyGlobalWithPrefix(t *testing.T, module llvm.Module, prefix string) llvm.Value {
	t.Helper()
	var found llvm.Value
	for global := module.FirstGlobal(); !global.IsNil(); global = llvm.NextGlobal(global) {
		if !strings.HasPrefix(global.Name(), prefix) {
			continue
		}
		if !found.IsNil() {
			t.Fatalf("multiple globals with prefix %q", prefix)
		}
		found = global
	}
	if found.IsNil() {
		t.Fatalf("no global with prefix %q", prefix)
	}
	return found
}

func coroDispatchProducerOnlyFunctionWithPrefix(t *testing.T, module llvm.Module, prefix string) llvm.Value {
	t.Helper()
	var found llvm.Value
	for function := module.FirstFunction(); !function.IsNil(); function = llvm.NextFunction(function) {
		if !strings.HasPrefix(function.Name(), prefix) {
			continue
		}
		if !found.IsNil() {
			t.Fatalf("multiple functions with prefix %q", prefix)
		}
		found = function
	}
	if found.IsNil() {
		t.Fatalf("no function with prefix %q", prefix)
	}
	return found
}

func coroDispatchProducerOnlyCallTo(t *testing.T, function llvm.Value, targetName string) llvm.Value {
	t.Helper()
	var found llvm.Value
	for _, block := range function.BasicBlocks() {
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if instruction.InstructionOpcode() != llvm.Call || targetName != "" && instruction.CalledValue().Name() != targetName {
				continue
			}
			if !found.IsNil() {
				t.Fatalf("function %q has multiple matching calls to %q", function.Name(), targetName)
			}
			found = instruction
		}
	}
	if found.IsNil() {
		t.Fatalf("function %q has no matching call to %q", function.Name(), targetName)
	}
	return found
}
