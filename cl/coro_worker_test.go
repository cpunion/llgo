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
	"go/importer"
	"go/token"
	"go/types"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroWorkerTestSource = `package foo

import _ "unsafe"

//go:linkname raw llgo.syscall
func raw(fn, a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr) (uintptr, uintptr, uintptr)

//go:linkname raw32 llgo.syscall32
func raw32(fn, a0 uintptr) (uintptr, uintptr, uintptr)

//go:linkname rawPointer llgo.syscallPtr
func rawPointer(fn, a0 uintptr) (uintptr, uintptr, uintptr)

//go:linkname funcPCABI0 llgo.funcPCABI0
func funcPCABI0(fn any) uintptr

//llgo:coro workeraddr 9
func libc_worker9_v1_trampoline()

//llgo:coro workeraddr 1
func libc_worker1_v1_trampoline()

func Root(a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr) (uintptr, uintptr, uintptr) {
	return raw(funcPCABI0(libc_worker9_v1_trampoline), a0, a1, a2, a3, a4, a5, a6, a7, a8)
}

func RootInt32(a0 uintptr) (uintptr, uintptr, uintptr) {
	return raw32(funcPCABI0(libc_worker1_v1_trampoline), a0)
}

func RootPointer(a0 uintptr) (uintptr, uintptr, uintptr) {
	return rawPointer(funcPCABI0(libc_worker1_v1_trampoline), a0)
}
`

const coroWorkerTypedSyncSyscallTestSource = `package typed

import _ "unsafe"

//go:linkname raw32 llgo.syscall32
func raw32(fn, a0 uintptr, f0 float64) (uintptr, uintptr, uintptr)

func Root(fn, a0 uintptr, f0 float64) (uintptr, uintptr, uintptr) {
	return raw32(fn, a0, f0)
}
`

func TestCoroTypedSyncSyscallDoesNotForgeWordWorkerABI(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, coroWorkerTypedSyncSyscallTestSource)
	root := ssaPkg.Func("Root")
	if root == nil {
		t.Fatal("typed syscall fixture has no Root")
	}
	var rawCall *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common() != nil && call.Common().StaticCallee() == ssaPkg.Func("raw32") {
				rawCall = call
			}
		}
	}
	if rawCall == nil {
		t.Fatal("typed syscall fixture has no raw32 call")
	}
	if err := planCoroSynchronousSyscallShape(rawCall); err != nil {
		t.Fatalf("typed synchronous syscall rejected: %v", err)
	}
	if err := validateCoroWorkerSyscallIntrinsicCallSite(rawCall); err == nil ||
		!strings.Contains(err.Error(), "argument 2 is not uintptr-shaped") {
		t.Fatalf("typed syscall worker validation = %v; want exact word-ABI rejection", err)
	}
}

func TestCoroWorkerSyscallCurrentFrame(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog, pkg, plan, root, rawCall := compileCoroWorkerFixture(t)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	rootPlan, ok := plan.FunctionPlan(root)
	if !ok || rootPlan.Emission != coro.EmitCoroutine || rootPlan.FuncRep != coro.DirectCoro ||
		rootPlan.Demand != coro.AsyncDemand || !rootPlan.Effect.Contains(coro.MayPark) ||
		!rootPlan.LocalEffect.Contains(coro.MayPark) {
		t.Fatalf("Root plan = %+v, present=%t; want one local may-park coroutine", rootPlan, ok)
	}
	if !plan.ElidesCall(rawCall) {
		t.Fatal("llgo.syscall declaration call is not frozen as a frontend-elided worker site")
	}
	if _, ok := plan.CallPlan(rawCall); ok {
		t.Fatal("llgo.syscall declaration unexpectedly retained a managed CallPlan")
	}

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify worker coroutine before CoroSplit: %v\n%s", err, module.String())
	}
	body := requireCoroPhysicalFunction(t, module, "foo.Root").String()
	assertCoroCancellationTerminalStatusPublication(t, requireCoroPhysicalFunction(t, module, "foo.Root"))
	if got := strings.Count(body, "call i8 @llvm.coro.suspend"); got != 3 {
		t.Fatalf("Root coro.suspend calls = %d, want initial + worker + final:\n%s", got, body)
	}
	for _, symbol := range []string{coroWorkerParkHookV1, coroWorkerResumeHookV1} {
		if got := strings.Count(body, "@"+symbol); got != 1 {
			t.Fatalf("Root references to %q = %d, want 1:\n%s", symbol, got, body)
		}
	}
	for _, symbol := range []string{coroOSThreadLockedHookV1, coroOSThreadForeignCallHookV1} {
		if got := strings.Count(body, "@"+symbol); got != 1 {
			t.Fatalf("Root references to locked-thread branch %q = %d, want 1:\n%s", symbol, got, body)
		}
	}
	lockedQuery := strings.Index(body, "call i1 @"+coroOSThreadLockedHookV1)
	directCall := strings.Index(body, "call i32 @"+coroOSThreadForeignCallHookV1)
	workerPark := strings.Index(body, "call void @"+coroWorkerParkHookV1)
	if lockedQuery < 0 || directCall < lockedQuery || workerPark < lockedQuery {
		t.Fatalf("Root does not branch from the lock query to both direct and worker paths:\n%s", body)
	}
	for _, forbidden := range []string{"@foo.raw", "@llgo.syscall"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("worker lowering retained ordinary intrinsic call %q:\n%s", forbidden, body)
		}
	}
	dispatch := regexp.MustCompile(
		`(?s)call i32 @` + regexp.QuoteMeta(coroWorkerResumeHookV1) + `\([^\n]+\)\n\s+switch i32 [^\[]+\[(.*?)\]`,
	).FindStringSubmatch(body)
	if len(dispatch) != 2 {
		t.Fatalf("Root has no isolated worker resume switch:\n%s", body)
	}
	for _, status := range []uint64{
		coroWorkerResumeSuccessV1,
		coroWorkerResumeTaskAbortV1,
		coroWorkerResumeShutdownV1,
		coroWorkerResumeFaultMemoryV1,
		coroWorkerResumeFaultDivideV1,
	} {
		if !regexp.MustCompile(`(?m)^\s+i32 ` + strconv.FormatUint(status, 10) + `, label `).MatchString(dispatch[1]) {
			t.Fatalf("Root worker resume switch lacks status %d:\n%s", status, dispatch[0])
		}
	}
	park := strings.Index(body, "call void @"+coroWorkerParkHookV1)
	suspend := strings.Index(body[park:], "call i8 @llvm.coro.suspend")
	resume := strings.Index(body[park:], "call i32 @"+coroWorkerResumeHookV1)
	if park < 0 || suspend < 0 || resume < 0 || suspend >= resume {
		t.Fatalf("Root does not publish worker park before suspend and consume after resume:\n%s", body)
	}

	runCoroABITestPipeline(t, prog, module)
	resumeBody := module.NamedFunction("foo.Root$coro.resume")
	if resumeBody.IsNil() || !strings.Contains(resumeBody.String(), "call i32 @"+coroWorkerResumeHookV1) {
		t.Fatalf("CoroSplit lost worker resume dispatch:\n%s", module.String())
	}
	assertCoroCancellationTerminalStatusPublication(t, resumeBody)
	object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		t.Fatalf("emit post-CoroSplit worker object: %v\n%s", err, module.String())
	}
	defer object.Dispose()
	for _, symbol := range []string{
		coroWorkerParkHookV1,
		coroWorkerResumeHookV1,
		coroOSThreadLockedHookV1,
		coroOSThreadForeignCallHookV1,
	} {
		if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(symbol)) {
			t.Fatalf("post-CoroSplit object lost worker ABI symbol %q", symbol)
		}
	}
}

func TestCoroWorkerSyscallFailureConventionsShareLowering(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog, pkg, _, _, _ := compileCoroWorkerFixture(t)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	tests := []struct {
		name     string
		required []*regexp.Regexp
		forbid   []*regexp.Regexp
	}{
		{
			name: "foo.Root",
			required: []*regexp.Regexp{
				regexp.MustCompile(`icmp eq i64 [^,]+, -1`),
			},
			forbid: []*regexp.Regexp{
				regexp.MustCompile(`trunc i64 [^\n]+ to i32`),
			},
		},
		{
			name: "foo.RootInt32",
			required: []*regexp.Regexp{
				regexp.MustCompile(`trunc i64 [^\n]+ to i32`),
				regexp.MustCompile(`icmp eq i32 [^,]+, -1`),
			},
			forbid: []*regexp.Regexp{
				regexp.MustCompile(`icmp eq i64 [^,]+, -1`),
			},
		},
		{
			name: "foo.RootPointer",
			required: []*regexp.Regexp{
				regexp.MustCompile(`icmp eq i64 [^,]+, 0`),
			},
			forbid: []*regexp.Regexp{
				regexp.MustCompile(`trunc i64 [^\n]+ to i32`),
				regexp.MustCompile(`icmp eq i64 [^,]+, -1`),
			},
		},
	}
	for _, test := range tests {
		text := requireCoroPhysicalFunction(t, module, test.name).String()
		for _, required := range test.required {
			if !required.MatchString(text) {
				t.Errorf("%s lacks failure predicate %s:\n%s", test.name, required, text)
			}
		}
		for _, forbidden := range test.forbid {
			if forbidden.MatchString(text) {
				t.Errorf("%s contains wrong failure predicate %s:\n%s", test.name, forbidden, text)
			}
		}
		for _, symbol := range []string{coroWorkerParkHookV1, coroWorkerResumeHookV1} {
			if got := strings.Count(text, "@"+symbol); got != 1 {
				t.Errorf("%s %q calls = %d, want one shared park/resume lowering", test.name, symbol, got)
			}
		}
	}
}

func TestCoroWorkerSyscallFailureConventionIdentityIsFrozen(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, coroWorkerTestSource)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog,
		nil,
		[]EmissionPackage{{SSA: ssaPkg, Files: files}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}

	wantOpcode := map[string]int{
		"raw":        llgoSyscall,
		"raw32":      llgoSyscall32,
		"rawPointer": llgoSyscallPtr,
	}
	identities := make(map[coro.FunctionID]string, len(wantOpcode))
	for name, want := range wantOpcode {
		function := ssaPkg.Func(name)
		opcode, intrinsic, err := universe.coroIntrinsicOpcode(function)
		if err != nil || !intrinsic || opcode != want {
			t.Fatalf("%s opcode = %d, %t, %v; want %d, true, nil", name, opcode, intrinsic, err, want)
		}
		id, err := coro.StableFunctionID(function, universe.FunctionIDConfig())
		if err != nil {
			t.Fatal(err)
		}
		if previous, duplicate := identities[id]; duplicate {
			t.Fatalf("failure conventions %s and %s share FunctionID %q", previous, name, id)
		}
		identities[id] = name
	}
}

func TestCoroWorkerProductionLinuxDynamicRawSyscallFailsClosed(t *testing.T) {
	source, err := os.ReadFile("../runtime/internal/lib/syscall/syscall_linux_coro.go")
	if err != nil {
		t.Fatal(err)
	}
	const packageClause = "package syscall\n"
	if strings.Count(string(source), packageClause) != 1 {
		t.Fatal("production Linux syscall source has an unexpected package clause")
	}
	// Compile the production declarations and function bodies verbatim. Only
	// the package name changes so its stdlib syscall type import is not a
	// self-import in this isolated compiler test.
	fixtureSource := strings.Replace(string(source), packageClause, "package syscallfixture\n", 1)
	ssaPkg, _, files := buildGoSSAPkg(t, fixtureSource)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog,
		nil,
		[]EmissionPackage{{SSA: ssaPkg, Files: files}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}

	root := ssaPkg.Func("RawSyscall")
	if root == nil {
		t.Fatal("production Linux syscall fixture has no RawSyscall body")
	}
	var rawCall *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Call.StaticCallee() == nil {
				continue
			}
			opcode, intrinsic, opcodeErr := universe.coroIntrinsicOpcode(call.Call.StaticCallee())
			if opcodeErr != nil {
				t.Fatal(opcodeErr)
			}
			if intrinsic && isLLGoSyscallIntrinsic(opcode) {
				if rawCall != nil {
					t.Fatal("production RawSyscall has multiple direct llgo.syscall calls")
				}
				rawCall = call
			}
		}
	}
	if rawCall == nil || rawCall.Call.StaticCallee().Name() != "llgoLinuxSyscall4" {
		t.Fatal("production RawSyscall has no exact llgoLinuxSyscall4 call")
	}
	if len(rawCall.Call.Args) != 5 || len(root.Params) == 0 || rawCall.Call.Args[1] != root.Params[0] {
		t.Fatalf("production RawSyscall trap operand = %v; want exact dynamic trap parameter %v", rawCall.Call.Args, root.Params)
	}
	if certificate, certified, certificateErr := universe.CoroWorkerSyscallCertificate(rawCall); certificateErr != nil || certified || certificate.ID != "" {
		t.Fatalf("dynamic RawSyscall worker certificate = %+v, %t, %v; want absent", certificate, certified, certificateErr)
	}
	semantics, intrinsic, semanticsErr := universe.CoroIntrinsicCallSiteSemantics(rawCall)
	if semanticsErr != nil || !intrinsic || semantics != CoroIntrinsicCallUnsupported {
		t.Fatalf("dynamic RawSyscall intrinsic semantics = %v, %t, %v; want unsupported, true, nil", semantics, intrinsic, semanticsErr)
	}
	site, frozen, siteErr := universe.CoroCallSitePlan(rawCall)
	if siteErr != nil || !frozen || !site.Intrinsic || !site.RawPlainSynchronousIntrinsic ||
		site.IntrinsicSemantics != CoroIntrinsicCallUnsupported || site.ElidesCall() {
		t.Fatalf("dynamic RawSyscall SitePlan = %+v, %t, %v; want retained managed edge plus exact raw/plain synchronous recipe", site, frozen, siteErr)
	}

	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
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
			if fn == root {
				return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callee := call.Common().StaticCallee()
			if callee != nil && callee.Pkg != nil && callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init" {
				return true, nil
			}
			callSemantics, callIntrinsic, callErr := universe.CoroIntrinsicCallSiteSemantics(call)
			return callIntrinsic && callSemantics.ElidesManagedCall(), callErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ElidesCall(rawCall) {
		t.Fatal("dynamic RawSyscall was elided without an exact constant-trap certificate")
	}
	if _, retained := plan.CallPlan(rawCall); !retained {
		t.Fatal("dynamic RawSyscall lost its fail-closed retained intrinsic edge")
	}
	if err := validateCoroWorkerSyscallCall(plan, universe, rawCall); err == nil ||
		!strings.Contains(err.Error(), "no frozen static target capability") {
		t.Fatalf("dynamic/fork/exec/exit trap worker validation = %v; want missing capability", err)
	}
	rootPlan, planned := plan.FunctionPlan(root)
	if !planned || rootPlan.Emission != coro.EmitCoroutine {
		t.Fatalf("production RawSyscall plan = %+v, present=%t; want managed coroutine preflight subject", rootPlan, planned)
	}
	if err := validateCoroPhysicalABIWithUniverse(root, rootPlan, plan, universe, true, true); err == nil {
		t.Fatal("physical coroutine preflight accepted dynamic RawSyscall without a constant-trap certificate")
	}
}

type compiledCoroWorkerFixture struct {
	prog  llssa.Program
	pkg   llssa.Package
	plan  *coro.SSAPlan
	roots map[string]*ssa.Function
	calls map[string]*ssa.Call
}

func compileCoroWorkerFixture(t *testing.T) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.Call,
) {
	t.Helper()
	compiled := compileCoroWorkerSourceFixture(
		t, coroWorkerTestSource, []string{"Root", "RootInt32", "RootPointer"},
	)
	return compiled.prog, compiled.pkg, compiled.plan, compiled.roots["Root"], compiled.calls["Root"]
}

func compileCoroWorkerSourceFixture(
	t *testing.T,
	source string,
	rootNames []string,
) compiledCoroWorkerFixture {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	// The host Go runtime imported by isolated cl tests does not contain LLGo's
	// private runtime type. Production builds load the exact target runtime;
	// this opaque test-only named array is sufficient because the hooks remain
	// unresolved and the test observes only frame storage and ABI calls.
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
	rootsByName := make(map[string]*ssa.Function, len(rootNames))
	callsByName := make(map[string]*ssa.Call, len(rootNames))
	analysisRoots := make(coro.Roots, 0, len(rootNames))
	rootSet := make(map[*ssa.Function]bool, len(rootNames))
	for _, name := range rootNames {
		root := ssaPkg.Func(name)
		if root == nil {
			prog.Dispose()
			t.Fatalf("worker fixture lacks root %q", name)
		}
		rootsByName[name] = root
		rootSet[root] = true
		analysisRoots = append(analysisRoots, coro.Root{Function: root, Demand: coro.AsyncDemand})
		for _, block := range root.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(*ssa.Call)
				if !ok || call.Call.StaticCallee() == nil {
					continue
				}
				opcode, intrinsic, intrinsicErr := universe.coroIntrinsicOpcode(call.Call.StaticCallee())
				if intrinsicErr != nil {
					prog.Dispose()
					t.Fatal(intrinsicErr)
				}
				if intrinsic && isLLGoSyscallIntrinsic(opcode) {
					if callsByName[name] != nil {
						prog.Dispose()
						t.Fatalf("worker fixture root %q has multiple direct llgo.syscall calls", name)
					}
					callsByName[name] = call
				}
			}
		}
		if callsByName[name] == nil {
			prog.Dispose()
			t.Fatalf("worker fixture root %q has no direct llgo.syscall call", name)
		}
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, analysisRoots, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if rootSet[fn] {
				// Production builds seed this fact through
				// CoroPlanInput.intrinsicCallSemantics. This isolated cl fixture
				// has no build-driver wrapper, so freeze the same owner effect.
				return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callee := call.Common().StaticCallee()
			if callee != nil && callee.Pkg != nil && callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init" {
				return true, nil
			}
			semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
			return intrinsic && semantics.ElidesManagedCall(), err
		},
		ClassifyElidedCallCertificate: func(_ *ssa.Function, call ssa.CallInstruction) (string, error) {
			certificate, ok, err := universe.CoroWorkerSyscallCertificate(call)
			if err != nil || !ok {
				return "", err
			}
			return certificate.ID, nil
		},
		ClassifyStaticCodeAddressCallArgument: func(_ *ssa.Function, call ssa.CallInstruction, argument int) (bool, error) {
			return universe.CoroStaticCodeAddressCallArgument(call, argument)
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{
		CoroPlan:         plan,
		EmissionUniverse: universe,

		CoroABI:      coro.PhysicalABIV1,
		SchedulerABI: coro.SchedulerProgramBootstrapChannelWorkerClosedStaticSpawnABIV0,
		PanicABI:     coro.PanicExplicitStatusABIV0,
		FuncRepABI:   coro.FuncRepABIV1, CoroTargetCapabilities: CoroNativeTargetCapabilities(),
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return compiledCoroWorkerFixture{
		prog: prog, pkg: pkg, plan: plan, roots: rootsByName, calls: callsByName,
	}
}
