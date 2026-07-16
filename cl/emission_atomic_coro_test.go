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

func TestAtomicIntrinsicIsExactInlineNoSuspend(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/atomicintrinsic", `package atomicintrinsic
type Counter int64
//llgo:link Add llgo.atomicAdd
func Add(ptr *Counter, value Counter) Counter { return value }
//llgo:link Load llgo.atomicLoad
func Load(ptr *Counter) Counter { return *ptr }
//llgo:link Store llgo.atomicStore
func Store(ptr *Counter, value Counter) {}
//llgo:link Compare llgo.atomicCmpXchg
func Compare(ptr *Counter, old, new Counter) (Counter, bool) { return old, false }
func Use(ptr *Counter) Counter {
	Store(ptr, 1)
	value, _ := Compare(ptr, 1, 2)
	return Add(ptr, value) + Load(ptr)
}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	calls := allocaCStrTestCalls(pkg.ssa.Func("Use"))
	if len(calls) != 4 {
		t.Fatalf("atomic Use calls = %d, want four", len(calls))
	}
	for _, call := range calls {
		semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
		if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
			t.Fatalf("atomic call %q semantics = %v, %v, %v; want inline-no-suspend, true, nil", call, semantics, intrinsic, err)
		}
	}
}

func TestAtomicIntrinsicRejectsMismatchedValueShape(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/atomicintrinsicbad", `package atomicintrinsicbad
//llgo:link Add llgo.atomicAdd
func Add(ptr *int64, value int32) int64 { return 0 }
func Use(ptr *int64) int64 { return Add(ptr, 1) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
	if _, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err == nil || !intrinsic || !strings.Contains(err.Error(), "exact pointer/value/result shape") {
		t.Fatalf("mismatched atomic semantics = _, %v, %v; want exact-shape error", intrinsic, err)
	}
}
