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
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroImplicitNilFaultFixture = `package foo

var Sink uint32

type Box struct { Value uint32 }
type Empty struct{}
type ZeroField struct {
	Value uint32
	Empty struct{}
}

func Cleanup() { Sink++ }
func RecoverFault() { recover() }

func Nullable(box *Box) uint32 { return box.Value }
func EmptyLoad(value *Empty) Empty { return *value }
func ZeroFieldEqual(first, second *ZeroField) bool { return first.Empty == second.Empty }

func InterfaceCompare(value **interface{}) bool { return **value == nil }
func StaticNil() int { return *(*int)(nil) }
func StaticNilFieldLoad() uint32 { var box *Box; return box.Value }
func NullableStore(value *uint32) { *value = 7 }
func StaticNilStore() { *(*uint32)(nil) = 7 }

func Guarded(box *Box) uint32 {
	if box == nil { return 0 }
	return box.Value
}

func WithCleanup(box *Box) {
	defer Cleanup()
	Sink = box.Value
}

func WithRecover(box *Box) {
	defer RecoverFault()
	Sink = box.Value
}

func StringAt(value string, index int) byte { return value[index] }

func ConstantStringAt(index int) byte { return "0123456789abcdef"[index] }

type Array4 [4]uint32

func ArrayAt(values Array4, index int) uint32 { return [4]uint32(values)[index] }

func SliceAt(values []uint32, index int) uint32 { return values[index] }
func SliceAtUnsigned(values []uint32, index uint64) uint32 { return values[index] }
func StaticNilSliceAt() int { var values []int; return values[0] }

func SliceHigh(values []uint32, high int) []uint32 { return values[:high] }
func ArrayHigh(values *Array4, high int) []uint32 { return values[:high] }
func StringHigh(value string, high int) string { return value[:high] }
func SliceLowUnsigned(values []uint32, low uint64, high int) []uint32 { return values[low:high] }
func SliceFullMaxUnsigned(values []uint32, low, high int, max uint64) []uint32 {
	return values[low:high:max]
}
func ArrayFullMax(values *Array4, low, high, max int) []uint32 {
	return values[low:high:max]
}
func SliceFullHigh(values []uint32, high, max int) []uint32 { return values[:high:max] }
func SliceFullLow(values []uint32, low, high, max int) []uint32 { return values[low:high:max] }

func Divide(value, divisor int64) int64 { return value / divisor }
func Remainder(value, divisor int64) int64 { return value % divisor }
func DivideConstantZero() int {
	zero := 0
	return 1 / zero
}
func GuardedDivide(value, divisor int64) int64 {
	if divisor == 0 { return 0 }
	return value / divisor
}
func DivideWithCleanup(value, divisor int64) int64 {
	defer Cleanup()
	return value / divisor
}
func DivideWithRecover(value, divisor int64) (result int64) {
	defer RecoverFault()
	return value / divisor
}

func PointerEqual(first, second *Box) bool { return first == second }

type ValueReceiver struct { Value uint32 }
func (value ValueReceiver) Touch() { Sink += value.Value }
func ValueReceiverCall(value *ValueReceiver) { value.Touch() }

func StaticArrayRange(value *[3]int) {
	for index := range *value { Sink += uint32(index) }
}

func NextArray() *[3]int { return nil }
func EffectfulArrayRange() {
	for index := range *NextArray() { Sink += uint32(index) }
}
`

func TestCoroImplicitNilFieldAddrNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, functions := compileCoroImplicitNilFaultFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify implicit nil fault before CoroSplit: %v\n%s", err, module.String())
			}
			for _, name := range []string{"Nullable", "EmptyLoad", "WithCleanup", "ValueReceiverCall"} {
				function := functions[name]
				functionPlan, ok := plan.FunctionPlan(function)
				if !ok || functionPlan.Emission != coro.EmitCoroutine || !functionPlan.Exec.Contains(coro.MayUnwind) {
					t.Fatalf("%s plan = %+v, present=%t; want may-unwind coroutine", name, functionPlan, ok)
				}
				body := requireCoroPhysicalFunction(t, module, "foo."+name).String()
				wantPrepare, wantPayload, wantWrapPayload, wantPanicPrepare := 1, 0, 0, 0
				if name == "WithCleanup" {
					wantPrepare, wantPayload, wantPanicPrepare = 0, 1, 1
				}
				if got := strings.Count(body, "call void @"+coroFaultPrepareHookV1); got != wantPrepare {
					t.Fatalf("%s nil-fault prepare calls = %d, want %d:\n%s", name, got, wantPrepare, body)
				}
				if got := strings.Count(body, "call void @"+coroFaultPayloadHookV1); got != wantPayload {
					t.Fatalf("%s nil-fault payload calls = %d, want %d:\n%s", name, got, wantPayload, body)
				}
				if got := strings.Count(body, "call void @"+coroWrapNilPayloadHookV1); got != wantWrapPayload {
					t.Fatalf("%s value-method payload calls = %d, want %d:\n%s", name, got, wantWrapPayload, body)
				}
				if got := strings.Count(body, "call void @"+coroPanicPrepareHookV1); got != wantPanicPrepare {
					t.Fatalf("%s explicit panic prepare calls = %d, want %d:\n%s", name, got, wantPanicPrepare, body)
				}
				if !strings.Contains(body, "icmp eq ptr") || strings.Contains(body, "AssertNilDeref") {
					t.Fatalf("%s did not use an inline pointer guard exclusively:\n%s", name, body)
				}
				if name != "WithCleanup" {
					hookName := coroFaultPrepareHookV1
					if hook := strings.Index(body, "call void @"+hookName); hook < 0 ||
						!strings.Contains(body[:hook], "store i16 5") || !strings.Contains(body[:hook], "store i16 4") {
						t.Fatalf("%s did not publish Panic/FinalSuspended before its hook:\n%s", name, body)
					}
				}
			}

			staticRange := requireCoroPhysicalFunction(t, module, "foo.StaticArrayRange").String()
			if strings.Contains(staticRange, coroFaultPrepareHookV1) ||
				strings.Contains(staticRange, "AssertNilDeref") {
				t.Fatalf("static array range evaluated its type-only pointer dereference:\n%s", staticRange)
			}
			effectfulRange := requireCoroPhysicalFunction(t, module, "foo.EffectfulArrayRange").String()
			if strings.Count(effectfulRange, "call void @"+coroFaultPrepareHookV1) != 1 ||
				strings.Contains(effectfulRange, "AssertNilDeref") {
				t.Fatalf("effectful array range lost its structured pointer fault:\n%s", effectfulRange)
			}

			interfaceCompare := functions["InterfaceCompare"]
			interfaceComparePlan, ok := plan.FunctionPlan(interfaceCompare)
			if !ok || interfaceComparePlan.Emission != coro.EmitCoroutine || !interfaceComparePlan.Exec.Contains(coro.MayUnwind) {
				t.Fatalf("InterfaceCompare plan = %+v, present=%t; want may-unwind coroutine", interfaceComparePlan, ok)
			}
			interfaceCompareBody := requireCoroPhysicalFunction(t, module, "foo.InterfaceCompare").String()
			if strings.Contains(interfaceCompareBody, "AssertNilDeref") ||
				strings.Count(interfaceCompareBody, "call void @"+coroFaultPrepareHookV1) != 2 {
				t.Fatalf("nested interface comparison did not use its two structured pointer guards exclusively:\n%s", interfaceCompareBody)
			}
			staticNil := functions["StaticNil"]
			staticNilPlan, ok := plan.FunctionPlan(staticNil)
			if !ok || staticNilPlan.Emission != coro.EmitCoroutine || !staticNilPlan.Exec.Contains(coro.MayUnwind) {
				t.Fatalf("StaticNil plan = %+v, present=%t; want may-unwind coroutine", staticNilPlan, ok)
			}
			staticNilBody := requireCoroPhysicalFunction(t, module, "foo.StaticNil").String()
			if strings.Contains(staticNilBody, "AssertNilDeref") ||
				strings.Count(staticNilBody, "call void @"+coroFaultPrepareHookV1) != 1 {
				t.Fatalf("constant nil dereference did not use its structured pointer guard exclusively:\n%s", staticNilBody)
			}
			staticFieldBody := requireCoroPhysicalFunction(t, module, "foo.StaticNilFieldLoad").String()
			if strings.Contains(staticFieldBody, "AssertNilDeref") ||
				strings.Count(staticFieldBody, "call void @"+coroFaultPrepareHookV1) != 1 {
				t.Fatalf("constant nil field load did not use its structured pointer guard exclusively:\n%s", staticFieldBody)
			}
			for _, name := range []string{"NullableStore", "StaticNilStore"} {
				function := functions[name]
				functionPlan, ok := plan.FunctionPlan(function)
				if !ok || functionPlan.Emission != coro.EmitCoroutine || !functionPlan.Exec.Contains(coro.MayUnwind) {
					t.Fatalf("%s plan = %+v, present=%t; want may-unwind coroutine", name, functionPlan, ok)
				}
				body := requireCoroPhysicalFunction(t, module, "foo."+name).String()
				if strings.Contains(body, "AssertNilDeref") ||
					strings.Count(body, "call void @"+coroFaultPrepareHookV1) != 1 ||
					!strings.Contains(body, "store i32 7") {
					t.Fatalf("%s did not use one structured Store guard followed by the normal-edge store:\n%s", name, body)
				}
			}
			zeroField := functions["ZeroFieldEqual"]
			zeroFieldPlan, ok := plan.FunctionPlan(zeroField)
			if !ok || zeroFieldPlan.Emission != coro.EmitCoroutine || !zeroFieldPlan.Exec.Contains(coro.MayUnwind) {
				t.Fatalf("ZeroFieldEqual plan = %+v, present=%t; want may-unwind coroutine", zeroFieldPlan, ok)
			}
			zeroFieldBody := requireCoroPhysicalFunction(t, module, "foo.ZeroFieldEqual").String()
			if strings.Contains(zeroFieldBody, "AssertNilDeref") ||
				strings.Count(zeroFieldBody, "call void @"+coroFaultPrepareHookV1) != 2 {
				t.Fatalf("zero-sized field comparison did not consume its two checked FieldAddr producers:\n%s", zeroFieldBody)
			}
			guarded := requireCoroPhysicalFunction(t, module, "foo.Guarded").String()
			if strings.Contains(guarded, coroFaultPrepareHookV1) || strings.Contains(guarded, "AssertNilDeref") {
				t.Fatalf("dominated non-nil FieldAddr retained a runtime/terminal guard:\n%s", guarded)
			}
			cleanup := requireCoroPhysicalFunction(t, module, "foo.WithCleanup").String()
			payload := strings.Index(cleanup, "call void @"+coroFaultPayloadHookV1)
			if !strings.Contains(cleanup, "switch i32") || payload < 0 || !strings.Contains(cleanup, "foo.Cleanup") ||
				!strings.Contains(cleanup, "call void @"+coroPanicPrepareHookV1) ||
				strings.Contains(cleanup, "call void @"+coroFaultPrepareHookV1) {
				t.Fatalf("implicit nil fault bypassed the static cleanup dispatcher:\n%s", cleanup)
			}
			recovering := requireCoroPhysicalFunction(t, module, "foo.WithRecover").String()
			if strings.Count(recovering, "call void @"+coroFaultPayloadHookV1) != 1 ||
				strings.Contains(recovering, "call void @"+coroFaultPrepareHookV1) ||
				countCoroIRDirectCalls(requireCoroPhysicalFunction(t, module, "foo.WithRecover"), coroAwaitPrepareHookV1) != 1 ||
				countCoroIRDirectCalls(requireCoroPhysicalFunction(t, module, "foo.RecoverFault"), coroRecoverTakeHookV1) != 1 {
				t.Fatalf("recoverable implicit fault does not use the shared panic/child transaction:\nWithRecover:\n%s\nRecoverFault:\n%s",
					recovering, requireCoroPhysicalFunction(t, module, "foo.RecoverFault").String())
			}

			runCoroABITestPipeline(t, prog, module)
			for _, name := range []string{"Nullable", "EmptyLoad", "WithCleanup", "ValueReceiverCall"} {
				resume := module.NamedFunction("foo." + name + "$coro.resume")
				wantPrepare, wantPayload, wantWrapPayload, wantPanicPrepare := 1, 0, 0, 0
				if name == "WithCleanup" {
					wantPrepare, wantPayload, wantPanicPrepare = 0, 1, 1
				}
				if resume.IsNil() || strings.Count(resume.String(), "call void @"+coroFaultPrepareHookV1) != wantPrepare ||
					strings.Count(resume.String(), "call void @"+coroFaultPayloadHookV1) != wantPayload ||
					strings.Count(resume.String(), "call void @"+coroWrapNilPayloadHookV1) != wantWrapPayload ||
					strings.Count(resume.String(), "call void @"+coroPanicPrepareHookV1) != wantPanicPrepare {
					t.Fatalf("post-split %s resume lost its nil-fault edge:\n%s", name, module.String())
				}
			}
			withRecover := module.NamedFunction("foo.WithRecover$coro.resume")
			recoverFault := module.NamedFunction("foo.RecoverFault$coro.resume")
			if withRecover.IsNil() || recoverFault.IsNil() ||
				strings.Count(withRecover.String(), "call void @"+coroFaultPayloadHookV1) != 1 ||
				countCoroIRDirectCalls(withRecover, coroAwaitPrepareHookV1) != 1 ||
				countCoroIRDirectCalls(recoverFault, coroRecoverTakeHookV1) != 1 {
				t.Fatalf("post-split recoverable implicit fault lost its payload/recover transaction:\n%s", module.String())
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit implicit nil-fault object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(coroFaultPrepareHookV1)) ||
				!bytes.Contains(object.Bytes(), []byte(coroFaultPayloadHookV1)) {
				t.Fatal("post-CoroSplit object lost a nil-fault hook")
			}
		})
	}
}

func TestCoroIntegerDivideByZeroUsesStructuredFaultNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, functions := compileCoroImplicitNilFaultFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify structured integer division before CoroSplit: %v\n%s", err, module.String())
			}
			for _, name := range []string{"Divide", "Remainder", "DivideConstantZero"} {
				functionPlan, ok := plan.FunctionPlan(functions[name])
				if !ok || functionPlan.Emission != coro.EmitCoroutine ||
					!functionPlan.Exec.Contains(coro.MayUnwind) {
					t.Fatalf("%s plan = %+v, present=%t; want may-unwind coroutine", name, functionPlan, ok)
				}
				body := requireCoroPhysicalFunction(t, module, "foo."+name).String()
				if strings.Contains(body, "AssertDivideByZero") ||
					strings.Contains(body, "call void @"+coroAwaitPrepareHookV1) ||
					strings.Count(body, "call void @"+coroFaultPrepareHookV1) != 1 ||
					!strings.Contains(body, fmt.Sprintf("i32 %d", coroFaultIntegerDivideByZeroV1)) {
					t.Fatalf("%s did not exclusively use one structured divide-by-zero fault:\n%s", name, body)
				}
			}

			guarded := requireCoroPhysicalFunction(t, module, "foo.GuardedDivide").String()
			if strings.Contains(guarded, "AssertDivideByZero") ||
				strings.Contains(guarded, coroFaultPrepareHookV1) {
				t.Fatalf("dominated non-zero division retained a helper or fault edge:\n%s", guarded)
			}

			for _, name := range []string{"DivideWithCleanup", "DivideWithRecover"} {
				body := requireCoroPhysicalFunction(t, module, "foo."+name).String()
				if strings.Contains(body, "AssertDivideByZero") ||
					strings.Contains(body, "call void @"+coroFaultPrepareHookV1) ||
					strings.Count(body, "call void @"+coroFaultPayloadHookV1) != 1 ||
					!strings.Contains(body, fmt.Sprintf("i32 %d", coroFaultIntegerDivideByZeroV1)) {
					t.Fatalf("%s did not route divide-by-zero through cleanup/recover payload:\n%s", name, body)
				}
			}

			runCoroABITestPipeline(t, prog, module)
			for _, name := range []string{"Divide", "Remainder", "DivideConstantZero"} {
				resume := module.NamedFunction("foo." + name + "$coro.resume")
				if resume.IsNil() || strings.Contains(resume.String(), "AssertDivideByZero") ||
					strings.Count(resume.String(), "call void @"+coroFaultPrepareHookV1) != 1 {
					t.Fatalf("post-split %s lost its structured divide-by-zero fault:\n%s", name, module.String())
				}
			}
		})
	}
}

func TestCoroImplicitIndexAddrBoundsNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, functions := compileCoroImplicitNilFaultFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			function := functions["SliceAt"]
			functionPlan, ok := plan.FunctionPlan(function)
			if !ok || functionPlan.Emission != coro.EmitCoroutine || !functionPlan.Exec.Contains(coro.MayUnwind) {
				t.Fatalf("SliceAt plan = %+v, present=%t; want may-unwind coroutine", functionPlan, ok)
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify structured IndexAddr before CoroSplit: %v\n%s", err, module.String())
			}
			body := requireCoroPhysicalFunction(t, module, "foo.SliceAt").String()
			if got := strings.Count(body, "call void @"+coroFaultPrepareHookV2); got != 1 {
				t.Fatalf("SliceAt fault prepare calls = %d, want one:\n%s", got, body)
			}
			if strings.Contains(body, "CheckIndexRange") || strings.Contains(body, "AssertIndexRange") {
				t.Fatalf("SliceAt retained a native-stack bounds helper:\n%s", body)
			}
			hook := strings.Index(body, "call void @"+coroFaultPrepareHookV2)
			if hook < 0 || !strings.Contains(body[hook:], fmt.Sprintf("i32 %d", coroBoundsFaultKind(coroBoundsFaultIndex, true))) ||
				!strings.Contains(body[hook:], "i64 ") {
				t.Fatalf("SliceAt did not select the index-bounds fault kind:\n%s", body)
			}
			gep := strings.Index(body, "getelementptr inbounds i32")
			if gep < 0 || hook > gep {
				t.Fatalf("SliceAt formed its element address before the terminal bounds edge:\n%s", body)
			}
			staticBody := requireCoroPhysicalFunction(t, module, "foo.StaticNilSliceAt").String()
			if got := strings.Count(staticBody, "call void @"+coroFaultPrepareHookV2); got != 1 ||
				strings.Contains(staticBody, "AssertNilDeref") {
				t.Fatalf("StaticNilSliceAt did not route its sole failure through the structured bounds edge:\n%s", staticBody)
			}

			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.SliceAt$coro.resume")
			if resume.IsNil() || strings.Count(resume.String(), "call void @"+coroFaultPrepareHookV2) != 1 {
				t.Fatalf("post-split SliceAt resume lost its bounds-fault edge:\n%s", module.String())
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit structured IndexAddr object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(coroFaultPrepareHookV2)) {
				t.Fatal("post-CoroSplit object lost the bounds-fault hook")
			}
		})
	}
}

func TestCoroExactBoundsFaultKindsNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			prog, pkg, plan, functions := compileCoroImplicitNilFaultFixture(t, target.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			tests := []struct {
				name  string
				kinds []uint32
			}{
				{
					name: "SliceAtUnsigned",
					kinds: []uint32{
						coroBoundsFaultKind(coroBoundsFaultIndex, false),
					},
				},
				{
					name: "SliceHigh",
					kinds: []uint32{
						coroBoundsFaultKind(coroBoundsFaultSliceAcap, true),
						coroBoundsFaultKind(coroBoundsFaultSliceB, true),
					},
				},
				{
					name: "ArrayHigh",
					kinds: []uint32{
						coroBoundsFaultKind(coroBoundsFaultSliceAlen, true),
						coroBoundsFaultKind(coroBoundsFaultSliceB, true),
					},
				},
				{
					name: "StringHigh",
					kinds: []uint32{
						coroBoundsFaultKind(coroBoundsFaultSliceAlen, true),
						coroBoundsFaultKind(coroBoundsFaultSliceB, true),
					},
				},
				{
					name: "SliceLowUnsigned",
					kinds: []uint32{
						coroBoundsFaultKind(coroBoundsFaultSliceAcap, true),
						coroBoundsFaultKind(coroBoundsFaultSliceB, false),
					},
				},
				{
					name: "SliceFullMaxUnsigned",
					kinds: []uint32{
						coroBoundsFaultKind(coroBoundsFaultSlice3Acap, false),
						coroBoundsFaultKind(coroBoundsFaultSlice3B, true),
						coroBoundsFaultKind(coroBoundsFaultSlice3C, true),
					},
				},
				{
					name: "ArrayFullMax",
					kinds: []uint32{
						coroBoundsFaultKind(coroBoundsFaultSlice3Alen, true),
						coroBoundsFaultKind(coroBoundsFaultSlice3B, true),
						coroBoundsFaultKind(coroBoundsFaultSlice3C, true),
					},
				},
				{
					name: "SliceFullHigh",
					kinds: []uint32{
						coroBoundsFaultKind(coroBoundsFaultSlice3Acap, true),
						coroBoundsFaultKind(coroBoundsFaultSlice3B, true),
						coroBoundsFaultKind(coroBoundsFaultSlice3C, true),
					},
				},
				{
					name: "SliceFullLow",
					kinds: []uint32{
						coroBoundsFaultKind(coroBoundsFaultSlice3Acap, true),
						coroBoundsFaultKind(coroBoundsFaultSlice3B, true),
						coroBoundsFaultKind(coroBoundsFaultSlice3C, true),
					},
				},
			}
			for _, test := range tests {
				functionPlan, ok := plan.FunctionPlan(functions[test.name])
				if !ok || functionPlan.Emission != coro.EmitCoroutine ||
					!functionPlan.Exec.Contains(coro.MayUnwind) {
					t.Fatalf("%s plan = %+v, present=%t; want may-unwind coroutine",
						test.name, functionPlan, ok)
				}
				body := requireCoroPhysicalFunction(t, module, "foo."+test.name).String()
				requireCoroParameterizedBoundsFaultKinds(t, body, test.kinds...)
			}

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify exact bounds faults before CoroSplit: %v\n%s", err, module.String())
			}
			runCoroABITestPipeline(t, prog, module)
			for _, test := range tests {
				resume := module.NamedFunction("foo." + test.name + "$coro.resume")
				if resume.IsNil() {
					t.Fatalf("post-split %s resume is missing", test.name)
				}
				requireCoroParameterizedBoundsFaultKinds(t, resume.String(), test.kinds...)
			}
		})
	}
}

func requireCoroParameterizedBoundsFaultKinds(t *testing.T, body string, kinds ...uint32) {
	t.Helper()
	var calls []string
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "call void @"+coroFaultPrepareHookV2) {
			calls = append(calls, line)
		}
	}
	if len(calls) != len(kinds) {
		t.Fatalf("parameterized bounds calls = %d, want %d:\n%s", len(calls), len(kinds), body)
	}
	for i, kind := range kinds {
		if !strings.Contains(calls[i], fmt.Sprintf("i32 %d", kind)) ||
			!strings.Contains(calls[i], "i64 ") {
			t.Fatalf("parameterized bounds call %d = %q, want kind %d and i64 x",
				i, calls[i], kind)
		}
	}
}

func TestCoroPurePointerEqualityNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, functions := compileCoroImplicitNilFaultFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			function := functions["PointerEqual"]
			functionPlan, ok := plan.FunctionPlan(function)
			if !ok || functionPlan.Emission != coro.EmitCoroutine {
				t.Fatalf("PointerEqual plan = %+v, present=%t; want coroutine", functionPlan, ok)
			}
			body := requireCoroPhysicalFunction(t, module, "foo.PointerEqual").String()
			if !strings.Contains(body, "icmp eq ptr") || strings.Contains(body, coroFaultPrepareHookV1) {
				t.Fatalf("pointer equality did not remain one direct non-faulting comparison:\n%s", body)
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify pointer equality before CoroSplit: %v\n%s", err, module.String())
			}
			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.PointerEqual$coro.resume")
			if resume.IsNil() || !strings.Contains(resume.String(), "icmp eq ptr") {
				t.Fatalf("post-split pointer equality lost its direct comparison:\n%s", module.String())
			}
		})
	}
}

func TestCoroImplicitNilFieldAddrProofSeparatesRootFromAccess(t *testing.T) {
	prog, _, _, root, audit, proof := prepareCoroFrameRootAudit(t, `package foo
type Box struct { Value uint32 }
func (box *Box) Root() uint32 { return box.Value }
`, "Root", EmissionUniverseOptions{})
	defer prog.Dispose()

	var field *ssa.FieldAddr
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if candidate, ok := instruction.(*ssa.FieldAddr); ok {
				field = candidate
			}
		}
	}
	if field == nil {
		t.Fatal("fixture has no FieldAddr")
	}
	if !proof.provesGuardableStableAddress(field, field) || proof.provesDominatedStableAddress(field, field) {
		t.Fatal("nullable FieldAddr did not retain separate transport/nonnull facts")
	}
	if roots := rootNames(proof.exactRetainedRoots()); len(roots) != 1 || roots[0] != "box" {
		t.Fatalf("nullable receiver is not the sole exact retained root: %v", roots)
	}
	if len(root.Params) != 1 || proof.exactRoots[root.Params[0]].kind != coroFrameRetentionRootReceiver {
		t.Fatalf("nullable method parameter was not classified as the receiver root: %+v", proof.exactRoots)
	}
	if reason := audit.validateFieldAddr(field); !strings.Contains(reason, "non-nil") {
		t.Fatalf("legacy audit accepted nullable FieldAddr or changed fail-closed reason: %q", reason)
	}
	audit.allowImplicitNilFault = true
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if handled, reason := audit.validate(instruction); handled && reason != "" {
				t.Fatalf("explicit-status instruction %T %q rejected: %s", instruction, instruction, reason)
			}
		}
	}
}

func compileCoroImplicitNilFaultFixture(
	t *testing.T,
	target *llssa.Target,
) (llssa.Program, llssa.Package, *coro.SSAPlan, map[string]*ssa.Function) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroImplicitNilFaultFixture)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	functions := map[string]*ssa.Function{
		"Nullable":             ssaPkg.Func("Nullable"),
		"EmptyLoad":            ssaPkg.Func("EmptyLoad"),
		"InterfaceCompare":     ssaPkg.Func("InterfaceCompare"),
		"StaticNil":            ssaPkg.Func("StaticNil"),
		"StaticNilFieldLoad":   ssaPkg.Func("StaticNilFieldLoad"),
		"NullableStore":        ssaPkg.Func("NullableStore"),
		"StaticNilStore":       ssaPkg.Func("StaticNilStore"),
		"ZeroFieldEqual":       ssaPkg.Func("ZeroFieldEqual"),
		"Guarded":              ssaPkg.Func("Guarded"),
		"WithCleanup":          ssaPkg.Func("WithCleanup"),
		"RecoverFault":         ssaPkg.Func("RecoverFault"),
		"WithRecover":          ssaPkg.Func("WithRecover"),
		"StringAt":             ssaPkg.Func("StringAt"),
		"ConstantStringAt":     ssaPkg.Func("ConstantStringAt"),
		"ArrayAt":              ssaPkg.Func("ArrayAt"),
		"SliceAt":              ssaPkg.Func("SliceAt"),
		"SliceAtUnsigned":      ssaPkg.Func("SliceAtUnsigned"),
		"StaticNilSliceAt":     ssaPkg.Func("StaticNilSliceAt"),
		"SliceHigh":            ssaPkg.Func("SliceHigh"),
		"ArrayHigh":            ssaPkg.Func("ArrayHigh"),
		"StringHigh":           ssaPkg.Func("StringHigh"),
		"SliceLowUnsigned":     ssaPkg.Func("SliceLowUnsigned"),
		"SliceFullMaxUnsigned": ssaPkg.Func("SliceFullMaxUnsigned"),
		"ArrayFullMax":         ssaPkg.Func("ArrayFullMax"),
		"SliceFullHigh":        ssaPkg.Func("SliceFullHigh"),
		"SliceFullLow":         ssaPkg.Func("SliceFullLow"),
		"Divide":               ssaPkg.Func("Divide"),
		"Remainder":            ssaPkg.Func("Remainder"),
		"DivideConstantZero":   ssaPkg.Func("DivideConstantZero"),
		"GuardedDivide":        ssaPkg.Func("GuardedDivide"),
		"DivideWithCleanup":    ssaPkg.Func("DivideWithCleanup"),
		"DivideWithRecover":    ssaPkg.Func("DivideWithRecover"),
		"PointerEqual":         ssaPkg.Func("PointerEqual"),
		"ValueReceiverCall":    ssaPkg.Func("ValueReceiverCall"),
		"StaticArrayRange":     ssaPkg.Func("StaticArrayRange"),
		"NextArray":            ssaPkg.Func("NextArray"),
		"EffectfulArrayRange":  ssaPkg.Func("EffectfulArrayRange"),
	}
	roots := make(coro.Roots, 0, len(functions))
	for _, function := range functions {
		roots = append(roots, coro.Root{Function: function, Demand: coro.AsyncDemand})
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, roots, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			for _, root := range functions {
				if function == root {
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
				}
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, functions
}
