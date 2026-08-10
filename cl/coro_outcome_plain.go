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

const coroOutcomePlainPrimarySuffix = "$outcome"

// outcomePlainPhysicalABI is the synchronous hidden ABI for a logically
// outcome-structured bounded body. The caller owns both result and completion
// storage; the callee returns normally after publishing exactly one terminal
// status.
// No parameter is an LLVM coroutine handle and this ABI emits no coro
// intrinsic, ramp, resume, or destroy symbol.
type outcomePlainPhysicalABI struct {
	physicalSig    *types.Signature
	resultSlotType *types.Struct
	resultCount    int
}

func newOutcomePlainPhysicalABI(sourceSig *types.Signature) outcomePlainPhysicalABI {
	sourceSig = coroPhysicalNormalizeSourceSignature(sourceSig)
	resultFields := make([]*types.Var, sourceSig.Results().Len())
	for index := range resultFields {
		resultFields[index] = types.NewField(
			token.NoPos, nil, fmt.Sprintf("r%d", index), sourceSig.Results().At(index).Type(), false,
		)
	}
	resultSlotType := types.NewStruct(resultFields, nil)
	physicalParams := make([]*types.Var, 0, sourceSig.Params().Len()+3)
	physicalParams = append(physicalParams,
		types.NewParam(token.NoPos, nil, "__llgo_g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "__llgo_out", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "__llgo_completion", types.Typ[types.UnsafePointer]),
	)
	for index := 0; index < sourceSig.Params().Len(); index++ {
		physicalParams = append(physicalParams, sourceSig.Params().At(index))
	}
	return outcomePlainPhysicalABI{
		physicalSig:    types.NewSignatureType(nil, nil, nil, types.NewTuple(physicalParams...), nil, false),
		resultSlotType: resultSlotType,
		resultCount:    sourceSig.Results().Len(),
	}
}

// outcomePlainCompletionType is compiler-owned ABI data. It carries one
// terminal status and a concrete interface pair. The allocation-free nil-fault
// cohort uses its own status and does not need payload words; future
// parameterized fault cohorts must extend the ABI only together with exact
// cross-package capability metadata.
func outcomePlainCompletionType(prog llssa.Program) llssa.Type {
	return prog.Struct(
		prog.Uint32(), // CompletionStatus
		prog.VoidPtr(),
		prog.VoidPtr(),
	)
}

const (
	outcomePlainCompletionStatus = iota
	outcomePlainCompletionTypeWord
	outcomePlainCompletionDataWord
)

type outcomePlainBodyContext struct {
	abi        outcomePlainPhysicalABI
	task       llssa.Expr
	resultSlot llssa.Expr
	completion llssa.Expr
}

func (body *outcomePlainBodyContext) publish(
	b llssa.Builder,
	status uint64,
	typeWord, dataWord llssa.Expr,
) {
	if body == nil || b == nil || body.completion.IsNil() {
		panic("outcome-plain terminal publication requires an active caller-owned record")
	}
	if typeWord.IsNil() != dataWord.IsNil() {
		panic("outcome-plain terminal publication has a partial panic pair")
	}
	null := b.Prog.Nil(b.Prog.VoidPtr())
	if typeWord.IsNil() {
		typeWord, dataWord = null, null
	}
	b.Store(b.FieldAddr(body.completion, outcomePlainCompletionTypeWord), b.Convert(b.Prog.VoidPtr(), typeWord))
	b.Store(b.FieldAddr(body.completion, outcomePlainCompletionDataWord), b.Convert(b.Prog.VoidPtr(), dataWord))
	// Publish status last so later concurrent-capable ABI versions can retain
	// the same payload-before-state transaction.
	b.Store(
		b.FieldAddr(body.completion, outcomePlainCompletionStatus),
		b.Prog.IntVal(status, b.Prog.Uint32()),
	)
	b.Return()
}

func (body *outcomePlainBodyContext) publishNilFault(b llssa.Builder) {
	body.publish(b, coroAwaitCompletionFaultNil, llssa.Nil, llssa.Nil)
}

func validateOutcomePlainFrozenPlan(
	plan *coroPhysicalFunctionPlan,
	logical coro.FunctionPlan,
) error {
	if plan == nil || plan.function == nil {
		return fmt.Errorf("outcome-plain emission requires one frozen physical plan")
	}
	if logical.Emission != coro.EmitOutcomePlain ||
		logical.ManagedEntry != coro.ManagedEntryOutcomePlain ||
		!logical.AtomicCostProof.ProvesOutcomePlain() {
		return fmt.Errorf("outcome-plain physical plan disagrees with its logical capability")
	}
	if plan.atomicCost != logical.AtomicCost || plan.atomicCostProof != logical.AtomicCostProof ||
		plan.atomicCertificate != logical.AtomicCostCertificate {
		return fmt.Errorf("outcome-plain physical plan lost its exact atomic-cost certificate")
	}
	if plan.cleanup != nil || plan.critical != nil || plan.needsPreempt || plan.preempt != nil {
		return fmt.Errorf("outcome-plain body acquired cleanup, critical, or preemption state")
	}
	directCalls := 0
	for instruction, physical := range plan.instructions {
		switch physical.recipe {
		case coroPhysicalInstructionOrdinary,
			coroPhysicalInstructionFieldAddr,
			coroPhysicalInstructionDeref,
			coroPhysicalInstructionStaticArrayRangeDerefElided,
			coroPhysicalInstructionStore:
		default:
			return fmt.Errorf("outcome-plain instruction %T escaped the bounded value recipe %s", instruction, physical.recipe)
		}
		if physical.operation != coroPhysicalOperationNone || physical.boundsGuard {
			return fmt.Errorf("outcome-plain instruction %T escaped the bounded recipe", instruction)
		}
		if physical.nilGuard {
			switch physical.recipe {
			case coroPhysicalInstructionFieldAddr,
				coroPhysicalInstructionDeref,
				coroPhysicalInstructionStore:
			default:
				return fmt.Errorf("outcome-plain instruction %T acquired an unsupported nil guard", instruction)
			}
		}
		if call, ok := instruction.(*ssa.Call); ok &&
			physical.semantic.recipe == coro.RecipeID("cl.ssa.call.v1") {
			directCalls++
			if logical.AtomicCostProof != coro.AtomicCostDAG ||
				physical.control != coroPhysicalControlDirectOutcome ||
				physical.controlTarget == nil || physical.controlTargetID == "" ||
				!physical.directOutcomeNativeResult {
				return fmt.Errorf("outcome-plain call %q lacks an exact native-safe outcome target", call.String())
			}
		} else if physical.control != coroPhysicalControlNone {
			return fmt.Errorf("outcome-plain instruction %T selected unsupported control %s", instruction, physical.control)
		}
		allowedSemantic := coroOutcomePlainLeafSemanticRecipe(physical.semantic)
		if logical.AtomicCostProof == coro.AtomicCostDAG {
			allowedSemantic = coroOutcomePlainDAGSemanticRecipe(physical.semantic)
		}
		if !allowedSemantic {
			return fmt.Errorf("outcome-plain instruction %T escaped the %s recipe", instruction, logical.AtomicCostProof)
		}
		switch physical.outcome {
		case coroPhysicalOutcomeNone, coroPhysicalOutcomeReturn, coroPhysicalOutcomePanic:
		default:
			return fmt.Errorf("outcome-plain instruction %T selected unsupported outcome %s", instruction, physical.outcome)
		}
	}
	if logical.AtomicCostProof == coro.AtomicCostLeaf && directCalls != 0 {
		return fmt.Errorf("outcome-plain leaf contains %d direct outcome calls", directCalls)
	}
	if logical.AtomicCostProof == coro.AtomicCostDAG && directCalls == 0 {
		return fmt.Errorf("outcome-plain DAG contains no direct outcome call")
	}
	return nil
}

func (p *context) compileOutcomePlainPhysicalBody(
	b llssa.Builder,
	fn *ssa.Function,
	abi outcomePlainPhysicalABI,
	logical coro.FunctionPlan,
	isInit bool,
) {
	if p.emissionUniverse == nil || p.emissionUniverse.coroProgramIR == nil {
		panic("outcome-plain physical body has no ProgramIR")
	}
	physicalPlan, err := (emissionCanonicalIndex{universe: p.emissionUniverse}).physicalFunctionPlanForEmission(fn, p.emissionOwner)
	if err != nil {
		panic(fmt.Errorf("load frozen outcome-plain physical plan: %w", err))
	}
	if err := validateOutcomePlainFrozenPlan(physicalPlan, logical); err != nil {
		panic(err)
	}
	emission, finishEmission := p.beginCoroManagedPhysicalEmission(physicalPlan, 3, true)
	defer finishEmission()

	b.SetBlock(p.fn.Block(0))
	completionType := outcomePlainCompletionType(p.prog)
	body := &outcomePlainBodyContext{
		abi:        abi,
		task:       p.fn.PhysicalParam(0),
		resultSlot: p.fn.PhysicalParam(1),
		completion: b.Convert(p.prog.Pointer(completionType), p.fn.PhysicalParam(2)),
	}
	bodyCapability := newOutcomePlainBodyCapability(body)
	sourceBlocks := make([]llssa.BasicBlock, len(fn.Blocks))
	for index := range sourceBlocks {
		sourceBlocks[index] = p.fn.Block(index)
	}
	emission.bindManagedPhysicalBody(bodyCapability, sourceBlocks)

	off := make([]int, len(fn.Blocks))
	for index, block := range fn.Blocks {
		off[index] = p.compilePhis(b, block)
	}
	p.blkInfos = blocks.Infos(fn.Blocks)
	for index := 0; index >= 0; index = p.blkInfos[index].Next {
		block := fn.Blocks[index]
		p.compileBlock(b, block, off[index], index == 1 && isInit)
	}
	for _, phi := range p.phis {
		phi()
	}
	emission.completeManagedPhysicalBody(bodyCapability)
	if err := p.pkg.EmitCoroAtomicCostCertificate(
		p.fn.Name(), logical.AtomicCost, uint8(logical.AtomicCostProof), logical.AtomicCostCertificate,
	); err != nil {
		panic(fmt.Errorf("publish outcome-plain atomic-cost certificate: %w", err))
	}
}

func (p *context) storeOutcomePlainResult(
	b llssa.Builder,
	body *outcomePlainBodyContext,
	results []llssa.Expr,
) {
	if body == nil || len(results) != body.abi.resultCount {
		panic("outcome-plain result count disagrees with its physical ABI")
	}
	if len(results) == 0 {
		return
	}
	resultType := p.prog.Type(body.abi.resultSlotType, llssa.InGo)
	typedSlot := b.Convert(p.prog.Pointer(resultType), body.resultSlot)
	for index, result := range results {
		b.Store(b.FieldAddr(typedSlot, index), result)
	}
}

func (p *context) compileOutcomePlainReturn(b llssa.Builder, results []llssa.Expr) {
	body := p.outcomePlainBody()
	if body == nil || b == nil || b.Func != p.fn {
		panic("outcome-plain return escaped its exact physical body")
	}
	p.storeOutcomePlainResult(b, body, results)
	body.publish(b, coroAwaitCompletionReturn, llssa.Nil, llssa.Nil)
}

func (p *context) compileOutcomePlainPanicPair(b llssa.Builder, typeWord, dataWord llssa.Expr) {
	body := p.outcomePlainBody()
	if body == nil || b == nil || b.Func != p.fn || typeWord.IsNil() || dataWord.IsNil() {
		panic("outcome-plain panic escaped its exact physical body")
	}
	body.publish(b, coroAwaitCompletionPanic, typeWord, dataWord)
}

func (p *context) compileCoroStaticOutcomeCall(
	b llssa.Builder,
	call *ssa.Call,
	instructionPlan coroPhysicalInstructionPlan,
) llssa.Expr {
	if !p.hasStructuredOutcomePhysicalBody() || call == nil || instructionPlan.control != coroPhysicalControlDirectOutcome {
		panic("outcome-plain call escaped its frozen direct control recipe")
	}
	callee := instructionPlan.controlTarget
	if callee == nil || len(callee.FreeVars) != 0 {
		panic("outcome-plain v0 requires one exact context-free target")
	}
	p.emitPCLineLabel(b, call.Pos())
	args := p.compileValues(b, call.Call.Args, p.funcKind(call.Call.Value))
	source, _ := call.Call.Value.(*ssa.Function)
	args = p.compileManagedGoLinknameCallArguments(b, source, callee, args)
	entry := p.mustFunctionSymbol(callee)
	sourceSig, err := p.emissionUniverse.coroPhysicalSourceSignature(callee)
	if err != nil {
		panic(fmt.Errorf("derive outcome-plain target %q ABI: %w", entry.plan.ID, err))
	}
	abi := newOutcomePlainPhysicalABI(sourceSig)
	calleeFn, _, kind := p.compileFunctionEntry(entry)
	if kind != goFunc {
		panic(fmt.Sprintf("outcome-plain target %q did not resolve to a Go entry", entry.plan.ID))
	}
	if p.hasOutcomePlainPhysicalBody() && entry.plan.External == coro.ExternalKnown {
		if err := p.pkg.EmitCoroAtomicCostDependency(
			calleeFn.Name(), entry.plan.AtomicCost, uint8(entry.plan.AtomicCostProof), entry.plan.AtomicCostCertificate,
		); err != nil {
			panic(fmt.Errorf("publish imported outcome atomic-cost dependency: %w", err))
		}
	}
	resultType := p.prog.Type(abi.resultSlotType, llssa.InGo)
	// Reuse the coroutine result-slot policy even though this exact call cannot
	// suspend. In particular, an outcome target may still return a very
	// large parameter; it must not turn into an unbounded alloca on a resume
	// function's fixed native stack.
	var resultSlot llssa.Expr
	if p.hasCoroPhysicalBody() {
		resultSlot = p.coroResultSlot(resultType)
	} else {
		if !p.hasOutcomePlainPhysicalBody() || !instructionPlan.directOutcomeNativeResult {
			panic("outcome-plain DAG call result escaped its frozen native-stack bound")
		}
		resultSlot = b.AllocaT(resultType)
	}
	// The completion record is three fixed words and is consumed before the next
	// suspension, so this site-local alloca is independently bounded.
	completion := b.AllocaZeroedT(outcomePlainCompletionType(p.prog))
	physicalArgs := make([]llssa.Expr, 0, len(args)+3)
	physicalArgs = append(physicalArgs,
		p.managedPhysicalTask(),
		b.Convert(p.prog.VoidPtr(), resultSlot),
		b.Convert(p.prog.VoidPtr(), completion),
	)
	physicalArgs = append(physicalArgs, args...)
	b.Call(calleeFn.Expr, physicalArgs...)

	status := b.Load(b.FieldAddr(completion, outcomePlainCompletionStatus))
	returned := p.fn.MakeBlock()
	panicked := p.fn.MakeBlock()
	goexited := p.fn.MakeBlock()
	faulted := p.fn.MakeBlock()
	invalid := p.fn.MakeBlock()
	dispatch := b.Switch(status, invalid)
	dispatch.Case(p.prog.IntVal(coroAwaitCompletionReturn, p.prog.Uint32()), returned)
	dispatch.Case(p.prog.IntVal(coroAwaitCompletionPanic, p.prog.Uint32()), panicked)
	dispatch.Case(p.prog.IntVal(coroAwaitCompletionGoexit, p.prog.Uint32()), goexited)
	dispatch.Case(p.prog.IntVal(coroAwaitCompletionFaultNil, p.prog.Uint32()), faulted)
	dispatch.End(b)

	line := p.coroCurrentSourceLine()
	b.SetBlockEx(panicked, llssa.AtEnd, false)
	typeWord := b.Load(b.FieldAddr(completion, outcomePlainCompletionTypeWord))
	dataWord := b.Load(b.FieldAddr(completion, outcomePlainCompletionDataWord))
	p.enterCoroPropagatedPanic(b, typeWord, dataWord, line)
	b.SetBlockEx(goexited, llssa.AtEnd, false)
	p.enterCoroPropagatedGoexit(b)
	b.SetBlockEx(faulted, llssa.AtEnd, false)
	p.enterCoroPropagatedNilFault(b)
	b.SetBlockEx(invalid, llssa.AtEnd, false)
	b.Unreachable()
	b.SetBlockContinuation(returned)
	value := p.loadCoroAwaitResult(b, resultSlot, sourceSig.Results())
	value, retagged := p.compileManagedGoLinknameCallResult(b, source, callee, value)
	if !retagged {
		p.recordCoroValueAddress(call, p.coroAwaitResultAddress(b, resultSlot, sourceSig.Results()))
	}
	return value
}
