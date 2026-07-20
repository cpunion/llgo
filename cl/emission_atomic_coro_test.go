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
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
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

func TestAtomicIntrinsicAcceptsRuntimeSignedDeltaAndDiscardedRMWResult(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/runtimeatomicshape", `package runtimeatomicshape
type Word uint32
//llgo:link Add llgo.atomicAddReturnNew
func Add(ptr *Word, delta int32) Word { return 0 }
//llgo:link And llgo.atomicAnd
func And(ptr *Word, value Word) {}
func Use(ptr *Word) Word {
	And(ptr, 7)
	return Add(ptr, -1)
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
	if len(calls) != 2 {
		t.Fatalf("runtime atomic-shape calls = %d, want two", len(calls))
	}
	for _, call := range calls {
		semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
		if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
			t.Fatalf("runtime atomic-shape call %q semantics = %v, %v, %v; want inline-no-suspend, true, nil", call, semantics, intrinsic, err)
		}
	}
}

func TestAtomicIntrinsicRejectsUnrelatedSignednessAndDiscardedResults(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/runtimeatomicnarrow", `package runtimeatomicnarrow
//llgo:link Load llgo.atomicLoad
func Load(ptr *uint32) int32 { return 0 }
//llgo:link Store llgo.atomicStore
func Store(ptr *uint32, value int32) {}
//llgo:link Swap llgo.atomicXchg
func Swap(ptr *uint32, value uint32) {}
type Word uint32
type Delta uint32
//llgo:link Add llgo.atomicAddReturnNew
func Add(ptr *Word, delta Delta) Word { return 0 }
func Use(ptr *uint32, word *Word) {
	_ = Load(ptr)
	Store(ptr, 1)
	Swap(ptr, 1)
	_ = Add(word, 1)
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
		t.Fatalf("narrow atomic calls = %d, want four", len(calls))
	}
	for _, call := range calls {
		if _, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err == nil || !intrinsic || !strings.Contains(err.Error(), "exact pointer/value/result shape") {
			t.Errorf("over-broad atomic semantics for %q = _, %v, %v; want exact-shape error", call, intrinsic, err)
		}
	}
}

func TestRawAddressAtomicIntrinsicRequiresExactUnsafePointerShape(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	pkg := testProg.addPackage(t, "example.com/emission/rawaddressatomicshape", `package rawaddressatomicshape
import "unsafe"
type Pointer unsafe.Pointer
//llgo:link Loadp llgo.atomicLoadUnsafe
func Loadp(ptr Pointer) Pointer { return nil }
func Use(ptr Pointer) Pointer { return Loadp(ptr) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	call := allocaCStrTestCalls(pkg.ssa.Func("Use"))[0]
	if _, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call); err == nil || !intrinsic || !strings.Contains(err.Error(), "exact unsafe.Pointer shape") {
		t.Fatalf("raw-address atomic semantics = _, %v, %v; want exact unsafe.Pointer error", intrinsic, err)
	}
}

func TestGoRuntimeAtomicSymbolsUsePortableIntrinsics(t *testing.T) {
	want := map[string]string{
		"internal/runtime/atomic.Casint32":   "atomicCmpXchgOK",
		"internal/runtime/atomic.Loadint32":  "atomicLoad",
		"internal/runtime/atomic.Loadp":      "atomicLoadUnsafe",
		"internal/runtime/atomic.Storeint32": "atomicStore",
		"internal/runtime/atomic.StorepNoWB": "atomicStoreUnsafe",
		"internal/runtime/atomic.Xadd":       "atomicAddReturnNew",
		"internal/runtime/atomic.And":        "atomicAnd",
	}
	for symbol, intrinsic := range want {
		if got := stdlibAtomicIntrinsicMap[symbol]; got != intrinsic {
			t.Errorf("stdlib atomic symbol %q = %q, want %q", symbol, got, intrinsic)
		}
	}
}

func TestGoRuntimeAtomicMappingRequiresBodylessCapableTarget(t *testing.T) {
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	pkg := testProg.addPackage(t, "internal/runtime/atomic", `package atomic
import "unsafe"
func Casint32(ptr *int32, old, new int32) bool
func Loadint32(ptr *int32) int32 { return *ptr }
func Loadp(ptr unsafe.Pointer) unsafe.Pointer
func StorepNoWB(ptr unsafe.Pointer, value unsafe.Pointer)
`)
	testProg.ssa.Build()
	bodyless := pkg.ssa.Func("Casint32")
	bodyful := pkg.ssa.Func("Loadint32")
	loadp := pkg.ssa.Func("Loadp")
	storep := pkg.ssa.Func("StorepNoWB")

	native := &llssa.Target{GOOS: "linux", GOARCH: "amd64"}
	wasm := &llssa.Target{GOOS: "js", GOARCH: "wasm"}
	baremetal := &llssa.Target{GOOS: "none", GOARCH: "arm", Target: "cortex-m"}
	baremetalWithoutNamedTarget := &llssa.Target{GOOS: "baremetal", GOARCH: "arm"}

	for _, test := range []struct {
		name         string
		functionName string
		target       *llssa.Target
		features     string
		want         string
		ok           bool
	}{
		{name: "native bodyless", functionName: "Casint32", target: native, want: "atomicCmpXchgOK", ok: true},
		{name: "native bodyful", functionName: "Loadint32", target: native},
		{name: "default wasm bodyless", functionName: "Casint32", target: wasm},
		{name: "wasm bodyful fallback", functionName: "Loadint32", target: wasm, features: "+atomics"},
		{name: "wasm explicit atomics", functionName: "Casint32", target: wasm, features: "+bulk-memory,+atomics", want: "atomicCmpXchgOK", ok: true},
		{name: "wasm feature disabled last", functionName: "Casint32", target: wasm, features: "+atomics,-atomics"},
		{name: "named baremetal", functionName: "Casint32", target: baremetal},
		{name: "baremetal bodyful fallback", functionName: "Loadint32", target: baremetal},
		{name: "bodyful fallback without named target", functionName: "Loadint32", target: baremetalWithoutNamedTarget},
		{name: "native raw load", functionName: "Loadp", target: native, want: "atomicLoadUnsafe", ok: true},
		{name: "native raw store", functionName: "StorepNoWB", target: native, want: "atomicStoreUnsafe", ok: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			functions := map[string]*ssa.Function{
				"Casint32":   bodyless,
				"Loadint32":  bodyful,
				"Loadp":      loadp,
				"StorepNoWB": storep,
			}
			fn := functions[test.functionName]
			got, ok := stdlibAtomicIntrinsic(fn, "internal/runtime/atomic."+test.functionName, test.target, test.features)
			if got != test.want || ok != test.ok {
				t.Fatalf("mapping = %q, %v; want %q, %v", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestRuntimeAtomicDedicatedLoweringIR(t *testing.T) {
	_, module := mustCompileLLPkgFromSrc(t, `package foo
import "unsafe"
type Word uint32
//llgo:link Add llgo.atomicAddReturnNew
func Add(ptr *Word, delta int32) Word { return 0 }
//llgo:link And llgo.atomicAnd
func And(ptr *Word, value Word) {}
//llgo:link Or llgo.atomicOr
func Or(ptr *Word, value Word) {}
//llgo:link Cas llgo.atomicCmpXchgOK
func Cas(ptr *Word, old, new Word) bool { return false }
//llgo:link Loadp llgo.atomicLoadUnsafe
func Loadp(ptr unsafe.Pointer) unsafe.Pointer { return nil }
//llgo:link Storep llgo.atomicStoreUnsafe
func Storep(ptr unsafe.Pointer, value unsafe.Pointer) {}
func Use(ptr *Word, slot unsafe.Pointer, value unsafe.Pointer) (Word, unsafe.Pointer, bool) {
	And(ptr, 7)
	Or(ptr, 8)
	Storep(slot, value)
	return Add(ptr, -1), Loadp(slot), Cas(ptr, 1, 2)
}
`)
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify dedicated runtime atomic IR: %v\n%s", err, module.String())
	}
	ir := module.String()
	for _, want := range []string{
		"atomicrmw and ptr",
		"atomicrmw or ptr",
		"atomicrmw add ptr",
		"cmpxchg ptr",
		"store atomic ptr",
		"load atomic ptr",
	} {
		if !strings.Contains(ir, want) {
			t.Errorf("compiled IR missing %q:\n%s", want, ir)
		}
	}
	if strings.Count(ir, "atomicrmw and ptr") != 1 || strings.Count(ir, "atomicrmw or ptr") != 1 {
		t.Fatalf("void And/Or should each lower exactly once:\n%s", ir)
	}
}
