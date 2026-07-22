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

func TestDeferDataElidesOnlyIntrinsicAndFreezesGetThreadDefer(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
import "unsafe"
func GetThreadDefer() unsafe.Pointer { return nil }
`)
	callerPkg := testProg.addPackage(t, "example.com/emission/deferdata", `package deferdata
import "unsafe"
//llgo:link DeferData llgo.deferData
func DeferData() unsafe.Pointer
func Use() unsafe.Pointer { return DeferData() }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	inputs := []EmissionPackage{
		{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
		{SSA: callerPkg.ssa, Files: []*ast.File{callerPkg.file}},
	}

	incomplete, err := PrepareEmissionUniverse(prog, nil, inputs)
	if err != nil {
		t.Fatal(err)
	}
	call := allocaCStrTestCalls(callerPkg.ssa.Func("Use"))[0]
	if semantics, intrinsic, err := incomplete.CoroIntrinsicCallSiteSemantics(call); err != nil || !intrinsic || semantics != CoroIntrinsicCallUnsupported {
		t.Fatalf("incomplete deferData semantics = %v, %v, %v; want legacy unsupported, true, nil", semantics, intrinsic, err)
	}

	universe, err := PrepareEmissionUniverseWithOptions(prog, nil, inputs, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		t.Fatal(err)
	}
	owner := callerPkg.ssa.Func("Use")
	helper := runtimePkg.ssa.Func("GetThreadDefer")
	lowered, err := universe.CoroLoweredCalls(owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(lowered) != 1 || lowered[0].LogicalName != "GetThreadDefer" || lowered[0].Target != helper {
		t.Fatalf("deferData lowered calls = %+v; want exact owner-scoped GetThreadDefer", lowered)
	}
	call = allocaCStrTestCalls(owner)[0]
	semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
	if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineWithLoweredCalls {
		t.Fatalf("deferData semantics = %v, %v, %v; want inline-with-lowered-calls, true, nil", semantics, intrinsic, err)
	}

	delete(universe.loweredCalls[owner], "GetThreadDefer")
	if semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineWithLoweredCalls {
		t.Fatalf("deferData frozen semantics after scratch mutation = %v, %v, %v", semantics, intrinsic, err)
	}
}

func TestDeferDataRejectsWrongResultShape(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/deferdatabad", `package deferdatabad
//llgo:link DeferData llgo.deferData
func DeferData() uintptr
func Use() uintptr { return DeferData() }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
	if _, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err == nil || !intrinsic || !strings.Contains(err.Error(), "func() unsafe.Pointer") {
		t.Fatalf("wrong-shape deferData semantics = _, %v, %v; want exact-shape error", intrinsic, err)
	}
}
