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
