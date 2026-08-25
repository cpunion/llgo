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
)

const coroDarwinEnvironmentCallableShadowFixture = `package darwinenv

//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(fn any) uintptr

//llgo:link syscall1Int32 llgo.syscall32
func syscall1Int32(fn, a1 uintptr) (uintptr, uintptr, uintptr)

//llgo:link syscall3Int32 llgo.syscall32
func syscall3Int32(fn, a1, a2, a3 uintptr) (uintptr, uintptr, uintptr)

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3
func libc_setenv_trampoline()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/1
func libc_unsetenv_trampoline()

func Setenv(name, value uintptr) uintptr {
	r1, _, _ := syscall3Int32(funcPCABI0(libc_setenv_trampoline), name, value, 1)
	return r1
}

func Unsetenv(name uintptr) uintptr {
	r1, _, _ := syscall1Int32(funcPCABI0(libc_unsetenv_trampoline), name)
	return r1
}
`

const coroCompilerSourcePatchWorkerFixture = `package sourcepatchworker

//llgo:link funcPCABI0 llgo.funcPCABI0
func funcPCABI0(fn any) uintptr

//llgo:link syscall3Int32 llgo.syscall32
func syscall3Int32(fn, a1, a2, a3 uintptr) (uintptr, uintptr, uintptr)

func libc_getpid_trampoline()

func Getpid() uintptr {
	r1, _, _ := syscall3Int32(funcPCABI0(libc_getpid_trampoline), 0, 0, 0)
	return r1
}
`

func TestCoroDarwinEnvironmentWorkerPublishesExactCallableShadows(t *testing.T) {
	testProg := newEmissionTestProgram()
	const packagePath = "example.com/emission/darwinenv"
	pkg := testProg.addPackage(t, packagePath, coroDarwinEnvironmentCallableShadowFixture)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	prog.SetLinkname(packagePath+".libc_setenv_trampoline", "C.setenv")
	prog.SetLinkname(packagePath+".libc_unsetenv_trampoline", "C.unsetenv")
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := AnalyzeCoroCallableShadows(universe)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		wrapper  string
		target   string
		physical string
		arity    int
	}{
		{wrapper: "Setenv", target: "libc_setenv_trampoline", physical: "setenv", arity: 3},
		{wrapper: "Unsetenv", target: "libc_unsetenv_trampoline", physical: "unsetenv", arity: 1},
	} {
		t.Run(test.wrapper, func(t *testing.T) {
			wrapper := pkg.ssa.Func(test.wrapper)
			producer := exactIntrinsicOpcodeCall(t, universe, wrapper, llgoFuncPCABI0)
			shadow, ok := analysis.Producer(producer)
			if !ok || shadow.Target == nil || shadow.Target.Name() != test.target ||
				shadow.PhysicalSymbol != test.physical || shadow.ContractCertificateID == "" ||
				shadow.LegacyWorkerAddressCompat ||
				shadow.ABI != (CoroCallableShadowABI{Family: coroCallableShadowWorkerSyscallFamily, WordArgs: test.arity}) {
				reason, rejected := analysis.ProducerRejection(producer)
				t.Fatalf("callable shadow = %+v, %t; rejection=%q,%t", shadow, ok, reason, rejected)
			}

			call := exactWorkerSyscallCall(t, universe, wrapper)
			certificate, certified, err := universe.CoroWorkerSyscallCertificate(call)
			if err != nil || !certified || certificate.ID == "" ||
				certificate.StaticTargetCount != 1 || certificate.WorkerABISignature == "" {
				t.Fatalf("worker certificate = %+v, %t, %v", certificate, certified, err)
			}
		})
	}
}

func TestCoroCompilerSourcePatchInfersExactWorkerAddressDeclaration(t *testing.T) {
	testProg := newEmissionTestProgram()
	const packagePath = "example.com/emission/sourcepatchworker"
	pkg := testProg.addPackage(t, packagePath, coroCompilerSourcePatchWorkerFixture)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	prog.SetLinkname(packagePath+".libc_getpid_trampoline", "C.getpid")
	filename := testProg.fset.PositionFor(pkg.file.Package, true).Filename
	universe, err := prepareStacklessEmissionUniverseWithOptions(
		prog, nil, []EmissionPackage{{
			SSA:                      pkg.ssa,
			Files:                    []*ast.File{pkg.file},
			CompilerSourcePatchFiles: []string{filename},
		}},
		EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
	)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := AnalyzeCoroCallableShadows(universe)
	if err != nil {
		t.Fatal(err)
	}
	producer := exactIntrinsicOpcodeCall(t, universe, pkg.ssa.Func("Getpid"), llgoFuncPCABI0)
	shadow, ok := analysis.Producer(producer)
	if !ok || shadow.Target == nil || shadow.Target.Name() != "libc_getpid_trampoline" ||
		shadow.PhysicalSymbol != "getpid" || shadow.ContractCertificateID == "" ||
		shadow.ABI != (CoroCallableShadowABI{Family: coroCallableShadowWorkerSyscallFamily, WordArgs: 3}) {
		reason, rejected := analysis.ProducerRejection(producer)
		t.Fatalf("source-patch callable shadow = %+v, %t; rejection=%q,%t", shadow, ok, reason, rejected)
	}
	call := exactWorkerSyscallCall(t, universe, pkg.ssa.Func("Getpid"))
	certificate, certified, err := universe.CoroWorkerSyscallCertificate(call)
	if err != nil || !certified || certificate.ID == "" || certificate.StaticTargetCount != 1 {
		t.Fatalf("source-patch worker certificate = %+v, %t, %v", certificate, certified, err)
	}
}

func TestCoroCompilerSourcePatchWorkerAddressAuthorityFailsClosed(t *testing.T) {
	build := func(t *testing.T, sourcePatchFiles []string) (*EmissionUniverse, emissionTestPackage, error) {
		t.Helper()
		testProg := newEmissionTestProgram()
		const packagePath = "example.com/emission/sourcepatchworkerclosed"
		pkg := testProg.addPackage(t, packagePath, coroCompilerSourcePatchWorkerFixture)
		testProg.ssa.Build()
		prog := newLLSSAProg(t)
		t.Cleanup(prog.Dispose)
		prog.SetLinkname(packagePath+".libc_getpid_trampoline", "C.getpid")
		universe, err := prepareStacklessEmissionUniverseWithOptions(
			prog, nil, []EmissionPackage{{
				SSA:                      pkg.ssa,
				Files:                    []*ast.File{pkg.file},
				CompilerSourcePatchFiles: sourcePatchFiles,
			}},
			EmissionUniverseOptions{CoroTargetCapabilities: CoroNativeTargetCapabilities()},
		)
		return universe, pkg, err
	}

	t.Run("ordinary source has no authority", func(t *testing.T) {
		universe, pkg, err := build(t, nil)
		if err != nil {
			t.Fatal(err)
		}
		analysis, err := AnalyzeCoroCallableShadows(universe)
		if err != nil {
			t.Fatal(err)
		}
		producer := exactIntrinsicOpcodeCall(t, universe, pkg.ssa.Func("Getpid"), llgoFuncPCABI0)
		if _, ok := analysis.Producer(producer); ok {
			t.Fatal("ordinary source unexpectedly gained source-patch worker-address authority")
		}
		if reason, ok := analysis.ProducerRejection(producer); !ok || reason != "target-lacks-workeraddr" {
			t.Fatalf("ordinary-source rejection = %q, %t; want target-lacks-workeraddr", reason, ok)
		}
	})

	t.Run("unmatched build input", func(t *testing.T) {
		_, _, err := build(t, []string{"not-the-selected-source-patch.go"})
		if err == nil || !strings.Contains(err.Error(), "has no exact input AST") {
			t.Fatalf("unmatched source-patch input error = %v", err)
		}
	})
}
