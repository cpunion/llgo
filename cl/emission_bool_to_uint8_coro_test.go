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

func TestBoolToUint8IntrinsicIsExactInlineNoSuspend(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/booltouint8", `package booltouint8
//llgo:link BoolToUint8 llgo.boolToUint8
func BoolToUint8(bool) uint8
func Use(value bool) uint8 { return BoolToUint8(value) }
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
		t.Fatalf("boolToUint8 semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}
	plan, frozen, err := universe.CoroCallSitePlan(call)
	if err != nil || !frozen || plan.Elision != CoroCallElidedIntrinsic {
		t.Fatalf("boolToUint8 call plan = %+v, %v, %v; want exact intrinsic elision", plan, frozen, err)
	}
}

func TestBoolToUint8IntrinsicRejectsWrongDeclarationShape(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/booltouint8bad", `package booltouint8bad
//llgo:link BoolToUint8 llgo.boolToUint8
func BoolToUint8(bool) uintptr
func Use(value bool) uintptr { return BoolToUint8(value) }
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
		!strings.Contains(err.Error(), "func(bool) uint8") {
		t.Fatalf("wrong-shape boolToUint8 semantics = _, %v, %v; want exact-shape error", intrinsic, err)
	}
}
