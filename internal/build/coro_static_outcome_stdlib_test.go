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

package build

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroStaticStdlibAtomicMethodPublishesOutcomeEntry(t *testing.T) {
	t.Setenv(llgoBuildCache, "off")
	source := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(source, []byte(`package main

import "sync/atomic"

var value atomic.Uint64

func main() { value.Add(1) }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	var (
		atomicPlan  coro.FunctionPlan
		atomicFacts coro.SSAFunctionBodyFacts
		atomicID    coro.FunctionID
		found       bool
		factsFound  bool
		diagnostic  string
		candidates  int
	)
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		if input.EmissionUniverse != nil && input.localBodyFacts != nil {
			for _, fn := range input.EmissionUniverse.Functions() {
				if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil || fn.Name() != "Add" ||
					!strings.HasSuffix(fn.Pkg.Pkg.Path(), "/sync/atomic") ||
					!strings.Contains(fn.String(), "Uint64") {
					continue
				}
				facts, err := input.localBodyFacts(fn)
				if err != nil {
					return nil, err
				}
				atomicFacts = facts
				ids := coro.FunctionIDConfig{}
				if input.augmentFunctionIDs != nil {
					ids = input.augmentFunctionIDs(ids)
				}
				atomicID, err = coro.StableFunctionID(fn, ids)
				if err != nil {
					return nil, err
				}
				factsFound = true
				break
			}
		}
		plan, err := defaultCoroPlanBuilder(input)
		if err != nil || !factsFound {
			return plan, err
		}
		if got, ok := plan.BasePlan().Lookup(atomicID); ok {
			atomicPlan = got
			diagnostic += fmt.Sprintf("builder-plan=%+v; ", got)
		}
		return plan, nil
	}
	conf.CoroPlanObserver = func(_ *ssa.Package, plan *coro.SSAPlan) {
		if found || plan == nil {
			return
		}
		for _, function := range plan.Functions() {
			fn := function.Function
			if fn != nil && fn.Pkg != nil && fn.Pkg.Pkg != nil && fn.Name() == "Add" {
				candidates++
				if candidates <= 32 {
					diagnostic += fmt.Sprintf("candidate=%s path=%s; ", fn.String(), fn.Pkg.Pkg.Path())
				}
			}
			if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil ||
				!strings.HasSuffix(fn.Pkg.Pkg.Path(), "/sync/atomic") || fn.Name() != "Add" ||
				!strings.Contains(fn.String(), "Uint64") {
				continue
			}
			found = true
			atomicPlan = function.Plan
		}
		if !found {
			return
		}
		for _, function := range plan.Functions() {
			fn := function.Function
			if fn == nil {
				continue
			}
			for _, block := range fn.Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(ssa.CallInstruction)
					if !ok {
						continue
					}
					callee := "<dynamic>"
					if common := call.Common(); common != nil && common.StaticCallee() != nil {
						callee = common.StaticCallee().String()
					}
					callPlan, planned := plan.CallPlan(call)
					relevant := callee != "<dynamic>" && strings.Contains(callee, "Uint64") && strings.HasSuffix(callee, ".Add")
					if !relevant {
						for _, target := range callPlan.Targets {
							if target == atomicPlan.ID {
								relevant = true
								break
							}
						}
					}
					if !relevant && fn.Pkg != nil && fn.Pkg.Pkg != nil &&
						strings.HasSuffix(fn.Pkg.Pkg.Path(), "/sync/atomic") && fn.Name() == "Add" {
						relevant = true
					}
					if !relevant {
						continue
					}
					diagnostic += fmt.Sprintf(
						"owner=%s call=%s callee=%s elided=%t planned=%t plan=%+v; ",
						fn.String(), call.String(), callee, plan.ElidesCall(call), planned, callPlan,
					)
				}
			}
		}
	}
	_, err := Do([]string{source}, conf)
	if err != nil {
		t.Fatalf("compile stdlib static-outcome fixture: %v", err)
	}
	if !found {
		t.Fatalf("sync/atomic.(*Uint64).Add is absent from the whole-program coroutine plan (%d Add candidates): %s", candidates, diagnostic)
	}
	if !factsFound {
		t.Fatal("sync/atomic.(*Uint64).Add has no frozen ProgramIR local-body facts")
	}
	if !atomicPlan.AtomicCostProof.ProvesOutcomePlain() || atomicPlan.AtomicCost == 0 {
		t.Fatalf(
			"sync/atomic.(*Uint64).Add plan = %+v, facts = %+v, want a static outcome entry; source calls: %s",
			atomicPlan, atomicFacts, diagnostic,
		)
	}
}

func TestCoroSpawnWaitGroupStaticOutcomeCoverage(t *testing.T) {
	t.Setenv(llgoBuildCache, "off")
	source := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(source, []byte(`package main

import "sync"

func main() {
	var done sync.WaitGroup
	done.Add(1)
	go done.Done()
	done.Wait()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	targets := []string{
		"(*sync.WaitGroup).Add",
		"(*sync.WaitGroup).Done",
		"runtime.semaRelease",
		"github.com/goplus/llgo/runtime/internal/runtime.coroKeyedPostOneV2",
	}
	seen := make(map[string]coro.FunctionPlan)
	frozenFacts := make(map[string]coro.SSAFunctionBodyFacts)
	allFacts := make(map[*ssa.Function]coro.SSAFunctionBodyFacts)
	traces := make(map[string]string)
	observed := false
	conf := NewDefaultConf(ModeGen)
	conf.ForceRebuild = true
	conf.CoroPlanBuilder = func(input CoroPlanInput) (*coro.SSAPlan, error) {
		if input.EmissionUniverse != nil && input.localBodyFacts != nil {
			for _, fn := range input.EmissionUniverse.Functions() {
				if fn == nil {
					continue
				}
				facts, err := input.localBodyFacts(fn)
				if err != nil {
					return nil, err
				}
				allFacts[fn] = facts
				for _, target := range targets {
					if fn.String() == target || strings.HasSuffix(fn.String(), target) || strings.Contains(fn.String(), target) {
						frozenFacts[target] = facts
					}
				}
			}
		}
		return defaultCoroPlanBuilder(input)
	}
	conf.CoroPlanObserver = func(_ *ssa.Package, plan *coro.SSAPlan) {
		if plan == nil || observed {
			return
		}
		observed = true
		for _, function := range plan.Functions() {
			if function.Function == nil {
				continue
			}
			name := function.Function.String()
			if strings.Contains(name, "WaitGroup") {
				t.Logf("WaitGroup candidate: %s", name)
			}
			if name == "(*sync.WaitGroup).Add" {
				var text bytes.Buffer
				function.Function.WriteTo(&text)
				t.Logf("WaitGroup.Add SSA:\n%s", text.String())
				for _, lowered := range plan.LoweredCalls(function.Function) {
					targetPlan, _ := plan.FunctionPlan(lowered.Target)
					t.Logf("WaitGroup.Add lowered %s -> %s: no-unwind=%t unwind-only=%t status-elided=%t raw=%t plan=%+v",
						lowered.LogicalName, lowered.Target.String(), lowered.NoUnwind, lowered.UnwindOnly,
						lowered.ExplicitStatusElided, lowered.RawPlain, targetPlan)
				}
				for _, block := range function.Function.Blocks {
					for _, instruction := range block.Instrs {
						call, ok := instruction.(ssa.CallInstruction)
						if !ok {
							continue
						}
						callPlan, planned := plan.CallPlan(call)
						t.Logf("WaitGroup.Add call %s: planned=%t elided=%t plan=%+v", call.String(), planned, plan.ElidesCall(call), callPlan)
					}
				}
				for _, atomicBlock := range allFacts[function.Function].AtomicPath.Blocks {
					for _, occurrence := range atomicBlock.Calls {
						callPlan, planned := plan.CallPlan(occurrence.Instruction)
						for _, id := range callPlan.Targets {
							target, _ := plan.Function(id)
							targetPlan, _ := plan.BasePlan().Lookup(id)
							t.Logf("WaitGroup.Add reachable call %s: planned=%t target=%s plan=%+v facts=%+v",
								occurrence.Instruction.String(), planned, target.String(), targetPlan, allFacts[target])
						}
					}
				}
			}
			for _, target := range targets {
				if name == target || strings.HasSuffix(name, target) || strings.Contains(name, target) {
					seen[target] = function.Plan
					traces[target] = fmt.Sprintf(
						"wait=%s; await=%s; yield=%s",
						plan.SuspensionEffectTrace(function.Function, coro.WaitForeign),
						plan.SuspensionEffectTrace(function.Function, coro.AwaitStructured),
						plan.SuspensionEffectTrace(function.Function, coro.YieldOnly),
					)
					t.Logf("matched %s as %s", target, name)
				}
			}
		}
		for _, rootName := range []string{"(*sync.WaitGroup).Add", "github.com/goplus/llgo/runtime/internal/lib/runtime.sync_fatal", "github.com/goplus/llgo/runtime/internal/lib/runtime.sync_runtime_Semrelease"} {
			var root *ssa.Function
			for _, function := range plan.Functions() {
				if function.Function != nil && function.Function.String() == rootName {
					root = function.Function
					break
				}
			}
			if root == nil {
				continue
			}
			seenClosure := map[*ssa.Function]bool{}
			var visit func(*ssa.Function, int)
			visit = func(owner *ssa.Function, depth int) {
				if owner == nil || seenClosure[owner] || depth > 5 {
					return
				}
				seenClosure[owner] = true
				ownerPlan, _ := plan.FunctionPlan(owner)
				facts := allFacts[owner]
				t.Logf("static closure root=%s depth=%d owner=%s static=%t emission=%s effect=%s exec=%s local=%t lowered=%d",
					rootName, depth, owner.String(), ownerPlan.HasStaticOutcome(), ownerPlan.Emission,
					ownerPlan.Effect, ownerPlan.Exec, facts.StaticOutcomeLocal, len(plan.LoweredCalls(owner)))
				if facts.AtomicPath != nil {
					for _, block := range facts.AtomicPath.Blocks {
						for _, occurrence := range block.Calls {
							callPlan, ok := plan.CallPlan(occurrence.Instruction)
							if !ok || len(callPlan.Targets) != 1 {
								continue
							}
							target, _ := plan.Function(callPlan.Targets[0])
							visit(target, depth+1)
						}
					}
				}
				for _, lowered := range plan.LoweredCalls(owner) {
					if !lowered.ExplicitStatusElided {
						visit(lowered.Target, depth+1)
					}
				}
			}
			visit(root, 0)
		}
		for _, function := range plan.Functions() {
			fn := function.Function
			if fn == nil || fn.String() != "github.com/goplus/llgo/runtime/internal/runtime.StringCat" {
				continue
			}
			t.Logf("StringCat owner plan=%+v facts=%+v", function.Plan, allFacts[fn])
			for _, lowered := range plan.LoweredCalls(fn) {
				targetPlan, _ := plan.FunctionPlan(lowered.Target)
				t.Logf("StringCat lowered %s -> %s: record=%+v target=%+v", lowered.LogicalName, lowered.Target, lowered, targetPlan)
			}
			for _, block := range fn.Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(ssa.CallInstruction)
					if !ok {
						continue
					}
					callPlan, planned := plan.CallPlan(call)
					t.Logf("StringCat source call %s: planned=%t elided=%t plan=%+v", call, planned, plan.ElidesCall(call), callPlan)
					for _, targetID := range callPlan.Targets {
						targetPlan, _ := plan.BasePlan().Lookup(targetID)
						target, _ := plan.Function(targetID)
						t.Logf("StringCat source target %s: %+v", target, targetPlan)
					}
				}
			}
		}
	}
	if _, err := Do([]string{source}, conf); err != nil {
		t.Fatalf("compile spawn/WaitGroup static-outcome fixture: %v", err)
	}
	for _, name := range targets {
		plan, ok := seen[name]
		if !ok {
			t.Errorf("%s is absent from the whole-program plan", name)
			continue
		}
		t.Logf("%s: emission=%s static=%t declared=%s local=%s effect=%s declared-exec=%s local-exec=%s exec=%s proof=%s cost=%d facts=%+v trace=%s", name, plan.Emission, plan.StaticOutcome, plan.DeclaredEffect, plan.LocalEffect, plan.Effect, plan.DeclaredExec, plan.LocalExec, plan.Exec, plan.AtomicCostProof, plan.AtomicCost, frozenFacts[name], traces[name])
	}
}
