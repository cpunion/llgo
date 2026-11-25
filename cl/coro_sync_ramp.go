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
	"sort"

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

type coroPhysicalSyncRampPlan struct {
	kind            coroSyncRampKind
	hasTryHelper    bool
	root            *coroSyncFastNode
	slowTargets     []coroSyncSlowTarget
	liveValues      []ssa.Value
	liveInByTag     map[uint8]map[ssa.Value]bool
	slowAugCFG      *coroSyncSlowAugCFG
	singleAwaitCall *ssa.Call
	singleAwaitPlan coroPhysicalInstructionPlan
	needsSlowSplit  bool
}

func (plan *coroPhysicalSyncRampPlan) returnsNullableHandle() bool {
	return plan != nil && plan.kind != coroSyncRampNone && plan.kind != coroSyncRampCallerInlineYield
}

const (
	coroSyncRampMaxNodes        = 48
	coroSyncRampMaxDepth        = 16
	coroSyncRampMaxInstrs       = 160
	coroSyncRampMaxLiveValues   = 4
	coroSyncRampMaxSlowTargets  = 6
	coroSyncRampMaxOutcomeCalls = 2
	coroSyncRampInlineMaxInstrs = 28
)

const coroOutcomePropagateHelperNameV1 = "__llgo_coro_outcome_propagate_v1"

// coroSyncPlanner bundles read-only frozen planning context shared across all
// sync-fast ramp passes. It consumes only frozen ProgramIR/SSAPlan facts and
// never matches package paths, function names, or source digests.
type coroSyncPlanner struct {
	whole              *coro.SSAPlan
	ir                 *coroProgramIR
	roots              map[*ssa.Function]bool
	resolve            func(*ssa.Function) (*ssa.Function, bool)
	needsRecover       func(*ssa.Function) bool
	reflectCheck       func(*ssa.CallCommon) bool
	targetHasSyncRamp  func(*ssa.Function) bool
	targetHasTryHelper func(*ssa.Function) bool
}

// planCoroPhysicalSyncRamps computes general nullable-handle sync-fast ramps
// across all staged physical function plans to a fixed point before ProgramIR
// seals them.
func planCoroPhysicalSyncRamps(
	stage *coroPhysicalPlanStage,
	whole *coro.SSAPlan,
	ir *coroProgramIR,
	resolve func(*ssa.Function) (*ssa.Function, bool),
	needsRecover func(*ssa.Function) bool,
	reflectCheck func(*ssa.CallCommon) bool,
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
		whole:        whole,
		ir:           ir,
		roots:        roots,
		resolve:      resolve,
		needsRecover: needsRecover,
		reflectCheck: reflectCheck,
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
	}

	// Converge in up to 4 passes: standalone ramps first, then outer wrappers
	// calling .try or nullable-handle sync-ramp callees.
	for pass := 0; pass < 4; pass++ {
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
}

func (pl *coroSyncPlanner) canonicalFunc(fn *ssa.Function) *ssa.Function {
	if fn != nil && pl.resolve != nil {
		if canonical, ok := pl.resolve(fn); ok && canonical != nil {
			return canonical
		}
	}
	return fn
}

func (pl *coroSyncPlanner) planOne(plan *coroPhysicalFunctionPlan) *coroPhysicalSyncRampPlan {
	if plan == nil || plan.function == nil || plan.owner == nil ||
		plan.tailForward != nil || plan.cleanup != nil || plan.critical != nil {
		return nil
	}
	fn := plan.function
	if len(fn.FreeVars) != 0 || len(fn.Blocks) == 0 ||
		fn.Signature == nil || fn.Signature.Variadic() ||
		hasNoInlineDirective(fn) || isCgoExternSymbol(fn) ||
		pl.roots[fn] || fn.Name() == "init" || fn.Name() == "main" {
		return nil
	}
	logical, planned := pl.whole.FunctionPlan(fn)
	if !planned || logical.External != coro.Defined ||
		logical.Emission != coro.EmitCoroutine ||
		logical.ManagedEntry != coro.ManagedEntryCoroutine ||
		logical.Primary != coro.PrimaryCoroutine ||
		logical.Recursive {
		return nil
	}
	// Mirror coro_abi.go's deadCleanupRecover proof: fn.Recover is only live
	// when ProgramIR retains NeedsCleanupFrame.
	if fn.Recover != nil && logical.Exec.Contains(coro.NeedsCleanupFrame) {
		return nil
	}
	preamble, err := pl.ir.functionPreambleForOwner(fn, plan.owner)
	if err != nil || len(preamble.managedRuntimeHelpers) != 0 {
		return nil
	}
	if !coroSyncRampFunctionHasSlowWork(plan, pl.ir) {
		return nil
	}

	cyclic := cyclicBlocks(fn.Blocks)

	if ramp := pl.planCallerInlineYield(plan, cyclic); ramp != nil {
		return ramp
	}
	// If DirectFastCFG matches only a pure entry guard (no atomics, calls, or
	// tail-calls), prefer SingleAwait when available so wrappers like
	// io.ReadAtLeast fast-path their awaited call too.
	directRamp := pl.planDirectFastCFG(plan, cyclic)
	if directRamp != nil && coroSyncDirectRampHasWork(directRamp) {
		return directRamp
	}
	if ramp := pl.planSingleAwait(plan, cyclic); ramp != nil {
		return ramp
	}
	return nil
}

func coroSyncDirectRampHasWork(ramp *coroPhysicalSyncRampPlan) bool {
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
	if fnSig == nil || targetSig == nil || !types.Identical(fnSig.Results(), targetSig.Results()) {
		return nil, false
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
		expected = append(expected, call)
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
	zeroInts map[ssa.Value]bool,
) (mayMutate bool, needsGuardBail bool, ok bool) {
	if !phys.semantic.evaluated || phys.elideValue ||
		phys.control != coroPhysicalControlNone ||
		phys.operation != coroPhysicalOperationNone ||
		phys.outcome != coroPhysicalOutcomeNone {
		return false, false, false
	}
	switch v := instr.(type) {
	case *ssa.DebugRef, *ssa.Phi:
		return false, false, true
	case *ssa.RunDefers:
		return false, false, plan.cleanup == nil
	case *ssa.Extract, *ssa.Field, *ssa.ChangeType:
		return false, false, phys.recipe == coroPhysicalInstructionOrdinary
	case *ssa.FieldAddr:
		if phys.recipe == coroPhysicalInstructionOrdinary {
			return false, false, true
		}
		return false, phys.nilGuard, phys.recipe == coroPhysicalInstructionFieldAddr
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
		if !isIface || !iface.Empty() {
			return false, false, false
		}
		if isUntypedNilConst(v.X) {
			return false, false, true
		}
		src := types.Unalias(v.X.Type()).Underlying()
		if _, isPtr := src.(*types.Pointer); isPtr {
			return false, false, true
		}
		b, isBasic := src.(*types.Basic)
		return false, false, isBasic && b.Kind() == types.UnsafePointer
	case *ssa.Slice:
		// Accept ONLY proven zero-call identity reslicing x[0:] on a slice
		// (e.g. buf[n:] on peeled iteration 1 where n==0). Non-identity slices
		// lower to runtime NewSlice3Bounds ($outcome) and must stay in $coro.slow.
		if phys.recipe != coroPhysicalInstructionOrdinary && phys.recipe != coroPhysicalInstructionSlice {
			return false, false, false
		}
		if _, isSlice := types.Unalias(v.X.Type()).Underlying().(*types.Slice); !isSlice {
			return false, false, false
		}
		if v.High != nil || v.Max != nil {
			return false, false, false
		}
		isZeroLow := v.Low == nil || coroSyncIsZeroIntConst(v.Low) || zeroInts[v.Low]
		return false, false, isZeroLow
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
		if alloc, local := v.Addr.(*ssa.Alloc); local && !alloc.Heap && alloc.Parent() == plan.function {
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

func coroSyncIsDirectOutcomeCall(call *ssa.Call, phys coroPhysicalInstructionPlan, reflectCheck func(*ssa.CallCommon) bool) bool {
	if phys.control != coroPhysicalControlDirectOutcome || !phys.directOutcomeNativeResult ||
		phys.controlReceiver != nil || phys.controlTarget == nil ||
		len(phys.controlTarget.FreeVars) != 0 || phys.recoverAlias ||
		(reflectCheck != nil && reflectCheck(call.Common())) {
		return false
	}
	raw, ok := call.Common().Value.(*ssa.Function)
	if !ok || raw != phys.controlTarget {
		return false
	}
	// Never inline/explore noreturn trap helpers (e.g. fatal/throw) on the fast path.
	for _, blk := range phys.controlTarget.Blocks {
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
				if coroSyncIsDirectOutcomeCall(call, phys, pl.reflectCheck) || isPureLenCapBuiltin(call) || pl.plainGoCallAllowed(call, phys) {
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
			if _, guardBail, ok := coroSyncFastPureInstrAllowed(plan, ins, phys, false, nil); !ok || guardBail {
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
	) (*coroSyncFastNode, bool)

	explore = func(
		blk, pred *ssa.BasicBlock,
		mutated bool,
		depth, outcomeCalls int,
		visited map[*ssa.BasicBlock]bool,
		known map[string]bool,
		zeroInts map[ssa.Value]bool,
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
					next, useful := explore(blk.Succs[0], blk, localMutated, depth+1, localOutcomeCalls, nextVis, localKnown, localZeroInts)
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
						next, useful := explore(succ, blk, localMutated, depth+1, localOutcomeCalls, nextVis, localKnown, localZeroInts)
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
					tNode, tUseful := explore(blk.Succs[0], blk, trueMut, depth+1, localOutcomeCalls, nextVis, coroSyncCloneKnown(localKnown, term.Cond, true), localZeroInts)
					fNode, fUseful := explore(blk.Succs[1], blk, falseMut, depth+1, localOutcomeCalls, nextVis, coroSyncCloneKnown(localKnown, term.Cond, false), localZeroInts)
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
					node.endInstr, node.exitKind, node.tailCall, node.tailTarget = i, coroSyncFastExitTailCall, call, target
					return node, true
				}
				if phys.control == coroPhysicalControlDirectAwait && phys.controlClosure == nil &&
					phys.controlTarget != nil && !localMutated && pl.targetHasTryHelper(phys.controlTarget) &&
					(pl.reflectCheck == nil || !pl.reflectCheck(call.Common())) {
					if raw, ok := call.Common().Value.(*ssa.Function); ok && raw == phys.controlTarget {
						if node.tryCalls == nil {
							node.tryCalls = make(map[*ssa.Call]*ssa.Function)
						}
						node.tryCalls[call] = phys.controlTarget
						localMutated = true
						node.nodeHasNonCASMutation = true
						continue
					}
				}
				if coroSyncIsDirectOutcomeCall(call, phys, pl.reflectCheck) {
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
					localMutated = true
					node.nodeHasNonCASMutation = true
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
				continue
			}
			mayMut, guardBail, ok := coroSyncFastPureInstrAllowed(plan, ins, phys, true, localZeroInts)
			if !ok {
				return bailOrSlow()
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

	root, useful := explore(fn.Blocks[0], nil, false, 0, 0, make(map[*ssa.BasicBlock]bool), make(map[string]bool), make(map[ssa.Value]bool))
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
	if tooManySlow || !hasReturn {
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
// Strategy 3: SingleAwait (pure prefix + 1 pass-through awaited call + post-call)
// -----------------------------------------------------------------------------

func (pl *coroSyncPlanner) planSingleAwait(
	plan *coroPhysicalFunctionPlan,
	cyclic map[*ssa.BasicBlock]bool,
) *coroPhysicalSyncRampPlan {
	fn := plan.function
	fnSig := coroPhysicalNormalizeSourceSignature(fn.Signature)
	if fnSig == nil || fnSig.Results().Len() == 0 {
		return nil
	}

	isCandidate := func(call *ssa.Call, phys coroPhysicalInstructionPlan) bool {
		if call == nil || call.Common() == nil || phys.recoverAlias ||
			phys.operation != coroPhysicalOperationNone || phys.outcome != coroPhysicalOutcomeNone ||
			phys.controlFailure != "" || (pl.reflectCheck != nil && pl.reflectCheck(call.Common())) {
			return false
		}
		var callSig *types.Signature
		switch phys.control {
		case coroPhysicalControlDirectAwait:
			raw, ok := call.Common().Value.(*ssa.Function)
			if phys.controlClosure != nil || phys.controlTarget == nil || phys.controlTarget == fn ||
				!ok || raw != phys.controlTarget || !pl.targetHasSyncRamp(phys.controlTarget) {
				return false
			}
			callSig = coroPhysicalNormalizeSourceSignature(phys.controlTarget.Signature)
		case coroPhysicalControlExactInterfaceAwait:
			if phys.controlTarget == nil || phys.controlTarget == fn || phys.controlReceiver == nil ||
				!pl.targetHasSyncRamp(phys.controlTarget) {
				return false
			}
			callSig = coroPhysicalNormalizeSourceSignature(phys.controlTarget.Signature)
		case coroPhysicalControlManagedInterfaceAwait:
			if phys.controlSignature == nil || phys.dispatchEntry == coro.ManagedEntryPlain || !phys.rawInterfaceReceiver {
				return false
			}
			callSig = coroPhysicalNormalizeSourceSignature(phys.controlSignature)
		case coroPhysicalControlDispatchAwait:
			if phys.dispatchEntry == coro.ManagedEntryPlain {
				return false
			}
			callSig = coroPhysicalNormalizeSourceSignature(call.Common().Signature())
		default:
			return false
		}
		return callSig != nil && types.Identical(callSig.Results(), fnSig.Results())
	}

	nodesUsed := 0
	var chosenCall *ssa.Call
	var chosenPlan coroPhysicalInstructionPlan
	postHasReturn := false

	bumpVisit := func(src map[*ssa.BasicBlock]int, blk *ssa.BasicBlock) map[*ssa.BasicBlock]int {
		dst := cloneMap(src)
		dst[blk]++
		return dst
	}

	var explorePost func(blk, pred *ssa.BasicBlock, startIdx, depth int, visits map[*ssa.BasicBlock]int, known map[string]bool, zeroInts map[ssa.Value]bool) *coroSyncFastNode
	explorePost = func(blk, pred *ssa.BasicBlock, startIdx, depth int, visits map[*ssa.BasicBlock]int, known map[string]bool, zeroInts map[ssa.Value]bool) *coroSyncFastNode {
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
					node.exitKind, node.nextNode = coroSyncFastExitJump, explorePost(blk.Succs[0], blk, 0, depth+1, bumpVisit(visits, blk), known, localZeroInts)
					return node
				case *ssa.If:
					nv := bumpVisit(visits, blk)
					if kv, ok := coroSyncStaticBranch(term, pl.ir, known); ok {
						succ := blk.Succs[1]
						if kv {
							succ = blk.Succs[0]
						}
						node.exitKind, node.nextNode = coroSyncFastExitJump, explorePost(succ, blk, 0, depth+1, nv, known, localZeroInts)
						return node
					}
					node.exitKind, node.cond = coroSyncFastExitIf, term.Cond
					node.trueNode = explorePost(blk.Succs[0], blk, 0, depth+1, nv, coroSyncCloneKnown(known, term.Cond, true), localZeroInts)
					node.falseNode = explorePost(blk.Succs[1], blk, 0, depth+1, nv, coroSyncCloneKnown(known, term.Cond, false), localZeroInts)
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
				continue
			}
			if call, ok := ins.(*ssa.Call); ok {
				if isPureLenCapBuiltin(call) {
					continue
				}
				return fallback
			}
			if mayMut, guardBail, ok := coroSyncFastPureInstrAllowed(plan, ins, phys, false, localZeroInts); !ok || mayMut || guardBail {
				return fallback
			}
		}
		return fallback
	}

	var explorePre func(blk, pred *ssa.BasicBlock, depth int, visits map[*ssa.BasicBlock]int, known map[string]bool, zeroInts map[ssa.Value]bool) *coroSyncFastNode
	explorePre = func(blk, pred *ssa.BasicBlock, depth int, visits map[*ssa.BasicBlock]int, known map[string]bool, zeroInts map[ssa.Value]bool) *coroSyncFastNode {
		bail := &coroSyncFastNode{exitKind: coroSyncFastExitBailStart}
		if blk == nil || !plan.reachableBlocks[blk] || depth > coroSyncRampMaxDepth ||
			nodesUsed >= coroSyncRampMaxNodes || visits[blk] >= 1 {
			return bail
		}
		nodesUsed++
		localZeroInts := cloneMap(zeroInts)
		node := &coroSyncFastNode{block: blk, predBlock: pred, endInstr: -1}
		for i, ins := range blk.Instrs {
			if i == len(blk.Instrs)-1 {
				switch term := ins.(type) {
				case *ssa.Return:
					node.exitKind, node.retInstr = coroSyncFastExitReturn, term
					return node
				case *ssa.Jump:
					node.exitKind, node.nextNode = coroSyncFastExitJump, explorePre(blk.Succs[0], blk, depth+1, bumpVisit(visits, blk), known, localZeroInts)
					return node
				case *ssa.If:
					nv := bumpVisit(visits, blk)
					if kv, ok := coroSyncStaticBranch(term, pl.ir, known); ok {
						succ := blk.Succs[1]
						if kv {
							succ = blk.Succs[0]
						}
						node.exitKind, node.nextNode = coroSyncFastExitJump, explorePre(succ, blk, depth+1, nv, known, localZeroInts)
						return node
					}
					node.exitKind, node.cond = coroSyncFastExitIf, term.Cond
					node.trueNode = explorePre(blk.Succs[0], blk, depth+1, nv, coroSyncCloneKnown(known, term.Cond, true), localZeroInts)
					node.falseNode = explorePre(blk.Succs[1], blk, depth+1, nv, coroSyncCloneKnown(known, term.Cond, false), localZeroInts)
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
				continue
			}
			if call, ok := ins.(*ssa.Call); ok {
				if isPureLenCapBuiltin(call) {
					continue
				}
				if pl.plainGoCallAllowed(call, phys) && coroSyncPlainHelperIsPure(pl.canonicalFunc(call.Common().StaticCallee()), pl.whole) {
					continue
				}
				if isCandidate(call, phys) {
					if chosenCall != nil {
						return bail
					}
					chosenCall, chosenPlan = call, phys
					node.endInstr, node.singleAwait = i, call
					// Fresh branch facts post-call so revisited loop headers re-evaluate Phis.
					node.singleAwaitPost = explorePost(blk, pred, i+1, depth+1, bumpVisit(visits, blk), make(map[string]bool), make(map[ssa.Value]bool))
					return node
				}
				return bail
			}
			if mayMut, _, ok := coroSyncFastPureInstrAllowed(plan, ins, phys, false, localZeroInts); !ok || mayMut {
				return bail
			}
		}
		return bail
	}

	root := explorePre(fn.Blocks[0], nil, 0, make(map[*ssa.BasicBlock]int), make(map[string]bool), make(map[ssa.Value]bool))
	if root == nil || chosenCall == nil || !postHasReturn {
		return nil
	}
	liveValues, liveInByTag, slowAugCFG, ok := collectCoroSyncSingleAwaitLiveValues(plan, chosenCall)
	if !ok || !coroSyncFastTreeCoversLiveIns(root, liveInByTag) {
		return nil
	}
	return &coroPhysicalSyncRampPlan{
		kind:            coroSyncRampSingleAwait,
		root:            root,
		liveValues:      liveValues,
		liveInByTag:     liveInByTag,
		slowAugCFG:      slowAugCFG,
		singleAwaitCall: chosenCall,
		singleAwaitPlan: chosenPlan,
		needsSlowSplit:  true,
	}
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
				*ssa.Extract, *ssa.Convert, *ssa.ChangeType, *ssa.If, *ssa.Jump, *ssa.Return:
				if un, isUn := v.(*ssa.UnOp); isUn && un.Op == token.ARROW {
					return false
				}
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

// -----------------------------------------------------------------------------
// Emission: $coro.slow, $coro.try, nullable-handle $coro ramp, outcome propagator
// -----------------------------------------------------------------------------

type coroSyncSlowResumeState struct {
	plan            *coroPhysicalSyncRampPlan
	resumeTagParam  llssa.Expr
	preChildParam   llssa.Expr
	liveParams      []llssa.Expr
	liveSlots       map[ssa.Value]llssa.Expr
	resumeAwaitBlk  llssa.BasicBlock
	resumePostBlk   llssa.BasicBlock
	singleAwaitDone bool
}

// dominatesDebugUse reports whether iv's LLVM SSA definition dominates ref
// inside the augmented CFG of $coro.slow (after adding slow-resume entry edges).
func (s *coroSyncSlowResumeState) dominatesDebugUse(iv instrOrValue, ref *ssa.DebugRef) bool {
	if s == nil || s.plan == nil || s.plan.slowAugCFG == nil {
		return true
	}
	cfg := s.plan.slowAugCFG
	defBlock, useBlock := iv.Block(), ref.Block()
	if defBlock == nil || useBlock == nil {
		return false
	}
	if defBlock == useBlock {
		if cfg.splitBlock != nil && defBlock == cfg.splitBlock {
			if iv == s.plan.singleAwaitCall {
				return true
			}
			defIdx, useIdx := -1, -1
			for i, ins := range defBlock.Instrs {
				if ins == iv {
					defIdx = i
				}
				if ins == ref {
					useIdx = i
				}
			}
			if defIdx < 0 || useIdx < 0 {
				return false
			}
			return (defIdx < cfg.splitCallIdx && useIdx < cfg.splitCallIdx) ||
				(defIdx > cfg.splitCallIdx && useIdx > cfg.splitCallIdx)
		}
		return true
	}
	if !cfg.reach[useBlock] {
		return true
	}
	if cfg.splitBlock != nil && defBlock == cfg.splitBlock && iv != s.plan.singleAwaitCall {
		for i, ins := range defBlock.Instrs {
			if ins == iv {
				if i < cfg.splitCallIdx {
					return false
				}
				break
			}
		}
	}
	return cfg.dom[useBlock][defBlock]
}

type coroSyncEmitEnv struct {
	vals map[ssa.Value]llssa.Expr
	zero map[ssa.Value]bool
}

func newCoroSyncEmitEnv() coroSyncEmitEnv {
	return coroSyncEmitEnv{
		vals: make(map[ssa.Value]llssa.Expr),
		zero: make(map[ssa.Value]bool),
	}
}

func (e coroSyncEmitEnv) clone() coroSyncEmitEnv {
	return coroSyncEmitEnv{
		vals: cloneMap(e.vals),
		zero: cloneMap(e.zero),
	}
}

func coroSyncSlowPhysicalSig(baseSig *types.Signature, ramp *coroPhysicalSyncRampPlan) *types.Signature {
	if ramp == nil || (len(ramp.slowTargets) == 0 && ramp.kind != coroSyncRampSingleAwait) {
		return baseSig
	}
	params := make([]*types.Var, 0, baseSig.Params().Len()+2+len(ramp.liveValues))
	for i := 0; i < baseSig.Params().Len(); i++ {
		params = append(params, baseSig.Params().At(i))
	}
	params = append(params, types.NewParam(token.NoPos, nil, "__llgo_resume_tag", types.Typ[types.Uint8]))
	if ramp.kind == coroSyncRampSingleAwait {
		params = append(params, types.NewParam(token.NoPos, nil, "__llgo_pre_child", types.Typ[types.UnsafePointer]))
	}
	for i, v := range ramp.liveValues {
		params = append(params, types.NewParam(token.NoPos, nil, fmt.Sprintf("__llgo_live_%d", i), v.Type()))
	}
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), baseSig.Results(), false)
}

func coroSyncTryHelperSig(sourceSig *types.Signature) *types.Signature {
	sourceSig = coroPhysicalNormalizeSourceSignature(sourceSig)
	params := make([]*types.Var, 0, sourceSig.Params().Len()+2)
	params = append(params,
		types.NewParam(token.NoPos, nil, "__llgo_g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "__llgo_out", types.Typ[types.UnsafePointer]),
	)
	for i := 0; i < sourceSig.Params().Len(); i++ {
		params = append(params, sourceSig.Params().At(i))
	}
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "__llgo_ok", types.Typ[types.Bool]))
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), results, false)
}

func (p *context) coroSyncTryHelperFunc(target *ssa.Function) llssa.Function {
	entry := p.mustFunctionSymbol(target)
	coroFn, _, kind := p.compileFunctionEntry(entry)
	if kind != goFunc || coroFn == nil {
		panic("sync-ramp .try callee did not resolve to a Go coroutine entry")
	}
	sourceSig, err := p.emissionUniverse.coroPhysicalSourceSignature(target)
	if err != nil {
		panic(fmt.Errorf("sync-ramp .try callee signature: %w", err))
	}
	return p.pkg.NewFuncEx(coroFn.Name()+".try", coroSyncTryHelperSig(sourceSig), llssa.InGo, false, false)
}

// compileCoroSyncRampPhysicalBody emits $coro.slow (reusing the standard
// coroutine physical body emitter), an optional $coro.try helper, and the
// plain nullable-handle $coro ramp.
func (p *context) compileCoroSyncRampPhysicalBody(
	b llssa.Builder,
	fn *ssa.Function,
	physicalPlan *coroPhysicalFunctionPlan,
	abi coroPhysicalABI,
	sourceParamBase int,
	isInit bool,
) {
	ramp := physicalPlan.syncRamp
	if ramp == nil || !ramp.needsSlowSplit {
		panic("compileCoroSyncRampPhysicalBody requires a split sync-fast ramp plan")
	}
	rampFn := p.fn
	slowSig := coroSyncSlowPhysicalSig(abi.physicalSig, ramp)
	slowFn := p.pkg.NewFuncEx(rampFn.Name()+".slow", slowSig, llssa.InGo, false, true)
	// Keep the cold coroutine-allocating .slow ramp out of the hot sync-fast
	// entry so the fast path stays leaf-like with minimal register spills.
	slowFn.Inline(llssa.NoInline)

	// 1. Materialize $coro.slow via the standard coroutine physical body emitter.
	if !slowFn.HasBody() {
		savedFn, savedBvals, savedAddrs := p.fn, p.bvals, p.coroValueAddrs
		savedPhis, savedBlkInfos, savedSlow := p.phis, p.blkInfos, p.coroSyncSlowResume
		savedDebugDIVars := p.debugDIVars

		p.fn = slowFn
		p.bvals = make(map[ssa.Value]llssa.Expr)
		p.coroValueAddrs = make(map[ssa.Value]llssa.Expr)
		p.phis, p.blkInfos = nil, nil
		if savedDebugDIVars != nil {
			p.debugDIVars = make(map[*types.Var]llssa.DIVar)
		}

		if len(ramp.slowTargets) > 0 || ramp.kind == coroSyncRampSingleAwait {
			extraBase := abi.physicalSig.Params().Len()
			state := &coroSyncSlowResumeState{
				plan:           ramp,
				resumeTagParam: slowFn.PhysicalParam(extraBase),
			}
			liveBase := extraBase + 1
			if ramp.kind == coroSyncRampSingleAwait {
				state.preChildParam = slowFn.PhysicalParam(extraBase + 1)
				liveBase = extraBase + 2
			}
			if len(ramp.liveValues) > 0 {
				state.liveParams = make([]llssa.Expr, len(ramp.liveValues))
				for i := range ramp.liveValues {
					state.liveParams[i] = slowFn.PhysicalParam(liveBase + i)
				}
			}
			p.coroSyncSlowResume = state
		}

		slowBuilder := slowFn.MakeBody(1)
		if p.options.Debug && p.goProg != nil && p.goProg.Fset != nil {
			pos := p.goProg.Fset.Position(fn.Pos())
			bodyPos := p.getFuncBodyPos(fn)
			logicalName := slowFn.Name()
			if fn.Pkg != nil && fn.Pkg.Pkg != nil {
				logicalName = funcInfoDisplayName(funcName(fn.Pkg.Pkg, fn, false))
			}
			slowBuilder.DebugFunctionNamed(slowFn, logicalName, slowFn.Name(), debugFunctionScope(fn), pos, bodyPos)
		}
		p.compileCoroPhysicalBodyCore(slowBuilder, fn, physicalPlan, abi, sourceParamBase, isInit)
		slowBuilder.EndBuild()
		slowBuilder.Dispose()

		p.debugDIVars = savedDebugDIVars
		p.coroSyncSlowResume, p.blkInfos, p.phis = savedSlow, savedBlkInfos, savedPhis
		p.coroValueAddrs, p.bvals, p.fn = savedAddrs, savedBvals, savedFn
	}

	nullPtr := p.prog.Nil(p.prog.VoidPtr())

	// 2. If hasTryHelper, materialize F$coro.try(g, out, args...) -> bool and
	// make F$coro a thin wrapper around F$coro.try + F$coro.slow.
	if ramp.hasTryHelper {
		sourceSig, err := p.emissionUniverse.coroPhysicalSourceSignature(fn)
		if err != nil {
			panic(fmt.Errorf("sync-ramp .try signature: %w", err))
		}
		_, isLinkOnce := p.linkOnceFns[fn]
		tryLinkOnce := isLinkOnce || fn.Synthetic != "" || fn.Origin() != nil || len(fn.TypeArgs()) != 0
		tryFn := p.pkg.NewFuncEx(rampFn.Name()+".try", coroSyncTryHelperSig(sourceSig), llssa.InGo, false, tryLinkOnce)
		if !tryFn.HasBody() {
			savedFn := p.fn
			p.fn = tryFn
			tb := tryFn.MakeBody(1)
			tb.SetBlock(tryFn.Block(0))
			emitter := &coroSyncFastEmitter{
				p: p, b: tb, fn: fn, plan: physicalPlan, ramp: ramp, abi: abi,
				task: tryFn.PhysicalParam(0), resultSlot: tryFn.PhysicalParam(1), tryMode: true,
			}
			emitter.initParams(tryFn, sourceParamBase)
			emitter.emitNode(ramp.root, newCoroSyncEmitEnv())
			tb.EndBuild()
			tb.Dispose()
			p.fn = savedFn
		}

		b.SetBlock(rampFn.Block(0))
		paramCount := abi.physicalSig.Params().Len()
		args := make([]llssa.Expr, paramCount)
		for i := 0; i < paramCount; i++ {
			args[i] = rampFn.PhysicalParam(i)
		}
		fastBlk, slowBlk, doneBlk := rampFn.MakeBlock(), rampFn.MakeBlock(), rampFn.MakeBlock()
		b.IfWithBranchWeights(b.BinOp(token.NEQ, args[1], nullPtr), fastBlk, slowBlk, 1000, 1)

		b.SetBlockEx(fastBlk, llssa.AtEnd, false)
		b.IfWithBranchWeights(b.Call(tryFn.Expr, args...), doneBlk, slowBlk, 1000, 1)

		b.SetBlockEx(doneBlk, llssa.AtEnd, false)
		b.Return(nullPtr)

		b.SetBlockEx(slowBlk, llssa.AtEnd, false)
		if abi.resultCount == 0 {
			args[1] = nullPtr
		}
		b.Return(b.Call(slowFn.Expr, args...))
		return
	}

	// 3. Emit the plain nullable-handle F$coro ramp directly.
	b.SetBlock(rampFn.Block(0))
	g, out := rampFn.PhysicalParam(0), rampFn.PhysicalParam(1)
	fastEntry, bailStart := rampFn.MakeBlock(), rampFn.MakeBlock()
	b.IfWithBranchWeights(b.BinOp(token.NEQ, out, nullPtr), fastEntry, bailStart, 1000, 1)

	b.SetBlockEx(bailStart, llssa.AtEnd, false)
	emitter := &coroSyncFastEmitter{
		p: p, b: b, fn: fn, plan: physicalPlan, ramp: ramp, abi: abi,
		task: g, resultSlot: out, slowFn: slowFn, bailStart: bailStart,
	}
	emitter.initParams(rampFn, sourceParamBase)
	emitter.emitSlowCall(0, nullPtr, nil)

	b.SetBlockEx(fastEntry, llssa.AtEnd, false)
	emitter.emitNode(ramp.root, newCoroSyncEmitEnv())
}

func (p *context) compileCoroCallerInlineYieldAwait(
	b llssa.Builder,
	target *ssa.Function,
	targetPlan *coroPhysicalFunctionPlan,
	args []llssa.Expr,
) {
	ramp := targetPlan.syncRamp
	if ramp == nil || ramp.kind != coroSyncRampCallerInlineYield {
		panic("caller-inline-yield await requires a frozen yield-inline plan")
	}
	doneBlk := p.fn.MakeBlock()
	emitter := &coroSyncFastEmitter{
		p: p, b: b, fn: target, plan: targetPlan, ramp: ramp,
		task: p.coroTask(), callerInline: true, callerDoneBlk: doneBlk, callerArgVals: args,
	}
	emitter.emitNode(ramp.root, newCoroSyncEmitEnv())
	b.SetBlockContinuation(doneBlk)
}

type coroSyncFastEmitter struct {
	p                 *context
	b                 llssa.Builder
	fn                *ssa.Function
	plan              *coroPhysicalFunctionPlan
	ramp              *coroPhysicalSyncRampPlan
	abi               coroPhysicalABI
	task              llssa.Expr
	resultSlot        llssa.Expr
	slowFn            llssa.Function
	bailStart         llssa.BasicBlock
	paramExprs        []llssa.Expr
	tryMode           bool
	callerInline      bool
	callerDoneBlk     llssa.BasicBlock
	callerArgVals     []llssa.Expr
	localAllocs       map[*ssa.Alloc]llssa.Expr
	singleAwaitPreEnv map[ssa.Value]llssa.Expr
	outcomeCompletion llssa.Expr
	traceDescriptor   llssa.Expr
}

func (e *coroSyncFastEmitter) initParams(llFn llssa.Function, sourceParamBase int) {
	e.paramExprs = make([]llssa.Expr, len(e.fn.Params))
	for i := range e.fn.Params {
		e.paramExprs[i] = llFn.PhysicalParam(sourceParamBase + i)
	}
}

func (e *coroSyncFastEmitter) entryAlloca(typ llssa.Type) llssa.Expr {
	alloc := e.b.Func.NewBuilder()
	defer alloc.Dispose()
	alloc.SetBlockEx(e.b.Func.Block(0), llssa.AtStart, true)
	return alloc.AllocaT(typ)
}

func (e *coroSyncFastEmitter) completionSlot() llssa.Expr {
	if e.outcomeCompletion.IsNil() {
		e.outcomeCompletion = e.entryAlloca(outcomePlainCompletionType(e.p.prog))
	}
	return e.outcomeCompletion
}

func (e *coroSyncFastEmitter) descriptorPtr() llssa.Expr {
	if e.traceDescriptor.IsNil() {
		e.traceDescriptor = e.b.Convert(e.p.prog.VoidPtr(), e.p.materializeCoroFrameDescriptor(e.abi))
	}
	return e.traceDescriptor
}

func (e *coroSyncFastEmitter) emitBailStart() {
	if e.tryMode {
		e.b.Return(e.p.prog.BoolVal(false))
		return
	}
	e.b.Jump(e.bailStart)
}

func (e *coroSyncFastEmitter) emitSlowCall(tag uint8, preChild llssa.Expr, vals map[ssa.Value]llssa.Expr) {
	nullPtr := e.p.prog.Nil(e.p.prog.VoidPtr())
	outArg := e.resultSlot
	if e.abi.resultCount == 0 {
		outArg = nullPtr
	}
	args := make([]llssa.Expr, 0, len(e.paramExprs)+4+len(e.ramp.liveValues))
	args = append(args, e.task, outArg)
	args = append(args, e.paramExprs...)
	if len(e.ramp.slowTargets) > 0 || e.ramp.kind == coroSyncRampSingleAwait {
		u8 := e.p.type_(types.Typ[types.Uint8], llssa.InGo)
		args = append(args, e.p.prog.IntVal(uint64(tag), u8))
		if e.ramp.kind == coroSyncRampSingleAwait {
			if preChild.IsNil() {
				preChild = nullPtr
			}
			args = append(args, preChild)
		}
		required := e.ramp.liveInByTag[tag]
		for _, lv := range e.ramp.liveValues {
			if vals != nil {
				if val, ok := vals[lv]; ok {
					args = append(args, val)
					continue
				}
			}
			if tag != 0 && required[lv] {
				panic(fmt.Sprintf("sync-ramp slow exit tag=%d in %q missing live-in SSA value %s", tag, e.fn.String(), lv.String()))
			}
			args = append(args, e.p.prog.Zero(e.p.type_(lv.Type(), llssa.InGo)))
		}
	}
	e.b.Return(e.b.Call(e.slowFn.Expr, args...))
}

func (e *coroSyncFastEmitter) evalValue(v ssa.Value, vals map[ssa.Value]llssa.Expr) llssa.Expr {
	if val, ok := vals[v]; ok {
		return val
	}
	switch v := v.(type) {
	case *ssa.Parameter:
		for i, param := range e.fn.Params {
			if param == v {
				if e.callerInline {
					return e.callerArgVals[i]
				}
				return e.paramExprs[i]
			}
		}
		panic("sync-ramp parameter not found")
	case *ssa.Const:
		return e.b.Const(v.Value, e.p.type_(types.Default(v.Type()), llssa.InGo))
	case *ssa.Global:
		return e.p.varOf(e.b, v)
	case *ssa.Function:
		return e.p.compileValue(e.b, v)
	default:
		panic(fmt.Sprintf("sync-ramp missing SSA value %T %q", v, v.String()))
	}
}

func (e *coroSyncFastEmitter) evalValueAs(v ssa.Value, target types.Type, vals map[ssa.Value]llssa.Expr) llssa.Expr {
	if isUntypedNilConst(v) {
		return e.p.nilOf(target)
	}
	return e.evalValue(v, vals)
}

func (e *coroSyncFastEmitter) emitOutcomePropagateCall(status, typeWord, dataWord llssa.Expr, pos token.Pos) {
	if e.tryMode {
		panic("sync-ramp .try helper cannot emit handle-returning outcome propagation")
	}
	line := uint32(0)
	if e.p.fset != nil && pos.IsValid() {
		line = uint32(e.p.fset.Position(pos).Line)
	}
	handle := e.b.Call(
		e.p.coroOutcomePropagateCoroutine().Expr,
		e.task,
		e.p.prog.Nil(e.p.prog.VoidPtr()),
		e.descriptorPtr(),
		e.p.prog.IntVal(uint64(line), e.p.prog.Uint32()),
		status,
		typeWord,
		dataWord,
	)
	e.b.Return(handle)
}

func (e *coroSyncFastEmitter) emitNilGuard(ptr llssa.Expr, mutated bool, pos token.Pos) {
	isNil := e.b.BinOp(token.EQL, ptr, e.p.prog.Nil(ptr.Type))
	failBlk, contBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
	e.b.IfWithBranchWeights(isNil, failBlk, contBlk, 1, 1000)

	e.b.SetBlockEx(failBlk, llssa.AtEnd, false)
	if !mutated {
		e.emitBailStart()
	} else {
		nullPtr := e.p.prog.Nil(e.p.prog.VoidPtr())
		e.emitOutcomePropagateCall(
			e.p.prog.IntVal(coroAwaitCompletionFaultNil, e.p.prog.Uint32()),
			nullPtr, nullPtr, pos,
		)
	}
	e.b.SetBlockContinuation(contBlk)
}

func (e *coroSyncFastEmitter) emitNode(node *coroSyncFastNode, parentEnv coroSyncEmitEnv) {
	switch node.exitKind {
	case coroSyncFastExitBailStart:
		e.emitBailStart()
		return
	case coroSyncFastExitResumeSlow:
		e.emitSlowCall(node.resumeTag, llssa.Nil, parentEnv.vals)
		return
	case coroSyncFastExitResumePostCall:
		e.emitSlowCall(2, llssa.Nil, e.singleAwaitPreEnv)
		return
	}

	env := parentEnv.clone()
	end := node.endInstr
	if end < 0 {
		end = len(node.block.Instrs) - 1
	}
	mutated := node.mutated
	for i := node.startInstr; i < end; i++ {
		ins := node.block.Instrs[i]
		phys := e.plan.instructions[ins]
		switch v := ins.(type) {
		case *ssa.DebugRef, *ssa.RunDefers:
		case *ssa.Phi:
			edge := v.Edges[coroSyncPhiEdgeIndex(v, node.predBlock)]
			env.vals[v] = e.evalValueAs(edge, v.Type(), env.vals)
			env.zero[v] = coroSyncIsZeroIntConst(edge) || env.zero[edge]
		case *ssa.Extract:
			env.vals[v] = e.b.Extract(e.evalValue(v.Tuple, env.vals), v.Index)
		case *ssa.Field:
			env.vals[v] = e.b.Field(e.evalValue(v.X, env.vals), v.Field)
		case *ssa.FieldAddr:
			x := e.evalValue(v.X, env.vals)
			if phys.recipe == coroPhysicalInstructionFieldAddr && phys.nilGuard {
				e.emitNilGuard(x, mutated, v.Pos())
			}
			env.vals[v] = e.b.FieldAddrKnownNonNil(x, v.Field)
		case *ssa.BinOp:
			if phys.recipe == coroPhysicalInstructionInterfaceNilCompare {
				val := e.evalValue(phys.valueOperand, env.vals)
				env.vals[v] = e.b.BinOp(v.Op, e.b.InterfaceTypeWord(val), e.p.prog.Nil(e.p.prog.VoidPtr()))
			} else if cval, ok := foldConstComparison(v); ok {
				env.vals[v] = e.p.prog.BoolVal(cval)
			} else {
				x := e.evalValueAs(v.X, v.Y.Type(), env.vals)
				y := e.evalValueAs(v.Y, v.X.Type(), env.vals)
				switch v.Op {
				case token.QUO, token.REM:
					env.vals[v] = e.b.BinOpWithNonZeroDivisor(v.Op, x, y)
				case token.SHL, token.SHR:
					env.vals[v] = e.b.BinOpWithNonNegativeShiftCount(v.Op, x, y)
				default:
					env.vals[v] = e.b.BinOp(v.Op, x, y)
				}
			}
		case *ssa.UnOp:
			x := e.evalValue(v.X, env.vals)
			if v.Op == token.MUL {
				if phys.recipe == coroPhysicalInstructionDeref && phys.nilGuard {
					e.emitNilGuard(x, mutated, v.Pos())
				}
				t := e.p.type_(v.Type(), llssa.InGo)
				if t.RawType() != nil && e.p.prog.SizeOf(t) == 0 {
					env.vals[v] = e.p.prog.Zero(t)
				} else {
					env.vals[v] = e.b.LoadKnownNonNil(x)
				}
			} else {
				env.vals[v] = e.b.UnOp(v.Op, x)
			}
		case *ssa.Convert:
			if isUntypedNilConst(v.X) {
				env.vals[v] = e.p.nilOf(v.Type())
			} else {
				env.vals[v] = e.b.Convert(e.p.type_(v.Type(), llssa.InGo), e.evalValue(v.X, env.vals))
			}
		case *ssa.ChangeType:
			if isUntypedNilConst(v.X) {
				env.vals[v] = e.p.nilOf(v.Type())
			} else {
				env.vals[v] = e.b.ChangeType(e.p.type_(v.Type(), llssa.InGo), e.evalValue(v.X, env.vals))
			}
		case *ssa.MakeInterface:
			t := e.p.type_(v.Type(), llssa.InGo)
			if isUntypedNilConst(v.X) {
				env.vals[v] = e.p.prog.Nil(t)
			} else {
				env.vals[v] = e.b.MakeInterface(t, e.evalValue(v.X, env.vals))
			}
		case *ssa.Slice:
			// Planner proves every surviving Slice on the fast tree is an
			// identity reslice x[0:] with zero runtime helper calls.
			env.vals[v] = e.evalValue(v.X, env.vals)
		case *ssa.Alloc:
			elem := e.p.type_(v.Type().(*types.Pointer).Elem(), llssa.InGo)
			if e.localAllocs == nil {
				e.localAllocs = make(map[*ssa.Alloc]llssa.Expr)
			}
			slot, ok := e.localAllocs[v]
			if !ok {
				slot = e.entryAlloca(elem)
				e.localAllocs[v] = slot
			}
			e.b.StoreKnownNonNil(slot, e.p.prog.Zero(elem))
			env.vals[v] = slot
		case *ssa.Store:
			addr := e.evalValue(v.Addr, env.vals)
			val := e.evalValueAs(v.Val, v.Addr.Type().Underlying().(*types.Pointer).Elem(), env.vals)
			e.b.StoreKnownNonNil(addr, val)
			if alloc, local := v.Addr.(*ssa.Alloc); !local || alloc.Heap {
				mutated = true
			}
		case *ssa.Call:
			env.vals[v] = e.emitCall(v, phys, node, env.vals, &mutated)
		default:
			panic(fmt.Sprintf("sync-ramp unsupported instruction %T", ins))
		}
	}

	if node.singleAwait != nil {
		e.emitSingleAwait(node.singleAwait, node.singleAwaitPost, env)
		return
	}

	switch node.exitKind {
	case coroSyncFastExitReturn:
		e.emitFastReturn(node.retInstr, env.vals)
	case coroSyncFastExitTailCall:
		e.emitTailCall(node.tailCall, node.tailTarget, env.vals)
	case coroSyncFastExitJump:
		e.emitNode(node.nextNode, env)
	case coroSyncFastExitIf:
		cond := e.evalValue(node.cond, env.vals)
		tBlk, fBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
		e.b.If(cond, tBlk, fBlk)
		e.b.SetBlockEx(tBlk, llssa.AtEnd, false)
		e.emitNode(node.trueNode, env)
		e.b.SetBlockEx(fBlk, llssa.AtEnd, false)
		e.emitNode(node.falseNode, env)
	default:
		panic(fmt.Sprintf("sync-ramp unexpected exit kind %d", node.exitKind))
	}
}

func (e *coroSyncFastEmitter) evalCallArgs(call *ssa.Call, vals map[ssa.Value]llssa.Expr) []llssa.Expr {
	sig := coroPhysicalNormalizeSourceSignature(call.Common().Signature())
	if call.Common().IsInvoke() {
		sig = call.Common().Signature()
	}
	args := make([]llssa.Expr, len(call.Call.Args))
	for i, arg := range call.Call.Args {
		if sig != nil && i < sig.Params().Len() {
			args[i] = e.evalValueAs(arg, sig.Params().At(i).Type(), vals)
		} else {
			args[i] = e.evalValue(arg, vals)
		}
	}
	return args
}

func (e *coroSyncFastEmitter) emitCall(
	call *ssa.Call,
	phys coroPhysicalInstructionPlan,
	node *coroSyncFastNode,
	vals map[ssa.Value]llssa.Expr,
	mutated *bool,
) llssa.Expr {
	if isPureLenCapBuiltin(call) {
		return e.b.BuiltinCall(call.Common().Value.(*ssa.Builtin).Name(), e.evalValue(call.Call.Args[0], vals))
	}
	if constVal, isConst := node.constBools[call]; isConst {
		return e.p.prog.BoolVal(constVal)
	}
	if node.yieldCalls[call] {
		if !e.callerInline {
			panic("sync-ramp yield call requires caller-inline mode")
		}
		e.p.compileCoroYield(e.b)
		return llssa.Expr{}
	}
	if opcode, isAtomic := node.atomicOpcodes[call]; isAtomic {
		args := e.evalCallArgs(call, vals)
		if !coroSyncIsReadOnlyAtomicOpcode(opcode) {
			*mutated = true
		}
		return e.p.compileAtomicIntrinsic(e.b, opcode, args)
	}
	if spec, isInlineAtomic := node.inlineAtomics[call]; isInlineAtomic {
		args := e.evalCallArgs(call, vals)
		e.emitNilGuard(args[0], *mutated, call.Pos())
		if spec.fieldIndex >= 0 {
			args[0] = e.b.FieldAddrKnownNonNil(args[0], spec.fieldIndex)
		}
		if !coroSyncIsReadOnlyAtomicOpcode(spec.opcode) {
			*mutated = true
		}
		return e.p.compileAtomicIntrinsic(e.b, spec.opcode, args)
	}
	if tryTarget, isTry := node.tryCalls[call]; isTry {
		args := e.evalCallArgs(call, vals)
		tryFn := e.p.coroSyncTryHelperFunc(tryTarget)
		sourceSig, err := e.p.emissionUniverse.coroPhysicalSourceSignature(tryTarget)
		if err != nil {
			panic(err)
		}
		outSlot := e.p.prog.Nil(e.p.prog.VoidPtr())
		if sourceSig.Results().Len() > 0 {
			outSlot = e.entryAlloca(e.p.prog.Type(coro.ResultSlotType(sourceSig.Results()), llssa.InGo))
		}
		callArgs := append([]llssa.Expr{e.task, e.b.Convert(e.p.prog.VoidPtr(), outSlot)}, args...)
		okVal := e.b.Call(tryFn.Expr, callArgs...)
		contBlk, failBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
		e.b.IfWithBranchWeights(okVal, contBlk, failBlk, 1000, 1)
		e.b.SetBlockEx(failBlk, llssa.AtEnd, false)
		e.emitBailStart()
		e.b.SetBlockContinuation(contBlk)
		*mutated = true
		return e.loadSlotTuple(outSlot, sourceSig.Results())
	}
	if phys.control == coroPhysicalControlDirectOutcome {
		args := e.evalCallArgs(call, vals)
		if e.callerInline {
			return e.p.compileCoroStaticOutcomeTargetCall(e.b, phys.controlTarget, args, true)
		}
		*mutated = true
		return e.emitDirectOutcomeCall(phys.controlTarget, args, call.Pos())
	}
	callee := call.Common().StaticCallee()
	args := e.evalCallArgs(call, vals)
	if !isRuntimeKeepAliveCall(call) {
		*mutated = true
	}
	fnExpr, _, kind := e.p.compileFunction(callee)
	if kind != goFunc || fnExpr == nil {
		panic(fmt.Sprintf("sync-ramp plain callee %q did not resolve to a Go entry", callee.String()))
	}
	return e.b.Call(fnExpr.Expr, args...)
}

func (e *coroSyncFastEmitter) emitDirectOutcomeCall(callee *ssa.Function, args []llssa.Expr, pos token.Pos) llssa.Expr {
	sourceSig, err := e.p.emissionUniverse.coroPhysicalSourceSignature(callee)
	if err != nil {
		panic(err)
	}
	abi := newOutcomePlainPhysicalABI(sourceSig)
	calleeFn, _, kind := e.p.compileOutcomePlainFunction(callee)
	if kind != goFunc || calleeFn == nil {
		panic(fmt.Sprintf("sync-ramp outcome callee %q did not resolve to a Go entry", callee.String()))
	}
	resSlot := e.p.prog.Nil(e.p.prog.VoidPtr())
	if sourceSig.Results().Len() > 0 {
		resSlot = e.entryAlloca(e.p.prog.Type(abi.resultSlotType, llssa.InGo))
	}
	comp := e.completionSlot()
	callArgs := append([]llssa.Expr{e.task, e.b.Convert(e.p.prog.VoidPtr(), resSlot), e.b.Convert(e.p.prog.VoidPtr(), comp)}, args...)
	e.b.Call(calleeFn.Expr, callArgs...)

	retBlk := e.b.Func.MakeBlock()
	e.checkOutcomeCompletionTo(comp, pos, retBlk)
	e.b.SetBlockContinuation(retBlk)
	return e.loadSlotTuple(resSlot, sourceSig.Results())
}

func (e *coroSyncFastEmitter) emitSingleAwait(call *ssa.Call, postNode *coroSyncFastNode, env coroSyncEmitEnv) {
	phys := e.ramp.singleAwaitPlan
	nullPtr := e.p.prog.Nil(e.p.prog.VoidPtr())
	syncCompleted := e.b.Func.MakeBlock()

	branchChild := func(child llssa.Expr) {
		asyncBlk := e.b.Func.MakeBlock()
		e.b.IfWithBranchWeights(e.b.BinOp(token.NEQ, child, nullPtr), asyncBlk, syncCompleted, 1, 1000)
		e.b.SetBlockEx(asyncBlk, llssa.AtEnd, false)
		e.emitSlowCall(1, child, env.vals)
	}

	switch phys.control {
	case coroPhysicalControlDirectAwait, coroPhysicalControlExactInterfaceAwait:
		args := e.evalCallArgs(call, env.vals)
		if phys.control == coroPhysicalControlExactInterfaceAwait {
			args = append([]llssa.Expr{e.evalValue(phys.controlReceiver, env.vals)}, args...)
		}
		calleeFn, _, kind := e.p.compileFunctionEntry(e.p.mustFunctionSymbol(phys.controlTarget))
		if kind != goFunc || calleeFn == nil {
			panic("sync-ramp single-await target did not resolve to a Go entry")
		}
		branchChild(e.b.Call(calleeFn.Expr, append([]llssa.Expr{e.task, e.resultSlot}, args...)...))

	case coroPhysicalControlManagedInterfaceAwait, coroPhysicalControlDispatchAwait:
		var fnVal llssa.Expr
		var sig *types.Signature
		args := e.evalCallArgs(call, env.vals)
		if phys.control == coroPhysicalControlManagedInterfaceAwait {
			intf := e.evalValue(call.Common().Value, env.vals)
			e.emitNilGuard(e.b.InterfaceTypeWord(intf), false, call.Pos())
			if phys.rawInterfaceReceiver {
				fnVal = e.b.ImethodRawDataKnownNonNil(intf, call.Common().Method)
			} else {
				fnVal = e.b.Imethod(intf, call.Common().Method)
			}
			sig = phys.controlSignature
		} else {
			fnVal = e.evalValue(call.Common().Value, env.vals)
			e.emitNilGuard(e.b.Field(fnVal, 0), false, call.Pos())
			sig = call.Common().Signature()
		}
		dabi, err := newCoroPlainDispatchABI(e.p, sig)
		if err != nil {
			panic(err)
		}
		opts := llssa.CoroDispatchCallOptions{
			Version:           coroPlainDispatchVersion,
			ABIHash:           dabi.hash,
			Result:            e.p.prog.Type(dabi.resultSlotType, llssa.InC),
			DescriptorNonNil:  true,
			TrustedDescriptor: phys.dispatchEntry != coro.ManagedEntryNone,
		}
		if phys.control == coroPhysicalControlManagedInterfaceAwait {
			if callPlan, ok := e.p.immutablePlan().CallPlan(call); ok && !callPlan.Open {
				opts.TrustedDescriptor = true
			}
		}
		switch phys.dispatchEntry {
		case coro.ManagedEntryCoroutine:
			sel := e.b.PrepareCoroDispatchStructuredOnly(fnVal, opts, false)
			branchChild(e.b.CallPreparedCoroDispatchCoroOnly(sel, e.task, e.resultSlot, args))
		case coro.ManagedEntryOutcomePlain:
			sel := e.b.PrepareCoroDispatchStructuredOnly(fnVal, opts, false)
			comp := e.completionSlot()
			e.b.CallPreparedCoroDispatchOutcomeOnly(sel, e.task, e.resultSlot, e.b.Convert(e.p.prog.VoidPtr(), comp), args)
			e.checkOutcomeCompletionTo(comp, call.Pos(), syncCompleted)
		default:
			sel := e.b.PrepareCoroDispatchCall(fnVal, opts)
			coroBlk, nonCoroBlk, outcomeBlk, plainBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock(), e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
			e.b.If(sel.HasCoro(), coroBlk, nonCoroBlk)
			e.b.SetBlockEx(nonCoroBlk, llssa.AtEnd, false)
			e.b.If(sel.HasOutcome(), outcomeBlk, plainBlk)

			e.b.SetBlockEx(coroBlk, llssa.AtEnd, false)
			branchChild(e.b.CallPreparedCoroDispatchCoro(sel, e.task, e.resultSlot, args))

			e.b.SetBlockEx(outcomeBlk, llssa.AtEnd, false)
			comp := e.completionSlot()
			e.b.CallPreparedCoroDispatchOutcome(sel, e.task, e.resultSlot, e.b.Convert(e.p.prog.VoidPtr(), comp), args)
			e.checkOutcomeCompletionTo(comp, call.Pos(), syncCompleted)

			e.b.SetBlockEx(plainBlk, llssa.AtEnd, false)
			plainRes := e.b.CallPreparedCoroDispatchPlain(sel, args)
			typedOut := e.b.Convert(e.p.prog.Pointer(e.p.prog.Type(e.abi.resultSlotType, llssa.InGo)), e.resultSlot)
			e.p.storeCoroDynamicDispatchResult(e.b, typedOut, plainRes, dabi.signature.Results())
			e.b.Jump(syncCompleted)
		}
	default:
		panic(fmt.Sprintf("sync-ramp unsupported single-await control %s", phys.control))
	}

	e.b.SetBlockEx(syncCompleted, llssa.AtEnd, false)
	// Snapshot pre-call env before post-call blocks can rebind loop Phis so
	// ResumePostCall (mode==2) restores the exact pre-call live values.
	e.singleAwaitPreEnv = cloneMap(env.vals)
	typedOut := e.b.Convert(e.p.prog.Pointer(e.p.prog.Type(e.abi.resultSlotType, llssa.InGo)), e.resultSlot)
	env.vals[call] = e.loadSlotTuple(typedOut, coroPhysicalNormalizeSourceSignature(e.fn.Signature).Results())
	e.emitNode(postNode, env)
}

func coroSyncIsZeroIntConst(v ssa.Value) bool {
	c, ok := v.(*ssa.Const)
	if !ok || c.Value == nil || c.Value.Kind() != constant.Int {
		return false
	}
	n, exact := constant.Int64Val(c.Value)
	return exact && n == 0
}

func (e *coroSyncFastEmitter) checkOutcomeCompletionTo(comp llssa.Expr, pos token.Pos, okBlk llssa.BasicBlock) {
	status := e.b.LoadKnownNonNil(e.b.FieldAddrKnownNonNil(comp, outcomePlainCompletionStatus))
	isRet := e.b.BinOp(token.EQL, status, e.p.prog.IntVal(coroAwaitCompletionReturn, e.p.prog.Uint32()))
	errBlk := e.b.Func.MakeBlock()
	e.b.IfWithBranchWeights(isRet, okBlk, errBlk, 1000, 1)
	e.b.SetBlockEx(errBlk, llssa.AtEnd, false)
	tw := e.b.LoadKnownNonNil(e.b.FieldAddrKnownNonNil(comp, outcomePlainCompletionTypeWord))
	dw := e.b.LoadKnownNonNil(e.b.FieldAddrKnownNonNil(comp, outcomePlainCompletionDataWord))
	e.emitOutcomePropagateCall(status, tw, dw, pos)
}

func (e *coroSyncFastEmitter) loadSlotTuple(slot llssa.Expr, results *types.Tuple) llssa.Expr {
	if results == nil || results.Len() == 0 {
		return llssa.Expr{}
	}
	if results.Len() == 1 {
		return e.b.LoadKnownNonNil(e.b.FieldAddr(slot, 0))
	}
	fields := make([]llssa.Expr, results.Len())
	for i := range fields {
		fields[i] = e.b.LoadKnownNonNil(e.b.FieldAddr(slot, i))
	}
	return e.b.Aggregate(e.p.prog.Type(results, llssa.InGo), fields...)
}

func (e *coroSyncFastEmitter) emitFastReturn(ret *ssa.Return, vals map[ssa.Value]llssa.Expr) {
	if e.callerInline {
		e.b.Jump(e.callerDoneBlk)
		return
	}
	resTuple := coroPhysicalNormalizeSourceSignature(e.fn.Signature).Results()
	if resTuple.Len() > 0 {
		typedOut := e.b.Convert(e.p.prog.Pointer(e.p.prog.Type(e.abi.resultSlotType, llssa.InGo)), e.resultSlot)
		for i, resVal := range ret.Results {
			e.b.StoreKnownNonNil(e.b.FieldAddrKnownNonNil(typedOut, i), e.evalValueAs(resVal, resTuple.At(i).Type(), vals))
		}
	}
	if e.tryMode {
		e.b.Return(e.p.prog.BoolVal(true))
		return
	}
	e.b.Return(e.p.prog.Nil(e.p.prog.VoidPtr()))
}

func (e *coroSyncFastEmitter) emitTailCall(call *ssa.Call, target *ssa.Function, vals map[ssa.Value]llssa.Expr) {
	args := e.evalCallArgs(call, vals)
	if e.callerInline {
		e.p.compileCoroTargetAwait(e.b, target, args)
		e.b.Jump(e.callerDoneBlk)
		return
	}
	calleeFn, _, kind := e.p.compileFunctionEntry(e.p.mustFunctionSymbol(target))
	if kind != goFunc || calleeFn == nil {
		panic("sync-ramp tail-call target did not resolve to a Go entry")
	}
	e.b.Return(e.b.Call(calleeFn.Expr, append([]llssa.Expr{e.task, e.resultSlot}, args...)...))
}

// coroOutcomePropagateCoroutine returns the module-shared linkonce coroutine
// used by sync-fast ramps to propagate an unexpected non-Return $outcome
// completion (Panic, FaultNil, or Goexit) without allocating frames on the
// fast path.
func (p *context) coroOutcomePropagateCoroutine() llssa.Function {
	ptr, u32 := types.Typ[types.UnsafePointer], types.Typ[types.Uint32]
	sourceSig := types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "desc", ptr),
			types.NewParam(token.NoPos, nil, "line", u32),
			types.NewParam(token.NoPos, nil, "status", u32),
			types.NewParam(token.NoPos, nil, "typeWord", ptr),
			types.NewParam(token.NoPos, nil, "dataWord", ptr),
		), nil, false)
	fn := p.pkg.NewFuncEx(coroOutcomePropagateHelperNameV1, coroPhysicalEntrySignature(sourceSig), llssa.InGo, false, true)
	if fn.HasBody() {
		return fn
	}
	var propComp *Compilation
	if p.compilation != nil {
		propComp = &Compilation{
			CoroABI: p.compilation.CoroABI, SchedulerABI: p.compilation.SchedulerABI,
			PanicABI: p.compilation.PanicABI, FuncRepABI: p.compilation.FuncRepABI,
		}
	}
	rampCtx := context{prog: p.prog, pkg: p.pkg, fn: fn, compilation: propComp}
	abi := newCoroPhysicalABI(&rampCtx, plannedFunctionSymbol{
		name: coroOutcomePropagateHelperNameV1,
		plan: coro.FunctionPlan{ID: coro.FunctionID(coroOutcomePropagateHelperNameV1)},
	}, sourceSig)
	b := fn.MakeBody(1)
	body := rampCtx.beginCoroBody(b, abi, nil)
	body.completion, body.finalSuspend = fn.MakeBlock(), fn.MakeBlock()
	body.bindCancellationCompletion(b)

	b.SetBlock(body.coro.InitialResumeBlock())
	body.activate(b)

	desc, line := fn.PhysicalParam(2), fn.PhysicalParam(3)
	status, typeWord, dataWord := fn.PhysicalParam(4), fn.PhysicalParam(5), fn.PhysicalParam(6)
	traceAppend := p.pkg.NewFunc(abi.panicTraceAppendHook, coroPanicTraceAppendSignature(), llssa.InC).Expr
	faultPrepare := p.pkg.NewFunc(coroFaultPrepareHookV1, coroFaultPrepareSignature(), llssa.InC).Expr

	panicked, faulted, goexited, invalid := fn.MakeBlock(), fn.MakeBlock(), fn.MakeBlock(), fn.MakeBlock()
	sw := b.Switch(status, invalid)
	sw.Case(p.prog.IntVal(coroAwaitCompletionPanic, p.prog.Uint32()), panicked)
	sw.Case(p.prog.IntVal(coroAwaitCompletionFaultNil, p.prog.Uint32()), faulted)
	sw.Case(p.prog.IntVal(coroAwaitCompletionGoexit, p.prog.Uint32()), goexited)
	sw.End(b)

	b.SetBlockEx(panicked, llssa.AtEnd, false)
	b.CoroAtomicLogicalPanicTraceAppend(traceAppend, body.task, desc, typeWord, dataWord, line,
		p.prog.IntVal(coroLogicalPanicTracePropagate, p.prog.Uint32()))
	body.panicWithLine(b, typeWord, dataWord, line)

	b.SetBlockEx(faulted, llssa.AtEnd, false)
	nullPtr := p.prog.Nil(p.prog.VoidPtr())
	b.CoroAtomicLogicalPanicTraceAppend(traceAppend, body.task, desc, nullPtr, nullPtr, line,
		p.prog.IntVal(coroLogicalPanicTraceNilPropagate, p.prog.Uint32()))
	body.publishStateValue(b, coroSuspendPanic, coroLifecycleFinalSuspended, line)
	b.Call(faultPrepare, body.task, body.coro.Handle(), b.Convert(p.prog.VoidPtr(), body.header),
		p.prog.IntVal(uint64(coroFaultNilV1), p.prog.Uint32()))
	b.Jump(body.finalSuspend)

	b.SetBlockEx(goexited, llssa.AtEnd, false)
	body.enterGoexit(b)
	b.SetBlockEx(invalid, llssa.AtEnd, false)
	b.Unreachable()

	b.SetBlock(body.completion)
	body.complete(b)
	b.SetBlock(body.finalSuspend)
	body.finish(b)
	b.EndBuild()
	b.Dispose()
	return fn
}

// -----------------------------------------------------------------------------
// $coro.slow Resume Wiring Helpers (called from coro_abi.go / compile.go)
// -----------------------------------------------------------------------------

func (p *context) initCoroSyncSlowLiveSlots(state *coroSyncSlowResumeState) {
	if state == nil || len(state.plan.liveValues) == 0 {
		return
	}
	state.liveSlots = make(map[ssa.Value]llssa.Expr, len(state.plan.liveValues))
	for _, v := range state.plan.liveValues {
		state.liveSlots[v] = p.coroFrameAlloca(p.type_(v.Type(), llssa.InGo))
	}
}

func (p *context) emitCoroSyncSlowEntryDispatch(
	b llssa.Builder,
	state *coroSyncSlowResumeState,
	sourceBlocks []llssa.BasicBlock,
) {
	if state == nil {
		b.Jump(sourceBlocks[0])
		return
	}
	storeLiveParams := func(tb llssa.Builder) {
		for i, v := range state.plan.liveValues {
			tb.StoreKnownNonNil(state.liveSlots[v], state.liveParams[i])
		}
	}
	u8 := p.type_(types.Typ[types.Uint8], llssa.InGo)
	invalid := p.fn.MakeBlock()
	sw := b.Switch(state.resumeTagParam, invalid)
	sw.Case(p.prog.IntVal(0, u8), sourceBlocks[0])
	if state.plan.kind == coroSyncRampSingleAwait {
		state.resumeAwaitBlk, state.resumePostBlk = p.fn.MakeBlock(), p.fn.MakeBlock()
		awaitBridge, postBridge := p.fn.MakeBlock(), p.fn.MakeBlock()
		sw.Case(p.prog.IntVal(1, u8), awaitBridge)
		sw.Case(p.prog.IntVal(2, u8), postBridge)
		sw.End(b)

		b.SetBlockEx(awaitBridge, llssa.AtEnd, false)
		storeLiveParams(b)
		b.Jump(state.resumeAwaitBlk)

		b.SetBlockEx(postBridge, llssa.AtEnd, false)
		storeLiveParams(b)
		b.Jump(state.resumePostBlk)
	} else {
		bridges := make([]llssa.BasicBlock, len(state.plan.slowTargets))
		for i, target := range state.plan.slowTargets {
			bridges[i] = p.fn.MakeBlock()
			sw.Case(p.prog.IntVal(uint64(target.tag), u8), bridges[i])
		}
		sw.End(b)
		for i, target := range state.plan.slowTargets {
			b.SetBlockEx(bridges[i], llssa.AtEnd, false)
			storeLiveParams(b)
			b.Jump(sourceBlocks[target.block.Index])
		}
	}
	b.SetBlockEx(invalid, llssa.AtEnd, false)
	b.Unreachable()
}

func (p *context) joinCoroSyncSlowSingleAwait(b llssa.Builder, call *ssa.Call, normalResult llssa.Expr) llssa.Expr {
	state := p.coroSyncSlowResume
	if state == nil || state.plan.kind != coroSyncRampSingleAwait || call != state.plan.singleAwaitCall {
		return normalResult
	}
	state.singleAwaitDone = true
	results := coroPhysicalNormalizeSourceSignature(p.goFn.Signature).Results()
	slot := p.coroFrameAlloca(p.type_(call.Type(), llssa.InGo))
	joinBlk := p.fn.MakeBlock()

	b.StoreKnownNonNil(slot, normalResult)
	b.Jump(joinBlk)

	b.SetBlockEx(state.resumeAwaitBlk, llssa.AtEnd, false)
	b.StoreKnownNonNil(slot, p.awaitCoroSyncSlowPreChild(b, state.preChildParam, results))
	b.Jump(joinBlk)

	b.SetBlockEx(state.resumePostBlk, llssa.AtEnd, false)
	b.StoreKnownNonNil(slot, p.loadCoroSyncSlowIncomingResult(b, results))
	b.Jump(joinBlk)

	b.SetBlockContinuation(joinBlk)
	return b.LoadKnownNonNil(slot)
}
