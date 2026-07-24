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

package coro

import (
	"go/constant"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestAnalyzeSSATrustedNoUnwindTrustsOnlyLocalImplicitFaults(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "trusted_no_unwind.go", `package coroid
func load(value *int) int { return *value }
func caller(value *int) int { return load(value) }
func explicitPanic() { panic("boom") }
func openCall(fn func()) { fn() }
`)
	load := packageFunction(t, pkg, "load")
	caller := packageFunction(t, pkg, "caller")
	explicitPanic := packageFunction(t, pkg, "explicitPanic")
	openCall := packageFunction(t, pkg, "openCall")
	plan, err := AnalyzeSSA(prog, Roots{
		{Function: caller, Demand: SyncDemand},
		{Function: explicitPanic, Demand: SyncDemand},
		{Function: openCall, Demand: SyncDemand},
	}, SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			switch fn {
			case load, explicitPanic, openCall:
				return SSAFunctionPolicy{TrustedNoUnwind: true}, nil
			default:
				return SSAFunctionPolicy{}, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []*ssa.Function{load, caller} {
		if got := functionPlanFor(t, plan, fn); got.Exec.Contains(MayUnwind) {
			t.Fatalf("%s trusted no-unwind closure remains may-unwind: %+v", fn.Name(), got)
		}
	}
	for _, fn := range []*ssa.Function{explicitPanic, openCall} {
		if got := functionPlanFor(t, plan, fn); !got.Exec.Contains(MayUnwind) {
			t.Fatalf("%s explicit/open unwind was hidden: %+v", fn.Name(), got)
		}
	}
}

func TestAnalyzeSSATrustedNoUnwindUsesExactNonNilStaticCallArguments(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "trusted_no_unwind_call_argument.go", `package coroid
type box struct { value int }
var global box
func load(value *box) int { return value.value }
func exact() int { return load(&global) }
func unknown(value *box) int { return load(value) }
func panicLoad(value *box) int {
	if value != nil { panic("boom") }
	return 0
}
func exactPanic() int { return panicLoad(&global) }
`)
	load := packageFunction(t, pkg, "load")
	exact := packageFunction(t, pkg, "exact")
	unknown := packageFunction(t, pkg, "unknown")
	exactPanic := packageFunction(t, pkg, "exactPanic")
	plan, err := AnalyzeSSA(prog, Roots{
		{Function: exact, Demand: SyncDemand},
		{Function: unknown, Demand: SyncDemand},
		{Function: exactPanic, Demand: SyncDemand},
	}, SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			switch fn {
			case exact, unknown, exactPanic:
				return SSAFunctionPolicy{TrustedNoUnwind: true}, nil
			default:
				return SSAFunctionPolicy{}, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, load); !got.Exec.Contains(MayUnwind) {
		t.Fatalf("globally nullable load unexpectedly became no-unwind: %+v", got)
	}
	if got := functionPlanFor(t, plan, exact); got.Exec.Contains(MayUnwind) {
		t.Fatalf("exact non-nil static call remains may-unwind: %+v", got)
	}
	for _, fn := range []*ssa.Function{unknown, exactPanic} {
		if got := functionPlanFor(t, plan, fn); !got.Exec.Contains(MayUnwind) {
			t.Fatalf("%s call-site proof hid a nullable receiver or explicit panic: %+v", fn.Name(), got)
		}
	}
}

func TestAnalyzeSSAExactNoUnwindTLSRecipeFixedPoint(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "no_unwind_tls.go", `package coroid
import "unsafe"

type slot struct {
	destructor func(*int)
	value int
	state uintptr
}

func foreignLeaf(unsafe.Pointer)

func rootRange(s *slot) (unsafe.Pointer, unsafe.Pointer) {
	begin := unsafe.Pointer(s)
	size := unsafe.Sizeof(*s)
	return begin, unsafe.Add(begin, size)
}

func deregisterSlot(s *slot) {
	if s == nil || s.state&1 == 0 { return }
	s.state &^= 1
	start, end := rootRange(s)
	if uintptr(end) > uintptr(start) { foreignLeaf(start) }
}

func slotDestructor(s *slot) {
	if s == nil { return }
	if s.destructor != nil { s.destructor(&s.value) }
	deregisterSlot(s)
	s.value = 0
	s.destructor = nil
	foreignLeaf(unsafe.Pointer(s))
}
`)
	rootRange := packageFunction(t, pkg, "rootRange")
	deregisterSlot := packageFunction(t, pkg, "deregisterSlot")
	slotDestructor := packageFunction(t, pkg, "slotDestructor")
	foreignLeaf := packageFunction(t, pkg, "foreignLeaf")
	dynamicCall := onlyNonBuiltinCallWithoutStaticCallee(t, slotDestructor)

	plan, err := AnalyzeSSA(prog, Roots{{Function: slotDestructor, Demand: SyncDemand}}, SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == foreignLeaf {
				return SSAFunctionPolicy{
					Effect:                    NoSuspend,
					Exec:                      IRQUnsafe,
					ForeignNoBlockCertificate: "test:foreign-leaf:v1",
					IgnoreBody:                true,
					External:                  ExternalKnown,
					OverrideExternal:          true,
				}, nil
			}
			return SSAFunctionPolicy{}, nil
		},
		ClassifyClosedDynamicCall: func(_ *ssa.Function, call ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error) {
			if call == dynamicCall {
				// The frozen field-flow proof establishes that the field is nil.
				// The dynamic call is therefore safe only inside its non-nil
				// (unreachable) branch.
				return SSAClosedDynamicCallCertificate{MayBeNil: true}, true, nil
			}
			return SSAClosedDynamicCallCertificate{}, false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []*ssa.Function{rootRange, deregisterSlot, slotDestructor} {
		if got := functionPlanFor(t, plan, fn); got.Exec.Contains(MayUnwind) {
			t.Fatalf("%s exact recipe remains may-unwind: %+v", fn.Name(), got)
		}
	}
}

func TestAnalyzeSSAExactNoUnwindFailsClosed(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "no_unwind_negative.go", `package coroid
import "unsafe"

func empty() {}
func unguardedLoad(p *int) int { return *p }
func guardedLoad(p *int) int { if p != nil { return *p }; return 0 }
func fieldValue(v struct{ value int }) int { return v.value }
func integerArithmetic(a, b uintptr) uintptr { return (a + 1) * b - 2 }
func constantShift(a uint64) uint64 { return a >> 32 }
func unsignedShift(a uint64, count uint) uint64 { return a << count }
func signedShift(a uint64, count int) uint64 { return a << count }
func stringConcat(a, b string) string { return a + b }
func divide(a, b int) int { return a / b }
func remainder(a, b int) int { return a % b }
func divideConstant(a int) int { return a / 16 }
func remainderConstant(a uintptr) uintptr { return a % 16 }
func panicBody() { panic("boom") }
func openDynamic(fn func()) { fn() }
func certifiedUnguarded(fn func()) { fn() }
func certifiedGuarded(fn func()) { if fn != nil { fn() } }
func index(values []int, i int) int { return values[i] }
func allocate() *int { return new(int) }
func sizeofAndUse(p *int) int { value := *p; _ = unsafe.Sizeof(value); return value }
func rootRange(p *int) int { return *p }
func safeLeaf(p *int) { if p != nil { *p = 0 } }
func safeCaller(p *int) { safeLeaf(p) }
func unsafeCaller(a, b int) int { return divide(a, b) }
func knownExternal()
func unknownExternal()
func knownExternalCaller(p *int) { if p != nil { knownExternal() } }
func unknownExternalCaller(p *int) { if p != nil { unknownExternal() } }
`)
	certifiedUnguarded := packageFunction(t, pkg, "certifiedUnguarded")
	certifiedGuarded := packageFunction(t, pkg, "certifiedGuarded")
	unguardedCall := onlyNonBuiltinCallWithoutStaticCallee(t, certifiedUnguarded)
	guardedCall := onlyNonBuiltinCallWithoutStaticCallee(t, certifiedGuarded)
	functions := []string{
		"empty", "unguardedLoad", "guardedLoad", "divide", "remainder", "panicBody",
		"openDynamic", "certifiedUnguarded", "certifiedGuarded", "index",
		"allocate", "sizeofAndUse", "rootRange", "safeLeaf", "safeCaller", "unsafeCaller",
		"knownExternalCaller", "unknownExternalCaller", "fieldValue",
		"integerArithmetic", "constantShift", "unsignedShift", "signedShift", "stringConcat",
		"divideConstant", "remainderConstant",
	}
	roots := make(Roots, 0, len(functions))
	for _, name := range functions {
		roots = append(roots, Root{Function: packageFunction(t, pkg, name), Demand: SyncDemand})
	}
	plan, err := AnalyzeSSA(prog, roots, SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			switch fn.Name() {
			case "knownExternal":
				return SSAFunctionPolicy{
					Effect: NoSuspend, Exec: IRQUnsafe, IgnoreBody: true,
					External: ExternalKnown, OverrideExternal: true,
				}, nil
			case "unknownExternal":
				return SSAFunctionPolicy{
					IgnoreBody: true, External: ExternalUnknownForeign, OverrideExternal: true,
				}, nil
			default:
				return SSAFunctionPolicy{}, nil
			}
		},
		ClassifyClosedDynamicCall: func(_ *ssa.Function, call ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error) {
			switch call {
			case unguardedCall, guardedCall:
				return SSAClosedDynamicCallCertificate{MayBeNil: true}, true, nil
			default:
				return SSAClosedDynamicCallCertificate{}, false, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"unguardedLoad", "divide", "remainder", "signedShift", "panicBody", "openDynamic",
		"certifiedUnguarded", "index", "allocate", "sizeofAndUse", "rootRange", "unsafeCaller",
		"unknownExternalCaller",
		"stringConcat",
	} {
		if got := functionPlanFor(t, plan, packageFunction(t, pkg, name)); !got.Exec.Contains(MayUnwind) {
			t.Errorf("%s unexpectedly proved no-unwind: %+v", name, got)
		}
	}
	for _, name := range []string{
		"empty", "guardedLoad", "certifiedGuarded", "safeLeaf", "safeCaller", "knownExternalCaller", "fieldValue",
		"integerArithmetic", "constantShift", "unsignedShift",
		"divideConstant", "remainderConstant",
	} {
		if got := functionPlanFor(t, plan, packageFunction(t, pkg, name)); got.Exec.Contains(MayUnwind) {
			t.Errorf("%s exact safe recipe remains may-unwind: %+v", name, got)
		}
	}
}

func TestNoUnwindIntegerDivisorRequiresExactNonZeroConstant(t *testing.T) {
	if !noUnwindNonZeroIntegerConstant(ssa.NewConst(constant.MakeInt64(-1), types.Typ[types.Int])) {
		t.Fatal("exact non-zero integer constant was not proved safe")
	}
	if noUnwindNonZeroIntegerConstant(ssa.NewConst(constant.MakeInt64(0), types.Typ[types.Int])) {
		t.Fatal("zero integer divisor unexpectedly proved safe")
	}
}

func TestNoUnwindShiftCountRejectsNegativeConstant(t *testing.T) {
	negative := ssa.NewConst(constant.MakeInt64(-1), types.Typ[types.UntypedInt])
	if noUnwindShiftCount(negative) {
		t.Fatal("negative constant shift count unexpectedly proved no-unwind")
	}
}

func TestSSAExactNoUnwindAcceptsGenericMapLenWithoutTrustingTypeParameters(t *testing.T) {
	_, pkg := buildCoroTestSSA(t, "no_unwind_generic_len.go", `package coroid
func MapLen[K comparable, V any](values map[K]V) int { return len(values) }
func MakeLen[K comparable, V any](values map[K]V) func() int {
	return func() int { return len(values) }
}
func Root(values map[int]string) int { return MakeLen(values)() }
`)
	origin := packageFunction(t, pkg, "MakeLen")
	if len(origin.AnonFuncs) != 1 {
		t.Fatalf("generic MakeLen anonymous functions = %d, want one", len(origin.AnonFuncs))
	}
	closure := origin.AnonFuncs[0]
	var lenCall *ssa.Call
	for _, block := range closure.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil {
				continue
			}
			builtin, ok := call.Common().Value.(*ssa.Builtin)
			if ok && builtin.Name() == "len" {
				lenCall = call
			}
		}
	}
	if lenCall == nil || !ssaExactLenBuiltinNoUnwind(lenCall) {
		t.Fatalf("generic map len was not recognized as one exact no-unwind builtin: %v", lenCall)
	}
	mapLen := packageFunction(t, pkg, "MapLen")
	analysis := newSSAExactNoUnwindAnalysis(
		mapLen,
		map[*ssa.Function]bool{mapLen: true},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	if ok, err := analysis.scan(); err != nil || !ok {
		t.Fatalf("generic map len exact no-unwind scan = %t, %v", ok, err)
	}

	constraint := types.NewInterfaceType(nil, nil).Complete()
	parameter := types.NewTypeParam(types.NewTypeName(token.NoPos, nil, "T", nil), constraint)
	if ssaExactLenOperandNoUnwind(parameter) {
		t.Fatal("bare type parameter unexpectedly acquired exact len semantics")
	}
	if !ssaExactLenOperandNoUnwind(types.NewMap(parameter, types.Typ[types.Int])) {
		t.Fatal("map[T]int did not retain exact map-header len semantics")
	}
}

func TestSSAExactNoUnwindFixedArrayIndexesRequireExactBounds(t *testing.T) {
	_, pkg := buildCoroTestSSA(t, "no_unwind_fixed_array.go", `package coroid
type bucket struct { size uintptr }
var buckets = [...]bucket{{16}, {32}, {64}, {128}, {256}, {512}, {1024}, {2048}}

func rangeAddress(size uintptr) uintptr {
	for index := range buckets {
		entry := &buckets[index]
		if entry.size == size { return entry.size }
	}
	return 0
}
func rangeValue(size uintptr) uintptr {
	values := buckets
	for _, entry := range values {
		if entry.size == size { return entry.size }
	}
	return values[len(values)-1].size
}
func boundedStep() uintptr {
	var total uintptr
	for index := 0; index < len(buckets); index += 2 { total += buckets[index].size }
	return total
}
func constantIndex() uintptr { return buckets[7].size }
func unsignedGuard(index uint) uintptr {
	if index < uint(len(buckets)) { return buckets[index].size }
	return 0
}
func guardedPointer(values *[8]bucket, index uint) uintptr {
	if values != nil && index < uint(len(values)) { return values[index].size }
	return 0
}

func sliceRange(values []bucket) uintptr {
	var total uintptr
	for _, entry := range values { total += entry.size }
	return total
}
func unguarded(index int) uintptr { return buckets[index].size }
func tooLargeGuard(index uint) uintptr {
	if index < 9 { return buckets[index].size }
	return 0
}
func signedUpperOnly(index int) uintptr {
	if index < len(buckets) { return buckets[index].size }
	return 0
}
func nilPointer(values *[8]bucket) uintptr { return values[0].size }
func overflowReentry() uintptr {
	var index int8
	for {
		if index < 8 {
			if index < 0 { return buckets[index].size }
		}
		index++
	}
}
`)

	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "rangeAddress", want: true},
		{name: "rangeValue", want: true},
		{name: "boundedStep", want: true},
		{name: "constantIndex", want: true},
		{name: "unsignedGuard", want: true},
		{name: "guardedPointer", want: true},
		{name: "sliceRange", want: false},
		{name: "unguarded", want: false},
		{name: "tooLargeGuard", want: false},
		{name: "signedUpperOnly", want: false},
		{name: "nilPointer", want: false},
		{name: "overflowReentry", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fn := packageFunction(t, pkg, test.name)
			safeIndexes := analyzeSSAExactSafeFixedArrayIndexes([]*ssa.Function{fn})
			analysis := newSSAExactNoUnwindAnalysis(
				fn,
				map[*ssa.Function]bool{fn: true},
				nil,
				nil,
				nil,
				nil,
				safeIndexes,
				nil,
			)
			proved, err := analysis.scan()
			if err != nil {
				t.Fatal(err)
			}
			if proved != test.want {
				t.Fatalf("exact no-unwind scan = %t, want %t\n%s", proved, test.want, fn.String())
			}
		})
	}
}

func TestSSAExactNoUnwindUsesFrozenElidedEdgeAndRetainsLoweredEffects(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "no_unwind_elided.go", `package coroid
func intrinsic(value uint64) uint64 { panic("declaration stub is never emitted") }
func helper() { panic("lowered helper") }
func owner(value uint64) uint64 { return intrinsic(value) }
`)
	owner := packageFunction(t, pkg, "owner")
	elidedTarget := packageFunction(t, pkg, "intrinsic")
	elidedCall := onlyStaticCallTo(t, owner, elidedTarget)
	config := SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			return call == elidedCall, nil
		},
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: SyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, owner); got.Exec.Contains(MayUnwind) {
		t.Fatalf("owner retained the elided declaration stub's unwind: %+v", got)
	}
	if !plan.ElidesCall(elidedCall) {
		t.Fatal("exact frontend-elided call was not frozen in the plan")
	}

	helped := config
	helper := packageFunction(t, pkg, "helper")
	helped.ClassifyLoweredCalls = func(fn *ssa.Function) ([]SSALoweredCall, error) {
		if fn == owner {
			return []SSALoweredCall{{LogicalName: "runtime.helper", Target: helper}}, nil
		}
		return nil, nil
	}
	plan, err = AnalyzeSSA(prog, Roots{{Function: owner, Demand: SyncDemand}}, helped)
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, owner); !got.Exec.Contains(MayUnwind) {
		t.Fatalf("owner lost its independently lowered helper's unwind: %+v", got)
	}
}

func TestSSAExactNoUnwindAcceptsDirectionalChannelLenCap(t *testing.T) {
	_, pkg := buildCoroTestSSA(t, "no_unwind_channel_len_cap.go", `package coroid
func RecvLen[T any](values <-chan T) int { return len(values) }
func SendLen[T any](values chan<- T) int { return len(values) }
func RecvCap[T any](values <-chan T) int { return cap(values) }
func SendCap[T any](values chan<- T) int { return cap(values) }
func SliceCap[T any](values []T) int { return cap(values) }
func ParamCap[T ~chan int](values T) int { return cap(values) }
`)
	for _, test := range []struct {
		function string
		builtin  string
		exact    bool
	}{
		{function: "RecvLen", builtin: "len", exact: true},
		{function: "SendLen", builtin: "len", exact: true},
		{function: "RecvCap", builtin: "cap", exact: true},
		{function: "SendCap", builtin: "cap", exact: true},
		{function: "SliceCap", builtin: "cap", exact: true},
		// Even a single-term constraint remains a bare type parameter at this
		// generic origin. The instantiated outer representation must be frozen
		// before the physical proof accepts it.
		{function: "ParamCap", builtin: "cap", exact: false},
	} {
		fn := packageFunction(t, pkg, test.function)
		call := onlyBuiltinCallByName(t, fn, test.builtin)
		var exact bool
		if test.builtin == "len" {
			exact = ssaExactLenBuiltinNoUnwind(call)
		} else {
			exact = ssaExactCapBuiltinNoUnwind(call)
		}
		if exact != test.exact {
			t.Errorf("%s %s exact no-unwind = %t, want %t (operand %s)", test.function, test.builtin, exact, test.exact, call.Common().Args[0].Type())
		}
		analysis := newSSAExactNoUnwindAnalysis(
			fn,
			map[*ssa.Function]bool{fn: true},
			nil,
			nil,
			nil,
			nil,
			nil,
			nil,
		)
		proved, err := analysis.scan()
		if err != nil {
			t.Fatalf("%s exact no-unwind scan: %v", test.function, err)
		}
		if proved != test.exact {
			t.Errorf("%s exact no-unwind scan = %t, want %t", test.function, proved, test.exact)
		}
	}

	constraint := types.NewInterfaceType(nil, nil).Complete()
	parameter := types.NewTypeParam(types.NewTypeName(token.NoPos, nil, "T", nil), constraint)
	if ssaExactCapOperandNoUnwind(parameter) {
		t.Fatal("bare type parameter unexpectedly acquired exact cap semantics")
	}
	if ssaExactCapOperandNoUnwind(constraint) {
		t.Fatal("interface unexpectedly acquired exact cap semantics")
	}
	for _, direction := range []types.ChanDir{types.SendRecv, types.RecvOnly, types.SendOnly} {
		channel := types.NewChan(direction, parameter)
		if !ssaExactLenOperandNoUnwind(channel) || !ssaExactCapOperandNoUnwind(channel) {
			t.Errorf("directional channel %s lost exact len/cap semantics", channel)
		}
	}
}

func onlyStaticCallTo(t *testing.T, owner, target *ssa.Function) *ssa.Call {
	t.Helper()
	for _, block := range owner.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common() != nil && call.Common().StaticCallee() == target {
				return call
			}
		}
	}
	t.Fatalf("%s has no static call to %s", owner.Name(), target.Name())
	return nil
}

func onlyBuiltinCallByName(t *testing.T, fn *ssa.Function, name string) *ssa.Call {
	t.Helper()
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil {
				continue
			}
			builtin, ok := call.Common().Value.(*ssa.Builtin)
			if ok && builtin.Name() == name {
				return call
			}
		}
	}
	t.Fatalf("%s has no %s builtin", fn.Name(), name)
	return nil
}

func onlyNonBuiltinCallWithoutStaticCallee(t *testing.T, fn *ssa.Function) ssa.CallInstruction {
	t.Helper()
	var result ssa.CallInstruction
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok || call.Common() == nil || call.Common().StaticCallee() != nil {
				continue
			}
			if _, builtin := call.Common().Value.(*ssa.Builtin); builtin {
				continue
			}
			if result != nil {
				t.Fatalf("%s has multiple dynamic calls", fn.Name())
			}
			result = call
		}
	}
	if result == nil {
		t.Fatalf("%s has no dynamic call", fn.Name())
	}
	return result
}
