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
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCompileUnsafeSizeAlignOperandIsNotEvaluated(t *testing.T) {
	_, module := mustCompileLLPkgFromSrc(t, `package foo

import "unsafe"

type payload struct {
	b byte
	n int64
}

func value[T any](p *T) T { return *p }

func sizeDeref[T any](p *T) uintptr {
	return unsafe.Sizeof(*p)
}

func alignDeref[T any](p *T) uintptr {
	return unsafe.Alignof(*p)
}

func sizeCall[T any](p *T) uintptr {
	return unsafe.Sizeof(value(p))
}

func sharedCall[T any](p *T) (uintptr, T) {
	v := value(p)
	return unsafe.Sizeof(v), v
}

func instantiate() {
	_ = sizeDeref[payload](nil)
	_ = alignDeref[payload](nil)
	_ = sizeCall[payload](nil)
	_, _ = sharedCall[payload](nil)
}
`)
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("compiled module failed verification: %v\n%s", err, module.String())
	}

	for _, prefix := range []string{"foo.sizeDeref[", "foo.alignDeref["} {
		body := unsafeSizeAlignFunctionWithPrefix(t, module, prefix).String()
		if strings.Contains(body, "AssertNilDeref") || strings.Contains(body, "runtime.Panic") {
			t.Fatalf("%s evaluated its nil dereference:\n%s", prefix, body)
		}
		if !strings.Contains(body, "ret i") {
			t.Fatalf("%s did not lower to an integer constant:\n%s", prefix, body)
		}
	}

	sizeCall := unsafeSizeAlignFunctionWithPrefix(t, module, "foo.sizeCall[").String()
	if strings.Contains(sizeCall, "foo.value[") {
		t.Fatalf("Sizeof evaluated its call operand:\n%s", sizeCall)
	}

	sharedCall := unsafeSizeAlignFunctionWithPrefix(t, module, "foo.sharedCall[").String()
	if !strings.Contains(sharedCall, "foo.value[") {
		t.Fatalf("a value shared with a real use lost that use:\n%s", sharedCall)
	}
}

func TestEmissionUniverseFreezesUnsafeSizeAlignUnevaluatedSSA(t *testing.T) {
	t.Run("generic size-only index has no phantom helper", func(t *testing.T) {
		testProg := newEmissionTestProgram()
		testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
		runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
func Present() {}
`)
		callerPkg := testProg.addPackage(t, "example.com/emission/sizeonly", `package sizeonly
import "unsafe"
type Record struct { Code uint32; Text string }
func SizeOnly[E any](values []E) uintptr { return unsafe.Sizeof(values[0]) }
func Use(values []Record) uintptr { return SizeOnly(values) }
`)
		testProg.ssa.Build()
		prog := newLLSSAProg(t)
		defer prog.Dispose()
		universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
			{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
			{SSA: callerPkg.ssa, Files: []*ast.File{callerPkg.file}},
		}, EmissionUniverseOptions{CompleteRuntimeABI: true})
		if err != nil {
			t.Fatalf("prepare size-only universe: %v", err)
		}

		instance := unsafeSizeAlignStaticCallee(t, callerPkg.ssa.Func("Use"), "SizeOnly")
		frozen, ok := universe.frozenUnsafeLayoutUnevaluatedSSA(instance)
		if !ok {
			t.Fatalf("generic instance %q has no frozen unevaluated SSA set", instance.String())
		}
		var omittedIndex ssa.Instruction
		for instruction := range frozen {
			switch instruction.(type) {
			case *ssa.Index, *ssa.IndexAddr:
				omittedIndex = instruction
				break
			}
		}
		if omittedIndex == nil {
			t.Fatalf("generic instance %q did not freeze its Sizeof-only slice index: frozen=%s", instance.String(), unsafeSizeAlignInstructionSetString(frozen))
		}
		lowered, err := universe.CoroLoweredCalls(instance)
		if err != nil {
			t.Fatal(err)
		}
		if len(lowered) != 0 {
			t.Fatalf("Sizeof-only generic index acquired phantom lowered calls: %+v", lowered)
		}
		audit, err := newCoroPhysicalPureSSAAudit(universe, nil, instance, "")
		if err != nil {
			t.Fatal(err)
		}
		if handled, reason := audit.validate(omittedIndex); !handled || reason != "" {
			t.Fatalf("frozen unevaluated index audit = handled %t, reason %q; want omitted", handled, reason)
		}
	})

	t.Run("generic offsetof composite producer is unevaluated", func(t *testing.T) {
		testProg := newEmissionTestProgram()
		testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
		runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
func Present() {}
`)
		callerPkg := testProg.addPackage(t, "example.com/emission/offsetof", `package offsetof
import "unsafe"
func identity[E any](value E) E { return value }
func Offset[E ~int](value E) uintptr {
	return unsafe.Offsetof(struct {
		Prefix byte
		Value E
	}{0, identity(value)}.Value)
}
func Use(value int) uintptr { return Offset(value) }
`)
		testProg.ssa.Build()
		prog := newLLSSAProg(t)
		defer prog.Dispose()
		universe, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
			{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
			{SSA: callerPkg.ssa, Files: []*ast.File{callerPkg.file}},
		}, EmissionUniverseOptions{CompleteRuntimeABI: true})
		if err != nil {
			t.Fatalf("prepare offsetof universe: %v", err)
		}

		instance := unsafeSizeAlignStaticCallee(t, callerPkg.ssa.Func("Use"), "Offset")
		frozen, ok := universe.frozenUnsafeLayoutUnevaluatedSSA(instance)
		if !ok {
			t.Fatalf("generic instance %q has no frozen layout-operand set", instance.String())
		}
		var producer *ssa.Call
		var offsetCall *ssa.Call
		for _, block := range instance.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(*ssa.Call)
				if !ok {
					continue
				}
				if builtin, ok := call.Call.Value.(*ssa.Builtin); ok && builtin.Name() == "Offsetof" {
					offsetCall = call
					continue
				}
				if _, omitted := frozen[call]; omitted {
					producer = call
				}
			}
		}
		if producer == nil || offsetCall == nil {
			var dump bytes.Buffer
			ssa.WriteFunction(&dump, instance)
			t.Fatalf("generic Offset frozen=%s; want omitted producer and live Offsetof\n%s", unsafeSizeAlignInstructionSetString(frozen), dump.String())
		}
		if _, omitted := frozen[offsetCall]; omitted {
			t.Fatal("Offsetof instruction itself must remain live")
		}
		if calls, err := universe.CoroLoweredCalls(instance); err != nil {
			t.Fatal(err)
		} else if len(calls) != 0 {
			t.Fatalf("Offsetof-only generic body acquired phantom lowered calls: %+v", calls)
		}
		audit, err := newCoroPhysicalPureSSAAudit(universe, nil, instance, "")
		if err != nil {
			t.Fatal(err)
		}
		if handled, reason := audit.validate(offsetCall); !handled || reason != "" {
			t.Fatalf("Offsetof audit = handled %t, reason %q; want exact constant lowering", handled, reason)
		}
	})

	t.Run("generic overlaps uses current-frame faults and retains plain helper", func(t *testing.T) {
		testProg := newEmissionTestProgram()
		testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
		runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
func CheckIndexRange(ok bool, index int64, signed bool, length int) {}
`)
		callerPkg := testProg.addPackage(t, "example.com/emission/overlaps", `package overlaps
import "unsafe"
type Record struct { Code uint32; Text string }
func Overlaps[E any](a, b []E) bool {
	if len(a) == 0 || len(b) == 0 { return false }
	elemSize := unsafe.Sizeof(a[0])
	if elemSize == 0 { return false }
	return uintptr(unsafe.Pointer(&a[0])) <= uintptr(unsafe.Pointer(&b[len(b)-1]))+(elemSize-1) &&
		uintptr(unsafe.Pointer(&b[0])) <= uintptr(unsafe.Pointer(&a[len(a)-1]))+(elemSize-1)
}
func Use(a, b []Record) bool { return Overlaps(a, b) }
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
		instance := unsafeSizeAlignStaticCallee(t, callerPkg.ssa.Func("Use"), "Overlaps")
		if calls, err := universe.CoroLoweredCalls(instance); err != nil {
			t.Fatal(err)
		} else if len(calls) != 0 {
			t.Fatalf("physical generic overlaps lowered calls = %+v; want current-frame implicit faults only", calls)
		}
		rangeHelper := runtimePkg.ssa.Func("CheckIndexRange")
		if target, ok, err := universe.ResolveCoroPlainLoweredCall(instance, "CheckIndexRange"); err != nil || !ok || target != rangeHelper {
			t.Fatalf("plain generic overlaps CheckIndexRange = %v, %t, %v; want exact runtime helper", target, ok, err)
		}

		ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
		if err != nil {
			t.Fatal(err)
		}
		functionIDs := universe.FunctionIDConfig()
		functionIDs.CoroABI = coro.PhysicalABIV1
		functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
		functionIDs.ArchiveReady = true
		plan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{{Function: instance, Demand: coro.AsyncDemand}}, coro.SSAConfig{
			EmissionUniverse:             ssaUniverse,
			FunctionIDs:                  functionIDs,
			MaxPlainInstructions:         -1,
			OutcomeMode:                  coro.OutcomeExplicitStatus,
			ClassifyLoweredCalls:         universe.CoroLoweredCalls,
			ClassifyDemandReferences:     universe.CoroDemandReferences,
			ClassifySyncDemandReferences: universe.CoroSyncDemandReferences,
		})
		if err != nil {
			t.Fatal(err)
		}
		functionPlan, ok := plan.FunctionPlan(instance)
		if !ok || functionPlan.Emission != coro.EmitCoroutine || functionPlan.Effect.Contains(coro.AwaitStructured) {
			t.Fatalf("generic overlaps plan = %+v, present=%t; want coroutine without managed await", functionPlan, ok)
		}
		report, err := (&Compilation{CoroPlan: plan, EmissionUniverse: universe}).BuildCoroLoweringFactsReport()
		if err != nil {
			t.Fatal(err)
		}
		functionID, _ := plan.FunctionID(instance)
		facts := loweringFactsFunctionByID(t, report.Facts, functionID)
		implicit := 0
		for _, fact := range facts.Sites {
			if len(fact.ImplicitPanic) == 0 {
				continue
			}
			implicit++
			if len(fact.Helpers) != 0 {
				t.Fatalf("current-frame implicit panic fact retained managed helper edges: %+v", fact)
			}
		}
		if implicit != 4 {
			t.Fatalf("generic overlaps implicit panic facts = %d, want four real guarded indexes", implicit)
		}
	})

	t.Run("shared real index retains helper", func(t *testing.T) {
		testProg := newEmissionTestProgram()
		testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
		runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, `package runtime
func Present() {}
`)
		callerPkg := testProg.addPackage(t, "example.com/emission/sizeshared", `package sizeshared
import "unsafe"
type Record struct { Code uint32; Text string }
func Shared(values []Record) (uintptr, Record) {
	value := values[0]
	return unsafe.Sizeof(value), value
}
`)
		testProg.ssa.Build()
		prog := newLLSSAProg(t)
		defer prog.Dispose()
		_, err := PrepareEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
			{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
			{SSA: callerPkg.ssa, Files: []*ast.File{callerPkg.file}},
		}, EmissionUniverseOptions{CompleteRuntimeABI: true})
		if err == nil || !strings.Contains(err.Error(), `missing runtime helper "CheckIndexRange"`) {
			t.Fatalf("shared real index preparation error = %v; want missing CheckIndexRange", err)
		}
	})
}

func unsafeSizeAlignInstructionSetString(instructions map[ssa.Instruction]none) string {
	values := make([]string, 0, len(instructions))
	for instruction := range instructions {
		values = append(values, instruction.String())
	}
	return strings.Join(values, "; ")
}

func unsafeSizeAlignStaticCallee(t *testing.T, fn *ssa.Function, name string) *ssa.Function {
	t.Helper()
	if fn == nil {
		t.Fatal("missing caller function")
	}
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Call.StaticCallee() == nil {
				continue
			}
			callee := call.Call.StaticCallee()
			origin := callee.Origin()
			if callee.Name() == name || origin != nil && origin.Name() == name {
				return callee
			}
		}
	}
	t.Fatalf("function %q has no static call to %q", fn.String(), name)
	return nil
}

func unsafeSizeAlignFunctionWithPrefix(t *testing.T, module llvm.Module, prefix string) llvm.Value {
	t.Helper()
	var found llvm.Value
	for function := module.FirstFunction(); !function.IsNil(); function = llvm.NextFunction(function) {
		if !strings.HasPrefix(function.Name(), prefix) {
			continue
		}
		if !found.IsNil() {
			t.Fatalf("multiple compiled functions with prefix %q: %s and %s", prefix, found.Name(), function.Name())
		}
		found = function
	}
	if found.IsNil() {
		t.Fatalf("missing compiled function with prefix %q", prefix)
	}
	return found
}
