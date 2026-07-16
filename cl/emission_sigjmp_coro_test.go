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

	llssa "github.com/goplus/llgo/ssa"
)

func TestLegacySigjmpIntrinsicsFreezeNativeRuntimeLeaves(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func Sigsetjmp(unsafe.Pointer, int32) int32 { return 0 }
func Siglongjmp(unsafe.Pointer, int32) {}
`)
	callerPkg := testProg.addPackage(t, "example.com/emission/sigjmp", `package sigjmp
import "unsafe"
//llgo:link Sigjmpbuf llgo.sigjmpbuf
func Sigjmpbuf() unsafe.Pointer
//llgo:link Sigsetjmp llgo.sigsetjmp
func Sigsetjmp(unsafe.Pointer, int32) int32
//llgo:link Siglongjmp llgo.siglongjmp
func Siglongjmp(unsafe.Pointer, int32)
func Use() int32 {
	buf := Sigjmpbuf()
	value := Sigsetjmp(buf, 0)
	Siglongjmp(buf, 1)
	return value
}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
		{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
		{SSA: callerPkg.ssa, Files: []*ast.File{callerPkg.file}},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		t.Fatal(err)
	}
	owner := callerPkg.ssa.Func("Use")
	lowered, err := universe.CoroLoweredCalls(owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(lowered) != 2 || lowered[0].LogicalName != "Siglongjmp" || lowered[0].Target != runtimePkg.ssa.Func("Siglongjmp") ||
		lowered[1].LogicalName != "Sigsetjmp" || lowered[1].Target != runtimePkg.ssa.Func("Sigsetjmp") {
		t.Fatalf("legacy sigjmp lowered calls = %+v; want exact native runtime leaves", lowered)
	}
	for _, call := range allocaCStrTestCalls(owner) {
		semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
		if err != nil || !intrinsic || !semantics.ElidesManagedCall() {
			t.Fatalf("legacy sigjmp call %q semantics = %v, %v, %v; want exact elided intrinsic", call, semantics, intrinsic, err)
		}
	}
}

func TestLegacySigjmpIntrinsicsFailClosedOnWasm(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	pkg := testProg.addPackage(t, "example.com/emission/sigjmpwasm", `package sigjmpwasm
import "unsafe"
//llgo:link Siglongjmp llgo.siglongjmp
func Siglongjmp(unsafe.Pointer, int32)
func Use(value unsafe.Pointer) { Siglongjmp(value, 1) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProgForTarget(t, &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"})
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
	if _, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err == nil || !intrinsic || !strings.Contains(err.Error(), "requires a non-legacy coroutine PanicABI") {
		t.Fatalf("wasm legacy siglongjmp semantics = _, %v, %v; want PanicABI fail-closed error", intrinsic, err)
	}
}
