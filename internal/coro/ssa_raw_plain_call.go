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
	"strings"

	"golang.org/x/tools/go/ssa"
)

func classifySSARawPlainCalls(
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	ignored map[*ssa.Function]struct{},
	trusted map[*ssa.Function]SSAFunctionPolicy,
	elided map[ssa.CallInstruction]bool,
	canonicalizer *ssaFunctionCanonicalizer,
	config SSAConfig,
) (map[ssa.CallInstruction]SSARawPlainCallCertificate, error) {
	result := make(map[ssa.CallInstruction]SSARawPlainCallCertificate)
	if config.ClassifyRawPlainCall == nil {
		return result, nil
	}
	for _, caller := range functions {
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				certificate, classified, err := config.ClassifyRawPlainCall(caller, call)
				if err != nil {
					return nil, fmt.Errorf("coro: classify raw-plain call in %q: %w", caller.Name(), err)
				}
				if !classified {
					if certificate != (SSARawPlainCallCertificate{}) {
						return nil, fmt.Errorf(
							"coro: unclassified raw-plain call in %q returned certificate data",
							caller.Name(),
						)
					}
					continue
				}
				if err := validateStableToken("raw-plain call certificate", certificate.ID); err != nil {
					return nil, fmt.Errorf("coro: raw-plain call in %q: %w", caller.Name(), err)
				}
				direct, ordinary := call.(*ssa.Call)
				common := call.Common()
				if !ordinary || direct == nil || common == nil || direct.Parent() != caller ||
					common.IsInvoke() || common.Method != nil || common.StaticCallee() == nil {
					return nil, fmt.Errorf(
						"coro: raw-plain call in %q must be one exact ordinary static *ssa.Call",
						caller.Name(),
					)
				}
				if elided[call] {
					return nil, fmt.Errorf("coro: raw-plain call in %q cannot also be frontend-elided", caller.Name())
				}
				target, resolved, resolveErr := canonicalizer.resolve(common.StaticCallee())
				if resolveErr != nil {
					return nil, fmt.Errorf("coro: resolve raw-plain target in %q: %w", caller.Name(), resolveErr)
				}
				if !resolved || target == nil || !included[target] {
					return nil, fmt.Errorf(
						"coro: raw-plain call in %q has no exact target in the effective program",
						caller.Name(),
					)
				}
				if target == caller || len(target.Blocks) == 0 || target.Signature == nil ||
					target.Signature.Recv() != nil || len(target.FreeVars) != 0 {
					return nil, fmt.Errorf(
						"coro: raw-plain target %q must be a distinct receiver-free, non-capturing Go body",
						target.Name(),
					)
				}
				if _, bodyIgnored := ignored[target]; bodyIgnored {
					return nil, fmt.Errorf("coro: raw-plain target %q has an ignored body", target.Name())
				}
				policy := trusted[target]
				if !policy.TrustedNoPreempt || !policy.TrustedNoUnwind {
					return nil, fmt.Errorf(
						"coro: raw-plain target %q lacks the exact no-preempt/no-unwind RawCritical body policy",
						target.Name(),
					)
				}
				result[call] = certificate
			}
		}
	}
	return result, nil
}

func applySSARawPlainCallPlans(
	base *Plan,
	plans map[ssa.CallInstruction]SSACallPlan,
	certificates map[ssa.CallInstruction]SSARawPlainCallCertificate,
	ids map[*ssa.Function]FunctionID,
) error {
	for call, certificate := range certificates {
		plan, ok := plans[call]
		ordinaryDirectRep := plan.Rep == DirectPlain || plan.Rep == DirectCoro
		if !ok || plan.Kind != CallDirect || !ordinaryDirectRep ||
			plan.Transport != ManagedTransport || plan.Open || plan.MayBeNil ||
			len(plan.Targets) != 1 || plan.SyncDispatch ||
			plan.InvocationPolicy != "" || plan.InvocationContract != "" ||
			plan.InvocationABI != "" || plan.InvocationCertificate != "" {
			return fmt.Errorf(
				"coro: raw-plain call %q in %q lost its exact closed static CallPlan "+
					"(present=%t kind=%v rep=%s transport=%s open=%t nil=%t targets=%v sync-dispatch=%t "+
					"invocation-policy=%q invocation-contract=%q invocation-abi=%q invocation-certificate=%q)",
				call.String(), call.Parent().Name(), ok, plan.Kind, plan.Rep, plan.Transport,
				plan.Open, plan.MayBeNil, plan.Targets, plan.SyncDispatch,
				plan.InvocationPolicy, plan.InvocationContract, plan.InvocationABI, plan.InvocationCertificate,
			)
		}
		target := call.Common().StaticCallee()
		targetID, exact := ids[target]
		if !exact || targetID != plan.Targets[0] {
			return fmt.Errorf("coro: raw-plain call in %q disagrees with its exact static target", call.Parent().Name())
		}
		ownerID, exact := ids[call.Parent()]
		ownerPlan, ownerPlanned := base.Lookup(ownerID)
		if !exact || !ownerPlanned {
			return fmt.Errorf("coro: raw-plain call in %q has no exact owner plan", call.Parent().Name())
		}
		if ownerPlan.Emission != EmitNone {
			targetPlan, planned := base.Lookup(targetID)
			if !planned || !validRawPlainVariantPlan(targetPlan) {
				return fmt.Errorf(
					"coro: raw-plain target %q (%s) has no valid raw/plain body (owner=%q owner-emission=%s external=%s managed=%s raw=%t raw-only=%t emission=%s primary=%s representation=%s declared-exec=%s local-exec=%s exec=%s; direct dependencies: %s)",
					targetID, target.String(), ownerID, ownerPlan.Emission,
					targetPlan.External, targetPlan.ManagedDemand, targetPlan.RawPlainDemand,
					targetPlan.RawPlainOnly, targetPlan.Emission, targetPlan.Primary, targetPlan.FuncRep,
					targetPlan.DeclaredExec, targetPlan.LocalExec, targetPlan.Exec,
					rawPlainDirectDependencySummary(base, target, ids, plans),
				)
			}
		}
		// Rep describes the physical entry selected at this occurrence. The
		// ordinary closed static plan may have selected the target's coroutine
		// primary, but the frozen RawCritical certificate retargets only this
		// call to the separately validated native-stack twin. A dormant owner
		// retains the same frozen occurrence recipe without demanding or
		// validating a body that this compilation will not emit.
		plan.Rep = DirectPlain
		plan.RawPlain = true
		plan.RawPlainCertificate = certificate.ID
		plans[call] = plan
	}
	return nil
}

func validRawPlainVariantPlan(plan FunctionPlan) bool {
	if plan.External != Defined || !plan.RawPlainDemand || plan.Demand == NoDemand {
		return false
	}
	switch plan.Emission {
	case EmitPlain:
		return !plan.RawPlainOnly && plan.ManagedDemand != NoDemand &&
			plan.Primary == PrimaryPlain && plan.FuncRep != DirectCoro &&
			!plan.Effect.MaySuspend()
	case EmitCoroutine:
		return !plan.RawPlainOnly && plan.ManagedDemand != NoDemand &&
			plan.Primary == PrimaryCoroutine && plan.Effect.MaySuspend()
	case EmitRawPlain:
		return plan.RawPlainOnly && plan.ManagedDemand == NoDemand &&
			plan.Primary == PrimaryPlain && plan.FuncRep == DirectPlain
	default:
		return false
	}
}

func rawPlainDirectDependencySummary(
	base *Plan,
	target *ssa.Function,
	ids map[*ssa.Function]FunctionID,
	calls map[ssa.CallInstruction]SSACallPlan,
) string {
	if base == nil || target == nil {
		return "<unavailable>"
	}
	seen := make(map[*ssa.Function]bool)
	var dependencies []string
	for _, block := range target.Blocks {
		for _, instruction := range block.Instrs {
			call, ordinary := instruction.(*ssa.Call)
			if !ordinary || call.Common() == nil {
				continue
			}
			callee := call.Common().StaticCallee()
			if callee == nil || seen[callee] {
				continue
			}
			seen[callee] = true
			id, identified := ids[callee]
			if callPlan, planned := calls[call]; planned && len(callPlan.Targets) == 1 {
				id = callPlan.Targets[0]
				identified = true
			}
			if !identified {
				dependencies = append(dependencies, callee.String()+"=<unplanned>")
				continue
			}
			plan, planned := base.Lookup(id)
			if !planned {
				dependencies = append(dependencies, callee.String()+"=<missing-plan>")
				continue
			}
			dependencies = append(dependencies, fmt.Sprintf(
				"%s={external:%s,effect:%s,exec:%s}",
				callee.String(), plan.External, plan.Effect, plan.Exec,
			))
		}
	}
	if len(dependencies) == 0 {
		return "<none>"
	}
	return strings.Join(dependencies, ", ")
}
