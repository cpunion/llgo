//go:build !llgo

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package cl

import (
	"fmt"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

// A dynamic child used to reconstruct and retire its pointer results inside
// awaitCoroChildWithRecoveryAndConsume, discard those SSA values, and load the
// now-cleared slot again at the descriptor join. Scalar-only dispatch tests do
// not expose this. Inspect the actual indirect call's result sink, rather than
// counting unrelated frame/header pointer loads or allowing a static rewrite.
func TestCoroManagedDispatchPointerResultLoadedAndRetiredOnce(t *testing.T) {
	for _, target := range []struct {
		name string
		spec *llssa.Target
	}{
		{name: "native64"},
		{name: "wasm32", spec: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		for _, shape := range []struct {
			name, result, value string
			pointerFields       []bool
		}{
			{"pointer", "*int", "value", []bool{true}},
			{"unsafe_pointer", "unsafe.Pointer", "unsafe.Pointer(value)", []bool{true}},
			{"multiple", "(*int, int, unsafe.Pointer)", "value, 7, unsafe.Pointer(value)", []bool{true, false, true}},
		} {
			for _, open := range []bool{false, true} {
				kind := "closed_coro"
				if open {
					kind = "mixed_plain_outcome_coro"
				}
				t.Run(target.name+"/"+kind+"/"+shape.name, func(t *testing.T) {
					source := fmt.Sprintf(`package foo
import "unsafe"
var _ unsafe.Pointer
var outcomePanic any
func Plain(value *int) %s { return %s }
func Async(value *int) %s { return %s }
func Outcome(value *int) %s {
    if outcomePanic != nil { panic(outcomePanic) }
    return %s
}
func Apply(callback func(*int) %s, value *int) %s {
    return callback(value)
}
`, shape.result, shape.value, shape.result, shape.value,
						shape.result, shape.value, shape.result, shape.result)
					ssaPkg, _, files := buildGoSSAPkg(t, source)
					var prog llssa.Program
					if target.spec == nil {
						prog = newLLSSAProg(t)
					} else {
						prog = newLLSSAProgForTarget(t, target.spec)
					}
					defer prog.Dispose()
					universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
					if err != nil {
						t.Fatal(err)
					}
					ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
					if err != nil {
						t.Fatal(err)
					}
					apply, plain, async := ssaPkg.Func("Apply"), ssaPkg.Func("Plain"), ssaPkg.Func("Async")
					dynamic := onlyCoroManagedDispatchValidationCall(t, apply)
					if dynamic.Common().StaticCallee() != nil {
						t.Fatal("fixture lost its function-parameter call")
					}
					ids := universe.FunctionIDConfig()
					ids.CoroABI = coro.PhysicalABIV1
					ids.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
					ids.ArchiveReady = true
					plan, err := coro.AnalyzeSSA(ssaPkg.Prog,
						coro.Roots{{Function: apply, Demand: coro.AsyncDemand}},
						coro.SSAConfig{
							EmissionUniverse: ssaUniverse, FunctionIDs: ids,
							OutcomeMode: coro.OutcomeExplicitStatus, MaxPlainInstructions: 64,
							ClassifyLocalBody: universe.CoroLocalBodyFacts,
							ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
								switch fn {
								case plain:
									return coro.SSAFunctionPolicy{NeedsDispatch: true}, nil
								case async:
									return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, NeedsDispatch: true}, nil
								default:
									return coro.SSAFunctionPolicy{}, nil
								}
							},
							ClassifyUnknownCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.UnknownTarget, error) {
								if open && call == dynamic {
									return coro.UnknownManagedDispatch, nil
								}
								return coro.UnknownManaged, nil
							},
							ClassifyClosedDynamicCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.SSAClosedDynamicCallCertificate, bool, error) {
								if !open && call == dynamic {
									return coro.SSAClosedDynamicCallCertificate{Targets: []*ssa.Function{async}}, true, nil
								}
								return coro.SSAClosedDynamicCallCertificate{}, false, nil
							},
						})
					if err != nil {
						t.Fatal(err)
					}
					callPlan, present := plan.CallPlan(dynamic)
					if !present || callPlan.Rep != coro.Dispatch || callPlan.Open != open {
						t.Fatalf("fixture stopped exercising descriptor dispatch: %+v, present=%t", callPlan, present)
					}
					if !open && !coroManagedDispatchPublishedCoroutineOnly(plan, callPlan) {
						t.Fatal("closed fixture lost its coroutine-only descriptor proof")
					}
					compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
					enableCoroPreemptCompilation(compilation)
					compilation.PanicABI = coro.PanicExplicitStatusABIV0
					compilation.FuncRepABI = coro.FuncRepABIV2
					compiled, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files,
						goembed.VarMap{}, PackageOptions{Compilation: compilation})
					if err != nil {
						t.Fatal(err)
					}
					module := compiled.Module()
					defer module.Dispose()
					if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
						t.Fatalf("verify dynamic result module: %v\n%s", err, module.String())
					}
					function := requireCoroPhysicalFunction(t, module, "foo.Apply")
					assertCoroDynamicResultSingleClaim(t, function, shape.pointerFields, open)
					runCoroABITestPipeline(t, prog, module)
					if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
						t.Fatalf("verify dynamic result after CoroSplit: %v\n%s", err, module.String())
					}
					if module.NamedFunction("foo.Apply$coro.resume").IsNil() {
						t.Fatal("CoroSplit removed the dynamic-child continuation")
					}
				})
			}
		}
	}
}

func assertCoroDynamicResultSingleClaim(t *testing.T, function llvm.Value, pointerFields []bool, mixed bool) {
	t.Helper()
	var sink llvm.Value
	plainCalls, outcomeCalls, coroCalls := 0, 0, 0
	for _, block := range function.BasicBlocks() {
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if instruction.InstructionOpcode() != llvm.Call || !instruction.CalledValue().IsAFunction().IsNil() {
				continue
			}
			// One source pointer parameter: plain(env,arg), outcome(g,out,
			// completion,env,arg), or coro(g,out,env,arg), plus the callee operand.
			switch instruction.OperandsCount() {
			case 3:
				plainCalls++
			case 6:
				outcomeCalls++
			case 5:
				if instruction.Type().TypeKind() == llvm.PointerTypeKind {
					coroCalls++
					sink = instruction.Operand(1)
				}
			}
		}
	}
	wantOther := 0
	if mixed {
		wantOther = 1
	}
	if coroCalls != 1 || plainCalls != wantOther || outcomeCalls != wantOther || sink.IsNil() {
		t.Fatalf("expected real indirect descriptor branches plain/outcome/coro=%d/%d/1, got %d/%d/%d:\n%s",
			wantOther, wantOther, plainCalls, outcomeCalls, coroCalls, function.String())
	}
	type access struct {
		block llvm.BasicBlock
		index int
	}
	loads := make([][]access, len(pointerFields))
	retirements := make([][]access, len(pointerFields))
	for _, block := range function.BasicBlocks() {
		index := 0
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			index++
			var address llvm.Value
			switch instruction.InstructionOpcode() {
			case llvm.Load:
				address = instruction.Operand(0)
			case llvm.Store:
				address = instruction.Operand(1)
			default:
				continue
			}
			if address.IsAGetElementPtrInst().IsNil() || address.OperandsCount() != 3 || address.Operand(0) != sink {
				continue
			}
			zero, field := address.Operand(1), address.Operand(2)
			if zero.IsAConstantInt().IsNil() || zero.ZExtValue() != 0 || field.IsAConstantInt().IsNil() || field.ZExtValue() >= uint64(len(pointerFields)) {
				t.Fatalf("unexpected result-slot projection: %s", address.String())
			}
			i := int(field.ZExtValue())
			if instruction.InstructionOpcode() == llvm.Load {
				loads[i] = append(loads[i], access{block, index})
			} else if !instruction.Operand(0).IsAConstantPointerNull().IsNil() && strings.Contains(instruction.String(), "store volatile ptr null") {
				retirements[i] = append(retirements[i], access{block, index})
			}
		}
	}
	for field, pointer := range pointerFields {
		wantRetire := 0
		if pointer {
			wantRetire = 1
		}
		if len(loads[field]) != 1 || len(retirements[field]) != wantRetire {
			t.Fatalf("result field %d loaded %d times and retired %d times; want 1/%d (a second load can read a cleared pointer):\n%s",
				field, len(loads[field]), len(retirements[field]), wantRetire, function.String())
		}
		if !pointer {
			continue
		}
		retire := retirements[field][0]
		// The common normal-result reconstruction loads the complete tuple
		// before retiring any pointer cell, on both inline and resumed paths.
		for other, accesses := range loads {
			if len(accesses) != 1 || accesses[0].block != retire.block || accesses[0].index >= retire.index {
				t.Fatalf("result field %d was not loaded before retirement of pointer field %d:\n%s", other, field, function.String())
			}
		}
	}
}
