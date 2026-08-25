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
	"fmt"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// coroFunctionProgramCapabilities computes the reusable per-function closure
// of optional runtime services. Local physical recipes provide worker seeds,
// the runtime/debug API provides the panic-on-fault seed, and bodyless archive
// declarations provide producer-owned transitive seeds. All ordinary, dynamic,
// interface, spawn, and compiler-lowered propagation reuses the frozen
// CallPlan rather than rediscovering targets during package codegen.
func (c *Compilation) coroFunctionProgramCapabilities() (
	map[*ssa.Function]coro.ProgramCapabilities,
	error,
) {
	if c == nil {
		return nil, fmt.Errorf("coroutine function capabilities require a compilation")
	}
	c.coroCapabilities.Do(func() {
		if err := c.preflightCoroPlan(); err != nil {
			c.coroCapabilitiesErr = err
			return
		}
		universe := c.immutableEmissionUniverse()
		if universe == nil || universe.coroProgramIR == nil {
			c.coroCapabilitiesErr = fmt.Errorf("coroutine function capabilities require a prepared ProgramIR")
			return
		}
		worker, err := universe.coroProgramIR.workerProgramCapabilitySeeds()
		if err != nil {
			c.coroCapabilitiesErr = err
			return
		}
		c.coroCapabilitiesByFunc, c.coroCapabilitiesErr = deriveCoroFunctionProgramCapabilities(
			c.CoroPlan, worker, c.CoroLibraryEffects,
		)
	})
	return c.coroCapabilitiesByFunc, c.coroCapabilitiesErr
}

func (c *Compilation) coroFunctionProgramCapability(
	function *ssa.Function,
) (coro.ProgramCapabilities, error) {
	if function == nil {
		return 0, fmt.Errorf("coroutine function capability requires an exact function")
	}
	capabilities, err := c.coroFunctionProgramCapabilities()
	if err != nil {
		return 0, err
	}
	result, ok := capabilities[function]
	if !ok {
		return 0, fmt.Errorf("function %q is absent from coroutine capability closure", function.Name())
	}
	return result, nil
}

func deriveCoroFunctionProgramCapabilities(
	plan *coro.SSAPlan,
	worker map[*ssa.Function]bool,
	imported map[*ssa.Function]coro.LibraryEffectFunction,
) (map[*ssa.Function]coro.ProgramCapabilities, error) {
	if plan == nil {
		return nil, fmt.Errorf("coroutine function capabilities require an SSA plan")
	}
	functions := plan.Functions()
	capabilities := make(map[*ssa.Function]coro.ProgramCapabilities, len(functions))
	reverse := make(map[*ssa.Function]map[*ssa.Function]struct{}, len(functions))

	addEdge := func(caller, callee *ssa.Function) error {
		if caller == nil || callee == nil {
			return fmt.Errorf("coroutine function capability graph contains a nil edge")
		}
		if _, ok := capabilities[callee]; !ok {
			return fmt.Errorf("coroutine function capability edge targets unplanned function %q", callee.Name())
		}
		calleePlan, planned := plan.FunctionPlan(callee)
		if !planned {
			return fmt.Errorf("coroutine function capability edge lost target plan for %q", callee.Name())
		}
		if calleePlan.Emission == coro.EmitNone {
			return nil
		}
		callers := reverse[callee]
		if callers == nil {
			callers = make(map[*ssa.Function]struct{})
			reverse[callee] = callers
		}
		callers[caller] = struct{}{}
		return nil
	}

	for _, item := range functions {
		function := item.Function
		if function == nil {
			return nil, fmt.Errorf("coroutine function capability plan contains a nil function")
		}
		if err := item.Plan.Emission.Validate(); err != nil {
			return nil, fmt.Errorf("coroutine function %q: %w", function.Name(), err)
		}
		if _, duplicate := capabilities[function]; duplicate {
			return nil, fmt.Errorf("coroutine function capability plan repeats %q", function.Name())
		}
		panicOnFault := function.Pkg != nil && function.Pkg.Pkg != nil &&
			llssa.PathOf(function.Pkg.Pkg) == "runtime/debug" &&
			function.Name() == "SetPanicOnFault"
		capability := coro.NewProgramCapabilities(worker[function], panicOnFault)
		if fact, ok := imported[function]; ok {
			if !fact.ProgramCapabilities.Valid() {
				return nil, fmt.Errorf("imported function %q has invalid program capabilities %#x", function.Name(), fact.ProgramCapabilities)
			}
			capability |= fact.ProgramCapabilities
		}
		capabilities[function] = capability
	}
	for function := range worker {
		if _, planned := capabilities[function]; !planned {
			return nil, fmt.Errorf("worker capability seed targets unplanned function %q", function.Name())
		}
	}
	for function := range imported {
		if _, planned := capabilities[function]; !planned {
			return nil, fmt.Errorf("imported capability seed targets unplanned function %q", function.Name())
		}
	}

	for _, item := range functions {
		caller := item.Function
		if item.Plan.Emission == coro.EmitNone || plan.IgnoresBody(caller) {
			continue
		}
		for _, block := range caller.Blocks {
			if block == nil {
				continue
			}
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || plan.ElidesCall(call) {
					continue
				}
				callPlan, planned := plan.CallPlan(call)
				if planned {
					for _, targetID := range callPlan.Targets {
						target, ok := plan.Function(targetID)
						if !ok || target == nil {
							return nil, fmt.Errorf("call in %q targets missing function %q", caller.Name(), targetID)
						}
						if err := addEdge(caller, target); err != nil {
							return nil, err
						}
					}
					continue
				}
				common := call.Common()
				if common != nil {
					if _, builtin := common.Value.(*ssa.Builtin); builtin {
						continue
					}
				}
				if common == nil || common.IsInvoke() || common.StaticCallee() == nil {
					return nil, fmt.Errorf("dynamic call in %q has no frozen coroutine CallPlan", caller.Name())
				}
				if _, ok := plan.FunctionPlan(common.StaticCallee()); ok {
					if err := addEdge(caller, common.StaticCallee()); err != nil {
						return nil, err
					}
				}
			}
		}
		for _, call := range plan.LoweredCalls(caller) {
			if call.Target == nil {
				return nil, fmt.Errorf("lowered call %q in %q has no target", call.LogicalName, caller.Name())
			}
			if _, ok := capabilities[call.Target]; !ok {
				return nil, fmt.Errorf("lowered call %q in %q targets an unplanned function", call.LogicalName, caller.Name())
			}
			if err := addEdge(caller, call.Target); err != nil {
				return nil, err
			}
		}
	}

	queue := make([]*ssa.Function, 0, len(capabilities))
	for function, capability := range capabilities {
		if capability != 0 {
			queue = append(queue, function)
		}
	}
	for len(queue) != 0 {
		callee := queue[0]
		queue = queue[1:]
		for caller := range reverse[callee] {
			joined := capabilities[caller] | capabilities[callee]
			if joined == capabilities[caller] {
				continue
			}
			if !joined.Valid() {
				return nil, fmt.Errorf("function %q acquired invalid program capabilities %#x", caller.Name(), joined)
			}
			capabilities[caller] = joined
			queue = append(queue, caller)
		}
	}
	return capabilities, nil
}
