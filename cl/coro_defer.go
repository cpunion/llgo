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
	"go/token"
	"go/types"

	"github.com/goplus/llgo/cl/blocks"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// PhysicalABIV1 cannot use LLGo's legacy setjmp/TLS defer chain: that chain
// describes a native activation, while a stackless coroutine activation lives
// in the LLVM coroutine frame. Acyclic sites use one frame-resident active bit
// and typed argument slots per site. If any site is cyclic, every site in that
// function instead pushes one managed, typed record onto a frame-rooted LIFO
// chain. Using one chain for the whole function preserves registration order
// when acyclic and cyclic sites interleave; exact site tags select statically
// compiled call paths, so no function pointer is recovered from scalar data.
type coroStaticCleanupTargetKind uint8

const (
	coroStaticCleanupPlain coroStaticCleanupTargetKind = iota
	coroStaticCleanupCoroutine
	coroStaticCleanupDispatch
	coroStaticCleanupIntrinsic
	coroStaticCleanupBuiltin
	coroStaticCleanupCgoWorker
)

type coroStaticCleanupSitePlan struct {
	instruction       *ssa.Defer
	target            *ssa.Function
	targetPlan        coro.FunctionPlan
	kind              coroStaticCleanupTargetKind
	closure           *ssa.MakeClosure
	descriptor        ssa.Value
	interfaceReceiver ssa.Value
	interfaceMethod   *types.Func
	signature         *types.Signature
	callPlan          coro.SSACallPlan
	intrinsic         int
	builtin           string
	cgoWorker         *coroWorkerCgoCallShape
	tag               uint32
}

type coroStaticCleanupPlan struct {
	sites                     []*coroStaticCleanupSitePlan
	terminalResultAllocations []*ssa.Alloc
	dynamic                   bool
	external                  bool
	stackOwner                *ssa.Function
	dynamicTrigger            *ssa.Defer
	dynamicAlloc              *ssa.Function
	dynamicFree               *ssa.Function
}

// CoroStaticCleanupPlainTarget reports the narrow EmitPlain exception usable
// by explicit-status entry resolution.  Every planned call consumer of target
// must be a certified static defer site; roots, compiler-inserted calls, and
// any ordinary/spawn/dynamic call keep the result false.  This is intentionally
// a query over the frozen SSA plan rather than a symbol-name annotation.
func (u *EmissionUniverse) CoroStaticCleanupPlainTarget(
	whole *coro.SSAPlan,
	target *ssa.Function,
	frameRetentionABI string,
) (bool, error) {
	if u == nil || whole == nil || target == nil {
		return false, fmt.Errorf("static cleanup plain-target query requires a universe, plan, and target")
	}
	targetPlan, ok := whole.FunctionPlan(target)
	if !ok {
		return false, fmt.Errorf("static cleanup plain-target query: target %q is absent from the plan", target.Name())
	}
	if targetPlan.External != coro.Defined || targetPlan.Emission != coro.EmitPlain ||
		targetPlan.Primary != coro.PrimaryPlain || targetPlan.FuncRep != coro.DirectPlain ||
		targetPlan.Demand == coro.NoDemand || targetPlan.Effect != coro.NoSuspend {
		return false, nil
	}
	for _, root := range whole.Roots() {
		if root.Function == target {
			return false, nil
		}
	}
	for _, owner := range whole.Functions() {
		for _, lowered := range whole.LoweredCalls(owner.Function) {
			if lowered.Target == target {
				return false, nil
			}
		}
	}

	certified := false
	for _, owner := range whole.Functions() {
		function := owner.Function
		if function == nil {
			continue
		}
		needsOwnerProof := false
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				callPlan, planned := whole.CallPlan(call)
				if !planned || !coroCleanupCallPlanContains(callPlan, targetPlan.ID) {
					continue
				}
				if _, deferCall := instruction.(*ssa.Defer); !deferCall || callPlan.Kind != coro.CallDefer {
					return false, nil
				}
				needsOwnerProof = true
			}
		}
		if !needsOwnerProof {
			continue
		}
		ownerCleanup, err := prepareCoroStaticCleanupPlan(
			function, whole, u, frameRetentionABI, true,
		)
		if err != nil {
			return false, fmt.Errorf("static cleanup plain-target query: owner %q: %w", owner.Plan.ID, err)
		}
		for _, site := range ownerCleanup.sites {
			if site.target == target && site.kind == coroStaticCleanupPlain {
				certified = true
			}
		}
	}
	return certified, nil
}

func coroCleanupCallPlanContains(plan coro.SSACallPlan, target coro.FunctionID) bool {
	for _, candidate := range plan.Targets {
		if candidate == target {
			return true
		}
	}
	return false
}

// range-over-func yield closures register their Defer instructions in the
// explicit stack created by the nearest non-yield source function. The stack
// handle is captured through any nested yield closures, so ownership follows
// the synthetic parent chain rather than the function that contains the Defer.
func coroExplicitDeferStackOwner(fn *ssa.Function) *ssa.Function {
	if fn == nil {
		return nil
	}
	for fn != nil && fn.Synthetic == rangeOverFuncYieldSynthetic {
		fn = fn.Parent()
	}
	return fn
}

func coroFunctionHasExplicitDeferStack(fn *ssa.Function) bool {
	if fn == nil {
		return false
	}
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if deferred, ok := instruction.(*ssa.Defer); ok && deferred.DeferStack != nil {
				return true
			}
		}
	}
	return false
}

// coroExplicitCleanupFamily returns the one source owner followed by its
// nested range-over-func yield closures in stable SSA anonymous-function order.
// Ordinary function literals are separate defer owners and are not traversed.
func coroExplicitCleanupFamily(owner *ssa.Function) []*ssa.Function {
	if owner == nil {
		return nil
	}
	result := []*ssa.Function{owner}
	var appendYields func(*ssa.Function)
	appendYields = func(parent *ssa.Function) {
		for _, child := range parent.AnonFuncs {
			if child == nil || child.Synthetic != rangeOverFuncYieldSynthetic {
				continue
			}
			result = append(result, child)
			appendYields(child)
		}
	}
	appendYields(owner)
	return result
}

func coroExplicitCleanupFamilySites(owner *ssa.Function) []*ssa.Defer {
	var result []*ssa.Defer
	for _, function := range coroExplicitCleanupFamily(owner) {
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				deferred, ok := instruction.(*ssa.Defer)
				if !ok {
					continue
				}
				if function != owner && deferred.DeferStack == nil {
					continue
				}
				result = append(result, deferred)
			}
		}
	}
	return result
}

func prepareCoroStaticCleanupPlan(
	fn *ssa.Function,
	whole *coro.SSAPlan,
	universe *EmissionUniverse,
	frameRetentionABI string,
	explicitPanic bool,
) (*coroStaticCleanupPlan, error) {
	if fn == nil || whole == nil {
		return nil, nil
	}
	caller, ok := whole.FunctionPlan(fn)
	if !ok {
		return nil, fmt.Errorf("function %q has no compilation plan", fn.Name())
	}
	stackOwner := coroExplicitDeferStackOwner(fn)
	external := stackOwner != nil && stackOwner != fn && coroFunctionHasExplicitDeferStack(fn)
	scanFunctions := []*ssa.Function{fn}
	if !external && stackOwner == fn {
		scanFunctions = coroExplicitCleanupFamily(fn)
	}
	infos := blocks.Infos(fn.Blocks)
	dynamicCleanup := coroFunctionRequiresDynamicCleanup(fn)
	if !external && len(scanFunctions) != 1 {
		for _, candidate := range scanFunctions[1:] {
			if coroFunctionHasExplicitDeferStack(candidate) {
				dynamicCleanup = true
				break
			}
		}
	}
	byInstruction := make(map[*ssa.Defer]*coroStaticCleanupSitePlan)
	allSites := make([]*coroStaticCleanupSitePlan, 0)
	var dynamicTrigger *ssa.Defer
	runDefers := 0
	for _, siteFunction := range scanFunctions {
		siteCaller, planned := whole.FunctionPlan(siteFunction)
		if !planned {
			return nil, fmt.Errorf("cleanup site function %q has no compilation plan", siteFunction.Name())
		}
		siteInfos := blocks.Infos(siteFunction.Blocks)
		for _, block := range siteFunction.Blocks {
			for instructionIndex, raw := range block.Instrs {
				switch instruction := raw.(type) {
				case *ssa.Defer:
					if siteFunction != fn && instruction.DeferStack == nil {
						continue
					}
					if external && instruction.DeferStack == nil {
						return nil, fmt.Errorf("range-over-func yield defer in block %d does not use its owner stack", block.Index)
					}
					if (instruction.DeferStack != nil || siteInfos[block.Index].InLoop) && dynamicTrigger == nil {
						dynamicTrigger = instruction
					}
					var cgoWorkerShape coroWorkerCgoCallShape
					var cgoWorker bool
					var err error
					if universe != nil {
						cgoWorkerShape, cgoWorker, err = validateCoroWorkerCgoCall(
							whole, universe, instruction,
						)
					}
					if err != nil {
						return nil, fmt.Errorf("defer in block %d: cgo worker cleanup: %w", block.Index, err)
					}
					var intrinsicOpcode int
					var intrinsic bool
					if universe != nil {
						if !cgoWorker {
							intrinsicOpcode, intrinsic, err = universe.coroProgramIR.deferredIntrinsicCleanupRecipe(instruction)
						}
					}
					if err != nil {
						return nil, fmt.Errorf("defer in block %d: %w", block.Index, err)
					}
					var target *ssa.Function
					var targetPlan coro.FunctionPlan
					kind := coroStaticCleanupIntrinsic
					builtinName := ""
					if cgoWorker {
						target = cgoWorkerShape.target
						var planned bool
						targetPlan, planned = whole.FunctionPlan(target)
						if !planned {
							return nil, fmt.Errorf("defer in block %d: cgo worker target has no function plan", block.Index)
						}
						kind = coroStaticCleanupCgoWorker
					} else if !intrinsic {
						if builtin, ok := instruction.Call.Value.(*ssa.Builtin); ok {
							builtinName, err = validateCoroDeferredBuiltinCleanup(instruction, dynamicCleanup)
							if err != nil {
								return nil, fmt.Errorf("defer in block %d: %w", block.Index, err)
							}
							if builtinName != builtin.Name() {
								return nil, fmt.Errorf("defer in block %d: builtin cleanup identity changed", block.Index)
							}
							kind = coroStaticCleanupBuiltin
						} else if instruction.Common().IsInvoke() {
							kind = coroStaticCleanupDispatch
						} else {
							target, targetPlan, kind, err = resolveCoroStaticCleanupTarget(whole, siteCaller, instruction, universe)
							if err != nil {
								return nil, fmt.Errorf("defer in block %d: %w", block.Index, err)
							}
						}
					}
					closure, _ := instruction.Call.Value.(*ssa.MakeClosure)
					// A plain defer still executes inline on this native activation and
					// therefore needs a strict no-unwind proof. A coroutine defer returns
					// Panic through the parent-owned CompletionRecord; awaitCoroChild
					// re-enters this same drainer after clearing the active site, so older
					// records still run with the replacement panic value.
					if kind == coroStaticCleanupPlain {
						if reason := validateCoroStaticCleanupNoUnwind(
							whole, universe, target, targetPlan, frameRetentionABI,
						); reason != "" {
							return nil, fmt.Errorf("plain defer target %q has no exact no-unwind proof: %s", targetPlan.ID, reason)
						}
					}
					site := &coroStaticCleanupSitePlan{
						instruction: instruction,
						target:      target,
						targetPlan:  targetPlan,
						kind:        kind,
						closure:     closure,
						intrinsic:   intrinsicOpcode,
						builtin:     builtinName,
					}
					if cgoWorker {
						shape := cgoWorkerShape
						site.cgoWorker = &shape
					}
					if kind == coroStaticCleanupDispatch {
						callPlan, planned := whole.CallPlan(instruction)
						if !planned {
							return nil, fmt.Errorf("defer in block %d: managed descriptor cleanup lost its CallPlan", block.Index)
						}
						site.callPlan = callPlan
						if instruction.Common().IsInvoke() {
							if callPlan.Open {
								if err := validateCoroManagedInterfaceDispatchCall(
									whole, universe, siteFunction, instruction, callPlan,
								); err != nil {
									return nil, fmt.Errorf("defer in block %d: %w", block.Index, err)
								}
							} else if _, err := resolveCoroInterfaceDispatchPlan(whole, universe, instruction); err != nil {
								return nil, fmt.Errorf("defer in block %d: managed interface cleanup: %w", block.Index, err)
							}
							site.interfaceReceiver = instruction.Call.Value
							site.interfaceMethod = instruction.Call.Method
							site.signature, err = coroInterfaceDispatchSourceSignature(instruction.Common())
							if err != nil {
								return nil, fmt.Errorf("defer in block %d: managed interface cleanup: %w", block.Index, err)
							}
						} else {
							site.descriptor = instruction.Call.Value
							site.signature = instruction.Call.Signature()
						}
						if err := validateCoroManagedCleanupPlainTargets(
							whole, universe, callPlan, frameRetentionABI,
						); err != nil {
							return nil, fmt.Errorf("defer in block %d: %w", block.Index, err)
						}
					}
					byInstruction[instruction] = site
					allSites = append(allSites, site)
				case *ssa.RunDefers:
					if siteFunction != fn {
						continue
					}
					if !coroStaticRunDefersReturns(block, instructionIndex) {
						return nil, fmt.Errorf("RunDefers in block %d is not followed only by named-result reloads and the terminal Return", block.Index)
					}
					runDefers++
				}
			}
		}
	}

	if len(byInstruction) == 0 {
		if caller.Exec.Contains(coro.NeedsCleanupFrame) || runDefers != 0 {
			return nil, fmt.Errorf("needs-cleanup-frame body has no supported static defer site")
		}
		return nil, nil
	}
	if !caller.Exec.Contains(coro.NeedsCleanupFrame) {
		return nil, fmt.Errorf("static defer body lacks needs-cleanup-frame execution classification")
	}
	if external {
		if !explicitPanic {
			return nil, fmt.Errorf("external coroutine defer registration requires the explicit-status panic ABI")
		}
		if dynamicTrigger == nil || stackOwner == nil {
			return nil, fmt.Errorf("external coroutine defer registration has no exact owner stack")
		}
		familySites := coroExplicitCleanupFamilySites(stackOwner)
		tags := make(map[*ssa.Defer]uint32, len(familySites))
		for index, instruction := range familySites {
			if uint64(index) >= uint64(^uint32(0)) {
				return nil, fmt.Errorf("external cleanup site count %d exceeds the stable tag space", len(familySites))
			}
			tags[instruction] = uint32(index) + 1
		}
		for _, site := range allSites {
			tag := tags[site.instruction]
			if tag == 0 {
				return nil, fmt.Errorf("external cleanup site %q is absent from owner %q", site.instruction, stackOwner.Name())
			}
			site.tag = tag
		}
		allocator, allocOK := whole.ResolveLoweredCall(fn, "AllocU")
		releaser, freeOK := whole.ResolveLoweredCall(fn, "FreeDeferNode")
		if !allocOK || allocator == nil || !freeOK || releaser == nil {
			return nil, fmt.Errorf("external cleanup requires exact registration-owner AllocU and FreeDeferNode edges")
		}
		return &coroStaticCleanupPlan{
			sites:          allSites,
			dynamic:        true,
			external:       true,
			stackOwner:     stackOwner,
			dynamicTrigger: dynamicTrigger,
			dynamicAlloc:   allocator,
			dynamicFree:    releaser,
		}, nil
	}
	if fn.Recover == nil {
		return nil, fmt.Errorf("static defer body has no canonical recover block")
	}
	if runDefers == 0 && coroStaticCleanupHasReachableNormalReturn(fn) {
		return nil, fmt.Errorf("static defer body has a reachable normal Return but no RunDefers instruction")
	}
	if !explicitPanic {
		return nil, fmt.Errorf("static coroutine defer cleanup requires the explicit-status panic ABI; legacy panic cannot guarantee cleanup")
	}
	terminalResultAllocations, err := coroStaticTerminalReconstructionAllocations(fn)
	if err != nil {
		return nil, err
	}
	if dynamicTrigger != nil {
		if uint64(len(allSites)) > uint64(^uint32(0)) {
			return nil, fmt.Errorf("dynamic cleanup site count %d exceeds the stable tag space", len(allSites))
		}
		for index, site := range allSites {
			// Zero remains an invalid/corrupt record marker. Source block and
			// instruction order are immutable in the prepared SSA universe, so this
			// one-based tag is deterministic for validation and code generation.
			site.tag = uint32(index) + 1
		}
		helperOwner := dynamicTrigger.Parent()
		allocator, allocOK := whole.ResolveLoweredCall(helperOwner, "AllocU")
		releaser, freeOK := whole.ResolveLoweredCall(helperOwner, "FreeDeferNode")
		if !allocOK || allocator == nil || !freeOK || releaser == nil {
			return nil, fmt.Errorf("dynamic cleanup requires exact owner-scoped AllocU and FreeDeferNode edges")
		}
		return &coroStaticCleanupPlan{
			sites:                     allSites,
			terminalResultAllocations: terminalResultAllocations,
			dynamic:                   true,
			stackOwner:                fn,
			dynamicTrigger:            dynamicTrigger,
			dynamicAlloc:              allocator,
			dynamicFree:               releaser,
		}, nil
	}

	// blocks.Infos' Next chain is a topological order outside SCCs.  Defer
	// sites in SCCs were rejected above, so reversing this list later is the
	// exact registration order for every path on which two sites both ran.
	ordered := make([]*coroStaticCleanupSitePlan, 0, len(byInstruction))
	for index := 0; index >= 0; index = infos[index].Next {
		for _, raw := range fn.Blocks[index].Instrs {
			if instruction, ok := raw.(*ssa.Defer); ok {
				ordered = append(ordered, byInstruction[instruction])
			}
		}
	}
	if len(ordered) != len(byInstruction) {
		return nil, fmt.Errorf("static defer order covers %d of %d sites", len(ordered), len(byInstruction))
	}
	return &coroStaticCleanupPlan{
		sites:                     ordered,
		terminalResultAllocations: terminalResultAllocations,
	}, nil
}

// coroStaticCleanupHasReachableNormalReturn distinguishes the two valid SSA
// exit shapes for a function with defer:
//
//   - a normal source return is preceded by RunDefers;
//   - a function whose only source exit is panic has no RunDefers at all and
//     resumes through fn.Recover after the coroutine cleanup drainer recovers.
//
// fn.Recover is deliberately excluded even if a future x/tools version adds a
// CFG edge to it: that block is the exceptional continuation owned by
// enterPanic, not a normal source return.
func coroStaticCleanupHasReachableNormalReturn(fn *ssa.Function) bool {
	if fn == nil {
		return false
	}
	reachable := coroPhysicalConstantReachableBlocks(fn)
	for block := range reachable {
		if block == nil || block == fn.Recover {
			continue
		}
		for _, instruction := range block.Instrs {
			if _, ok := instruction.(*ssa.Return); ok {
				return true
			}
		}
	}
	return false
}

// validateCoroManagedCleanupPlainTargets closes the one unwind hole in
// cleanup-time descriptor dispatch. A coroutine capability reports Panic via
// its child CompletionRecord; a plain capability executes inline in the
// drainer and therefore must have the same exact no-unwind proof as a static
// plain defer. Until the descriptor producer ABI publishes an equivalent
// capability bit, an open set is deliberately rejected: an unknown HasPlain
// target cannot be inferred safe from its function type.
func validateCoroManagedCleanupPlainTargets(
	whole *coro.SSAPlan,
	universe *EmissionUniverse,
	callPlan coro.SSACallPlan,
	frameRetentionABI string,
) error {
	if whole == nil {
		return fmt.Errorf("managed descriptor cleanup requires a compilation plan")
	}
	if callPlan.Open {
		return fmt.Errorf("open managed descriptor cleanup has no plain no-unwind producer invariant")
	}
	for _, targetID := range callPlan.Targets {
		target, found := whole.Function(targetID)
		if !found || target == nil {
			return fmt.Errorf("managed descriptor cleanup target %q is absent from the plan", targetID)
		}
		targetPlan, found := whole.FunctionPlan(target)
		if !found || targetPlan.ID != targetID {
			return fmt.Errorf("managed descriptor cleanup target %q has no canonical function plan", targetID)
		}
		switch targetPlan.Emission {
		case coro.EmitCoroutine:
			// The direct child completion transaction owns unwind/recovery.
		case coro.EmitPlain:
			if reason := validateCoroStaticCleanupNoUnwind(
				whole, universe, target, targetPlan, frameRetentionABI,
			); reason != "" {
				return fmt.Errorf("plain descriptor cleanup target %q has no exact no-unwind proof: %s", targetID, reason)
			}
		default:
			return fmt.Errorf("managed descriptor cleanup target %q has unsupported emission %s", targetID, targetPlan.Emission)
		}
	}
	return nil
}

// coroDeferRequiresDynamicCleanup identifies the source occurrence that makes
// the whole owner use the heterogeneous cleanup chain.
func coroDeferRequiresDynamicCleanup(instruction *ssa.Defer) bool {
	if instruction == nil || instruction.Parent() == nil || instruction.Block() == nil {
		return false
	}
	infos := blocks.Infos(instruction.Parent().Blocks)
	index := instruction.Block().Index
	return index >= 0 && index < len(infos) && infos[index].InLoop
}

// coroFunctionRequiresDynamicCleanup computes the owner-wide lowering choice
// once for ProgramIR construction. If one defer can execute repeatedly, every
// defer in the owner uses the same chain so registration order remains exact;
// consequently every source defer receives its own AllocU/FreeDeferNode
// physical placement in SitePlan.
func coroFunctionRequiresDynamicCleanup(fn *ssa.Function) bool {
	if fn == nil || len(fn.Blocks) == 0 {
		return false
	}
	infos := blocks.Infos(fn.Blocks)
	for _, block := range fn.Blocks {
		if block == nil || block.Index < 0 || block.Index >= len(infos) {
			continue
		}
		for _, instruction := range block.Instrs {
			deferred, ok := instruction.(*ssa.Defer)
			if ok && (deferred.DeferStack != nil || infos[block.Index].InLoop) {
				return true
			}
		}
	}
	return false
}

func validateCoroDynamicCleanupHelpers(plan *coroStaticCleanupPlan, whole *coro.SSAPlan) error {
	if plan == nil || !plan.dynamic {
		return nil
	}
	if whole == nil || plan.dynamicTrigger == nil || plan.dynamicAlloc == nil || plan.dynamicFree == nil {
		return fmt.Errorf("dynamic cleanup helper proof is incomplete")
	}
	for _, helper := range []struct {
		name   string
		target *ssa.Function
	}{
		{name: "AllocU", target: plan.dynamicAlloc},
		{name: "FreeDeferNode", target: plan.dynamicFree},
	} {
		call, frozen := whole.ResolveLoweredCallRecord(plan.dynamicTrigger.Parent(), helper.name)
		if !frozen || call.Target != helper.target || call.NoUnwind || call.RawPlain || call.UnwindOnly || call.ExplicitStatusElided {
			return fmt.Errorf("dynamic cleanup %s edge is not one exact ordinary lowered call", helper.name)
		}
		targetPlan, frozen := whole.FunctionPlan(helper.target)
		if !frozen || targetPlan.External != coro.Defined || targetPlan.Emission != coro.EmitPlain ||
			targetPlan.Primary != coro.PrimaryPlain || targetPlan.FuncRep != coro.DirectPlain ||
			targetPlan.Demand == coro.NoDemand || targetPlan.Effect != coro.NoSuspend ||
			targetPlan.Exec&(coro.MayUnwind|coro.BlockForeign|coro.NeedsPreempt|coro.OpaqueExec) != 0 {
			return fmt.Errorf(
				"dynamic cleanup %s target is not a demanded non-suspending, non-unwinding direct plain body (emission=%s primary=%s representation=%s demand=%s effect=%s exec=%s)",
				helper.name, targetPlan.Emission, targetPlan.Primary, targetPlan.FuncRep,
				targetPlan.Demand, targetPlan.Effect, targetPlan.Exec,
			)
		}
	}
	allocSignature := plan.dynamicAlloc.Signature
	if allocSignature == nil || allocSignature.Recv() != nil || allocSignature.Variadic() ||
		allocSignature.Params().Len() != 1 || allocSignature.Results().Len() != 1 ||
		!coroFrameRetentionUintptrLike(allocSignature.Params().At(0).Type()) ||
		!coroFrameRetentionUnsafePointer(allocSignature.Results().At(0).Type()) {
		return fmt.Errorf("dynamic cleanup AllocU target has an invalid func(uintptr) unsafe.Pointer ABI")
	}
	freeSignature := plan.dynamicFree.Signature
	if freeSignature == nil || freeSignature.Recv() != nil || freeSignature.Variadic() ||
		freeSignature.Params().Len() != 1 || freeSignature.Results().Len() != 0 ||
		!coroFrameRetentionUnsafePointer(freeSignature.Params().At(0).Type()) {
		return fmt.Errorf("dynamic cleanup FreeDeferNode target has an invalid func(unsafe.Pointer) ABI")
	}
	return nil
}

func coroStaticRunDefersReturns(block *ssa.BasicBlock, instructionIndex int) bool {
	_, ok := coroStaticRunDefersReconstructionAllocations(block, instructionIndex)
	return ok
}

// coroStaticRunDefersReconstructionAllocations recognizes the exact x/tools
// terminal shape used for named results: after instructionIndex, zero or more
// direct loads from owner-local result cells, then Return. instructionIndex
// may be -1 only for the caller-certified canonical Recover block, whose whole
// body is the reconstruction suffix. It returns the cells rather than merely
// a boolean so cleanup planning, frame proof, and code generation share one
// structural fact.
func coroStaticRunDefersReconstructionAllocations(
	block *ssa.BasicBlock,
	instructionIndex int,
) ([]*ssa.Alloc, bool) {
	if block == nil || instructionIndex < -1 || instructionIndex >= len(block.Instrs) {
		return nil, false
	}
	if len(block.Succs) != 0 {
		return nil, false
	}
	suffix := block.Instrs[instructionIndex+1:]
	loads := make(map[*ssa.UnOp]*ssa.Alloc)
	seenAllocations := make(map[*ssa.Alloc]struct{})
	allocations := make([]*ssa.Alloc, 0)
	for index, instruction := range suffix {
		if _, debug := instruction.(*ssa.DebugRef); debug {
			continue
		}
		switch instruction := instruction.(type) {
		case *ssa.UnOp:
			// A function with named results materializes those results in entry
			// allocas so deferred calls can observe or replace them. x/tools emits
			// the final loads after RunDefers and before Return. Accept only an
			// exact load from one owner-local allocation; every other operation
			// remains outside this terminal reconstruction tail.
			alloc, ok := instruction.X.(*ssa.Alloc)
			if !ok || instruction.Op != token.MUL || alloc.Parent() != block.Parent() {
				return nil, false
			}
			loads[instruction] = alloc
			if _, seen := seenAllocations[alloc]; !seen {
				seenAllocations[alloc] = struct{}{}
				allocations = append(allocations, alloc)
			}
		case *ssa.Return:
			for _, remaining := range suffix[index+1:] {
				if _, debug := remaining.(*ssa.DebugRef); !debug {
					return nil, false
				}
			}
			// Every accepted reconstruction load must flow directly to this
			// terminal Return. This keeps the exception narrower than the general
			// pure-SSA validator and prevents a future SSA shape from smuggling a
			// computation into the post-cleanup continuation.
			for load := range loads {
				referrers := load.Referrers()
				if referrers == nil || len(*referrers) == 0 {
					return nil, false
				}
				for _, referrer := range *referrers {
					if referrer == instruction {
						continue
					}
					if _, debug := referrer.(*ssa.DebugRef); !debug {
						return nil, false
					}
				}
			}
			return allocations, true
		default:
			return nil, false
		}
	}
	return nil, false
}

// coroStaticTerminalReconstructionAllocations returns the deterministic union
// of ordinary heap cells whose values are reconstructed after RunDefers or by
// x/tools' canonical exceptional Recover return. Only source-entry cells are
// eligible: moving a conditional or loop allocation to the coroutine prologue
// would change its execution count. Stack/frame cells need no special
// treatment because coroFrameAlloc already defines them in the physical ramp.
func coroStaticTerminalReconstructionAllocations(fn *ssa.Function) ([]*ssa.Alloc, error) {
	if fn == nil {
		return nil, nil
	}
	selected := make(map[*ssa.Alloc]struct{})
	keep := func(allocation *ssa.Alloc, context string) error {
		if !allocation.Heap {
			return nil
		}
		if allocation.Block() == nil || allocation.Block().Index != 0 {
			return fmt.Errorf("%s terminal heap allocation %q is outside source block zero", context, allocation.Name())
		}
		selected[allocation] = struct{}{}
		return nil
	}
	for _, block := range fn.Blocks {
		for instructionIndex, instruction := range block.Instrs {
			if _, ok := instruction.(*ssa.RunDefers); !ok {
				continue
			}
			allocations, ok := coroStaticRunDefersReconstructionAllocations(block, instructionIndex)
			if !ok {
				return nil, fmt.Errorf("RunDefers in block %d is not followed only by named-result reloads and the terminal Return", block.Index)
			}
			for _, allocation := range allocations {
				if err := keep(allocation, "RunDefers"); err != nil {
					return nil, err
				}
			}
		}
	}
	if fn.Recover != nil {
		allocations, ok := coroStaticRunDefersReconstructionAllocations(fn.Recover, -1)
		if !ok {
			return nil, fmt.Errorf("canonical Recover block %d is not composed only of named-result reloads and the terminal Return", fn.Recover.Index)
		}
		for _, allocation := range allocations {
			if err := keep(allocation, "Recover"); err != nil {
				return nil, err
			}
		}
	}
	ordered := make([]*ssa.Alloc, 0, len(selected))
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			allocation, ok := instruction.(*ssa.Alloc)
			if !ok {
				continue
			}
			if _, keep := selected[allocation]; keep {
				ordered = append(ordered, allocation)
			}
		}
	}
	return ordered, nil
}

// x/tools creates one implicit exceptional Return block for every function
// containing a syntactic defer, even when the source never calls recover.  The
// legacy setjmp lowering used that block; the explicit-status cleanup drainer
// does not.  Accept only the canonical predecessor-free, return-only shape so
// a real recover path cannot become silently unreachable.
func validateCoroStaticCleanupRecoverBlock(fn *ssa.Function) error {
	if fn == nil || fn.Recover == nil {
		return nil
	}
	block := fn.Recover
	if len(block.Preds) != 0 || len(block.Succs) != 0 {
		return fmt.Errorf("implicit recover block has predecessors=%d successors=%d", len(block.Preds), len(block.Succs))
	}
	returns := 0
	for _, instruction := range block.Instrs {
		switch instruction.(type) {
		case *ssa.DebugRef, *ssa.UnOp, *ssa.Return:
			if _, ok := instruction.(*ssa.Return); ok {
				returns++
			}
		default:
			return fmt.Errorf("implicit recover block contains %T", instruction)
		}
	}
	if returns != 1 {
		return fmt.Errorf("implicit recover block has %d returns", returns)
	}
	return nil
}

// validateCoroDeferredBuiltinCleanup freezes the builtin operation recipe
// independently from its defer carrier. Arguments are captured in source
// order by the common cleanup record; the named operation executes only when
// the drainer pops that record. Builtins with result-only expression semantics
// never reach an ssa.Defer and remain rejected here.
func validateCoroDeferredBuiltinCleanup(instruction *ssa.Defer, dynamicCleanup bool) (string, error) {
	if instruction == nil || instruction.Common() == nil || instruction.DeferStack != nil {
		return "", fmt.Errorf("builtin cleanup requires one owner-local defer")
	}
	common := instruction.Common()
	builtin, ok := common.Value.(*ssa.Builtin)
	if !ok || common.IsInvoke() || common.StaticCallee() != nil || common.Method != nil {
		return "", fmt.Errorf("builtin cleanup requires one exact builtin operand")
	}
	name := builtin.Name()
	arity := len(common.Args)
	requireArity := func(want int) error {
		if arity != want {
			return fmt.Errorf("deferred builtin %q has %d arguments, want %d", name, arity, want)
		}
		return nil
	}
	switch name {
	case "delete":
		if err := requireArity(2); err != nil {
			return "", err
		}
		mapping, ok := types.Unalias(common.Args[0].Type()).Underlying().(*types.Map)
		if !ok || !types.Identical(common.Args[1].Type(), mapping.Key()) {
			return "", fmt.Errorf("deferred delete has no exact map/key operand shape")
		}
		if dynamicCleanup {
			return "", fmt.Errorf("deferred delete in a dynamic cleanup owner requires distinct record/key allocation roles")
		}
	case "copy":
		if err := requireArity(2); err != nil {
			return "", err
		}
		if _, ok := types.Unalias(common.Args[0].Type()).Underlying().(*types.Slice); !ok {
			return "", fmt.Errorf("deferred copy destination is not a slice")
		}
	case "clear":
		if err := requireArity(1); err != nil {
			return "", err
		}
		switch types.Unalias(common.Args[0].Type()).Underlying().(type) {
		case *types.Map, *types.Slice:
		default:
			return "", fmt.Errorf("deferred clear operand is neither a map nor a slice")
		}
	case "close":
		if err := requireArity(1); err != nil {
			return "", err
		}
		if _, ok := types.Unalias(common.Args[0].Type()).Underlying().(*types.Chan); !ok {
			return "", fmt.Errorf("deferred close operand is not a channel")
		}
	case "panic":
		if err := requireArity(1); err != nil {
			return "", err
		}
		iface, ok := types.Unalias(common.Args[0].Type()).Underlying().(*types.Interface)
		if !ok || !iface.Empty() {
			return "", fmt.Errorf("deferred panic payload is not one empty interface")
		}
	case "recover":
		if err := requireArity(0); err != nil {
			return "", err
		}
	case "print", "println":
		// The frontend already froze each variadic operand into common.Args.
	default:
		return "", fmt.Errorf("deferred builtin %q has no coroutine cleanup recipe", name)
	}
	for index, argument := range common.Args {
		if argument == nil {
			return "", fmt.Errorf("deferred builtin %q argument %d is nil", name, index)
		}
		if err := validateCoroPhysicalValueType(argument.Type(), make(map[types.Type]bool)); err != nil {
			return "", fmt.Errorf("deferred builtin %q argument %d has unsupported type: %w", name, index, err)
		}
	}
	return name, nil
}

// coroDeferredIntrinsicCleanupRecipe selects the narrow relocated intrinsic
// cohort frozen by ProgramIR. The source defer site captures its already
// evaluated arguments; the cleanup drainer later emits this exact recipe
// without constructing or recovering a function pointer.
func (ir *coroProgramIR) deferredIntrinsicCleanupRecipe(
	instruction *ssa.Defer,
) (opcode int, recognized bool, err error) {
	if ir == nil || instruction == nil {
		return 0, false, nil
	}
	frozen, found, err := ir.callSitePlan(instruction)
	if err != nil || !found {
		return 0, false, err
	}
	if frozen.failure != "" {
		return 0, false, fmt.Errorf("invalid frozen intrinsic: %s", frozen.failure)
	}
	if !frozen.plan.Intrinsic {
		return 0, false, nil
	}
	if frozen.plan.Elision != CoroCallElidedIntrinsic ||
		frozen.plan.IntrinsicSemantics != CoroIntrinsicCallInlineNoSuspend ||
		frozen.intrinsicPlacement != coroRuntimeHelperAtCleanup ||
		!isCoroAtomicIntrinsic(frozen.opcode) {
		return 0, false, fmt.Errorf("deferred intrinsic has no executor-safe cleanup recipe")
	}
	return frozen.opcode, true, nil
}

func resolveCoroStaticCleanupTarget(
	whole *coro.SSAPlan,
	caller coro.FunctionPlan,
	instruction *ssa.Defer,
	universes ...*EmissionUniverse,
) (*ssa.Function, coro.FunctionPlan, coroStaticCleanupTargetKind, error) {
	if whole == nil || instruction == nil || instruction.Common() == nil {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("requires an exact compilation CallPlan")
	}
	common := instruction.Common()
	callPlan, ok := whole.CallPlan(instruction)
	if !ok {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("defer has no compilation CallPlan")
	}
	var raw *ssa.Function
	var closure *ssa.MakeClosure
	switch value := common.Value.(type) {
	case *ssa.Function:
		raw = value
	case *ssa.MakeClosure:
		closure = value
		var exact bool
		raw, exact = closure.Fn.(*ssa.Function)
		if !exact || raw == nil || len(raw.FreeVars) == 0 || len(closure.Bindings) != len(raw.FreeVars) {
			return nil, coro.FunctionPlan{}, 0, fmt.Errorf("captured coroutine defer requires its exact MakeClosure environment")
		}
	default:
		if err := validateCoroManagedDispatchDefer(whole, instruction.Parent(), instruction, callPlan, universes...); err != nil {
			return nil, coro.FunctionPlan{}, 0, fmt.Errorf("dynamic function defer: %w", err)
		}
		return nil, coro.FunctionPlan{}, coroStaticCleanupDispatch, nil
	}
	if raw == nil || common.IsInvoke() || common.StaticCallee() != raw {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("requires an exact static function or captured MakeClosure, not a dynamically selected function, method value, or invoke")
	}
	if callPlan.Kind != coro.CallDefer || callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf(
			"requires one closed non-nil defer target, got kind=%v representation=%s open=%t may-be-nil=%t targets=%d",
			callPlan.Kind, callPlan.Rep, callPlan.Open, callPlan.MayBeNil, len(callPlan.Targets),
		)
	}
	target, ok := whole.Function(callPlan.Targets[0])
	if !ok || target == nil {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("defer target %q has no exact canonical static function", callPlan.Targets[0])
	}
	canonicalRaw := raw
	if len(universes) != 0 && universes[0] != nil {
		resolved, frozen := universes[0].Resolve(raw)
		if !frozen || resolved == nil {
			return nil, coro.FunctionPlan{}, 0, fmt.Errorf("defer source target %q is absent from the frozen emission universe", raw.Name())
		}
		canonicalRaw = resolved
	}
	if target != canonicalRaw {
		redirected := false
		if len(universes) != 0 && universes[0] != nil {
			site, frozen, err := universes[0].CoroCallSitePlan(instruction)
			if err != nil {
				return nil, coro.FunctionPlan{}, 0, fmt.Errorf("read deferred managed-call redirect: %w", err)
			}
			redirected = frozen &&
				site.ManagedStaticTarget == target &&
				site.ManagedStaticTargetCertificate != ""
		}
		if !redirected {
			return nil, coro.FunctionPlan{}, 0, fmt.Errorf("defer target %q is not its exact canonical static function or frozen managed redirect", callPlan.Targets[0])
		}
	}
	targetPlan, ok := whole.FunctionPlan(target)
	if !ok || targetPlan.ID != callPlan.Targets[0] {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("defer target %q has no canonical function plan", callPlan.Targets[0])
	}
	if target.Signature == nil {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("signature-less defer target is unsupported")
	}
	// A variadic source call has no special physical defer ABI. x/tools SSA
	// has already evaluated and packed the trailing arguments into the final
	// []T operand before the Defer instruction. The frame record retains that
	// ordinary slice value, and coroPhysicalNormalizeSourceSignature clears
	// the source-only variadic marker for the eventual plain/coroutine call.
	// validateCoroStaticCleanupOperands below freezes the exact packed shape.
	if closure != nil {
		if err := validateCoroCapturedClosureProducer(whole, closure, targetPlan); err != nil {
			return nil, coro.FunctionPlan{}, 0, fmt.Errorf("captured coroutine defer: %w", err)
		}
		if callPlan.Rep != coro.DirectCoro {
			return nil, coro.FunctionPlan{}, 0, fmt.Errorf("captured MakeClosure defer requires direct coroutine representation, got %s", callPlan.Rep)
		}
	}
	if target.Signature.Recv() != nil {
		var err error
		if len(universes) != 0 {
			err = validateCoroStaticMethodCallOperands(instruction, target, universes[0])
		} else {
			err = validateCoroStaticMethodCallOperands(instruction, target, nil)
		}
		if err != nil {
			return nil, coro.FunctionPlan{}, 0, err
		}
	} else if err := validateCoroStaticCleanupOperands(common, target); err != nil {
		return nil, coro.FunctionPlan{}, 0, err
	}
	switch callPlan.Rep {
	case coro.DirectPlain:
		if targetPlan.External != coro.Defined || targetPlan.Emission != coro.EmitPlain ||
			targetPlan.Primary != coro.PrimaryPlain || targetPlan.FuncRep != coro.DirectPlain ||
			targetPlan.Demand == coro.NoDemand || targetPlan.Effect != coro.NoSuspend {
			return nil, coro.FunctionPlan{}, 0, fmt.Errorf(
				"plain defer target %q is not one demanded defined bounded plain entry (external=%s emission=%s primary=%s representation=%s effect=%s demand=%s)",
				targetPlan.ID, targetPlan.External, targetPlan.Emission, targetPlan.Primary,
				targetPlan.FuncRep, targetPlan.Effect, targetPlan.Demand,
			)
		}
		return target, targetPlan, coroStaticCleanupPlain, nil
	case coro.DirectCoro:
		if err := validateCoroAwaitTarget(caller, targetPlan); err != nil {
			return nil, coro.FunctionPlan{}, 0, fmt.Errorf("coroutine defer target: %w", err)
		}
		return target, targetPlan, coroStaticCleanupCoroutine, nil
	default:
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("defer target uses unsupported representation %s", callPlan.Rep)
	}
}

// validateCoroCapturedClosureProducer separates the representation of one
// closure value from the representation selected at an exact call site. A
// MakeClosure that also escapes through storage must publish the canonical
// Dispatch descriptor, while a defer whose operand is that exact producer
// still has a closed DirectCoro CallPlan. Both physical closure carriers are
// two words and retain the same environment in field one; the static
// await/cleanup lowerer consumes only that environment and the frozen target,
// never the producer's code word.
func validateCoroCapturedClosureProducer(
	whole *coro.SSAPlan,
	closure *ssa.MakeClosure,
	targetPlan coro.FunctionPlan,
) error {
	if whole == nil || closure == nil {
		return fmt.Errorf("requires an exact MakeClosure producer")
	}
	target, exactTarget := closure.Fn.(*ssa.Function)
	if !exactTarget || target == nil || len(target.FreeVars) == 0 ||
		len(closure.Bindings) != len(target.FreeVars) {
		return fmt.Errorf("requires its exact captured target and environment")
	}
	plannedTarget, planned := whole.FunctionPlan(target)
	if !planned || plannedTarget.ID != targetPlan.ID {
		return fmt.Errorf("producer target has no matching canonical function plan")
	}
	for index, binding := range closure.Bindings {
		if binding == nil || target.FreeVars[index] == nil ||
			!types.Identical(binding.Type(), target.FreeVars[index].Type()) {
			return fmt.Errorf("environment binding %d does not match its target free variable", index)
		}
	}
	valuePlan, exactValue := whole.ValuePlan(closure)
	if !exactValue || len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 {
		return fmt.Errorf("has no exact scalar callable value plan")
	}
	leaf := valuePlan.Funcs[0]
	if leaf.Transport != coro.ManagedTransport {
		return fmt.Errorf("uses non-managed transport %s", leaf.Transport)
	}
	if leaf.Rep != coro.DirectCoro && leaf.Rep != coro.Dispatch {
		return fmt.Errorf("uses unsupported value representation %s", leaf.Rep)
	}
	if leaf.Rep == coro.Dispatch && targetPlan.FuncRep != coro.Dispatch {
		return fmt.Errorf("descriptor producer target uses non-dispatch representation %s", targetPlan.FuncRep)
	}
	targetPresent := false
	for _, candidate := range leaf.Targets {
		if candidate == targetPlan.ID {
			targetPresent = true
			break
		}
	}
	if !targetPresent {
		return fmt.Errorf("exact target %q is absent from its callable value plan", targetPlan.ID)
	}
	return nil
}

func validateCoroStaticCleanupOperands(common *ssa.CallCommon, target *ssa.Function) error {
	if common == nil || target == nil || target.Signature == nil || target.Signature.Recv() != nil {
		return fmt.Errorf("static cleanup operands require one receiver-free function")
	}
	signature := coroPhysicalNormalizeSourceSignature(target.Signature)
	if signature.Params().Len() != len(target.Params) || len(common.Args) != len(target.Params) {
		return fmt.Errorf(
			"static cleanup argument shape mismatch: signature=%d SSA-params=%d call-args=%d",
			signature.Params().Len(), len(target.Params), len(common.Args),
		)
	}
	for index, parameter := range target.Params {
		if parameter == nil || common.Args[index] == nil ||
			!types.Identical(parameter.Type(), signature.Params().At(index).Type()) ||
			!types.Identical(common.Args[index].Type(), parameter.Type()) {
			return fmt.Errorf("static cleanup operand %d does not match the target parameter ABI", index)
		}
		if err := validateCoroPhysicalValueType(parameter.Type(), make(map[types.Type]bool)); err != nil {
			return fmt.Errorf("static cleanup operand %d has unsupported type: %w", index, err)
		}
	}
	return nil
}

// validateCoroStaticCleanupNoUnwind overrides the planner's deliberately
// conservative MayUnwind bit only with an exact lowering audit.  It accepts
// pure SSA plus compiler-elided no-call/structured-park intrinsics.  Managed
// callees, implicit panic helpers, nested defer, recover, and preemption remain
// closed until child-frame panic outcomes can be propagated to the drainer.
func validateCoroStaticCleanupNoUnwind(
	whole *coro.SSAPlan,
	universe *EmissionUniverse,
	target *ssa.Function,
	plan coro.FunctionPlan,
	frameRetentionABI string,
) string {
	if target == nil || len(target.Blocks) == 0 {
		return "target has no defined SSA body"
	}
	if target.Recover != nil {
		return "recover block requires panic-aware cleanup unwinding"
	}
	if plan.Exec&(coro.NeedsCleanupFrame|coro.NeedsPreempt|coro.OpaqueExec) != 0 {
		return "target requires nested cleanup, preemption, or opaque execution"
	}
	for _, info := range blocks.Infos(target.Blocks) {
		if info.InLoop {
			return "cyclic cleanup target requires preemption and cancellation masking"
		}
	}
	audit, err := newCoroPhysicalPureSSAAudit(universe, whole, target, frameRetentionABI)
	if err != nil {
		return "cannot build pure-SSA audit: " + err.Error()
	}
	for _, block := range target.Blocks {
		for _, instruction := range block.Instrs {
			if handled, reason := audit.validate(instruction); handled {
				if reason != "" {
					return fmt.Sprintf("block %d instruction %T: %s", block.Index, instruction, reason)
				}
				continue
			}
			switch instruction := instruction.(type) {
			case *ssa.DebugRef, *ssa.Jump, *ssa.Return:
			case *ssa.If:
				if !coroLeafScalar(instruction.Cond.Type()) {
					return fmt.Sprintf("block %d has a non-scalar condition", block.Index)
				}
			case *ssa.Call:
				if whole == nil || !whole.ElidesCall(instruction) || universe == nil {
					return fmt.Sprintf("block %d has an ordinary managed call", block.Index)
				}
				semantics, intrinsic, err := coroIntrinsicCallSiteSemantics(universe, instruction)
				if err != nil {
					return fmt.Sprintf("block %d intrinsic: %v", block.Index, err)
				}
				if !intrinsic || (semantics != CoroIntrinsicCallInlineNoSuspend && semantics != CoroIntrinsicCallInlineSuspend &&
					semantics != CoroIntrinsicCallInlineYield) {
					return fmt.Sprintf("block %d intrinsic has unproved semantics %d", block.Index, uint8(semantics))
				}
			default:
				return fmt.Sprintf("block %d instruction %T has no no-unwind lowering proof", block.Index, instruction)
			}
		}
	}
	return ""
}

const (
	coroStaticCleanupContinueComplete uint32 = 1
	coroStaticCleanupContinueRecover  uint32 = 2
	coroStaticCleanupContinueFirstRun uint32 = 3
)

type coroStaticCleanupSiteState struct {
	plan            *coroStaticCleanupSitePlan
	active          llssa.Expr
	descriptor      llssa.Expr
	descriptorType  llssa.Type
	closureContext  llssa.Expr
	args            []llssa.Expr
	keepalives      []llssa.Expr
	nodeType        llssa.Type
	descriptorField int
	closureField    int
	argsField       int
	keepaliveField  int
}

type coroStaticCleanupContinuation struct {
	id    uint32
	block llssa.BasicBlock
}

type coroStaticCleanupState struct {
	sites         []*coroStaticCleanupSiteState
	byDefer       map[*ssa.Defer]*coroStaticCleanupSiteState
	dynamic       bool
	external      bool
	dynamicHead   llssa.Expr
	dynamicHeader llssa.Type
	dynamicAlloc  *ssa.Function
	dynamicFree   *ssa.Function
	continuation  llssa.Expr
	panicActive   llssa.Expr
	panicType     llssa.Expr
	panicData     llssa.Expr
	panicLine     llssa.Expr
	entry         llssa.BasicBlock
	complete      llssa.BasicBlock
	panic         llssa.BasicBlock
	run           []coroStaticCleanupContinuation
}

// beginCoroStaticCleanup allocates and initializes every owner value before the
// initial suspend. A cancellation decision on the first resume can therefore
// safely enter the same empty drainer as every later terminal path. An external
// range-over-func yield plan is registration-only: it retains the frozen node
// layouts but adds no unused drainer or capture slots to the yield frame.
func (p *context) beginCoroStaticCleanup(b llssa.Builder, plan *coroStaticCleanupPlan) *coroStaticCleanupState {
	if plan == nil || len(plan.sites) == 0 {
		return nil
	}
	state := &coroStaticCleanupState{
		sites:        make([]*coroStaticCleanupSiteState, 0, len(plan.sites)),
		byDefer:      make(map[*ssa.Defer]*coroStaticCleanupSiteState, len(plan.sites)),
		dynamic:      plan.dynamic,
		external:     plan.external,
		dynamicAlloc: plan.dynamicAlloc,
		dynamicFree:  plan.dynamicFree,
	}
	if !state.external {
		state.continuation = b.AllocaT(p.prog.Uint32())
		b.Store(state.continuation, p.prog.IntVal(0, p.prog.Uint32()))
		state.panicActive = b.AllocaT(p.prog.Bool())
		b.Store(state.panicActive, p.prog.BoolVal(false))
		state.panicType = b.AllocaT(p.prog.VoidPtr())
		state.panicData = b.AllocaT(p.prog.VoidPtr())
		state.panicLine = b.AllocaT(p.prog.Uint32())
		b.Store(state.panicType, p.prog.Nil(p.prog.VoidPtr()))
		b.Store(state.panicData, p.prog.Nil(p.prog.VoidPtr()))
		b.Store(state.panicLine, p.prog.IntVal(0, p.prog.Uint32()))
	}
	if state.dynamic {
		if state.dynamicAlloc == nil || state.dynamicFree == nil {
			panic("dynamic coroutine cleanup lacks frozen allocator/release targets")
		}
		if !state.external {
			state.dynamicHead = b.AllocaT(p.prog.VoidPtr())
			b.Store(state.dynamicHead, p.prog.Nil(p.prog.VoidPtr()))
			state.dynamicHeader = p.prog.Struct(p.prog.VoidPtr(), p.prog.Uint32())
		}
	}
	for _, sitePlan := range plan.sites {
		site := &coroStaticCleanupSiteState{
			plan:            sitePlan,
			descriptorField: -1,
			closureField:    -1,
			keepaliveField:  -1,
		}
		if !state.dynamic {
			site.active = b.AllocaT(p.prog.Bool())
			b.Store(site.active, p.prog.BoolVal(false))
		}
		var nodeFields []llssa.Type
		if state.dynamic {
			nodeFields = append(nodeFields, p.prog.VoidPtr(), p.prog.Uint32())
		}
		if sitePlan.kind == coroStaticCleanupDispatch {
			if sitePlan.signature == nil ||
				(sitePlan.descriptor == nil && (sitePlan.interfaceReceiver == nil || sitePlan.interfaceMethod == nil)) {
				panic("managed descriptor cleanup site has no frozen descriptor/signature")
			}
			descriptorType := p.type_(sitePlan.signature, llssa.InGo)
			closure, ok := types.Unalias(descriptorType.RawType()).Underlying().(*types.Struct)
			if !ok || !llssa.IsClosure(closure) {
				panic(fmt.Sprintf("managed descriptor cleanup site lowered signature %s as %s, want canonical closure", sitePlan.signature, descriptorType.RawType()))
			}
			site.descriptorType = descriptorType
			if !state.external {
				site.descriptor = b.AllocaT(descriptorType)
				b.Store(site.descriptor, p.prog.Zero(descriptorType))
			}
			if state.dynamic {
				site.descriptorField = len(nodeFields)
				nodeFields = append(nodeFields, descriptorType)
			}
		}
		if sitePlan.closure != nil {
			if sitePlan.kind != coroStaticCleanupCoroutine || p.emissionUniverse == nil {
				panic("captured static cleanup requires a prepared coroutine target")
			}
			signature, err := p.emissionUniverse.coroPhysicalEntrySourceSignature(sitePlan.target)
			if err != nil || signature == nil || signature.Params().Len() == 0 {
				panic(fmt.Sprintf("captured static cleanup target %q has no exact context ABI: %v", sitePlan.targetPlan.ID, err))
			}
			contextType := p.prog.Type(signature.Params().At(0).Type(), llssa.InGo)
			if !state.external {
				site.closureContext = b.AllocaT(contextType)
				b.Store(site.closureContext, p.prog.Nil(contextType))
			}
			if state.dynamic {
				site.closureField = len(nodeFields)
				nodeFields = append(nodeFields, contextType)
			}
		}
		site.argsField = len(nodeFields)
		for _, argument := range sitePlan.instruction.Call.Args {
			argumentType := p.type_(argument.Type(), llssa.InGo)
			slot := llssa.Nil
			if !state.external {
				slot = b.AllocaT(argumentType)
			}
			site.args = append(site.args, slot)
			if state.dynamic {
				nodeFields = append(nodeFields, argumentType)
			}
		}
		keepaliveSources := p.coroCallKeepaliveSources(sitePlan.instruction)
		if len(keepaliveSources) != 0 {
			site.keepaliveField = len(nodeFields)
		}
		for _, source := range keepaliveSources {
			keepaliveType := p.coroCallKeepaliveStorageType(source)
			slot := llssa.Nil
			if !state.external {
				slot = b.AllocaT(keepaliveType)
				b.Store(slot, p.prog.Zero(keepaliveType))
			}
			site.keepalives = append(site.keepalives, slot)
			if state.dynamic {
				nodeFields = append(nodeFields, keepaliveType)
			}
		}
		if state.dynamic {
			site.nodeType = p.prog.Struct(nodeFields...)
		}
		state.sites = append(state.sites, site)
		state.byDefer[sitePlan.instruction] = site
	}
	return state
}

// bindBlocks runs only after BeginCoro has created its canonical ramp and
// initial-suspend blocks; cleanup implementation blocks must not perturb that
// presplit layout contract.
func (s *coroStaticCleanupState) bindBlocks(function llssa.Function) {
	if s == nil {
		return
	}
	s.entry = function.MakeBlock()
	s.complete = function.MakeBlock()
	s.panic = function.MakeBlock()
}

func (s *coroStaticCleanupState) register(p *context, b llssa.Builder, instruction *ssa.Defer) {
	if s == nil || s.external {
		panic("coroutine local cleanup registration has no owner-local state")
	}
	s.registerAt(p, b, instruction, s.dynamicHead)
}

func (s *coroStaticCleanupState) registerExternal(p *context, b llssa.Builder, instruction *ssa.Defer) {
	if s == nil || !s.external || !s.dynamic || instruction == nil || instruction.DeferStack == nil {
		panic("coroutine external cleanup registration has no explicit owner stack")
	}
	stack := p.compileValue(b, instruction.DeferStack)
	head := b.Convert(p.prog.Pointer(p.prog.VoidPtr()), stack)
	s.registerAt(p, b, instruction, head)
}

func (s *coroStaticCleanupState) registerAt(
	p *context,
	b llssa.Builder,
	instruction *ssa.Defer,
	dynamicHead llssa.Expr,
) {
	if s == nil || instruction == nil {
		panic("coroutine static cleanup registration has no state or instruction")
	}
	site := s.byDefer[instruction]
	if site == nil {
		panic("coroutine defer escaped its static cleanup plan")
	}
	// SSA values preserve Go's left-to-right evaluation. Save the already
	// evaluated exact closure environment, then every receiver/argument, before
	// making the record active. The context slot is a typed frame root and stays
	// live until the deferred child has completed.
	descriptor := llssa.Nil
	if site.plan.kind == coroStaticCleanupDispatch {
		if site.descriptorType == nil || (!s.external && site.descriptor.IsNil()) {
			panic("managed descriptor cleanup registration has no typed descriptor slot")
		}
		if site.plan.interfaceReceiver != nil {
			if site.plan.interfaceMethod == nil {
				panic("managed interface cleanup registration has no frozen method")
			}
			intf := p.compileValue(b, site.plan.interfaceReceiver)
			p.compileCoroImplicitNilAccessGuard(b, b.InterfaceTypeWord(intf))
			descriptor = b.Imethod(intf, site.plan.interfaceMethod)
		} else {
			if site.plan.descriptor == nil {
				panic("managed function cleanup registration has no frozen descriptor")
			}
			descriptor = p.compileValue(b, site.plan.descriptor)
		}
		closure, ok := types.Unalias(descriptor.RawType()).Underlying().(*types.Struct)
		var expectedClosure *types.Struct
		if site.descriptorType != nil {
			expectedClosure, _ = types.Unalias(site.descriptorType.RawType()).Underlying().(*types.Struct)
		}
		if !ok || !llssa.IsClosure(closure) || expectedClosure == nil ||
			!llssa.IsClosure(expectedClosure) || !types.Identical(closure, expectedClosure) {
			want := "<missing>"
			if site.descriptorType != nil {
				want = site.descriptorType.RawType().String()
			}
			panic(fmt.Sprintf("managed descriptor cleanup registration lowered callee as %s, want %s", descriptor.RawType(), want))
		}
		if !types.Identical(descriptor.RawType(), site.descriptorType.RawType()) {
			// Named function types such as context.CancelFunc retain their Go
			// identity on the SSA value, while the frozen dispatch ABI stores
			// the canonical {code,environment} carrier for the call signature.
			// The exact closure fields above prove a representation-preserving
			// retag; Builder.ChangeType rebuilds the aggregate without a native
			// stack temporary.
			descriptor = b.ChangeType(site.descriptorType, descriptor)
		}
		if !s.dynamic {
			b.Store(site.descriptor, descriptor)
		}
	}
	closureContext := llssa.Nil
	if site.plan.closure != nil {
		if !s.external && site.closureContext.IsNil() {
			panic("captured coroutine defer has no closure-context slot")
		}
		closure := p.compileValue(b, site.plan.closure)
		closureContext = b.Field(closure, 1)
		if !s.dynamic {
			b.Store(site.closureContext, closureContext)
		}
	}
	functionKind := p.funcKind(instruction.Call.Value)
	if site.plan.kind == coroStaticCleanupDispatch {
		functionKind = fnNormal
	}
	args := p.compileValues(b, instruction.Call.Args, functionKind)
	if len(args) != len(site.args) {
		panic(fmt.Sprintf("coroutine defer arguments=%d do not match cleanup slots=%d", len(args), len(site.args)))
	}
	keepalives := p.compileCoroCallKeepaliveValues(b, instruction)
	if len(keepalives) != len(site.keepalives) {
		panic(fmt.Sprintf(
			"coroutine defer keepalives=%d do not match cleanup slots=%d",
			len(keepalives), len(site.keepalives),
		))
	}
	if site.plan.kind == coroStaticCleanupIntrinsic {
		p.observeCoroDeferredIntrinsicCapture()
	} else if site.plan.kind == coroStaticCleanupCgoWorker {
		// The source defer captures an already-certified raw adapter call but
		// does not execute it. Account for the frozen call elision here; the
		// relocated cleanup observer separately owns the later worker helpers.
		p.observeCoroDeferredCgoWorkerCapture()
	}
	if s.dynamic {
		s.pushDynamicTo(p, b, site, dynamicHead, descriptor, closureContext, args, keepalives)
		return
	}
	for index, argument := range args {
		b.Store(site.args[index], argument)
	}
	for index, keepalive := range keepalives {
		b.Store(site.keepalives[index], keepalive)
	}
	b.Store(site.active, b.Prog.BoolVal(true))
}

// pushDynamic publishes a fully initialized heterogeneous record with one
// release-store-equivalent compiler order: Go closure/arguments are evaluated
// first, the private node is filled next, and the frame-rooted head changes
// last. No scheduler suspension is permitted in the frozen AllocU edge.
func (s *coroStaticCleanupState) pushDynamicTo(
	p *context, b llssa.Builder, site *coroStaticCleanupSiteState,
	dynamicHead llssa.Expr,
	descriptor, closureContext llssa.Expr, args, keepalives []llssa.Expr,
) {
	if s == nil || p == nil || site == nil || site.nodeType == nil || dynamicHead.IsNil() ||
		s.dynamicAlloc == nil || site.plan == nil || site.plan.tag == 0 {
		panic("dynamic coroutine cleanup push has incomplete frozen state")
	}
	allocator, _, kind := p.compileFunction(s.dynamicAlloc)
	if allocator == nil || kind != goFunc {
		panic("dynamic coroutine cleanup AllocU target did not resolve to a Go entry")
	}
	p.observeCoroSiteRuntimeHelper("AllocU")
	raw := b.Call(allocator.Expr, llssa.SizeOf(p.prog, site.nodeType))
	node := b.Convert(p.prog.Pointer(site.nodeType), raw)
	b.Store(b.FieldAddr(node, 0), b.Load(dynamicHead))
	b.Store(b.FieldAddr(node, 1), p.prog.IntVal(uint64(site.plan.tag), p.prog.Uint32()))
	if site.descriptorField >= 0 {
		if descriptor.IsNil() {
			panic("dynamic managed cleanup push lost its descriptor")
		}
		b.Store(b.FieldAddr(node, site.descriptorField), descriptor)
	}
	if site.closureField >= 0 {
		if closureContext.IsNil() {
			panic("dynamic captured cleanup push lost its closure context")
		}
		b.Store(b.FieldAddr(node, site.closureField), closureContext)
	}
	for index, argument := range args {
		b.Store(b.FieldAddr(node, site.argsField+index), argument)
	}
	for index, keepalive := range keepalives {
		if site.keepaliveField < 0 {
			panic("dynamic coroutine cleanup lost its keepalive field")
		}
		b.Store(b.FieldAddr(node, site.keepaliveField+index), keepalive)
	}
	b.Store(dynamicHead, b.Convert(p.prog.VoidPtr(), node))
}

func (s *coroStaticCleanupState) enter(b llssa.Builder, continuation uint32) {
	if s == nil || s.entry == nil {
		panic("coroutine static cleanup entry is not bound")
	}
	b.Store(s.continuation, b.Prog.IntVal(uint64(continuation), b.Prog.Uint32()))
	b.Store(s.panicActive, b.Prog.BoolVal(false))
	b.Store(s.panicType, b.Prog.Nil(b.Prog.VoidPtr()))
	b.Store(s.panicData, b.Prog.Nil(b.Prog.VoidPtr()))
	b.Store(s.panicLine, b.Prog.IntVal(0, b.Prog.Uint32()))
	b.Jump(s.entry)
}

func (s *coroStaticCleanupState) enterCompletion(b llssa.Builder) {
	s.enter(b, coroStaticCleanupContinueComplete)
}

// enterCancellation replaces the cleanup base with terminal cancellation but
// deliberately preserves a live panic overlay. An older defer may still
// recover that panic; without recovery the panic wins, while recovery exposes
// the retained Abort/Shutdown base and resumes cancellation propagation.
func (s *coroStaticCleanupState) enterCancellation(b llssa.Builder) {
	s.setCancellationBase(b)
	s.resume(b)
}

// enterGoexit replaces every earlier cleanup base and abandons an active panic
// overlay. Goexit is not a panic: recover must return nil until a later defer
// raises a new panic. That later panic may still be recovered, after which this
// retained Goexit base resumes and terminates the logical goroutine.
func (s *coroStaticCleanupState) enterGoexit(b llssa.Builder) {
	if s == nil || s.entry == nil {
		panic("coroutine Goexit cleanup entry is not bound")
	}
	b.Store(s.continuation, b.Prog.IntVal(uint64(coroStaticCleanupContinueComplete), b.Prog.Uint32()))
	b.Store(s.panicActive, b.Prog.BoolVal(false))
	b.Store(s.panicType, b.Prog.Nil(b.Prog.VoidPtr()))
	b.Store(s.panicData, b.Prog.Nil(b.Prog.VoidPtr()))
	b.Store(s.panicLine, b.Prog.IntVal(0, b.Prog.Uint32()))
	b.Jump(s.entry)
}

// setCancellationBase changes only the continuation selected after the last
// cleanup record. It intentionally does not clear or jump: a canceled child
// resume must first consume and reconcile that child's already-published
// Return/Recovered/Panic outcome before re-entering the drainer.
func (s *coroStaticCleanupState) setCancellationBase(b llssa.Builder) {
	if s == nil || s.entry == nil {
		panic("coroutine cancellation cleanup entry is not bound")
	}
	b.Store(s.continuation, b.Prog.IntVal(uint64(coroStaticCleanupContinueComplete), b.Prog.Uint32()))

}

func (s *coroStaticCleanupState) resume(b llssa.Builder) {
	if s == nil || s.entry == nil {
		panic("coroutine cleanup resume entry is not bound")
	}
	b.Jump(s.entry)
}

func (s *coroStaticCleanupState) enterPanic(
	b llssa.Builder,
	typeWord, dataWord llssa.Expr,
	line uint32,
) {
	// A panic reached from source execution has no earlier cleanup base. A
	// successful recover returns through x/tools' canonical Recover block.
	b.Store(s.continuation, b.Prog.IntVal(uint64(coroStaticCleanupContinueRecover), b.Prog.Uint32()))
	s.replacePanic(b, typeWord, dataWord, line)
}

// replacePanic overlays either the first source panic or a child outcome whose
// runtime handoff has already reconciled trace replacement. Preserve the
// cleanup base (normal return, Recover, RunDefers, and future cancel/Goexit).
func (s *coroStaticCleanupState) replacePanic(
	b llssa.Builder,
	typeWord, dataWord llssa.Expr,
	line uint32,
) {
	s.setPanicOverlay(b, typeWord, dataWord, line)
	s.resume(b)
}

// replacePanicInline marks an exact cleanup-local language replacement before
// changing the overlay. Runtime cannot infer this case from payload identity:
// a deferred panic(x) is a new panic even when x has the same two interface
// words as the panic currently being propagated.
func (s *coroStaticCleanupState) replacePanicInline(
	p *context,
	b llssa.Builder,
	typeWord, dataWord llssa.Expr,
	line uint32,
) {
	if s == nil || p == nil || b == nil || b.Func != p.fn {
		panic("inline coroutine panic replacement requires an exact runtime hook")
	}
	p.emitCoroPanicTraceReplacement(b)
	s.replacePanic(b, typeWord, dataWord, line)
}

func (s *coroStaticCleanupState) setPanicOverlay(
	b llssa.Builder,
	typeWord, dataWord llssa.Expr,
	line uint32,
) {
	if s == nil {
		panic("coroutine cleanup panic overlay has no state")
	}
	b.Store(s.panicActive, b.Prog.BoolVal(true))
	b.Store(s.panicType, b.Convert(b.Prog.VoidPtr(), typeWord))
	b.Store(s.panicData, b.Convert(b.Prog.VoidPtr(), dataWord))
	b.Store(s.panicLine, b.Prog.IntVal(uint64(line), b.Prog.Uint32()))
}

// recoverAwaitArguments encodes the current panic overlay into the unified V3
// child handoff. Selects avoid a second runtime hook and keep normal cleanup on
// the same CompletionRecord transaction with nil recovery words.
func (s *coroStaticCleanupState) recoverAwaitArguments(
	p *context, b llssa.Builder,
) (mode, typeWord, dataWord llssa.Expr) {
	if s == nil || p == nil || p.coroBody() == nil {
		panic("coroutine cleanup recovery arguments require an active drainer")
	}
	active := b.Load(s.panicActive)
	mode = b.SelectValue(
		active,
		b.Prog.IntVal(coroAwaitRecoverDirect, b.Prog.Uint32()),
		b.Prog.IntVal(coroAwaitRecoverNone, b.Prog.Uint32()),
	)
	typeWord = b.SelectValue(active, b.Load(s.panicType), b.Prog.Nil(b.Prog.VoidPtr()))
	dataWord = b.SelectValue(active, b.Load(s.panicData), b.Prog.Nil(b.Prog.VoidPtr()))
	return
}

func (s *coroStaticCleanupState) reconcileDeferredChildReturn(
	p *context, b llssa.Builder, status uint64,
) {
	if s == nil || p == nil || status != coroAwaitCompletionReturnRecovered {
		panic("coroutine cleanup child return has an invalid completion status")
	}
	valid := p.fn.MakeBlock()
	invalid := p.fn.MakeBlock()
	active := b.Load(s.panicActive)
	b.If(active, valid, invalid)
	b.SetBlockEx(valid, llssa.AtEnd, false)
	// Preserve the base continuation. This is what makes a panic raised during
	// normal RunDefers/cancellation cleanup resume its original control after an
	// older defer recovers it.
	b.Store(s.panicActive, b.Prog.BoolVal(false))
	b.Store(s.panicType, b.Prog.Nil(b.Prog.VoidPtr()))
	b.Store(s.panicData, b.Prog.Nil(b.Prog.VoidPtr()))
	b.Store(s.panicLine, b.Prog.IntVal(0, b.Prog.Uint32()))
	merged := p.fn.MakeBlock()
	b.Jump(merged)
	b.SetBlockEx(invalid, llssa.AtEnd, false)
	b.Unreachable()
	b.SetBlockEx(merged, llssa.AtEnd, false)
}

func (s *coroStaticCleanupState) runDefers(b llssa.Builder, _ *ssa.RunDefers) {
	if s == nil {
		panic("coroutine RunDefers has no static cleanup state")
	}
	if uint64(len(s.run)) > uint64(^uint32(0)-coroStaticCleanupContinueFirstRun) {
		panic("too many coroutine RunDefers continuations")
	}
	continuation := coroStaticCleanupContinuation{
		id:    coroStaticCleanupContinueFirstRun + uint32(len(s.run)),
		block: b.Func.MakeBlock(),
	}
	s.run = append(s.run, continuation)
	s.enter(b, continuation.id)
	b.SetBlockContinuation(continuation.block)
}

func (s *coroStaticCleanupState) emit(p *context, b llssa.Builder) {
	if s == nil || s.entry == nil || s.complete == nil || s.panic == nil {
		panic("coroutine static cleanup blocks are not bound")
	}
	if s.dynamic {
		s.emitDynamic(p, b)
		return
	}
	done := p.fn.MakeBlock()
	next := done
	// Construct from oldest to newest while wiring each skipped/executed site
	// to the already-built older suffix.  entry finally points at the newest.
	for index := 0; index < len(s.sites); index++ {
		site := s.sites[index]
		check := p.fn.MakeBlock()
		call := p.fn.MakeBlock()
		b.SetBlock(check)
		b.If(b.Load(site.active), call, next)
		b.SetBlock(call)
		// Clear before invoking.  A panic, cancellation resume, or erroneous
		// second RunDefers can never execute this exact record twice.
		b.Store(site.active, b.Prog.BoolVal(false))
		args := make([]llssa.Expr, len(site.args))
		for argument := range args {
			args[argument] = b.Load(site.args[argument])
		}
		if s.emitSiteCall(p, b, site, args, false) {
			b.Jump(next)
		}
		next = check
	}
	b.SetBlock(s.entry)
	b.Jump(next)

	b.SetBlock(done)
	s.emitCompletionDispatch(p, b)
}

// emitDynamic drains the one owner-local heterogeneous LIFO chain. Each node
// is copied into its site's typed frame slots before unlink/free, so a deferred
// coroutine may suspend without retaining an untyped or released allocation.
// Popping before invocation is the dynamic equivalent of clearing a static
// site's active bit: panic, Abort, and Shutdown can re-enter this same loop
// without executing a record twice.
func (s *coroStaticCleanupState) emitDynamic(p *context, b llssa.Builder) {
	if s.dynamicHead.IsNil() || s.dynamicHeader == nil || s.dynamicFree == nil {
		panic("dynamic coroutine cleanup drainer has incomplete frozen state")
	}
	done := p.fn.MakeBlock()
	nonempty := p.fn.MakeBlock()
	invalid := p.fn.MakeBlock()
	siteBlocks := make([]llssa.BasicBlock, len(s.sites))
	for index := range siteBlocks {
		siteBlocks[index] = p.fn.MakeBlock()
	}

	b.SetBlock(s.entry)
	record := b.Load(s.dynamicHead)
	b.If(b.BinOp(token.NEQ, record, p.prog.Nil(p.prog.VoidPtr())), nonempty, done)

	b.SetBlock(nonempty)
	header := b.Convert(p.prog.Pointer(s.dynamicHeader), record)
	next := b.Load(b.FieldAddr(header, 0))
	tag := b.Load(b.FieldAddr(header, 1))
	// Unlink before any site-specific work. The record remains valid until its
	// typed payload has been copied and the frozen release helper runs below.
	b.Store(s.dynamicHead, next)
	dispatch := b.Switch(tag, invalid)
	for index, site := range s.sites {
		if site == nil || site.plan == nil || site.plan.tag == 0 {
			panic("dynamic coroutine cleanup site has no stable tag")
		}
		dispatch.Case(p.prog.IntVal(uint64(site.plan.tag), p.prog.Uint32()), siteBlocks[index])
	}
	dispatch.End(b)

	for index, site := range s.sites {
		b.SetBlock(siteBlocks[index])
		node := b.Convert(p.prog.Pointer(site.nodeType), record)
		if site.descriptorField >= 0 {
			b.Store(site.descriptor, b.Load(b.FieldAddr(node, site.descriptorField)))
		}
		if site.closureField >= 0 {
			b.Store(site.closureContext, b.Load(b.FieldAddr(node, site.closureField)))
		}
		for argument := range site.args {
			b.Store(site.args[argument], b.Load(b.FieldAddr(node, site.argsField+argument)))
		}
		for keepalive := range site.keepalives {
			if site.keepaliveField < 0 {
				panic("dynamic coroutine cleanup lost its keepalive field")
			}
			b.Store(
				site.keepalives[keepalive],
				b.Load(b.FieldAddr(node, site.keepaliveField+keepalive)),
			)
		}
		finishSite := s.beginRelocatedSiteEmission(p, site)
		s.releaseDynamicRecord(p, b, site, record)
		args := make([]llssa.Expr, len(site.args))
		for argument := range args {
			args[argument] = b.Load(site.args[argument])
		}
		continues := s.emitSiteCall(p, b, site, args, true)
		finishSite()
		if continues {
			b.Jump(s.entry)
		}
	}

	b.SetBlock(invalid)
	b.Unreachable()
	b.SetBlock(done)
	s.emitCompletionDispatch(p, b)
}

func (s *coroStaticCleanupState) releaseDynamicRecord(p *context, b llssa.Builder, site *coroStaticCleanupSiteState, record llssa.Expr) {
	if site == nil || site.plan == nil || site.plan.instruction == nil {
		panic("dynamic coroutine cleanup release has no exact source SitePlan")
	}
	releaser, _, kind := p.compileFunction(s.dynamicFree)
	if releaser == nil || kind != goFunc {
		panic("dynamic coroutine cleanup FreeDeferNode target did not resolve to a Go entry")
	}
	p.observeCoroSiteRuntimeHelper("FreeDeferNode")
	b.Call(releaser.Expr, record)
}

func (s *coroStaticCleanupState) beginRelocatedSiteEmission(
	p *context,
	site *coroStaticCleanupSiteState,
) func() {
	if s == nil || p == nil || site == nil || site.plan == nil || site.plan.instruction == nil {
		panic("coroutine cleanup relocated emission has no exact source site")
	}
	return p.beginCoroRelocatedSiteEmission(site.plan.instruction, coroRuntimeHelperAtCleanup)
}

func (s *coroStaticCleanupState) emitSiteCall(
	p *context, b llssa.Builder, site *coroStaticCleanupSiteState, args []llssa.Expr,
	siteEmissionActive bool,
) bool {
	switch site.plan.kind {
	case coroStaticCleanupPlain:
		function, _, kind := p.compileFunction(site.plan.target)
		if function == nil || kind != goFunc {
			panic(fmt.Sprintf("coroutine plain cleanup target %q did not resolve to a Go entry", site.plan.targetPlan.ID))
		}
		b.Call(function.Expr, args...)
		return true
	case coroStaticCleanupCoroutine:
		closureContext := llssa.Nil
		if site.plan.closure != nil {
			if site.closureContext.IsNil() {
				panic("captured coroutine cleanup lost its closure-context slot")
			}
			closureContext = b.Load(site.closureContext)
		}
		p.compileCoroTargetAwaitWithContextAndRecovery(b, site.plan.target, closureContext, args, s, nil)
		return true
	case coroStaticCleanupDispatch:
		if site.descriptor.IsNil() || site.plan.signature == nil {
			panic("managed descriptor cleanup lost its typed descriptor/signature")
		}
		p.compileCoroManagedDispatchAwaitValueWithRecovery(
			b, b.Load(site.descriptor), args, site.plan.signature, s, nil,
		)
		return true
	case coroStaticCleanupIntrinsic:
		if !isCoroAtomicIntrinsic(site.plan.intrinsic) {
			panic("coroutine intrinsic cleanup lost its frozen atomic opcode")
		}
		finishSite := p.beginCoroRelocatedIntrinsicEmission(
			site.plan.instruction, coroRuntimeHelperAtCleanup,
		)
		p.compileAtomicIntrinsic(b, site.plan.intrinsic, args)
		p.completeCoroIntrinsicCallEmission(
			site.plan.intrinsic, CoroIntrinsicCallInlineNoSuspend,
		)
		finishSite()
		return true
	case coroStaticCleanupBuiltin:
		finishSite := func() {}
		if !siteEmissionActive {
			finishSite = s.beginRelocatedSiteEmission(p, site)
		}
		deferred := site.plan.instruction
		if deferred == nil {
			panic("coroutine deferred builtin lost its source Defer instruction")
		}
		p.recordCallerLocationForCall(b, &deferred.Call)
		switch site.plan.builtin {
		case "delete", "copy", "clear", "print", "println":
			b.Call(llssa.Builtin(site.plan.builtin), args...)
			finishSite()
			return true
		case "close":
			if len(args) != 1 {
				panic("coroutine deferred close lost its captured channel")
			}
			p.compileCoroChanCloseWithRecovery(b, args[0], s)
			finishSite()
			return true
		case "panic":
			if len(args) != 1 {
				panic("coroutine deferred panic lost its captured payload")
			}
			typeWord := b.EfaceType(args[0])
			dataWord := b.InterfaceData(args[0])
			finishSite()
			s.replacePanicInline(p, b, typeWord, dataWord, p.coroCurrentSourceLine())
			return false
		case "recover":
			if len(args) != 0 {
				panic("coroutine deferred recover acquired arguments")
			}
			// `defer recover()` is not a direct recover invocation by a deferred
			// function and therefore returns nil without consuming the panic.
			finishSite()
			return true
		default:
			panic("coroutine deferred builtin lost its frozen operation recipe")
		}
	case coroStaticCleanupCgoWorker:
		if site.plan.cgoWorker == nil {
			panic("coroutine deferred cgo worker lost its frozen typed shape")
		}
		finishSite := func() {}
		if !siteEmissionActive {
			finishSite = s.beginRelocatedSiteEmission(p, site)
		}
		p.compileCoroWorkerCgoTransaction(
			b, *site.plan.cgoWorker, args, site.keepalives,
		)
		finishSite()
		return true
	default:
		panic("coroutine cleanup target has an invalid kind")
	}
}

func (s *coroStaticCleanupState) emitCompletionDispatch(p *context, b llssa.Builder) {
	invalid := p.fn.MakeBlock()
	baseDispatch := p.fn.MakeBlock()
	// The panic overlay wins until one exact deferred child reports
	// CompletionReturnRecovered. Only then may the original base continuation
	// (normal return, recover-return reconstruction, RunDefers, or future
	// cancellation/Goexit) resume.
	b.If(b.Load(s.panicActive), s.panic, baseDispatch)
	b.SetBlock(baseDispatch)
	dispatch := b.Switch(b.Load(s.continuation), invalid)
	dispatch.Case(b.Prog.IntVal(uint64(coroStaticCleanupContinueComplete), b.Prog.Uint32()), s.complete)
	if p.goFn == nil || p.goFn.Recover == nil {
		panic("coroutine cleanup recover continuation has no canonical SSA recover block")
	}
	dispatch.Case(
		b.Prog.IntVal(uint64(coroStaticCleanupContinueRecover), b.Prog.Uint32()),
		p.sourceBlock(p.goFn.Recover.Index),
	)
	for _, continuation := range s.run {
		dispatch.Case(b.Prog.IntVal(uint64(continuation.id), b.Prog.Uint32()), continuation.block)
	}
	dispatch.End(b)

	b.SetBlock(invalid)
	// The continuation is written only by compiler-owned constant stores.  An
	// unknown value is therefore unreachable IR, not a user-triggerable runtime
	// outcome that needs a second scheduler/error hook.
	b.Unreachable()
}
