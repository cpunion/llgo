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

	"github.com/goplus/llgo/cl/blocks"
	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// PhysicalABIV1 cannot use LLGo's legacy setjmp/TLS defer chain: that chain
// describes a native activation, while a stackless coroutine activation lives
// in the LLVM coroutine frame.  The first cleanup slice is deliberately
// static.  Acyclic sites execute at most once, so one frame-resident active bit
// and typed argument slots per site are sufficient.  Reverse CFG order is LIFO
// for every executable path through an acyclic site set and avoids a second
// heterogeneous runtime cleanup stack.
type coroStaticCleanupTargetKind uint8

const (
	coroStaticCleanupPlain coroStaticCleanupTargetKind = iota
	coroStaticCleanupCoroutine
)

type coroStaticCleanupSitePlan struct {
	instruction *ssa.Defer
	target      *ssa.Function
	targetPlan  coro.FunctionPlan
	kind        coroStaticCleanupTargetKind
}

type coroStaticCleanupPlan struct {
	sites []*coroStaticCleanupSitePlan
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
	runDefers := 0
	for _, block := range fn.Blocks {
		for instructionIndex, raw := range block.Instrs {
			switch instruction := raw.(type) {
			case *ssa.Defer:
				if instruction.DeferStack != nil {
					return nil, fmt.Errorf("defer in block %d uses an alternate dynamic defer stack", block.Index)
				}
				if infos[block.Index].InLoop {
					return nil, fmt.Errorf("defer in cyclic block %d requires a dynamic cleanup stack", block.Index)
				}
				target, targetPlan, kind, err := resolveCoroStaticCleanupTarget(whole, caller, instruction)
				if err != nil {
					return nil, fmt.Errorf("defer in block %d: %w", block.Index, err)
				}
				if reason := validateCoroStaticCleanupNoUnwind(
					whole, universe, target, targetPlan, frameRetentionABI,
				); reason != "" {
					return nil, fmt.Errorf("defer target %q has no exact no-unwind proof: %s", targetPlan.ID, reason)
				}
				byInstruction[instruction] = &coroStaticCleanupSitePlan{
					instruction: instruction,
					target:      target,
					targetPlan:  targetPlan,
					kind:        kind,
				}
			case *ssa.RunDefers:
				if !coroStaticRunDefersReturns(block, instructionIndex) {
					return nil, fmt.Errorf("RunDefers in block %d is not immediately followed by the terminal Return", block.Index)
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
	return &coroStaticCleanupPlan{sites: ordered}, nil
}

func coroStaticRunDefersReturns(block *ssa.BasicBlock, instructionIndex int) bool {
	if block == nil || instructionIndex < 0 || instructionIndex >= len(block.Instrs) {
		return false
	}
	for _, instruction := range block.Instrs[instructionIndex+1:] {
		if _, debug := instruction.(*ssa.DebugRef); debug {
			continue
		}
		_, returns := instruction.(*ssa.Return)
		return returns
	}
	return false
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
) (*ssa.Function, coro.FunctionPlan, coroStaticCleanupTargetKind, error) {
	if whole == nil || instruction == nil || instruction.Common() == nil {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("requires an exact compilation CallPlan")
	}
	common := instruction.Common()
	raw, direct := common.Value.(*ssa.Function)
	if !direct || raw == nil || common.IsInvoke() || common.StaticCallee() != raw {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("requires a static function or declared method, not a closure, method value, or invoke")
	}
	callPlan, ok := whole.CallPlan(instruction)
	if !ok {
		return nil, coro.FunctionPlan{}, 0, fmt.Errorf("defer has no compilation CallPlan")
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
	if target.Signature.Recv() != nil {
		if err := validateCoroStaticMethodCallOperands(instruction, target); err != nil {
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
	audit, err := newCoroPhysicalPureSSAAudit(universe, target, frameRetentionABI)
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
				if !intrinsic || (semantics != CoroIntrinsicCallInlineNoSuspend && semantics != CoroIntrinsicCallInlineSuspend) {
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
	coroStaticCleanupContinuePanic    uint32 = 2
	coroStaticCleanupContinueFirstRun uint32 = 3
)

type coroStaticCleanupSiteState struct {
	plan   *coroStaticCleanupSitePlan
	active llssa.Expr
	args   []llssa.Expr
}

type coroStaticCleanupContinuation struct {
	id    uint32
	block llssa.BasicBlock
}

type coroStaticCleanupState struct {
	sites        []*coroStaticCleanupSiteState
	byDefer      map[*ssa.Defer]*coroStaticCleanupSiteState
	continuation llssa.Expr
	panicType    llssa.Expr
	panicData    llssa.Expr
	entry        llssa.BasicBlock
	complete     llssa.BasicBlock
	panic        llssa.BasicBlock
	run          []coroStaticCleanupContinuation
}

// beginCoroStaticCleanup allocates and initializes every value before the
// initial suspend.  A cancellation decision on the first resume can therefore
// safely enter the same empty drainer as every later terminal path.
func (p *context) beginCoroStaticCleanup(b llssa.Builder, plan *coroStaticCleanupPlan) *coroStaticCleanupState {
	if plan == nil || len(plan.sites) == 0 {
		return nil
	}
	state := &coroStaticCleanupState{
		sites:   make([]*coroStaticCleanupSiteState, 0, len(plan.sites)),
		byDefer: make(map[*ssa.Defer]*coroStaticCleanupSiteState, len(plan.sites)),
	}
	state.continuation = b.AllocaT(p.prog.Uint32())
	b.Store(state.continuation, p.prog.IntVal(0, p.prog.Uint32()))
	state.panicType = b.AllocaT(p.prog.VoidPtr())
	state.panicData = b.AllocaT(p.prog.VoidPtr())
	b.Store(state.panicType, p.prog.Nil(p.prog.VoidPtr()))
	b.Store(state.panicData, p.prog.Nil(p.prog.VoidPtr()))
	for _, sitePlan := range plan.sites {
		site := &coroStaticCleanupSiteState{plan: sitePlan}
		site.active = b.AllocaT(p.prog.Bool())
		b.Store(site.active, p.prog.BoolVal(false))
		for _, argument := range sitePlan.instruction.Call.Args {
			site.args = append(site.args, b.AllocaT(p.type_(argument.Type(), llssa.InGo)))
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
	// SSA values preserve Go's left-to-right evaluation.  Save every evaluated
	// receiver/argument before making the record active.
	args := p.compileValues(b, instruction.Call.Args, p.funcKind(instruction.Call.Value))
	if len(args) != len(site.args) {
		panic(fmt.Sprintf("coroutine defer arguments=%d do not match cleanup slots=%d", len(args), len(site.args)))
	}
	for index, argument := range args {
		b.Store(site.args[index], argument)
	}
	b.Store(site.active, b.Prog.BoolVal(true))
}

func (s *coroStaticCleanupState) enter(b llssa.Builder, continuation uint32) {
	if s == nil || s.entry == nil {
		panic("coroutine static cleanup entry is not bound")
	}
	b.Store(s.continuation, b.Prog.IntVal(uint64(continuation), b.Prog.Uint32()))
	b.Jump(s.entry)
}

func (s *coroStaticCleanupState) enterCompletion(b llssa.Builder) {
	s.enter(b, coroStaticCleanupContinueComplete)
}

func (s *coroStaticCleanupState) enterPanic(b llssa.Builder, typeWord, dataWord llssa.Expr) {
	b.Store(s.panicType, b.Convert(b.Prog.VoidPtr(), typeWord))
	b.Store(s.panicData, b.Convert(b.Prog.VoidPtr(), dataWord))
	s.enter(b, coroStaticCleanupContinuePanic)
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
	b.SetBlock(continuation.block)
}

func (s *coroStaticCleanupState) emit(p *context, b llssa.Builder) {
	if s == nil || s.entry == nil || s.complete == nil || s.panic == nil {
		panic("coroutine static cleanup blocks are not bound")
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
		switch site.plan.kind {
		case coroStaticCleanupPlain:
			function, _, kind := p.compileFunction(site.plan.target)
			if function == nil || kind != goFunc {
				panic(fmt.Sprintf("coroutine plain cleanup target %q did not resolve to a Go entry", site.plan.targetPlan.ID))
			}
			b.Call(function.Expr, args...)
		case coroStaticCleanupCoroutine:
			p.compileCoroTargetAwait(b, site.plan.target, args)
		default:
			panic("coroutine static cleanup target has an invalid kind")
		}
		b.Jump(next)
		next = check
	}
	b.SetBlock(s.entry)
	b.Jump(next)

	invalid := p.fn.MakeBlock()
	b.SetBlock(done)
	dispatch := b.Switch(b.Load(s.continuation), invalid)
	dispatch.Case(b.Prog.IntVal(uint64(coroStaticCleanupContinueComplete), b.Prog.Uint32()), s.complete)
	dispatch.Case(b.Prog.IntVal(uint64(coroStaticCleanupContinuePanic), b.Prog.Uint32()), s.panic)
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
