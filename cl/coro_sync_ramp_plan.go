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
	"go/constant"
	"go/token"
	"go/types"
	"math/big"
	"sort"
	"strings"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// coroSyncRampKind classifies a coroutine primary whose public $coro entry can
// execute a bounded synchronous fast path without creating an LLVM coroutine
// frame, returning a nil handle when the fast path completes normally.
type coroSyncRampKind uint8

const (
	coroSyncRampNone coroSyncRampKind = iota
	// coroSyncRampTryBailout executes an acyclic or single-iteration peeled
	// synchronous prefix where every bailout happens before any observable
	// side effect (or on a failed atomic CAS). Pure CAS/read-only ramps also
	// publish a private $coro.try helper composable by outer sync-fast ramps.
	coroSyncRampTryBailout
	// coroSyncRampTailSlow executes an acyclic synchronous prefix whose slow
	// paths tail-call direct $coro helpers directly, avoiding a frame on both
	// fast and slow paths.
	coroSyncRampTailSlow
	// coroSyncRampResumeSlow executes an acyclic synchronous prefix that may
	// mutate state (e.g. an atomic RMW) before hitting either a fast Return or
	// one of a small set of phi-free slow continuation blocks in $coro.slow.
	coroSyncRampResumeSlow
	// coroSyncRampSingleAwait executes a pure entry prefix followed by one
	// awaited $coro call whose result slot matches this function's own result
	// slot, forwarding the caller's out pointer directly through and returning
	// nil when the child completes synchronously and the post-call path returns.
	coroSyncRampSingleAwait
	// coroSyncRampCallerInlineYield describes a tiny acyclic void function
	// whose fast path contains a cooperative llgo.coroYield() before a fast
	// completion check or slow tail-call. Direct coroutine callers inline the
	// fast path in their own frame while dynamic/spawned callers use $coro.
	coroSyncRampCallerInlineYield
	// coroSyncRampBoundedLoop executes a pure read-only single-natural-loop
	// leaf coroutine synchronously for up to a compile-time proven backedge
	// budget derived from DefaultMaxPlainInstructions, bailing out cleanly to
	// $coro.slow (preserving full preemption) whenever the work bound is
	// exceeded or a nil/bounds guard trips.
	coroSyncRampBoundedLoop
)

type coroSyncFastExitKind uint8

const (
	coroSyncFastExitNone coroSyncFastExitKind = iota
	coroSyncFastExitReturn
	coroSyncFastExitTailCall
	coroSyncFastExitBailStart
	coroSyncFastExitResumeSlow
	coroSyncFastExitResumePostCall
	coroSyncFastExitIf
	coroSyncFastExitJump
)

// coroSyncInlineAtomic describes a trivial single-block $outcome atomic wrapper
// (such as sync/atomic.(*Uint64).Add) lowered directly as a nil-checked
// FieldAddr + atomic intrinsic on the sync-fast path.
type coroSyncInlineAtomic struct {
	fieldIndex int // -1 if atomic operates directly on param 0
	opcode     int
}

type coroSyncFastNode struct {
	block      *ssa.BasicBlock
	predBlock  *ssa.BasicBlock
	startInstr int
	endInstr   int // exclusive; -1 means through terminator
	mutated    bool

	singleAwait     *ssa.Call
	singleAwaitPost *coroSyncFastNode

	exitKind    coroSyncFastExitKind
	retInstr    *ssa.Return
	tailCall    *ssa.Call
	tailTarget  *ssa.Function
	resumeTag   uint8
	resumeBlock *ssa.BasicBlock
	cond        ssa.Value
	trueNode    *coroSyncFastNode
	falseNode   *coroSyncFastNode
	nextNode    *coroSyncFastNode

	tryCalls      map[*ssa.Call]*ssa.Function
	atomicOpcodes map[*ssa.Call]int
	inlineAtomics map[*ssa.Call]coroSyncInlineAtomic
	constBools    map[*ssa.Call]bool
	yieldCalls    map[*ssa.Call]bool

	nodeHasOutcomeCall     bool
	nodeHasPlainSideEffect bool
	nodeHasNonCASMutation  bool
	nodeHasPostMutNilGuard bool
}

type coroSyncSlowTarget struct {
	tag   uint8
	block *ssa.BasicBlock
}

type coroSyncSlowAugCFG struct {
	reach               map[*ssa.BasicBlock]bool
	dom                 map[*ssa.BasicBlock]map[*ssa.BasicBlock]bool
	splitBlock          *ssa.BasicBlock
	splitCallIdx        int
	splitBlockReentered bool
}

type coroSyncLoopEntryGuardKind uint8

const (
	coroSyncLoopEntryGuardNone coroSyncLoopEntryGuardKind = iota
	coroSyncLoopEntryGuardValue
	coroSyncLoopEntryGuardLen
	coroSyncLoopEntryGuardCap
)

type coroSyncBoundedLoopPlan struct {
	header          *ssa.BasicBlock
	latch           *ssa.BasicBlock
	entryPred       *ssa.BasicBlock
	liveBlocks      map[*ssa.BasicBlock]bool
	maxBackedges    uint32
	entryGuardKind  coroSyncLoopEntryGuardKind
	entryGuardValue ssa.Value
	entryGuardLimit uint64
}

type coroPhysicalSyncRampPlan struct {
	kind               coroSyncRampKind
	hasTryHelper       bool
	hasBoxHelper       bool
	root               *coroSyncFastNode
	boundedLoop        *coroSyncBoundedLoopPlan
	slowTargets        []coroSyncSlowTarget
	liveValues         []ssa.Value
	liveInByTag        map[uint8]map[ssa.Value]bool
	slowAugCFG         *coroSyncSlowAugCFG
	singleAwaitCall    *ssa.Call
	singleAwaitPlan    coroPhysicalInstructionPlan
	singleAwaitBoxed   bool
	singleAwaitResults *types.Tuple
	needsSlowSplit     bool
}

func (plan *coroPhysicalSyncRampPlan) returnsNullableHandle() bool {
	return plan != nil && plan.kind != coroSyncRampNone && plan.kind != coroSyncRampCallerInlineYield
}

const (
	coroSyncRampMaxNodes          = 96
	coroSyncRampMaxDepth          = 16
	coroSyncRampMaxInstrs         = 320
	coroSyncRampMaxLiveValues     = 6
	coroSyncRampMaxSlowTargets    = 6
	coroSyncRampMaxOutcomeCalls   = 6
	coroSyncRampInlineMaxInstrs   = 28
	coroSyncBoundedLoopWorkBudget = 1024
)

const (
	coroOutcomePropagateHelperNameV1 = "__llgo_coro_outcome_propagate_v1"
	// coroOutcomePropagateLocalFaultNil distinguishes a nil-pointer fault
	// originating inside the sync-fast ramp itself (after a prior call mutated
	// observable state) from coroAwaitCompletionFaultNil propagated out of an
	// already-traced child $outcome call.
	coroOutcomePropagateLocalFaultNil uint64 = 8
)

// coroSyncPlanner bundles read-only frozen planning context shared across all
// sync-fast ramp passes. It consumes only frozen ProgramIR/SSAPlan facts and
// never matches package paths, function names, or source digests.
type coroSyncPlanner struct {
	prog               llssa.Program
	whole              *coro.SSAPlan
	ir                 *coroProgramIR
	roots              map[*ssa.Function]bool
	resolve            func(*ssa.Function) (*ssa.Function, bool)
	needsRecover       func(*ssa.Function) bool
	reflectCheck       func(*ssa.CallCommon) bool
	targetHasSyncRamp         func(*ssa.Function) bool
	targetHasTryHelper        func(*ssa.Function) bool
	targetHasReadOnlyTry      func(*ssa.Function) bool
	targetHasBoxHelper        func(*ssa.Function) bool
	hasSliceCopy              bool
}

// planCoroPhysicalSyncRamps computes general nullable-handle sync-fast ramps
// across all staged physical function plans to a fixed point before ProgramIR
// seals them.
func planCoroPhysicalSyncRamps(
	stage *coroPhysicalPlanStage,
	prog llssa.Program,
	whole *coro.SSAPlan,
	ir *coroProgramIR,
	resolve func(*ssa.Function) (*ssa.Function, bool),
	needsRecover func(*ssa.Function) bool,
	reflectCheck func(*ssa.CallCommon) bool,
	hasAllocZ bool,
	hasSliceCopy bool,
) {
	if stage == nil || whole == nil || ir == nil || len(stage.plans) == 0 {
		return
	}
	roots := make(map[*ssa.Function]bool, len(whole.Roots()))
	for _, root := range whole.Roots() {
		if root.Function != nil {
			roots[root.Function] = true
		}
	}

	keys := make([]emissionFunctionOwnerKey, 0, len(stage.plans))
	byFunc := make(map[*ssa.Function][]*coroPhysicalFunctionPlan, len(stage.plans))
	for key, plan := range stage.plans {
		keys = append(keys, key)
		if plan != nil && plan.function != nil {
			byFunc[plan.function] = append(byFunc[plan.function], plan)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		an, bn := "", ""
		if a.function != nil {
			an = a.function.String()
		}
		if b.function != nil {
			bn = b.function.String()
		}
		if an != bn {
			return an < bn
		}
		ao, bo := "", ""
		if a.owner != nil {
			ao = a.owner.identity
		}
		if b.owner != nil {
			bo = b.owner.identity
		}
		return ao < bo
	})

	pl := &coroSyncPlanner{
		prog:         prog,
		whole:        whole,
		ir:           ir,
		roots:        roots,
		resolve:      resolve,
		needsRecover: needsRecover,
		reflectCheck: reflectCheck,
		hasSliceCopy: hasSliceCopy,
		targetHasSyncRamp: func(fn *ssa.Function) bool {
			seen := make(map[*ssa.Function]bool)
			for fn != nil && !seen[fn] {
				seen[fn] = true
				plans := byFunc[fn]
				if len(plans) == 0 {
					return false
				}
				var forward *ssa.Function
				allForward, allSync := true, true
				for _, p := range plans {
					if p == nil {
						return false
					}
					if p.tailForward != nil {
						if forward == nil {
							forward = p.tailForward.target
						} else if forward != p.tailForward.target {
							allForward = false
						}
					} else {
						allForward = false
					}
					if !p.syncRamp.returnsNullableHandle() {
						allSync = false
					}
				}
				if allSync {
					return true
				}
				if allForward && forward != nil {
					fn = forward
					continue
				}
				return false
			}
			return false
		},
		targetHasTryHelper: func(fn *ssa.Function) bool {
			plans := byFunc[fn]
			if len(plans) == 0 {
				return false
			}
			for _, p := range plans {
				if p == nil || p.syncRamp == nil || !p.syncRamp.hasTryHelper {
					return false
				}
			}
			return true
		},
		targetHasReadOnlyTry: func(fn *ssa.Function) bool {
			plans := byFunc[fn]
			if len(plans) == 0 {
				return false
			}
			for _, p := range plans {
				if p == nil || p.syncRamp == nil || !p.syncRamp.hasTryHelper ||
					p.syncRamp.kind != coroSyncRampBoundedLoop {
					return false
				}
			}
			return true
		},
		targetHasBoxHelper: func(fn *ssa.Function) bool {
			if !hasAllocZ {
				return false
			}
			plans := byFunc[fn]
			if len(plans) == 0 {
				return false
			}
			for _, p := range plans {
				if p == nil || p.syncRamp == nil || !coroSyncRampCanBox(p.function, p.syncRamp) {
					return false
				}
			}
			return true
		},
	}

	// Pre-pass: discover pure read-only BoundedLoop leaves first so their
	// $coro.try helpers are available to outer DirectFastCFG callers regardless
	// of alphabetical symbol ordering.
	for _, key := range keys {
		plan := stage.plans[key]
		if plan == nil || plan.syncRamp != nil {
			continue
		}
		if ramp := pl.planBoundedLoopCandidate(plan); ramp != nil {
			plan.syncRamp = ramp
		}
	}

	// Converge in up to 6 passes: standalone ramps first, then outer wrappers
	// calling .try or nullable-handle sync-ramp callees.
	for pass := 0; pass < 6; pass++ {
		changed := false
		for _, key := range keys {
			plan := stage.plans[key]
			if plan == nil || plan.syncRamp != nil {
				continue
			}
			if ramp := pl.planOne(plan); ramp != nil {
				plan.syncRamp = ramp
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	// Demand-mark .box helpers only for targets actually referenced by a boxed
	// SingleAwait caller (and transitively through TailSlow tail-call chains).
	var markBoxTarget func(fn *ssa.Function)
	markBoxTarget = func(fn *ssa.Function) {
		if fn == nil || !pl.targetHasBoxHelper(fn) {
			return
		}
		for _, p := range byFunc[fn] {
			if p == nil || p.syncRamp == nil || p.syncRamp.hasBoxHelper {
				continue
			}
			p.syncRamp.hasBoxHelper = true
			if p.syncRamp.kind == coroSyncRampTailSlow {
				var walk func(n *coroSyncFastNode)
				walk = func(n *coroSyncFastNode) {
					if n == nil {
						return
					}
					if n.exitKind == coroSyncFastExitTailCall && n.tailTarget != nil {
						markBoxTarget(n.tailTarget)
					}
					walk(n.nextNode)
					walk(n.trueNode)
					walk(n.falseNode)
				}
				walk(p.syncRamp.root)
			}
		}
	}
	for _, key := range keys {
		plan := stage.plans[key]
		if plan != nil && plan.syncRamp != nil &&
			plan.syncRamp.kind == coroSyncRampSingleAwait &&
			plan.syncRamp.singleAwaitBoxed {
			markBoxTarget(plan.syncRamp.singleAwaitPlan.controlTarget)
		}
	}
}

func coroSyncRampCanBox(fn *ssa.Function, ramp *coroPhysicalSyncRampPlan) bool {
	if fn == nil || ramp == nil {
		return false
	}
	fnSig := coroPhysicalNormalizeSourceSignature(fn.Signature)
	return fnSig != nil && fnSig.Results().Len() > 0 &&
		(ramp.kind == coroSyncRampTryBailout ||
			ramp.kind == coroSyncRampTailSlow ||
			ramp.hasTryHelper ||
			ramp.kind == coroSyncRampBoundedLoop ||
			ramp.kind == coroSyncRampResumeSlow)
}

func (pl *coroSyncPlanner) canonicalFunc(fn *ssa.Function) *ssa.Function {
	if fn != nil && pl.resolve != nil {
		if canonical, ok := pl.resolve(fn); ok && canonical != nil {
			return canonical
		}
	}
	return fn
}

func (pl *coroSyncPlanner) planEligibleBase(plan *coroPhysicalFunctionPlan) (coro.FunctionPlan, coroFunctionPreamblePlan, bool) {
	if plan == nil || plan.function == nil || plan.owner == nil ||
		plan.tailForward != nil || plan.critical != nil {
		return coro.FunctionPlan{}, coroFunctionPreamblePlan{}, false
	}
	fn := plan.function
	if len(fn.FreeVars) != 0 || len(fn.Blocks) == 0 ||
		fn.Signature == nil || fn.Signature.Variadic() ||
		hasNoInlineDirective(fn) || isCgoExternSymbol(fn) ||
		pl.roots[fn] || isCoroProgramManagedEntry(fn) || fn.Name() == "main" {
		return coro.FunctionPlan{}, coroFunctionPreamblePlan{}, false
	}
	logical, planned := pl.whole.FunctionPlan(fn)
	if !planned || logical.External != coro.Defined ||
		logical.Emission != coro.EmitCoroutine ||
		logical.ManagedEntry != coro.ManagedEntryCoroutine ||
		logical.Primary != coro.PrimaryCoroutine {
		return coro.FunctionPlan{}, coroFunctionPreamblePlan{}, false
	}
	// Mirror coro_abi.go's deadCleanupRecover proof: fn.Recover is only live
	// when ProgramIR retains NeedsCleanupFrame. Functions with deferred cleanup
	// (plan.cleanup != nil) always have a synthetic fn.Recover block for
	// defer unwind; if they don't call recover() themselves, they can still
	// take a pre-defer DirectFastCFG early-return prefix (len(slowTargets)==0)
	// while $coro.slow preserves the full cleanup/recover frame.
	if fn.Recover != nil && logical.Exec.Contains(coro.NeedsCleanupFrame) {
		if plan.cleanup == nil || (pl.needsRecover != nil && pl.needsRecover(fn)) {
			return coro.FunctionPlan{}, coroFunctionPreamblePlan{}, false
		}
	}
	preamble, err := pl.ir.functionPreambleForOwner(fn, plan.owner)
	if err != nil {
		return coro.FunctionPlan{}, coroFunctionPreamblePlan{}, false
	}
	for _, helper := range preamble.managedRuntimeHelpers {
		switch helper {
		case "BindRecoverFrame":
			// Functions with deferred cleanup carry BindRecoverFrame in their
			// preamble for $coro.slow. An early-return prefix that executes no
			// ssa.Defer and has no slowTargets never enters cleanup, so it can
			// safely bypass the preamble on the fast path.
			if plan.cleanup == nil {
				return coro.FunctionPlan{}, coroFunctionPreamblePlan{}, false
			}
		case "PushCallerLocationFrame":
			// Functions with logical caller frames can still execute a pure,
			// non-panicking, caller-unobserving TryBailout fast path while
			// $coro.slow retains Push/Update/PopCallerLocationFrame.
		default:
			return coro.FunctionPlan{}, coroFunctionPreamblePlan{}, false
		}
	}
	if _, isDelivery := pl.ir.deliveryProducers[fn]; isDelivery {
		return coro.FunctionPlan{}, coroFunctionPreamblePlan{}, false
	}
	// Sync-fast ramps lower expressions outside a managed coroutine frame and
	// cannot invoke non-raw-plain locality helpers (LocalPackageLogical /
	// EnsureLogicalLocalInitializer) for //llgo:gls or //llgo:tls globals.
	if pl.ir != nil {
		key := emissionFunctionOwnerKey{function: fn, owner: plan.owner}
		for _, site := range pl.ir.sitePlans[key] {
			if site.hasManagedRuntimeHelper("LocalPackageLogical") ||
				site.hasManagedRuntimeHelper("EnsureLogicalLocalInitializer") {
				return coro.FunctionPlan{}, coroFunctionPreamblePlan{}, false
			}
		}
	}
	if !coroSyncRampFunctionHasSlowWork(plan, pl.ir) &&
		!(strings.HasPrefix(fn.Synthetic, "wrapper for ") && logical.Effect == coro.NoSuspend) {
		return coro.FunctionPlan{}, coroFunctionPreamblePlan{}, false
	}
	return logical, preamble, true
}

func (pl *coroSyncPlanner) planBoundedLoopCandidate(plan *coroPhysicalFunctionPlan) *coroPhysicalSyncRampPlan {
	logical, _, ok := pl.planEligibleBase(plan)
	if !ok || logical.Recursive || plan.cleanup != nil {
		return nil
	}
	cyclic := cyclicBlocks(plan.function.Blocks)
	if len(cyclic) == 0 {
		return nil
	}
	return pl.planBoundedLoopLeaf(plan, cyclic)
}

func (pl *coroSyncPlanner) planOne(plan *coroPhysicalFunctionPlan) *coroPhysicalSyncRampPlan {
	logical, preamble, ok := pl.planEligibleBase(plan)
	if !ok {
		return nil
	}
	fn := plan.function
	cyclic := cyclicBlocks(fn.Blocks)
	hasSlowWork := coroSyncRampFunctionHasSlowWork(plan, pl.ir)

	// Recursive coroutines (e.g. recursive descent parsers) may still execute a
	// strictly non-recursive DirectFastCFG leaf fast path (tail-calling only
	// non-recursive callees and bailing recursive branches to $coro.slow),
	// guaranteeing zero native-stack recursion and full preemption.
	if logical.Recursive {
		if plan.cleanup != nil || preamble.logicalCallerEntry {
			return nil
		}
		directRamp := pl.planDirectFastCFG(plan, cyclic, true)
		if directRamp != nil && (!hasSlowWork || coroSyncDirectRampHasWork(directRamp, len(cyclic) == 0)) {
			directRamp.hasTryHelper = false
			return directRamp
		}
		return nil
	}

	// Functions with deferred cleanup (e.g. context.(*cancelCtx).Done) or a
	// PushCallerLocationFrame preamble (e.g. reflect.Value.Elem/Addr/Pointer)
	// can still take a pure TryBailout early-return prefix that never executes
	// any ssa.Defer, never tail-calls another coroutine, and (when logical
	// caller frames are active) cannot unwind or observe the caller stack;
	// any guard/error/slow path bails to $coro.slow (tag 0).
	if plan.cleanup != nil || preamble.logicalCallerEntry {
		directRamp := pl.planDirectFastCFG(plan, cyclic, false)
		if directRamp != nil && directRamp.kind == coroSyncRampTryBailout &&
			len(directRamp.slowTargets) == 0 &&
			(!hasSlowWork || coroSyncDirectRampHasWork(directRamp, len(cyclic) == 0)) &&
			(!preamble.logicalCallerEntry || pl.coroSyncFastTreeSafeWithoutCallerFrame(directRamp.root, plan)) {
			return directRamp
		}
		return nil
	}

	if hasSlowWork {
		if ramp := pl.planCallerInlineYield(plan, cyclic); ramp != nil {
			return ramp
		}
	}
	// If DirectFastCFG matches only a pure entry guard (no atomics, calls, or
	// tail-calls), prefer SingleAwait when available so wrappers like
	// io.ReadAtLeast fast-path their awaited call too. Functions forced to
	// EmitCoroutine solely by interface/promoted-method ABI (!hasSlowWork)
	// always benefit from a direct sync-fast ramp.
	directRamp := pl.planDirectFastCFG(plan, cyclic, false)
	if directRamp != nil && (!hasSlowWork || coroSyncDirectRampHasWork(directRamp, len(cyclic) == 0)) {
		return directRamp
	}
	if hasSlowWork {
		if ramp := pl.planSingleAwait(plan, cyclic); ramp != nil {
			return ramp
		}
		if ramp := pl.planBoundedLoopLeaf(plan, cyclic); ramp != nil {
			return ramp
		}
	}
	return nil
}

// coroSyncFastTreeSafeWithoutCallerFrame proves that a TryBailout fast-path
// tree cannot panic/unwind, cannot observe runtime.Caller/Callers, and cannot
// invoke any callee that itself pushes or observes a logical caller frame.
// Any nil/bounds/dynamic-compare guard or unrecognized path bails to $coro.slow,
// which executes PushCallerLocationFrame and UpdateLogicalCallerLocation.
func (pl *coroSyncPlanner) coroSyncFastTreeSafeWithoutCallerFrame(
	root *coroSyncFastNode,
	plan *coroPhysicalFunctionPlan,
) bool {
	if root == nil {
		return false
	}
	var check func(n *coroSyncFastNode) bool
	check = func(n *coroSyncFastNode) bool {
		if n == nil {
			return true
		}
		if n.nodeHasPostMutNilGuard {
			return false
		}
		for _, target := range n.tryCalls {
			if !pl.targetHasReadOnlyTry(target) {
				return false
			}
		}
		if n.block != nil {
			end := n.endInstr
			if end < 0 {
				end = len(n.block.Instrs) - 1
			}
			for i := n.startInstr; i < end; i++ {
				call, isCall := n.block.Instrs[i].(*ssa.Call)
				if !isCall {
					continue
				}
				if isPureLenCapBuiltin(call) || isPureCopyBuiltin(call) || isRuntimeKeepAliveCall(call) {
					continue
				}
				if _, ok := n.constBools[call]; ok {
					continue
				}
				if _, ok := n.atomicOpcodes[call]; ok {
					continue
				}
				if _, ok := n.inlineAtomics[call]; ok {
					continue
				}
				if _, ok := n.tryCalls[call]; ok {
					continue
				}
				phys := plan.instructions[call]
				var callee *ssa.Function
				if phys.control == coroPhysicalControlDirectOutcome {
					callee = pl.canonicalFunc(phys.controlTarget)
					if !pl.coroSyncOutcomeCalleeIsReadOnly(callee) {
						return false
					}
				} else if pl.plainGoCallAllowed(call, phys) {
					callee = pl.canonicalFunc(call.Common().StaticCallee())
				} else {
					return false
				}
				if callee == nil || isRuntimeCallerLookupFunc(callee) {
					return false
				}
				calleeLogical, planned := pl.whole.FunctionPlan(callee)
				if !planned || calleeLogical.Exec.Contains(coro.MayUnwind|coro.OpaqueExec|coro.BlockForeign) {
					return false
				}
				if calleePreamble, err := pl.ir.functionPreambleForOwner(callee, plan.owner); err == nil && calleePreamble.logicalCallerEntry {
					return false
				}
			}
		}
		switch n.exitKind {
		case coroSyncFastExitReturn, coroSyncFastExitBailStart:
			return true
		case coroSyncFastExitJump:
			return check(n.nextNode)
		case coroSyncFastExitIf:
			return check(n.trueNode) && check(n.falseNode)
		default:
			return false
		}
	}
	return check(root)
}

func coroSyncDirectRampHasWork(ramp *coroPhysicalSyncRampPlan, acyclic bool) bool {
	if ramp == nil {
		return false
	}
	if ramp.kind == coroSyncRampTailSlow || ramp.kind == coroSyncRampResumeSlow {
		return true
	}
	var walk func(n *coroSyncFastNode) bool
	walk = func(n *coroSyncFastNode) bool {
		if n == nil {
			return false
		}
		if len(n.atomicOpcodes) > 0 || len(n.inlineAtomics) > 0 || len(n.tryCalls) > 0 ||
			n.nodeHasOutcomeCall || n.nodeHasPlainSideEffect || n.nodeHasNonCASMutation ||
			n.exitKind == coroSyncFastExitTailCall {
			return true
		}
		if acyclic && n.block != nil {
			end := n.endInstr
			if end < 0 {
				end = len(n.block.Instrs) - 1
			}
			for i := n.startInstr; i < end; i++ {
				switch n.block.Instrs[i].(type) {
				case *ssa.IndexAddr, *ssa.Index, *ssa.Slice, *ssa.TypeAssert, *ssa.MakeInterface:
					return true
				}
			}
		}
		return walk(n.nextNode) || walk(n.trueNode) || walk(n.falseNode)
	}
	return walk(ramp.root)
}

// coroSyncRampFunctionHasSlowWork ensures we only synthesize a sync-fast ramp
// when the function genuinely contains a suspending or cyclic slow path.
func coroSyncRampFunctionHasSlowWork(plan *coroPhysicalFunctionPlan, ir *coroProgramIR) bool {
	if len(cyclicBlocks(plan.function.Blocks)) != 0 {
		return true
	}
	for instr, phys := range plan.instructions {
		if !phys.semantic.evaluated {
			continue
		}
		switch phys.control {
		case coroPhysicalControlDirectAwait,
			coroPhysicalControlDispatchAwait,
			coroPhysicalControlClosedInterfaceAwait,
			coroPhysicalControlManagedInterfaceAwait,
			coroPhysicalControlExactInterfaceAwait,
			coroPhysicalControlDirectSpawn,
			coroPhysicalControlDispatchSpawn:
			return true
		}
		if phys.operation != coroPhysicalOperationNone {
			return true
		}
		if call, ok := instr.(*ssa.Call); ok {
			if frozen, found, err := ir.callSitePlan(call); err == nil && found &&
				frozen.plan.Intrinsic && (frozen.opcode == llgoCoroYield || frozen.opcode == llgoCoroPark) {
				return true
			}
		}
	}
	return false
}

// tailCallTarget checks whether call is the tail call of block (followed only
// by Extract/DebugRef and a Return returning its exact results, or for void
// functions a Jump to a trivial Return block).
func (pl *coroSyncPlanner) tailCallTarget(
	plan *coroPhysicalFunctionPlan,
	block *ssa.BasicBlock,
	callIdx int,
	call *ssa.Call,
) (*ssa.Function, bool) {
	if block == nil || call == nil || call.Common() == nil || call.Common().IsInvoke() {
		return nil, false
	}
	if pl.reflectCheck != nil && pl.reflectCheck(call.Common()) {
		return nil, false
	}
	phys, ok := plan.instructions[call]
	if !ok || phys.control != coroPhysicalControlDirectAwait || phys.controlClosure != nil ||
		phys.controlTarget == nil || phys.controlTarget == plan.function ||
		len(phys.controlTarget.FreeVars) != 0 || phys.controlTarget.Signature == nil ||
		phys.controlTarget.Signature.Variadic() || phys.operation != coroPhysicalOperationNone ||
		phys.outcome != coroPhysicalOutcomeNone || phys.recoverAlias {
		return nil, false
	}
	if raw, ok := call.Common().Value.(*ssa.Function); !ok || raw != phys.controlTarget {
		return nil, false
	}
	if pl.needsRecover != nil && pl.needsRecover(phys.controlTarget) {
		return nil, false
	}
	targetPlan, planned := pl.whole.FunctionPlan(phys.controlTarget)
	if !planned || targetPlan.Emission != coro.EmitCoroutine ||
		targetPlan.ManagedEntry != coro.ManagedEntryCoroutine ||
		targetPlan.Primary != coro.PrimaryCoroutine {
		return nil, false
	}
	fnSig := coroPhysicalNormalizeSourceSignature(plan.function.Signature)
	targetSig := coroPhysicalNormalizeSourceSignature(phys.controlTarget.Signature)
	if fnSig == nil || targetSig == nil {
		return nil, false
	}
	sameResults := types.Identical(fnSig.Results(), targetSig.Results())
	ptrConvertTail := false
	if !sameResults {
		if fnSig.Results().Len() == 1 && targetSig.Results().Len() == 1 &&
			isOpaquePtrType(fnSig.Results().At(0).Type()) &&
			isOpaquePtrType(targetSig.Results().At(0).Type()) {
			ptrConvertTail = true
		} else {
			return nil, false
		}
	}
	resCount := fnSig.Results().Len()
	expected := make([]ssa.Value, 0, resCount)
	cursor := callIdx + 1
	skipDebug := func() {
		for cursor < len(block.Instrs) {
			if _, dbg := block.Instrs[cursor].(*ssa.DebugRef); !dbg {
				break
			}
			cursor++
		}
	}
	switch resCount {
	case 0:
	case 1:
		if ptrConvertTail {
			skipDebug()
			if cursor >= len(block.Instrs) {
				return nil, false
			}
			switch conv := block.Instrs[cursor].(type) {
			case *ssa.Convert:
				if conv.X != call || !types.Identical(conv.Type(), fnSig.Results().At(0).Type()) {
					return nil, false
				}
				expected = append(expected, conv)
				cursor++
			case *ssa.ChangeType:
				if conv.X != call || !types.Identical(conv.Type(), fnSig.Results().At(0).Type()) {
					return nil, false
				}
				expected = append(expected, conv)
				cursor++
			default:
				return nil, false
			}
		} else {
			expected = append(expected, call)
		}
	default:
		for i := 0; i < resCount; i++ {
			skipDebug()
			if cursor >= len(block.Instrs) {
				return nil, false
			}
			ext, ok := block.Instrs[cursor].(*ssa.Extract)
			if !ok || ext.Tuple != call || ext.Index != i {
				return nil, false
			}
			expected = append(expected, ext)
			cursor++
		}
	}
	skipDebug()
	if cursor != len(block.Instrs)-1 {
		return nil, false
	}
	switch term := block.Instrs[cursor].(type) {
	case *ssa.Return:
		if len(term.Results) != len(expected) {
			return nil, false
		}
		for i := range expected {
			if term.Results[i] != expected[i] {
				return nil, false
			}
		}
		return phys.controlTarget, true
	case *ssa.Jump:
		if resCount == 0 && len(block.Succs) == 1 && coroSyncBlockIsTrivialVoidReturn(block.Succs[0]) {
			return phys.controlTarget, true
		}
	}
	return nil, false
}

func coroSyncBlockIsTrivialVoidReturn(blk *ssa.BasicBlock) bool {
	for hops := 0; hops < 4 && blk != nil && len(blk.Instrs) > 0; hops++ {
		for i, ins := range blk.Instrs {
			if i == len(blk.Instrs)-1 {
				switch term := ins.(type) {
				case *ssa.Return:
					return len(term.Results) == 0
				case *ssa.Jump:
					if len(blk.Succs) != 1 {
						return false
					}
					blk = blk.Succs[0]
				default:
					return false
				}
			} else if _, dbg := ins.(*ssa.DebugRef); !dbg {
				return false
			}
		}
	}
	return false
}

// -----------------------------------------------------------------------------
// Instruction & Callee Predicates
// -----------------------------------------------------------------------------

func coroSyncFastPureInstrAllowed(
	plan *coroPhysicalFunctionPlan,
	instr ssa.Instruction,
	phys coroPhysicalInstructionPlan,
	allowAfterMutation bool,
	allowDynamicSlice bool,
	zeroInts map[ssa.Value]bool,
	nilIfaces map[ssa.Value]bool,
) (mayMutate bool, needsGuardBail bool, ok bool) {
	if _, isRunDefers := instr.(*ssa.RunDefers); isRunDefers {
		// planDirectFastCFG never executes an ssa.Defer on the fast prefix, so
		// any RunDefers reached on a fast return path is a guaranteed no-op.
		return false, false, phys.semantic.evaluated && phys.control == coroPhysicalControlNone &&
			phys.operation == coroPhysicalOperationNone &&
			(phys.outcome == coroPhysicalOutcomeNone || phys.outcome == coroPhysicalOutcomeRunDefers)
	}
	if !phys.semantic.evaluated || phys.elideValue ||
		phys.control != coroPhysicalControlNone ||
		phys.operation != coroPhysicalOperationNone ||
		phys.outcome != coroPhysicalOutcomeNone {
		return false, false, false
	}
	switch v := instr.(type) {
	case *ssa.DebugRef, *ssa.Phi:
		return false, false, true
	case *ssa.Extract, *ssa.Field, *ssa.ChangeType:
		return false, false, phys.recipe == coroPhysicalInstructionOrdinary
	case *ssa.FieldAddr:
		if phys.recipe == coroPhysicalInstructionOrdinary {
			return false, false, true
		}
		return false, phys.nilGuard, phys.recipe == coroPhysicalInstructionFieldAddr
	case *ssa.IndexAddr:
		if phys.recipe != coroPhysicalInstructionIndexAddr && phys.recipe != coroPhysicalInstructionOrdinary {
			return false, false, false
		}
		if phys.container != coroPhysicalContainerSlice && phys.container != coroPhysicalContainerArrayPointer {
			return false, false, false
		}
		return false, phys.nilGuard || phys.boundsGuard, true
	case *ssa.Index:
		if phys.recipe != coroPhysicalInstructionIndex && phys.recipe != coroPhysicalInstructionOrdinary {
			return false, false, false
		}
		if phys.container != coroPhysicalContainerString &&
			phys.container != coroPhysicalContainerSlice &&
			phys.container != coroPhysicalContainerArrayPointer {
			return false, false, false
		}
		return false, phys.nilGuard || phys.boundsGuard, true
	case *ssa.BinOp:
		if phys.recipe == coroPhysicalInstructionInterfaceNilCompare {
			return false, false, phys.valueOperand != nil
		}
		if phys.recipe != coroPhysicalInstructionOrdinary {
			return false, false, false
		}
		// Reject string BinOps (StringEqual/StringCat/StringLess call runtime helpers).
		if b, isBasic := types.Unalias(v.X.Type()).Underlying().(*types.Basic); isBasic && b.Kind() == types.String {
			return false, false, false
		}
		if v.Op == token.EQL || v.Op == token.NEQ {
			switch types.Unalias(v.X.Type()).Underlying().(type) {
			case *types.Basic, *types.Pointer, *types.Chan:
			case *types.Interface:
				if !coroSyncIsConcreteInterfaceType(v.X.Type()) || !coroSyncIsConcreteInterfaceType(v.Y.Type()) {
					return false, false, false
				}
				if coroSyncIsNilInterfaceValue(v.X, nilIfaces) || coroSyncIsNilInterfaceValue(v.Y, nilIfaces) {
					return false, false, true
				}
				if coroSyncIsDirectPtrEmptyIfaceCompare(v.X, v.Y) {
					return false, false, true
				}
				// Dynamic non-nil interface comparison (e.g. err == io.ErrUnexpectedEOF):
				// fast-path when either operand is nil at runtime and bail cleanly when
				// both are non-nil so EfaceEqual/IfaceEqual runs in $coro.slow.
				return false, true, true
			default:
				return false, false, false
			}
		}
		if (v.Op == token.QUO || v.Op == token.REM) && !ssaIntegerValueProvenNonZeroAt(v.Y, v) {
			return false, false, false
		}
		if (v.Op == token.SHL || v.Op == token.SHR) && !ssaIntegerValueProvenNonNegativeAt(v.Y, v) {
			return false, false, false
		}
		return false, false, true
	case *ssa.UnOp:
		if v.Op == token.ARROW {
			return false, false, false
		}
		if v.Op == token.MUL {
			if phys.recipe == coroPhysicalInstructionOrdinary {
				return false, false, true
			}
			return false, phys.nilGuard, phys.recipe == coroPhysicalInstructionDeref
		}
		return false, false, phys.recipe == coroPhysicalInstructionOrdinary
	case *ssa.Convert:
		if phys.recipe != coroPhysicalInstructionOrdinary {
			return false, false, false
		}
		src := types.Unalias(v.X.Type()).Underlying()
		dst := types.Unalias(v.Type()).Underlying()
		if sb, ok1 := src.(*types.Basic); ok1 {
			if db, ok2 := dst.(*types.Basic); ok2 && sb.Kind() != types.String && db.Kind() != types.String {
				return false, false, true
			}
		}
		return false, false, isSimplePtrOrUnsafe(src) && isSimplePtrOrUnsafe(dst)
	case *ssa.MakeInterface:
		if phys.recipe != coroPhysicalInstructionOrdinary {
			return false, false, false
		}
		iface, isIface := types.Unalias(v.Type()).Underlying().(*types.Interface)
		if !isIface {
			return false, false, false
		}
		if isUntypedNilConst(v.X) {
			return false, false, true
		}
		if !isDirectPointerIfaceValue(v.X.Type()) {
			return false, false, false
		}
		if !iface.Empty() && !llssa.CanBuildStaticItab(iface, v.X.Type()) {
			return false, false, false
		}
		return false, false, true
	case *ssa.TypeAssert:
		if phys.recipe != coroPhysicalInstructionOrdinary {
			return false, false, false
		}
		srcIface, isIface := types.Unalias(v.X.Type()).Underlying().(*types.Interface)
		if !isIface || !srcIface.Empty() {
			return false, false, false
		}
		if !isDirectPointerIfaceValue(v.AssertedType) {
			return false, false, false
		}
		return false, !v.CommaOk, true
	case *ssa.Slice:
		// Accept identity reslicing x[0:] everywhere, and bounds-checked
		// two-index slicing x[low:high] on slices and strings (bailing cleanly
		// on out-of-range bounds before mutation or in SingleAwait post-call).
		if phys.recipe != coroPhysicalInstructionOrdinary && phys.recipe != coroPhysicalInstructionSlice {
			return false, false, false
		}
		if v.Max != nil {
			return false, false, false
		}
		isSliceBase := false
		switch base := types.Unalias(v.X.Type()).Underlying().(type) {
		case *types.Slice:
			isSliceBase = true
		case *types.Basic:
			isSliceBase = base.Kind() == types.String
		}
		if !isSliceBase {
			return false, false, false
		}
		isZeroLow := v.Low == nil || coroSyncIsZeroIntConst(v.Low) || zeroInts[v.Low]
		if isZeroLow && v.High == nil {
			return false, false, true
		}
		for _, bound := range []ssa.Value{v.Low, v.High} {
			if bound == nil {
				continue
			}
			if basic, isBasic := types.Unalias(bound.Type()).Underlying().(*types.Basic); !isBasic || basic.Info()&types.IsInteger == 0 {
				return false, false, false
			}
		}
		if allowDynamicSlice {
			return false, false, true
		}
		return false, true, true
	case *ssa.Alloc:
		if v.Heap {
			return false, false, false
		}
		return false, false, phys.recipe == coroPhysicalInstructionOrdinary ||
			phys.recipe == coroPhysicalInstructionFrameAllocation ||
			phys.recipe == coroPhysicalInstructionFrameBitcastAllocation
	case *ssa.Store:
		if phys.recipe != coroPhysicalInstructionOrdinary && phys.recipe != coroPhysicalInstructionStore {
			return false, false, false
		}
		if ssaAddrStaysInLocalAlloc(v.Addr, plan.function) {
			return false, false, true
		}
		if allowAfterMutation && !phys.nilGuard {
			return true, false, true
		}
		return false, false, false
	default:
		return false, false, false
	}
}

func coroSyncIsConcreteInterfaceType(t types.Type) bool {
	if t == nil {
		return false
	}
	u := types.Unalias(t)
	if _, isTP := u.(*types.TypeParam); isTP {
		return false
	}
	_, isIface := u.Underlying().(*types.Interface)
	return isIface
}

func coroSyncIsNilInterfaceValue(v ssa.Value, nilIfaces map[ssa.Value]bool) bool {
	if v == nil {
		return false
	}
	if nilIfaces != nil && nilIfaces[v] {
		return true
	}
	if c, ok := v.(*ssa.Const); ok && c.IsNil() {
		return true
	}
	return false
}

// coroSyncIsDirectPtrEmptyIfaceCompare reports whether an interface equality
// compares two empty interfaces where at least one operand is directly boxed
// from a pointer/chan/unsafe.Pointer (all comparable, never panicking, and
// stored directly in the interface's two words without heap indirection).
func coroSyncIsDirectPtrEmptyIfaceCompare(x, y ssa.Value) bool {
	if x == nil || y == nil {
		return false
	}
	ix, okX := types.Unalias(x.Type()).Underlying().(*types.Interface)
	iy, okY := types.Unalias(y.Type()).Underlying().(*types.Interface)
	if !okX || !okY || !ix.Empty() || !iy.Empty() {
		return false
	}
	return coroSyncIsDirectPtrMakeInterface(x) || coroSyncIsDirectPtrMakeInterface(y)
}

func coroSyncIsDirectPtrMakeInterface(v ssa.Value) bool {
	mk, ok := v.(*ssa.MakeInterface)
	return ok && !isUntypedNilConst(mk.X) && isDirectPointerIfaceValue(mk.X.Type())
}

func isSimplePtrOrUnsafe(t types.Type) bool {
	switch u := t.(type) {
	case *types.Pointer:
		return true
	case *types.Basic:
		return u.Kind() == types.UnsafePointer || u.Kind() == types.Uintptr
	default:
		return false
	}
}

func isOpaquePtrType(t types.Type) bool {
	if t == nil {
		return false
	}
	switch u := types.Unalias(t).Underlying().(type) {
	case *types.Pointer:
		return true
	case *types.Basic:
		return u.Kind() == types.UnsafePointer
	default:
		return false
	}
}

func isDirectPointerIfaceValue(t types.Type) bool {
	if t == nil {
		return false
	}
	switch u := types.Unalias(t).Underlying().(type) {
	case *types.Pointer, *types.Chan:
		return true
	case *types.Basic:
		return u.Kind() == types.UnsafePointer
	default:
		return false
	}
}

func isPureCopyBuiltin(call *ssa.Call) bool {
	if call == nil || call.Common() == nil || call.Common().IsInvoke() || len(call.Call.Args) != 2 {
		return false
	}
	b, ok := call.Common().Value.(*ssa.Builtin)
	if !ok || b.Name() != "copy" {
		return false
	}
	dst, ok := types.Unalias(call.Call.Args[0].Type()).Underlying().(*types.Slice)
	if !ok {
		return false
	}
	switch src := types.Unalias(call.Call.Args[1].Type()).Underlying().(type) {
	case *types.Slice:
		return types.Identical(dst.Elem(), src.Elem())
	case *types.Basic:
		return src.Kind() == types.String && types.Identical(types.Unalias(dst.Elem()), types.Typ[types.Byte])
	default:
		return false
	}
}

// coroSyncOutcomeCalleeIsReadOnly proves that a small direct $outcome callee
// performs no heap/shared-memory writes (only reads, read-only atomics, pure
// helper calls, and stores to its own non-heap stack locals), so calling it
// before a fast-path bailout leaves program state unchanged.
func (pl *coroSyncPlanner) coroSyncOutcomeCalleeIsReadOnly(callee *ssa.Function) bool {
	return pl.coroSyncOutcomeCalleeIsReadOnlyDepth(callee, 0)
}

func (pl *coroSyncPlanner) coroSyncOutcomeCalleeIsReadOnlyNoExplicitPanic(callee *ssa.Function) bool {
	if !pl.coroSyncOutcomeCalleeIsReadOnly(callee) {
		return false
	}
	callee = pl.canonicalFunc(callee)
	if callee == nil {
		return false
	}
	for _, blk := range callee.Blocks {
		for _, ins := range blk.Instrs {
			if _, isPanic := ins.(*ssa.Panic); isPanic {
				return false
			}
		}
	}
	return true
}

func (pl *coroSyncPlanner) coroSyncOutcomeCalleeIsReadOnlyDepth(callee *ssa.Function, depth int) bool {
	if depth > 4 {
		return false
	}
	callee = pl.canonicalFunc(callee)
	if callee == nil || len(callee.Blocks) == 0 || len(callee.Blocks) > 8 || len(callee.FreeVars) != 0 {
		return false
	}
	for _, blk := range callee.Blocks {
		for _, ins := range blk.Instrs {
			switch v := ins.(type) {
			case *ssa.DebugRef, *ssa.Phi, *ssa.BinOp, *ssa.UnOp, *ssa.Field, *ssa.FieldAddr,
				*ssa.Index, *ssa.IndexAddr, *ssa.Extract, *ssa.Convert, *ssa.ChangeType,
				*ssa.MakeInterface, *ssa.Slice, *ssa.If, *ssa.Jump, *ssa.Return, *ssa.Panic:
				if un, isUn := v.(*ssa.UnOp); isUn && un.Op == token.ARROW {
					return false
				}
			case *ssa.Alloc:
				// Any fresh allocation owned by callee cannot alias caller-visible
				// memory; we verify below that stores never target external memory.
				continue
			case *ssa.Store:
				if !ssaAddrStaysInOwnedAlloc(v.Addr, callee) {
					return false
				}
			case *ssa.Call:
				if isPureLenCapBuiltin(v) {
					continue
				}
				if frozen, found, err := pl.ir.callSitePlan(v); err == nil && found &&
					frozen.plan.Intrinsic && coroSyncIsReadOnlyAtomicOpcode(frozen.opcode) {
					continue
				}
				if !v.Common().IsInvoke() {
					if nested := pl.canonicalFunc(v.Common().StaticCallee()); nested != nil && nested != callee {
						if coroSyncPlainHelperIsPure(nested, pl.whole) ||
							pl.coroSyncOutcomeCalleeIsReadOnlyDepth(nested, depth+1) {
							continue
						}
					}
				}
				return false
			default:
				return false
			}
		}
	}
	return true
}

func ssaAddrStaysInLocalAlloc(addr ssa.Value, fn *ssa.Function) bool {
	return ssaAddrStaysInAlloc(addr, fn, false)
}

func ssaAddrStaysInOwnedAlloc(addr ssa.Value, fn *ssa.Function) bool {
	return ssaAddrStaysInAlloc(addr, fn, true)
}

func ssaAddrStaysInAlloc(addr ssa.Value, fn *ssa.Function, allowHeap bool) bool {
	for depth := 0; depth < 8 && addr != nil; depth++ {
		switch a := addr.(type) {
		case *ssa.Alloc:
			return (allowHeap || !a.Heap) && a.Parent() == fn
		case *ssa.FieldAddr:
			addr = a.X
		case *ssa.IndexAddr:
			addr = a.X
		case *ssa.Convert:
			addr = a.X
		case *ssa.ChangeType:
			addr = a.X
		default:
			return false
		}
	}
	return false
}

func isPureLenCapBuiltin(call *ssa.Call) bool {
	if call == nil || call.Common() == nil || call.Common().IsInvoke() || len(call.Call.Args) != 1 {
		return false
	}
	b, ok := call.Common().Value.(*ssa.Builtin)
	if !ok {
		return false
	}
	argType := types.Unalias(call.Call.Args[0].Type()).Underlying()
	switch b.Name() {
	case "len":
		if _, isSlice := argType.(*types.Slice); isSlice {
			return true
		}
		if basic, isBasic := argType.(*types.Basic); isBasic && basic.Kind() == types.String {
			return true
		}
		return false
	case "cap":
		_, isSlice := argType.(*types.Slice)
		return isSlice
	default:
		return false
	}
}

func isRuntimeKeepAliveCall(call *ssa.Call) bool {
	if call == nil || call.Common() == nil || call.Common().IsInvoke() {
		return false
	}
	callee := call.Common().StaticCallee()
	return callee != nil && callee.Pkg != nil && callee.Pkg.Pkg != nil &&
		callee.Pkg.Pkg.Path() == "runtime" && callee.Name() == "KeepAlive"
}

func coroSyncIsCASAtomicOpcode(opcode int) bool {
	return opcode == llgoAtomicCmpXchg || opcode == llgoAtomicCmpXchgOK
}

func coroSyncIsReadOnlyAtomicOpcode(opcode int) bool {
	return opcode == llgoAtomicLoad || opcode == llgoAtomicLoadUnsafe
}

func (pl *coroSyncPlanner) isDirectOutcomeCall(call *ssa.Call, phys coroPhysicalInstructionPlan) bool {
	if phys.control != coroPhysicalControlDirectOutcome || !phys.directOutcomeNativeResult ||
		phys.controlReceiver != nil || phys.controlTarget == nil ||
		len(phys.controlTarget.FreeVars) != 0 || phys.recoverAlias ||
		(pl.reflectCheck != nil && pl.reflectCheck(call.Common())) {
		return false
	}
	raw, ok := call.Common().Value.(*ssa.Function)
	if !ok {
		return false
	}
	// Resolve //go:linkname declarations to their canonical body before
	// comparing targets or rejecting noreturn trap helpers (e.g. fatal/throw).
	canonical := pl.canonicalFunc(phys.controlTarget)
	if canonical == nil || pl.canonicalFunc(raw) != canonical {
		return false
	}
	for _, blk := range canonical.Blocks {
		if len(blk.Instrs) > 0 {
			if _, isRet := blk.Instrs[len(blk.Instrs)-1].(*ssa.Return); isRet {
				return true
			}
		}
	}
	return false
}

func coroSyncPhiEdgeIndex(phi *ssa.Phi, pred *ssa.BasicBlock) int {
	if phi == nil || pred == nil {
		return -1
	}
	for i, p := range phi.Block().Preds {
		if p == pred {
			return i
		}
	}
	return -1
}

func coroSyncMatchCASBranch(block *ssa.BasicBlock, casCall *ssa.Call, casOpcode int) (ssa.Value, bool) {
	if block == nil || casCall == nil || len(block.Instrs) == 0 {
		return nil, false
	}
	ifInstr, isIf := block.Instrs[len(block.Instrs)-1].(*ssa.If)
	if !isIf {
		return nil, false
	}
	if casOpcode == llgoAtomicCmpXchgOK && ifInstr.Cond == casCall {
		return casCall, true
	}
	if casOpcode == llgoAtomicCmpXchg {
		if ext, isExt := ifInstr.Cond.(*ssa.Extract); isExt && ext.Tuple == casCall && ext.Index == 1 {
			return ext, true
		}
	}
	return nil, false
}

// matchInlineAtomicWrapper recognizes any single-block direct $outcome atomic
// method wrapper (e.g. func (x *T) Op(args...) R { return atomicOp(&x.v, args...) }).
func (pl *coroSyncPlanner) matchInlineAtomicWrapper(callee *ssa.Function) (coroSyncInlineAtomic, bool) {
	callee = pl.canonicalFunc(callee)
	if callee == nil || len(callee.Blocks) != 1 || len(callee.FreeVars) != 0 || len(callee.Params) == 0 {
		return coroSyncInlineAtomic{}, false
	}
	if _, isPtr := types.Unalias(callee.Params[0].Type()).Underlying().(*types.Pointer); !isPtr {
		return coroSyncInlineAtomic{}, false
	}
	blk := callee.Blocks[0]
	fieldIdx := -1
	var addrVal ssa.Value = callee.Params[0]
	var atomicCall *ssa.Call
	atomicOpcode := 0
	for i, ins := range blk.Instrs {
		if i == len(blk.Instrs)-1 {
			ret, isRet := ins.(*ssa.Return)
			if !isRet || atomicCall == nil {
				return coroSyncInlineAtomic{}, false
			}
			if len(ret.Results) == 0 {
				return coroSyncInlineAtomic{fieldIndex: fieldIdx, opcode: atomicOpcode}, true
			}
			if len(ret.Results) == 1 && ret.Results[0] == atomicCall {
				return coroSyncInlineAtomic{fieldIndex: fieldIdx, opcode: atomicOpcode}, true
			}
			return coroSyncInlineAtomic{}, false
		}
		switch v := ins.(type) {
		case *ssa.DebugRef:
		case *ssa.FieldAddr:
			if fieldIdx >= 0 || atomicCall != nil || v.X != callee.Params[0] {
				return coroSyncInlineAtomic{}, false
			}
			fieldIdx = v.Field
			addrVal = v
		case *ssa.Call:
			if atomicCall != nil || v.Common() == nil || v.Common().IsInvoke() {
				return coroSyncInlineAtomic{}, false
			}
			frozen, found, err := pl.ir.callSitePlan(v)
			if err != nil || !found || !frozen.plan.Intrinsic || !isCoroAtomicIntrinsic(frozen.opcode) {
				return coroSyncInlineAtomic{}, false
			}
			if len(v.Call.Args) != len(callee.Params) || v.Call.Args[0] != addrVal {
				return coroSyncInlineAtomic{}, false
			}
			for k := 1; k < len(callee.Params); k++ {
				if v.Call.Args[k] != callee.Params[k] {
					return coroSyncInlineAtomic{}, false
				}
			}
			atomicCall = v
			atomicOpcode = frozen.opcode
		default:
			return coroSyncInlineAtomic{}, false
		}
	}
	return coroSyncInlineAtomic{}, false
}

// evalPureConstBoolCall proves whether a zero-argument bool Go call evaluates
// to a compile-time constant using only frozen canonical bindings and program
// predicate intrinsics (such as ExecutionDomainEnabled). When the predicate is
// enabled (non-constant), it returns (false, false) so the call remains dynamic.
func (pl *coroSyncPlanner) evalPureConstBoolCall(call *ssa.Call) (bool, bool) {
	if call == nil || call.Common() == nil || call.Common().IsInvoke() || len(call.Common().Args) != 0 {
		return false, false
	}
	target := call.Common().StaticCallee()
	if target == nil || target.Signature == nil || target.Signature.Params().Len() != 0 ||
		target.Signature.Results().Len() != 1 || !types.Identical(target.Signature.Results().At(0).Type(), types.Typ[types.Bool]) {
		return false, false
	}
	c, ok := pl.evalPureConstFunc(target, make(map[*ssa.Function]bool))
	if !ok || c == nil || c.Value == nil || c.Value.Kind() != constant.Bool {
		return false, false
	}
	return constant.BoolVal(c.Value), true
}

func (pl *coroSyncPlanner) evalPureConstFunc(fn *ssa.Function, stack map[*ssa.Function]bool) (*ssa.Const, bool) {
	if fn == nil || len(stack) >= 8 {
		return nil, false
	}
	fn = pl.canonicalFunc(fn)
	if stack[fn] || fn.Signature == nil || fn.Signature.Params().Len() != 0 || len(fn.FreeVars) != 0 {
		return nil, false
	}
	class, frozen := pl.ir.callableClassifications[fn]
	if !frozen {
		return nil, false
	}
	if class.intrinsic {
		pred := coroProgramPredicateCapability(class.opcode)
		b := pl.ir.programPredicates
		if pred != 0 && b.Valid() && b.Mask&pred != 0 {
			return ssa.NewConst(constant.MakeBool(b.Values&pred != 0), types.Typ[types.Bool]), true
		}
		return nil, false
	}
	if !class.classified || class.background != llssa.InGo || len(fn.Blocks) == 0 {
		return nil, false
	}
	stack[fn] = true
	defer delete(stack, fn)

	values := make(map[ssa.Value]*ssa.Const)
	get := func(v ssa.Value) *ssa.Const {
		if c, ok := v.(*ssa.Const); ok {
			return c
		}
		return values[v]
	}
	isNilPtrType := func(t types.Type) bool {
		switch u := types.Unalias(t).Underlying().(type) {
		case *types.Pointer:
			return true
		case *types.Basic:
			return u.Kind() == types.UnsafePointer
		default:
			return false
		}
	}
	blk := fn.Blocks[0]
	var pred *ssa.BasicBlock
	visited := make(map[*ssa.BasicBlock]bool)
	for blk != nil && !visited[blk] {
		visited[blk] = true
		var next *ssa.BasicBlock
		for _, ins := range blk.Instrs {
			switch v := ins.(type) {
			case *ssa.DebugRef:
			case *ssa.Call:
				if v.Common().IsInvoke() || len(v.Common().Args) != 0 {
					return nil, false
				}
				res, ok := pl.evalPureConstFunc(v.Common().StaticCallee(), stack)
				if !ok {
					return nil, false
				}
				values[v] = res
			case *ssa.UnOp:
				op := get(v.X)
				if v.Op != token.NOT || op == nil || op.Value == nil || op.Value.Kind() != constant.Bool {
					return nil, false
				}
				values[v] = ssa.NewConst(constant.MakeBool(!constant.BoolVal(op.Value)), types.Typ[types.Bool])
			case *ssa.BinOp:
				l, r := get(v.X), get(v.Y)
				if l == nil || r == nil || (v.Op != token.EQL && v.Op != token.NEQ) {
					return nil, false
				}
				var eq bool
				if l.IsNil() || r.IsNil() {
					if !l.IsNil() || !r.IsNil() {
						return nil, false
					}
					eq = true
				} else {
					eq = constant.Compare(l.Value, token.EQL, r.Value)
				}
				if v.Op == token.NEQ {
					eq = !eq
				}
				values[v] = ssa.NewConst(constant.MakeBool(eq), types.Typ[types.Bool])
			case *ssa.Convert:
				op := get(v.X)
				if op == nil || !op.IsNil() || !isNilPtrType(v.Type()) {
					return nil, false
				}
				values[v] = ssa.NewConst(nil, v.Type())
			case *ssa.ChangeType:
				op := get(v.X)
				if op == nil || !op.IsNil() || !isNilPtrType(v.Type()) {
					return nil, false
				}
				values[v] = ssa.NewConst(nil, v.Type())
			case *ssa.Phi:
				idx := coroSyncPhiEdgeIndex(v, pred)
				if idx < 0 {
					return nil, false
				}
				sel := get(v.Edges[idx])
				if sel == nil {
					return nil, false
				}
				values[v] = sel
			case *ssa.If:
				c := get(v.Cond)
				if c == nil || c.Value == nil || c.Value.Kind() != constant.Bool || len(blk.Succs) != 2 {
					return nil, false
				}
				if constant.BoolVal(c.Value) {
					next = blk.Succs[0]
				} else {
					next = blk.Succs[1]
				}
			case *ssa.Jump:
				if len(blk.Succs) != 1 {
					return nil, false
				}
				next = blk.Succs[0]
			case *ssa.Return:
				if len(v.Results) != 1 {
					return nil, false
				}
				res := get(v.Results[0])
				return res, res != nil
			default:
				return nil, false
			}
		}
		pred, blk = blk, next
	}
	return nil, false
}

func (pl *coroSyncPlanner) plainGoCallAllowed(call *ssa.Call, phys coroPhysicalInstructionPlan) bool {
	if phys.control != coroPhysicalControlNone || phys.operation != coroPhysicalOperationNone ||
		phys.controlFailureHard || phys.outcome != coroPhysicalOutcomeNone ||
		call.Common().IsInvoke() || call.Common().StaticCallee() == nil {
		return false
	}
	if pl.reflectCheck != nil && pl.reflectCheck(call.Common()) {
		return false
	}
	if isRuntimeKeepAliveCall(call) {
		return true
	}
	if frozen, found, err := pl.ir.callSitePlan(call); err == nil && found && frozen.plan.Elision != CoroCallNotElided {
		return false
	}
	callee := pl.canonicalFunc(call.Common().StaticCallee())
	calleePlan, ok := pl.whole.FunctionPlan(callee)
	class := pl.ir.callableClassifications[callee]
	if !ok || calleePlan.Emission != coro.EmitPlain || calleePlan.Primary != coro.PrimaryPlain ||
		!class.classified || class.background != llssa.InGo || class.intrinsic ||
		len(callee.FreeVars) != 0 || callee.Signature.Variadic() {
		return false
	}
	if calleePlan.External == coro.Defined && pl.whole.IgnoresBody(callee) {
		return false
	}
	if calleePlan.External != coro.Defined && calleePlan.External != coro.ExternalKnown {
		return false
	}
	return !calleePlan.Exec.Contains(coro.MayUnwind | coro.OpaqueExec | coro.BlockForeign)
}

// -----------------------------------------------------------------------------
// Path-Sensitive Branch Facts & Static Branch Resolution
// -----------------------------------------------------------------------------

func cloneMap[K comparable, V any](src map[K]V) map[K]V {
	dst := make(map[K]V, len(src)+1)
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func coroSyncExprKey(v ssa.Value) string {
	return coroSyncExprKeyDepth(v, 0)
}

func coroSyncExprKeyDepth(v ssa.Value, depth int) string {
	if v == nil || depth > 8 {
		return ""
	}
	switch x := v.(type) {
	case *ssa.Const:
		return fmt.Sprintf("C(%s)", x.String())
	case *ssa.BinOp:
		lk, rk := coroSyncExprKeyDepth(x.X, depth+1), coroSyncExprKeyDepth(x.Y, depth+1)
		if lk == "" || rk == "" {
			return fmt.Sprintf("V(%p)", v)
		}
		op := x.Op
		switch op {
		case token.ADD, token.MUL, token.AND, token.OR, token.XOR, token.EQL, token.NEQ:
			if lk > rk {
				lk, rk = rk, lk
			}
		case token.GTR:
			op, lk, rk = token.LSS, rk, lk
		case token.GEQ:
			op, lk, rk = token.LEQ, rk, lk
		}
		return fmt.Sprintf("B(%d,%s,%s)", op, lk, rk)
	case *ssa.UnOp:
		if x.Op == token.MUL || x.Op == token.ARROW {
			return fmt.Sprintf("V(%p)", v)
		}
		if xk := coroSyncExprKeyDepth(x.X, depth+1); xk != "" {
			return fmt.Sprintf("U(%d,%s)", x.Op, xk)
		}
	case *ssa.Convert:
		if xk := coroSyncExprKeyDepth(x.X, depth+1); xk != "" {
			return fmt.Sprintf("K(%s,%s)", x.Type().String(), xk)
		}
	case *ssa.ChangeType:
		if xk := coroSyncExprKeyDepth(x.X, depth+1); xk != "" {
			return fmt.Sprintf("T(%s,%s)", x.Type().String(), xk)
		}
	}
	return fmt.Sprintf("V(%p)", v)
}

func coroSyncCloneKnown(src map[string]bool, cond ssa.Value, val bool) map[string]bool {
	dst := cloneMap(src)
	if cond == nil {
		return dst
	}
	if key := coroSyncExprKey(cond); key != "" {
		dst[key] = val
	}
	if bin, ok := cond.(*ssa.BinOp); ok {
		lk, rk := coroSyncExprKeyDepth(bin.X, 1), coroSyncExprKeyDepth(bin.Y, 1)
		if lk != "" && rk != "" {
			var invOp token.Token
			il, ir := lk, rk
			switch bin.Op {
			case token.EQL:
				invOp = token.NEQ
				if il > ir {
					il, ir = ir, il
				}
			case token.NEQ:
				invOp = token.EQL
				if il > ir {
					il, ir = ir, il
				}
			case token.LSS:
				invOp, il, ir = token.LEQ, rk, lk
			case token.LEQ:
				invOp, il, ir = token.LSS, rk, lk
			case token.GTR:
				invOp = token.LEQ
			case token.GEQ:
				invOp = token.LSS
			}
			if invOp != 0 {
				dst[fmt.Sprintf("B(%d,%s,%s)", invOp, il, ir)] = !val
			}
		}
	}
	if un, ok := cond.(*ssa.UnOp); ok && un.Op == token.NOT {
		if inner := coroSyncExprKey(un.X); inner != "" {
			dst[inner] = !val
		}
	}
	return dst
}

func coroSyncStaticBranch(term *ssa.If, ir *coroProgramIR, known map[string]bool) (bool, bool) {
	if val, ok := ir.constantBranches[term]; ok {
		return val, true
	}
	if c, ok := term.Cond.(*ssa.Const); ok && c.Value != nil && c.Value.Kind() == constant.Bool {
		return constant.BoolVal(c.Value), true
	}
	if val, ok := known[coroSyncExprKey(term.Cond)]; ok {
		return val, true
	}
	if un, ok := term.Cond.(*ssa.UnOp); ok && un.Op == token.NOT {
		if val, ok := known[coroSyncExprKey(un.X)]; ok {
			return !val, true
		}
	}
	return false, false
}

// -----------------------------------------------------------------------------
// Strategy 1: CallerInlineYield (tiny acyclic void functions with coroYield)
// -----------------------------------------------------------------------------

func (pl *coroSyncPlanner) planCallerInlineYield(
	plan *coroPhysicalFunctionPlan,
	cyclic map[*ssa.BasicBlock]bool,
) *coroPhysicalSyncRampPlan {
	fn := plan.function
	if len(cyclic) != 0 || fn.Signature.Results().Len() != 0 {
		return nil
	}
	totalInstrs, yieldCount := 0, 0
	for _, blk := range fn.Blocks {
		if !plan.reachableBlocks[blk] {
			continue
		}
		totalInstrs += len(blk.Instrs)
		for _, ins := range blk.Instrs {
			if call, ok := ins.(*ssa.Call); ok {
				if frozen, found, err := pl.ir.callSitePlan(call); err == nil && found &&
					frozen.plan.Intrinsic && frozen.opcode == llgoCoroYield {
					yieldCount++
				}
			}
		}
	}
	if yieldCount == 0 || yieldCount > 2 || totalInstrs > coroSyncRampInlineMaxInstrs {
		return nil
	}

	nodesUsed := 0
	hasReturn := false
	var build func(blk, pred *ssa.BasicBlock, depth int) *coroSyncFastNode
	build = func(blk, pred *ssa.BasicBlock, depth int) *coroSyncFastNode {
		if blk == nil || !plan.reachableBlocks[blk] || depth > coroSyncRampMaxDepth || nodesUsed >= coroSyncRampMaxNodes {
			return nil
		}
		nodesUsed++
		node := &coroSyncFastNode{block: blk, predBlock: pred, endInstr: -1, yieldCalls: make(map[*ssa.Call]bool)}
		for i, ins := range blk.Instrs {
			if i == len(blk.Instrs)-1 {
				switch term := ins.(type) {
				case *ssa.Return:
					if len(term.Results) != 0 {
						return nil
					}
					node.exitKind, node.retInstr = coroSyncFastExitReturn, term
					hasReturn = true
					return node
				case *ssa.Jump:
					next := build(blk.Succs[0], blk, depth+1)
					if next == nil {
						return nil
					}
					node.exitKind, node.nextNode = coroSyncFastExitJump, next
					return node
				case *ssa.If:
					if val, ok := coroSyncStaticBranch(term, pl.ir, nil); ok {
						succ := blk.Succs[1]
						if val {
							succ = blk.Succs[0]
						}
						next := build(succ, blk, depth+1)
						if next == nil {
							return nil
						}
						node.exitKind, node.nextNode = coroSyncFastExitJump, next
						return node
					}
					tNode, fNode := build(blk.Succs[0], blk, depth+1), build(blk.Succs[1], blk, depth+1)
					if tNode == nil || fNode == nil {
						return nil
					}
					node.exitKind, node.cond, node.trueNode, node.falseNode = coroSyncFastExitIf, term.Cond, tNode, fNode
					return node
				default:
					return nil
				}
			}
			phys := plan.instructions[ins]
			if call, isCall := ins.(*ssa.Call); isCall {
				if frozen, found, err := pl.ir.callSitePlan(call); err == nil && found &&
					frozen.plan.Intrinsic && frozen.opcode == llgoCoroYield {
					node.yieldCalls[call] = true
					continue
				}
				if pl.isDirectOutcomeCall(call, phys) || isPureLenCapBuiltin(call) || pl.plainGoCallAllowed(call, phys) {
					continue
				}
				if target, isTail := pl.tailCallTarget(plan, blk, i, call); isTail {
					node.endInstr, node.exitKind, node.tailCall, node.tailTarget = i, coroSyncFastExitTailCall, call, target
					return node
				}
				return nil
			}
			if _, isAlloc := ins.(*ssa.Alloc); isAlloc {
				return nil
			}
			if _, isStore := ins.(*ssa.Store); isStore {
				return nil
			}
			if _, guardBail, ok := coroSyncFastPureInstrAllowed(plan, ins, phys, false, false, nil, nil); !ok || guardBail {
				return nil
			}
		}
		return nil
	}

	root := build(fn.Blocks[0], nil, 0)
	if root == nil || !hasReturn {
		return nil
	}
	return &coroPhysicalSyncRampPlan{kind: coroSyncRampCallerInlineYield, root: root}
}

// -----------------------------------------------------------------------------
// Strategy 2: DirectFastCFG (TryBailout / TailSlow / ResumeSlow)
// -----------------------------------------------------------------------------

func (pl *coroSyncPlanner) planDirectFastCFG(
	plan *coroPhysicalFunctionPlan,
	cyclic map[*ssa.BasicBlock]bool,
	recursiveCaller bool,
) *coroPhysicalSyncRampPlan {
	fn := plan.function
	nodesUsed, instrsUsed := 0, 0

	canResumeAtBlock := func(blk *ssa.BasicBlock) bool {
		return blk != nil && plan.reachableBlocks[blk] && (len(blk.Instrs) == 0 || !isPhi(blk.Instrs[0]))
	}

	var explore func(
		blk, pred *ssa.BasicBlock,
		mutated bool,
		depth, outcomeCalls int,
		visited map[*ssa.BasicBlock]bool,
		known map[string]bool,
		zeroInts map[ssa.Value]bool,
		nilIfaces map[ssa.Value]bool,
	) (*coroSyncFastNode, bool)

	explore = func(
		blk, pred *ssa.BasicBlock,
		mutated bool,
		depth, outcomeCalls int,
		visited map[*ssa.BasicBlock]bool,
		known map[string]bool,
		zeroInts map[ssa.Value]bool,
		nilIfaces map[ssa.Value]bool,
	) (*coroSyncFastNode, bool) {
		if blk == nil || !plan.reachableBlocks[blk] || depth > coroSyncRampMaxDepth ||
			nodesUsed >= coroSyncRampMaxNodes || instrsUsed >= coroSyncRampMaxInstrs || visited[blk] {
			if !mutated {
				return &coroSyncFastNode{exitKind: coroSyncFastExitBailStart}, false
			}
			if canResumeAtBlock(blk) {
				return &coroSyncFastNode{exitKind: coroSyncFastExitResumeSlow, resumeBlock: blk}, false
			}
			return nil, false
		}
		if len(blk.Instrs) == 0 {
			return nil, false
		}

		savedNodes, savedInstrs := nodesUsed, instrsUsed
		nodesUsed++
		node := &coroSyncFastNode{block: blk, predBlock: pred, endInstr: -1, mutated: mutated}
		localMutated := mutated
		localOutcomeCalls := outcomeCalls
		localKnown := known
		localZeroInts := cloneMap(zeroInts)
		localNilIfaces := cloneMap(nilIfaces)
		var casCall *ssa.Call
		casOpcode := 0
		casStartedClean := false

		bailOrSlow := func() (*coroSyncFastNode, bool) {
			nodesUsed, instrsUsed = savedNodes, savedInstrs
			if !mutated {
				return &coroSyncFastNode{exitKind: coroSyncFastExitBailStart}, false
			}
			if !canResumeAtBlock(blk) {
				return nil, false
			}
			return &coroSyncFastNode{exitKind: coroSyncFastExitResumeSlow, resumeBlock: blk}, false
		}

		for i, ins := range blk.Instrs {
			instrsUsed++
			if i == len(blk.Instrs)-1 {
				switch term := ins.(type) {
				case *ssa.Return:
					node.exitKind, node.retInstr = coroSyncFastExitReturn, term
					return node, true
				case *ssa.Panic:
					return bailOrSlow()
				case *ssa.Jump:
					nextVis := cloneMap(visited)
					nextVis[blk] = true
					next, useful := explore(blk.Succs[0], blk, localMutated, depth+1, localOutcomeCalls, nextVis, localKnown, localZeroInts, localNilIfaces)
					if next == nil || (!useful && (localMutated || mutated)) {
						if !useful && !mutated && localMutated {
							nodesUsed, instrsUsed = savedNodes, savedInstrs
							return &coroSyncFastNode{exitKind: coroSyncFastExitBailStart}, false
						}
						if !useful && mutated && canResumeAtBlock(blk) {
							return bailOrSlow()
						}
						if next == nil {
							return bailOrSlow()
						}
					}
					node.exitKind, node.nextNode = coroSyncFastExitJump, next
					return node, useful
				case *ssa.If:
					nextVis := cloneMap(visited)
					nextVis[blk] = true
					if knownVal, isKnown := coroSyncStaticBranch(term, pl.ir, localKnown); isKnown {
						succ := blk.Succs[1]
						if knownVal {
							succ = blk.Succs[0]
						}
						next, useful := explore(succ, blk, localMutated, depth+1, localOutcomeCalls, nextVis, localKnown, localZeroInts, localNilIfaces)
						if next == nil {
							return bailOrSlow()
						}
						if !useful {
							if !mutated && localMutated {
								nodesUsed, instrsUsed = savedNodes, savedInstrs
								return &coroSyncFastNode{exitKind: coroSyncFastExitBailStart}, false
							}
							if mutated && canResumeAtBlock(blk) {
								return bailOrSlow()
							}
						}
						node.exitKind, node.nextNode = coroSyncFastExitJump, next
						return node, useful
					}
					trueMut, falseMut := localMutated, localMutated
					if casCall != nil {
						if condVal, matched := coroSyncMatchCASBranch(blk, casCall, casOpcode); matched && condVal == term.Cond {
							trueMut = true
							falseMut = !casStartedClean
						}
					}
					tNode, tUseful := explore(blk.Succs[0], blk, trueMut, depth+1, localOutcomeCalls, nextVis, coroSyncCloneKnown(localKnown, term.Cond, true), localZeroInts, localNilIfaces)
					if tNode == nil && !trueMut {
						tNode, tUseful = &coroSyncFastNode{exitKind: coroSyncFastExitBailStart}, false
					}
					fNode, fUseful := explore(blk.Succs[1], blk, falseMut, depth+1, localOutcomeCalls, nextVis, coroSyncCloneKnown(localKnown, term.Cond, false), localZeroInts, localNilIfaces)
					if fNode == nil && !falseMut {
						fNode, fUseful = &coroSyncFastNode{exitKind: coroSyncFastExitBailStart}, false
					}
					if tNode == nil || fNode == nil {
						return bailOrSlow()
					}
					if casCall != nil && !mutated && !tUseful {
						nodesUsed, instrsUsed = savedNodes, savedInstrs
						return &coroSyncFastNode{exitKind: coroSyncFastExitBailStart}, false
					}
					if !tUseful && !fUseful {
						if !mutated {
							nodesUsed, instrsUsed = savedNodes, savedInstrs
							return &coroSyncFastNode{exitKind: coroSyncFastExitBailStart}, false
						}
						if canResumeAtBlock(blk) {
							return bailOrSlow()
						}
					}
					node.exitKind, node.cond, node.trueNode, node.falseNode = coroSyncFastExitIf, term.Cond, tNode, fNode
					return node, tUseful || fUseful
				default:
					nodesUsed, instrsUsed = savedNodes, savedInstrs
					return nil, false
				}
			}

			phys := plan.instructions[ins]
			if call, isCall := ins.(*ssa.Call); isCall {
				if isPureLenCapBuiltin(call) {
					continue
				}
				if isPureCopyBuiltin(call) && pl.copyBuiltinAllowed() {
					localMutated = true
					node.nodeHasNonCASMutation = true
					continue
				}
				if frozen, found, err := pl.ir.callSitePlan(call); err == nil && found && frozen.plan.Intrinsic {
					if isCoroAtomicIntrinsic(frozen.opcode) {
						if node.atomicOpcodes == nil {
							node.atomicOpcodes = make(map[*ssa.Call]int)
						}
						node.atomicOpcodes[call] = frozen.opcode
						if coroSyncIsReadOnlyAtomicOpcode(frozen.opcode) {
							continue
						}
						if coroSyncIsCASAtomicOpcode(frozen.opcode) && casCall == nil {
							if _, matched := coroSyncMatchCASBranch(blk, call, frozen.opcode); matched {
								casCall, casOpcode, casStartedClean = call, frozen.opcode, !localMutated
								localMutated = true
								continue
							}
						}
						localMutated = true
						node.nodeHasNonCASMutation = true
						continue
					}
					return bailOrSlow()
				}
				if target, isTail := pl.tailCallTarget(plan, blk, i, call); isTail {
					allowTail := true
					if recursiveCaller {
						if tp, ok := pl.whole.FunctionPlan(target); !ok || tp.Recursive {
							allowTail = false
						}
					}
					if allowTail {
						node.endInstr, node.exitKind, node.tailCall, node.tailTarget = i, coroSyncFastExitTailCall, call, target
						return node, true
					}
				}
				if phys.control == coroPhysicalControlDirectAwait && phys.controlClosure == nil &&
					phys.controlTarget != nil && !localMutated && pl.targetHasTryHelper(phys.controlTarget) &&
					(pl.reflectCheck == nil || !pl.reflectCheck(call.Common())) {
					if raw, ok := call.Common().Value.(*ssa.Function); ok && raw == phys.controlTarget {
						if node.tryCalls == nil {
							node.tryCalls = make(map[*ssa.Call]*ssa.Function)
						}
						node.tryCalls[call] = phys.controlTarget
						if !pl.targetHasReadOnlyTry(phys.controlTarget) {
							localMutated = true
							node.nodeHasNonCASMutation = true
						}
						continue
					}
				}
				if pl.isDirectOutcomeCall(call, phys) {
					if spec, isInlineAtomic := pl.matchInlineAtomicWrapper(phys.controlTarget); isInlineAtomic {
						if node.inlineAtomics == nil {
							node.inlineAtomics = make(map[*ssa.Call]coroSyncInlineAtomic)
						}
						node.inlineAtomics[call] = spec
						if localMutated {
							node.nodeHasPostMutNilGuard = true
						}
						if coroSyncIsReadOnlyAtomicOpcode(spec.opcode) {
							continue
						}
						if coroSyncIsCASAtomicOpcode(spec.opcode) && casCall == nil {
							if _, matched := coroSyncMatchCASBranch(blk, call, spec.opcode); matched {
								casCall, casOpcode, casStartedClean = call, spec.opcode, !localMutated
								localMutated = true
								continue
							}
						}
						localMutated = true
						node.nodeHasNonCASMutation = true
						continue
					}
					if localOutcomeCalls >= coroSyncRampMaxOutcomeCalls {
						return bailOrSlow()
					}
					localOutcomeCalls++
					node.nodeHasOutcomeCall = true
					if !pl.coroSyncOutcomeCalleeIsReadOnly(phys.controlTarget) {
						localMutated = true
						node.nodeHasNonCASMutation = true
					}
					continue
				}
				if pl.plainGoCallAllowed(call, phys) {
					if isRuntimeKeepAliveCall(call) {
						continue
					}
					if constVal, isConst := pl.evalPureConstBoolCall(call); isConst {
						if node.constBools == nil {
							node.constBools = make(map[*ssa.Call]bool)
						}
						node.constBools[call] = constVal
						localKnown = coroSyncCloneKnown(localKnown, call, constVal)
						continue
					}
					if coroSyncPlainHelperIsPure(pl.canonicalFunc(call.Common().StaticCallee()), pl.whole) {
						continue
					}
					node.nodeHasPlainSideEffect = true
					localMutated = true
					node.nodeHasNonCASMutation = true
					continue
				}
				return bailOrSlow()
			}

			if phi, isPhiInstr := ins.(*ssa.Phi); isPhiInstr {
				idx := coroSyncPhiEdgeIndex(phi, pred)
				if idx < 0 {
					nodesUsed, instrsUsed = savedNodes, savedInstrs
					return nil, false
				}
				edge := phi.Edges[idx]
				if c, isConst := edge.(*ssa.Const); isConst && c.Value != nil && c.Value.Kind() == constant.Bool {
					localKnown = coroSyncCloneKnown(localKnown, phi, constant.BoolVal(c.Value))
				} else if kv, ok := localKnown[coroSyncExprKey(edge)]; ok {
					localKnown = coroSyncCloneKnown(localKnown, phi, kv)
				}
				localZeroInts[phi] = coroSyncIsZeroIntConst(edge) || localZeroInts[edge]
				localNilIfaces[phi] = coroSyncIsNilInterfaceValue(edge, localNilIfaces)
				continue
			}
			mayMut, guardBail, ok := coroSyncFastPureInstrAllowed(plan, ins, phys, true, false, localZeroInts, localNilIfaces)
			if !ok {
				return bailOrSlow()
			}
			if mk, isMk := ins.(*ssa.MakeInterface); isMk && isUntypedNilConst(mk.X) {
				localNilIfaces[mk] = true
			}
			if guardBail && localMutated {
				if !phys.nilGuard || phys.boundsGuard {
					return bailOrSlow()
				}
				node.nodeHasPostMutNilGuard = true
			}
			if mayMut {
				localMutated = true
				node.nodeHasNonCASMutation = true
			}
		}
		nodesUsed, instrsUsed = savedNodes, savedInstrs
		return nil, false
	}

	root, useful := explore(fn.Blocks[0], nil, false, 0, 0, make(map[*ssa.BasicBlock]bool), make(map[string]bool), make(map[ssa.Value]bool), make(map[ssa.Value]bool))
	if root == nil || !useful || root.exitKind == coroSyncFastExitBailStart || root.exitKind == coroSyncFastExitResumeSlow {
		return nil
	}

	// Summarize the surviving pruned fast tree and assign ResumeSlow tags.
	var hasReturn, hasTailCall, hasOutcomeCall, hasPlainSideEffect, hasNonCASMutation, hasPostMutNilGuard, tooManySlow bool
	slowTargetIdx := make(map[*ssa.BasicBlock]uint8)
	var slowTargets []coroSyncSlowTarget

	var summarize func(n *coroSyncFastNode)
	summarize = func(n *coroSyncFastNode) {
		if n == nil || tooManySlow {
			return
		}
		hasOutcomeCall = hasOutcomeCall || n.nodeHasOutcomeCall
		hasPlainSideEffect = hasPlainSideEffect || n.nodeHasPlainSideEffect
		hasNonCASMutation = hasNonCASMutation || n.nodeHasNonCASMutation
		hasPostMutNilGuard = hasPostMutNilGuard || n.nodeHasPostMutNilGuard
		switch n.exitKind {
		case coroSyncFastExitReturn:
			hasReturn = true
		case coroSyncFastExitTailCall:
			hasTailCall = true
		case coroSyncFastExitResumeSlow:
			tag, exists := slowTargetIdx[n.resumeBlock]
			if !exists {
				if len(slowTargets) >= coroSyncRampMaxSlowTargets {
					tooManySlow = true
					return
				}
				tag = uint8(len(slowTargets) + 1)
				slowTargetIdx[n.resumeBlock] = tag
				slowTargets = append(slowTargets, coroSyncSlowTarget{tag: tag, block: n.resumeBlock})
			}
			n.resumeTag = tag
		case coroSyncFastExitJump:
			summarize(n.nextNode)
		case coroSyncFastExitIf:
			summarize(n.trueNode)
			summarize(n.falseNode)
		}
	}
	summarize(root)
	if tooManySlow || (!hasReturn && !hasTailCall) {
		return nil
	}

	var liveValues []ssa.Value
	var liveInByTag map[uint8]map[ssa.Value]bool
	var slowAugCFG *coroSyncSlowAugCFG
	if len(slowTargets) > 0 {
		var ok bool
		liveValues, liveInByTag, slowAugCFG, ok = collectCoroSyncResumeSlowLiveValues(plan, slowTargets)
		if !ok || !coroSyncFastTreeCoversLiveIns(root, liveInByTag) {
			return nil
		}
	}

	kind := coroSyncRampTryBailout
	hasTryHelper := false
	switch {
	case len(slowTargets) > 0:
		kind = coroSyncRampResumeSlow
	case hasTailCall:
		kind = coroSyncRampTailSlow
	default:
		if !hasOutcomeCall && !hasPlainSideEffect && !hasNonCASMutation && !hasPostMutNilGuard &&
			fn.Synthetic == "" && fn.Origin() == nil && len(fn.TypeArgs()) == 0 {
			hasTryHelper = true
		}
	}

	return &coroPhysicalSyncRampPlan{
		kind:           kind,
		hasTryHelper:   hasTryHelper,
		root:           root,
		slowTargets:    slowTargets,
		liveValues:     liveValues,
		liveInByTag:    liveInByTag,
		slowAugCFG:     slowAugCFG,
		needsSlowSplit: true,
	}
}

// -----------------------------------------------------------------------------
// Strategy 3: SingleAwait (pure prefix + 1 awaited call + post-call)
// -----------------------------------------------------------------------------

func (pl *coroSyncPlanner) ifacePtrDataOutcomeAvailable(caller *ssa.Function) bool {
	if pl.whole == nil || caller == nil {
		return false
	}
	target, ok := pl.whole.ResolveLoweredCall(caller, "IfacePtrData")
	if !ok || target == nil {
		return false
	}
	target = pl.canonicalFunc(target)
	fp, planned := pl.whole.FunctionPlan(target)
	return planned && fp.External == coro.Defined &&
		fp.Emission == coro.EmitOutcomePlain &&
		fp.ManagedEntry == coro.ManagedEntryOutcomePlain
}

func (pl *coroSyncPlanner) planSingleAwait(
	plan *coroPhysicalFunctionPlan,
	cyclic map[*ssa.BasicBlock]bool,
) *coroPhysicalSyncRampPlan {
	fn := plan.function
	fnSig := coroPhysicalNormalizeSourceSignature(fn.Signature)
	if fnSig == nil {
		return nil
	}

	isCandidate := func(call *ssa.Call, phys coroPhysicalInstructionPlan) (callResults *types.Tuple, boxed bool, ok bool) {
		if call == nil || call.Common() == nil || phys.recoverAlias ||
			phys.operation != coroPhysicalOperationNone || phys.outcome != coroPhysicalOutcomeNone ||
			phys.controlFailure != "" || (pl.reflectCheck != nil && pl.reflectCheck(call.Common())) {
			return nil, false, false
		}
		var callSig *types.Signature
		var directTarget *ssa.Function
		switch phys.control {
		case coroPhysicalControlDirectAwait:
			raw, ok := call.Common().Value.(*ssa.Function)
			if phys.controlClosure != nil || phys.controlTarget == nil || phys.controlTarget == fn ||
				!ok || raw != phys.controlTarget || !pl.targetHasSyncRamp(phys.controlTarget) {
				return nil, false, false
			}
			directTarget = phys.controlTarget
			callSig = coroPhysicalNormalizeSourceSignature(phys.controlTarget.Signature)
		case coroPhysicalControlExactInterfaceAwait:
			if phys.controlTarget == nil || phys.controlTarget == fn || phys.controlReceiver == nil ||
				!pl.targetHasSyncRamp(phys.controlTarget) {
				return nil, false, false
			}
			directTarget = phys.controlTarget
			callSig = coroPhysicalNormalizeSourceSignature(phys.controlTarget.Signature)
		case coroPhysicalControlManagedInterfaceAwait:
			if phys.controlSignature == nil || phys.dispatchEntry == coro.ManagedEntryPlain {
				return nil, false, false
			}
			if !phys.rawInterfaceReceiver && !pl.ifacePtrDataOutcomeAvailable(fn) {
				return nil, false, false
			}
			callSig = coroPhysicalNormalizeSourceSignature(phys.controlSignature)
		case coroPhysicalControlDispatchAwait:
			if phys.dispatchEntry == coro.ManagedEntryPlain {
				return nil, false, false
			}
			callSig = coroPhysicalNormalizeSourceSignature(call.Common().Signature())
		default:
			return nil, false, false
		}
		if callSig == nil {
			return nil, false, false
		}
		res := callSig.Results()
		if res.Len() == 0 {
			return res, false, true
		}
		if fnSig.Results().Len() > 0 && types.Identical(res, fnSig.Results()) {
			return res, false, true
		}
		if directTarget != nil && pl.targetHasBoxHelper(directTarget) {
			return res, true, true
		}
		return nil, false, false
	}

	nodesUsed := 0
	var chosenCall *ssa.Call
	var chosenPlan coroPhysicalInstructionPlan
	var chosenResults *types.Tuple
	chosenBoxed := false
	postHasReturn := false

	bumpVisit := func(src map[*ssa.BasicBlock]int, blk *ssa.BasicBlock) map[*ssa.BasicBlock]int {
		dst := cloneMap(src)
		dst[blk]++
		return dst
	}

	var explorePost func(blk, pred *ssa.BasicBlock, startIdx, depth, outcomeCalls int, visits map[*ssa.BasicBlock]int, known map[string]bool, zeroInts, nilIfaces map[ssa.Value]bool) *coroSyncFastNode
	explorePost = func(blk, pred *ssa.BasicBlock, startIdx, depth, outcomeCalls int, visits map[*ssa.BasicBlock]int, known map[string]bool, zeroInts, nilIfaces map[ssa.Value]bool) *coroSyncFastNode {
		fallback := &coroSyncFastNode{exitKind: coroSyncFastExitResumePostCall}
		if blk == nil || !plan.reachableBlocks[blk] || depth > coroSyncRampMaxDepth || nodesUsed >= coroSyncRampMaxNodes {
			return fallback
		}
		limit := 1
		if startIdx == 0 && cyclic[blk] && len(blk.Instrs) > 0 && isPhi(blk.Instrs[0]) {
			limit = 2
		}
		if startIdx == 0 && visits[blk] >= limit {
			return fallback
		}
		nodesUsed++
		localZeroInts := cloneMap(zeroInts)
		localNilIfaces := cloneMap(nilIfaces)
		localOutcomeCalls := outcomeCalls
		node := &coroSyncFastNode{block: blk, predBlock: pred, startInstr: startIdx, endInstr: -1, mutated: true}
		for i := startIdx; i < len(blk.Instrs); i++ {
			ins := blk.Instrs[i]
			if i == len(blk.Instrs)-1 {
				switch term := ins.(type) {
				case *ssa.Return:
					node.exitKind, node.retInstr = coroSyncFastExitReturn, term
					postHasReturn = true
					return node
				case *ssa.Jump:
					node.exitKind, node.nextNode = coroSyncFastExitJump, explorePost(blk.Succs[0], blk, 0, depth+1, localOutcomeCalls, bumpVisit(visits, blk), known, localZeroInts, localNilIfaces)
					return node
				case *ssa.If:
					nv := bumpVisit(visits, blk)
					if kv, ok := coroSyncStaticBranch(term, pl.ir, known); ok {
						succ := blk.Succs[1]
						if kv {
							succ = blk.Succs[0]
						}
						node.exitKind, node.nextNode = coroSyncFastExitJump, explorePost(succ, blk, 0, depth+1, localOutcomeCalls, nv, known, localZeroInts, localNilIfaces)
						return node
					}
					node.exitKind, node.cond = coroSyncFastExitIf, term.Cond
					node.trueNode = explorePost(blk.Succs[0], blk, 0, depth+1, localOutcomeCalls, nv, coroSyncCloneKnown(known, term.Cond, true), localZeroInts, localNilIfaces)
					node.falseNode = explorePost(blk.Succs[1], blk, 0, depth+1, localOutcomeCalls, nv, coroSyncCloneKnown(known, term.Cond, false), localZeroInts, localNilIfaces)
					return node
				default:
					return fallback
				}
			}
			phys := plan.instructions[ins]
			if phi, ok := ins.(*ssa.Phi); ok {
				idx := coroSyncPhiEdgeIndex(phi, pred)
				if idx < 0 {
					return fallback
				}
				edge := phi.Edges[idx]
				localZeroInts[phi] = coroSyncIsZeroIntConst(edge) || localZeroInts[edge]
				localNilIfaces[phi] = coroSyncIsNilInterfaceValue(edge, localNilIfaces)
				continue
			}
			if call, ok := ins.(*ssa.Call); ok {
				if isPureLenCapBuiltin(call) {
					continue
				}
				if pl.plainGoCallAllowed(call, phys) && coroSyncPlainHelperIsPure(pl.canonicalFunc(call.Common().StaticCallee()), pl.whole) {
					continue
				}
				if pl.isDirectOutcomeCall(call, phys) && localOutcomeCalls < coroSyncRampMaxOutcomeCalls &&
					pl.coroSyncOutcomeCalleeIsReadOnlyNoExplicitPanic(phys.controlTarget) {
					localOutcomeCalls++
					node.nodeHasOutcomeCall = true
					continue
				}
				return fallback
			}
			if mayMut, guardBail, ok := coroSyncFastPureInstrAllowed(plan, ins, phys, false, true, localZeroInts, localNilIfaces); !ok || mayMut || (guardBail && phys.nilGuard) {
				return fallback
			}
			if mk, isMk := ins.(*ssa.MakeInterface); isMk && isUntypedNilConst(mk.X) {
				localNilIfaces[mk] = true
			}
		}
		return fallback
	}

	var explorePre func(blk, pred *ssa.BasicBlock, depth, outcomeCalls int, visits map[*ssa.BasicBlock]int, known map[string]bool, zeroInts, nilIfaces map[ssa.Value]bool) *coroSyncFastNode
	explorePre = func(blk, pred *ssa.BasicBlock, depth, outcomeCalls int, visits map[*ssa.BasicBlock]int, known map[string]bool, zeroInts, nilIfaces map[ssa.Value]bool) *coroSyncFastNode {
		bail := &coroSyncFastNode{exitKind: coroSyncFastExitBailStart}
		if blk == nil || !plan.reachableBlocks[blk] || depth > coroSyncRampMaxDepth ||
			nodesUsed >= coroSyncRampMaxNodes || visits[blk] >= 1 {
			return bail
		}
		nodesUsed++
		localZeroInts := cloneMap(zeroInts)
		localNilIfaces := cloneMap(nilIfaces)
		localOutcomeCalls := outcomeCalls
		node := &coroSyncFastNode{block: blk, predBlock: pred, endInstr: -1}
		for i, ins := range blk.Instrs {
			if i == len(blk.Instrs)-1 {
				switch term := ins.(type) {
				case *ssa.Return:
					node.exitKind, node.retInstr = coroSyncFastExitReturn, term
					return node
				case *ssa.Jump:
					node.exitKind, node.nextNode = coroSyncFastExitJump, explorePre(blk.Succs[0], blk, depth+1, localOutcomeCalls, bumpVisit(visits, blk), known, localZeroInts, localNilIfaces)
					return node
				case *ssa.If:
					nv := bumpVisit(visits, blk)
					if kv, ok := coroSyncStaticBranch(term, pl.ir, known); ok {
						succ := blk.Succs[1]
						if kv {
							succ = blk.Succs[0]
						}
						node.exitKind, node.nextNode = coroSyncFastExitJump, explorePre(succ, blk, depth+1, localOutcomeCalls, nv, known, localZeroInts, localNilIfaces)
						return node
					}
					node.exitKind, node.cond = coroSyncFastExitIf, term.Cond
					node.trueNode = explorePre(blk.Succs[0], blk, depth+1, localOutcomeCalls, nv, coroSyncCloneKnown(known, term.Cond, true), localZeroInts, localNilIfaces)
					node.falseNode = explorePre(blk.Succs[1], blk, depth+1, localOutcomeCalls, nv, coroSyncCloneKnown(known, term.Cond, false), localZeroInts, localNilIfaces)
					return node
				default:
					return bail
				}
			}
			phys := plan.instructions[ins]
			if phi, ok := ins.(*ssa.Phi); ok {
				idx := coroSyncPhiEdgeIndex(phi, pred)
				if idx < 0 {
					return bail
				}
				edge := phi.Edges[idx]
				localZeroInts[phi] = coroSyncIsZeroIntConst(edge) || localZeroInts[edge]
				localNilIfaces[phi] = coroSyncIsNilInterfaceValue(edge, localNilIfaces)
				continue
			}
			if call, ok := ins.(*ssa.Call); ok {
				if isPureLenCapBuiltin(call) {
					continue
				}
				if pl.plainGoCallAllowed(call, phys) && coroSyncPlainHelperIsPure(pl.canonicalFunc(call.Common().StaticCallee()), pl.whole) {
					continue
				}
				if pl.isDirectOutcomeCall(call, phys) && localOutcomeCalls < coroSyncRampMaxOutcomeCalls &&
					pl.coroSyncOutcomeCalleeIsReadOnlyNoExplicitPanic(phys.controlTarget) {
					localOutcomeCalls++
					node.nodeHasOutcomeCall = true
					continue
				}
				if callRes, boxed, ok := isCandidate(call, phys); ok {
					if chosenCall != nil {
						return bail
					}
					chosenCall, chosenPlan, chosenResults, chosenBoxed = call, phys, callRes, boxed
					node.endInstr, node.singleAwait = i, call
					// Fresh branch facts post-call so revisited loop headers re-evaluate Phis.
					node.singleAwaitPost = explorePost(blk, pred, i+1, depth+1, 0, bumpVisit(visits, blk), make(map[string]bool), make(map[ssa.Value]bool), make(map[ssa.Value]bool))
					return node
				}
				return bail
			}
			if mayMut, _, ok := coroSyncFastPureInstrAllowed(plan, ins, phys, false, true, localZeroInts, localNilIfaces); !ok || mayMut {
				return bail
			}
			if mk, isMk := ins.(*ssa.MakeInterface); isMk && isUntypedNilConst(mk.X) {
				localNilIfaces[mk] = true
			}
		}
		return bail
	}

	root := explorePre(fn.Blocks[0], nil, 0, 0, make(map[*ssa.BasicBlock]int), make(map[string]bool), make(map[ssa.Value]bool), make(map[ssa.Value]bool))
	if root == nil || chosenCall == nil || !postHasReturn {
		return nil
	}
	liveValues, liveInByTag, slowAugCFG, ok := collectCoroSyncSingleAwaitLiveValues(plan, chosenCall)
	if !ok || !coroSyncFastTreeCoversLiveIns(root, liveInByTag) {
		return nil
	}
	return &coroPhysicalSyncRampPlan{
		kind:               coroSyncRampSingleAwait,
		root:               root,
		liveValues:         liveValues,
		liveInByTag:        liveInByTag,
		slowAugCFG:         slowAugCFG,
		singleAwaitCall:    chosenCall,
		singleAwaitPlan:    chosenPlan,
		singleAwaitBoxed:   chosenBoxed,
		singleAwaitResults: chosenResults,
		needsSlowSplit:     true,
	}
}

// -----------------------------------------------------------------------------
// Strategy 4: BoundedLoopLeaf (pure read-only single-loop leaf with work bound)
// -----------------------------------------------------------------------------

func (pl *coroSyncPlanner) planBoundedLoopLeaf(
	plan *coroPhysicalFunctionPlan,
	cyclic map[*ssa.BasicBlock]bool,
) *coroPhysicalSyncRampPlan {
	if pl.prog == nil || len(cyclic) == 0 {
		return nil
	}
	fn := plan.function
	if len(fn.Blocks) == 0 || !plan.reachableBlocks[fn.Blocks[0]] {
		return nil
	}

	liveSuccs := func(blk *ssa.BasicBlock) []*ssa.BasicBlock {
		if blk == nil || len(blk.Instrs) == 0 || !plan.reachableBlocks[blk] {
			return nil
		}
		switch term := blk.Instrs[len(blk.Instrs)-1].(type) {
		case *ssa.Jump:
			if len(blk.Succs) == 1 && plan.reachableBlocks[blk.Succs[0]] {
				return blk.Succs
			}
		case *ssa.If:
			if len(blk.Succs) != 2 {
				return nil
			}
			if kv, ok := coroSyncStaticBranch(term, pl.ir, nil); ok {
				succ := blk.Succs[1]
				if kv {
					succ = blk.Succs[0]
				}
				if plan.reachableBlocks[succ] {
					return []*ssa.BasicBlock{succ}
				}
				return nil
			}
			var out []*ssa.BasicBlock
			for _, s := range blk.Succs {
				if plan.reachableBlocks[s] {
					out = append(out, s)
				}
			}
			return out
		}
		return nil
	}

	// Collect blocks reachable from entry over live edges.
	liveBlocks := map[*ssa.BasicBlock]bool{fn.Blocks[0]: true}
	queue := []*ssa.BasicBlock{fn.Blocks[0]}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, succ := range liveSuccs(cur) {
			if !liveBlocks[succ] {
				liveBlocks[succ] = true
				queue = append(queue, succ)
			}
		}
	}
	if len(liveBlocks) > coroSyncRampMaxNodes {
		return nil
	}

	// Reject duplicate live predecessor edges and oversized bodies.
	totalInstrs := 0
	hasReturn := false
	for _, blk := range fn.Blocks {
		if !liveBlocks[blk] {
			continue
		}
		if len(blk.Instrs) == 0 {
			return nil
		}
		seenPred := make(map[*ssa.BasicBlock]bool, len(blk.Preds))
		for _, pred := range blk.Preds {
			if !liveBlocks[pred] {
				continue
			}
			// Check if edge pred -> blk is actually live.
			edgeLive := false
			for _, s := range liveSuccs(pred) {
				if s == blk {
					edgeLive = true
					break
				}
			}
			if !edgeLive {
				continue
			}
			if seenPred[pred] {
				return nil
			}
			seenPred[pred] = true
		}
		totalInstrs += len(blk.Instrs)
		if totalInstrs > coroSyncRampMaxInstrs {
			return nil
		}
		// Reject duplicate successors on an If terminator (same block on both arms).
		if ifTerm, isIf := blk.Instrs[len(blk.Instrs)-1].(*ssa.If); isIf {
			if _, isStatic := coroSyncStaticBranch(ifTerm, pl.ir, nil); !isStatic &&
				len(blk.Succs) == 2 && blk.Succs[0] == blk.Succs[1] {
				return nil
			}
		}
	}

	budget := pl.whole.MaxPlainInstructions()
	if budget <= 0 {
		budget = coro.DefaultMaxPlainInstructions
	}
	// A bounded-loop sync ramp is strictly a call-free read-only leaf whose
	// callers retain their own preemption countdown. Under the default budget,
	// scale up to coroSyncBoundedLoopWorkBudget so typical short strings and
	// identifiers (~32-64 bytes) finish synchronously while longer inputs still
	// bail promptly to $coro.slow. Smaller explicit budgets are honored verbatim.
	leafBudget := uint64(budget)
	if budget >= coro.DefaultMaxPlainInstructions {
		leafBudget = uint64(budget) * 8
		if leafBudget > coroSyncBoundedLoopWorkBudget {
			leafBudget = coroSyncBoundedLoopWorkBudget
		}
	}

	evaluated := func(ins ssa.Instruction) bool {
		return plan.instructions[ins].semantic.evaluated
	}
	blockCost := func(blk *ssa.BasicBlock) uint64 {
		var cost uint64
		for _, ins := range blk.Instrs {
			phys := plan.instructions[ins]
			if phys.semantic.evaluated && !phys.semantic.debug {
				cost++
			}
		}
		return cost
	}

	proof, ok := coroProveBoundedLoopTripBudget(
		pl.prog, fn, liveBlocks, evaluated, blockCost, liveSuccs, leafBudget,
	)
	if !ok || proof.maxBackedges == 0 {
		return nil
	}

	entryPred := proof.loop.header.Preds[proof.loop.entryPred]
	if !liveBlocks[entryPred] || !liveBlocks[proof.loop.latch] {
		return nil
	}

	// Verify every instruction in every live block is strictly pure read-only
	// so bailing out to $coro.slow from any guard or trip-budget check has zero
	// observable side effects.
	for _, blk := range fn.Blocks {
		if !liveBlocks[blk] {
			continue
		}
		for i, ins := range blk.Instrs {
			if i == len(blk.Instrs)-1 {
				switch term := ins.(type) {
				case *ssa.Return:
					hasReturn = true
				case *ssa.Jump:
					if len(blk.Succs) != 1 || !liveBlocks[blk.Succs[0]] {
						return nil
					}
				case *ssa.If:
					if len(blk.Succs) != 2 {
						return nil
					}
					if _, isStatic := coroSyncStaticBranch(term, pl.ir, nil); !isStatic {
						if !liveBlocks[blk.Succs[0]] || !liveBlocks[blk.Succs[1]] {
							return nil
						}
					}
				default:
					return nil
				}
				continue
			}
			phys := plan.instructions[ins]
			switch v := ins.(type) {
			case *ssa.DebugRef:
				continue
			case *ssa.RunDefers:
				if plan.cleanup != nil {
					return nil
				}
				continue
			case *ssa.Phi:
				if len(v.Edges) != len(blk.Preds) {
					return nil
				}
				for idx, pred := range blk.Preds {
					if !liveBlocks[pred] {
						continue
					}
					edge := v.Edges[idx]
					if edge == nil {
						return nil
					}
					switch edge.(type) {
					case *ssa.Parameter, *ssa.Const, *ssa.Global, *ssa.Function, ssa.Instruction:
					default:
						return nil
					}
				}
				continue
			case *ssa.Call:
				if !isPureLenCapBuiltin(v) ||
					phys.control != coroPhysicalControlNone ||
					phys.operation != coroPhysicalOperationNone ||
					phys.outcome != coroPhysicalOutcomeNone {
					return nil
				}
				continue
			case *ssa.IndexAddr:
				if !phys.semantic.evaluated || phys.elideValue ||
					phys.control != coroPhysicalControlNone ||
					phys.operation != coroPhysicalOperationNone ||
					phys.outcome != coroPhysicalOutcomeNone ||
					phys.recipe != coroPhysicalInstructionIndexAddr ||
					(phys.container != coroPhysicalContainerSlice && phys.container != coroPhysicalContainerArrayPointer) {
					return nil
				}
				refs := v.Referrers()
				if refs == nil {
					return nil
				}
				staysLocal := ssaAddrStaysInLocalAlloc(v, plan.function)
				for _, ref := range *refs {
					if _, isDbg := ref.(*ssa.DebugRef); isDbg {
						continue
					}
					if un, isUn := ref.(*ssa.UnOp); isUn && un.Op == token.MUL && un.X == v {
						continue
					}
					if st, isSt := ref.(*ssa.Store); isSt && st.Addr == v && staysLocal {
						continue
					}
					return nil
				}
				continue
			case *ssa.Index:
				if !phys.semantic.evaluated || phys.elideValue ||
					phys.control != coroPhysicalControlNone ||
					phys.operation != coroPhysicalOperationNone ||
					phys.outcome != coroPhysicalOutcomeNone ||
					phys.recipe != coroPhysicalInstructionIndex ||
					(phys.container != coroPhysicalContainerString &&
						phys.container != coroPhysicalContainerSlice &&
						phys.container != coroPhysicalContainerArrayPointer) {
					return nil
				}
				continue
			case *ssa.Alloc:
				if v.Heap ||
					phys.control != coroPhysicalControlNone ||
					phys.operation != coroPhysicalOperationNone ||
					phys.outcome != coroPhysicalOutcomeNone ||
					(phys.recipe != coroPhysicalInstructionOrdinary &&
						phys.recipe != coroPhysicalInstructionFrameAllocation &&
						phys.recipe != coroPhysicalInstructionFrameBitcastAllocation) {
					return nil
				}
				continue
			case *ssa.Store:
				if phys.control != coroPhysicalControlNone ||
					phys.operation != coroPhysicalOperationNone ||
					phys.outcome != coroPhysicalOutcomeNone ||
					(phys.recipe != coroPhysicalInstructionOrdinary &&
						phys.recipe != coroPhysicalInstructionStore) ||
					!ssaAddrStaysInLocalAlloc(v.Addr, plan.function) {
					return nil
				}
				continue
			case *ssa.Slice:
				return nil
			default:
				mayMut, _, allowed := coroSyncFastPureInstrAllowed(plan, ins, phys, false, false, nil, nil)
				if !allowed || mayMut {
					return nil
				}
			}
		}
	}
	if !hasReturn {
		return nil
	}

	loopPlan := &coroSyncBoundedLoopPlan{
		header:       proof.loop.header,
		latch:        proof.loop.latch,
		entryPred:    entryPred,
		liveBlocks:   liveBlocks,
		maxBackedges: proof.maxBackedges,
	}

	// When the loop has no interior early exits (every exit is through header's
	// canonical `i < bound` check with step +1), emit an O(1) entry fast-reject
	// on the entryPred -> header edge so long inputs bail to $coro.slow before
	// executing even one loop iteration.
	if !proof.hasInteriorExit &&
		proof.loop.step.Cmp(big.NewInt(1)) == 0 &&
		proof.loop.operator == token.LSS &&
		proof.start.Sign() >= 0 {
		limitBig := new(big.Int).Add(proof.start, new(big.Int).SetUint64(uint64(proof.maxBackedges)))
		if limitBig.IsUint64() && coroIntegerFits(limitBig, proof.loop.minimum, proof.loop.maximum) {
			domEntry := func(v ssa.Value) bool {
				switch x := v.(type) {
				case *ssa.Parameter, *ssa.Const, *ssa.Global:
					return true
				case ssa.Instruction:
					return x.Block() != nil && liveBlocks[x.Block()] && x.Block().Dominates(entryPred)
				default:
					return false
				}
			}
			switch bv := proof.loop.boundValue.(type) {
			case *ssa.Call:
				if isPureLenCapBuiltin(bv) && domEntry(bv.Call.Args[0]) {
					if bv.Common().Value.(*ssa.Builtin).Name() == "len" {
						loopPlan.entryGuardKind = coroSyncLoopEntryGuardLen
					} else {
						loopPlan.entryGuardKind = coroSyncLoopEntryGuardCap
					}
					loopPlan.entryGuardValue = bv.Call.Args[0]
					loopPlan.entryGuardLimit = limitBig.Uint64()
				}
			default:
				if domEntry(bv) {
					loopPlan.entryGuardKind = coroSyncLoopEntryGuardValue
					loopPlan.entryGuardValue = bv
					loopPlan.entryGuardLimit = limitBig.Uint64()
				}
			}
		}
	}

	hasTryHelper := fn.Synthetic == "" && fn.Origin() == nil && len(fn.TypeArgs()) == 0
	return &coroPhysicalSyncRampPlan{
		kind:           coroSyncRampBoundedLoop,
		hasTryHelper:   hasTryHelper,
		boundedLoop:    loopPlan,
		needsSlowSplit: true,
	}
}

func (pl *coroSyncPlanner) copyBuiltinAllowed() bool {
	return pl.hasSliceCopy
}

func coroSyncPlainHelperIsPure(callee *ssa.Function, whole *coro.SSAPlan) bool {
	if callee == nil || len(callee.Blocks) == 0 || len(callee.Blocks) > 4 || len(callee.FreeVars) != 0 {
		return false
	}
	calleePlan, ok := whole.FunctionPlan(callee)
	if !ok || calleePlan.Emission != coro.EmitPlain || calleePlan.Effect != coro.NoSuspend ||
		calleePlan.Exec.Contains(coro.MayUnwind|coro.OpaqueExec|coro.BlockForeign) {
		return false
	}
	for _, blk := range callee.Blocks {
		for _, ins := range blk.Instrs {
			switch v := ins.(type) {
			case *ssa.DebugRef, *ssa.Phi, *ssa.BinOp, *ssa.UnOp, *ssa.Field, *ssa.FieldAddr,
				*ssa.Index, *ssa.IndexAddr, *ssa.Extract, *ssa.Convert, *ssa.ChangeType,
				*ssa.If, *ssa.Jump, *ssa.Return:
				if un, isUn := v.(*ssa.UnOp); isUn && un.Op == token.ARROW {
					return false
				}
			case *ssa.Alloc:
				if v.Heap {
					return false
				}
			case *ssa.Store:
				if !ssaAddrStaysInLocalAlloc(v.Addr, callee) {
					return false
				}
			case *ssa.Call:
				if isPureLenCapBuiltin(v) {
					continue
				}
				return false
			default:
				return false
			}
		}
	}
	return true
}

// -----------------------------------------------------------------------------
// Cross-Boundary Live-In Analysis & Coverage Proof
// -----------------------------------------------------------------------------

func collectCoroSyncResumeSlowLiveValues(
	plan *coroPhysicalFunctionPlan,
	targets []coroSyncSlowTarget,
) ([]ssa.Value, map[uint8]map[ssa.Value]bool, *coroSyncSlowAugCFG, bool) {
	entryBlocks := make([]*ssa.BasicBlock, len(targets))
	for i, t := range targets {
		entryBlocks[i] = t.block
	}
	all, augCFG, ok := collectCoroSyncCrossBoundaryLiveValues(plan, entryBlocks, nil)
	if !ok {
		return nil, nil, nil, false
	}
	byTag := make(map[uint8]map[ssa.Value]bool, len(targets))
	for _, t := range targets {
		per, _, ok := collectCoroSyncCrossBoundaryLiveValues(plan, []*ssa.BasicBlock{t.block}, nil)
		if !ok {
			return nil, nil, nil, false
		}
		req := make(map[ssa.Value]bool, len(per))
		for _, v := range per {
			req[v] = true
		}
		byTag[t.tag] = req
	}
	return all, byTag, augCFG, true
}

func collectCoroSyncSingleAwaitLiveValues(
	plan *coroPhysicalFunctionPlan,
	call *ssa.Call,
) ([]ssa.Value, map[uint8]map[ssa.Value]bool, *coroSyncSlowAugCFG, bool) {
	if call == nil || call.Block() == nil {
		return nil, nil, nil, false
	}
	all, augCFG, ok := collectCoroSyncCrossBoundaryLiveValues(plan, []*ssa.BasicBlock{call.Block()}, call)
	if !ok {
		return nil, nil, nil, false
	}
	req := make(map[ssa.Value]bool, len(all))
	for _, v := range all {
		req[v] = true
	}
	return all, map[uint8]map[ssa.Value]bool{1: req, 2: req}, augCFG, true
}

// coroSyncFastTreeCoversLiveIns proves that every fast-tree path reaching a
// ResumeSlow or SingleAwait exit defines every SSA value required by its tag.
func coroSyncFastTreeCoversLiveIns(root *coroSyncFastNode, liveInByTag map[uint8]map[ssa.Value]bool) bool {
	if root == nil || len(liveInByTag) == 0 {
		return true
	}
	var walk func(n *coroSyncFastNode, defined map[ssa.Value]bool) bool
	walk = func(n *coroSyncFastNode, defined map[ssa.Value]bool) bool {
		if n == nil {
			return true
		}
		switch n.exitKind {
		case coroSyncFastExitBailStart, coroSyncFastExitResumePostCall:
			return true
		case coroSyncFastExitResumeSlow:
			for lv := range liveInByTag[n.resumeTag] {
				if !defined[lv] {
					return false
				}
			}
			return true
		}
		cur := cloneMap(defined)
		end := n.endInstr
		if end < 0 {
			end = len(n.block.Instrs) - 1
		}
		for i := n.startInstr; i < end; i++ {
			if val, ok := n.block.Instrs[i].(ssa.Value); ok {
				cur[val] = true
			}
		}
		if n.singleAwait != nil {
			for lv := range liveInByTag[1] {
				if !cur[lv] {
					return false
				}
			}
			return true
		}
		switch n.exitKind {
		case coroSyncFastExitJump:
			return walk(n.nextNode, cur)
		case coroSyncFastExitIf:
			return walk(n.trueNode, cur) && walk(n.falseNode, cur)
		default:
			return true
		}
	}
	return walk(root, make(map[ssa.Value]bool))
}

func collectCoroSyncCrossBoundaryLiveValues(
	plan *coroPhysicalFunctionPlan,
	entryBlocks []*ssa.BasicBlock,
	splitCall *ssa.Call,
) ([]ssa.Value, *coroSyncSlowAugCFG, bool) {
	fn := plan.function
	reach := make(map[*ssa.BasicBlock]bool)
	queue := make([]*ssa.BasicBlock, 0, len(entryBlocks))
	for _, b := range entryBlocks {
		if b != nil && !reach[b] {
			reach[b] = true
			queue = append(queue, b)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, succ := range cur.Succs {
			if succ != nil && plan.reachableBlocks[succ] && !reach[succ] {
				reach[succ] = true
				queue = append(queue, succ)
			}
		}
	}

	// Compute dominators on the augmented CFG where synthetic entry jumps to
	// fn.Blocks[0] AND every entryBlock.
	var allBlocks []*ssa.BasicBlock
	for _, b := range fn.Blocks {
		if plan.reachableBlocks[b] {
			allBlocks = append(allBlocks, b)
		}
	}
	isEntry := make(map[*ssa.BasicBlock]bool, len(entryBlocks)+1)
	isEntry[fn.Blocks[0]] = true
	for _, b := range entryBlocks {
		isEntry[b] = true
	}

	dom := make(map[*ssa.BasicBlock]map[*ssa.BasicBlock]bool, len(allBlocks))
	for _, b := range allBlocks {
		if isEntry[b] {
			dom[b] = map[*ssa.BasicBlock]bool{b: true}
		} else {
			full := make(map[*ssa.BasicBlock]bool, len(allBlocks))
			for _, o := range allBlocks {
				full[o] = true
			}
			dom[b] = full
		}
	}
	for changed := true; changed; {
		changed = false
		for _, b := range allBlocks {
			if isEntry[b] {
				continue
			}
			var next map[*ssa.BasicBlock]bool
			for _, pred := range b.Preds {
				if !plan.reachableBlocks[pred] {
					continue
				}
				if next == nil {
					next = cloneMap(dom[pred])
				} else {
					for d := range next {
						if !dom[pred][d] {
							delete(next, d)
						}
					}
				}
			}
			if next == nil {
				next = make(map[*ssa.BasicBlock]bool)
			}
			next[b] = true
			if len(next) != len(dom[b]) {
				dom[b] = next
				changed = true
			}
		}
	}

	var splitBlock *ssa.BasicBlock
	splitCallIdx := -1
	splitBlockReentered := false
	if splitCall != nil {
		splitBlock = splitCall.Block()
		for i, ins := range splitBlock.Instrs {
			if ins == splitCall {
				splitCallIdx = i
				break
			}
		}
		if splitCallIdx < 0 {
			return nil, nil, false
		}
		seen := make(map[*ssa.BasicBlock]bool)
		reQueue := make([]*ssa.BasicBlock, 0, len(splitBlock.Succs))
		for _, succ := range splitBlock.Succs {
			if succ != nil && plan.reachableBlocks[succ] && !seen[succ] {
				seen[succ] = true
				reQueue = append(reQueue, succ)
			}
		}
		for len(reQueue) > 0 {
			cur := reQueue[0]
			reQueue = reQueue[1:]
			if cur == splitBlock {
				splitBlockReentered = true
				break
			}
			for _, succ := range cur.Succs {
				if succ != nil && plan.reachableBlocks[succ] && !seen[succ] {
					seen[succ] = true
					reQueue = append(reQueue, succ)
				}
			}
		}
	}

	needed := make(map[ssa.Value]bool)
	var ordered []ssa.Value
	var scratch instructionOperandScratch

	recordIfLive := func(val ssa.Value, useBlock *ssa.BasicBlock, useIdx int) bool {
		if val == nil || val == splitCall {
			return true
		}
		switch v := val.(type) {
		case *ssa.Parameter, *ssa.Const, *ssa.Global, *ssa.Function, *ssa.Builtin:
			return true
		case ssa.Instruction:
			defBlock := v.Block()
			if defBlock == nil {
				return false
			}
			if defBlock == splitBlock {
				defIdx := -1
				for idx, ins := range splitBlock.Instrs {
					if ins == v {
						defIdx = idx
						break
					}
				}
				if defIdx >= 0 && defIdx < splitCallIdx {
					if useBlock == splitBlock && useIdx < splitCallIdx {
						return true
					}
				} else if dom[useBlock][defBlock] {
					return true
				}
			} else if dom[useBlock][defBlock] {
				return true
			}
			if _, isAlloc := v.(*ssa.Alloc); isAlloc {
				return false
			}
			if _, isTuple := val.Type().(*types.Tuple); isTuple {
				return false
			}
			if !needed[val] {
				needed[val] = true
				ordered = append(ordered, val)
			}
			return true
		default:
			return false
		}
	}

	for _, blk := range allBlocks {
		if !reach[blk] {
			continue
		}
		start := 0
		if blk == splitBlock && !splitBlockReentered {
			start = splitCallIdx + 1
		}
		for idx := start; idx < len(blk.Instrs); idx++ {
			ins := blk.Instrs[idx]
			if _, isDbg := ins.(*ssa.DebugRef); isDbg {
				continue
			}
			if phi, ok := ins.(*ssa.Phi); ok {
				for eIdx, edge := range phi.Edges {
					pred := blk.Preds[eIdx]
					if plan.reachableBlocks[pred] && !recordIfLive(edge, pred, len(pred.Instrs)) {
						return nil, nil, false
					}
				}
				continue
			}
			scratch.operands = ins.Operands(scratch.inline[:0])
			for _, op := range scratch.operands {
				if op != nil && !recordIfLive(*op, blk, idx) {
					return nil, nil, false
				}
			}
		}
	}
	if len(ordered) > coroSyncRampMaxLiveValues {
		return nil, nil, false
	}
	augCFG := &coroSyncSlowAugCFG{
		reach:               reach,
		dom:                 dom,
		splitBlock:          splitBlock,
		splitCallIdx:        splitCallIdx,
		splitBlockReentered: splitBlockReentered,
	}
	return ordered, augCFG, true
}
