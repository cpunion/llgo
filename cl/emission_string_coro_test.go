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

	llssa "github.com/goplus/llgo/ssa"
)

func TestStringIntrinsicFreezesExactVarargsHelper(t *testing.T) {
	testProg := newEmissionTestProgram()
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
func StringFromCStr(*int8) string { return "" }
func StringFrom(*int8, int) string { return "" }
`)
	callerPkg := testProg.addPackage(t, "example.com/emission/stringintrinsic", `package stringintrinsic
//llgo:link String llgo.string
func String(value *int8, __llgo_va_list ...any) string
func WithoutLen(value *int8) string { return String(value) }
func WithLen(value *int8, length int) string { return String(value, length) }
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

	for _, test := range []struct {
		owner  string
		helper string
	}{
		{owner: "WithoutLen", helper: "StringFromCStr"},
		{owner: "WithLen", helper: "StringFrom"},
	} {
		owner := callerPkg.ssa.Func(test.owner)
		lowered, err := universe.CoroLoweredCalls(owner)
		if err != nil {
			t.Fatal(err)
		}
		if len(lowered) != 1 || lowered[0].LogicalName != test.helper || lowered[0].Target != runtimePkg.ssa.Func(test.helper) {
			t.Fatalf("%s lowered calls = %+v; want exact %s", test.owner, lowered, test.helper)
		}
		calls := allocaCStrTestCalls(owner)
		if len(calls) != 1 {
			t.Fatalf("%s calls = %d, want one", test.owner, len(calls))
		}
		semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(calls[0])
		if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineWithLoweredCalls {
			t.Fatalf("%s semantics = %v, %v, %v; want inline-with-lowered-calls, true, nil", test.owner, semantics, intrinsic, err)
		}
	}
}

func TestStringIntrinsicRejectsWrongDeclarationShape(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/stringintrinsicbad", `package stringintrinsicbad
//llgo:link String llgo.string
func String(value *int8, __llgo_va_list ...any) uintptr
func Use(value *int8) uintptr { return String(value) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
	if _, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err == nil || !intrinsic || !strings.Contains(err.Error(), "func(*int8, ...any) string") {
		t.Fatalf("wrong-shape string semantics = _, %v, %v; want exact-shape error", intrinsic, err)
	}
}

func TestStringDataIntrinsicIsExactInlineNoSuspend(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/stringdataintrinsic", `package stringdataintrinsic
//llgo:link StringData llgo.stringData
func StringData(string) *int8
func Use(value string) *int8 { return StringData(value) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file},
	}})
	if err != nil {
		t.Fatal(err)
	}
	call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
	semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
	if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
		t.Fatalf("stringData semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}
}

func TestStringDataIntrinsicRejectsWrongDeclarationShape(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/stringdataintrinsicbad", `package stringdataintrinsicbad
//llgo:link StringData llgo.stringData
func StringData(string) uintptr
func Use(value string) uintptr { return StringData(value) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{
		SSA: pkg.ssa, Files: []*ast.File{pkg.file},
	}})
	if err != nil {
		t.Fatal(err)
	}
	call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
	if _, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err == nil || !intrinsic ||
		!strings.Contains(err.Error(), "func(string) *int8") {
		t.Fatalf("wrong-shape stringData semantics = _, %v, %v; want exact-shape error", intrinsic, err)
	}
}
