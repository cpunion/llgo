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
	"go/types"

	"github.com/xgo-dev/llgo/cl/ssawrap"
	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// materializeManagedForeignFunctionValueAdapter creates the single Go-callable
// body used when an exact C declaration or C-linked method crosses a managed
// function-value boundary. Direct calls and raw code-address uses continue to
// name target. The wrapper's ordinary SSA call lets the existing callable
// contract, worker, same-M, and effect propagation machinery choose its
// execution protocol. A method wrapper retains the source receiver in its
// signature; the ordinary method-descriptor path performs the receiver-aware
// normalization when the value is published.
func (u *EmissionUniverse) materializeManagedForeignFunctionValueAdapter(
	target *ssa.Function,
	useOwner *preparedEmissionPackage,
) (bool, error) {
	if u == nil || target == nil {
		return false, nil
	}
	target = u.canonicalAlias(target)
	if target == nil {
		return false, fmt.Errorf("prepare emission universe: managed foreign function-value target has a cyclic alias")
	}
	// Package-less SSA wrappers are owned by the package that uses the exact
	// wrapper instance. addResolvedRequired has just frozen its frontend kind
	// under that owner. Declared functions, including C declarations, retain
	// their exact declaration-package owner instead.
	owner := useOwner
	if target.Pkg != nil {
		owner = u.ownerOf(target)
	}
	if owner == nil {
		return false, fmt.Errorf(
			"prepare emission universe: managed function-value target %q has no exact emission owner",
			target.Name(),
		)
	}
	kind, frozen := u.functionKinds[emissionFunctionOwnerKey{function: target, owner: owner}]
	if !frozen {
		return false, fmt.Errorf(
			"prepare emission universe: managed function-value target %q has no frozen frontend kind",
			target.Name(),
		)
	}
	if !isNativeFuncKind(kind) {
		return false, nil
	}
	if target.Signature == nil {
		return false, fmt.Errorf(
			"prepare emission universe: C function-value target %q must have one signature",
			target.Name(),
		)
	}
	if wrapper := u.managedForeignWraps[target]; wrapper != nil {
		return true, nil
	}
	structuralKey, err := u.managedForeignFunctionValueWrapperStructuralKey(target, owner)
	if err != nil {
		return false, err
	}
	wrapperName := target.Name() + "$wrapper$llgo$managed-foreign$v1$" + emissionDigest(structuralKey)
	wrapper := ssawrap.MakeCallWrapperNamed(u.goProg, target, wrapperName)
	u.managedForeignWraps[target] = wrapper
	u.managedForeignWrapInfo[wrapper] = target
	u.syntheticKeys[wrapper] = structuralKey
	if err := u.recordFunctionKind(wrapper, owner, goFunc); err != nil {
		return false, err
	}
	u.fnOwners[wrapper] = owner
	state, stateFrozen := u.fnStates[target]
	if !stateFrozen {
		return false, fmt.Errorf(
			"prepare emission universe: C function-value target %q has no frozen provenance",
			target.Name(),
		)
	}
	u.fnStates[wrapper] = state
	u.addRequiredWithState(wrapper, owner, state)
	return true, nil
}

func (u *EmissionUniverse) managedForeignFunctionValueWrapperStructuralKey(
	target *ssa.Function,
	owner *preparedEmissionPackage,
) (string, error) {
	if u == nil || target == nil || owner == nil || target.Signature == nil {
		return "", fmt.Errorf("managed foreign function-value wrapper requires an exact target, owner, and signature")
	}
	identity := u.finalIdentity(target)
	if identity == "" || identity == "<nil>" || identity == "<cyclic-alias>" {
		return "", fmt.Errorf("managed foreign function-value target %q has no exact identity", target.Name())
	}
	effective := u.effectiveType(owner, target, target.Signature)
	return framedEmissionKey(
		"llgo-managed-foreign-function-value-wrapper-v1",
		owner.identity,
		identity,
		u.emissionTypeKeys.strict(effective),
	), nil
}

// CoroManagedFunctionValueTarget returns the exact Go body representing source
// in managed function-value transport. Absence means source uses its ordinary
// canonical Go entry. This query never authorizes static-call or raw-address
// redirection.
func (u *EmissionUniverse) CoroManagedFunctionValueTarget(
	source *ssa.Function,
) (adapter *ssa.Function, adapted bool, err error) {
	if u == nil || source == nil {
		return nil, false, fmt.Errorf("coroutine managed function-value resolution requires a universe and source function")
	}
	canonical := u.canonicalAlias(source)
	if canonical == nil {
		return nil, false, fmt.Errorf("coroutine managed function-value source %q has a cyclic alias", source.Name())
	}
	if _, required := u.required[canonical]; !required {
		// Effective frontend bodies can retain operands from an unselected
		// fallback package. Such a value is deliberately outside the managed
		// program: the SSA flow canonicalizer will mark it open/unknown. Adapter
		// lookup must therefore be a no-op rather than turning an excluded source
		// operand into a preparation failure.
		return source, false, nil
	}
	adapter = u.managedForeignWraps[canonical]
	if adapter == nil {
		// Intrinsic wrappers belong to the package that materializes the value.
		// That keeps their body in the same independently-built archive as the
		// descriptor which references it. The analyzer resolver is deliberately
		// occurrence-independent, so the active universe must expose one exact
		// owner wrapper for this source declaration. Restrict this bridge to the
		// atomics whose wrapper lifetime and no-suspend behavior are exact; other
		// intrinsics can carry current-frame or structural semantics.
		intrinsicOwner := u.ownerOf(canonical)
		opcode, exactAtomic := u.intrinsicOps[emissionFunctionOwnerKey{
			function: canonical,
			owner:    intrinsicOwner,
		}]
		for key, candidate := range u.callWraps {
			if key.intrinsic != canonical || candidate == nil {
				continue
			}
			if intrinsicOwner == nil || !exactAtomic || !isCoroAtomicIntrinsic(opcode) {
				continue
			}
			if adapter != nil && adapter != candidate {
				return nil, false, fmt.Errorf(
					"coroutine managed function-value source %q has owner-dependent intrinsic wrappers",
					source.Name(),
				)
			}
			adapter = candidate
		}
		if adapter == nil {
			return nil, false, nil
		}
	}
	_, intrinsicAdapter := u.callWrapInfo[adapter]
	if (!intrinsicAdapter && u.managedForeignWrapInfo[adapter] != canonical) || u.canonicalAlias(adapter) != adapter {
		return nil, false, fmt.Errorf(
			"coroutine managed function-value source %q has inconsistent adapter metadata",
			source.Name(),
		)
	}
	if _, required := u.required[adapter]; !required {
		return nil, false, fmt.Errorf(
			"coroutine managed function-value adapter for %q is outside the frozen emission universe",
			source.Name(),
		)
	}
	return adapter, true, nil
}

// validateManagedIntrinsicFunctionValueWrapper proves that wrapper is the
// package-owned Go carrier for one exact inline atomic declaration. Unlike a
// native adapter this body is deliberately owned by the use package, so its
// descriptor and definition are emitted into the same archive.
func (u *EmissionUniverse) validateManagedIntrinsicFunctionValueWrapper(wrapper *ssa.Function) error {
	if u == nil || wrapper == nil {
		return fmt.Errorf("managed intrinsic function-value wrapper requires a universe and function")
	}
	info, compilerOwned := u.callWrapInfo[wrapper]
	if !compilerOwned || info.intrinsic == nil {
		return fmt.Errorf("function %q is not a compiler-owned intrinsic wrapper", wrapper.Name())
	}
	owner := u.packages[info.owner]
	if owner == nil || u.ownerOf(wrapper) != owner {
		return fmt.Errorf("managed intrinsic function-value wrapper has no exact use-package owner")
	}
	structuralKey, err := u.intrinsicWrapperStructuralKey(info)
	if err != nil {
		return err
	}
	if wrapper.Synthetic != "wrapper" || wrapper.Parent() != nil || len(wrapper.FreeVars) != 0 ||
		wrapper.Origin() != nil || len(wrapper.TypeArgs()) != 0 || wrapper.Signature == nil ||
		wrapper.Signature.Recv() != nil || wrapper.Signature.Variadic() ||
		!types.Identical(wrapper.Signature, info.intrinsic.Signature) ||
		u.syntheticKeys[wrapper] != structuralKey || u.callWraps[info] != wrapper {
		return fmt.Errorf("managed intrinsic function-value wrapper lost its exact identity or signature")
	}
	forward, err := validateCoroExactSyntheticForwarder(wrapper, info.intrinsic)
	if err != nil || forward.Common() == nil || forward.Common().StaticCallee() != info.intrinsic {
		if err == nil {
			err = fmt.Errorf("forwarding call does not target the exact intrinsic declaration")
		}
		return fmt.Errorf("managed intrinsic function-value forwarding body is invalid: %w", err)
	}
	frozen, found, err := u.coroProgramIR.callSitePlan(forward)
	if err != nil || !found || frozen.failure != "" ||
		frozen.plan.Elision != CoroCallElidedIntrinsic || !frozen.plan.Intrinsic ||
		frozen.plan.IntrinsicSemantics != CoroIntrinsicCallInlineNoSuspend ||
		frozen.plan.StaticSpawnTarget != nil || frozen.plan.ManagedStaticTarget != nil ||
		frozen.plan.GeneratedCgoTarget != nil || frozen.plan.RawPlain ||
		!isCoroAtomicIntrinsic(frozen.opcode) {
		if err == nil && !found {
			err = fmt.Errorf("forwarding call is absent from ProgramIR")
		} else if err == nil && frozen.failure != "" {
			err = fmt.Errorf("forwarding call failed classification: %s", frozen.failure)
		}
		return fmt.Errorf("managed intrinsic function-value forwarding recipe is invalid: %v", err)
	}
	return nil
}

func (u *EmissionUniverse) validateManagedForeignFunctionValueWrapper(
	wrapper *ssa.Function,
) error {
	if u == nil || wrapper == nil {
		return fmt.Errorf("managed foreign function-value wrapper requires a universe and function")
	}
	target, compilerOwned := u.managedForeignWrapInfo[wrapper]
	if !compilerOwned || target == nil {
		return fmt.Errorf("function %q is not a compiler-owned managed foreign adapter", wrapper.Name())
	}
	owner := u.ownerOf(wrapper)
	key, err := u.managedForeignFunctionValueWrapperStructuralKey(target, owner)
	if err != nil {
		return err
	}
	if wrapper.Synthetic != "wrapper" || wrapper.Parent() != nil || len(wrapper.FreeVars) != 0 ||
		wrapper.Origin() != nil || len(wrapper.TypeArgs()) != 0 || wrapper.Signature == nil ||
		!types.Identical(wrapper.Signature, target.Signature) ||
		u.syntheticKeys[wrapper] != key || u.managedForeignWraps[target] != wrapper {
		return fmt.Errorf("managed foreign function-value wrapper lost its exact identity or signature")
	}
	forward, err := validateCoroExactSyntheticForwarder(wrapper, target)
	if err != nil {
		return fmt.Errorf("managed foreign function-value forwarding body is invalid: %w", err)
	}
	if forward.Common() == nil || forward.Common().StaticCallee() != target {
		return fmt.Errorf("managed foreign function-value forwarding body does not call its exact C declaration")
	}
	site, frozen, err := u.CoroCallSitePlan(forward)
	if err != nil || !frozen {
		if err == nil {
			err = fmt.Errorf("forwarding call is absent from ProgramIR")
		}
		return fmt.Errorf("managed foreign function-value forwarding recipe is invalid: %w", err)
	}
	if site.Elision != CoroCallNotElided || site.Intrinsic || site.StaticSpawnTarget != nil ||
		site.ManagedStaticTarget != nil || site.RawPlain ||
		(site.GeneratedCgoTarget != nil && site.GeneratedCgoTarget != target) {
		return fmt.Errorf("managed foreign function-value forwarding recipe is not one exact C call")
	}
	return nil
}

// coroManagedFunctionValuePlanTarget resolves the target that the immutable
// ValuePlan actually publishes. The emission universe owns adapter identity;
// the plan remains the authority that a particular function value uses it.
// Static calls and raw-address consumers must not use this helper.
func coroManagedFunctionValuePlanTarget(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	source *ssa.Function,
) (*ssa.Function, error) {
	if plan == nil || universe == nil || source == nil {
		return nil, fmt.Errorf("managed function-value target resolution requires a plan, universe, and source")
	}
	adapter, adapted, err := universe.CoroManagedFunctionValueTarget(source)
	if err != nil {
		return nil, err
	}
	if !adapted {
		return source, nil
	}
	valuePlan, planned := plan.ValuePlan(source)
	if !planned || len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 {
		return nil, fmt.Errorf(
			"managed foreign function-value source %q has no scalar frozen ValuePlan",
			source.Name(),
		)
	}
	leaf := valuePlan.Funcs[0]
	if leaf.Transport != coro.ManagedTransport {
		return nil, fmt.Errorf(
			"managed foreign function-value source %q has non-managed transport %s",
			source.Name(), leaf.Transport,
		)
	}
	targetID, identified := plan.FunctionID(adapter)
	if !identified {
		return nil, fmt.Errorf(
			"managed foreign function-value adapter for %q has no FunctionID",
			source.Name(),
		)
	}
	for _, candidate := range leaf.Targets {
		if candidate == targetID {
			return adapter, nil
		}
	}
	return nil, fmt.Errorf(
		"managed foreign function-value source %q does not carry its frozen adapter %q",
		source.Name(), targetID,
	)
}

// managedFunctionValueTarget consumes the occurrence-independent adapter map
// only when whole-program value flow selected that adapter for this managed
// source value. Raw C conversions and structural code-address consumers retain
// their original declaration even when another occurrence publishes a Go
// descriptor for the same symbol.
func (p *context) managedFunctionValueTarget(
	source *ssa.Function,
) (*ssa.Function, bool) {
	if p == nil || source == nil || p.compilation == nil ||
		p.immutablePlan() == nil || p.immutableEmissionUniverse() == nil {
		return source, false
	}
	adapter, adapted, err := p.immutableEmissionUniverse().CoroManagedFunctionValueTarget(source)
	if err != nil {
		panic(err)
	}
	if !adapted {
		return source, false
	}
	target, err := coroManagedFunctionValuePlanTarget(
		p.immutablePlan(),
		p.immutableEmissionUniverse(),
		source,
	)
	if err != nil {
		panic(err)
	}
	if target != adapter {
		panic(fmt.Errorf(
			"managed foreign function-value source %q resolved to an inconsistent adapter",
			source.Name(),
		))
	}
	return target, true
}
