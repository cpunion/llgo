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
	ParsePkgSyntax(prog, ssaPkg.Pkg, files)
	universe, err := PrepareEmissionUniverseWithOptions(
		prog,
		nil,
		[]EmissionPackage{{SSA: ssaPkg, Files: files}},
		EmissionUniverseOptions{EnableCoroWorker: true},
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
			if !ok || call.Common().StaticCallee() == nil {
				continue
			}
			background, classified, backgroundErr := universe.FunctionBackground(call.Common().StaticCallee())
			if backgroundErr == nil && classified && background == llssa.InC {
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
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapWorkerABIV0
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

func TestCoroWorkerClosedForeignCallUsesTypedThunk(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	fixture := prepareCoroWorkerForeignFixture(t, coroWorkerGenericForeignTestSource, "Root")
	defer fixture.prog.Dispose()
	target := fixture.call.Common().StaticCallee()
	planCertificate, planCertified := fixture.plan.CallableContractCertificate(target)
	universeCertificate, universeCertified, certificateErr := fixture.universe.CoroCallableContractCertificate(target)
	if certificateErr != nil || !planCertified || !universeCertified || planCertificate != universeCertificate {
		t.Fatalf("generic worker callable certificates = plan:%+v/%t universe:%+v/%t err:%v", planCertificate, planCertified, universeCertificate, universeCertified, certificateErr)
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
		CoroPlan:                      fixture.plan,
		EmissionUniverse:              fixture.universe,
		EnableCoroEntryResolution:     true,
		EnableCoroPhysicalABI:         true,
		EnableCoroChildAwait:          true,
		EnableCoroProgramBootstrapRun: true,
		EnableCoroWorker:              true,
		CoroABI:                       coro.PhysicalABIV1,
		SchedulerABI:                  coro.SchedulerProgramBootstrapWorkerABIV0,
		PanicABI:                      coro.PanicLegacyABIV0,
		FuncRepABI:                    coro.FuncRepABIV0,
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
	if !regexp.MustCompile(`trunc i64 [^\n]+ to i32`).MatchString(body) {
		t.Fatalf("Root does not unpack the signed 32-bit result from its worker word:\n%s", body)
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
		`sext i32 [^\n]+ to i64`,
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

func TestCoroWorkerForeignCallShapeRejectsUnsafeABIs(t *testing.T) {
	tests := []struct {
		name        string
		declaration string
		statement   string
		want        string
	}{
		{"float argument", "func foreign(float64) uintptr", "_ = foreign(1)", "argument 0 type float64 is not losslessly word-packable"},
		{"aggregate argument", "func foreign(struct{ X uintptr }) uintptr", "_ = foreign(struct{ X uintptr }{})", "argument 0 type struct"},
		{"float result", "func foreign(uintptr) float64", "_ = foreign(1)", "result type float64 is not losslessly word-packable"},
		{"pointer result", "func foreign(uintptr) *byte", "_ = foreign(1)", "result type *byte is not losslessly word-packable integer data"},
		{"multiple results", "func foreign(uintptr) (uintptr, uintptr)", "_, _ = foreign(1)", "requires zero or one result"},
		{"variadic", "func foreign(...uintptr) uintptr", "_ = foreign(1)", "receiver-free, non-variadic"},
		{"too many arguments", "func foreign(uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr) uintptr", "_ = foreign(0, 0, 0, 0, 0, 0, 0, 0, 0, 0)", "zero to 9 arguments"},
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
		{"caller affinity", "may-block", "caller-thread", "none", "by-value", "callable affinity"},
		{"owner affinity", "may-block", "owner-thread", "none", "by-value", "callable affinity"},
		{"host affinity", "may-block", "host-main", "none", "by-value", "callable affinity"},
		{"unknown reentry", "may-block", "any-thread", "unknown", "by-value", "callable reentry"},
		{"managed callback", "may-block", "any-thread", "managed-callback", "by-value", "callable reentry"},
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
			err := validateCoroWorkerForeignAuthorization(fixture.plan, fixture.universe, target)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("generic callable worker authorization error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestCoroWorkerForeignCallRequiresFrozenCertificate(t *testing.T) {
	source := strings.Replace(coroWorkerForeignTestSource, "//llgo:coro worker\n", "", 1)
	fixture := prepareCoroWorkerForeignFixture(t, source, "Root")
	defer fixture.prog.Dispose()
	_, recognized, err := validateCoroWorkerForeignCall(
		fixture.plan, fixture.universe, fixture.call, fixture.prog.PointerSize(),
	)
	if !recognized || err == nil || !strings.Contains(err.Error(), "no exact worker-safe certificate") {
		t.Fatalf("uncertified worker call validation = recognized:%t err:%v", recognized, err)
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
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapWorkerABIV0
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
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapWorkerABIV0
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
	err = validateCoroWorkerForeignAuthorization(forgedPlan, fixture.universe, target)
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
