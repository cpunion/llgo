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
	"testing"
)

func TestSkipIntrinsicIsExactInlineNoSuspendForGoLinkname(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	pkg := testProg.addPackage(t, "example.com/emission/skip", `package skip
import _ "unsafe"
//go:linkname Skip llgo.skip
func Skip(func()) (uintptr, uintptr)
func Use(callback func()) (uintptr, uintptr) { return Skip(callback) }
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

	semantics, intrinsic, err := universe.CoroIntrinsicSemantics(pkg.ssa.Func("Skip"))
	if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
		t.Fatalf("skip function semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}
	calls := allocaCStrTestCalls(pkg.ssa.Func("Use"))
	if len(calls) != 1 {
		t.Fatalf("Use calls = %d; want the exact skip call", len(calls))
	}
	semantics, intrinsic, err = universe.CoroIntrinsicCallSiteSemantics(calls[0])
	if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
		t.Fatalf("skip call semantics = %v, %v, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}
	plan, frozen, err := universe.CoroCallSitePlan(calls[0])
	if err != nil || !frozen || plan.Elision != CoroCallElidedIntrinsic {
		t.Fatalf("skip call plan = %+v, %v, %v; want exact intrinsic elision", plan, frozen, err)
	}
}
