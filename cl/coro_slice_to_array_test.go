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
	"go/token"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroSliceToArrayFixture = `package foo

type Octet byte
type Octets []Octet
type FourOctets [4]Octet

func Pointer4(value []byte) *[4]byte { return (*[4]byte)(value) }
func Value4(value []byte) [4]byte { return [4]byte(value) }
func ExplicitValue4(value []byte) [4]byte { return *(*[4]byte)(value) }
func Pointer0(value []byte) *[0]byte { return (*[0]byte)(value) }
func Value0(value []byte) [0]byte { return [0]byte(value) }
func ExplicitValue0(value []byte) [0]byte { return *(*[0]byte)(value) }
func GuardedExplicitValue0(value []byte) [0]byte {
	pointer := (*[0]byte)(value)
	if pointer == nil { return [0]byte{} }
	return *pointer
}
func NamedPointer4(value Octets) *FourOctets { return (*FourOctets)(value) }
`

func TestCoroSliceToArrayPointerNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			prog, pkg, universe, plan, functions := compileCoroSliceToArrayFixture(t, target.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify slice-to-array conversion before CoroSplit: %v\n%s", err, module.String())
			}
			for _, name := range []string{"Pointer4", "Value4", "ExplicitValue4", "NamedPointer4"} {
				function := functions[name]
				functionPlan, ok := plan.FunctionPlan(function)
				if !ok || functionPlan.Emission != coro.EmitCoroutine || !functionPlan.Exec.Contains(coro.MayUnwind) {
					t.Fatalf("%s plan = %+v, present=%t; want may-unwind coroutine", name, functionPlan, ok)
				}
				body := requireCoroPhysicalFunction(t, module, "foo."+name).String()
				requireCoroSliceToArrayFault(t, name, body, coroFaultSliceConvertV1)
			}

			for _, name := range []string{"Pointer0", "GuardedExplicitValue0"} {
				functionPlan, ok := plan.FunctionPlan(functions[name])
				if !ok || functionPlan.Emission != coro.EmitCoroutine || functionPlan.Exec.Contains(coro.MayUnwind) {
					t.Fatalf("%s plan = %+v, present=%t; want exact no-unwind coroutine", name, functionPlan, ok)
				}
			}
			explicit0Plan, ok := plan.FunctionPlan(functions["ExplicitValue0"])
			if !ok || !explicit0Plan.Exec.Contains(coro.MayUnwind) {
				t.Fatalf("ExplicitValue0 plan = %+v, present=%t; want nullable explicit deref", explicit0Plan, ok)
			}
			for _, name := range []string{"Pointer0", "Value0", "GuardedExplicitValue0"} {
				body := requireCoroPhysicalFunction(t, module, "foo."+name).String()
				if strings.Contains(body, coroFaultPrepareHookV1) || strings.Contains(body, "PanicSliceConvert") ||
					strings.Contains(body, "AssertNilDeref") {
					t.Fatalf("%s retained a zero-length fault edge:\n%s", name, body)
				}
			}
			pointer0 := requireCoroPhysicalFunction(t, module, "foo.Pointer0").String()
			if !strings.Contains(pointer0, "extractvalue") {
				t.Fatalf("Pointer0 did not preserve the input slice data projection:\n%s", pointer0)
			}
			explicit0 := requireCoroPhysicalFunction(t, module, "foo.ExplicitValue0").String()
			requireCoroSliceToArrayFault(t, "ExplicitValue0", explicit0, coroFaultNilV1)
			if strings.Contains(explicit0, "i32 10") {
				t.Fatalf("ExplicitValue0 incorrectly used the slice-length fault:\n%s", explicit0)
			}

			for name, function := range functions {
				conversion := coroOnlySliceToArrayPointer(function)
				if conversion == nil {
					if name != "Value0" {
						t.Fatalf("%s fixture has no SliceToArrayPointer", name)
					}
					continue
				}
				audit, err := newCoroPhysicalPureSSAAudit(universe, plan, function, CoroFrameRetentionParkABIV2)
				if err != nil {
					t.Fatalf("%s audit: %v", name, err)
				}
				helpers := strings.Join(universe.loweredRuntimeHelpers(audit.ctx, conversion), ",")
				length, exact := coroSliceToArrayPointerLen(conversion, audit.typeOf)
				if !exact {
					t.Fatalf("%s conversion has no exact array length", name)
				}
				if length == 0 && helpers != "" || length != 0 && helpers != "PanicSliceConvert" {
					t.Fatalf("%s length=%d helpers=%q", name, length, helpers)
				}
			}

			runCoroABITestPipeline(t, prog, module)
			for _, name := range []string{"Pointer4", "Value4", "ExplicitValue4", "NamedPointer4"} {
				resume := module.NamedFunction("foo." + name + "$coro.resume")
				if resume.IsNil() {
					t.Fatalf("post-split %s has no resume function", name)
				}
				requireCoroSliceToArrayFault(t, name+" resume", resume.String(), coroFaultSliceConvertV1)
			}
			for _, name := range []string{"Pointer0", "Value0", "GuardedExplicitValue0"} {
				resume := module.NamedFunction("foo." + name + "$coro.resume")
				if resume.IsNil() || strings.Contains(resume.String(), coroFaultPrepareHookV1) {
					t.Fatalf("post-split %s acquired a zero-length fault edge:\n%s", name, module.String())
				}
			}
			explicit0Resume := module.NamedFunction("foo.ExplicitValue0$coro.resume")
			if explicit0Resume.IsNil() {
				t.Fatal("post-split ExplicitValue0 has no resume function")
			}
			requireCoroSliceToArrayFault(t, "ExplicitValue0 resume", explicit0Resume.String(), coroFaultNilV1)

			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit slice-to-array conversion object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(coroFaultPrepareHookV1)) {
				t.Fatal("post-CoroSplit object lost the slice-to-array fault hook")
			}
		})
	}
}

func TestCoroSliceToArrayPointerSSAAndFailClosedBoundary(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, coroSliceToArrayFixture)
	for _, name := range []string{"Pointer4", "Pointer0"} {
		conversion := coroOnlySliceToArrayPointer(ssaPkg.Func(name))
		if conversion == nil || conversion.Pos() == token.NoPos {
			t.Fatalf("%s is not an explicit pointer conversion: %v", name, conversion)
		}
	}
	for _, name := range []string{"Value4", "ExplicitValue4", "ExplicitValue0", "GuardedExplicitValue0"} {
		function := ssaPkg.Func(name)
		conversion := coroOnlySliceToArrayPointer(function)
		deref := coroOnlySliceToArrayDeref(function)
		if conversion == nil || deref == nil {
			t.Fatalf("%s lacks conversion/deref shape:\n%s", name, function.String())
		}
		_, _, synthetic := coroSliceToArrayValueDeref(deref, nil)
		wantSynthetic := name == "Value4"
		if synthetic != wantSynthetic {
			t.Fatalf("%s synthetic deref = %t, want %t (conversion pos=%v, deref pos=%v)",
				name, synthetic, wantSynthetic, conversion.Pos(), deref.Pos())
		}
	}
	if conversion := coroOnlySliceToArrayPointer(ssaPkg.Func("Value0")); conversion != nil {
		t.Fatalf("[0]byte(value) unexpectedly emitted %s", conversion)
	}

	for _, test := range []struct {
		name     string
		wantFail bool
	}{
		{name: "Pointer4", wantFail: true},
		{name: "Pointer0"},
	} {
		function := ssaPkg.Func(test.name)
		conversion := coroOnlySliceToArrayPointer(function)
		audit := &coroPhysicalPureSSAAudit{
			fn:              function,
			reachableBlocks: coroPhysicalConstantReachableBlocks(function),
		}
		reason := audit.validateSliceToArrayPointer(conversion)
		if test.wantFail && !strings.Contains(reason, "explicit-status panic ABI") {
			t.Fatalf("%s legacy rejection = %q", test.name, reason)
		}
		if !test.wantFail && reason != "" {
			t.Fatalf("%s zero-length conversion rejected: %s", test.name, reason)
		}
	}
}

func TestSliceToArrayPointerZeroLengthPlainLowering(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	ssaPkg, _, files := buildGoSSAPkg(t, coroSliceToArrayFixture)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	pointer0 := module.NamedFunction("foo.Pointer0")
	if pointer0.IsNil() || strings.Contains(pointer0.String(), "PanicSliceConvert") {
		t.Fatalf("plain Pointer0 retained PanicSliceConvert:\n%s", module.String())
	}
	pointer4 := module.NamedFunction("foo.Pointer4")
	if pointer4.IsNil() || !strings.Contains(pointer4.String(), "PanicSliceConvert") {
		t.Fatalf("plain Pointer4 lost its checked lowering:\n%s", module.String())
	}
}

func TestSliceToArrayPointerZeroLengthReferenceSemantics(t *testing.T) {
	var nilSlice []byte
	if pointer := (*[0]byte)(nilSlice); pointer != nil {
		t.Fatalf("nil slice converted to non-nil *[0]byte: %p", pointer)
	}
	empty := make([]byte, 0)
	if pointer := (*[0]byte)(empty); pointer == nil {
		t.Fatal("empty non-nil slice converted to nil *[0]byte")
	}
	if panicked := func() (panicked bool) {
		defer func() { panicked = recover() != nil }()
		_ = *(*[0]byte)(nilSlice)
		return false
	}(); !panicked {
		t.Fatal("explicit dereference of nil *[0]byte did not panic")
	}
	_ = [0]byte(nilSlice)

	shortWithCapacity := make([]byte, 2, 4)
	for _, convert := range []struct {
		name string
		call func()
	}{
		{name: "pointer", call: func() { _ = (*[4]byte)(shortWithCapacity) }},
		{name: "value", call: func() { _ = [4]byte(shortWithCapacity) }},
	} {
		if panicked := func() (panicked bool) {
			defer func() { panicked = recover() != nil }()
			convert.call()
			return false
		}(); !panicked {
			t.Fatalf("%s conversion used cap instead of len", convert.name)
		}
	}

	storage := []byte{1, 2, 3, 4}
	pointer4 := (*[4]byte)(storage)
	pointer4[0] = 9
	if storage[0] != 9 {
		t.Fatal("slice-to-array-pointer conversion did not alias the backing storage")
	}
	value4 := [4]byte(storage)
	value4[0] = 7
	if storage[0] != 9 {
		t.Fatal("slice-to-array-value conversion did not copy the array value")
	}
}

func requireCoroSliceToArrayFault(t *testing.T, name, body string, kind uint32) {
	t.Helper()
	if got := strings.Count(body, "call void @"+coroFaultPrepareHookV1); got != 1 {
		t.Fatalf("%s fault prepare calls = %d, want one:\n%s", name, got, body)
	}
	if strings.Contains(body, "PanicSliceConvert") || strings.Contains(body, "AssertNilDeref") {
		t.Fatalf("%s retained a native-stack fault helper:\n%s", name, body)
	}
	hook := strings.Index(body, "call void @"+coroFaultPrepareHookV1)
	line := body[hook:]
	if end := strings.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	if !strings.Contains(line, fmt.Sprintf("i32 %d", kind)) {
		t.Fatalf("%s selected the wrong fault kind; hook=%q", name, line)
	}
}

func coroOnlySliceToArrayPointer(function *ssa.Function) *ssa.SliceToArrayPointer {
	if function == nil {
		return nil
	}
	var found *ssa.SliceToArrayPointer
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			if conversion, ok := instruction.(*ssa.SliceToArrayPointer); ok {
				if found != nil {
					return nil
				}
				found = conversion
			}
		}
	}
	return found
}

func coroOnlySliceToArrayDeref(function *ssa.Function) *ssa.UnOp {
	if function == nil {
		return nil
	}
	var found *ssa.UnOp
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			deref, ok := instruction.(*ssa.UnOp)
			if !ok || deref.Op != token.MUL {
				continue
			}
			if _, conversion := deref.X.(*ssa.SliceToArrayPointer); !conversion || found != nil {
				continue
			}
			found = deref
		}
	}
	return found
}

func compileCoroSliceToArrayFixture(
	t *testing.T,
	target *llssa.Target,
) (llssa.Program, llssa.Package, *EmissionUniverse, *coro.SSAPlan, map[string]*ssa.Function) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroSliceToArrayFixture)
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
	for _, name := range []string{
		"Pointer4", "Value4", "ExplicitValue4", "Pointer0", "Value0", "ExplicitValue0", "GuardedExplicitValue0", "NamedPointer4",
	} {
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
			if root, ok := functions[function.Name()]; ok && root == function {
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
	return prog, pkg, universe, plan, functions
}
