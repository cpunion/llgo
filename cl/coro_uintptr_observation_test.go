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
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

const coroUintptrObservationFixture = `package foo

import "unsafe"

func Endpoint(first, last unsafe.Pointer, offset uintptr) bool {
	return uintptr(first) <= uintptr(last)+offset
}

func Overlaps(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 { return false }
	elemSize := unsafe.Sizeof(a[0])
	if elemSize == 0 { return false }
	return uintptr(unsafe.Pointer(&a[0])) <= uintptr(unsafe.Pointer(&b[len(b)-1]))+(elemSize-1) &&
		uintptr(unsafe.Pointer(&b[0])) <= uintptr(unsafe.Pointer(&a[len(a)-1]))+(elemSize-1)
}

type OverlapRecord struct {
	Code uint32
	Text string
}

func GenericOverlaps[E any](a, b []E) bool {
	if len(a) == 0 || len(b) == 0 { return false }
	elemSize := unsafe.Sizeof(a[0])
	if elemSize == 0 { return false }
	return uintptr(unsafe.Pointer(&a[0])) <= uintptr(unsafe.Pointer(&b[len(b)-1]))+(elemSize-1) &&
		uintptr(unsafe.Pointer(&b[0])) <= uintptr(unsafe.Pointer(&a[len(a)-1]))+(elemSize-1)
}

func UseGenericOverlaps(a, b []OverlapRecord) bool { return GenericOverlaps(a, b) }
`

func TestCoroPointerUintptrGenericAffineObservationShape(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, coroUintptrObservationFixture)
	origin := ssaPkg.Func("GenericOverlaps")
	var instance *ssa.Function
	for function := range ssautil.AllFunctions(ssaPkg.Prog) {
		if function == nil || function.Origin() != origin || len(function.TypeArgs()) != 1 {
			continue
		}
		named, ok := types.Unalias(function.TypeArgs()[0]).(*types.Named)
		if ok && named.Obj() != nil && named.Obj().Name() == "OverlapRecord" {
			instance = function
			break
		}
	}
	if instance == nil {
		t.Fatal("GenericOverlaps[OverlapRecord] instance was not materialized")
	}
	found, accepted, instructions := 0, 0, 0
	for _, block := range instance.Blocks {
		for _, instruction := range block.Instrs {
			if _, debug := instruction.(*ssa.DebugRef); !debug {
				instructions++
			}
			conversion, ok := instruction.(*ssa.Convert)
			if !ok || !coroFrameRetentionPointerToUintptr(conversion) {
				continue
			}
			found++
			if coroPointerUintptrScalarTerminal(conversion) {
				accepted++
			}
		}
	}
	if found != 4 || accepted != found {
		var dump bytes.Buffer
		ssa.WriteFunction(&dump, instance)
		t.Fatalf("generic overlaps pointer words found=%d accepted=%d, want four exact scalar terminals\n%s", found, accepted, dump.String())
	}
	if instructions > coro.DefaultMaxPlainInstructions {
		t.Fatalf("generic overlaps instruction count=%d unexpectedly exceeds default preemption budget=%d", instructions, coro.DefaultMaxPlainInstructions)
	}

	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: instance, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse: ssaUniverse,
		FunctionIDs:      functionIDs,
		OutcomeMode:      coro.OutcomeExplicitStatus,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == instance {
				return coro.SSAFunctionPolicy{Exec: coro.MayUnwind}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	functionPlan, planned := plan.FunctionPlan(instance)
	if !planned || functionPlan.Emission != coro.EmitCoroutine || functionPlan.Exec.Contains(coro.NeedsPreempt) ||
		functionPlan.Effect&^coro.OutcomeStructured != coro.NoSuspend {
		t.Fatalf("generic overlaps plan = %+v, present=%t; want non-preempting outcome-only coroutine", functionPlan, planned)
	}
	audit, err := newCoroPhysicalPureSSAAudit(universe, plan, instance, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range instance.Blocks {
		for _, instruction := range block.Instrs {
			conversion, ok := instruction.(*ssa.Convert)
			if !ok || !coroFrameRetentionPointerToUintptr(conversion) {
				continue
			}
			if reason := audit.validateConvert(conversion); reason != "" {
				t.Fatalf("generic overlaps active pointer-word validation rejected %q: %s", conversion, reason)
			}
		}
	}
}

func TestCoroPointerUintptrAffineObservationNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name        string
		target      *llssa.Target
		uintptrType string
	}{
		{name: "native", uintptrType: "i64"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}, uintptrType: "i32"},
	} {
		t.Run(target.name, func(t *testing.T) {
			prog, pkg, plan, functions := compileCoroUintptrObservationFixture(t, target.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify uintptr observation before CoroSplit: %v\n%s", err, module.String())
			}

			for name, function := range functions {
				functionPlan, ok := plan.FunctionPlan(function)
				if !ok || functionPlan.Emission != coro.EmitCoroutine ||
					functionPlan.Exec.Contains(coro.NeedsPreempt) ||
					functionPlan.Effect&^coro.OutcomeStructured != coro.NoSuspend {
					t.Fatalf("%s plan = %+v, present=%t; want non-preempting outcome-only coroutine", name, functionPlan, ok)
				}
				body := requireCoroPhysicalFunction(t, module, "foo."+name).String()
				if strings.Contains(body, coroAwaitPrepareHookV1) || strings.Contains(body, coroPreemptPollHookV1) {
					t.Fatalf("%s scalar observation acquired an await/preempt hook:\n%s", name, body)
				}
				if got := strings.Count(body, "ptrtoint ptr"); got < 2 || !strings.Contains(body, "to "+target.uintptrType) {
					t.Fatalf("%s ptrtoint lowering is incomplete for %s (count=%d):\n%s", name, target.uintptrType, got, body)
				}
			}
			assertCoroUintptrAffineIR(t, "Endpoint", requireCoroPhysicalFunction(t, module, "foo.Endpoint").String())

			runCoroABITestPipeline(t, prog, module)
			for name := range functions {
				resume := module.NamedFunction("foo." + name + "$coro.resume")
				if resume.IsNil() {
					t.Fatalf("post-split %s has no resume function", name)
				}
				resumeIR := resume.String()
				if strings.Contains(resumeIR, coroAwaitPrepareHookV1) || strings.Contains(resumeIR, coroPreemptPollHookV1) {
					t.Fatalf("post-split %s acquired an await/preempt hook:\n%s", name, resumeIR)
				}
			}
			assertCoroUintptrAffineIR(t, "Endpoint resume", module.NamedFunction("foo.Endpoint$coro.resume").String())

			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit uintptr observation object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 {
				t.Fatal("uintptr observation emitted an empty object")
			}
		})
	}
}

func TestCoroPointerUintptrAffineObservationRemainsFailClosed(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "return", body: "return uintptr(pointer) + offset"},
		{name: "store", body: "escaped = uintptr(pointer) + offset; return 0"},
		{name: "call", body: "consume(uintptr(pointer) + offset); return 0"},
		{name: "multiply", body: "return (uintptr(pointer) * offset) == 0"},
		{name: "reconstruct", body: "return uintptr(unsafe.Pointer(uintptr(pointer) + offset)) == 0"},
		{name: "pointer offset", body: "return uintptr(pointer) <= uintptr(other) + uintptr(pointer)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := "uintptr"
			if strings.Contains(test.body, "==") || strings.Contains(test.body, "<=") {
				result = "bool"
			}
			source := `package foo
import "unsafe"
var escaped uintptr
func consume(uintptr)
func Root(pointer, other unsafe.Pointer, offset uintptr) ` + result + ` { ` + test.body + ` }
`
			ssaPkg, _, _ := buildGoSSAPkg(t, source)
			root := ssaPkg.Func("Root")
			found := 0
			accepted := 0
			for _, block := range root.Blocks {
				for _, instruction := range block.Instrs {
					conversion, ok := instruction.(*ssa.Convert)
					if !ok || !coroFrameRetentionPointerToUintptr(conversion) {
						continue
					}
					found++
					if coroPointerUintptrScalarTerminal(conversion) {
						accepted++
					}
				}
			}
			if found == 0 {
				t.Fatal("negative fixture has no pointer-to-uintptr conversion")
			}
			if accepted == found {
				t.Fatalf("all %d unsafe pointer words acquired scalar-terminal authority:\n%s", found, root.String())
			}
		})
	}
}

func TestCoroPointerUintptrAffineObservationRequiresNonPreemptingPlan(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, coroUintptrObservationFixture)
	root := ssaPkg.Func("Endpoint")
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		OutcomeMode:          coro.OutcomeExplicitStatus,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == root {
				return coro.SSAFunctionPolicy{Exec: coro.MayUnwind | coro.NeedsPreempt}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	functionPlan, ok := plan.FunctionPlan(root)
	if !ok || !functionPlan.Exec.Contains(coro.NeedsPreempt) {
		t.Fatalf("preempting fixture plan = %+v, present=%t", functionPlan, ok)
	}
	audit, err := newCoroPhysicalPureSSAAudit(universe, plan, root, "")
	if err != nil {
		t.Fatal(err)
	}
	var conversion *ssa.Convert
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			candidate, ok := instruction.(*ssa.Convert)
			if ok && coroFrameRetentionPointerToUintptr(candidate) && coroPointerUintptrScalarTerminal(candidate) {
				conversion = candidate
			}
		}
	}
	if conversion == nil {
		t.Fatal("preempting fixture has no structural affine scalar terminal")
	}
	if reason := audit.validateConvert(conversion); !strings.Contains(reason, "not bound to an exact managed-child/worker") {
		t.Fatalf("NeedsPreempt affine observation rejection = %q", reason)
	}
}

func assertCoroUintptrAffineIR(t *testing.T, name, body string) {
	t.Helper()
	secondPointer := strings.LastIndex(body, "ptrtoint ptr")
	if secondPointer < 0 {
		t.Fatalf("%s has no affine pointer word:\n%s", name, body)
	}
	affine := body[secondPointer:]
	add, comparison := strings.Index(affine, " add "), strings.Index(affine, "icmp ule")
	if add < 0 || comparison < add {
		t.Fatalf("%s does not lower pointer+offset before comparison:\n%s", name, body)
	}
	span := affine[:comparison]
	for _, hook := range []string{coroAwaitPrepareHookV1, coroPreemptPollHookV1, "llvm.coro.suspend"} {
		if strings.Contains(span, hook) {
			t.Fatalf("%s affine pointer lifetime crosses %s:\n%s", name, hook, body)
		}
	}
}

func compileCoroUintptrObservationFixture(
	t *testing.T,
	target *llssa.Target,
) (llssa.Program, llssa.Package, *coro.SSAPlan, map[string]*ssa.Function) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroUintptrObservationFixture)
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
		"Endpoint": ssaPkg.Func("Endpoint"),
		"Overlaps": ssaPkg.Func("Overlaps"),
	}
	var roots coro.Roots
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
		OutcomeMode:          coro.OutcomeExplicitStatus,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			for _, root := range functions {
				if function == root {
					// MayUnwind forces an explicit OutcomeStructured physical body
					// without adding a real suspension or preemption capability.
					return coro.SSAFunctionPolicy{Exec: coro.MayUnwind}, nil
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
	compilation.CoroProfile = CoroProfileStackless
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
