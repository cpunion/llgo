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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroSliceBoundsFixture = `package foo

type Bytes []byte
type Array8 [8]byte
const constantDigits = "0123456789abcdef"

func Slice2(value Bytes, low, high int) Bytes { return value[low:high] }
func Slice2Suffix(value []byte, low int) []byte { return value[low:] }
func Slice2Wide(value []byte, low, high uint64) []byte { return value[low:high] }
func Slice3(value []byte, low, high, max int) []byte { return value[low:high:max] }
func String2(value string, low, high int) string { return value[low:high] }
func StringConst(low, high int) string { return constantDigits[low:high] }
func Pointer2(value *Array8, low, high int) []byte { return value[low:high] }
`

func TestCoroDynamicSliceBoundsNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			prog, pkg, plan, functions := compileCoroSliceBoundsFixture(t, target.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify structured Slice before CoroSplit: %v\n%s", err, module.String())
			}
			for _, test := range []struct {
				name       string
				faults     int
				boundsKind int
				minUGT     int
			}{
				{name: "Slice2", faults: 1, boundsKind: 1, minUGT: 2},
				{name: "Slice2Suffix", faults: 1, boundsKind: 1, minUGT: 2},
				{name: "Slice2Wide", faults: 1, boundsKind: 1, minUGT: 2},
				{name: "Slice3", faults: 1, boundsKind: 1, minUGT: 3},
				{name: "String2", faults: 1, boundsKind: 1, minUGT: 2},
				{name: "StringConst", faults: 1, boundsKind: 1, minUGT: 2},
				{name: "Pointer2", faults: 2, boundsKind: 1, minUGT: 2},
			} {
				function := functions[test.name]
				functionPlan, ok := plan.FunctionPlan(function)
				if !ok || functionPlan.Emission != coro.EmitCoroutine || !functionPlan.Exec.Contains(coro.MayUnwind) {
					t.Fatalf("%s plan = %+v, present=%t; want may-unwind coroutine", test.name, functionPlan, ok)
				}
				body := requireCoroPhysicalFunction(t, module, "foo."+test.name).String()
				if got := strings.Count(body, "call void @"+coroFaultPrepareHookV1); got != test.faults {
					t.Fatalf("%s fault prepare calls = %d, want %d:\n%s", test.name, got, test.faults, body)
				}
				if got := strings.Count(body, "icmp ugt"); got < test.minUGT {
					t.Fatalf("%s inclusive bounds comparisons = %d, want at least %d:\n%s", test.name, got, test.minUGT, body)
				}
				for _, helper := range []string{"StringSlice2", "NewSlice2", "NewSlice3Bounds"} {
					if strings.Contains(body, helper) {
						t.Fatalf("%s retained native-stack helper %s:\n%s", test.name, helper, body)
					}
				}
				if got := strings.Count(body, "i32 2"); got < test.boundsKind {
					t.Fatalf("%s did not select the index/slice-bounds fault kind:\n%s", test.name, body)
				}
				if hook, aggregate := strings.Index(body, "call void @"+coroFaultPrepareHookV1), strings.LastIndex(body, "insertvalue"); hook < 0 || aggregate < hook {
					t.Fatalf("%s constructed its result before the terminal bounds edge:\n%s", test.name, body)
				}
			}

			runCoroABITestPipeline(t, prog, module)
			for name := range functions {
				resume := module.NamedFunction("foo." + name + "$coro.resume")
				if resume.IsNil() || !strings.Contains(resume.String(), coroFaultPrepareHookV1) {
					t.Fatalf("post-split %s resume lost its structured slice fault edge:\n%s", name, module.String())
				}
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit structured Slice object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(coroFaultPrepareHookV1)) {
				t.Fatal("post-CoroSplit object lost the structured slice fault hook")
			}
		})
	}
}

func TestCoroDynamicSliceBoundsFailClosed(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, coroSliceBoundsFixture)
	for _, name := range []string{"Slice2", "Slice3", "String2", "StringConst"} {
		function := ssaPkg.Func(name)
		slice := coroOnlySliceInstruction(t, function)
		audit := &coroPhysicalPureSSAAudit{
			fn:              function,
			reachableBlocks: coroPhysicalConstantReachableBlocks(function),
		}
		if reason := audit.validateSlice(slice); !strings.Contains(reason, "explicit-status panic ABI") {
			t.Fatalf("%s legacy rejection = %q", name, reason)
		}
	}

	function := ssaPkg.Func("Slice3")
	slice := coroOnlySliceInstruction(t, function)
	audit := &coroPhysicalPureSSAAudit{
		fn:                    function,
		reachableBlocks:       coroPhysicalConstantReachableBlocks(function),
		allowImplicitNilFault: true,
	}
	high := slice.High
	slice.High = nil
	defer func() { slice.High = high }()
	if reason := audit.validateSlice(slice); !strings.Contains(reason, "requires explicit high and max") {
		t.Fatalf("malformed slice3 rejection = %q", reason)
	}
}

func TestCoroDynamicSliceBoundsGoRuleMatrix(t *testing.T) {
	storage := make([]byte, 2, 4)
	for _, test := range []struct {
		name       string
		low, high  int
		wantPanic  bool
		wantLenCap [2]int
	}{
		{name: "length view", low: 0, high: 2, wantLenCap: [2]int{2, 4}},
		{name: "two-index uses cap", low: 1, high: 4, wantLenCap: [2]int{3, 3}},
		{name: "empty cap suffix", low: 4, high: 4, wantLenCap: [2]int{0, 0}},
		{name: "negative low", low: -1, high: 0, wantPanic: true},
		{name: "high above cap", low: 0, high: 5, wantPanic: true},
		{name: "low above high", low: 3, high: 2, wantPanic: true},
	} {
		t.Run("slice2/"+test.name, func(t *testing.T) {
			result, panicked := recoverSlice2(storage, test.low, test.high)
			if panicked != test.wantPanic {
				t.Fatalf("panic = %t, want %t", panicked, test.wantPanic)
			}
			if !panicked && [2]int{len(result), cap(result)} != test.wantLenCap {
				t.Fatalf("len/cap = %v, want %v", [2]int{len(result), cap(result)}, test.wantLenCap)
			}
		})
	}

	for _, test := range []struct {
		name            string
		low, high, max  int
		wantPanic       bool
		wantLength, cap int
	}{
		{name: "cap extension", low: 1, high: 3, max: 4, wantLength: 2, cap: 3},
		{name: "max above cap", low: 0, high: 2, max: 5, wantPanic: true},
		{name: "high above max", low: 0, high: 4, max: 3, wantPanic: true},
		{name: "low above high", low: 3, high: 2, max: 4, wantPanic: true},
	} {
		t.Run("slice3/"+test.name, func(t *testing.T) {
			result, panicked := recoverSlice3(storage, test.low, test.high, test.max)
			if panicked != test.wantPanic {
				t.Fatalf("panic = %t, want %t", panicked, test.wantPanic)
			}
			if !panicked && (len(result) != test.wantLength || cap(result) != test.cap) {
				t.Fatalf("len/cap = %d/%d, want %d/%d", len(result), cap(result), test.wantLength, test.cap)
			}
		})
	}

	if _, panicked := recoverStringSlice("ab", 0, 3); !panicked {
		t.Fatal("string slice accepted high above len")
	}
	if _, panicked := recoverWideSlice(storage, 0, ^uint64(0)); !panicked {
		t.Fatal("slice accepted a uint64 bound that cannot fit target int")
	}
}

func compileCoroSliceBoundsFixture(
	t *testing.T,
	target *llssa.Target,
) (llssa.Program, llssa.Package, *coro.SSAPlan, map[string]*ssa.Function) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroSliceBoundsFixture)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	functions := make(map[string]*ssa.Function)
	var roots coro.Roots
	for _, name := range []string{"Slice2", "Slice2Suffix", "Slice2Wide", "Slice3", "String2", "StringConst", "Pointer2"} {
		function := ssaPkg.Func(name)
		functions[name] = function
		roots = append(roots, coro.Root{Function: function, Demand: coro.AsyncDemand})
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, roots, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if _, ok := functions[function.Name()]; ok && functions[function.Name()] == function {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
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
	compilation.EnableCoroExplicitStatusPanicABI = true
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

func coroOnlySliceInstruction(t *testing.T, function *ssa.Function) *ssa.Slice {
	t.Helper()
	var found *ssa.Slice
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			candidate, ok := instruction.(*ssa.Slice)
			if !ok {
				continue
			}
			if found != nil {
				t.Fatalf("%s has more than one Slice instruction", function)
			}
			found = candidate
		}
	}
	if found == nil {
		t.Fatalf("%s has no Slice instruction", function)
	}
	return found
}

func recoverSlice2(value []byte, low, high int) (result []byte, panicked bool) {
	defer func() { panicked = recover() != nil }()
	result = value[low:high]
	return
}

func recoverSlice3(value []byte, low, high, max int) (result []byte, panicked bool) {
	defer func() { panicked = recover() != nil }()
	result = value[low:high:max]
	return
}

func recoverStringSlice(value string, low, high int) (result string, panicked bool) {
	defer func() { panicked = recover() != nil }()
	result = value[low:high]
	return
}

func recoverWideSlice(value []byte, low, high uint64) (result []byte, panicked bool) {
	defer func() { panicked = recover() != nil }()
	result = value[low:high]
	return
}
