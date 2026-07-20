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
)

type coroStaticCleanupSitePlan struct {
	instruction *ssa.Defer
	target      *ssa.Function
	targetPlan  coro.FunctionPlan
	kind        coroStaticCleanupTargetKind
	closure     *ssa.MakeClosure
	descriptor  ssa.Value
	signature   *types.Signature
	callPlan    coro.SSACallPlan
	tag         uint32
}

type coroStaticCleanupPlan struct {
	sites                     []*coroStaticCleanupSitePlan
	terminalResultAllocations []*ssa.Alloc
	dynamic                   bool
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
	infos := blocks.Infos(fn.Blocks)
	byInstruction := make(map[*ssa.Defer]*coroStaticCleanupSitePlan)
	allSites := make([]*coroStaticCleanupSitePlan, 0)
	var dynamicTrigger *ssa.Defer
	runDefers := 0
	for _, block := range fn.Blocks {
		for instructionIndex, raw := range block.Instrs {
			switch instruction := raw.(type) {
			case *ssa.Defer:
				if instruction.DeferStack != nil {
					return nil, fmt.Errorf("defer in block %d uses an alternate dynamic defer stack", block.Index)
				}
				if infos[block.Index].InLoop && dynamicTrigger == nil {
					dynamicTrigger = instruction
				}
				target, targetPlan, kind, err := resolveCoroStaticCleanupTarget(whole, caller, instruction, universe)
				if err != nil {
					return nil, fmt.Errorf("defer in block %d: %w", block.Index, err)
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
				}
				if kind == coroStaticCleanupDispatch {
					site.descriptor = instruction.Call.Value
					site.signature = instruction.Call.Signature()
					callPlan, planned := whole.CallPlan(instruction)
					if !planned {
						return nil, fmt.Errorf("defer in block %d: managed descriptor cleanup lost its CallPlan", block.Index)
					}
					site.callPlan = callPlan
					if err := validateCoroManagedCleanupPlainTargets(
						whole, universe, callPlan, frameRetentionABI,
					); err != nil {
						return nil, fmt.Errorf("defer in block %d: %w", block.Index, err)
					}
				}
				byInstruction[instruction] = site
				allSites = append(allSites, site)
			case *ssa.RunDefers:
				if !coroStaticRunDefersReturns(block, instructionIndex) {
					return nil, fmt.Errorf("RunDefers in block %d is not followed only by named-result reloads and the terminal Return", block.Index)
				}
				runDefers++
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
	if runDefers == 0 {
		return nil, fmt.Errorf("static defer body has no RunDefers instruction")
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
		allocator, allocOK := whole.ResolveLoweredCall(fn, "AllocU")
		releaser, freeOK := whole.ResolveLoweredCall(fn, "FreeDeferNode")
		if !allocOK || allocator == nil || !freeOK || releaser == nil {
			return nil, fmt.Errorf("dynamic cleanup requires exact owner-scoped AllocU and FreeDeferNode edges")
		}
		return &coroStaticCleanupPlan{
			sites:                     allSites,
			terminalResultAllocations: terminalResultAllocations,
			dynamic:                   true,
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

// coroDeferRequiresDynamicCleanup is shared by universe preparation and the
// cleanup planner. Compiler-generated allocation/release edges are frozen only
// for a source defer that belongs to a cyclic CFG block; one such occurrence
// authorizes the single per-owner AllocU/FreeDeferNode identities used by every
// record in that owner.
func coroDeferRequiresDynamicCleanup(instruction *ssa.Defer) bool {
	if instruction == nil || instruction.Parent() == nil || instruction.Block() == nil {
		return false
	}
	infos := blocks.Infos(instruction.Parent().Blocks)
	index := instruction.Block().Index
	return index >= 0 && index < len(infos) && infos[index].InLoop
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
		if !frozen || call.Target != helper.target || call.RawPlain || call.UnwindOnly || call.ExplicitStatusElided {
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
// terminal shape used for named results: RunDefers, zero or more direct loads
// from owner-local result cells, then Return. It returns the cells rather than
// merely a boolean so cleanup planning, frame proof, and code generation share
// one structural fact.
func coroStaticRunDefersReconstructionAllocations(
	block *ssa.BasicBlock,
	instructionIndex int,
) ([]*ssa.Alloc, bool) {
	if block == nil || instructionIndex < 0 || instructionIndex >= len(block.Instrs) {
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
// of ordinary heap cells whose values are reconstructed after RunDefers. Only
// source-entry cells are eligible: moving a conditional or loop allocation to
// the coroutine prologue would change its execution count. Stack/frame cells
// need no special treatment because coroFrameAlloc already defines them in the
// physical ramp.
func coroStaticTerminalReconstructionAllocations(fn *ssa.Function) ([]*ssa.Alloc, error) {
	if fn == nil {
		return nil, nil
	}
	selected := make(map[*ssa.Alloc]struct{})
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
				if !allocation.Heap {
					continue
				}
				if allocation.Block() == nil || allocation.Block().Index != 0 {
					return nil, fmt.Errorf("RunDefers terminal heap allocation %q is outside source block zero", allocation.Name())
				}
				selected[allocation] = struct{}{}
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
	if !ok || target == nil || target != raw {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("defer target %q is not its exact canonical static function", callPlan.Targets[0])
	}
	targetPlan, ok := whole.FunctionPlan(target)
	if !ok || targetPlan.ID != callPlan.Targets[0] {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("defer target %q has no canonical function plan", callPlan.Targets[0])
	}
	if target.Signature == nil || target.Signature.Variadic() {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("variadic or signature-less defer target is unsupported")
	}
	if closure != nil {
		valuePlan, exact := whole.ValuePlan(closure)
		if !exact || len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 ||
			valuePlan.Funcs[0].Rep != coro.DirectCoro || valuePlan.Funcs[0].MayBeNil ||
			len(valuePlan.Funcs[0].Targets) != 1 || valuePlan.Funcs[0].Targets[0] != targetPlan.ID {
			return nil, coro.FunctionPlan{}, 0, fmt.Errorf("captured coroutine defer has no exact direct coroutine closure plan")
		}
		if callPlan.Rep != coro.DirectCoro {
			return nil, coro.FunctionPlan{}, 0, fmt.Errorf("captured MakeClosure defer requires direct coroutine representation, got %s", callPlan.Rep)
		}
	}
	if target.Signature.Recv() != nil {
		if err := validateCoroStaticMethodCallOperands(instruction, target, nil); err != nil {
			return nil, coro.FunctionPlan{}, 0, err
		}
	} else if err := validateCoroStaticCleanupOperands(common, target); err != nil {
		return nil, coro.FunctionPlan{}, 0, err
	}
	if coroPhysicalSignatureContainsFunctionValue(coroPhysicalNormalizeSourceSignature(target.Signature)) {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("function-valued defer arguments require dynamic cleanup records")
	}
	if targetPlan.Exec.Contains(coro.NeedsCleanupFrame) {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("defer target %q registers nested cleanup", targetPlan.ID)
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
				semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(instruction)
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
	nodeType        llssa.Type
	descriptorField int
	closureField    int
	argsField       int
}

type coroStaticCleanupContinuation struct {
	id    uint32
	block llssa.BasicBlock
}

type coroStaticCleanupState struct {
	sites         []*coroStaticCleanupSiteState
	byDefer       map[*ssa.Defer]*coroStaticCleanupSiteState
	dynamic       bool
	dynamicHead   llssa.Expr
	dynamicHeader llssa.Type
	dynamicAlloc  *ssa.Function
	dynamicFree   *ssa.Function
	continuation  llssa.Expr
	panicActive   llssa.Expr
	panicType     llssa.Expr
	panicData     llssa.Expr
	entry         llssa.BasicBlock
	complete      llssa.BasicBlock
	panic         llssa.BasicBlock
	run           []coroStaticCleanupContinuation
}

// beginCoroStaticCleanup allocates and initializes every value before the
// initial suspend.  A cancellation decision on the first resume can therefore
// safely enter the same empty drainer as every later terminal path.
func (p *context) beginCoroStaticCleanup(b llssa.Builder, plan *coroStaticCleanupPlan) *coroStaticCleanupState {
	if plan == nil || len(plan.sites) == 0 {
		return nil
	}
	state := &coroStaticCleanupState{
		sites:        make([]*coroStaticCleanupSiteState, 0, len(plan.sites)),
		byDefer:      make(map[*ssa.Defer]*coroStaticCleanupSiteState, len(plan.sites)),
		dynamic:      plan.dynamic,
		dynamicAlloc: plan.dynamicAlloc,
		dynamicFree:  plan.dynamicFree,
	}
	state.continuation = b.AllocaT(p.prog.Uint32())
	b.Store(state.continuation, p.prog.IntVal(0, p.prog.Uint32()))
	state.panicActive = b.AllocaT(p.prog.Bool())
	b.Store(state.panicActive, p.prog.BoolVal(false))
	state.panicType = b.AllocaT(p.prog.VoidPtr())
	state.panicData = b.AllocaT(p.prog.VoidPtr())
	b.Store(state.panicType, p.prog.Nil(p.prog.VoidPtr()))
	b.Store(state.panicData, p.prog.Nil(p.prog.VoidPtr()))
	if state.dynamic {
		if state.dynamicAlloc == nil || state.dynamicFree == nil {
			panic("dynamic coroutine cleanup lacks frozen allocator/release targets")
		}
		state.dynamicHead = b.AllocaT(p.prog.VoidPtr())
		b.Store(state.dynamicHead, p.prog.Nil(p.prog.VoidPtr()))
		state.dynamicHeader = p.prog.Struct(p.prog.VoidPtr(), p.prog.Uint32())
	}
	for _, sitePlan := range plan.sites {
		site := &coroStaticCleanupSiteState{
			plan:            sitePlan,
			descriptorField: -1,
			closureField:    -1,
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
			if sitePlan.descriptor == nil || sitePlan.signature == nil {
				panic("managed descriptor cleanup site has no frozen descriptor/signature")
			}
			descriptorType := p.type_(sitePlan.descriptor.Type(), llssa.InGo)
			closure, ok := types.Unalias(descriptorType.RawType()).Underlying().(*types.Struct)
			if !ok || !llssa.IsClosure(closure) {
				panic(fmt.Sprintf("managed descriptor cleanup site lowered %s as %s, want canonical closure", sitePlan.descriptor.Type(), descriptorType.RawType()))
			}
			site.descriptor = b.AllocaT(descriptorType)
			site.descriptorType = descriptorType
			b.Store(site.descriptor, p.prog.Zero(descriptorType))
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
			site.closureContext = b.AllocaT(contextType)
			b.Store(site.closureContext, p.prog.Nil(contextType))
			if state.dynamic {
				site.closureField = len(nodeFields)
				nodeFields = append(nodeFields, contextType)
			}
		}
		site.argsField = len(nodeFields)
		for _, argument := range sitePlan.instruction.Call.Args {
			argumentType := p.type_(argument.Type(), llssa.InGo)
			site.args = append(site.args, b.AllocaT(argumentType))
			if state.dynamic {
				nodeFields = append(nodeFields, argumentType)
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
		if site.descriptor.IsNil() || site.plan.descriptor == nil {
			panic("managed descriptor cleanup registration has no typed descriptor slot")
		}
		descriptor = p.compileValue(b, site.plan.descriptor)
		closure, ok := types.Unalias(descriptor.RawType()).Underlying().(*types.Struct)
		if !ok || !llssa.IsClosure(closure) || site.descriptorType == nil ||
			!types.Identical(descriptor.RawType(), site.descriptorType.RawType()) {
			want := "<missing>"
			if site.descriptorType != nil {
				want = site.descriptorType.RawType().String()
			}
			panic(fmt.Sprintf("managed descriptor cleanup registration lowered callee as %s, want %s", descriptor.RawType(), want))
		}
		if !s.dynamic {
			b.Store(site.descriptor, descriptor)
		}
	}
	closureContext := llssa.Nil
	if site.plan.closure != nil {
		if site.closureContext.IsNil() {
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
	if s.dynamic {
		s.pushDynamic(p, b, site, descriptor, closureContext, args)
		return
	}
	for index, argument := range args {
		b.Store(site.args[index], argument)
	}
	b.Store(site.active, b.Prog.BoolVal(true))
}

// pushDynamic publishes a fully initialized heterogeneous record with one
// release-store-equivalent compiler order: Go closure/arguments are evaluated
// first, the private node is filled next, and the frame-rooted head changes
// last. No scheduler suspension is permitted in the frozen AllocU edge.
func (s *coroStaticCleanupState) pushDynamic(
	p *context, b llssa.Builder, site *coroStaticCleanupSiteState,
	descriptor, closureContext llssa.Expr, args []llssa.Expr,
) {
	if s == nil || p == nil || site == nil || site.nodeType == nil || s.dynamicHead.IsNil() ||
		s.dynamicAlloc == nil || site.plan == nil || site.plan.tag == 0 {
		panic("dynamic coroutine cleanup push has incomplete frozen state")
	}
	allocator, _, kind := p.compileFunction(s.dynamicAlloc)
	if allocator == nil || kind != goFunc {
		panic("dynamic coroutine cleanup AllocU target did not resolve to a Go entry")
	}
	raw := b.Call(allocator.Expr, llssa.SizeOf(p.prog, site.nodeType))
	node := b.Convert(p.prog.Pointer(site.nodeType), raw)
	b.Store(b.FieldAddr(node, 0), b.Load(s.dynamicHead))
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
	b.Store(s.dynamicHead, b.Convert(p.prog.VoidPtr(), node))
}

func (s *coroStaticCleanupState) enter(b llssa.Builder, continuation uint32) {
	if s == nil || s.entry == nil {
		panic("coroutine static cleanup entry is not bound")
	}
	b.Store(s.continuation, b.Prog.IntVal(uint64(continuation), b.Prog.Uint32()))
	b.Store(s.panicActive, b.Prog.BoolVal(false))
	b.Store(s.panicType, b.Prog.Nil(b.Prog.VoidPtr()))
	b.Store(s.panicData, b.Prog.Nil(b.Prog.VoidPtr()))
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

func (s *coroStaticCleanupState) enterPanic(b llssa.Builder, typeWord, dataWord llssa.Expr) {
	// A panic reached from source execution has no earlier cleanup base. A
	// successful recover returns through x/tools' canonical Recover block.
	b.Store(s.continuation, b.Prog.IntVal(uint64(coroStaticCleanupContinueRecover), b.Prog.Uint32()))
	s.replacePanic(b, typeWord, dataWord)
}

// replacePanic is used only when a deferred child itself panics. Preserve the
// cleanup base (normal return, Recover, RunDefers, and future cancel/Goexit)
// while replacing the active panic overlay with the child's newer payload.
func (s *coroStaticCleanupState) replacePanic(b llssa.Builder, typeWord, dataWord llssa.Expr) {
	s.setPanicOverlay(b, typeWord, dataWord)
	s.resume(b)
}

func (s *coroStaticCleanupState) setPanicOverlay(b llssa.Builder, typeWord, dataWord llssa.Expr) {
	if s == nil {
		panic("coroutine cleanup panic overlay has no state")
	}
	b.Store(s.panicActive, b.Prog.BoolVal(true))
	b.Store(s.panicType, b.Convert(b.Prog.VoidPtr(), typeWord))
	b.Store(s.panicData, b.Convert(b.Prog.VoidPtr(), dataWord))
}

// recoverAwaitArguments encodes the current panic overlay into the unified V3
// child handoff. Selects avoid a second runtime hook and keep normal cleanup on
// the same CompletionRecord transaction with nil recovery words.
func (s *coroStaticCleanupState) recoverAwaitArguments(
	p *context, b llssa.Builder,
) (mode, typeWord, dataWord llssa.Expr) {
	if s == nil || p == nil || p.currentCoro == nil {
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
		s.emitSiteCall(p, b, site, args)
		b.Jump(next)
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
		s.releaseDynamicRecord(p, b, record)
		args := make([]llssa.Expr, len(site.args))
		for argument := range args {
			args[argument] = b.Load(site.args[argument])
		}
		s.emitSiteCall(p, b, site, args)
		b.Jump(s.entry)
	}

	b.SetBlock(invalid)
	b.Unreachable()
	b.SetBlock(done)
	s.emitCompletionDispatch(p, b)
}

func (s *coroStaticCleanupState) releaseDynamicRecord(p *context, b llssa.Builder, record llssa.Expr) {
	releaser, _, kind := p.compileFunction(s.dynamicFree)
	if releaser == nil || kind != goFunc {
		panic("dynamic coroutine cleanup FreeDeferNode target did not resolve to a Go entry")
	}
	b.Call(releaser.Expr, record)
}

func (s *coroStaticCleanupState) emitSiteCall(
	p *context, b llssa.Builder, site *coroStaticCleanupSiteState, args []llssa.Expr,
) {
	switch site.plan.kind {
	case coroStaticCleanupPlain:
		function, _, kind := p.compileFunction(site.plan.target)
		if function == nil || kind != goFunc {
			panic(fmt.Sprintf("coroutine plain cleanup target %q did not resolve to a Go entry", site.plan.targetPlan.ID))
		}
		b.Call(function.Expr, args...)
	case coroStaticCleanupCoroutine:
		closureContext := llssa.Nil
		if site.plan.closure != nil {
			if site.closureContext.IsNil() {
				panic("captured coroutine cleanup lost its closure-context slot")
			}
			closureContext = b.Load(site.closureContext)
		}
		p.compileCoroTargetAwaitWithContextAndRecovery(b, site.plan.target, closureContext, args, s, nil)
	case coroStaticCleanupDispatch:
		if site.descriptor.IsNil() || site.plan.signature == nil {
			panic("managed descriptor cleanup lost its typed descriptor/signature")
		}
		p.compileCoroManagedDispatchAwaitValueWithRecovery(
			b, b.Load(site.descriptor), args, site.plan.signature, s, nil,
		)
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
