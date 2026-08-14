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
	"strconv"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func isCoroPythonIntrinsicOpcode(opcode int) bool {
	switch opcode {
	case llgoPyList, llgoPyTuple, llgoPyStr:
		return true
	default:
		return false
	}
}

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
	// Freeze WebAssembly imports here with the other ProgramIR source facts.
	// Keeping this inside the sole builder authority avoids creating another
	// plan reader or letting codegen reparse declaration comments.
	if u.prog != nil {
		target := u.prog.Target()
		if target != nil && target.GOARCH == "wasm" {
			for _, function := range u.functions {
				if function == nil {
					continue
				}
				spec, present, err := attachedWasmImportSource(function)
				if err != nil {
					return fmt.Errorf("prepare emission universe: function %q: %w", function.Name(), err)
				}
				if !present {
					continue
				}
				if canonical := u.canonicalAlias(function); canonical == nil || canonical != function {
					return fmt.Errorf("prepare emission universe: wasm import %q is not an exact canonical declaration", function.Name())
				}
				if _, exists := ir.wasmImports[function]; exists {
					return fmt.Errorf("prepare emission universe: duplicate frozen wasm import for %q", function.Name())
				}
				ir.wasmImports[function] = spec
			}
		}
	}
	// RawCritical is deliberately stronger than nopreempt/nounwind. Freeze an
	// occurrence-level native-stack capability for each exact ordinary static
	// call before the owner-scoped SitePlans consume it. Names, addresses,
	// interface values, methods, defer, go, and recursive entry never match.
	u.rawCriticalCalls = make(map[ssa.CallInstruction]string)
	for _, caller := range u.functions {
		caller = u.canonicalAlias(caller)
		if caller == nil || len(caller.Blocks) == 0 {
			continue
		}
		callerIdentity := u.finalIdentity(caller)
		if callerIdentity == "" || callerIdentity == "<nil>" || callerIdentity == "<cyclic-alias>" {
			return fmt.Errorf("prepare emission universe: raw-critical caller %q has no exact identity", caller.Name())
		}
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ordinary := instruction.(*ssa.Call)
				if !ordinary || call == nil || call.Common() == nil || call.Common().IsInvoke() ||
					call.Common().Method != nil {
					continue
				}
				target := u.canonicalAlias(call.Common().StaticCallee())
				bodyCertificate, critical := u.rawCritical[target]
				if !critical {
					continue
				}
				if target == nil || target == caller {
					return fmt.Errorf(
						"prepare emission universe: raw-critical call in %q has an invalid recursive target",
						caller.Name(),
					)
				}
				if _, required := u.required[target]; !required {
					return fmt.Errorf(
						"prepare emission universe: raw-critical target %q is outside the frozen program",
						target.Name(),
					)
				}
				targetIdentity := u.finalIdentity(target)
				if targetIdentity == "" || targetIdentity == "<nil>" || targetIdentity == "<cyclic-alias>" {
					return fmt.Errorf("prepare emission universe: raw-critical target %q has no exact identity", target.Name())
				}
				semantic, err := coro.SemanticInstructionOrdinal(call)
				if err != nil {
					return fmt.Errorf(
						"prepare emission universe: identify raw-critical call in %q: %w",
						caller.Name(), err,
					)
				}
				u.rawCriticalCalls[call] = emissionDigest(framedEmissionKey(
					coroRawCriticalCallCertificateDomain,
					bodyCertificate,
					callerIdentity,
					strconv.Itoa(block.Index),
					strconv.Itoa(semantic),
					targetIdentity,
				))
			}
		}
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
					if box, ok := instruction.(*ssa.MakeInterface); ok {
						if err := u.freezeCoroErasedPythonFunctionInterface(ctx, box); err != nil {
							return fmt.Errorf(
								"function %q erased Python function interface: %w",
								function.Name(), err,
							)
						}
					}
					call, ok := instruction.(ssa.CallInstruction)
					if !ok || call.Common() == nil {
						continue
					}
					site := byInstruction[instruction]
					semantic, semanticFound := ir.semanticPlans[key][instruction]
					if !semanticFound {
						return fmt.Errorf("function %q call %q has no frozen semantic SitePlan", function.Name(), call.String())
					}
					frontendUnevaluated := !semantic.evaluated
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
					var cgoWorkerCertificate CoroCgoWorkerCallCertificate
					var cgoWorkerTarget *ssa.Function
					cgoWorkerCertified := false
					var cgoErrnoCertificate CoroCgoWorkerCallCertificate
					cgoErrnoCertified := false
					if !frontendUnevaluated && !noInit && !patchRedirect && classifyErr == nil {
						cgoWorkerCertificate, cgoWorkerTarget, cgoWorkerCertified, classifyErr =
							u.freezeCoroCgoWorkerCallCertificate(ctx, call)
					}
					semantics, intrinsic, opcode := CoroIntrinsicCallUnsupported, false, 0
					controlOperation := CoroControlNone
					pythonOperation := false
					var pythonTarget *ssa.Function
					var hostOperation coroHostOperationCallShape
					var workerCertificate CoroWorkerSyscallCertificate
					workerCertified := false
					if !frontendUnevaluated && !noInit && !patchRedirect && !cgoWorkerCertified && classifyErr == nil {
						callee := call.Common().StaticCallee()
						if callee != nil {
							// ProgramIR inventories fallback bodies even when their
							// frontend declaration is replaced and the fallback's
							// private callees are deliberately absent from the
							// emission universe. Preserve the source site, but only
							// classify an intrinsic edge for a frozen target.
							if resolved, frozen := u.Resolve(callee); frozen {
								background, classified, backgroundErr := u.FunctionBackground(resolved)
								if backgroundErr != nil {
									classifyErr = fmt.Errorf("classify frozen Python operation: %w", backgroundErr)
								} else if classified && background == llssa.InPython {
									pythonOperation = true
									pythonTarget = resolved
								} else {
									opcode, intrinsic, classifyErr = u.coroIntrinsicOpcode(resolved)
									if intrinsic && isCoroPythonIntrinsicOpcode(opcode) {
										pythonOperation = true
										intrinsic = false
									} else if intrinsic {
										controlOperation = coroControlOperationForIntrinsic(opcode)
									}
								}
							}
						}
						if classifyErr == nil && pythonOperation {
							if _, direct := call.(*ssa.Call); !direct {
								classifyErr = fmt.Errorf("Python operation requires one exact direct call")
							} else if !isCoroProgramManagedEntry(function) {
								classifyErr = fmt.Errorf(
									"Python operation in %q has no compiler-owned program-root owner realm",
									function.Name(),
								)
							}
						}
						if classifyErr == nil && intrinsic && opcode == llgoCgoCgocall {
							cgoErrnoCertificate, cgoErrnoCertified, classifyErr =
								ctx.freezeCoroCgoErrnoWorkerCallCertificate(call)
						}
						if classifyErr == nil && intrinsic && opcode == llgoCoroHostOperation {
							direct, ok := call.(*ssa.Call)
							if !ok {
								classifyErr = fmt.Errorf(
									"llgo.coroHostOperation requires one exact direct call",
								)
							} else {
								hostOperation, classifyErr = planCoroHostOperationCallShape(direct)
							}
						}
						if classifyErr == nil && intrinsic && isLLGoSyscallIntrinsic(opcode) && u.CoroWorkerSupported() {
							if direct, ok := call.(*ssa.Call); ok && direct.Common() != nil && !direct.Common().IsInvoke() &&
								direct.Parent() != nil && u.canonicalAlias(direct.Parent()) == direct.Parent() {
								workerCertificate, workerCertified = u.workerSyscalls[direct]
							}
						}
						if classifyErr == nil && !pythonOperation {
							semantics, intrinsic, classifyErr = u.classifyCoroIntrinsicCallSite(
								ctx, site, call, opcode, intrinsic, controlOperation,
								workerCertificate, workerCertified,
								cgoErrnoCertificate, cgoErrnoCertified,
							)
						}
					}
					rawPlainSynchronousIntrinsic := false
					if classifyErr == nil && intrinsic && isLLGoSyscallIntrinsic(opcode) {
						direct, ok := call.(*ssa.Call)
						if !ok {
							classifyErr = fmt.Errorf(
								"raw/plain llgo.syscall requires one exact direct call",
							)
						} else if validateErr := planCoroSynchronousSyscallShape(direct); validateErr != nil {
							classifyErr = validateErr
						} else {
							rawPlainSynchronousIntrinsic = true
						}
					}
					var managedStaticTarget *ssa.Function
					var managedStaticCertificate coroLocalExportBindingCertificate
					managedStaticTarget, managedStaticCertificate, managedStatic, managedStaticErr :=
						coroLocalExportManagedCallTarget(
							u.localExportBindings,
							u.required,
							u.canonicalAlias,
							call,
						)
					if classifyErr == nil && managedStaticErr != nil {
						classifyErr = managedStaticErr
					}
					if managedStatic && classifyErr == nil &&
						(frontendUnevaluated || noInit || patchRedirect ||
							cgoWorkerCertified || pythonOperation || intrinsic) {
						classifyErr = fmt.Errorf(
							"local-export managed call redirect overlaps an elided, patched, worker, or intrinsic recipe",
						)
					}
					plan := CoroCallSitePlan{
						IntrinsicSemantics:           semantics,
						Intrinsic:                    intrinsic,
						ControlOperation:             controlOperation,
						RawPlainSynchronousIntrinsic: rawPlainSynchronousIntrinsic,
					}
					if pythonOperation && classifyErr == nil {
						semanticOrdinal, err := coro.SemanticInstructionOrdinal(call)
						if err != nil {
							classifyErr = fmt.Errorf("identify Python operation: %w", err)
						} else {
							targetIdentity := "python-intrinsic:" + strconv.Itoa(opcode)
							if pythonTarget != nil {
								targetIdentity = u.finalIdentity(pythonTarget)
								if targetIdentity == "" || targetIdentity == "<nil>" || targetIdentity == "<cyclic-alias>" {
									classifyErr = fmt.Errorf("Python operation target %q has no exact identity", pythonTarget.Name())
								}
							}
							if classifyErr == nil {
								plan.ElisionCertificate = emissionDigest(framedEmissionKey(
									"cl-coro-python-operation-v1",
									u.finalIdentity(function),
									strconv.Itoa(block.Index),
									strconv.Itoa(semanticOrdinal),
									targetIdentity,
								))
								plan.PythonTarget = pythonTarget
							}
						}
					}
					if managedStatic && classifyErr == nil {
						plan.ManagedStaticTarget = managedStaticTarget
						plan.ManagedStaticTargetCertificate = managedStaticCertificate.ID
					}
					if spawn, spawned := call.(*ssa.Go); spawned && classifyErr == nil {
						if _, builtin := spawn.Common().Value.(*ssa.Builtin); builtin {
							wrapper, wrapped := u.builtinSpawnWraps[builtinSpawnWrapperKey{
								owner: ctx.goPkg,
								spawn: spawn,
							}]
							if !wrapped || wrapper == nil {
								classifyErr = fmt.Errorf(
									"spawned builtin %q has no compiler-owned coroutine carrier",
									spawn.Common().Value.Name(),
								)
							} else {
								plan.StaticSpawnTarget = wrapper
							}
						}
					}
					if rawCertificate := u.rawCriticalCalls[call]; rawCertificate != "" {
						if frontendUnevaluated || noInit || patchRedirect || cgoWorkerCertified ||
							pythonOperation || intrinsic || managedStatic {
							classifyErr = fmt.Errorf(
								"raw-critical call overlaps an elided, redirected, generated-worker, or intrinsic recipe",
							)
						} else {
							plan.RawPlain = true
							plan.RawPlainCertificate = rawCertificate
						}
					}
					intrinsicPlacement := coroRuntimeHelperAtSource
					if _, deferred := call.(*ssa.Defer); deferred && intrinsic && classifyErr == nil &&
						isCoroAtomicIntrinsic(opcode) {
						intrinsicPlacement = coroRuntimeHelperAtCleanup
					}
					if _, spawned := call.(*ssa.Go); spawned && intrinsic && classifyErr == nil &&
						plan.StaticSpawnTarget == nil &&
						isCoroAtomicIntrinsic(opcode) {
						callee := call.Common().StaticCallee()
						wrapper, wrapped := u.intrinsicWrapper(ctx.goPkg, callee)
						if !wrapped || wrapper == nil {
							classifyErr = fmt.Errorf(
								"spawned inline intrinsic %q has no compiler-owned coroutine carrier",
								callee.Name(),
							)
						} else {
							plan.StaticSpawnTarget = wrapper
						}
					}
					switch {
					case frontendUnevaluated:
						plan.Elision = CoroCallElidedFrontendUnevaluated
					case noInit:
						plan.Elision = CoroCallElidedNoInit
					case patchRedirect:
						plan.Elision = CoroCallElidedPatchRedirect
					case cgoWorkerCertified:
						plan.Elision = CoroCallElidedCgoWorker
						plan.ElisionCertificate = cgoWorkerCertificate.ID
						plan.CgoWorkerTarget = cgoWorkerTarget
					case pythonOperation:
						plan.Elision = CoroCallElidedPython
					case intrinsic && classifyErr == nil && semantics.ElidesManagedCall() && plan.StaticSpawnTarget == nil:
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
						plan:               plan,
						opcode:             opcode,
						intrinsicPlacement: intrinsicPlacement,
						hostOperation:      hostOperation,
						workerCertificate:  workerCertificate,
						workerCertified:    workerCertified,
						cgoWorker:          cgoWorkerCertificate,
						patchRedirect:      frozenPatchRedirect,
						patchAttempted:     redirected || redirectErr != nil,
					}
					if cgoErrnoCertified {
						frozenCall.cgoWorker = cgoErrnoCertificate
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
	if err := ir.finalizeOutcomePlainIntrinsicSemantics(u.prog, u.functions, u.sortedUseOwners); err != nil {
		return fmt.Errorf("finalize outcome-plain intrinsic semantics: %w", err)
	}
	ir.callsFrozen = true
	// These maps are mutable builder scratch. All production call-site
	// certificate payloads now live in ProgramIR; retaining a second readable
	// store would permit future consumers to bypass the frozen SitePlan.
	u.workerSyscalls = nil
	u.workerSyscallOwners = nil
	u.workerSyscallIncoming = nil
	u.patchInitRedirects = nil
	u.rawCriticalCalls = nil
	u.rawCritical = nil
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
		first.intrinsicPlacement != second.intrinsicPlacement ||
		first.hostOperation != second.hostOperation ||
		first.workerCertificate != second.workerCertificate || first.workerCertified != second.workerCertified ||
		first.cgoWorker != second.cgoWorker ||
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
			left.resultProjectionID != right.resultProjectionID ||
			left.trapPolicyIdentity != right.trapPolicyIdentity ||
			left.stableIdentity != right.stableIdentity ||
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
