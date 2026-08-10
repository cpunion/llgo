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

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// This file is the narrow migration boundary between the ordinary SSA
// compiler and physical coroutine emission. Ordinary compile.go/instr.go code
// delegates semantic operations here and never reads coroBodyContext or the
// physical emission session. Each adapter either owns the complete coroutine
// case or reports false so the established plain lowering remains authoritative.

func (p *context) compileCoroInstructionPrologue(b llssa.Builder, instr ssa.Instruction) bool {
	body := p.coroBody()
	if body == nil {
		return false
	}
	if _, debug := instr.(*ssa.DebugRef); debug {
		p.compileInstr(b, instr)
		return true
	}
	criticalRole := coroCriticalCallNone
	criticalDepth := uint32(0)
	if body.critical != nil {
		var proven bool
		criticalDepth, proven = body.critical.beforeDepth[instr]
		if !proven {
			panic("coroutine critical proof has no instruction input depth")
		}
		if call, ok := instr.(*ssa.Call); ok {
			criticalRole = body.critical.roles[call]
		}
	}
	outerCriticalEnter := criticalRole == coroCriticalCallEnter && criticalDepth == 0
	if criticalDepth == 0 && !outerCriticalEnter {
		physical := p.coroEmissionPlan()
		if physical != nil && physical.preempt != nil {
			if physical.preempt.pollsBefore(instr) {
				body.pollAndSuspendForPreempt(b)
			}
		} else {
			body.countInstructionAndMaybeYield(b)
		}
	}
	if !outerCriticalEnter {
		body.sourceBlockPollFresh = false
	}
	switch instr := instr.(type) {
	case *ssa.ChangeType, *ssa.MakeInterface:
		plan := p.coroEmissionPlan()
		if plan == nil {
			panic("coroutine value-elision prologue has no frozen physical plan")
		}
		physical, err := plan.instructionPlan(instr)
		if err != nil {
			panic(fmt.Errorf("coroutine value-elision prologue: %w", err))
		}
		if physical.elideValue {
			// A direct-await/function-value adapter or exact devirtualized
			// interface call owns every executable consumer.
			return true
		}
	}
	return false
}

func (p *context) compileCoroPatchInitAtBlock(b llssa.Builder) bool {
	if !p.hasCoroPhysicalBody() {
		return false
	}
	p.compileCoroPatchInitAwait(b)
	return true
}

func (p *context) coroCurrentSourceCall() *ssa.Call {
	observer := p.coroEmissionSite()
	if observer == nil {
		return nil
	}
	call, _ := observer.instruction.(*ssa.Call)
	return call
}

// tryCompileCoroPhysicalCall is the sole source-call dispatcher for a physical
// coroutine body. It consumes one frozen instruction plan and reports the
// selected control/operation recipe before delegating operand emission. The
// specialized emitters never re-read CallPlan, feature flags, or raw call
// shape to decide whether they own the site.
func (p *context) tryCompileCoroPhysicalCall(b llssa.Builder, call *ssa.Call) (llssa.Expr, bool) {
	if !p.hasStructuredOutcomePhysicalBody() {
		return llssa.Expr{}, false
	}
	if call == nil {
		panic("physical coroutine call dispatcher received a nil source call")
	}
	// Direct-await and descriptor-await recipes bypass the ordinary callEx
	// path completely. Preserve source-level reflect registry requirements at
	// this sole physical call dispatcher, before the logical target becomes a
	// renamed coroutine entry.
	p.pkg.NeedAbiInit |= reflectStaticCallABIInitKind(call.Common())
	instructionPlan, planned := p.plannedCoroPhysicalControl(call)
	if !planned {
		panic("physical coroutine call has no frozen instruction plan")
	}
	if instructionPlan.control != coroPhysicalControlNone {
		// Direct/descriptor awaits bypass callEx and llssa.Builder.Call, where
		// source-level reflect method demands are normally recorded. Attach the
		// semantic fact to the physical owner before selecting the lowering
		// recipe; DCE must never rediscover it from a renamed $coro target.
		p.recordReflectValueMethodCall(p.fn.Name(), call.Common())
	}
	if instructionPlan.control != coroPhysicalControlNone {
		p.observeCoroPhysicalControl(call, instructionPlan.control)
	}
	if p.hasOutcomePlainPhysicalBody() && instructionPlan.control != coroPhysicalControlDirectOutcome {
		panic(fmt.Sprintf("outcome-plain DAG call selected incompatible frozen control recipe %s", instructionPlan.control))
	}
	switch instructionPlan.control {
	case coroPhysicalControlDirectAwait:
		return p.compileCoroStaticAwait(b, call, instructionPlan), true
	case coroPhysicalControlDirectOutcome:
		return p.compileCoroStaticOutcomeCall(b, call, instructionPlan), true
	case coroPhysicalControlDispatchAwait:
		return p.compileCoroManagedDispatchAwait(b, call, instructionPlan), true
	case coroPhysicalControlClosedInterfaceAwait:
		return p.compileCoroInterfaceDispatchAwait(b, call, instructionPlan), true
	case coroPhysicalControlManagedInterfaceAwait:
		return p.compileCoroManagedInterfaceAwait(b, call, instructionPlan), true
	case coroPhysicalControlExactInterfaceCall:
		return p.compileCoroExactInterfaceCall(b, call, instructionPlan), true
	case coroPhysicalControlExactInterfaceAwait:
		return p.compileCoroExactInterfaceAwait(b, call, instructionPlan), true
	case coroPhysicalControlPlainDispatch:
		return p.compileCoroPhysicalPlainDispatch(b, call, instructionPlan), true
	case coroPhysicalControlNilDispatchFault:
		return p.compileCoroPhysicalNilDispatchFault(b, call, instructionPlan), true
	case coroPhysicalControlRawPlainCall:
		if instructionPlan.controlTarget == nil || instructionPlan.controlTargetID == "" {
			panic("physical raw/plain call has no frozen target identity")
		}
		return p.compileCoroRawPlainTargetCall(b, call, instructionPlan.controlTarget), true
	case coroPhysicalControlNone:
		if instructionPlan.operation != coroPhysicalOperationWorkerCgo &&
			instructionPlan.operation != coroPhysicalOperationWorkerCgoErrno &&
			instructionPlan.operation != coroPhysicalOperationWorkerForeign &&
			instructionPlan.operation != coroPhysicalOperationSameMForeign &&
			instructionPlan.operation != coroPhysicalOperationSameMPython {
			return llssa.Expr{}, false
		}
		p.observeCoroPhysicalOperation(call, instructionPlan.operation)
		switch instructionPlan.operation {
		case coroPhysicalOperationWorkerCgo:
			if instructionPlan.operationCgo == nil {
				panic("physical coroutine cgo worker call has no frozen typed shape")
			}
			return p.compileCoroWorkerCgoCall(b, call, *instructionPlan.operationCgo), true
		case coroPhysicalOperationWorkerCgoErrno:
			if instructionPlan.operationCgoErrno == nil {
				panic("physical coroutine C2 worker call has no frozen typed errno shape")
			}
			result := p.compileCoroWorkerCgoErrnoCall(b, call, *instructionPlan.operationCgoErrno)
			p.completeCoroIntrinsicCallEmission(
				llgoCgoCgocall,
				CoroIntrinsicCallInlineForeignSuspend,
			)
			p.cgoReturn(b, true)
			p.cgoReturned = true
			return result, true
		case coroPhysicalOperationWorkerForeign:
			if instructionPlan.operationWorker == nil {
				panic("physical coroutine worker call has no frozen foreign shape")
			}
			return p.compileCoroWorkerForeignCall(b, call, *instructionPlan.operationWorker), true
		case coroPhysicalOperationSameMForeign:
			if instructionPlan.operationWorker == nil {
				panic("physical coroutine same-M call has no frozen foreign shape")
			}
			return p.compileCoroSameMForeignCall(b, call, *instructionPlan.operationWorker), true
		case coroPhysicalOperationSameMPython:
			return p.compileCoroPythonOperation(
				b,
				call,
				instructionPlan.operationPythonTarget,
				instructionPlan.operationPythonOpcode,
			), true
		default:
			panic("physical coroutine worker call selected an unsupported operation")
		}
	default:
		panic(fmt.Sprintf("source call selected incompatible frozen physical control recipe %s", instructionPlan.control))
	}
}

func (p *context) compileCoroTerminalResultAllocation(allocation *ssa.Alloc) llssa.Expr {
	body := p.coroBody()
	if body == nil || allocation == nil || !allocation.Heap || allocation.Block() == nil || allocation.Block().Index != 0 {
		panic("coroutine terminal-result allocation lost its source-entry heap identity")
	}
	value := body.terminalResultAllocs[allocation]
	if value.IsNil() {
		panic("coroutine terminal-result allocation lost its frozen physical storage")
	}
	return value
}

func (p *context) compileCoroReturn(b llssa.Builder, results []llssa.Expr) {
	p.compileCoroTerminalOutcome(b, coroPhysicalOutcomeReturn, results)
}

func (p *context) compileCoroGoexit(b llssa.Builder) {
	p.compileCoroTerminalOutcome(b, coroPhysicalOutcomeGoexit, nil)
}

func coroDeferStackBuiltinCall(call *ssa.Call) bool {
	if call == nil || call.Common() == nil {
		return false
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	return ok && builtin.Name() == "ssa:deferstack"
}

func (p *context) compileCoroDeferStack(b llssa.Builder, instruction *ssa.Call) llssa.Expr {
	cleanup := p.coroCleanup()
	if cleanup == nil || cleanup.external ||
		!cleanup.dynamic || cleanup.dynamicHead.IsNil() ||
		!coroDeferStackBuiltinCall(instruction) {
		panic("coroutine explicit defer stack has no owner-local dynamic cleanup head")
	}
	return b.Convert(p.type_(instruction.Type(), llssa.InGo), cleanup.dynamicHead)
}

// compileCoroTerminalOutcome centralizes the physical frame termination
// protocol. Return commits values before final suspend; Goexit replaces the
// cleanup base and never reaches the retained SSA call continuation.
func (p *context) compileCoroTerminalOutcome(
	b llssa.Builder,
	outcome coroPhysicalOutcomeRecipe,
	results []llssa.Expr,
) {
	if p.hasOutcomePlainPhysicalBody() {
		if outcome != coroPhysicalOutcomeReturn {
			panic(fmt.Sprintf("unsupported outcome-plain terminal outcome recipe %s", outcome))
		}
		p.compileOutcomePlainReturn(b, results)
		return
	}
	body := p.coroBody()
	if body == nil || body.completion == nil || b == nil || b.Func != p.fn {
		panic("terminal outcome escaped its planned physical coroutine body")
	}
	switch outcome {
	case coroPhysicalOutcomeReturn:
		p.storeCoroLeafResult(b, body.abi, body.resultSlot, results)
		b.Jump(body.completion)
	case coroPhysicalOutcomeGoexit:
		if len(results) != 0 || body.terminalStatus.IsNil() || !p.coroEmissionExplicitStatus() {
			panic("Goexit requires an active explicit-status coroutine body")
		}
		body.enterGoexit(b)
	default:
		panic(fmt.Sprintf("unsupported physical terminal outcome recipe %s", outcome))
	}
}

func (body *coroBodyContext) enterGoexit(b llssa.Builder) {
	if body == nil || body.completion == nil || body.terminalStatus.IsNil() || b == nil {
		panic("structured Goexit requires a complete physical coroutine body")
	}
	b.Store(
		body.terminalStatus,
		b.Prog.IntVal(coroAwaitCompletionGoexit, b.Prog.Uint32()),
	)
	if body.cleanup == nil {
		b.Jump(body.completion)
		return
	}
	body.cleanup.enterGoexit(b)
}

func (p *context) compileCoroDefer(b llssa.Builder, instruction *ssa.Defer) {
	body := p.coroBody()
	if body == nil {
		panic("coroutine defer escaped its frozen cleanup plan")
	}
	if body.externalCleanup != nil {
		body.externalCleanup.registerExternal(p, b, instruction)
		return
	}
	if body.cleanup == nil {
		panic("coroutine defer escaped its owner-local cleanup plan")
	}
	body.cleanup.register(p, b, instruction)
}

func (p *context) compileCoroRunDefers(b llssa.Builder, instruction *ssa.RunDefers) {
	body := p.coroBody()
	if body == nil || body.cleanup == nil {
		panic("coroutine RunDefers escaped its frozen cleanup plan")
	}
	body.cleanup.runDefers(b, instruction)
}

func (p *context) compileCoroSyntheticSelectPanic(b llssa.Builder, instruction *ssa.Panic) {
	body := p.coroBody()
	if body == nil || instruction == nil {
		panic("coroutine select invariant trap escaped its planned physical body")
	}
	if body.unsupportedRunDecision == nil {
		panic("coroutine select invariant panic requires a fail-closed trap block")
	}
	b.Jump(body.unsupportedRunDecision)
}

func (p *context) tryCompileCoroFreeVar(b llssa.Builder, fn *ssa.Function, index int) (llssa.Expr, bool) {
	if !p.hasCoroPhysicalBody() || len(fn.FreeVars) == 0 {
		return llssa.Expr{}, false
	}
	_, hasEnv, err := p.emissionUniverse.closureEnvironments.entryEnvironment(fn)
	if err != nil {
		panic(fmt.Errorf("physical free variable closure environment: %w", err))
	}
	if !hasEnv {
		// A source closure whose captures are all zero-sized has no physical
		// environment. compileValue recreates those pointers from the module's
		// canonical zero-sized allocation sentinel.
		return llssa.Expr{}, false
	}
	// Physical captured coroutine entries expose their typed context explicitly
	// at (g,out,ctx,...). Do not use Function.FreeVar: that legacy helper
	// hard-codes implicit ctx at parameter zero, which is the G word in the
	// coroutine ABI. Load per use so the value is dominated in every resumed
	// block after CoroSplit.
	ctx := b.Load(p.fn.PhysicalParam(2))
	return b.Field(ctx, index), true
}

func (p *context) compileClosureEnvironment() llssa.Expr {
	if p.fn == nil || p.goFn == nil {
		panic("closureEnv(): called outside an env-bearing function")
	}
	if p.hasCoroPhysicalBody() {
		_, hasEnv, err := p.emissionUniverse.closureEnvironments.entryEnvironment(p.goFn)
		if err != nil {
			panic(fmt.Errorf("closureEnv(): %w", err))
		}
		if !hasEnv {
			panic("closureEnv(): physical function has no frozen environment")
		}
		return p.fn.PhysicalParam(2)
	}
	if !p.fn.NeedsEnv() {
		panic("closureEnv(): called outside an env-bearing function")
	}
	return p.fn.Env()
}

func (p *context) coroUsesExplicitStatusFaults() bool {
	return p.coroEmissionExplicitStatus()
}
