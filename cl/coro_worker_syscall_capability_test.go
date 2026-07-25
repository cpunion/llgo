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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/typepatch"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroWorkerSyscallCapabilityFixture = `package workerword

//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(fn any) uintptr

//llgo:link raw llgo.syscall
func raw(fn, a0 uintptr) (uintptr, uintptr, uintptr)

//llgo:link raw6 llgo.syscall
func raw6(fn, a0, a1, a2, a3, a4, a5 uintptr) (uintptr, uintptr, uintptr)

//llgo:coro workeraddr 1
func libc_fixed_worker_v1_trampoline()

//llgo:coro workeraddr 6
func libc_fixed_six_worker_v1_trampoline()

//llgo:coro workeraddr 0
func libc_wrong_arity_worker_v1_trampoline()

func libc_ordinary_trampoline()

func Fixed(a0 uintptr) uintptr {
	r1, _, _ := raw(funcPCABI0(libc_fixed_worker_v1_trampoline), a0)
	return r1
}

func FixedSix(a0, a1, a2, a3, a4, a5 uintptr) uintptr {
	r1, _, _ := raw6(funcPCABI0(libc_fixed_six_worker_v1_trampoline), a0, a1, a2, a3, a4, a5)
	return r1
}

func privateCarrier(fn, a0 uintptr) uintptr {
	r1, _, _ := raw(fn, a0)
	return r1
}

func ThroughPrivateCarrier(a0 uintptr) uintptr {
	return privateCarrier(funcPCABI0(libc_fixed_worker_v1_trampoline), a0)
}

func privateMixedCarrier(fn, a0 uintptr) uintptr {
	r1, _, _ := raw(fn, a0)
	return r1
}

func ThroughMixedCarrierOK(a0 uintptr) uintptr {
	return privateMixedCarrier(funcPCABI0(libc_fixed_worker_v1_trampoline), a0)
}

func ThroughMixedCarrierWrong(a0 uintptr) uintptr {
	return privateMixedCarrier(funcPCABI0(libc_wrong_arity_worker_v1_trampoline), a0)
}

func Arbitrary(fn, a0 uintptr) uintptr {
	r1, _, _ := raw(fn, a0)
	return r1
}

func Uncertified(a0 uintptr) uintptr {
	r1, _, _ := raw(funcPCABI0(libc_ordinary_trampoline), a0)
	return r1
}

func ExportedCarrier(fn, a0 uintptr) uintptr {
	r1, _, _ := raw(fn, a0)
	return r1
}

func ThroughExportedCarrier(a0 uintptr) uintptr {
	return ExportedCarrier(funcPCABI0(libc_fixed_worker_v1_trampoline), a0)
}

var escapedCarrier = privateEscapedCarrier

func privateEscapedCarrier(fn, a0 uintptr) uintptr {
	r1, _, _ := raw(fn, a0)
	return r1
}

func ThroughEscapedCarrier(a0 uintptr) uintptr {
	return escapedCarrier(funcPCABI0(libc_fixed_worker_v1_trampoline), a0)
}

func Arithmetic(a0 uintptr) uintptr {
	fn := funcPCABI0(libc_fixed_worker_v1_trampoline) + 0
	r1, _, _ := raw(fn, a0)
	return r1
}

func Incompatible(a0 uintptr) uintptr {
	r1, _, _ := raw(funcPCABI0(libc_wrong_arity_worker_v1_trampoline), a0)
	return r1
}
`

func TestCoroWorkerSyscallFunctionWordCapabilityIsFailClosed(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/workerword", coroWorkerSyscallCapabilityFixture)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}

	wantCertified := map[string]bool{
		"Fixed":               true,
		"FixedSix":            true,
		"privateCarrier":      true,
		"privateMixedCarrier": true,
	}
	for _, name := range []string{
		"Fixed", "FixedSix", "privateCarrier", "Arbitrary", "Uncertified", "ExportedCarrier",
		"privateEscapedCarrier", "privateMixedCarrier", "Arithmetic", "Incompatible",
	} {
		call := exactWorkerSyscallCall(t, universe, pkg.ssa.Func(name))
		certificate, certified, err := universe.CoroWorkerSyscallCertificate(call)
		if err != nil {
			t.Fatalf("%s certificate: %v", name, err)
		}
		if certified != wantCertified[name] {
			t.Fatalf("%s certified = %t, certificate=%+v; want %t", name, certified, certificate, wantCertified[name])
		}
		semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
		if err != nil || !intrinsic {
			t.Fatalf("%s intrinsic semantics = %v, %t, %v", name, semantics, intrinsic, err)
		}
		wantSemantics := CoroIntrinsicCallUnsupported
		if wantCertified[name] {
			wantSemantics = CoroIntrinsicCallInlineSuspend
			if certificate.ID == "" || certificate.WorkerABISignature == "" ||
				certificate.PhysicalTargetSetID == "" || certificate.CallableShadowSetID == "" ||
				certificate.StaticTargetCount != 1 {
				t.Fatalf("%s incomplete exact certificate: %+v", name, certificate)
			}
		}
		if semantics != wantSemantics {
			t.Fatalf("%s semantics = %v; want %v", name, semantics, wantSemantics)
		}
	}
}

func TestCoroWorkerSyscallConditionalIncomingPlanNarrowing(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/workerwordconditional", coroWorkerSyscallCapabilityFixture)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	carrier := pkg.ssa.Func("privateMixedCarrier")
	call := exactWorkerSyscallCall(t, universe, carrier)
	certificate, certified, err := universe.CoroWorkerSyscallCertificate(call)
	if err != nil || !certified || certificate.ID == "" {
		t.Fatalf("conditional carrier certificate = %+v, %t, %v", certificate, certified, err)
	}
	incoming := frozenCoroWorkerIncomingForTest(t, universe, call)
	if len(incoming) != 2 || !incoming[0].certified && !incoming[1].certified || incoming[0].certified && incoming[1].certified {
		t.Fatalf("conditional incoming inventory = %+v; want one certified and one fail-closed edge", incoming)
	}

	analyze := func(roots coro.Roots) *coro.SSAPlan {
		ssaUniverse, err := coro.NewSSAEmissionUniverse(pkg.ssa.Prog, universe.Functions())
		if err != nil {
			t.Fatal(err)
		}
		plan, err := coro.AnalyzeSSA(pkg.ssa.Prog, roots, coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          universe.FunctionIDConfig(),
			MaxPlainInstructions: -1,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == carrier {
					return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
			ClassifyElidedCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (bool, error) {
				semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(candidate)
				return intrinsic && semantics.ElidesManagedCall(), err
			},
			ClassifyElidedCallCertificate: func(_ *ssa.Function, candidate ssa.CallInstruction) (string, error) {
				certificate, certified, err := universe.CoroWorkerSyscallCertificate(candidate)
				if err != nil || !certified {
					return "", err
				}
				return certificate.ID, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	safe := analyze(coro.Roots{{Function: pkg.ssa.Func("ThroughMixedCarrierOK"), Demand: coro.AsyncDemand}})
	if err := validateCoroWorkerSyscallCall(safe, universe, call); err != nil {
		t.Fatalf("safe-only conditional plan rejected: %v", err)
	}
	rawCaller := pkg.ssa.Func("ThroughMixedCarrierWrong")
	rawOnly := analyze(coro.Roots{{Function: rawCaller, RawPlainDemand: true}})
	rawCallerPlan, _ := rawOnly.FunctionPlan(rawCaller)
	carrierPlan, _ := rawOnly.FunctionPlan(carrier)
	if !coroWorkerHasExactRawPlainOnly(rawOnly, rawCaller, rawCallerPlan) ||
		!coroWorkerHasExactRawPlainOnly(rawOnly, carrier, carrierPlan) {
		t.Fatalf("raw-only conditional path caller=%+v carrier=%+v", rawCallerPlan, carrierPlan)
	}
	if err := validateCoroWorkerSyscallCall(rawOnly, universe, call); err != nil {
		t.Fatalf("raw-only uncertified incoming path rejected: %v", err)
	}
	mixed := analyze(coro.Roots{
		{Function: pkg.ssa.Func("ThroughMixedCarrierOK"), Demand: coro.AsyncDemand},
		{Function: pkg.ssa.Func("ThroughMixedCarrierOK"), RawPlainDemand: true},
	})
	mixedCarrierPlan, _ := mixed.FunctionPlan(carrier)
	if mixedCarrierPlan.ManagedDemand == coro.NoDemand || !mixedCarrierPlan.RawPlainDemand ||
		!mixed.HasRawPlainVariant(carrier) {
		t.Fatalf("mixed carrier lost its raw-plain variant: %+v", mixedCarrierPlan)
	}
	if err := validateCoroWorkerSyscallCall(mixed, universe, call); err != nil {
		t.Fatalf("mixed managed/raw conditional plan rejected: %v", err)
	}
	unsafe := analyze(coro.Roots{
		{Function: pkg.ssa.Func("ThroughMixedCarrierOK"), Demand: coro.AsyncDemand},
		{Function: pkg.ssa.Func("ThroughMixedCarrierWrong"), Demand: coro.AsyncDemand},
	})
	if err := validateCoroWorkerSyscallCall(unsafe, universe, call); err == nil || !strings.Contains(err.Error(), "active static incoming edge") {
		t.Fatalf("active incompatible target did not fail closed: %v", err)
	}
}

func TestCoroLinuxSyscallTrapPolicyNarrowsActiveConstantCallers(t *testing.T) {
	testProg := newEmissionTestProgram()
testProg.addPackage(t, "syscall", `package syscall
type Errno uintptr
const (
	SYS_WRITE      = 1
	SYS_EXIT       = 60
	SYS_CAPGET     = 125
	SYS_GETGROUPS  = 115
	SYS_EXIT_GROUP = 231
)
`)
	const packagePath = "example.com/emission/linuxtrap"
	pkg := testProg.addPackage(t, packagePath, `package linuxtrap
import stdsyscall "syscall"

//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(fn any) uintptr

//llgo:link raw llgo.syscall
func raw(fn, trap, a1, a2, a3 uintptr) (uintptr, uintptr, uintptr)

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/4
func libc___llgo_linux_syscall3_v1_trampoline()

func carrier(trap, a1, a2, a3 uintptr) uintptr {
	r1, _, _ := raw(funcPCABI0(libc___llgo_linux_syscall3_v1_trampoline), trap, a1, a2, a3)
	return r1
}

func Safe() uintptr {
	return carrier(stdsyscall.SYS_WRITE, 1, 2, 3)
}

func CredentialCapabilityQuery() uintptr {
	return carrier(stdsyscall.SYS_CAPGET, 0, 0, 0)
}

func CredentialGroupsQuery() uintptr {
	return carrier(stdsyscall.SYS_GETGROUPS, 0, 0, 0)
}

func ProcessControl() uintptr {
	return carrier(stdsyscall.SYS_EXIT, 0, 0, 0)
}

func ProcessGroupExit() uintptr {
	return carrier(stdsyscall.SYS_EXIT_GROUP, 0, 0, 0)
}

func Dynamic(trap uintptr) uintptr {
	return carrier(trap, 0, 0, 0)
}
`)
	testProg.ssa.Build()
	prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "linux", GOARCH: "amd64"})
	defer prog.Dispose()
	prog.SetLinkname(packagePath+".libc___llgo_linux_syscall3_v1_trampoline", "C.__llgo_linux_syscall3_v1")
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}

	carrier := pkg.ssa.Func("carrier")
	call := exactWorkerSyscallCall(t, universe, carrier)
	certificate, certified, err := universe.CoroWorkerSyscallCertificate(call)
	if err != nil || !certified || certificate.ID == "" || certificate.StaticTargetCount != 1 {
		t.Fatalf("Linux constant-trap certificate = %+v, %t, %v", certificate, certified, err)
	}
	incoming := frozenCoroWorkerIncomingForTest(t, universe, call)
	status := make(map[string]bool, len(incoming))
	for _, edge := range incoming {
		if edge.call != nil && edge.call.Parent() != nil && edge.trapPolicyIdentity != "" {
			status[edge.call.Parent().Name()] = edge.certified
		}
	}
	if len(status) != 6 || !status["Safe"] ||
		!status["CredentialCapabilityQuery"] || !status["CredentialGroupsQuery"] ||
		status["ProcessControl"] || status["ProcessGroupExit"] || status["Dynamic"] {
		t.Fatalf("Linux constant-trap incoming inventory = %+v; want I/O and read-only credential queries certified", incoming)
	}

	analyze := func(root coro.Root) *coro.SSAPlan {
		ssaUniverse, err := coro.NewSSAEmissionUniverse(pkg.ssa.Prog, universe.Functions())
		if err != nil {
			t.Fatal(err)
		}
		plan, err := coro.AnalyzeSSA(pkg.ssa.Prog, coro.Roots{root}, coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          universe.FunctionIDConfig(),
			MaxPlainInstructions: -1,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == carrier {
					return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
			ClassifyElidedCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (bool, error) {
				semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(candidate)
				return intrinsic && semantics.ElidesManagedCall(), err
			},
			ClassifyElidedCallCertificate: func(_ *ssa.Function, candidate ssa.CallInstruction) (string, error) {
				certificate, certified, err := universe.CoroWorkerSyscallCertificate(candidate)
				if err != nil || !certified {
					return "", err
				}
				return certificate.ID, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	for _, name := range []string{"Safe", "CredentialCapabilityQuery", "CredentialGroupsQuery"} {
		safe := analyze(coro.Root{Function: pkg.ssa.Func(name), Demand: coro.AsyncDemand})
		if err := validateCoroWorkerSyscallCall(safe, universe, call); err != nil {
			t.Fatalf("%s safe Linux constant trap rejected: %v", name, err)
		}
	}
	for _, name := range []string{"ProcessControl", "ProcessGroupExit", "Dynamic"} {
		unsafe := analyze(coro.Root{Function: pkg.ssa.Func(name), Demand: coro.AsyncDemand})
		if err := validateCoroWorkerSyscallCall(unsafe, universe, call); err == nil ||
			(!strings.Contains(err.Error(), "active static incoming edge") &&
				!strings.Contains(err.Error(), "externally established entry")) {
			t.Fatalf("%s Linux trap did not fail closed: %v", name, err)
		}
	}
	raw := analyze(coro.Root{Function: pkg.ssa.Func("Dynamic"), RawPlainDemand: true})
	if err := validateCoroWorkerSyscallCall(raw, universe, call); err != nil {
		t.Fatalf("raw/plain dynamic Linux trap rejected: %v", err)
	}
}

func TestCoroWorkerAddressPatchAliasesUpstreamFuncPCABI0Target(t *testing.T) {
	testProg := newEmissionTestProgram()
	const originalPath = "example.com/emission/darwinsyscall"
	const alternatePath = "example.com/emission/darwinsyscall_alt"
	original := testProg.addPackage(t, originalPath, `package syscall
//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(any) uintptr
//llgo:link llgoSyscall3Int32 llgo.syscall32
func llgoSyscall3Int32(fn, a1, a2, a3 uintptr) (uintptr, uintptr, uintptr)
func libc_getrlimit_trampoline()
func libc_fork_trampoline()
func rawSyscall(fn, a1, a2, a3 uintptr) uintptr {
	r1, _, _ := llgoSyscall3Int32(fn, a1, a2, a3)
	return r1
}
func Getrlimit(a1, a2 uintptr) uintptr {
	return rawSyscall(funcPCABI0(libc_getrlimit_trampoline), a1, a2, 0)
}
func Fork() uintptr {
	return rawSyscall(funcPCABI0(libc_fork_trampoline), 0, 0, 0)
}
`)
	alternate := testProg.addPackage(t, alternatePath, `package syscall
//llgo:coro workeraddr 3
func libc_getrlimit_trampoline()
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	prog.SetLinkname(alternatePath+".libc_getrlimit_trampoline", "C.getrlimit")
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog,
		Patches{originalPath: {Alt: alternate.ssa, Types: typepatch.Clone(alternate.types)}},
		[]EmissionPackage{{SSA: original.ssa, Files: []*ast.File{original.file}}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	originalTarget := original.ssa.Func("libc_getrlimit_trampoline")
	alternateTarget := alternate.ssa.Func("libc_getrlimit_trampoline")
	if resolved, ok := universe.Resolve(originalTarget); !ok || resolved != alternateTarget {
		t.Fatalf("patched getrlimit target resolved to %v, %t; want exact alternate %v", resolved, ok, alternateTarget)
	}
	getrlimitPC := exactIntrinsicOpcodeCall(t, universe, original.ssa.Func("Getrlimit"), llgoFuncPCABI0)
	if observed, err := universe.CoroStaticCodeAddressCallArgument(getrlimitPC, 0); err != nil || !observed {
		t.Fatalf("patched workeraddr FuncPCABI0 operand = %t, %v; want exact code-address-only publication", observed, err)
	}
	forkPC := exactIntrinsicOpcodeCall(t, universe, original.ssa.Func("Fork"), llgoFuncPCABI0)
	if observed, err := universe.CoroStaticCodeAddressCallArgument(forkPC, 0); err != nil || !observed {
		t.Fatalf("uncertified trampoline FuncPCABI0 operand = %t, %v; want exact address-only observation", observed, err)
	}
	call := exactWorkerSyscallCall(t, universe, original.ssa.Func("rawSyscall"))
	certificate, certified, err := universe.CoroWorkerSyscallCertificate(call)
	if err != nil || !certified || certificate.StaticTargetCount != 1 {
		t.Fatalf("patched getrlimit carrier certificate = %+v, %t, %v", certificate, certified, err)
	}
	incoming := frozenCoroWorkerIncomingForTest(t, universe, call)
	if len(incoming) != 2 || incoming[0].certified == incoming[1].certified {
		t.Fatalf("patched getrlimit/fork incoming inventory = %+v; want one certified target and one fail-closed target", incoming)
	}
}

func TestCoroWorkerCallablePatchCatalogAliasesDarwinStatTargetsByArchitecture(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		goarch        string
		threeTarget   string
		threePhysical string
		sixTarget     string
		sixPhysical   string
	}{
		{
			goarch:        "arm64",
			threeTarget:   "libc_stat_trampoline",
			threePhysical: "stat",
			sixTarget:     "libc_fstatat_trampoline",
			sixPhysical:   "fstatat",
		},
		{
			goarch:        "amd64",
			threeTarget:   "libc_stat64_trampoline",
			threePhysical: "stat64",
			sixTarget:     "libc_fstatat64_trampoline",
			sixPhysical:   "fstatat64",
		},
	} {
		test := test
		t.Run(test.goarch, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			originalPath := "example.com/emission/darwinstat/" + test.goarch
			alternatePath := originalPath + "_alt"
			originalSource := strings.NewReplacer(
				"THREE_TARGET", test.threeTarget,
				"SIX_TARGET", test.sixTarget,
			).Replace(`package syscall
//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(any) uintptr
//llgo:link llgoSyscall3 llgo.syscall32
func llgoSyscall3(fn, a1, a2, a3 uintptr) (uintptr, uintptr, uintptr)
//llgo:link llgoSyscall6 llgo.syscall32
func llgoSyscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (uintptr, uintptr, uintptr)
func THREE_TARGET()
func SIX_TARGET()
func libc_fork_trampoline()
func syscall(fn, a1, a2, a3 uintptr) uintptr {
	r1, _, _ := llgoSyscall3(fn, a1, a2, a3)
	return r1
}
func syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) uintptr {
	r1, _, _ := llgoSyscall6(fn, a1, a2, a3, a4, a5, a6)
	return r1
}
func Stat(a1, a2 uintptr) uintptr {
	return syscall(funcPCABI0(THREE_TARGET), a1, a2, 0)
}
func Fstatat(a1, a2, a3, a4 uintptr) uintptr {
	return syscall6(funcPCABI0(SIX_TARGET), a1, a2, a3, a4, 0, 0)
}
func Fork() uintptr {
	return syscall(funcPCABI0(libc_fork_trampoline), 0, 0, 0)
}
`)
			alternateSource := strings.NewReplacer(
				"THREE_TARGET", test.threeTarget,
				"SIX_TARGET", test.sixTarget,
			).Replace(`package syscall
//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
func THREE_TARGET()
//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/6
func SIX_TARGET()
`)
			original := testProg.addPackage(t, originalPath, originalSource)
			alternate := testProg.addPackage(t, alternatePath, alternateSource)
			testProg.ssa.Build()
			prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "darwin", GOARCH: test.goarch})
			defer prog.Dispose()
			prog.SetLinkname(alternatePath+"."+test.threeTarget, "C."+test.threePhysical)
			prog.SetLinkname(alternatePath+"."+test.sixTarget, "C."+test.sixPhysical)
			universe, err := prepareStacklessEmissionUniverseWithOptions(
				prog,
				Patches{originalPath: {Alt: alternate.ssa, Types: typepatch.Clone(alternate.types)}},
				[]EmissionPackage{{SSA: original.ssa, Files: []*ast.File{original.file}}},
				EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
			)
			if err != nil {
				t.Fatal(err)
			}

			for _, target := range []string{test.threeTarget, test.sixTarget} {
				if resolved, ok := universe.Resolve(original.ssa.Func(target)); !ok || resolved != alternate.ssa.Func(target) {
					t.Fatalf("patched %s resolved to %v, %t; want exact architecture declaration", target, resolved, ok)
				}
			}
			analysis, err := AnalyzeCoroCallableShadows(universe)
			if err != nil {
				t.Fatal(err)
			}
			for _, wrapper := range []struct {
				name     string
				target   string
				physical string
				arity    int
			}{
				{"Stat", test.threeTarget, test.threePhysical, 3},
				{"Fstatat", test.sixTarget, test.sixPhysical, 6},
			} {
				producer := exactIntrinsicOpcodeCall(t, universe, original.ssa.Func(wrapper.name), llgoFuncPCABI0)
				shadow, ok := analysis.Producer(producer)
				if !ok || shadow.Target != alternate.ssa.Func(wrapper.target) || shadow.PhysicalSymbol != wrapper.physical ||
					shadow.LegacyWorkerAddressCompat || shadow.ABI.WordArgs != wrapper.arity || shadow.ContractCertificateID == "" {
					reason, rejected := analysis.ProducerRejection(producer)
					t.Fatalf("%s shadow = %+v, %t; rejection=%q,%t", wrapper.name, shadow, ok, reason, rejected)
				}
			}
			forkProducer := exactIntrinsicOpcodeCall(t, universe, original.ssa.Func("Fork"), llgoFuncPCABI0)
			if reason, ok := analysis.ProducerRejection(forkProducer); !ok || reason != "target-lacks-workeraddr" {
				t.Fatalf("fork producer rejection = %q, %t; want target-lacks-workeraddr", reason, ok)
			}
			for _, carrier := range []string{"syscall", "syscall6"} {
				call := exactWorkerSyscallCall(t, universe, original.ssa.Func(carrier))
				certificate, certified, err := universe.CoroWorkerSyscallCertificate(call)
				if err != nil || !certified || certificate.StaticTargetCount != 1 {
					t.Fatalf("%s certificate = %+v, %t, %v", carrier, certificate, certified, err)
				}
			}
			incoming := frozenCoroWorkerIncomingForTest(t, universe, exactWorkerSyscallCall(t, universe, original.ssa.Func("syscall")))
			if len(incoming) != 2 || incoming[0].certified == incoming[1].certified {
				t.Fatalf("stat/fork incoming inventory = %+v; want one exact certificate and one fail-closed edge", incoming)
			}
		})
	}
}

func frozenCoroWorkerIncomingForTest(t *testing.T, universe *EmissionUniverse, call *ssa.Call) []coroWorkerSyscallIncomingEdge {
	t.Helper()
	if universe == nil || universe.coroProgramIR == nil {
		t.Fatal("worker incoming fixture has no ProgramIR")
	}
	frozen, found, err := universe.coroProgramIR.callSitePlan(call)
	if err != nil || !found {
		t.Fatalf("worker incoming SitePlan = found %t, error %v", found, err)
	}
	return frozen.workerIncoming
}

func TestCoroWorkerAddressOnlyDeclarationDoesNotCollideWithTypedPhysicalABI(t *testing.T) {
	testProg := newEmissionTestProgram()
	const syscallPath = "example.com/emission/addressonlysyscall"
	const alternatePath = syscallPath + "_alt"
	const runtimePath = "example.com/emission/addressonlyruntime"
	original := testProg.addPackage(t, syscallPath, `package syscall
//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(any) uintptr
//llgo:link raw llgo.syscall
func raw(fn, a0, a1, a2 uintptr) (uintptr, uintptr, uintptr)
func libc_write_trampoline()
func Write(a0, a1, a2 uintptr) uintptr {
	r1, _, _ := raw(funcPCABI0(libc_write_trampoline), a0, a1, a2)
	return r1
}
`)
	alternate := testProg.addPackage(t, alternatePath, `package syscall
//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
func libc_write_trampoline()
`)
	typed := testProg.addPackage(t, runtimePath, `package runtimelib
//llgo:coro worker
func c_write(fd int32, pointer uintptr, size uintptr) int
func Write(fd int32, pointer uintptr, size uintptr) int {
	return c_write(fd, pointer, size)
}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	prog.SetLinkname(alternatePath+".libc_write_trampoline", "C.write")
	prog.SetLinkname(runtimePath+".c_write", "C.write")

	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog,
		Patches{syscallPath: {Alt: alternate.ssa, Types: typepatch.Clone(alternate.types)}},
		[]EmissionPackage{
			{SSA: original.ssa, Files: []*ast.File{original.file}},
			{SSA: typed.ssa, Files: []*ast.File{typed.file}},
		},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}

	typedTarget := typed.ssa.Func("c_write")
	typedWorker, typedWorkerOK, err := universe.CoroForeignWorkerCertificate(typedTarget)
	if err != nil || !typedWorkerOK || typedWorker.ID == "" || typedWorker.PhysicalSymbol != "write" {
		t.Fatalf("typed C.write worker certificate = %+v, %t, %v", typedWorker, typedWorkerOK, err)
	}
	typedIdentity, typedIdentityOK, err := universe.CoroCallableIdentityCertificate(typedTarget)
	if err != nil || !typedIdentityOK || typedIdentity.PhysicalSymbol != "write" {
		t.Fatalf("typed C.write identity = %+v, %t, %v", typedIdentity, typedIdentityOK, err)
	}

	addressTarget := alternate.ssa.Func("libc_write_trampoline")
	addressContract, addressContractOK, err := universe.CoroCallableContractCertificate(addressTarget)
	if err != nil || !addressContractOK || addressContract.ID == "" ||
		addressContract.PhysicalSymbol != "write" || addressContract.CallableABI != "word-call.v1/3" ||
		!addressContract.CallableABIExplicit {
		t.Fatalf("address-only C.write contract = %+v, %t, %v", addressContract, addressContractOK, err)
	}
	addressIdentity, addressIdentityOK, err := universe.CoroCallableIdentityCertificate(addressTarget)
	if err != nil || !addressIdentityOK || addressIdentity.PhysicalSymbol != "write" {
		t.Fatalf("address-only C.write identity = %+v, %t, %v", addressIdentity, addressIdentityOK, err)
	}
	if typedIdentity.ID == addressIdentity.ID || typedIdentity.PhysicalABISignature == addressIdentity.PhysicalABISignature {
		t.Fatalf("typed/address-only identities collapsed: typed=%+v address=%+v", typedIdentity, addressIdentity)
	}
	if _, ordinaryWorker, err := universe.CoroForeignWorkerCertificate(addressTarget); err != nil || ordinaryWorker {
		t.Fatalf("address-only C.write ordinary worker capability = %t, %v; want absent", ordinaryWorker, err)
	}

	producer := exactIntrinsicOpcodeCall(t, universe, original.ssa.Func("Write"), llgoFuncPCABI0)
	analysis, err := AnalyzeCoroCallableShadows(universe)
	if err != nil {
		t.Fatal(err)
	}
	shadow, shadowOK := analysis.Producer(producer)
	if !shadowOK || shadow.Target != addressTarget || shadow.PhysicalSymbol != "write" ||
		shadow.ABI.WordArgs != 3 || shadow.ContractCertificateID != addressContract.ID {
		t.Fatalf("address-only C.write producer shadow = %+v, %t", shadow, shadowOK)
	}
	syscallCall := exactWorkerSyscallCall(t, universe, original.ssa.Func("Write"))
	workerCall, workerCallOK, err := universe.CoroWorkerSyscallCertificate(syscallCall)
	if err != nil || !workerCallOK || workerCall.ID == "" || workerCall.StaticTargetCount != 1 {
		t.Fatalf("address-only C.write worker syscall certificate = %+v, %t, %v", workerCall, workerCallOK, err)
	}
}

func TestCoroWorkerAddressPatchAcceptsPrivateFixedAdapter(t *testing.T) {
	testProg := newEmissionTestProgram()
	const originalPath = "example.com/emission/darwinraw"
	const alternatePath = "example.com/emission/darwinraw_alt"
	original := testProg.addPackage(t, originalPath, `package syscall
func Raw(a1 uintptr) uintptr
`)
	alternate := testProg.addPackage(t, alternatePath, `package syscall
//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(any) uintptr
//llgo:link llgoSyscall1 llgo.syscall
func llgoSyscall1(fn, a1 uintptr) (uintptr, uintptr, uintptr)
//llgo:coro workeraddr 1
func libc___llgo_private_v1_trampoline()
func Raw(a1 uintptr) uintptr {
	r1, _, _ := llgoSyscall1(funcPCABI0(libc___llgo_private_v1_trampoline), a1)
	return r1
}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	prog.SetLinkname(alternatePath+".funcPCABI0", "llgo.funcPCABI0")
	prog.SetLinkname(alternatePath+".llgoSyscall1", "llgo.syscall")
	prog.SetLinkname(alternatePath+".libc___llgo_private_v1_trampoline", "C.__llgo_private_v1")
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog,
		Patches{originalPath: {Alt: alternate.ssa, Types: typepatch.Clone(alternate.types)}},
		[]EmissionPackage{{SSA: original.ssa, Files: []*ast.File{original.file}}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	target := alternate.ssa.Func("libc___llgo_private_v1_trampoline")
	if canonical := universe.canonicalAlias(target); canonical != target {
		t.Fatalf("patch-private worker target canonical = %v; want exact alternate %v", canonical, target)
	}
	call := exactWorkerSyscallCall(t, universe, alternate.ssa.Func("Raw"))
	certificate, certified, err := universe.CoroWorkerSyscallCertificate(call)
	if err != nil || !certified || certificate.StaticTargetCount != 1 {
		t.Fatalf("patch-private worker certificate = %+v, %t, %v", certificate, certified, err)
	}
}

func TestCoroWorkerCarrierAcceptsManagedVisibilityLinknameOnly(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/workerlinkvisibility", `package workerlinkvisibility
//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(any) uintptr
//llgo:link raw llgo.syscall
func raw(fn, a1 uintptr) (uintptr, uintptr, uintptr)
//llgo:coro workeraddr 1
func libc_fixed_worker_v1_trampoline()
//go:linkname carrier
func carrier(fn, a1 uintptr) uintptr {
	r1, _, _ := raw(fn, a1)
	return r1
}
func Root(a1 uintptr) uintptr {
	return carrier(funcPCABI0(libc_fixed_worker_v1_trampoline), a1)
}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	carrier := pkg.ssa.Func("carrier")
	if _, visible, err := universe.coroGoLinknameVisibilityCertificate(carrier); err != nil || !visible {
		t.Fatalf("carrier visibility certificate = %t, %v", visible, err)
	}
	call := exactWorkerSyscallCall(t, universe, carrier)
	if certificate, certified, err := universe.CoroWorkerSyscallCertificate(call); err != nil || !certified || certificate.StaticTargetCount != 1 {
		t.Fatalf("visibility-only carrier certificate = %+v, %t, %v", certificate, certified, err)
	}
}

func TestCoroWorkerSyscallExactCertificateJoinsPlanAndRejectsForgery(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/workerwordplan", coroWorkerSyscallCapabilityFixture)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	root := pkg.ssa.Func("Fixed")
	call := exactWorkerSyscallCall(t, universe, root)
	frontend, ok, err := universe.CoroWorkerSyscallCertificate(call)
	if err != nil || !ok {
		t.Fatalf("frontend certificate = %+v, %t, %v", frontend, ok, err)
	}

	analyze := func(certificate string) *coro.SSAPlan {
		ssaUniverse, err := coro.NewSSAEmissionUniverse(pkg.ssa.Prog, universe.Functions())
		if err != nil {
			t.Fatal(err)
		}
		plan, err := coro.AnalyzeSSA(pkg.ssa.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          universe.FunctionIDConfig(),
			MaxPlainInstructions: -1,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == root {
					return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
			ClassifyElidedCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (bool, error) {
				semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(candidate)
				return intrinsic && semantics.ElidesManagedCall(), err
			},
			ClassifyElidedCallCertificate: func(_ *ssa.Function, candidate ssa.CallInstruction) (string, error) {
				if candidate == call {
					return certificate, nil
				}
				return "", nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	exact := analyze(frontend.ID)
	if got, ok := exact.ElidedCallCertificate(call); !ok || got != frontend.ID {
		t.Fatalf("planned exact certificate = %q, %t; want %q", got, ok, frontend.ID)
	}
	if err := validateCoroWorkerSyscallCall(exact, universe, call); err != nil {
		t.Fatalf("exact plan/universe join rejected: %v", err)
	}
	forged := analyze("forged-worker-address-capability")
	if err := validateCoroWorkerSyscallCall(forged, universe, call); err == nil || !strings.Contains(err.Error(), "disagrees") {
		t.Fatalf("forged plan certificate rejection = %v", err)
	}

	carrier := pkg.ssa.Func("privateCarrier")
	carrierCall := exactWorkerSyscallCall(t, universe, carrier)
	carrierCertificate, ok, err := universe.CoroWorkerSyscallCertificate(carrierCall)
	if err != nil || !ok {
		t.Fatalf("private carrier frontend certificate = %+v, %t, %v", carrierCertificate, ok, err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(pkg.ssa.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	carrierRootPlan, err := coro.AnalyzeSSA(pkg.ssa.Prog, coro.Roots{{Function: carrier, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          universe.FunctionIDConfig(),
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == carrier {
				return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (bool, error) {
			semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(candidate)
			return intrinsic && semantics.ElidesManagedCall(), err
		},
		ClassifyElidedCallCertificate: func(_ *ssa.Function, candidate ssa.CallInstruction) (string, error) {
			certificate, certified, err := universe.CoroWorkerSyscallCertificate(candidate)
			if err != nil || !certified {
				return "", err
			}
			return certificate.ID, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCoroWorkerSyscallCall(carrierRootPlan, universe, carrierCall); err == nil ||
		!strings.Contains(err.Error(), "externally established entry") {
		t.Fatalf("parameter-owner root rejection = %v", err)
	}
}

func TestCoroWorkerAddressDirectiveIsExclusiveAndShapeBound(t *testing.T) {
	for _, test := range []struct {
		name    string
		target  string
		wantErr string
	}{
		{
			name: "mixed target capabilities",
			target: `//llgo:coro workeraddr 0
//llgo:coro worker
func libc_target_trampoline()`,
			wantErr: "mutually exclusive",
		},
		{
			name: "bodyful target",
			target: `//llgo:coro workeraddr 0
func libc_target_trampoline() {}`,
			wantErr: "bodyless non-method",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			pkg := testProg.addPackage(t, "example.com/emission/workeraddrbad/"+strings.ReplaceAll(test.name, " ", "_"), `package bad
//llgo:link pc llgo.funcPCABI0
func pc(any) uintptr
//llgo:link raw llgo.syscall
func raw(uintptr) (uintptr, uintptr, uintptr)
`+test.target+`
func Root() { _, _, _ = raw(pc(libc_target_trampoline)) }
`)
			testProg.ssa.Build()
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			_, err := prepareStacklessEmissionUniverseWithOptions(
				prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
				EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
			)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Prepare error = %v; want %q", err, test.wantErr)
			}
		})
	}
}

func TestCoroWorkerAddressPhysicalSymbolCollisionFailsClosed(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/workeraddrcollision", `package collision
//llgo:link pc llgo.funcPCABI0
func pc(any) uintptr
//llgo:link raw llgo.syscall
func raw(uintptr) (uintptr, uintptr, uintptr)
//llgo:coro workeraddr 0
func libc_same_worker_target_trampoline()
//llgo:coro workeraddr 1
func same_worker_target_trampoline()
func First() { _, _, _ = raw(pc(libc_same_worker_target_trampoline)) }
func Second() { _, _, _ = raw(pc(same_worker_target_trampoline)) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	_, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err == nil || !strings.Contains(err.Error(), "conflicting producer targets or ABIs") {
		t.Fatalf("workeraddr physical collision error = %v", err)
	}
}

func exactWorkerSyscallCall(t *testing.T, universe *EmissionUniverse, fn *ssa.Function) *ssa.Call {
	t.Helper()
	if fn == nil {
		t.Fatal("worker syscall fixture has no function")
	}
	var found *ssa.Call
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil {
				continue
			}
			opcode, intrinsic, err := universe.coroIntrinsicOpcode(call.Common().StaticCallee())
			if err != nil {
				t.Fatal(err)
			}
			if !intrinsic || !isLLGoSyscallIntrinsic(opcode) {
				continue
			}
			if found != nil {
				t.Fatalf("function %q has multiple llgo.syscall calls", fn.Name())
			}
			found = call
		}
	}
	if found == nil {
		t.Fatalf("function %q has no llgo.syscall call", fn.Name())
	}
	return found
}

func exactIntrinsicOpcodeCall(t *testing.T, universe *EmissionUniverse, fn *ssa.Function, wantOpcode int) *ssa.Call {
	t.Helper()
	if fn == nil {
		t.Fatal("intrinsic fixture has no function")
	}
	var found *ssa.Call
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil {
				continue
			}
			opcode, intrinsic, err := universe.coroIntrinsicOpcode(call.Common().StaticCallee())
			if err != nil {
				t.Fatal(err)
			}
			if !intrinsic || opcode != wantOpcode {
				continue
			}
			if found != nil {
				t.Fatalf("function %q has multiple intrinsic opcode %d calls", fn.Name(), wantOpcode)
			}
			found = call
		}
	}
	if found == nil {
		t.Fatalf("function %q has no intrinsic opcode %d call", fn.Name(), wantOpcode)
	}
	return found
}
