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
	"fmt"
	"go/ast"
	"go/importer"
	"go/token"
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

const coroWorkerForeignTestSource = `package foreignworker

import "unsafe"

type FD int32
type Count uintptr

//llgo:coro worker
//go:linkname foreign C.foreign_word_probe
func foreign(FD, unsafe.Pointer, Count) FD

func Root(fd FD, pointer unsafe.Pointer, count Count) FD {
	return foreign(fd, pointer, count)
}
`

const coroWorkerGenericForeignTestSource = `package foreignworker

import "unsafe"

type FD int32
type Count uintptr

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete
//go:linkname foreign C.foreign_word_probe
func foreign(FD, unsafe.Pointer, Count) FD

func Root(fd FD, pointer unsafe.Pointer, count Count) FD {
	return foreign(fd, pointer, count)
}
`

const coroWorkerDynamicForeignTestSource = `package foreignworker

//llgo:type C
type Callback func(int32) int32

func Root(callback Callback, value int32) int32 {
	return callback(value)
}
`

const coroManagedReentryForeignTestSource = `package foreignworker

import _ "unsafe"

var ready chan struct{}

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=managed-callback memory=borrow-until-return
//go:linkname foreign C.foreign_managed_reentry_probe
func foreign(func(int32) int32, int32) int32

func callback(value int32) int32 {
	<-ready
	return value + 1
}

func Root(value int32) int32 {
	return foreign(callback, value)
}
`

const coroSameMForeignTestSource = `package foreignworker

import _ "unsafe"

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=caller-thread reentry=none memory=borrow-until-return
//go:linkname foreign C.foreign_caller_thread_probe
func foreign(int32) int32

func Root(value int32) int32 {
	return foreign(value)
}
`

const coroManagedReentryPlainForeignTestSource = `package foreignworker

import _ "unsafe"

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=managed-callback memory=borrow-until-return
//go:linkname foreign C.foreign_managed_plain_reentry_probe
func foreign(func(int32) int32, int32) int32

func callback(value int32) int32 {
	return value + 1
}

func Root(value int32) int32 {
	return foreign(callback, value)
}
`

type preparedCoroWorkerForeignFixture struct {
	prog     llssa.Program
	ssaPkg   *ssa.Package
	files    []*ast.File
	universe *EmissionUniverse
	plan     *coro.SSAPlan
	root     *ssa.Function
	call     *ssa.Call
}

func coroWorkerCallableForeignSource(progress, affinity, reentry, memory string) string {
	return fmt.Sprintf(`package foreignworker
import _ "unsafe"
//llgo:coro contract foreign.v1 scope=declaration progress=%s affinity=%s reentry=%s memory=%s
//go:linkname foreign C.foreign_callable_probe
func foreign(uintptr) uintptr
func Root(value uintptr) uintptr { return foreign(value) }
`, progress, affinity, reentry, memory)
}

func prepareCoroWorkerForeignFixture(t *testing.T, source, rootName string) preparedCoroWorkerForeignFixture {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	prog.SetRuntime(func() *types.Package {
		runtimePackage, err := importer.For("source", nil).Import(llssa.PkgRuntime)
		if err != nil {
			t.Fatal("load runtime failed:", err)
		}
		if runtimePackage.Scope().Lookup("CoroWorkerParkV1") == nil {
			name := types.NewTypeName(token.NoPos, runtimePackage, "CoroWorkerParkV1", nil)
			types.NewNamed(name, types.NewArray(types.Typ[types.Uintptr], 32), nil)
			if previous := runtimePackage.Scope().Insert(name); previous != nil {
				t.Fatalf("install test runtime type: duplicate %v", previous)
			}
		}
		return runtimePackage
	})
	// Production import records //llgo:type background metadata before the
	// emission universe freezes physical signatures. Mirror that ordering so C
	// callback word-shape tests exercise the real ABI.
	ParsePkgSyntax(prog, ssaPkg.Prog.Fset, ssaPkg.Pkg, files)
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog,
		nil,
		[]EmissionPackage{{SSA: ssaPkg, Files: files}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	root := ssaPkg.Func(rootName)
	if root == nil {
		prog.Dispose()
		t.Fatalf("foreign worker fixture lacks root %q", rootName)
	}
	var foreignCall *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil {
				continue
			}
			foreign := false
			if target := call.Common().StaticCallee(); target != nil {
				background, classified, backgroundErr := universe.FunctionBackground(target)
				foreign = backgroundErr == nil && classified && background == llssa.InC
			} else if !call.Common().IsInvoke() && call.Common().Method == nil {
				foreign = prog.TypeBackground(call.Common().Value.Type()) == llssa.InC
			}
			if foreign {
				if foreignCall != nil {
					prog.Dispose()
					t.Fatalf("foreign worker root %q has multiple C calls", rootName)
				}
				foreignCall = call
			}
		}
	}
	if foreignCall == nil {
		prog.Dispose()
		t.Fatalf("foreign worker root %q has no exact C call", rootName)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			background, classified, backgroundErr := universe.FunctionBackground(fn)
			if backgroundErr != nil {
				return coro.SSAFunctionPolicy{}, backgroundErr
			}
			if classified && background == llssa.InC {
				worker, workerCertified, workerErr := universe.CoroForeignWorkerCertificate(fn)
				if workerErr != nil {
					return coro.SSAFunctionPolicy{}, workerErr
				}
				callable, callableCertified, callableErr := universe.CoroCallableContractCertificate(fn)
				if callableErr != nil {
					return coro.SSAFunctionPolicy{}, callableErr
				}
				if workerCertified && callableCertified {
					return coro.SSAFunctionPolicy{}, fmt.Errorf("mutually exclusive legacy worker and generic callable certificates")
				}
				if callableCertified {
					external := coro.ExternalUnknownForeign
					exec := coro.BlockForeign | coro.IRQUnsafe | coro.CallableContractExecConstraints(callable.Contract)
					switch callable.Contract.Progress {
					case coro.ProgressExecutorSafe:
						external = coro.ExternalKnown
						exec &^= coro.BlockForeign
					case coro.ProgressMayBlock, coro.ProgressUnknown, coro.ProgressAsyncCompletion:
					case coro.ProgressNoReturn:
						exec |= coro.NoReturn
					}
					return coro.SSAFunctionPolicy{
						IgnoreBody: true, External: external, OverrideExternal: true,
						Exec: exec, CallableContractCertificate: callable,
					}, nil
				}
				identity := ""
				if workerCertified {
					identity = worker.ID
				}
				return coro.SSAFunctionPolicy{
					IgnoreBody: true, External: coro.ExternalUnknownForeign, OverrideExternal: true,
					Exec: coro.BlockForeign | coro.IRQUnsafe, ForeignWorkerCertificate: identity,
				}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callee := call.Common().StaticCallee()
			return callee != nil && callee.Pkg != nil && callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init", nil
		},
		ClassifyUnknownCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.UnknownTarget, error) {
			if call == foreignCall && call.Common().StaticCallee() == nil {
				return coro.UnknownForeign, nil
			}
			return coro.UnknownManaged, nil
		},
		ClassifyRawCFunctionType: func(typ types.Type) (bool, error) {
			_, signature := types.Unalias(typ).Underlying().(*types.Signature)
			return signature && prog.TypeBackground(typ) == llssa.InC, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return preparedCoroWorkerForeignFixture{
		prog: prog, ssaPkg: ssaPkg, files: files, universe: universe,
		plan: plan, root: root, call: foreignCall,
	}
}

func TestCoroWorkerDynamicRawCCodePointerUsesTypedThunk(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	fixture := prepareCoroWorkerForeignFixture(t, coroWorkerDynamicForeignTestSource, "Root")
	defer fixture.prog.Dispose()

	rootPlan, planned := fixture.plan.FunctionPlan(fixture.root)
	if !planned || rootPlan.Emission != coro.EmitCoroutine ||
		!rootPlan.Effect.Contains(coro.WaitForeign) {
		t.Fatalf("dynamic raw C Root plan = %+v, present=%t", rootPlan, planned)
	}
	callPlan, planned := fixture.plan.CallPlan(fixture.call)
	if !planned || callPlan.Kind != coro.CallForeign ||
		callPlan.Rep != coro.DirectPlain ||
		callPlan.Transport != coro.RawCCodePointer ||
		!callPlan.Open || callPlan.Unresolved != coro.UnknownForeign {
		t.Fatalf("dynamic raw C CallPlan = %+v, present=%t", callPlan, planned)
	}
	shape, recognized, err := validateCoroWorkerForeignCall(
		fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
	)
	if !recognized || err != nil || shape.target != nil || shape.calleeType == nil ||
		shape.calleeField != 0 || shape.argumentBase != 1 || !shape.nilGuard ||
		shape.resultField != 2 {
		t.Fatalf("dynamic raw C worker shape = %+v, recognized=%t, error=%v", shape, recognized, err)
	}

	compilation := &Compilation{
		CoroPlan: fixture.plan, EmissionUniverse: fixture.universe,
		CoroABI:      coro.PhysicalABIV1,
		SchedulerABI: coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0,
		PanicABI:     coro.PanicExplicitStatusABIV0,
		FuncRepABI:   coro.FuncRepABIV1, CoroTargetCapabilities: CoroNativeTargetCapabilities(),
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		fixture.prog, nil, nil, nil, fixture.ssaPkg, fixture.files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify dynamic raw C worker coroutine: %v\n%s", err, module.String())
	}
	body := requireCoroPhysicalFunction(t, module, "foreignworker.Root").String()
	if strings.Contains(body, "call i32 %") {
		t.Fatalf("physical coroutine directly invokes the raw C pointer:\n%s", body)
	}
	for _, symbol := range []string{coroWorkerParkHookV1, coroWorkerResumeHookV1} {
		if got := strings.Count(body, "@"+symbol); got != 1 {
			t.Fatalf("Root %q calls = %d, want one:\n%s", symbol, got, body)
		}
	}
	var thunk llvm.Value
	for function := module.FirstFunction(); !function.IsNil(); function = llvm.NextFunction(function) {
		if strings.HasPrefix(function.Name(), coroWorkerForeignThunkPrefixV1) {
			if !thunk.IsNil() {
				t.Fatalf("module has multiple dynamic foreign thunks: %q and %q", thunk.Name(), function.Name())
			}
			thunk = function
		}
	}
	if thunk.IsNil() || !regexp.MustCompile(`call i32 %[^(]+\(i32`).MatchString(thunk.String()) {
		t.Fatalf("dynamic worker thunk does not invoke its record callee:\n%s", module.String())
	}
	runCoroABITestPipeline(t, fixture.prog, module)
}

func TestCoroWorkerClosedDefaultForeignCallUsesTypedThunk(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	source := strings.Replace(coroWorkerForeignTestSource, "//llgo:coro worker\n", "", 1)
	fixture := prepareCoroWorkerForeignFixture(t, source, "Root")
	defer fixture.prog.Dispose()
	target := fixture.call.Common().StaticCallee()
	planCertificate, planCertified := fixture.plan.CallableContractCertificate(target)
	universeCertificate, universeCertified, certificateErr := fixture.universe.CoroCallableContractCertificate(target)
	if certificateErr != nil || !planCertified || !universeCertified || planCertificate != universeCertificate {
		t.Fatalf("default worker callable certificates = plan:%+v/%t universe:%+v/%t err:%v", planCertificate, planCertified, universeCertificate, universeCertified, certificateErr)
	}
	if _, legacy := fixture.plan.ForeignWorkerCertificate(target); legacy {
		t.Fatal("generic worker lowering unexpectedly retained a legacy worker certificate")
	}
	rootPlan, planned := fixture.plan.FunctionPlan(fixture.root)
	if !planned || rootPlan.Emission != coro.EmitCoroutine || rootPlan.Primary != coro.PrimaryCoroutine ||
		rootPlan.LocalEffect != coro.NoSuspend || !rootPlan.Effect.Contains(coro.WaitForeign) {
		t.Fatalf("Root plan = %+v, present=%t; want one call-edge wait-foreign coroutine", rootPlan, planned)
	}
	callPlan, planned := fixture.plan.CallPlan(fixture.call)
	if !planned || callPlan.Kind != coro.CallForeign || callPlan.Open || callPlan.Rep != coro.DirectPlain || len(callPlan.Targets) != 1 {
		t.Fatalf("foreign CallPlan = %+v, present=%t", callPlan, planned)
	}
	audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.root, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rootNames(audit.currentFrameRetentionProof().exactCallKeepaliveRoots(fixture.call)), ","); got != "pointer" {
		t.Fatalf("foreign worker keepalive roots = %q, want pointer", got)
	}
	compilation := &Compilation{
		CoroPlan:         fixture.plan,
		EmissionUniverse: fixture.universe,

		CoroABI:      coro.PhysicalABIV1,
		SchedulerABI: coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0,
		PanicABI:     coro.PanicExplicitStatusABIV0,
		FuncRepABI:   coro.FuncRepABIV1, CoroTargetCapabilities: CoroNativeTargetCapabilities(),
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		fixture.prog, nil, nil, nil, fixture.ssaPkg, fixture.files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify foreign worker coroutine: %v\n%s", err, module.String())
	}
	body := requireCoroPhysicalFunction(t, module, "foreignworker.Root").String()
	if strings.Contains(body, "@foreign_word_probe") {
		t.Fatalf("coroutine body directly calls the typed foreign symbol:\n%s", body)
	}
	for _, symbol := range []string{coroWorkerParkHookV1, coroWorkerResumeHookV1} {
		if got := strings.Count(body, "@"+symbol); got != 1 {
			t.Fatalf("Root %q calls = %d, want one:\n%s", symbol, got, body)
		}
	}
	if !strings.Contains(body, "call void (...) @llvm.fake.use(ptr") {
		t.Fatalf("Root does not keep the typed pointer live after worker acknowledgement:\n%s", body)
	}
	if strings.Contains(body, "trunc i64") ||
		!regexp.MustCompile(`getelementptr inbounds (?:nuw )?\{ i32, ptr, i64, i32 \}, ptr [^\n]+, i32 0, i32 3\n\s+%[^\s]+ = load i32`).MatchString(body) {
		t.Fatalf("Root does not load the signed 32-bit result directly from its typed worker record:\n%s", body)
	}
	var thunk llvm.Value
	for function := module.FirstFunction(); !function.IsNil(); function = llvm.NextFunction(function) {
		if strings.HasPrefix(function.Name(), coroWorkerForeignThunkPrefixV1) {
			if !thunk.IsNil() {
				t.Fatalf("module has multiple foreign thunks: %q and %q", thunk.Name(), function.Name())
			}
			thunk = function
		}
	}
	if thunk.IsNil() {
		t.Fatalf("module has no typed foreign worker thunk:\n%s", module.String())
	}
	thunkText := thunk.String()
	for _, pattern := range []string{
		`define linkonce i64 @` + regexp.QuoteMeta(thunk.Name()) + `\(i64`,
		`call i32 @foreign_word_probe\(i32`,
		`inttoptr i64`,
		`store i32 [^\n]+, ptr`,
		`ret i64 0`,
	} {
		if !regexp.MustCompile(pattern).MatchString(thunkText) {
			t.Errorf("typed thunk lacks %q:\n%s", pattern, thunkText)
		}
	}
	runCoroABITestPipeline(t, fixture.prog, module)
	resume := module.NamedFunction("foreignworker.Root$coro.resume")
	if resume.IsNil() || !strings.Contains(resume.String(), "call i32 @"+coroWorkerResumeHookV1) ||
		!strings.Contains(resume.String(), "call void (...) @llvm.fake.use(ptr") {
		t.Fatalf("CoroSplit lost foreign worker resume:\n%s", module.String())
	}
}

func TestCoroManagedReentryForeignCallUsesStaticCoroutineAdapter(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	fixture := prepareCoroWorkerForeignFixture(
		t, coroManagedReentryForeignTestSource, "Root",
	)
	defer fixture.prog.Dispose()

	shape, recognized, err := validateCoroWorkerForeignCall(
		fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
	)
	if !recognized || err != nil ||
		shape.mode != coroForeignCallModeSameM ||
		len(shape.reentryCallbacks) != 1 ||
		shape.reentryCallbacks[0] == nil ||
		len(shape.rawCallbacks) != 0 {
		t.Fatalf(
			"managed-reentry shape = %+v, recognized=%t, error=%v",
			shape, recognized, err,
		)
	}
	callbackPlan, planned := fixture.plan.FunctionPlan(shape.reentryCallbacks[0])
	if !planned || callbackPlan.Emission != coro.EmitCoroutine ||
		callbackPlan.Primary != coro.PrimaryCoroutine ||
		!callbackPlan.Effect.Contains(coro.MayPark) ||
		callbackPlan.RawPlainDemand || callbackPlan.RawPlainEntry {
		t.Fatalf(
			"managed callback plan = %+v, present=%t; want one inferred coroutine-only target",
			callbackPlan, planned,
		)
	}

	compilation := &Compilation{
		CoroPlan: fixture.plan, EmissionUniverse: fixture.universe,
		CoroABI:                coro.PhysicalABIV1,
		SchedulerABI:           coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0,
		PanicABI:               coro.PanicExplicitStatusABIV0,
		FuncRepABI:             coro.FuncRepABIV1,
		CoroTargetCapabilities: CoroNativeTargetCapabilities(),
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		fixture.prog, nil, nil, nil, fixture.ssaPkg, fixture.files,
		goembed.VarMap{}, PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify managed-reentry coroutine: %v\n%s", err, module.String())
	}
	root := requireCoroPhysicalFunction(t, module, "foreignworker.Root").String()
	if !strings.Contains(root, "@"+coroSameMForeignCallHookV1) ||
		strings.Contains(root, "@"+coroWorkerParkHookV1) ||
		strings.Contains(root, "@foreign_managed_reentry_probe") {
		t.Fatalf("Root did not select the managed-reentry boundary:\n%s", root)
	}

	var adapter llvm.Value
	var thunk llvm.Value
	for function := module.FirstFunction(); !function.IsNil(); function = llvm.NextFunction(function) {
		switch {
		case strings.HasPrefix(function.Name(), coroForeignReentryAdapterPrefixV1):
			if !adapter.IsNil() {
				t.Fatalf("module has multiple managed callback adapters")
			}
			adapter = function
		case strings.HasPrefix(function.Name(), coroWorkerForeignThunkPrefixV1):
			if !thunk.IsNil() {
				t.Fatalf("module has multiple managed foreign thunks")
			}
			thunk = function
		}
	}
	if adapter.IsNil() {
		t.Fatalf("module has no static managed callback adapter:\n%s", module.String())
	}
	adapterText := adapter.String()
	for _, symbol := range []string{
		coroForeignReentryAcquireHookV1,
		coroForeignReentryRunHookV1,
		coroForeignReentryFailureHookV1,
	} {
		if !strings.Contains(adapterText, "@"+symbol) {
			t.Errorf("managed callback adapter lacks %q:\n%s", symbol, adapterText)
		}
	}
	if !strings.Contains(adapterText, "foreignworker.callback$coro") {
		t.Errorf("managed callback adapter lacks the exact coroutine target:\n%s", adapterText)
	}
	if thunk.IsNil() ||
		!strings.Contains(thunk.String(), "@foreign_managed_reentry_probe") {
		t.Fatalf("managed foreign thunk does not invoke the exact C declaration:\n%s", module.String())
	}
	runCoroABITestPipeline(t, fixture.prog, module)
}

func TestCoroCallerThreadForeignCallUsesSameMEpisode(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	fixture := prepareCoroWorkerForeignFixture(
		t, coroSameMForeignTestSource, "Root",
	)
	defer fixture.prog.Dispose()

	shape, recognized, err := validateCoroWorkerForeignCall(
		fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
	)
	if !recognized || err != nil ||
		shape.mode != coroForeignCallModeSameM ||
		len(shape.reentryCallbacks) != 0 ||
		len(shape.rawCallbacks) != 0 {
		t.Fatalf(
			"same-M shape = %+v, recognized=%t, error=%v",
			shape, recognized, err,
		)
	}
	rootPlan, planned := fixture.plan.FunctionPlan(fixture.root)
	if !planned {
		t.Fatal("same-M root has no frozen function plan")
	}
	workerOnlyErr := func() error {
		capabilities := fixture.universe.coroCapabilities
		defer func() {
			fixture.universe.coroCapabilities = capabilities
		}()
		fixture.universe.coroCapabilities =
			coro.NewTargetCapabilities(true, false, false)
		return validateCoroPhysicalABIWithUniverseCapabilities(
			fixture.root, rootPlan, fixture.plan, fixture.universe,
			true, true, false, true,
		)
	}()
	if workerOnlyErr == nil || !strings.Contains(
		workerOnlyErr.Error(), "same-M foreign call requires the native foreign-episode capability",
	) {
		t.Fatalf("worker-only same-M validation error = %v", workerOnlyErr)
	}

	compilation := &Compilation{
		CoroPlan: fixture.plan, EmissionUniverse: fixture.universe,
		CoroABI:                coro.PhysicalABIV1,
		SchedulerABI:           coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0,
		PanicABI:               coro.PanicExplicitStatusABIV0,
		FuncRepABI:             coro.FuncRepABIV1,
		CoroTargetCapabilities: CoroNativeTargetCapabilities(),
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		fixture.prog, nil, nil, nil, fixture.ssaPkg, fixture.files,
		goembed.VarMap{}, PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify same-M foreign coroutine: %v\n%s", err, module.String())
	}
	root := requireCoroPhysicalFunction(t, module, "foreignworker.Root").String()
	if !strings.Contains(root, "@"+coroSameMForeignCallHookV1) ||
		strings.Contains(root, "@"+coroWorkerParkHookV1) ||
		strings.Contains(root, "@foreign_caller_thread_probe") {
		t.Fatalf("Root did not select the same-M foreign episode:\n%s", root)
	}
	var thunk llvm.Value
	for function := module.FirstFunction(); !function.IsNil(); function = llvm.NextFunction(function) {
		if strings.HasPrefix(function.Name(), coroWorkerForeignThunkPrefixV1) {
			if !thunk.IsNil() {
				t.Fatalf("module has multiple same-M foreign thunks")
			}
			thunk = function
		}
	}
	if thunk.IsNil() ||
		!strings.Contains(thunk.String(), "@foreign_caller_thread_probe") {
		t.Fatalf("same-M foreign thunk does not invoke the exact C declaration:\n%s", module.String())
	}
	runCoroABITestPipeline(t, fixture.prog, module)
}

func TestImportedLibraryForeignCallableDrivesPhysicalSameMEpisode(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	producer := prepareCoroWorkerForeignFixture(
		t, coroSameMForeignTestSource, "Root",
	)
	defer producer.prog.Dispose()
	producerTarget := producer.call.Common().StaticCallee()
	functionID, identified := producer.plan.FunctionID(producerTarget)
	identity, identityOK, identityErr :=
		producer.universe.CoroCallableIdentityCertificate(producerTarget)
	contract, contractOK, contractErr :=
		producer.universe.CoroCallableContractCertificate(producerTarget)
	if !identified || identityErr != nil || !identityOK ||
		contractErr != nil || !contractOK {
		t.Fatalf(
			"producer callable facts = id:%q/%t identity:%+v/%t/%v contract:%+v/%t/%v",
			functionID, identified, identity, identityOK, identityErr,
			contract, contractOK, contractErr,
		)
	}
	fact := coro.LibraryEffectForeignCallable{
		Function:    functionID,
		Identity:    identity,
		Contract:    contract,
		HasContract: true,
	}
	if err := fact.Validate(); err != nil {
		t.Fatal(err)
	}

	consumerSource := strings.Replace(
		coroSameMForeignTestSource,
		"//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=caller-thread reentry=none memory=borrow-until-return",
		"// producer archive owns the exact callable contract",
		1,
	)
	consumer := prepareCoroWorkerForeignFixture(t, consumerSource, "Root")
	defer consumer.prog.Dispose()
	consumerTarget := consumer.call.Common().StaticCallee()
	defaulted, err := consumer.universe.CoroLibraryEffects().
		CallableContractDefault(consumerTarget)
	if err != nil || !defaulted {
		t.Fatalf("consumer callable contract defaulted = %t, %v", defaulted, err)
	}
	functionIDs := consumer.universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI =
		coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	consumerID, err := coro.StableFunctionID(consumerTarget, functionIDs)
	if err != nil || consumerID != fact.Function {
		t.Fatalf("consumer function ID = %q, %v; producer=%q", consumerID, err, fact.Function)
	}

	ssaUniverse, err := coro.NewSSAEmissionUniverse(
		consumer.ssaPkg.Prog, consumer.universe.Functions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	importedPlan, err := coro.AnalyzeSSA(
		consumer.ssaPkg.Prog,
		coro.Roots{{Function: consumer.root, Demand: coro.AsyncDemand}},
		coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          functionIDs,
			MaxPlainInstructions: -1,
			ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if function != consumerTarget {
					return coro.SSAFunctionPolicy{}, nil
				}
				return coro.SSAFunctionPolicy{
					Exec: coro.BlockForeign | coro.IRQUnsafe |
						coro.CallableContractExecConstraints(fact.Contract.Contract),
					CallableIdentityCertificate: fact.Identity,
					CallableContractCertificate: fact.Contract,
					IgnoreBody:                  true,
					External:                    coro.ExternalUnknownForeign,
					OverrideExternal:            true,
				}, nil
			},
			ClassifyElidedCall: func(
				_ *ssa.Function, call ssa.CallInstruction,
			) (bool, error) {
				callee := call.Common().StaticCallee()
				return callee != nil && callee.Pkg != nil &&
					callee.Pkg.Pkg.Path() == "unsafe" &&
					callee.Name() == "init", nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	library := map[*ssa.Function]coro.LibraryEffectForeignCallable{
		consumerTarget: fact,
	}
	shape, recognized, err := validateCoroWorkerForeignCallWithAuthority(
		coroStaticForeignCallAuthority{
			plan: importedPlan, universe: consumer.universe,
			libraryForeign: library,
		},
		consumer.call,
		consumer.prog.PointerSize(),
	)
	if !recognized || err != nil || shape.mode != coroForeignCallModeSameM {
		t.Fatalf(
			"imported same-M shape = %+v, recognized=%t, error=%v",
			shape, recognized, err,
		)
	}

	endianness := ""
	switch consumer.prog.TargetData().ByteOrder() {
	case llvm.LittleEndian:
		endianness = "little"
	case llvm.BigEndian:
		endianness = "big"
	default:
		t.Fatal("unknown target byte order")
	}
	capabilities := CoroNativeTargetCapabilities()
	planMetadata := coro.PlanDigestMetadata{
		CoroABI:        coro.PhysicalABIV1,
		SchedulerABI:   functionIDs.SchedulerABI,
		PanicABI:       coro.PanicExplicitStatusABIV0,
		FuncRepABI:     coro.FuncRepABIV1,
		TargetTriple:   consumer.prog.TargetSpec().Triple,
		TargetCPU:      consumer.prog.TargetSpec().CPU,
		TargetFeatures: consumer.prog.TargetSpec().Features,
		TargetABI:      consumer.prog.TargetSpec().TargetABI,
		PointerBits:    consumer.prog.PointerSize() * 8,
		Endianness:     endianness,
		DataLayout:     consumer.prog.DataLayout(),
	}
	libraryMetadata := coro.LibraryEffectMetadata{
		FunctionIDSchema:   coro.FunctionIDSchema,
		CoroABI:            planMetadata.CoroABI,
		SchedulerABI:       planMetadata.SchedulerABI,
		PanicABI:           planMetadata.PanicABI,
		FuncRepABI:         planMetadata.FuncRepABI,
		TargetTriple:       planMetadata.TargetTriple,
		TargetCPU:          planMetadata.TargetCPU,
		TargetFeatures:     planMetadata.TargetFeatures,
		TargetABI:          planMetadata.TargetABI,
		PointerBits:        planMetadata.PointerBits,
		Endianness:         planMetadata.Endianness,
		DataLayout:         planMetadata.DataLayout,
		TargetCapabilities: capabilities,
	}
	if err := consumer.universe.CoroLibraryEffects().ValidateForeignCallable(
		consumerTarget, libraryMetadata, fact,
	); err != nil {
		t.Fatal(err)
	}
	compilation := &Compilation{
		CoroPlan:                    importedPlan,
		CoroPlanDigest:              strings.Repeat("0", 64),
		CoroPlanMetadata:            planMetadata,
		CoroLibraryEffectMetadata:   libraryMetadata,
		CoroLibraryForeignCallables: library,
		EmissionUniverse:            consumer.universe,
		CoroABI:                     planMetadata.CoroABI,
		SchedulerABI:                planMetadata.SchedulerABI,
		PanicABI:                    planMetadata.PanicABI,
		FuncRepABI:                  planMetadata.FuncRepABI,
		CoroTargetCapabilities:      capabilities,
	}
	if err := compilation.validateCoroLibraryEffects(); err != nil {
		t.Fatal(err)
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		consumer.prog, nil, nil, nil, consumer.ssaPkg, consumer.files,
		goembed.VarMap{}, PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify imported same-M coroutine: %v\n%s", err, module.String())
	}
	root := requireCoroPhysicalFunction(
		t, module, "foreignworker.Root",
	).String()
	if !strings.Contains(root, "@"+coroSameMForeignCallHookV1) ||
		strings.Contains(root, "@"+coroWorkerParkHookV1) ||
		strings.Contains(root, "@foreign_caller_thread_probe") {
		t.Fatalf("imported metadata did not select the same-M episode:\n%s", root)
	}
	records, err := CoroLibraryEffectSummaryRecords(pkg)
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := coro.ParseLibraryEffectSummaryRecords(records)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 ||
		len(summaries[0].ForeignCallables) != 1 ||
		summaries[0].ForeignCallables[0] != fact {
		t.Fatalf("transitively published foreign callable = %+v", summaries)
	}
}

func TestCoroManagedReentryPlainCallbackUsesThinCoroutineRamp(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	fixture := prepareCoroWorkerForeignFixture(
		t, coroManagedReentryPlainForeignTestSource, "Root",
	)
	defer fixture.prog.Dispose()
	shape, recognized, err := validateCoroWorkerForeignCall(
		fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
	)
	if !recognized || err != nil ||
		shape.mode != coroForeignCallModeSameM ||
		len(shape.reentryCallbacks) != 1 {
		t.Fatalf(
			"plain managed-reentry shape = %+v, recognized=%t, error=%v",
			shape, recognized, err,
		)
	}
	callback := shape.reentryCallbacks[0]
	callbackPlan, planned := fixture.plan.FunctionPlan(callback)
	if !planned || callbackPlan.Emission != coro.EmitPlain ||
		callbackPlan.Primary != coro.PrimaryPlain ||
		callbackPlan.Effect != coro.NoSuspend ||
		callbackPlan.RawPlainDemand || callbackPlan.RawPlainEntry {
		t.Fatalf(
			"plain callback plan = %+v, present=%t; want one unchanged plain primary",
			callbackPlan, planned,
		)
	}

	compilation := &Compilation{
		CoroPlan: fixture.plan, EmissionUniverse: fixture.universe,
		CoroABI:                coro.PhysicalABIV1,
		SchedulerABI:           coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0,
		PanicABI:               coro.PanicExplicitStatusABIV0,
		FuncRepABI:             coro.FuncRepABIV1,
		CoroTargetCapabilities: CoroNativeTargetCapabilities(),
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		fixture.prog, nil, nil, nil, fixture.ssaPkg, fixture.files,
		goembed.VarMap{}, PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify plain managed-reentry coroutine: %v\n%s", err, module.String())
	}
	if !module.NamedFunction("foreignworker.callback$coro").IsNil() {
		t.Fatal("plain callback unexpectedly acquired a cloned coroutine primary")
	}
	var ramp llvm.Value
	var adapter llvm.Value
	for function := module.FirstFunction(); !function.IsNil(); function = llvm.NextFunction(function) {
		switch {
		case strings.HasPrefix(function.Name(), coroForeignReentryPlainRampPrefixV1):
			ramp = function
		case strings.HasPrefix(function.Name(), coroForeignReentryAdapterPrefixV1):
			adapter = function
		}
	}
	if ramp.IsNil() || adapter.IsNil() {
		t.Fatalf("plain callback lacks a thin ramp or C adapter:\n%s", module.String())
	}
	if !strings.Contains(ramp.String(), "@foreignworker.callback") ||
		!strings.Contains(ramp.String(), "llvm.coro.") {
		t.Fatalf("thin ramp does not call the sole plain primary:\n%s", ramp.String())
	}
	if !strings.Contains(adapter.String(), "@"+ramp.Name()) {
		t.Fatalf("C adapter does not create the thin coroutine ramp:\n%s", adapter.String())
	}
	runCoroABITestPipeline(t, fixture.prog, module)
}

func TestCoroManagedReentryRejectsOpenOrCapturedCallbackTargets(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "captured closure",
			body: `
func Root(value int32) int32 {
	delta := int32(1)
	return foreign(func(input int32) int32 { return input + delta }, value)
}
`,
		},
		{
			name: "open function value",
			body: `
var callbackValue func(int32) int32
func Root(value int32) int32 { return foreign(callbackValue, value) }
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := `package foreignworker
import _ "unsafe"
//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=managed-callback memory=borrow-until-return
//go:linkname foreign C.foreign_managed_reentry_reject_probe
func foreign(func(int32) int32, int32) int32
` + test.body
			fixture := prepareCoroWorkerForeignFixture(t, source, "Root")
			defer fixture.prog.Dispose()
			_, recognized, err := validateCoroWorkerForeignCall(
				fixture.plan, fixture.universe, fixture.call,
				fixture.prog.PointerSize(),
			)
			if !recognized || err == nil ||
				!strings.Contains(
					err.Error(),
					"requires one exact non-capturing managed callback target",
				) {
				t.Fatalf(
					"managed callback rejection = recognized:%t err:%v",
					recognized, err,
				)
			}
		})
	}
}

func TestCoroWorkerForeignCallShapeAcceptsTypedRecordABIs(t *testing.T) {
	tests := []struct {
		name        string
		declaration string
		statement   string
		wantArgs    int
		wantResult  bool
	}{
		{"zero arguments", "func foreign() uintptr", "_ = foreign()", 0, true},
		{"float argument", "func foreign(float64) uintptr", "_ = foreign(1)", 1, true},
		{"aggregate argument", "func foreign(struct{ X uintptr }) uintptr", "_ = foreign(struct{ X uintptr }{})", 1, true},
		{"float result", "func foreign(uintptr) float64", "_ = foreign(1)", 1, true},
		{"foreign or borrowed pointer result", "func foreign(uintptr) *byte", "_ = foreign(1)", 1, true},
		{"foreign or borrowed pointer aggregate result", "func foreign(uintptr) struct{ P *byte }", "_ = foreign(1)", 1, true},
		{
			"more than queue word limit",
			"func foreign(uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr) uintptr",
			"_ = foreign(0, 0, 0, 0, 0, 0, 0, 0, 0, 0)",
			10,
			true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := `package foreignworker
import _ "unsafe"
//llgo:coro worker
//go:linkname foreign C.foreign_typed_record_probe
` + test.declaration + `
func Root() { ` + test.statement + ` }
`
			fixture := prepareCoroWorkerForeignFixture(t, source, "Root")
			defer fixture.prog.Dispose()
			shape, recognized, err := validateCoroWorkerForeignCall(
				fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
			)
			if !recognized || err != nil || shape.record == nil || shape.argc != test.wantArgs ||
				(shape.result != nil) != test.wantResult {
				t.Fatalf("typed-record preflight = shape:%+v recognized:%t err:%v", shape, recognized, err)
			}
		})
	}
}

func TestCoroWorkerForeignCallShapeAcceptsCDeclarationMethod(t *testing.T) {
	const source = `package foreignworker
type Handle struct{}
//llgo:link (*Handle).Read C.foreign_method_probe
func (handle *Handle) Read(value uintptr) uintptr { return 0 }
func Root(handle *Handle) uintptr { return handle.Read(1) }
`
	fixture := prepareCoroWorkerForeignFixture(t, source, "Root")
	defer fixture.prog.Dispose()
	shape, recognized, err := validateCoroWorkerForeignCall(
		fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
	)
	if !recognized || err != nil || shape.target == nil || shape.signature == nil ||
		shape.signature.Recv() != nil || shape.argc != 2 ||
		shape.signature.Params().At(0).Type().String() != "*foreignworker.Handle" {
		t.Fatalf("C method worker shape = %+v, recognized=%t, error=%v", shape, recognized, err)
	}
}

func TestCoroWorkerForeignCallShapeSpecializesCVarargs(t *testing.T) {
	const source = `package foreignworker
import _ "unsafe"
//go:linkname foreign C.foreign_varargs_probe
func foreign(format *byte, __llgo_va_list ...any) uintptr
func Root(format *byte, value uintptr) uintptr { return foreign(format, value) }
`
	fixture := prepareCoroWorkerForeignFixture(t, source, "Root")
	defer fixture.prog.Dispose()
	shape, recognized, err := validateCoroWorkerForeignCall(
		fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
	)
	if !recognized || err != nil || shape.target == nil || shape.signature == nil ||
		!shape.variadic || shape.signature.Variadic() || shape.argc != 2 ||
		shape.signature.Params().At(0).Type().String() != "*byte" ||
		shape.signature.Params().At(1).Type().String() != "uintptr" ||
		shape.record == nil || shape.argumentBase != 0 ||
		shape.record.NumFields() != 3 ||
		shape.record.Field(0).Type().String() != "*byte" ||
		shape.record.Field(1).Type().String() != "uintptr" {
		t.Fatalf("C varargs worker shape = %+v, recognized=%t, error=%v", shape, recognized, err)
	}
}

func TestCoroWorkerForeignCallShapeAcceptsExplicitStringViewAlias(t *testing.T) {
	const source = `package foreignworker
import _ "unsafe"
type StringView = string
//llgo:coro worker
//go:linkname foreign C.foreign_string_view_probe
func foreign(StringView) uintptr
func Root(value string) uintptr { return foreign(value) }
`
	fixture := prepareCoroWorkerForeignFixture(t, source, "Root")
	defer fixture.prog.Dispose()
	shape, recognized, err := validateCoroWorkerForeignCall(
		fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
	)
	if !recognized || err != nil || shape.argc != 1 || shape.record == nil ||
		shape.record.Field(0).Type().String() != "foreignworker.StringView" {
		t.Fatalf("C string-view worker shape = %+v, recognized=%t, error=%v", shape, recognized, err)
	}
}

func TestCoroWorkerForeignCallShapeRejectsUnsafeABIs(t *testing.T) {
	tests := []struct {
		name        string
		declaration string
		statement   string
		want        string
	}{
		{"string argument", "func foreign(string) uintptr", "_ = foreign(\"\")", "argument 0 type string cannot be represented in a typed worker call record"},
		{"slice argument", "func foreign([]byte) uintptr", "_ = foreign(nil)", "argument 0 type []byte cannot be represented in a typed worker call record"},
		{"Go function argument", "func foreign(func()) uintptr", "_ = foreign(func(){})", "argument 0 type func() cannot be represented in a typed worker call record"},
		{"string result", "func foreign(uintptr) string", "_ = foreign(1)", "result type string cannot be represented in a declaration-authorized typed worker call record"},
		{"multiple results", "func foreign(uintptr) (uintptr, uintptr)", "_, _ = foreign(1)", "requires zero or one result"},
		{"non-LLGo variadic", "func foreign(...uintptr) uintptr", "_ = foreign(1)", "requires the trailing __llgo_va_list ...any ABI"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := `package foreignworker
import _ "unsafe"
//llgo:coro worker
//go:linkname foreign C.foreign_reject_probe
` + test.declaration + `
func Root() { ` + test.statement + ` }
`
			fixture := prepareCoroWorkerForeignFixture(t, source, "Root")
			defer fixture.prog.Dispose()
			_, recognized, err := validateCoroWorkerForeignCall(
				fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
			)
			if !recognized || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("preflight error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestCoroWorkerDynamicForeignCallRejectsPointerResultWithoutDeclarationProvenance(t *testing.T) {
	const source = `package foreignworker
//llgo:type C
type Callback func(uintptr) *byte
func Root(callback Callback) *byte { return callback(1) }
`
	fixture := prepareCoroWorkerForeignFixture(t, source, "Root")
	defer fixture.prog.Dispose()
	_, recognized, err := validateCoroWorkerForeignCall(
		fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
	)
	if !recognized || err == nil ||
		!strings.Contains(err.Error(), "result type *byte cannot be represented in a pointer-free typed worker call record") {
		t.Fatalf("dynamic pointer-result preflight = recognized:%t err:%v", recognized, err)
	}
}

func TestCoroWorkerForeignCallAcceptsExplicitCFunctionPointerArgument(t *testing.T) {
	const source = `package foreignworker
import "unsafe"
//llgo:type C
type Callback func(unsafe.Pointer)
//llgo:coro worker
//go:linkname foreign C.foreign_callback_registration_probe
func foreign(Callback, unsafe.Pointer)
func callback(unsafe.Pointer) {}
func Root(pointer unsafe.Pointer) { foreign(callback, pointer) }
`
	fixture := prepareCoroWorkerForeignFixture(t, source, "Root")
	defer fixture.prog.Dispose()
	shape, recognized, err := validateCoroWorkerForeignCall(
		fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
	)
	if !recognized || err != nil || shape.argc != 2 {
		t.Fatalf("C callback worker call = shape:%+v recognized:%t err:%v", shape, recognized, err)
	}
}

func TestCoroWorkerGenericCallableContractAcceptsSupportedMemoryLifetimes(t *testing.T) {
	for _, memory := range []string{"by-value", "borrow-until-return", "borrow-until-complete"} {
		t.Run(memory, func(t *testing.T) {
			fixture := prepareCoroWorkerForeignFixture(t, coroWorkerCallableForeignSource(
				"may-block", "any-thread", "none", memory,
			), "Root")
			defer fixture.prog.Dispose()
			shape, recognized, err := validateCoroWorkerForeignCall(
				fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
			)
			if !recognized || err != nil || shape.target == nil || shape.argc != 1 {
				t.Fatalf("generic callable worker validation = shape:%+v recognized:%t err:%v", shape, recognized, err)
			}
		})
	}
}

func TestCoroWorkerForeignCallRejectsAddressOnlyWordCallableABI(t *testing.T) {
	const source = `package foreignworker
import _ "unsafe"
//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/0
//go:linkname libc_direct_probe_trampoline C.direct_probe
func libc_direct_probe_trampoline()
func Root() { libc_direct_probe_trampoline() }
`
	fixture := prepareCoroWorkerForeignFixture(t, source, "Root")
	defer fixture.prog.Dispose()
	_, recognized, err := validateCoroWorkerForeignCall(
		fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
	)
	if !recognized || err == nil || !strings.Contains(err.Error(), "address-only") ||
		!strings.Contains(err.Error(), "FuncPCABI0-to-llgo.syscall") {
		t.Fatalf("address-only typed foreign call validation = recognized:%t err:%v", recognized, err)
	}
}

func TestCoroWorkerGenericCallableContractRejectsUnsupportedDimensions(t *testing.T) {
	tests := []struct {
		name                        string
		progress, affinity, reentry string
		memory                      string
		want                        string
	}{
		{"unknown progress", "unknown", "any-thread", "none", "by-value", "callable progress"},
		{"executor-safe progress", "executor-safe", "any-thread", "none", "by-value", "callable progress"},
		{"async completion", "async-completion", "any-thread", "none", "by-value", "callable progress"},
		{"no return", "no-return", "any-thread", "none", "by-value", "callable progress"},
		{"unknown affinity", "may-block", "unknown", "none", "by-value", "callable affinity"},
		{"owner affinity", "may-block", "owner-thread", "none", "by-value", "callable affinity"},
		{"host affinity", "may-block", "host-main", "none", "by-value", "callable affinity"},
		{"unknown reentry", "may-block", "any-thread", "unknown", "by-value", "callable reentry"},
		{"caller unknown reentry", "may-block", "caller-thread", "unknown", "by-value", "callable reentry"},
		{"unknown memory", "may-block", "any-thread", "none", "unknown", "callable memory lifetime"},
		{"retained memory", "may-block", "any-thread", "none", "retained", "callable memory lifetime"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := prepareCoroWorkerForeignFixture(t, coroWorkerCallableForeignSource(
				test.progress, test.affinity, test.reentry, test.memory,
			), "Root")
			defer fixture.prog.Dispose()
			target, frozen := fixture.universe.Resolve(fixture.call.Common().StaticCallee())
			if !frozen || target == nil {
				t.Fatal("generic callable target is absent from the frozen universe")
			}
			_, err := (coroStaticForeignCallAuthority{
				plan: fixture.plan, universe: fixture.universe,
			}).authorize(target)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("generic callable worker authorization error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestCoroStaticForeignCallAuthoritySelectsExecutionMode(t *testing.T) {
	tests := []struct {
		name     string
		affinity string
		reentry  string
		wantMode coroForeignCallMode
		want     coro.ReentryClass
	}{
		{
			name: "any thread no reentry", affinity: "any-thread",
			reentry: "none", wantMode: coroForeignCallModeWorker,
			want: coro.ReentryNone,
		},
		{
			name: "any thread managed callback", affinity: "any-thread",
			reentry: "managed-callback", wantMode: coroForeignCallModeSameM,
			want: coro.ReentryManagedCallback,
		},
		{
			name: "caller thread no reentry", affinity: "caller-thread",
			reentry: "none", wantMode: coroForeignCallModeSameM,
			want: coro.ReentryNone,
		},
		{
			name: "caller thread managed callback", affinity: "caller-thread",
			reentry: "managed-callback", wantMode: coroForeignCallModeSameM,
			want: coro.ReentryManagedCallback,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := prepareCoroWorkerForeignFixture(t, coroWorkerCallableForeignSource(
				"may-block", test.affinity, test.reentry, "borrow-until-return",
			), "Root")
			defer fixture.prog.Dispose()
			target, frozen := fixture.universe.Resolve(fixture.call.Common().StaticCallee())
			if !frozen || target == nil {
				t.Fatal("generic callable target is absent from the frozen universe")
			}
			authorization, err := (coroStaticForeignCallAuthority{
				plan: fixture.plan, universe: fixture.universe,
			}).authorize(target)
			if err != nil {
				t.Fatal(err)
			}
			if authorization.mode != test.wantMode ||
				authorization.reentry != test.want {
				t.Fatalf(
					"foreign authorization = %+v; want mode=%d reentry=%s",
					authorization, test.wantMode, test.want,
				)
			}
		})
	}
}

func TestCoroWorkerForeignCallUsesFrozenDefaultContract(t *testing.T) {
	source := strings.Replace(coroWorkerForeignTestSource, "//llgo:coro worker\n", "", 1)
	fixture := prepareCoroWorkerForeignFixture(t, source, "Root")
	defer fixture.prog.Dispose()
	target := fixture.call.Common().StaticCallee()
	certificate, certified, err := fixture.universe.CoroCallableContractCertificate(target)
	if err != nil || !certified ||
		certificate.Contract.Progress != coro.ProgressMayBlock ||
		certificate.Contract.Affinity != coro.AffinityAnyThread ||
		certificate.Contract.Reentry != coro.ReentryNone ||
		certificate.Contract.Memory != coro.MemoryBorrowUntilComplete {
		t.Fatalf("default foreign callable contract = %+v, %t, %v", certificate, certified, err)
	}
	planned, plannedOK := fixture.plan.CallableContractCertificate(target)
	if !plannedOK || planned != certificate {
		t.Fatalf("planned default foreign callable contract = %+v, %t; want %+v", planned, plannedOK, certificate)
	}
	_, recognized, err := validateCoroWorkerForeignCall(
		fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
	)
	if !recognized || err != nil {
		t.Fatalf("default-contract worker call validation = recognized:%t err:%v", recognized, err)
	}
}

func TestCoroWorkerForeignCallRejectsForgedPlanCertificate(t *testing.T) {
	fixture := prepareCoroWorkerForeignFixture(t, coroWorkerForeignTestSource, "Root")
	defer fixture.prog.Dispose()
	ssaUniverse, err := coro.NewSSAEmissionUniverse(fixture.ssaPkg.Prog, fixture.universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := fixture.universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	forged, err := coro.AnalyzeSSA(fixture.ssaPkg.Prog, coro.Roots{{Function: fixture.root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			background, classified, backgroundErr := fixture.universe.FunctionBackground(fn)
			if backgroundErr != nil {
				return coro.SSAFunctionPolicy{}, backgroundErr
			}
			if classified && background == llssa.InC {
				return coro.SSAFunctionPolicy{
					IgnoreBody: true, External: coro.ExternalUnknownForeign, OverrideExternal: true,
					Exec: coro.BlockForeign | coro.IRQUnsafe, ForeignWorkerCertificate: "forged-worker-certificate",
				}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callee := call.Common().StaticCallee()
			return callee != nil && callee.Pkg != nil && callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, recognized, err := validateCoroWorkerForeignCall(
		forged, fixture.universe, fixture.call, fixture.prog.PointerSize(),
	)
	if !recognized || err == nil || !strings.Contains(err.Error(), "identity differs") {
		t.Fatalf("forged worker call validation = recognized:%t err:%v", recognized, err)
	}
}

func TestCoroWorkerGenericCallableRejectsPlanUniverseCertificateMismatch(t *testing.T) {
	fixture := prepareCoroWorkerForeignFixture(t, coroWorkerGenericForeignTestSource, "Root")
	defer fixture.prog.Dispose()
	target, frozen := fixture.universe.Resolve(fixture.call.Common().StaticCallee())
	if !frozen || target == nil {
		t.Fatal("generic callable target is absent from the frozen universe")
	}
	frontend, certified, err := fixture.universe.CoroCallableContractCertificate(target)
	if err != nil || !certified {
		t.Fatalf("frontend callable certificate = %+v, %t, %v", frontend, certified, err)
	}
	forgedCertificate := frontend
	forgedCertificate.CanonicalFunctionIdentity += "#forged-plan"
	if err := forgedCertificate.Validate(); err != nil {
		t.Fatalf("test forged callable certificate is structurally invalid: %v", err)
	}

	ssaUniverse, err := coro.NewSSAEmissionUniverse(fixture.ssaPkg.Prog, fixture.universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := fixture.universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	forgedPlan, err := coro.AnalyzeSSA(fixture.ssaPkg.Prog, coro.Roots{{Function: fixture.root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			background, classified, backgroundErr := fixture.universe.FunctionBackground(fn)
			if backgroundErr != nil {
				return coro.SSAFunctionPolicy{}, backgroundErr
			}
			if classified && background == llssa.InC {
				certificate, present, certificateErr := fixture.universe.CoroCallableContractCertificate(fn)
				if certificateErr != nil {
					return coro.SSAFunctionPolicy{}, certificateErr
				}
				if present {
					if resolved, ok := fixture.universe.Resolve(fn); ok && resolved == target {
						certificate = forgedCertificate
					}
					return coro.SSAFunctionPolicy{
						IgnoreBody: true, External: coro.ExternalUnknownForeign, OverrideExternal: true,
						Exec: coro.BlockForeign | coro.IRQUnsafe, CallableContractCertificate: certificate,
					}, nil
				}
				return coro.SSAFunctionPolicy{
					IgnoreBody: true, External: coro.ExternalUnknownForeign, OverrideExternal: true,
					Exec: coro.BlockForeign | coro.IRQUnsafe,
				}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callee := call.Common().StaticCallee()
			return callee != nil && callee.Pkg != nil && callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (coroStaticForeignCallAuthority{
		plan: forgedPlan, universe: fixture.universe,
	}).authorize(target)
	if err == nil || !strings.Contains(err.Error(), "certificate differs") {
		t.Fatalf("forged generic callable authorization error = %v; want complete certificate mismatch", err)
	}
}

func TestCoroWorkerForeignWordShapeIsTargetWidthExact(t *testing.T) {
	if !coroWorkerWordType(types.Typ[types.Int32], 4) ||
		!coroWorkerWordType(types.NewPointer(types.Typ[types.Byte]), 4) ||
		!coroWorkerWordType(types.Typ[types.UnsafePointer], 4) {
		t.Fatal("32-bit integer/pointer worker words were rejected")
	}
	for _, typ := range []types.Type{
		types.Typ[types.Int64], types.Typ[types.Float32], types.NewStruct(nil, nil), types.NewSlice(types.Typ[types.Byte]),
	} {
		if coroWorkerWordType(typ, 4) {
			t.Errorf("32-bit worker accepted non-word type %s", typ)
		}
	}
	for _, typ := range []types.Type{
		types.NewPointer(types.Typ[types.Byte]), types.Typ[types.UnsafePointer], types.Typ[types.Int64],
	} {
		if coroWorkerResultWordType(typ, 4) {
			t.Errorf("32-bit worker accepted unsafe result word type %s", typ)
		}
	}
	for _, typ := range []types.Type{types.Typ[types.Int8], types.Typ[types.Uint32], types.Typ[types.Uintptr]} {
		if !coroWorkerResultWordType(typ, 4) {
			t.Errorf("32-bit worker rejected integer result word type %s", typ)
		}
	}
}
