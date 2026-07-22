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

const coroUnsafeSliceFixture = `package foo
import "unsafe"

type Triple struct { A, B, C byte }
type Zero struct{}

func Bytes(pointer *byte, length int) []byte { return unsafe.Slice(pointer, length) }
func WideUnsigned(pointer *byte, length uint64) []byte { return unsafe.Slice(pointer, length) }
func WideSigned(pointer *byte, length int64) []byte { return unsafe.Slice(pointer, length) }
func Triples(pointer *Triple, length uintptr) []Triple { return unsafe.Slice(pointer, length) }
func Zeros(pointer *Zero, length int) []Zero { return unsafe.Slice(pointer, length) }
func NilZero() []byte { return unsafe.Slice((*byte)(nil), 0) }
func NilOne() []byte { return unsafe.Slice((*byte)(nil), 1) }
func MakeString(pointer *byte, length int) string { return unsafe.String(pointer, length) }
func WideString(pointer *byte, length uint64) string { return unsafe.String(pointer, length) }
func NilStringZero() string { return unsafe.String((*byte)(nil), 0) }
func NilStringOne() string { return unsafe.String((*byte)(nil), 1) }
func StringBytes(value string) *byte { return unsafe.StringData(value) }
func SliceBytes(value []byte) *byte { return unsafe.SliceData(value) }
`

func TestCoroUnsafeSliceNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			prog, pkg, plan, functions := compileCoroUnsafeSliceFixture(t, target.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify unsafe.Slice before CoroSplit: %v\n%s", err, module.String())
			}
			for name, wantFaults := range map[string]int{
				"Bytes": 3, "WideUnsigned": 3, "WideSigned": 3, "Triples": 3,
				"Zeros": 2, "NilZero": 3, "NilOne": 3,
			} {
				functionPlan, ok := plan.FunctionPlan(functions[name])
				if !ok || functionPlan.Emission != coro.EmitCoroutine || !functionPlan.Exec.Contains(coro.MayUnwind) {
					t.Fatalf("%s plan = %+v, present=%t; want may-unwind coroutine", name, functionPlan, ok)
				}
				body := requireCoroPhysicalFunction(t, module, "foo."+name).String()
				if got := strings.Count(body, "call void @"+coroFaultPrepareHookV1); got != wantFaults {
					t.Fatalf("%s fault calls = %d, want %d:\n%s", name, got, wantFaults, body)
				}
				if strings.Contains(body, "AssertRuntimeError") || !strings.Contains(body, "i32 4") || !strings.Contains(body, "i32 5") {
					t.Fatalf("%s retained helper or lost exact unsafe.Slice fault kinds:\n%s", name, body)
				}
				if name != "NilZero" && name != "NilOne" {
					if hook, aggregate := strings.Index(body, "call void @"+coroFaultPrepareHookV1), strings.LastIndex(body, "insertvalue"); hook < 0 || aggregate < hook {
						t.Fatalf("%s formed its slice before the terminal fault edges:\n%s", name, body)
					}
				}
			}
			for _, name := range []string{"MakeString", "WideString", "NilStringZero", "NilStringOne"} {
				functionPlan, ok := plan.FunctionPlan(functions[name])
				if !ok || functionPlan.Emission != coro.EmitCoroutine || !functionPlan.Exec.Contains(coro.MayUnwind) {
					t.Fatalf("%s plan = %+v, present=%t; want may-unwind coroutine", name, functionPlan, ok)
				}
				body := requireCoroPhysicalFunction(t, module, "foo."+name).String()
				if got := strings.Count(body, "call void @"+coroFaultPrepareHookV1); got != 3 {
					t.Fatalf("%s fault calls = %d, want 3:\n%s", name, got, body)
				}
				if strings.Contains(body, "AssertRuntimeError") || !strings.Contains(body, "i32 8") || !strings.Contains(body, "i32 9") {
					t.Fatalf("%s retained helper or lost exact unsafe.String fault kinds:\n%s", name, body)
				}
			}

			triples := requireCoroPhysicalFunction(t, module, "foo.Triples").String()
			if !strings.Contains(triples, " mul ") || !strings.Contains(triples, "icmp ugt") {
				t.Fatalf("three-byte element did not retain multiplication/span overflow checks:\n%s", triples)
			}
			zeros := requireCoroPhysicalFunction(t, module, "foo.Zeros").String()
			if strings.Contains(zeros, "ptrtoint") || strings.Contains(zeros, " mul ") {
				t.Fatalf("zero-sized element emitted an address-span calculation:\n%s", zeros)
			}
			if target.name == "wasm32" {
				for _, name := range []string{"WideUnsigned", "WideSigned"} {
					body := requireCoroPhysicalFunction(t, module, "foo."+name).String()
					if !strings.Contains(body, "trunc i64") || !strings.Contains(body, "icmp ne i64") {
						t.Fatalf("%s omitted the wasm32 wide-length round trip:\n%s", name, body)
					}
				}
				wideString := requireCoroPhysicalFunction(t, module, "foo.WideString").String()
				if !strings.Contains(wideString, "trunc i64") || !strings.Contains(wideString, "icmp ne i64") {
					t.Fatalf("WideString omitted the wasm32 wide-length round trip:\n%s", wideString)
				}
			}
			for _, name := range []string{"StringBytes", "SliceBytes"} {
				body := requireCoroPhysicalFunction(t, module, "foo."+name).String()
				if !strings.Contains(body, "extractvalue") {
					t.Fatalf("%s did not remain a pure header projection:\n%s", name, body)
				}
			}

			runCoroABITestPipeline(t, prog, module)
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit unsafe.Slice object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(coroFaultPrepareHookV1)) {
				t.Fatal("post-CoroSplit object lost the unsafe.Slice fault hook")
			}
		})
	}
}

func TestCoroUnsafeSlicePureAuditFailsClosed(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, coroUnsafeSliceFixture)
	function := ssaPkg.Func("Bytes")
	call := coroUnsafeSliceBuiltinCall(t, function)
	audit := &coroPhysicalPureSSAAudit{fn: function, reachableBlocks: coroPhysicalConstantReachableBlocks(function)}
	if reason := audit.validateUnsafeSliceBuiltin(call); !strings.Contains(reason, "explicit-status panic ABI") {
		t.Fatalf("legacy unsafe.Slice rejection = %q", reason)
	}
	audit.allowImplicitNilFault = true
	if reason := audit.validateUnsafeSliceBuiltin(call); reason != "" {
		t.Fatalf("explicit-status unsafe.Slice rejection = %q", reason)
	}
	stringFunction := ssaPkg.Func("MakeString")
	stringCall := coroUnsafeBuiltinCall(t, stringFunction, "String")
	stringAudit := &coroPhysicalPureSSAAudit{fn: stringFunction, reachableBlocks: coroPhysicalConstantReachableBlocks(stringFunction)}
	if reason := stringAudit.validateUnsafeStringBuiltin(stringCall); !strings.Contains(reason, "explicit-status panic ABI") {
		t.Fatalf("legacy unsafe.String rejection = %q", reason)
	}
	stringAudit.allowImplicitNilFault = true
	if reason := stringAudit.validateUnsafeStringBuiltin(stringCall); reason != "" {
		t.Fatalf("explicit-status unsafe.String rejection = %q", reason)
	}
	for _, test := range []struct {
		function string
		builtin  string
	}{
		{function: "StringBytes", builtin: "StringData"},
		{function: "SliceBytes", builtin: "SliceData"},
	} {
		function := ssaPkg.Func(test.function)
		call := coroUnsafeBuiltinCall(t, function, test.builtin)
		dataAudit := &coroPhysicalPureSSAAudit{fn: function, reachableBlocks: coroPhysicalConstantReachableBlocks(function)}
		if reason := dataAudit.validateUnsafeDataBuiltin(call, test.builtin); reason != "" {
			t.Fatalf("unsafe.%s rejection = %q", test.builtin, reason)
		}
	}
}

func compileCoroUnsafeSliceFixture(
	t *testing.T,
	target *llssa.Target,
) (llssa.Program, llssa.Package, *coro.SSAPlan, map[string]*ssa.Function) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroUnsafeSliceFixture)
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
	functions := make(map[string]*ssa.Function)
	var roots coro.Roots
	for _, name := range []string{
		"Bytes", "WideUnsigned", "WideSigned", "Triples", "Zeros", "NilZero", "NilOne",
		"MakeString", "WideString", "NilStringZero", "NilStringOne", "StringBytes", "SliceBytes",
	} {
		function := ssaPkg.Func(name)
		functions[name] = function
		roots = append(roots, coro.Root{Function: function, Demand: coro.AsyncDemand})
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, roots, coro.SSAConfig{
		EmissionUniverse: ssaUniverse, FunctionIDs: functionIDs, MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if functions[function.Name()] == function {
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
	compilation.CoroProfile = CoroProfileStackless
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, functions
}

func coroUnsafeSliceBuiltinCall(t *testing.T, function *ssa.Function) *ssa.Call {
	return coroUnsafeBuiltinCall(t, function, "Slice")
}

func coroUnsafeBuiltinCall(t *testing.T, function *ssa.Function, name string) *ssa.Call {
	t.Helper()
	var found *ssa.Call
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok {
				continue
			}
			builtin, ok := call.Common().Value.(*ssa.Builtin)
			if !ok || builtin.Name() != name {
				continue
			}
			if found != nil {
				t.Fatalf("%s has more than one unsafe.Slice builtin", function)
			}
			found = call
		}
	}
	if found == nil {
		t.Fatalf("%s has no unsafe.%s builtin", function, name)
	}
	return found
}
