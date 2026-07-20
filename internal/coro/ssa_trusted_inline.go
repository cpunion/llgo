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

	"golang.org/x/tools/go/ssa"
)

// trustedInlineContractExecProjections derives both execution lanes from the
// target-owned frozen certificate. The occurrence producer supplies only the
// selected ContractID/ABI/certificate identity; it cannot manufacture these
// projections as a second source of truth.
func trustedInlineContractExecProjections(
	certificate CallableContractCertificate,
) (defaultExec, selectedExec ExecFlags, err error) {
	if err := certificate.Validate(); err != nil {
		return 0, 0, err
	}
	if certificate.Scope != CallableContractScopeDeclaration || !certificate.HasTrustedInlineContract {
		return 0, 0, fmt.Errorf("trusted-inline execution projection requires one declaration-owned refinement")
	}
	if err := ValidateTrustedInlineCallableContractRefinement(
		certificate.TrustedInlineContract, certificate.Contract,
	); err != nil {
		return 0, 0, err
	}
	defaultExec = CallableContractExecConstraints(certificate.Contract)
	selectedExec = CallableContractExecConstraints(certificate.TrustedInlineContract)
	if unsupported := (defaultExec | selectedExec) &^ callableContractExecFlags; unsupported != 0 {
		return 0, 0, fmt.Errorf("callable contract projected unsupported execution flags %s", unsupported)
	}
	if widening := selectedExec &^ defaultExec; widening != 0 {
		return 0, 0, fmt.Errorf("trusted-inline selected execution projection widens default by %s", widening)
	}
	return defaultExec, selectedExec, nil
}

func classifySSATrustedInlineCalls(
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	ignored map[*ssa.Function]struct{},
	policies map[*ssa.Function]SSAFunctionPolicy,
	elided map[ssa.CallInstruction]bool,
	canonicalizer *ssaFunctionCanonicalizer,
	config SSAConfig,
) (map[ssa.CallInstruction]SSATrustedInlineCallCertificate, error) {
	result := make(map[ssa.CallInstruction]SSATrustedInlineCallCertificate)
	if config.ClassifyTrustedInlineCall == nil {
		return result, nil
	}
	for _, caller := range functions {
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				certificate, classified, err := config.ClassifyTrustedInlineCall(caller, call)
				if err != nil {
					return nil, fmt.Errorf("coro: classify trusted-inline call in %q: %w", caller.Name(), err)
				}
				if !classified {
					if certificate != (SSATrustedInlineCallCertificate{}) {
						return nil, fmt.Errorf("coro: unclassified trusted-inline call in %q returned non-empty certificate facts", caller.Name())
					}
					continue
				}
				if err := validateStableToken("trusted-inline certificate", certificate.ID); err != nil {
					return nil, fmt.Errorf("coro: trusted-inline call in %q: %w", caller.Name(), err)
				}
				if err := validateStableToken("trusted-inline contract", string(certificate.Contract)); err != nil {
					return nil, fmt.Errorf("coro: trusted-inline call in %q: %w", caller.Name(), err)
				}
				if err := validateStableToken("trusted-inline ABI", certificate.ABI); err != nil {
					return nil, fmt.Errorf("coro: trusted-inline call in %q: %w", caller.Name(), err)
				}
				direct, ordinary := call.(*ssa.Call)
				common := call.Common()
				if !ordinary || direct == nil || common == nil || call.Parent() != caller ||
					common.IsInvoke() || common.StaticCallee() == nil {
					return nil, fmt.Errorf("coro: trusted-inline call in %q must be one exact ordinary static *ssa.Call", caller.Name())
				}
				if elided[call] {
					return nil, fmt.Errorf("coro: trusted-inline call in %q cannot also be frontend-elided", caller.Name())
				}
				target, resolved, resolveErr := canonicalizer.resolve(common.StaticCallee())
				if resolveErr != nil {
					return nil, fmt.Errorf("coro: resolve trusted-inline target in %q: %w", caller.Name(), resolveErr)
				}
				if !resolved || target == nil || !included[target] {
					return nil, fmt.Errorf("coro: trusted-inline call in %q has no exact target in the effective program", caller.Name())
				}
				policy, planned := policies[target]
				_, bodyIgnored := ignored[target]
				if !planned || !bodyIgnored || policy.External != ExternalUnknownForeign ||
					policy.Effect != NoSuspend || !policy.Exec.Contains(BlockForeign) ||
					policy.Exec.Contains(NeedsPreempt) || target.Signature == nil {
					return nil, fmt.Errorf(
						"coro: trusted-inline target %q must remain one ignored no-suspend unknown-foreign BlockForeign declaration",
						target.Name(),
					)
				}
				targetCertificate := policy.CallableContractCertificate
				if targetCertificate.IsZero() {
					return nil, fmt.Errorf("coro: trusted-inline target %q has no target-owned callable contract certificate", target.Name())
				}
				if err := targetCertificate.Validate(); err != nil {
					return nil, fmt.Errorf("coro: trusted-inline target %q has an invalid callable contract certificate: %w", target.Name(), err)
				}
				if !targetCertificate.HasTrustedInlineContract {
					return nil, fmt.Errorf("coro: trusted-inline target %q owns no trusted-inline refinement", target.Name())
				}
				defaultExec, _, projectionErr := trustedInlineContractExecProjections(targetCertificate)
				if projectionErr != nil {
					return nil, fmt.Errorf("coro: trusted-inline target %q has invalid execution projections: %w", target.Name(), projectionErr)
				}
				if got := policy.Exec & callableContractExecFlags; got != defaultExec {
					return nil, fmt.Errorf(
						"coro: trusted-inline target %q default contract execution projection is %s, policy lane is %s",
						target.Name(), defaultExec, got,
					)
				}
				if certificate.Contract != targetCertificate.TrustedInlineContract.ID {
					return nil, fmt.Errorf(
						"coro: trusted-inline call in %q claims contract %q, target %q owns %q",
						caller.Name(), certificate.Contract, target.Name(), targetCertificate.TrustedInlineContract.ID,
					)
				}
				if certificate.ABI != targetCertificate.CallableABI {
					return nil, fmt.Errorf(
						"coro: trusted-inline call in %q claims ABI %q, target %q owns %q",
						caller.Name(), certificate.ABI, target.Name(), targetCertificate.CallableABI,
					)
				}
				result[call] = certificate
			}
		}
	}
	return result, nil
}

// classifySSATrustedInlineNoUnwindCalls projects the narrower managed-unwind
// fact from an already validated exact invocation. Executor-safe progress does
// not by itself exclude a managed callback; only ReentryNone permits the
// generic no-unwind body proof to treat the selected C call as a leaf.
func classifySSATrustedInlineNoUnwindCalls(
	calls map[ssa.CallInstruction]SSATrustedInlineCallCertificate,
	policies map[*ssa.Function]SSAFunctionPolicy,
	canonicalizer *ssaFunctionCanonicalizer,
) (map[ssa.CallInstruction]bool, error) {
	result := make(map[ssa.CallInstruction]bool)
	for call, invocation := range calls {
		if call == nil || call.Common() == nil || call.Common().StaticCallee() == nil {
			return nil, fmt.Errorf("coro: trusted-inline no-unwind projection lost its exact static call")
		}
		target, resolved, err := canonicalizer.resolve(call.Common().StaticCallee())
		if err != nil {
			return nil, fmt.Errorf("coro: resolve trusted-inline no-unwind target: %w", err)
		}
		if !resolved || target == nil {
			return nil, fmt.Errorf("coro: trusted-inline no-unwind projection has no canonical target")
		}
		certificate := policies[target].CallableContractCertificate
		if certificate.IsZero() || !certificate.HasTrustedInlineContract ||
			invocation.Contract != certificate.TrustedInlineContract.ID ||
			invocation.ABI != certificate.CallableABI {
			return nil, fmt.Errorf("coro: trusted-inline no-unwind projection is not bound to its target-owned selected contract")
		}
		if certificate.TrustedInlineContract.Reentry == ReentryNone {
			result[call] = true
		}
	}
	return result, nil
}

func applySSATrustedInlineCallPlans(
	plans map[ssa.CallInstruction]SSACallPlan,
	certificates map[ssa.CallInstruction]SSATrustedInlineCallCertificate,
) error {
	for call, certificate := range certificates {
		plan, ok := plans[call]
		if !ok || plan.Kind != CallTrustedInline || plan.Rep != DirectPlain || plan.Open ||
			plan.MayBeNil || len(plan.Targets) != 1 {
			return fmt.Errorf("coro: trusted-inline call lost its exact closed DirectPlain CallPlan")
		}
		plan.InvocationPolicy = InvocationTrustedInline
		plan.InvocationContract = certificate.Contract
		plan.InvocationABI = certificate.ABI
		plan.InvocationCertificate = certificate.ID
		plans[call] = plan
	}
	return nil
}
