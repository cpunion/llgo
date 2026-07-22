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
	"slices"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// freezeCallSites is the final pre-SSAPlan ProgramIR builder stage. Runtime
// helper closure, patch redirects, physical identities, and worker
// certificates must already be immutable. The stage validates raw SSA exactly
// once, writes the result into each owner-scoped SitePlan, and rejects any
// owner-dependent semantic result for one logical call occurrence. Physical
// function projections are added transactionally after the SSAPlan fixed point.
func (ir *coroProgramIR) freezeCallSites(u *EmissionUniverse) error {
	if ir == nil || u == nil {
		return fmt.Errorf("coroutine call SitePlan freeze requires one ProgramIR and emission universe")
	}
	if ir.callsFrozen {
		return fmt.Errorf("coroutine call SitePlans were frozen more than once")
	}
	for _, function := range u.functions {
		if function == nil {
			continue
		}
		for _, owner := range u.sortedUseOwners(function) {
			key := emissionFunctionOwnerKey{function: function, owner: owner}
			ctx, err := u.functionABIContext(function, owner)
			if err != nil {
				return fmt.Errorf("function %q call SitePlan context: %w", function.Name(), err)
			}
			_, _, functionKind := ctx.funcName(function)
			if functionKind != goFunc {
				continue
			}
			if _, frozen := ir.siteOwners[key]; !frozen {
				return fmt.Errorf("function %q call SitePlan has no frozen owner", function.Name())
			}
			byInstruction := ir.sitePlans[key]
			if byInstruction == nil {
				byInstruction = make(map[ssa.Instruction]coroEmissionSitePlan)
				ir.sitePlans[key] = byInstruction
			}
			for _, block := range function.Blocks {
				for _, instruction := range block.Instrs {
					call, ok := instruction.(ssa.CallInstruction)
					if !ok || call.Common() == nil {
						continue
					}
					site := byInstruction[instruction]
					noInit := FrontendElidesNoInitCall(call)
					patchRedirect := false
					var frozenPatchRedirect coroPatchInitRedirect
					var classifyErr error
					logicalName, patchTarget, redirected, redirectErr := u.CoroPatchInitRedirect(call)
					if redirectErr != nil {
						classifyErr = fmt.Errorf("classify frozen patch initializer replacement: %w", redirectErr)
					} else if redirected {
						patchRedirect = true
						frozenPatchRedirect = coroPatchInitRedirect{logicalName: logicalName, target: patchTarget}
					}
					semantics, intrinsic, opcode := CoroIntrinsicCallUnsupported, false, 0
					var workerCertificate CoroWorkerSyscallCertificate
					workerCertified := false
					if !noInit && !patchRedirect && classifyErr == nil {
						callee := call.Common().StaticCallee()
						if callee != nil {
							opcode, intrinsic, classifyErr = u.coroIntrinsicOpcode(callee)
						}
						if classifyErr == nil && intrinsic && isLLGoSyscallIntrinsic(opcode) && u.enableCoroWorker {
							if direct, ok := call.(*ssa.Call); ok && direct.Common() != nil && !direct.Common().IsInvoke() &&
								direct.Parent() != nil && u.canonicalAlias(direct.Parent()) == direct.Parent() {
								workerCertificate, workerCertified = u.workerSyscalls[direct]
							}
						}
						if classifyErr == nil {
							semantics, intrinsic, classifyErr = u.classifyCoroIntrinsicCallSite(
								ctx, site, call, opcode, intrinsic, workerCertificate, workerCertified,
							)
						}
					}
					plan := CoroCallSitePlan{
						IntrinsicSemantics: semantics,
						Intrinsic:          intrinsic,
					}
					switch {
					case noInit:
						plan.Elision = CoroCallElidedNoInit
					case patchRedirect:
						plan.Elision = CoroCallElidedPatchRedirect
					case intrinsic && classifyErr == nil && semantics.ElidesManagedCall():
						plan.Elision = CoroCallElidedIntrinsic
					}
					if plan.ElidesCall() && intrinsic && classifyErr == nil && workerCertified {
						if workerCertificate.ID == "" {
							classifyErr = fmt.Errorf("freeze intrinsic elision certificate: certified call has an empty identity")
						} else {
							plan.ElisionCertificate = workerCertificate.ID
						}
					}
					frozenCall := coroFrozenCallSitePlan{
						plan:              plan,
						opcode:            opcode,
						workerCertificate: workerCertificate,
						workerCertified:   workerCertified,
						patchRedirect:     frozenPatchRedirect,
						patchAttempted:    redirected || redirectErr != nil,
					}
					if workerCertified {
						frozenCall.workerOwners = cloneCoroWorkerOwnerSet(u.workerSyscallOwners[call])
						frozenCall.workerIncoming = cloneCoroWorkerIncomingEdges(u.workerSyscallIncoming[call])
					}
					if classifyErr != nil {
						frozenCall.failure = classifyErr.Error()
					}
					if previous, exists := ir.callPlans[call]; exists {
						if !sameCoroFrozenCallSitePlan(previous, frozenCall) {
							return fmt.Errorf("function %q call %q has owner-dependent frozen SitePlans", function.Name(), call.String())
						}
						frozenCall = previous
					}
					ir.callPlans[call] = frozenCall
					site.callPlan = frozenCall
					site.hasCallPlan = true
					byInstruction[instruction] = site
				}
			}
		}
	}
	ir.callsFrozen = true
	// These maps are mutable builder scratch. All production call-site
	// certificate payloads now live in ProgramIR; retaining a second readable
	// store would permit future consumers to bypass the frozen SitePlan.
	u.workerSyscalls = nil
	u.workerSyscallOwners = nil
	u.workerSyscallIncoming = nil
	u.patchInitRedirects = nil
	return nil
}

func cloneCoroWorkerOwnerSet(source map[*ssa.Function]none) map[*ssa.Function]none {
	if len(source) == 0 {
		return nil
	}
	result := make(map[*ssa.Function]none, len(source))
	for function := range source {
		result[function] = none{}
	}
	return result
}

func cloneCoroWorkerIncomingEdges(source []coroWorkerSyscallIncomingEdge) []coroWorkerSyscallIncomingEdge {
	if len(source) == 0 {
		return nil
	}
	result := append([]coroWorkerSyscallIncomingEdge(nil), source...)
	for index := range result {
		result[index].targetKeys = append([]string(nil), result[index].targetKeys...)
	}
	return result
}

func sameCoroFrozenCallSitePlan(first, second coroFrozenCallSitePlan) bool {
	if first.plan != second.plan || first.failure != second.failure || first.opcode != second.opcode ||
		first.workerCertificate != second.workerCertificate || first.workerCertified != second.workerCertified ||
		first.patchRedirect != second.patchRedirect || first.patchAttempted != second.patchAttempted ||
		len(first.workerOwners) != len(second.workerOwners) || len(first.workerIncoming) != len(second.workerIncoming) {
		return false
	}
	for function := range first.workerOwners {
		if _, exists := second.workerOwners[function]; !exists {
			return false
		}
	}
	for index, left := range first.workerIncoming {
		right := second.workerIncoming[index]
		if left.call != right.call || left.carrier != right.carrier || left.parameter != right.parameter ||
			left.certified != right.certified || left.reason != right.reason ||
			left.foreignPointerResultMask != right.foreignPointerResultMask ||
			left.resultProjectionID != right.resultProjectionID || left.stableIdentity != right.stableIdentity ||
			!slices.Equal(left.targetKeys, right.targetKeys) {
			return false
		}
	}
	return true
}

// CoroCallSitePlan returns the single frozen frontend call decision used by
// whole-program analysis, ABI closure checks, physical preflight, and
// emission. Invalid intrinsic shapes are retained as exact failed plans so all
// consumers report the same builder-owned diagnostic without rescanning SSA.
func (u *EmissionUniverse) CoroCallSitePlan(call ssa.CallInstruction) (CoroCallSitePlan, bool, error) {
	if u == nil || u.coroProgramIR == nil {
		return CoroCallSitePlan{}, false, fmt.Errorf("emission universe has no coroutine ProgramIR")
	}
	frozen, ok, err := u.coroProgramIR.callSitePlan(call)
	if err != nil || !ok {
		return CoroCallSitePlan{}, ok, err
	}
	if frozen.failure != "" {
		return frozen.plan, true, fmt.Errorf("%s", frozen.failure)
	}
	return frozen.plan, true, nil
}

// CoroLocalBodyFacts returns the ProgramIR-owned local semantic projection for
// one exact canonical function. Production whole-program analysis consumes
// this callback instead of rescanning raw SSA for Effect/Exec facts.
func (u *EmissionUniverse) CoroLocalBodyFacts(function *ssa.Function) (coro.SSAFunctionBodyFacts, error) {
	if u == nil || u.coroProgramIR == nil {
		return coro.SSAFunctionBodyFacts{}, fmt.Errorf("coroutine local body facts require a prepared ProgramIR")
	}
	canonical, frozen := u.Resolve(function)
	if !frozen || canonical == nil || canonical != function {
		return coro.SSAFunctionBodyFacts{}, fmt.Errorf("coroutine local body facts require one exact canonical function")
	}
	return u.coroProgramIR.functionLocalBodyFacts(function)
}

type coroCallSitePlanReader interface {
	CoroCallSitePlan(ssa.CallInstruction) (CoroCallSitePlan, bool, error)
}

// coroIntrinsicCallSiteSemantics is the package-local compatibility projection
// of the frozen call SitePlan. It performs no opcode or operand classification.
func coroIntrinsicCallSiteSemantics(reader coroCallSitePlanReader, call ssa.CallInstruction) (CoroIntrinsicCallSemantics, bool, error) {
	if reader == nil {
		return CoroIntrinsicCallUnsupported, false, fmt.Errorf("coroutine intrinsic projection requires a call SitePlan reader")
	}
	plan, found, err := reader.CoroCallSitePlan(call)
	if err != nil {
		return plan.IntrinsicSemantics, plan.Intrinsic, err
	}
	if !found {
		return CoroIntrinsicCallUnsupported, false, nil
	}
	return plan.IntrinsicSemantics, plan.Intrinsic, nil
}
