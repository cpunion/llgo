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
// of optional runtime services. Local physical recipes provide worker and
// exact native-default-fault seeds, the runtime/debug API provides the dynamic
// panic-on-fault seed, and bodyless archive declarations provide producer-owned
// transitive seeds. All ordinary, dynamic, interface, spawn, and
// compiler-lowered propagation reuses the frozen CallPlan rather than
// rediscovering targets during package codegen.
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
		plan := c.immutablePlan()
		if plan == nil {
			c.coroCapabilitiesErr = fmt.Errorf("coroutine function capabilities require an immutable SSA plan")
			return
		}
		universe := c.immutableEmissionUniverse()
		if universe == nil || universe.coroProgramIR == nil {
			c.coroCapabilitiesErr = fmt.Errorf("coroutine function capabilities require a prepared ProgramIR")
			return
		}
		local, err := universe.coroProgramIR.programCapabilitySeeds()
		if err != nil {
			c.coroCapabilitiesErr = err
			return
		}
		c.coroPredicateMasksByFunc = universe.coroProgramIR.coroLocalProgramPredicateMasks()
		c.coroCapabilitiesByFunc, c.coroCapabilitiesErr = deriveCoroFunctionProgramCapabilities(
			plan, local, c.CoroLibraryEffects, c.coroPredicateMasksByFunc,
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
	local map[*ssa.Function]coro.ProgramCapabilities,
	imported map[*ssa.Function]coro.LibraryEffectFunction,
	predicateMasks ...map[*ssa.Function]coro.ProgramCapabilities,
) (map[*ssa.Function]coro.ProgramCapabilities, error) {
	if plan == nil {
		return nil, fmt.Errorf("coroutine function capabilities require an SSA plan")
	}
	functions := plan.Functions()
	// Predicate dependencies share this exact existing edge projection and
	// fixed point with service demand; they are not another call graph.
	masks := make(map[*ssa.Function]coro.ProgramCapabilities)
	if len(predicateMasks) != 0 {
		masks = predicateMasks[0]
	}
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
		// Local and imported C implementations share the final plan's exact
		// certificate authority. Seed the caller, not the external declaration:
		// a code-address-only publication must retain no runtime boundary.
		if coroCallableNeedsNativeFaultBoundary(plan, callee) {
			capabilities[caller] |= coro.NativeDefaultFaultBoundaryProgramCapability()
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
		panicOnFaultAPI := function.Pkg != nil && function.Pkg.Pkg != nil &&
			llssa.PathOf(function.Pkg.Pkg) == "runtime/debug" &&
			function.Name() == "SetPanicOnFault"
		capability := local[function]
		if !capability.Valid() {
			return nil, fmt.Errorf("local function %q has invalid program capabilities %#x", function.Name(), capability)
		}
		if panicOnFaultAPI {
			capability |= coro.NewProgramCapabilities(false, true)
		}
		if coroExecutionDomainSeed(function) {
			capability |= coro.ExecutionDomainProgramCapability()
		}
		if coroGCObservationSeed(function) {
			capability |= coro.GCObservationProgramCapability()
		}
		if fact, ok := imported[function]; ok {
			if !fact.ProgramCapabilities.Valid() {
				return nil, fmt.Errorf("imported function %q has invalid program capabilities %#x", function.Name(), fact.ProgramCapabilities)
			}
			capability |= fact.ProgramCapabilities
			if !fact.ProgramPredicates.Valid() {
				return nil, fmt.Errorf("imported function %q has invalid program predicates", function.Name())
			}
			masks[function] |= fact.ProgramPredicates.Mask
		}
		capabilities[function] = capability
		masks[function] |= 0
	}
	for function := range local {
		if _, planned := capabilities[function]; !planned {
			return nil, fmt.Errorf("local capability seed targets unplanned function %q", function.Name())
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
		if capability != 0 || masks[function] != 0 {
			queue = append(queue, function)
		}
	}
	for len(queue) != 0 {
		callee := queue[0]
		queue = queue[1:]
		for caller := range reverse[callee] {
			joined := capabilities[caller] | capabilities[callee]
			joinedMask := masks[caller] | masks[callee]
			if joined == capabilities[caller] && joinedMask == masks[caller] {
				continue
			}
			if !joined.Valid() {
				return nil, fmt.Errorf("function %q acquired invalid program capabilities %#x", caller.Name(), joined)
			}
			capabilities[caller] = joined
			masks[caller] = joinedMask
			queue = append(queue, caller)
		}
	}
	return capabilities, nil
}

// Optional semantic services follow invocation demand, not mere body emission:
// a certified FuncPC observation retains an entry without ever calling it.
// Reuse the existing per-entry call closure and canonical roots. Raw callback
// publications and open managed descriptors remain potential external entries,
// even when their invocation is not represented by a local static call.
// Physical worker/fault services keep their conservative emitted-body policy.
func projectCoroProgramCapabilities(plan *coro.SSAPlan, capabilities map[*ssa.Function]coro.ProgramCapabilities) coro.ProgramCapabilities {
	var result coro.ProgramCapabilities
	for _, item := range plan.Functions() {
		if item.Plan.Emission == coro.EmitNone {
			continue
		}
		capability := capabilities[item.Function]
		result |= capability &^ coro.ProgramPredicateMask()
		_, root := plan.Root(item.Function)
		if root || item.Plan.RawPlainDemand || item.Plan.FuncRep == coro.Dispatch {
			result |= capability & coro.ProgramPredicateMask()
		}
	}
	return result
}

// Only entering/creating an isolated domain requires its accounting. In
// particular sync.WaitGroup's IsInBubble query must not enable it for every
// ordinary program. Both the standard-library declaration and its runtime
// implementation can own the canonical function after linkname resolution.
func coroExecutionDomainSeed(function *ssa.Function) bool {
	if function == nil || function.Pkg == nil || function.Pkg.Pkg == nil {
		return false
	}
	switch llssa.PathOf(function.Pkg.Pkg) {
	case "runtime":
		return function.Name() == "synctest_run" || function.Name() == "synctest_inBubble"
	case "internal/synctest":
		return function.Name() == "Run" || function.Name() == "inBubble"
	default:
		return false
	}
}

// Only actual registration requests post-GC observations. The scheduler's
// query/observer, allocator bootstrap and weak conversion are not seeds.
func coroGCObservationSeed(function *ssa.Function) bool {
	return function != nil && function.Pkg != nil && function.Pkg.Pkg != nil &&
		llssa.PathOf(function.Pkg.Pkg) == "github.com/xgo-dev/llgo/runtime/internal/runtime" &&
		function.Name() == "EnableGCObserver" && function.Signature.Recv() == nil &&
		function.Signature.Params().Len() == 0 && function.Signature.Results().Len() == 0
}

func coroCallableNeedsNativeFaultBoundary(plan *coro.SSAPlan, function *ssa.Function) bool {
	certificate, ok := plan.CallableContractCertificate(function)
	return ok && certificate.NativeDefaultFaultBoundary
}

// coroCallNeedsNativeFaultBoundary reads the same final invocation targets as
// capability propagation. Imported policies are installed after ProgramIR's
// source-site freeze, so the final SSA plan owns this declaration fact.
func coroCallNeedsNativeFaultBoundary(plan *coro.SSAPlan, call ssa.CallInstruction) bool {
	if call == nil || plan.ElidesCall(call) {
		return false
	}
	if callPlan, ok := plan.CallPlan(call); ok {
		for _, id := range callPlan.Targets {
			if target, ok := plan.Function(id); ok && coroCallableNeedsNativeFaultBoundary(plan, target) {
				return true
			}
		}
		return false
	}
	return call.Common() != nil && coroCallableNeedsNativeFaultBoundary(plan, call.Common().StaticCallee())
}
