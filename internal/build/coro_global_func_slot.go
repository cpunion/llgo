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
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
)

// coroGlobalFunctionSlotProof is the build-owned half of one closed
// dynamic-call certificate. The SSA certificate freezes the target set seen by
// fixed-point analysis. identityID and members bind every original/Alt SSA
// pointer to the one LLVM-internal physical cell certified by EmissionUniverse.
// inactive freezes every other writer/escape edge whose
// contribution was omitted from that set. Post-plan validation accepts the
// omission only when the exact owner has EmitNone, so an external/raw root or a
// newly demanded writer cannot silently widen the slot after analysis.
type coroGlobalFunctionSlotProof struct {
	global         *ssa.Global
	identityID     string
	physicalSymbol string
	members        []*ssa.Global
	call           ssa.CallInstruction
	certificate    coro.SSAClosedDynamicCallCertificate
	stores         []coroGlobalFunctionSlotStoreProof
	inactive       []coroGlobalFunctionSlotHazard
}

// coroGlobalFunctionSlotStoreProof binds one exact ordinary Go Store to the
// non-capturing body published through a closed, LLVM-internal function cell.
// The occurrence is build-owned: a CoroPlanBuilder cannot create or retarget
// it. Publication itself does not imply invocation: dormant readers let codegen
// elide the unobservable Store, while a live reader independently adds managed
// demand and keeps the canonical Dispatch descriptor.
type coroGlobalFunctionSlotStoreProof struct {
	owner  *ssa.Function
	store  *ssa.Store
	target *ssa.Function
}

type coroGlobalFunctionSlotHazard struct {
	owner       *ssa.Function
	instruction ssa.Instruction
	reason      string
}

type coroGlobalFunctionSlotFlow struct {
	ctx       *context
	functions []*ssa.Function
	callsTo   map[*ssa.Function][]ssa.CallInstruction
	escapes   map[*ssa.Function][]coroGlobalFunctionSlotHazard
}

type coroGlobalFunctionSlotValueProof struct {
	targets  map[*ssa.Function]struct{}
	stores   []coroGlobalFunctionSlotStoreProof
	inactive []coroGlobalFunctionSlotHazard
}

// coroGlobalFunctionSlotValueEnvironment is an exact, immutable substitution
// frame for one statically called function-value factory. A substitution keeps
// the caller environment in which its actual argument was evaluated; using the
// callee environment here would accidentally bind same-named/nested formals.
type coroGlobalFunctionSlotValueEnvironment struct {
	parameters map[*ssa.Parameter]coroGlobalFunctionSlotValueSubstitution
}

type coroGlobalFunctionSlotValueSubstitution struct {
	value       ssa.Value
	owner       *ssa.Function
	instruction ssa.Instruction
	environment *coroGlobalFunctionSlotValueEnvironment
}

type coroGlobalFunctionSlotFactoryResult struct {
	target *ssa.Function
	result int
}

type coroGlobalFunctionSlotValueState struct {
	writerParameters map[*ssa.Parameter]bool
	factoryResults   map[coroGlobalFunctionSlotFactoryResult]bool
}

// proveCoroGlobalFunctionSlotClosedDynamicCalls closes direct calls through an
// exact package-level function cell. It is deliberately independent of package
// and source names. Only a cell with certified LLVM internal linkage is
// considered, so separately linked code cannot add a hidden writer. Every
// emitted Go-body use of every original/Alt member address is audited;
// direct stores accept only nil, one exact context-free Go function, or
// a formal whose complete static incoming flow has the same property.
//
// Unknown writes and address/function-owner escapes are not discarded. They
// become exact conditional hazards and must be EmitNone in the final plan. This
// permits a private optional registration function with no linked caller to
// leave a slot nil, while importing its registrar or passing the cell address
// to raw code makes the same certificate fail closed.
func proveCoroGlobalFunctionSlotClosedDynamicCalls(
	ctx *context,
) (map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate, map[ssa.CallInstruction]coroGlobalFunctionSlotProof, error) {
	certificates := make(map[ssa.CallInstruction]coro.SSAClosedDynamicCallCertificate)
	proofs := make(map[ssa.CallInstruction]coroGlobalFunctionSlotProof)
	if ctx == nil || ctx.coroEmission == nil || ctx.coroSSAEmission == nil || ctx.prog == nil {
		return certificates, proofs, nil
	}
	functions, err := coroFrozenGoBodies(ctx)
	if err != nil {
		return nil, nil, err
	}
	flow, err := newCoroGlobalFunctionSlotFlow(ctx, functions)
	if err != nil {
		return nil, nil, err
	}

	type candidate struct {
		identity cl.CoroGlobalPhysicalIdentity
		global   *ssa.Global
		calls    []ssa.CallInstruction
	}
	var candidates []candidate
	byIdentity := make(map[string]int)
	for _, owner := range functions {
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil || call.Common().StaticCallee() != nil || call.Common().IsInvoke() {
					continue
				}
				switch exact := call.(type) {
				case *ssa.Call:
				case *ssa.Defer:
					if exact.DeferStack != nil {
						continue
					}
				default:
					continue
				}
				global, ok := coroDirectGlobalFunctionSlotLoad(call.Common().Value)
				if !ok || !coroGlobalFunctionSlotSignature(global, call.Common().Signature()) {
					continue
				}
				identity, certified, err := ctx.coroEmission.CoroGlobalPhysicalIdentity(global)
				if err != nil {
					return nil, nil, fmt.Errorf("resolve global function slot %q physical identity: %w", global.Name(), err)
				}
				if !certified || !identity.InternalLinkage {
					continue
				}
				index, exists := byIdentity[identity.ID]
				if !exists {
					index = len(candidates)
					byIdentity[identity.ID] = index
					candidates = append(candidates, candidate{identity: identity, global: global})
				}
				candidates[index].calls = append(candidates[index].calls, call)
			}
		}
	}

	for _, candidate := range candidates {
		certificate, stores, inactive, proved, err := flow.proveSlot(candidate.identity)
		if err != nil {
			return nil, nil, fmt.Errorf("prove global function slot %q: %w", candidate.identity.PhysicalSymbol, err)
		}
		if !proved {
			continue
		}
		for _, call := range candidate.calls {
			cloned := cloneCoroClosedDynamicCallCertificate(certificate)
			certificates[call] = cloned
			proofs[call] = coroGlobalFunctionSlotProof{
				global:         candidate.global,
				identityID:     candidate.identity.ID,
				physicalSymbol: candidate.identity.PhysicalSymbol,
				members:        append([]*ssa.Global(nil), candidate.identity.Members...),
				call:           call,
				certificate:    cloneCoroClosedDynamicCallCertificate(certificate),
				stores:         append([]coroGlobalFunctionSlotStoreProof(nil), stores...),
				inactive:       append([]coroGlobalFunctionSlotHazard(nil), inactive...),
			}
		}
	}
	return certificates, proofs, nil
}

// collectCoroGlobalFunctionSlotStores projects the per-call slot proofs into
// one occurrence-keyed classifier input. Multiple dynamic calls through the
// same cell repeat the same frozen Store proof; disagreement is corruption, not
// a reason to guess which target belongs to the managed publication.
func collectCoroGlobalFunctionSlotStores(
	proofs map[ssa.CallInstruction]coroGlobalFunctionSlotProof,
) (map[*ssa.Store]coroGlobalFunctionSlotStoreProof, error) {
	result := make(map[*ssa.Store]coroGlobalFunctionSlotStoreProof)
	for call, proof := range proofs {
		if call == nil || proof.call != call || proof.identityID == "" || len(proof.members) == 0 {
			return nil, fmt.Errorf("collect global function-slot Stores: incomplete call proof")
		}
		members := make(map[*ssa.Global]struct{}, len(proof.members))
		for _, member := range proof.members {
			if member == nil {
				return nil, fmt.Errorf("collect global function-slot Stores for %q: nil physical member", proof.physicalSymbol)
			}
			members[member] = struct{}{}
		}
		for _, publication := range proof.stores {
			if publication.owner == nil || publication.store == nil || publication.target == nil ||
				publication.store.Parent() != publication.owner {
				return nil, fmt.Errorf("collect global function-slot Stores for %q: incomplete Store occurrence", proof.physicalSymbol)
			}
			global, direct := publication.store.Addr.(*ssa.Global)
			if _, member := members[global]; !direct || !member {
				return nil, fmt.Errorf("collect global function-slot Stores for %q: Store no longer names its exact cell", proof.physicalSymbol)
			}
			targetMember := false
			for _, target := range proof.certificate.Targets {
				targetMember = targetMember || target == publication.target
			}
			if !targetMember {
				return nil, fmt.Errorf("collect global function-slot Stores for %q: target %q is absent from the closed target set", proof.physicalSymbol, publication.target.Name())
			}
			if previous, duplicate := result[publication.store]; duplicate {
				if previous.owner != publication.owner || previous.target != publication.target {
					return nil, fmt.Errorf("collect global function-slot Stores for %q: one Store has conflicting frozen targets", proof.physicalSymbol)
				}
				continue
			}
			result[publication.store] = publication
		}
	}
	return result, nil
}

func newCoroGlobalFunctionSlotFlow(ctx *context, functions []*ssa.Function) (*coroGlobalFunctionSlotFlow, error) {
	flow := &coroGlobalFunctionSlotFlow{
		ctx:       ctx,
		functions: append([]*ssa.Function(nil), functions...),
		callsTo:   make(map[*ssa.Function][]ssa.CallInstruction),
		escapes:   make(map[*ssa.Function][]coroGlobalFunctionSlotHazard),
	}
	for _, owner := range functions {
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				call, isCall := instruction.(ssa.CallInstruction)
				if isCall && call.Common() != nil {
					if raw := call.Common().StaticCallee(); raw != nil {
						if target, ok := ctx.coroEmission.Resolve(raw); ok && target != nil {
							flow.callsTo[target] = append(flow.callsTo[target], call)
						}
					}
				}
				for _, operand := range instruction.Operands(nil) {
					if operand == nil || *operand == nil {
						continue
					}
					raw, ok := (*operand).(*ssa.Function)
					if !ok {
						continue
					}
					target, resolved := ctx.coroEmission.Resolve(raw)
					if !resolved || target == nil {
						continue
					}
					if _, debug := instruction.(*ssa.DebugRef); debug {
						continue
					}
					if isCall && call.Common() != nil && call.Common().Value == raw {
						if static := call.Common().StaticCallee(); static != nil {
							if canonical, ok := ctx.coroEmission.Resolve(static); ok && canonical == target {
								continue
							}
						}
					}
					flow.escapes[target] = appendUniqueCoroGlobalFunctionSlotHazard(flow.escapes[target], coroGlobalFunctionSlotHazard{
						owner: owner, instruction: instruction,
						reason: fmt.Sprintf("writer function address escapes through %T", instruction),
					})
				}
			}
		}
	}
	return flow, nil
}

func (flow *coroGlobalFunctionSlotFlow) proveSlot(
	identity cl.CoroGlobalPhysicalIdentity,
) (coro.SSAClosedDynamicCallCertificate, []coroGlobalFunctionSlotStoreProof, []coroGlobalFunctionSlotHazard, bool, error) {
	if flow == nil || identity.ID == "" || !identity.InternalLinkage || len(identity.Members) == 0 {
		return coro.SSAClosedDynamicCallCertificate{}, nil, nil, false, nil
	}
	global := identity.Members[0]
	if global == nil || global.Pkg == nil || global.Pkg.Pkg == nil || flow.ctx == nil || flow.ctx.prog == nil {
		return coro.SSAClosedDynamicCallCertificate{}, nil, nil, false, nil
	}
	signature, ok := coroGlobalFunctionSlotType(global)
	if !ok {
		return coro.SSAClosedDynamicCallCertificate{}, nil, nil, false, nil
	}
	members := make(map[*ssa.Global]struct{}, len(identity.Members))
	for _, member := range identity.Members {
		memberSignature, valid := coroGlobalFunctionSlotType(member)
		if member == nil || !valid || !types.Identical(memberSignature, signature) {
			return coro.SSAClosedDynamicCallCertificate{}, nil, nil, false, nil
		}
		members[member] = struct{}{}
	}
	proof := coroGlobalFunctionSlotValueProof{targets: make(map[*ssa.Function]struct{})}
	state := &coroGlobalFunctionSlotValueState{
		writerParameters: make(map[*ssa.Parameter]bool),
		factoryResults:   make(map[coroGlobalFunctionSlotFactoryResult]bool),
	}
	for _, owner := range flow.functions {
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				usesGlobal := false
				for _, operand := range instruction.Operands(nil) {
					if operand == nil || *operand == nil {
						continue
					}
					operandGlobal, isGlobal := (*operand).(*ssa.Global)
					if _, usesGlobal = members[operandGlobal]; isGlobal && usesGlobal {
						break
					}
				}
				if !usesGlobal {
					continue
				}
				switch current := instruction.(type) {
				case *ssa.DebugRef:
					continue
				case *ssa.UnOp:
					if member, exact := current.X.(*ssa.Global); exact && current.Op == token.MUL {
						if _, exact = members[member]; exact {
							continue
						}
					}
				case *ssa.Store:
					if member, exact := current.Addr.(*ssa.Global); exact {
						if _, exact = members[member]; exact {
							valueProof := flow.proveStoredValue(current.Val, signature, owner, current, nil, state)
							if target, exact := coroExactGlobalFunctionSlotStoreTarget(flow.ctx, current, signature); exact {
								valueProof.stores = append(valueProof.stores, coroGlobalFunctionSlotStoreProof{
									owner: owner, store: current, target: target,
								})
							}
							mergeCoroGlobalFunctionSlotValueProof(&proof, valueProof)
							continue
						}
					}
				}
				proof.inactive = appendUniqueCoroGlobalFunctionSlotHazard(proof.inactive, coroGlobalFunctionSlotHazard{
					owner: owner, instruction: instruction,
					reason: fmt.Sprintf("global function cell address escapes through %T", instruction),
				})
			}
		}
	}
	if len(proof.targets) > 1 {
		// SSAClosedDynamicCallCertificate V0 admits one exact non-nil target.
		// Retaining the ordinary open call is safer than dropping targets.
		return coro.SSAClosedDynamicCallCertificate{}, nil, nil, false, nil
	}
	certificate := coro.SSAClosedDynamicCallCertificate{MayBeNil: true}
	for target := range proof.targets {
		certificate.Targets = []*ssa.Function{target}
	}
	return certificate, proof.stores, proof.inactive, true, nil
}

func (flow *coroGlobalFunctionSlotFlow) proveStoredValue(
	value ssa.Value,
	signature *types.Signature,
	owner *ssa.Function,
	instruction ssa.Instruction,
	environment *coroGlobalFunctionSlotValueEnvironment,
	state *coroGlobalFunctionSlotValueState,
) coroGlobalFunctionSlotValueProof {
	for value != nil {
		switch current := value.(type) {
		case *ssa.ChangeType:
			value = current.X
			continue
		case *ssa.Convert:
			value = current.X
			continue
		case *ssa.Const:
			if current.IsNil() {
				return coroGlobalFunctionSlotValueProof{targets: make(map[*ssa.Function]struct{})}
			}
		case *ssa.Function:
			if target, exact := coroExactGlobalFunctionSlotBareTarget(flow.ctx, current, signature); exact {
				return coroGlobalFunctionSlotValueProof{targets: map[*ssa.Function]struct{}{target: {}}}
			}
		case *ssa.MakeClosure:
			if target, exact := coroExactGlobalFunctionSlotClosureTarget(flow.ctx, current, signature); exact {
				return coroGlobalFunctionSlotValueProof{targets: map[*ssa.Function]struct{}{target: {}}}
			}
		case *ssa.Parameter:
			if environment != nil {
				if substitution, found := environment.parameters[current]; found {
					return flow.proveStoredValue(
						substitution.value, signature, substitution.owner, substitution.instruction,
						substitution.environment, state,
					)
				}
			}
			return flow.proveParameter(current, signature, environment, state)
		case *ssa.Phi:
			result := coroGlobalFunctionSlotValueProof{targets: make(map[*ssa.Function]struct{})}
			for _, edge := range current.Edges {
				mergeCoroGlobalFunctionSlotValueProof(&result,
					flow.proveStoredValue(edge, signature, owner, instruction, environment, state))
			}
			return result
		case *ssa.Call:
			return flow.proveFactoryResult(current, 0, signature, environment, state)
		case *ssa.Extract:
			call, ok := current.Tuple.(*ssa.Call)
			if ok {
				return flow.proveFactoryResult(call, current.Index, signature, environment, state)
			}
		}
		break
	}
	return coroGlobalFunctionSlotValueProof{
		targets: make(map[*ssa.Function]struct{}),
		inactive: []coroGlobalFunctionSlotHazard{{
			owner: owner, instruction: instruction,
			reason: "global function cell has a store with unknown target provenance",
		}},
	}
}

func (flow *coroGlobalFunctionSlotFlow) proveParameter(
	parameter *ssa.Parameter,
	signature *types.Signature,
	environment *coroGlobalFunctionSlotValueEnvironment,
	state *coroGlobalFunctionSlotValueState,
) coroGlobalFunctionSlotValueProof {
	result := coroGlobalFunctionSlotValueProof{targets: make(map[*ssa.Function]struct{})}
	if parameter == nil || parameter.Parent() == nil {
		return result
	}
	owner := parameter.Parent()
	if state == nil {
		state = &coroGlobalFunctionSlotValueState{
			writerParameters: make(map[*ssa.Parameter]bool),
			factoryResults:   make(map[coroGlobalFunctionSlotFactoryResult]bool),
		}
	}
	if state.writerParameters[parameter] {
		result.inactive = append(result.inactive, coroGlobalFunctionSlotHazard{
			owner: owner, reason: "parameter-fed global writer has cyclic incoming provenance",
		})
		return result
	}
	state.writerParameters[parameter] = true
	defer delete(state.writerParameters, parameter)

	index := -1
	for candidate, formal := range owner.Params {
		if formal == parameter {
			index = candidate
			break
		}
	}
	if index < 0 || !coroClosedGlobalFunctionSlotWriter(owner) {
		result.inactive = append(result.inactive, coroGlobalFunctionSlotHazard{
			owner: owner, reason: "parameter-fed global writer is not one private top-level Go function",
		})
		return result
	}
	for _, escape := range flow.escapes[owner] {
		result.inactive = appendUniqueCoroGlobalFunctionSlotHazard(result.inactive, escape)
	}
	incoming := flow.callsTo[owner]
	if len(incoming) == 0 {
		result.inactive = appendUniqueCoroGlobalFunctionSlotHazard(result.inactive, coroGlobalFunctionSlotHazard{
			owner: owner, reason: "parameter-fed global writer has no frozen static incoming call",
		})
		return result
	}
	for _, call := range incoming {
		if call.Common() == nil || index >= len(call.Common().Args) {
			result.inactive = appendUniqueCoroGlobalFunctionSlotHazard(result.inactive, coroGlobalFunctionSlotHazard{
				owner: call.Parent(), instruction: call,
				reason: "parameter-fed global writer has a malformed static incoming call",
			})
			continue
		}
		mergeCoroGlobalFunctionSlotValueProof(&result,
			flow.proveStoredValue(call.Common().Args[index], signature, call.Parent(), call, environment, state))
	}
	return result
}

// proveFactoryResult evaluates one exact result of a statically called,
// receiver-free Go factory. It never executes SSA and never infers a result
// from a function name: every return edge is traversed, with the exact call
// arguments substituted for the callee formals. A recursive result-flow cycle
// remains an explicit final-plan hazard.
func (flow *coroGlobalFunctionSlotFlow) proveFactoryResult(
	call *ssa.Call,
	resultIndex int,
	signature *types.Signature,
	environment *coroGlobalFunctionSlotValueEnvironment,
	state *coroGlobalFunctionSlotValueState,
) coroGlobalFunctionSlotValueProof {
	hazard := func(reason string) coroGlobalFunctionSlotValueProof {
		var owner *ssa.Function
		if call != nil {
			owner = call.Parent()
		}
		return coroGlobalFunctionSlotValueProof{
			targets: make(map[*ssa.Function]struct{}),
			inactive: []coroGlobalFunctionSlotHazard{{
				owner: owner, instruction: call, reason: reason,
			}},
		}
	}
	if state == nil {
		state = &coroGlobalFunctionSlotValueState{
			writerParameters: make(map[*ssa.Parameter]bool),
			factoryResults:   make(map[coroGlobalFunctionSlotFactoryResult]bool),
		}
	}
	target, exact := coroExactGlobalFunctionSlotFactory(flow.ctx, call)
	if !exact {
		return hazard("global function cell factory result is not from one exact static Go body")
	}
	results := target.Signature.Results()
	if resultIndex < 0 || resultIndex >= results.Len() || !types.Identical(results.At(resultIndex).Type(), signature) {
		return hazard("global function cell factory result has an incompatible function signature")
	}
	// Key recursion by the canonical factory body and result, not by the SSA
	// call or substitution environment. A recursive return expression creates a
	// fresh call and environment at every step; including either pointer would
	// therefore miss self/mutual recursion and let proof construction recurse
	// forever. Re-entering the same canonical result while it is active is an
	// unresolved value-flow cycle regardless of the actual arguments.
	key := coroGlobalFunctionSlotFactoryResult{target: target, result: resultIndex}
	if state.factoryResults[key] {
		return hazard("global function cell factory result has cyclic return provenance")
	}
	state.factoryResults[key] = true
	defer delete(state.factoryResults, key)

	calleeEnvironment := &coroGlobalFunctionSlotValueEnvironment{
		parameters: make(map[*ssa.Parameter]coroGlobalFunctionSlotValueSubstitution, len(target.Params)),
	}
	for index, parameter := range target.Params {
		calleeEnvironment.parameters[parameter] = coroGlobalFunctionSlotValueSubstitution{
			value:       call.Common().Args[index],
			owner:       call.Parent(),
			instruction: call,
			environment: environment,
		}
	}
	proof := coroGlobalFunctionSlotValueProof{targets: make(map[*ssa.Function]struct{})}
	returns := 0
	for _, block := range target.Blocks {
		for _, current := range block.Instrs {
			returned, ok := current.(*ssa.Return)
			if !ok {
				continue
			}
			returns++
			if resultIndex >= len(returned.Results) {
				mergeCoroGlobalFunctionSlotValueProof(&proof,
					hazard("global function cell factory has a malformed return result"))
				continue
			}
			mergeCoroGlobalFunctionSlotValueProof(&proof, flow.proveStoredValue(
				returned.Results[resultIndex], signature, target, returned, calleeEnvironment, state,
			))
		}
	}
	if returns == 0 {
		return hazard("global function cell factory has no frozen return edge")
	}
	return proof
}

func coroDirectGlobalFunctionSlotLoad(value ssa.Value) (*ssa.Global, bool) {
	for value != nil {
		switch current := value.(type) {
		case *ssa.ChangeType:
			value = current.X
		case *ssa.Convert:
			value = current.X
		case *ssa.UnOp:
			if current.Op != token.MUL {
				return nil, false
			}
			global, ok := current.X.(*ssa.Global)
			return global, ok
		default:
			return nil, false
		}
	}
	return nil, false
}

func coroGlobalFunctionSlotType(global *ssa.Global) (*types.Signature, bool) {
	if global == nil || global.Type() == nil {
		return nil, false
	}
	pointer, ok := types.Unalias(global.Type()).Underlying().(*types.Pointer)
	if !ok {
		return nil, false
	}
	signature, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Signature)
	return signature, ok && signature != nil
}

func coroGlobalFunctionSlotSignature(global *ssa.Global, signature *types.Signature) bool {
	slot, ok := coroGlobalFunctionSlotType(global)
	return ok && signature != nil && types.Identical(slot, signature)
}

func coroExactGlobalFunctionSlotBareTarget(
	ctx *context,
	raw *ssa.Function,
	signature *types.Signature,
) (*ssa.Function, bool) {
	if ctx == nil || ctx.coroEmission == nil || raw == nil || len(raw.FreeVars) != 0 {
		return nil, false
	}
	target, exact := ctx.coroEmission.Resolve(raw)
	if !exact || target == nil || !coroContextFreeGlobalFunctionSlotTarget(target) ||
		!coroExactGlobalFunctionSlotTargetShape(ctx, target, signature) {
		return nil, false
	}
	return target, true
}

// coroExactGlobalFunctionSlotStoreTarget deliberately starts with the smallest
// source shape whose descriptor construction can be conditionally removed: one
// exact non-capturing function operand (possibly behind
// representation-preserving conversions). Parameter-fed writers and factories
// remain part of the closed dynamic-call proof, but do not acquire occurrence
// authority for demand suppression or Store elision here.
func coroExactGlobalFunctionSlotStoreTarget(
	ctx *context,
	store *ssa.Store,
	signature *types.Signature,
) (*ssa.Function, bool) {
	if store == nil || store.Val == nil {
		return nil, false
	}
	value := store.Val
	for value != nil {
		switch current := value.(type) {
		case *ssa.ChangeType:
			value = current.X
			continue
		case *ssa.Convert:
			value = current.X
			continue
		}
		break
	}
	raw, ok := value.(*ssa.Function)
	if !ok || raw == nil || len(raw.FreeVars) != 0 {
		return nil, false
	}
	return coroExactGlobalFunctionSlotBareTarget(ctx, raw, signature)
}

// coroContextFreeGlobalFunctionSlotTarget admits both a package declaration
// and a source function literal with no captured environment. x/tools exposes
// the latter as a bare *ssa.Function operand (not MakeClosure), and it has the
// same exact program-lifetime code identity as a top-level function. Synthetic
// wrappers remain excluded because their callable ABI/ownership is established
// by separate compiler mechanisms.
func coroContextFreeGlobalFunctionSlotTarget(target *ssa.Function) bool {
	if target == nil || len(target.FreeVars) != 0 || target.Synthetic != "" {
		return false
	}
	if target.Parent() == nil {
		return true
	}
	_, sourceLiteral := target.Syntax().(*ast.FuncLit)
	return sourceLiteral
}

func coroExactGlobalFunctionSlotClosureTarget(
	ctx *context,
	closure *ssa.MakeClosure,
	signature *types.Signature,
) (*ssa.Function, bool) {
	if ctx == nil || ctx.coroEmission == nil || closure == nil {
		return nil, false
	}
	raw, exact := closure.Fn.(*ssa.Function)
	if !exact || raw == nil || len(closure.Bindings) != len(raw.FreeVars) {
		return nil, false
	}
	target, resolved := ctx.coroEmission.Resolve(raw)
	// Captured environments are laid out from the exact target FreeVars. Patch
	// aliases may have source-compatible signatures but do not prove identical
	// environment storage, so only the canonical MakeClosure producer qualifies.
	if !resolved || target == nil || target != raw || len(closure.Bindings) != len(target.FreeVars) ||
		!coroExactGlobalFunctionSlotTargetShape(ctx, target, signature) {
		return nil, false
	}
	for index, binding := range closure.Bindings {
		if binding == nil || target.FreeVars[index] == nil ||
			!types.Identical(binding.Type(), target.FreeVars[index].Type()) {
			return nil, false
		}
	}
	return target, true
}

func coroExactGlobalFunctionSlotTargetShape(ctx *context, target *ssa.Function, signature *types.Signature) bool {
	if ctx == nil || target == nil || signature == nil || target.Signature == nil ||
		target.Signature.Recv() != nil || !types.Identical(target.Signature, signature) ||
		typeParamLen(target.Signature.TypeParams()) != 0 || typeParamLen(target.Signature.RecvTypeParams()) != 0 ||
		target.Origin() != nil || len(target.TypeArgs()) != 0 {
		return false
	}
	goBody, err := frozenGoEmittedBody(ctx.coroEmission, target)
	return err == nil && goBody
}

func coroExactGlobalFunctionSlotFactory(ctx *context, call *ssa.Call) (*ssa.Function, bool) {
	if ctx == nil || ctx.coroEmission == nil || call == nil || call.Common() == nil || call.Parent() == nil {
		return nil, false
	}
	common := call.Common()
	raw := common.StaticCallee()
	if raw == nil || common.IsInvoke() {
		return nil, false
	}
	target, resolved := ctx.coroEmission.Resolve(raw)
	if !resolved || target == nil || target != raw || target.Signature == nil ||
		target.Parent() != nil || len(target.FreeVars) != 0 || target.Signature.Recv() != nil || target.Signature.Variadic() ||
		typeParamLen(target.Signature.TypeParams()) != 0 || typeParamLen(target.Signature.RecvTypeParams()) != 0 ||
		target.Origin() != nil || len(target.TypeArgs()) != 0 || len(target.Params) != len(common.Args) {
		return nil, false
	}
	for index, parameter := range target.Params {
		if parameter == nil || common.Args[index] == nil || !types.Identical(parameter.Type(), common.Args[index].Type()) {
			return nil, false
		}
	}
	goBody, err := frozenGoEmittedBody(ctx.coroEmission, target)
	return target, err == nil && goBody
}

func coroClosedGlobalFunctionSlotWriter(owner *ssa.Function) bool {
	if owner == nil || owner.Signature == nil || owner.Parent() != nil || len(owner.FreeVars) != 0 ||
		owner.Signature.Recv() != nil || token.IsExported(owner.Name()) ||
		typeParamLen(owner.Signature.TypeParams()) != 0 || typeParamLen(owner.Signature.RecvTypeParams()) != 0 {
		return false
	}
	declaration, ok := owner.Syntax().(*ast.FuncDecl)
	if !ok {
		return false
	}
	if declaration.Doc == nil {
		return true
	}
	for _, comment := range declaration.Doc.List {
		text := strings.TrimSpace(comment.Text)
		for _, prefix := range []string{
			"//go:linkname", "//llgo:link", "// llgo:link", "//export", "//go:wasmexport", "//go:wasmimport",
		} {
			if strings.HasPrefix(text, prefix) {
				return false
			}
		}
	}
	return true
}

func mergeCoroGlobalFunctionSlotValueProof(destination *coroGlobalFunctionSlotValueProof, source coroGlobalFunctionSlotValueProof) {
	if destination.targets == nil {
		destination.targets = make(map[*ssa.Function]struct{})
	}
	for target := range source.targets {
		destination.targets[target] = struct{}{}
	}
	for _, publication := range source.stores {
		duplicate := false
		for _, previous := range destination.stores {
			if previous.store == publication.store && previous.owner == publication.owner && previous.target == publication.target {
				duplicate = true
				break
			}
		}
		if !duplicate {
			destination.stores = append(destination.stores, publication)
		}
	}
	for _, hazard := range source.inactive {
		destination.inactive = appendUniqueCoroGlobalFunctionSlotHazard(destination.inactive, hazard)
	}
}

func appendUniqueCoroGlobalFunctionSlotHazard(
	destination []coroGlobalFunctionSlotHazard,
	hazard coroGlobalFunctionSlotHazard,
) []coroGlobalFunctionSlotHazard {
	for _, previous := range destination {
		if previous.owner == hazard.owner && previous.instruction == hazard.instruction && previous.reason == hazard.reason {
			return destination
		}
	}
	return append(destination, hazard)
}
