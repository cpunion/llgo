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
	role := coroFrameRetentionInstructionNone
	if body.frameRetention != nil {
		role = body.frameRetention.roles[instr]
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
	switch role {
	case coroFrameRetentionInstructionPrepare:
		if body.frameRetaining {
			panic("nested coroutine frame-retention critical span")
		}
		// A retained frame pointer must never exist while an ordinary
		// preemption handoff can make this G independently runnable. Poll
		// immediately before the fail-stop prepare, then suppress budget polls
		// until the exact fail-stop retire has returned.
		if body.needsPreempt {
			body.pollAndSuspendForPreempt(b)
		}
		body.instructions = 0
		body.frameRetaining = true
	case coroFrameRetentionInstructionPark, coroFrameRetentionInstructionRetire:
		if !body.frameRetaining {
			panic("coroutine frame-retention park/retire outside its critical span")
		}
	default:
		if !body.frameRetaining && criticalDepth == 0 && !outerCriticalEnter {
			body.countInstructionAndMaybeYield(b)
		}
	}
	if !outerCriticalEnter {
		body.sourceBlockPollFresh = false
	}
	return false
}

func (p *context) compileCoroInstructionEpilogue(instr ssa.Instruction) {
	body := p.coroBody()
	if body == nil || body.frameRetention == nil ||
		body.frameRetention.roles[instr] != coroFrameRetentionInstructionRetire {
		return
	}
	body.frameRetaining = false
	body.instructions = 0
}

func (p *context) compileCoroPatchInitAtBlock(b llssa.Builder) bool {
	if !p.hasCoroPhysicalBody() {
		return false
	}
	p.compileCoroPatchInitAwait(b)
	return true
}

func (p *context) coroWorkerLoweringEnabled() bool {
	return p.hasCoroPhysicalBody() && p.compilation != nil && p.compilation.EnableCoroWorker
}

func (p *context) coroCurrentSourceCall() *ssa.Call {
	observer := p.coroEmissionSite()
	if observer == nil {
		return nil
	}
	call, _ := observer.instruction.(*ssa.Call)
	return call
}

func (p *context) tryCompileCoroAllocation(
	b llssa.Builder,
	allocation *ssa.Alloc,
	elem llssa.Type,
	exactScalarBitcast bool,
) (llssa.Expr, bool) {
	body := p.coroBody()
	if body == nil {
		return llssa.Expr{}, false
	}
	if value, selected := body.terminalResultAllocs[allocation]; selected {
		if !allocation.Heap || allocation.Block() == nil || allocation.Block().Index != 0 {
			panic("coroutine terminal-result allocation lost its source-entry heap identity")
		}
		return value, true
	}
	if exactScalarBitcast {
		return p.coroFrameAlloca(elem), true
	}
	if !allocation.Heap {
		return p.coroFrameAlloc(elem), true
	}
	if body.frameRetention != nil {
		if _, retained := body.frameRetention.allocations[allocation]; retained {
			return p.coroFrameAlloc(elem), true
		}
	}
	return llssa.Expr{}, false
}

func (p *context) compileCoroReturn(b llssa.Builder, results []llssa.Expr) {
	body := p.coroBody()
	if body == nil {
		panic("coroutine return escaped its planned physical body")
	}
	if body.completion == nil {
		panic("coroutine return has no completion block")
	}
	p.storeCoroLeafResult(b, body.abi, body.resultSlot, results)
	b.Jump(body.completion)
}

func (p *context) compileCoroDefer(b llssa.Builder, instruction *ssa.Defer) {
	body := p.coroBody()
	if body == nil || body.cleanup == nil {
		panic("coroutine defer escaped its frozen cleanup plan")
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
	// Physical captured coroutine entries expose their typed context explicitly
	// at (g,out,ctx,...). Do not use Function.FreeVar: that legacy helper
	// hard-codes implicit ctx at parameter zero, which is the G word in the
	// coroutine ABI. Load per use so the value is dominated in every resumed
	// block after CoroSplit.
	ctx := b.Load(p.fn.PhysicalParam(2))
	return b.Field(ctx, index), true
}

func (p *context) coroUsesExplicitStatusFaults() bool {
	return p.coroEmissionExplicitStatus()
}
