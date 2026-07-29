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
	"fmt"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// DemandTrace reports the frozen direct roots and one-hop incoming edges that
// can explain why a function has an entry demand. It is diagnostic only:
// demand propagation remains owned by the immutable graph fixed point.
func (p *SSAPlan) DemandTrace(target *ssa.Function) string {
	if p == nil || target == nil {
		return "unavailable"
	}
	var sources []string
	targetID, identified := p.byFunction[target]
	for _, root := range p.roots {
		if root.Function == target || identified && root.ID == targetID {
			sources = append(sources, fmt.Sprintf(
				"root(%s managed=%s raw=%t)", root.Function, root.ManagedDemand, root.RawPlainDemand,
			))
		}
	}
	if identified {
		for _, owner := range p.functions {
			if owner.Function == nil || owner.Function == target {
				continue
			}
			if owner.Plan.ManagedDemand == NoDemand {
				continue
			}
			for _, block := range owner.Function.Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(ssa.CallInstruction)
					if ok {
						callPlan, planned := p.callPlans[call]
						if planned {
							for _, candidate := range callPlan.Targets {
								if candidate == targetID {
									sources = append(sources, fmt.Sprintf(
										"%s via %s(kind=%v managed=%s raw=%t emission=%s effect=%s)",
										owner.Function.String(), call.String(), callPlan.Kind,
										owner.Plan.ManagedDemand, owner.Plan.RawPlainDemand,
										owner.Plan.Emission, owner.Plan.Effect,
									))
									break
								}
							}
						}
					}
					operands := instruction.Operands(nil)
					for _, operand := range operands {
						if operand == nil || *operand == nil {
							continue
						}
						valuePlan, planned := p.valuePlans[*operand]
						if !planned {
							continue
						}
						found := false
						for _, leaf := range valuePlan.Funcs {
							for _, candidate := range leaf.Targets {
								if candidate == targetID {
									sources = append(sources, fmt.Sprintf(
										"%s via value %s in %s(managed=%s raw=%t emission=%s)",
										owner.Function.String(), (*operand).String(), instruction.String(),
										owner.Plan.ManagedDemand, owner.Plan.RawPlainDemand, owner.Plan.Emission,
									))
									found = true
									break
								}
							}
							if found {
								break
							}
						}
					}
				}
			}
			for _, lowered := range p.loweredCalls[owner.Function] {
				if lowered.Target == target {
					managedContribution := owner.Plan.ManagedDemand != NoDemand &&
						(!lowered.ExplicitStatusElided || owner.Plan.Emission != EmitCoroutine)
					if !managedContribution {
						continue
					}
					sources = append(sources, fmt.Sprintf(
						"%s via lowered %s(no-unwind=%t unwind-only=%t explicit-elided=%t contributes-managed=%t managed=%s raw=%t emission=%s effect=%s)",
						owner.Function.String(), lowered.LogicalName, lowered.NoUnwind, lowered.UnwindOnly,
						lowered.ExplicitStatusElided, managedContribution, owner.Plan.ManagedDemand,
						owner.Plan.RawPlainDemand, owner.Plan.Emission, owner.Plan.Effect,
					))
				}
			}
		}
	}
	if len(sources) == 0 {
		return "unavailable"
	}
	sort.Strings(sources)
	if len(sources) > 16 {
		sources = append(sources[:16], fmt.Sprintf("... %d more", len(sources)-16))
	}
	return strings.Join(sources, "; ")
}

// OpaqueEffectTrace returns one deterministic source-to-leaf explanation for
// an opaque aggregate effect. It is diagnostic only: the immutable plan
// remains the scheduling authority, and absence of a trace never weakens a
// validation failure.
func (p *SSAPlan) OpaqueEffectTrace(start *ssa.Function) string {
	if p == nil || start == nil {
		return "unavailable"
	}
	visiting := make(map[*ssa.Function]bool)
	var walk func(*ssa.Function, int) []string
	walk = func(fn *ssa.Function, depth int) []string {
		if fn == nil || depth > 32 || visiting[fn] {
			return nil
		}
		functionPlan, planned := p.FunctionPlan(fn)
		if !planned || !functionPlan.Effect.IsOpaque() {
			return nil
		}
		visiting[fn] = true
		defer delete(visiting, fn)
		head := fn.String()
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil {
					continue
				}
				callPlan, planned := p.CallPlan(call)
				if !planned || callPlan.Kind == CallSpawn {
					continue
				}
				if callPlan.Open && callPlan.Unresolved == UnknownManaged {
					return []string{head, fmt.Sprintf("%s [open managed]", call.String())}
				}
				for _, targetID := range callPlan.Targets {
					target, found := p.Function(targetID)
					if !found || target == nil {
						continue
					}
					targetPlan, planned := p.FunctionPlan(target)
					if !planned || !targetPlan.Effect.IsOpaque() {
						continue
					}
					if tail := walk(target, depth+1); len(tail) != 0 {
						return append([]string{head + " via " + call.String()}, tail...)
					}
				}
			}
		}
		for _, lowered := range p.LoweredCalls(fn) {
			targetPlan, planned := p.FunctionPlan(lowered.Target)
			if !planned || !targetPlan.Effect.IsOpaque() {
				continue
			}
			if tail := walk(lowered.Target, depth+1); len(tail) != 0 {
				return append([]string{head + " via lowered " + lowered.LogicalName}, tail...)
			}
		}
		return []string{fmt.Sprintf("%s [local=%s declared=%s external=%s exec=%s]",
			head, functionPlan.LocalEffect.Normalize(), functionPlan.DeclaredEffect.Normalize(),
			functionPlan.External, functionPlan.Exec)}
	}
	trace := walk(start, 0)
	if len(trace) == 0 {
		return "unavailable"
	}
	return strings.Join(trace, " -> ")
}

// SuspensionEffectTrace returns one deterministic source-to-leaf explanation
// for any of the requested non-opaque suspension-effect bits. It complements
// OpaqueEffectTrace: validation gates commonly reject one precise capability
// such as WaitForeign, which is propagated through the same immutable call
// graph but is not an opaque effect.
//
// The method is diagnostic only. It never changes the plan and deliberately
// reports "unavailable" when the requested bits are absent or the frozen call
// projection cannot explain them.
func (p *SSAPlan) SuspensionEffectTrace(start *ssa.Function, requested Effect) string {
	if p == nil || start == nil {
		return "unavailable"
	}
	requested = requested.Normalize() & knownSuspendEffects
	if requested == NoSuspend {
		return "unavailable"
	}
	visiting := make(map[*ssa.Function]bool)
	var walk func(*ssa.Function, Effect, int) []string
	walk = func(fn *ssa.Function, wanted Effect, depth int) []string {
		if fn == nil || wanted == NoSuspend || depth > 32 || visiting[fn] {
			return nil
		}
		functionPlan, planned := p.FunctionPlan(fn)
		if !planned {
			return nil
		}
		wanted &= functionPlan.Effect.Normalize()
		if wanted == NoSuspend {
			return nil
		}
		visiting[fn] = true
		defer delete(visiting, fn)
		head := fn.String()

		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil {
					continue
				}
				callPlan, planned := p.CallPlan(call)
				if !planned {
					continue
				}
				if wanted&WaitForeign != 0 &&
					(callPlan.Kind == CallForeign || callPlan.Open && callPlan.Unresolved == UnknownForeign) {
					return []string{head, fmt.Sprintf("%s [foreign edge]", call.String())}
				}
				switch callPlan.Kind {
				case CallDirect, CallDefer, CallDirectNoUnwind:
					for _, targetID := range callPlan.Targets {
						target, found := p.Function(targetID)
						if !found || target == nil {
							continue
						}
						targetPlan, planned := p.FunctionPlan(target)
						if !planned {
							continue
						}
						if wanted&WaitForeign != 0 && targetPlan.LocalExec.Contains(BlockForeign) {
							return []string{
								head + " via " + call.String(),
								fmt.Sprintf("%s [block-foreign]", target.String()),
							}
						}
						next := wanted & targetPlan.Effect.Normalize()
						if next == NoSuspend {
							continue
						}
						if tail := walk(target, next, depth+1); len(tail) != 0 {
							return append([]string{head + " via " + call.String()}, tail...)
						}
					}
				}
			}
		}
		for _, lowered := range p.LoweredCalls(fn) {
			if lowered.Target == nil || lowered.RawPlain || lowered.UnwindOnly || lowered.ExplicitStatusElided {
				continue
			}
			targetPlan, planned := p.FunctionPlan(lowered.Target)
			if !planned {
				continue
			}
			if wanted&WaitForeign != 0 && targetPlan.LocalExec.Contains(BlockForeign) {
				return []string{
					head + " via lowered " + lowered.LogicalName,
					fmt.Sprintf("%s [block-foreign]", lowered.Target.String()),
				}
			}
			next := wanted & targetPlan.Effect.Normalize()
			if next == NoSuspend {
				continue
			}
			if tail := walk(lowered.Target, next, depth+1); len(tail) != 0 {
				return append([]string{head + " via lowered " + lowered.LogicalName}, tail...)
			}
		}
		if local := wanted & functionPlan.LocalEffect.Normalize(); local != NoSuspend {
			return []string{fmt.Sprintf(
				"%s [local=%s declared=%s external=%s exec=%s]",
				head, functionPlan.LocalEffect.Normalize(), functionPlan.DeclaredEffect.Normalize(),
				functionPlan.External, functionPlan.Exec,
			)}
		}
		return nil
	}
	trace := walk(start, requested, 0)
	if len(trace) == 0 {
		return "unavailable"
	}
	return strings.Join(trace, " -> ")
}
