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
	"golang.org/x/tools/go/ssa"
)

func TestEmissionUniverseCompleteRuntimeABIGate(t *testing.T) {
	testProg := newEmissionTestProgram()
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
func Present() {}
`)
	callerPkg := testProg.addPackage(t, "example.com/emission/runtimeabigate", `package runtimeabigate
func Allocate() *int { return new(int) }
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
		t.Fatalf("prepare incomplete/report universe: %v", err)
	}
	if incomplete.CompleteRuntimeABI() {
		t.Fatal("compatibility PrepareEmissionUniverse unexpectedly claims a complete runtime ABI")
	}
	lowered, err := incomplete.CoroLoweredCalls(callerPkg.ssa.Func("Allocate"))
	if err != nil {
		t.Fatal(err)
	}
	if len(lowered) != 0 {
		t.Fatalf("incomplete/report universe lowered calls = %+v; want legacy unresolved runtime markers", lowered)
	}

	_, err = PrepareEmissionUniverseWithOptions(prog, nil, inputs, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err == nil || !strings.Contains(err.Error(), `missing runtime helper "AllocZ"`) {
		t.Fatalf("complete runtime ABI error = %v; want missing AllocZ failure", err)
	}
}

func TestEmissionUniverseCompleteRuntimeABIFreezesExactHelper(t *testing.T) {
	testProg := newEmissionTestProgram()
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
func AllocZ(size uintptr) uintptr { return 0 }
`)
	callerPkg := testProg.addPackage(t, "example.com/emission/runtimeabiexact", `package runtimeabiexact
func Allocate() *int { return new(int) }
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
	if !universe.CompleteRuntimeABI() {
		t.Fatal("complete construction did not retain its runtime ABI contract")
	}
	owner := callerPkg.ssa.Func("Allocate")
	target := runtimePkg.ssa.Func("AllocZ")
	lowered, err := universe.CoroLoweredCalls(owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(lowered) != 1 || lowered[0].LogicalName != "AllocZ" || lowered[0].Target != target {
		t.Fatalf("complete runtime ABI lowered calls = %+v; want exact AllocZ target", lowered)
	}
}

func TestEmissionUniverseFreezesRawEqualityCallbacksAsSynchronousReferences(t *testing.T) {
	testProg := newEmissionTestProgram()
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
func strequal(p, q *byte) bool { return false }
func memequalptr(p, q *byte) bool { return false }
func AllocU(size uintptr) *byte { return nil }
`)
	callerPkg := testProg.addPackage(t, "example.com/emission/rawsyncref", `package rawsyncref
func Box(value string) any { return value }
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
	owner := callerPkg.ssa.Func("Box")
	all, err := universe.CoroDemandReferences(owner)
	if err != nil {
		t.Fatal(err)
	}
	synchronous, err := universe.CoroSyncDemandReferences(owner)
	if err != nil {
		t.Fatal(err)
	}
	target := runtimePkg.ssa.Func("strequal")
	contains := func(values []*ssa.Function, want *ssa.Function) bool {
		for _, value := range values {
			if value == want {
				return true
			}
		}
		return false
	}
	if target == nil || !contains(all, target) || !contains(synchronous, target) {
		t.Fatalf("Box ABI references: all=%v synchronous=%v; want exact strequal in both", all, synchronous)
	}
}

func TestEmissionUniverseCompleteRuntimeABIRequiresRuntimePackage(t *testing.T) {
	testProg := newEmissionTestProgram()
	callerPkg := testProg.addPackage(t, "example.com/emission/runtimeabimissing", `package runtimeabimissing
func Use() {}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	_, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{{
		SSA: callerPkg.ssa, Files: []*ast.File{callerPkg.file},
	}}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err == nil || !strings.Contains(err.Error(), "complete runtime ABI requires package") {
		t.Fatalf("complete runtime ABI without runtime error = %v", err)
	}
}

func TestEmissionUniverseCoroChannelRetainsPlainAndPhysicalHelpers(t *testing.T) {
	testProg := newEmissionTestProgram()
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
func CoroChanTrySend(ch chan int, value *int, size int) bool { return false }
func CoroChanTryRecv(ch chan int, value *int, size int) (bool, bool) { return false, false }
func CoroChanTryClose(ch chan int) uint32 { return 0 }
type ChanOp struct{}
func CoroChanSelectTry(ops ...ChanOp) (int, bool, bool, bool) { return 0, false, false, false }
func CoroChanSelectPark(ops ...ChanOp) {}
func CoroChanSelectResume(ops ...ChanOp) (int, bool, uint32) { return 0, false, 0 }
func ChanSend(ch chan int, value *int, size int) bool { return false }
func ChanRecv(ch chan int, value *int, size int) bool { return false }
func Select(ops ...ChanOp) (int, bool) { return 0, false }
func TrySelect(ops ...ChanOp) (int, bool, bool) { return 0, false, false }
func ChanClose(ch chan int) {}
func AllocU(size uintptr) *byte { return nil }
func Panic(value any) {}
func strequal(left, right string) bool { return false }
func memequalptr(left, right *byte) bool { return false }
`)
	callerPkg := testProg.addPackage(t, "example.com/emission/corochannelhelpers", `package corochannelhelpers
func Send(ch chan int, value int) { ch <- value }
func Recv(ch chan int) int { return <-ch }
func BlockingSelect(first, second chan int, value int) {
	select { case first <- value: case <-second: }
}
func NonblockingSelect(first, second chan int, value int) {
	select { case first <- value: case <-second: default: }
}
func Close(ch chan int) { close(ch) }
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
		{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
		{SSA: callerPkg.ssa, Files: []*ast.File{callerPkg.file}},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true, EnableCoroChannel: true})
	if err != nil {
		t.Fatal(err)
	}

	required := make(map[*ssa.Function]bool)
	for _, fn := range universe.Functions() {
		required[fn] = true
	}
	for _, helper := range []string{
		"CoroChanTrySend", "CoroChanTryRecv", "CoroChanTryClose", "CoroChanSelectTry", "CoroChanSelectPark", "CoroChanSelectResume",
		"ChanSend", "ChanRecv", "ChanClose", "Select", "TrySelect",
	} {
		if fn := runtimePkg.ssa.Func(helper); fn == nil || !required[fn] {
			t.Fatalf("runtime helper %q was not retained for dual channel representations", helper)
		}
	}
	for _, test := range []struct {
		owner string
		want  []string
	}{
		{owner: "Send", want: []string{"CoroChanTrySend"}},
		{owner: "Recv", want: []string{"CoroChanTryRecv"}},
		{owner: "BlockingSelect", want: []string{"CoroChanSelectPark", "CoroChanSelectResume", "CoroChanSelectTry"}},
		{owner: "NonblockingSelect", want: []string{"CoroChanSelectTry"}},
		{owner: "Close", want: []string{"CoroChanTryClose"}},
	} {
		lowered, err := universe.CoroLoweredCalls(callerPkg.ssa.Func(test.owner))
		if err != nil {
			t.Fatal(err)
		}
		if len(lowered) != len(test.want) {
			t.Fatalf("%s physical lowered calls = %+v; want %v", test.owner, lowered, test.want)
		}
		for index, want := range test.want {
			if lowered[index].LogicalName != want {
				t.Fatalf("%s physical lowered calls = %+v; want %v", test.owner, lowered, test.want)
			}
			if !lowered[index].RawPlain {
				t.Fatalf("%s physical lowered call %q is managed; want compiler-owned raw/plain occurrence", test.owner, want)
			}
		}
	}
	if target, ok, err := universe.ResolveCoroPlainLoweredCall(callerPkg.ssa.Func("Close"), "ChanClose"); err != nil || !ok || target != runtimePkg.ssa.Func("ChanClose") {
		t.Fatalf("Close plain lowered call = %v, %t, %v; want exact ChanClose", target, ok, err)
	}
}
