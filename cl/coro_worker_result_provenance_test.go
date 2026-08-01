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
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestCoroWorkerResultProjectionDirectiveIsCanonical(t *testing.T) {
	for _, test := range []struct {
		name      string
		directive string
		body      string
		wantOK    bool
	}{
		{name: "exact", directive: "//llgo:coro workerresult v1 fn=0 map=r1:r1", body: "{}", wantOK: true},
		{name: "two ordered mappings", directive: "//llgo:coro workerresult v1 fn=0 map=r1:r1,r2:r2", body: "{}", wantOK: true},
		{name: "extra space", directive: "//llgo:coro  workerresult v1 fn=0 map=r1:r1", body: "{}"},
		{name: "wrong field order", directive: "//llgo:coro workerresult v1 map=r1:r1 fn=0", body: "{}"},
		{name: "leading zero", directive: "//llgo:coro workerresult v1 fn=00 map=r1:r1", body: "{}"},
		{name: "duplicate result", directive: "//llgo:coro workerresult v1 fn=0 map=r1:r1,r1:r2", body: "{}"},
		{name: "unordered result", directive: "//llgo:coro workerresult v1 fn=0 map=r2:r2,r1:r1", body: "{}"},
		{name: "unknown word", directive: "//llgo:coro workerresult v1 fn=0 map=result1:r1", body: "{}"},
		{name: "bodyless", directive: "//llgo:coro workerresult v1 fn=0 map=r1:r1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "package p\n" + test.directive + "\nfunc f(fn uintptr) uintptr " + test.body + "\n"
			file, err := parser.ParseFile(token.NewFileSet(), "projection.go", source, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			decl, _ := file.Decls[0].(*ast.FuncDecl)
			_, ok, parseErr := parseCoroWorkerResultProjectionDecl(decl)
			if got := ok && parseErr == nil; got != test.wantOK {
				t.Fatalf("projection parse = ok:%t err:%v; want success=%t", ok, parseErr, test.wantOK)
			}
		})
	}
}

func TestCoroWorkerWordCallableABIResultMetadataIsExact(t *testing.T) {
	for _, test := range []struct {
		value    string
		wantOK   bool
		wantArgs int
		wantMask uint8
	}{
		{value: "word-call.v1/0", wantOK: true},
		{value: "word-call.v1/9", wantOK: true, wantArgs: 9},
		{value: "word-call.v1/3+foreign-pointer-result=r1", wantOK: true, wantArgs: 3, wantMask: 1},
		{value: ""},
		{value: "word-call.v2/3+foreign-pointer-result=r1"},
		{value: "word-call.v1/"},
		{value: "word-call.v1/01"},
		{value: "word-call.v1/+3"},
		{value: "word-call.v1/-0"},
		{value: "word-call.v1/10"},
		{value: "word-call.v1/3+foreign-pointer-result=r2"},
		{value: "word-call.v1/3+foreign-pointer-result=r1+foreign-pointer-result=r1"},
		{value: "word-call.v1/3+foreign-pointer-result=r1x"},
		{value: "word-call.v1/3 +foreign-pointer-result=r1"},
	} {
		shape, ok := parseCoroWorkerWordCallableABI(test.value)
		if ok != test.wantOK || shape.wordArgs != test.wantArgs || shape.foreignPointerResultMask != test.wantMask {
			t.Errorf("parseCoroWorkerWordCallableABI(%q) = %+v, %t; want args=%d mask=%#x ok=%t",
				test.value, shape, ok, test.wantArgs, test.wantMask, test.wantOK)
		}
	}
}

const coroWorkerResultProvenanceFixture = `package workerresult

import "unsafe"

//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(fn any) uintptr

//llgo:link raw llgo.syscall
func raw(fn, a0 uintptr) (uintptr, uintptr, uintptr)

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/1+foreign-pointer-result=r1
func libc_pointer_result_v1_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/1
func libc_scalar_result_v1_trampoline()

func DirectR1(a0 uintptr) unsafe.Pointer {
	r1, _, _ := raw(funcPCABI0(libc_pointer_result_v1_trampoline), a0)
	return unsafe.Pointer(r1)
}

func DirectR2(a0 uintptr) unsafe.Pointer {
	_, r2, _ := raw(funcPCABI0(libc_pointer_result_v1_trampoline), a0)
	return unsafe.Pointer(r2)
}

func DerivedR1(a0 uintptr) unsafe.Pointer {
	r1, _, _ := raw(funcPCABI0(libc_pointer_result_v1_trampoline), a0)
	return unsafe.Pointer(r1 + a0)
}

func ScalarR1(a0 uintptr) unsafe.Pointer {
	r1, _, _ := raw(funcPCABI0(libc_scalar_result_v1_trampoline), a0)
	return unsafe.Pointer(r1)
}

func privateCarrier(fn, a0 uintptr) uintptr {
	r1, _, _ := raw(fn, a0)
	return r1
}

func projectedCarrier(fn, a0 uintptr) (uintptr, uintptr, uintptr) {
	r1, r2, err := raw(fn, a0)
	return r1, r2, err
}

func projectedTwoSinks(fn, a0 uintptr) (uintptr, uintptr, uintptr) {
	r1, r2, err := raw(fn, a0)
	raw(fn, a0)
	return r1, r2, err
}

func ThroughProjectedPointer(a0 uintptr) unsafe.Pointer {
	r1, _, _ := projectedCarrier(funcPCABI0(libc_pointer_result_v1_trampoline), a0)
	return unsafe.Pointer(r1)
}

func ThroughProjectedScalar(a0 uintptr) unsafe.Pointer {
	r1, _, _ := projectedCarrier(funcPCABI0(libc_scalar_result_v1_trampoline), a0)
	return unsafe.Pointer(r1)
}

func ThroughProjectedDerived(a0 uintptr) unsafe.Pointer {
	r1, _, _ := projectedCarrier(funcPCABI0(libc_pointer_result_v1_trampoline), a0)
	return unsafe.Pointer(r1 + a0)
}

func ThroughProjectedTwoSinksPointer(a0 uintptr) unsafe.Pointer {
	r1, _, _ := projectedTwoSinks(funcPCABI0(libc_pointer_result_v1_trampoline), a0)
	return unsafe.Pointer(r1)
}

func ThroughPointer(a0 uintptr) uintptr {
	return privateCarrier(funcPCABI0(libc_pointer_result_v1_trampoline), a0)
}

func ThroughPrivatePointer(a0 uintptr) unsafe.Pointer {
	return unsafe.Pointer(privateCarrier(funcPCABI0(libc_pointer_result_v1_trampoline), a0))
}

func ThroughScalar(a0 uintptr) uintptr {
	return privateCarrier(funcPCABI0(libc_scalar_result_v1_trampoline), a0)
}
`

func TestCoroWorkerForeignPointerResultProjectsAcrossExactWrapperCall(t *testing.T) {
	prog, pkg, universe := prepareCoroWorkerResultProvenanceFixture(t)
	defer prog.Dispose()

	for _, test := range []struct {
		function string
		want     bool
	}{
		{function: "ThroughProjectedPointer", want: true},
		{function: "ThroughProjectedTwoSinksPointer", want: true},
		{function: "ThroughPrivatePointer", want: true},
		{function: "ThroughProjectedScalar"},
		{function: "ThroughProjectedDerived"},
	} {
		t.Run(test.function, func(t *testing.T) {
			root := pkg.Func(test.function)
			plan := analyzeCoroWorkerResultProvenancePlan(t, pkg, universe, root)
			audit, err := newCoroPhysicalPureSSAAudit(universe, plan, root, "")
			if err != nil {
				t.Fatal(err)
			}
			var conversion *ssa.Convert
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					candidate, ok := instruction.(*ssa.Convert)
					if ok && coroFrameRetentionUintptrLike(candidate.X.Type()) && coroFrameRetentionPointerLike(candidate.Type()) {
						conversion = candidate
					}
				}
			}
			if conversion == nil {
				t.Fatal("fixture has no uintptr-to-pointer conversion")
			}
			if got := audit.provesWorkerForeignPointerResult(conversion.X); got != test.want {
				t.Fatalf("projected worker result proof for %T %q = %t; want %t", conversion.X, conversion.X, got, test.want)
			}
			reason := audit.validateConvert(conversion)
			if test.want && reason != "" {
				t.Fatalf("exact projected r1 extract rejected: %s", reason)
			}
			if !test.want && !strings.Contains(reason, "has no traceable exact pointer provenance") {
				t.Fatalf("non-exact projected result rejection = %q; want provenance failure", reason)
			}
		})
	}
}

func TestCoroWorkerForeignPointerResultMeetsClosedDynamicTargets(t *testing.T) {
	const prefix = `package dynamicworkerresult

import "unsafe"

//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(fn any) uintptr

//llgo:link raw llgo.syscall
func raw(fn, a0 uintptr) (uintptr, uintptr, uintptr)

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/1+foreign-pointer-result=r1
func pointer_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/1
func scalar_trampoline()

func pointerA(a0 uintptr) uintptr {
	r1, _, _ := raw(funcPCABI0(pointer_trampoline), a0)
	return r1
}
`
	for _, test := range []struct {
		name   string
		extra  string
		second string
		want   bool
	}{
		{
			name: "all pointer results",
			extra: `func pointerB(a0 uintptr) uintptr {
	r1, _, _ := raw(funcPCABI0(pointer_trampoline), a0)
	return r1
}
`,
			second: "pointerB",
			want:   true,
		},
		{
			name: "mixed scalar result",
			extra: `func scalar(a0 uintptr) uintptr {
	r1, _, _ := raw(funcPCABI0(scalar_trampoline), a0)
	return r1
}
`,
			second: "scalar",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := prefix + test.extra + `func Root(second bool, a0 uintptr) unsafe.Pointer {
	fn := pointerA
	if second {
		fn = ` + test.second + `
	}
	return unsafe.Pointer(fn(a0))
}
`
			pkg, _, files := buildGoSSAPkg(t, source)
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, err := prepareStacklessEmissionUniverseWithOptions(
				prog, nil, []EmissionPackage{{SSA: pkg, Files: files}},
				EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
			)
			if err != nil {
				t.Fatal(err)
			}
			ssaUniverse, err := coro.NewSSAEmissionUniverse(pkg.Prog, universe.Functions())
			if err != nil {
				t.Fatal(err)
			}
			root := pkg.Func("Root")
			plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse:  ssaUniverse,
				FunctionIDs:       universe.FunctionIDConfig(),
				DynamicResolution: coro.DynamicCHAClosed,
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
			audit, err := newCoroPhysicalPureSSAAudit(universe, plan, root, "")
			if err != nil {
				t.Fatal(err)
			}
			var conversion *ssa.Convert
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					candidate, ok := instruction.(*ssa.Convert)
					if ok && coroFrameRetentionUintptrLike(candidate.X.Type()) && coroFrameRetentionPointerLike(candidate.Type()) {
						conversion = candidate
					}
				}
			}
			if conversion == nil {
				t.Fatal("fixture has no uintptr-to-pointer conversion")
			}
			call, ok := conversion.X.(*ssa.Call)
			if !ok {
				t.Fatalf("conversion source = %T; want dynamic call", conversion.X)
			}
			callPlan, planned := plan.CallPlan(call)
			if !planned || callPlan.Open || len(callPlan.Targets) < 2 {
				t.Fatalf("dynamic call plan = %+v, %t; want closed multi-target plan", callPlan, planned)
			}
			if got := audit.provesWorkerForeignPointerResult(conversion.X); got != test.want {
				t.Fatalf("closed dynamic pointer-result proof = %t; want %t (plan=%+v)", got, test.want, callPlan)
			}
			reason := audit.validateConvert(conversion)
			if test.want && reason != "" {
				t.Fatalf("closed all-pointer result rejected: %s", reason)
			}
			if !test.want && !strings.Contains(reason, "has no traceable exact pointer provenance") {
				t.Fatalf("closed mixed result rejection = %q; want provenance failure", reason)
			}
		})
	}
}

func TestCoroWorkerForeignPointerResultThroughLinuxMmapWrapper(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.addPackage(t, "syscall", `package syscall
const (
	SYS_READ = 0
	SYS_MMAP = 9
)
`)
	const packagePath = "example.com/emission/linuxmmapresult"
	prepared := testProg.addPackage(t, packagePath, `package linuxmmapresult

import (
	stdsyscall "syscall"
)

//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(fn any) uintptr

//llgo:link raw llgo.syscall
func raw(fn, trap, a1, a2, a3, a4, a5, a6 uintptr) (uintptr, uintptr, uintptr)

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/7
func libc___llgo_linux_syscall6_v1_trampoline()

type errno uintptr
func (errno) Error() string { return "errno" }

func syscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err error) {
	r1, r2, e1 := raw(funcPCABI0(libc___llgo_linux_syscall6_v1_trampoline), trap, a1, a2, a3, a4, a5, a6)
	if e1 != 0 {
		err = errno(e1)
	}
	return
}

func mmap(a0 uintptr) (xaddr uintptr, err error) {
	r0, _, e1 := syscall6(stdsyscall.SYS_MMAP, a0, 0, 0, 0, 0, 0)
	xaddr = uintptr(r0)
	if e1 != nil {
		err = e1
	}
	return
}

func read(a0 uintptr) (value uintptr, err error) {
	r0, _, e1 := syscall6(stdsyscall.SYS_READ, a0, 0, 0, 0, 0, 0)
	if e1 != nil {
		err = e1
	}
	return r0, err
}

type mmapper struct {
	mmap func(uintptr) (uintptr, error)
}

var mapper = &mmapper{mmap: mmap}

func Root(a0 uintptr) uintptr {
	addr, errno := mapper.mmap(a0)
	if errno != nil {
		return 0
	}
	return addr
}

func ReadRoot(a0 uintptr) uintptr {
	value, errno := read(a0)
	if errno != nil {
		return 0
	}
	return value
}
`)
	testProg.ssa.Build()
	pkg := prepared.ssa
	prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "linux", GOARCH: "amd64"})
	defer prog.Dispose()
	prog.SetLinkname(packagePath+".libc___llgo_linux_syscall6_v1_trampoline", "C.__llgo_linux_syscall6_v1")
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg, Files: []*ast.File{prepared.file}}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(pkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		root string
		want bool
	}{
		{root: "Root", want: true},
		{root: "ReadRoot"},
	} {
		t.Run(test.root, func(t *testing.T) {
			root := pkg.Func(test.root)
			plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse:  ssaUniverse,
				FunctionIDs:       universe.FunctionIDConfig(),
				DynamicResolution: coro.DynamicCHAClosed,
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					if fn == pkg.Func("syscall6") {
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
			audit, err := newCoroPhysicalPureSSAAudit(universe, plan, root, "")
			if err != nil {
				t.Fatal(err)
			}
			var result *ssa.Extract
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					candidate, ok := instruction.(*ssa.Extract)
					if ok && candidate.Index == 0 && coroFrameRetentionUintptrLike(candidate.Type()) {
						if _, call := candidate.Tuple.(*ssa.Call); call {
							result = candidate
						}
					}
				}
			}
			if result == nil {
				t.Fatal("fixture has no uintptr call result")
			}
			if _, ok := result.Tuple.(*ssa.Call); !ok {
				t.Fatalf("tuple = %T %q; want wrapper call", result.Tuple, result.Tuple)
			}
			if got := audit.provesWorkerForeignPointerResult(result); got != test.want {
				t.Fatalf("Linux trap pointer proof = %t, want %t", got, test.want)
			}
		})
	}
}

func TestCoroWorkerForeignPointerResultCertificateMask(t *testing.T) {
	prog, pkg, universe := prepareCoroWorkerResultProvenanceFixture(t)
	defer prog.Dispose()

	for _, test := range []struct {
		function    string
		wantTargets int
		wantMask    uint8
	}{
		{function: "DirectR1", wantTargets: 1, wantMask: 1},
		{function: "DirectR2", wantTargets: 1, wantMask: 1},
		{function: "DerivedR1", wantTargets: 1, wantMask: 1},
		{function: "ScalarR1", wantTargets: 1, wantMask: 0},
		// A private carrier callable by either target may park safely, but it
		// cannot promise pointer provenance that only one incoming target owns.
		{function: "privateCarrier", wantTargets: 2, wantMask: 0},
	} {
		call := exactWorkerSyscallCall(t, universe, pkg.Func(test.function))
		certificate, certified, err := universe.CoroWorkerSyscallCertificate(call)
		if err != nil || !certified || certificate.ID == "" ||
			certificate.StaticTargetCount != test.wantTargets ||
			certificate.ForeignPointerResultMask != test.wantMask {
			t.Errorf("%s certificate = %+v, %t, %v; want targets=%d mask=%#x",
				test.function, certificate, certified, err, test.wantTargets, test.wantMask)
		}
	}
}

func TestCoroWorkerForeignPointerResultOnlyAuthorizesExactDirectExtract(t *testing.T) {
	prog, pkg, universe := prepareCoroWorkerResultProvenanceFixture(t)
	defer prog.Dispose()

	for _, test := range []struct {
		function string
		want     bool
	}{
		{function: "DirectR1", want: true},
		{function: "DirectR2"},
		{function: "DerivedR1"},
		{function: "ScalarR1"},
	} {
		t.Run(test.function, func(t *testing.T) {
			root := pkg.Func(test.function)
			call := exactWorkerSyscallCall(t, universe, root)
			certificate, certified, err := universe.CoroWorkerSyscallCertificate(call)
			if err != nil || !certified || certificate.ID == "" {
				t.Fatalf("worker certificate = %+v, %t, %v", certificate, certified, err)
			}
			plan := analyzeCoroWorkerResultProvenancePlan(t, pkg, universe, root)
			audit, err := newCoroPhysicalPureSSAAudit(universe, plan, root, "")
			if err != nil {
				t.Fatal(err)
			}
			var conversion *ssa.Convert
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					candidate, ok := instruction.(*ssa.Convert)
					if ok && coroFrameRetentionUintptrLike(candidate.X.Type()) && coroFrameRetentionPointerLike(candidate.Type()) {
						if conversion != nil {
							t.Fatalf("fixture has multiple uintptr-to-pointer conversions")
						}
						conversion = candidate
					}
				}
			}
			if conversion == nil {
				t.Fatal("fixture has no uintptr-to-pointer conversion")
			}
			if got := audit.provesWorkerForeignPointerResult(conversion.X); got != test.want {
				t.Fatalf("worker result proof for %T %q = %t; want %t", conversion.X, conversion.X, got, test.want)
			}
			reason := audit.validateConvert(conversion)
			if test.want && reason != "" {
				t.Fatalf("exact r1 extract rejected: %s", reason)
			}
			if !test.want && !strings.Contains(reason, "has no traceable exact pointer provenance") {
				t.Fatalf("non-exact result rejection = %q; want provenance failure", reason)
			}
		})
	}
}

func prepareCoroWorkerResultProvenanceFixture(t *testing.T) (llssa.Program, *ssa.Package, *EmissionUniverse) {
	t.Helper()
	pkg, _, files := buildGoSSAPkg(t, coroWorkerResultProvenanceFixture)
	prog := newLLSSAProg(t)
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg, Files: files}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, universe
}

func analyzeCoroWorkerResultProvenancePlan(
	t *testing.T,
	pkg *ssa.Package,
	universe *EmissionUniverse,
	root *ssa.Function,
) *coro.SSAPlan {
	t.Helper()
	ssaUniverse, err := coro.NewSSAEmissionUniverse(pkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          universe.FunctionIDConfig(),
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == root || fn == pkg.Func("privateCarrier") ||
				fn == pkg.Func("projectedCarrier") || fn == pkg.Func("projectedTwoSinks") {
				return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, candidate ssa.CallInstruction) (bool, error) {
			if callee := candidate.Common().StaticCallee(); callee != nil && callee.Pkg != nil &&
				callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init" {
				return true, nil
			}
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
