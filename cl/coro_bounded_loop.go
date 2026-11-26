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
	"go/constant"
	"go/token"
	"go/types"
	"math/big"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// A loop whose trip count alone exceeds this ceiling cannot fit the normal
// 128-instruction preemption budget in practice. The cap also keeps a hostile
// constant loop from turning ProgramIR construction into an unbounded proof.
const coroBoundedLoopTripProofLimit = 4096

// coroCanonicalLoop describes a single natural backedge controlled by a
// canonical integer induction phi in header with a non-zero constant step.
type coroCanonicalLoop struct {
	header        *ssa.BasicBlock
	latch         *ssa.BasicBlock
	entryPred     int
	latchPred     int
	bodySuccessor int
	loopBlocks    map[*ssa.BasicBlock]bool
	induction     *ssa.Phi
	boundValue    ssa.Value
	operator      token.Token
	startValue    ssa.Value
	step          *big.Int
	minimum       *big.Int
	maximum       *big.Int
	unsigned      bool
}

func coroMatchCanonicalLoop(
	prog llssa.Program,
	function *ssa.Function,
	included map[*ssa.BasicBlock]bool,
	evaluated func(ssa.Instruction) bool,
	successors func(*ssa.BasicBlock) []*ssa.BasicBlock,
) (coroCanonicalLoop, bool) {
	if prog == nil || function == nil || len(function.Blocks) == 0 {
		return coroCanonicalLoop{}, false
	}

	type backedge struct{ latch, header *ssa.BasicBlock }
	var edge backedge
	backedges := 0
	for _, block := range function.Blocks {
		if !included[block] {
			continue
		}
		for _, successor := range successors(block) {
			if included[successor] && successor.Dominates(block) {
				edge = backedge{latch: block, header: successor}
				backedges++
			}
		}
	}
	if backedges != 1 || edge.header == edge.latch ||
		coroCFGSubsetHasCycleSkippingEdge(function.Blocks, included, edge.latch, edge.header, successors) {
		return coroCanonicalLoop{}, false
	}

	header, latch := edge.header, edge.latch
	if len(header.Preds) != 2 || len(header.Succs) != 2 || !included[header.Succs[0]] || !included[header.Succs[1]] {
		return coroCanonicalLoop{}, false
	}
	latchPred := -1
	for index, predecessor := range header.Preds {
		if predecessor == latch {
			latchPred = index
		}
	}
	if latchPred < 0 {
		return coroCanonicalLoop{}, false
	}
	entryPred := 1 - latchPred

	loopBlocks := map[*ssa.BasicBlock]bool{header: true, latch: true}
	stack := []*ssa.BasicBlock{latch}
	for len(stack) != 0 {
		block := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if block == header {
			continue
		}
		for _, predecessor := range block.Preds {
			if !included[predecessor] || loopBlocks[predecessor] {
				continue
			}
			if !header.Dominates(predecessor) {
				return coroCanonicalLoop{}, false
			}
			loopBlocks[predecessor] = true
			stack = append(stack, predecessor)
		}
	}
	if loopBlocks[header.Preds[entryPred]] {
		return coroCanonicalLoop{}, false
	}
	bodySuccessor := -1
	for index, successor := range header.Succs {
		if loopBlocks[successor] {
			if bodySuccessor >= 0 {
				return coroCanonicalLoop{}, false
			}
			bodySuccessor = index
		}
	}
	if bodySuccessor < 0 || loopBlocks[header.Succs[1-bodySuccessor]] {
		return coroCanonicalLoop{}, false
	}

	if len(header.Instrs) == 0 {
		return coroCanonicalLoop{}, false
	}
	branch, ok := header.Instrs[len(header.Instrs)-1].(*ssa.If)
	if !ok || !evaluated(branch) {
		return coroCanonicalLoop{}, false
	}
	comparison, ok := branch.Cond.(*ssa.BinOp)
	if !ok || comparison.Block() != header || !evaluated(comparison) {
		return coroCanonicalLoop{}, false
	}

	operator := comparison.Op
	induction, ok := comparison.X.(*ssa.Phi)
	boundValue := comparison.Y
	if !ok || induction.Block() != header {
		induction, ok = comparison.Y.(*ssa.Phi)
		boundValue = comparison.X
		operator = coroSwapComparison(operator)
	}
	if !ok || induction.Block() != header || len(induction.Edges) != 2 {
		return coroCanonicalLoop{}, false
	}
	if bodySuccessor == 1 {
		operator = coroInvertComparison(operator)
	}
	if !coroSupportedLoopComparison(operator) {
		return coroCanonicalLoop{}, false
	}

	update, ok := induction.Edges[latchPred].(*ssa.BinOp)
	if !ok || update.Block() != latch || !evaluated(update) || !types.Identical(update.Type(), induction.Type()) {
		return coroCanonicalLoop{}, false
	}
	step, ok := coroCanonicalInductionStep(induction, update)
	if !ok || step.Sign() == 0 {
		return coroCanonicalLoop{}, false
	}

	basic, ok := induction.Type().Underlying().(*types.Basic)
	if !ok || basic.Info()&types.IsInteger == 0 {
		return coroCanonicalLoop{}, false
	}
	bytes := prog.PhysicalSizeOfGoType(induction.Type())
	if bytes <= 0 || bytes > 8 {
		return coroCanonicalLoop{}, false
	}
	unsigned := basic.Info()&types.IsUnsigned != 0
	minimum, maximum := coroIntegerBounds(uint(bytes*8), unsigned)

	return coroCanonicalLoop{
		header:        header,
		latch:         latch,
		entryPred:     entryPred,
		latchPred:     latchPred,
		bodySuccessor: bodySuccessor,
		loopBlocks:    loopBlocks,
		induction:     induction,
		boundValue:    boundValue,
		operator:      operator,
		startValue:    induction.Edges[entryPred],
		step:          step,
		minimum:       minimum,
		maximum:       maximum,
		unsigned:      unsigned,
	}, true
}

// coroBoundedCycleCost recognizes one deliberately narrow loop language. The
// default remains fail-closed: a cyclic function owns a scheduler poll unless
// its frozen ProgramIR proves one natural backedge controlled by a canonical
// integer phi with constant start, step, and bound. Removing the backedge must
// make the complete evaluated CFG acyclic, so this proof cannot hide a nested
// or irreducible loop.
//
// The returned cost counts local semantic instruction occurrences. A call is
// one occurrence; the callee's independently planned effect and preemption
// requirements still propagate through the ordinary call graph.
func coroBoundedCycleCost(
	prog llssa.Program,
	function *ssa.Function,
	semantic map[ssa.Instruction]coroSemanticInstructionPlan,
	included map[*ssa.BasicBlock]bool,
	blocks []coro.SSAAtomicBlockFacts,
	successors func(*ssa.BasicBlock) []*ssa.BasicBlock,
) (uint64, bool) {
	if len(blocks) != len(function.Blocks) {
		return 0, false
	}
	loop, ok := coroMatchCanonicalLoop(prog, function, included, func(ins ssa.Instruction) bool {
		return semantic[ins].evaluated
	}, successors)
	if !ok {
		return 0, false
	}
	start, ok := coroSSAIntegerConstant(loop.startValue)
	if !ok {
		return 0, false
	}
	bound, ok := coroSSAIntegerConstant(loop.boundValue)
	if !ok {
		return 0, false
	}
	if !coroIntegerFits(start, loop.minimum, loop.maximum) || !coroIntegerFits(bound, loop.minimum, loop.maximum) {
		return 0, false
	}
	iterations, ok := coroConstantTripCount(start, loop.step, bound, loop.operator, loop.minimum, loop.maximum)
	if !ok {
		return 0, false
	}

	loopCost, headerCost, outsideCost := new(big.Int), new(big.Int), new(big.Int)
	for _, block := range function.Blocks {
		if !included[block] {
			continue
		}
		if block.Index < 0 || block.Index >= len(blocks) || blocks[block.Index].Index != block.Index {
			return 0, false
		}
		cost := new(big.Int).SetUint64(blocks[block.Index].LocalCost)
		switch {
		case block == loop.header:
			headerCost.Set(cost)
		case loop.loopBlocks[block]:
			loopCost.Add(loopCost, cost)
		default:
			outsideCost.Add(outsideCost, cost)
		}
	}
	// The header executes once more to observe loop termination. Summing every
	// other natural-loop block per iteration is deliberately conservative for
	// branches while remaining exact enough to gate small fixed-trip loops.
	iterationCount := new(big.Int).SetUint64(iterations)
	upper := new(big.Int).Mul(loopCost, iterationCount)
	upper.Add(upper, new(big.Int).Mul(headerCost, new(big.Int).Add(iterationCount, big.NewInt(1))))
	upper.Add(upper, outsideCost)
	if upper.Sign() <= 0 || !upper.IsUint64() {
		return 0, false
	}
	return upper.Uint64(), true
}

type coroBoundedLoopBudgetProof struct {
	loop            coroCanonicalLoop
	start           *big.Int
	maxBackedges    uint32
	hasInteriorExit bool
}

// coroProveBoundedLoopTripBudget reuses the compiler's canonical single-loop
// matcher and block LocalCost model to prove an exact maximum backedge count
// whose worst-case execution (both normal completion and bailout) stays within
// workBudget non-debug SSA instructions.
func coroProveBoundedLoopTripBudget(
	prog llssa.Program,
	function *ssa.Function,
	included map[*ssa.BasicBlock]bool,
	evaluated func(ssa.Instruction) bool,
	blockCost func(*ssa.BasicBlock) uint64,
	successors func(*ssa.BasicBlock) []*ssa.BasicBlock,
	workBudget uint64,
) (coroBoundedLoopBudgetProof, bool) {
	if workBudget == 0 {
		return coroBoundedLoopBudgetProof{}, false
	}
	loop, ok := coroMatchCanonicalLoop(prog, function, included, evaluated, successors)
	if !ok {
		return coroBoundedLoopBudgetProof{}, false
	}
	start, ok := coroEvalSSAIntConstExpr(loop.startValue, 0)
	if !ok || !coroIntegerFits(start, loop.minimum, loop.maximum) {
		return coroBoundedLoopBudgetProof{}, false
	}
	if !coroValueIsLoopInvariant(loop.boundValue, loop.loopBlocks, 0) {
		return coroBoundedLoopBudgetProof{}, false
	}
	// Require monotonic progress toward the loop-invariant bound.
	if loop.step.Sign() > 0 {
		if loop.operator != token.LSS && loop.operator != token.LEQ {
			return coroBoundedLoopBudgetProof{}, false
		}
	} else {
		if loop.operator != token.GTR && loop.operator != token.GEQ {
			return coroBoundedLoopBudgetProof{}, false
		}
	}

	var headerCost, loopCost, outsideCost uint64
	hasInteriorExit := false
	for _, block := range function.Blocks {
		if !included[block] {
			continue
		}
		cost := blockCost(block)
		switch {
		case block == loop.header:
			headerCost = cost
		case loop.loopBlocks[block]:
			loopCost += cost
			for _, succ := range successors(block) {
				if included[succ] && !loop.loopBlocks[succ] {
					hasInteriorExit = true
				}
			}
		default:
			outsideCost += cost
		}
	}
	perIter := headerCost + loopCost
	if perIter == 0 || outsideCost+perIter > workBudget {
		return coroBoundedLoopBudgetProof{}, false
	}
	// With rem initialized to maxBackedges and checked at the latch before
	// taking the backedge, at most (maxBackedges + 1) passes through header and
	// loop body can execute on either a normal exit or a bailout.
	maxPasses := (workBudget - outsideCost) / perIter
	if maxPasses < 2 {
		return coroBoundedLoopBudgetProof{}, false
	}
	maxBackedges := maxPasses - 1
	if maxBackedges > coroBoundedLoopTripProofLimit {
		maxBackedges = coroBoundedLoopTripProofLimit
	}

	// Prove the induction variable cannot wrap during the bounded fast loop.
	endVal := new(big.Int).Mul(loop.step, new(big.Int).SetUint64(maxBackedges+1))
	endVal.Add(endVal, start)
	if !coroIntegerFits(endVal, loop.minimum, loop.maximum) {
		return coroBoundedLoopBudgetProof{}, false
	}

	return coroBoundedLoopBudgetProof{
		loop:            loop,
		start:           start,
		maxBackedges:    uint32(maxBackedges),
		hasInteriorExit: hasInteriorExit,
	}, true
}

func coroEvalSSAIntConstExpr(value ssa.Value, depth int) (*big.Int, bool) {
	if value == nil || depth > 4 {
		return nil, false
	}
	if c, ok := coroSSAIntegerConstant(value); ok {
		return c, true
	}
	bin, ok := value.(*ssa.BinOp)
	if !ok {
		return nil, false
	}
	l, ok1 := coroEvalSSAIntConstExpr(bin.X, depth+1)
	r, ok2 := coroEvalSSAIntConstExpr(bin.Y, depth+1)
	if !ok1 || !ok2 {
		return nil, false
	}
	switch bin.Op {
	case token.ADD:
		return new(big.Int).Add(l, r), true
	case token.SUB:
		return new(big.Int).Sub(l, r), true
	default:
		return nil, false
	}
}

func coroValueIsLoopInvariant(value ssa.Value, loopBlocks map[*ssa.BasicBlock]bool, depth int) bool {
	if value == nil || depth > 4 {
		return false
	}
	switch v := value.(type) {
	case *ssa.Parameter, *ssa.Const, *ssa.Global:
		return true
	case *ssa.Call:
		if !loopBlocks[v.Block()] {
			return true
		}
		return isPureLenCapBuiltin(v) && coroValueIsLoopInvariant(v.Call.Args[0], loopBlocks, depth+1)
	case *ssa.Convert:
		if !loopBlocks[v.Block()] {
			return true
		}
		return coroValueIsLoopInvariant(v.X, loopBlocks, depth+1)
	case *ssa.ChangeType:
		if !loopBlocks[v.Block()] {
			return true
		}
		return coroValueIsLoopInvariant(v.X, loopBlocks, depth+1)
	case *ssa.Field:
		if !loopBlocks[v.Block()] {
			return true
		}
		return coroValueIsLoopInvariant(v.X, loopBlocks, depth+1)
	case ssa.Instruction:
		return v.Block() != nil && !loopBlocks[v.Block()]
	default:
		return false
	}
}

func coroSSAIntegerConstant(value ssa.Value) (*big.Int, bool) {
	constantValue, ok := value.(*ssa.Const)
	if !ok || constantValue.Value == nil {
		return nil, false
	}
	integer := constant.ToInt(constantValue.Value)
	if integer.Kind() != constant.Int {
		return nil, false
	}
	switch value := constant.Val(integer).(type) {
	case int64:
		return big.NewInt(value), true
	case *big.Int:
		return new(big.Int).Set(value), true
	default:
		return nil, false
	}
}

func coroCanonicalInductionStep(induction *ssa.Phi, update *ssa.BinOp) (*big.Int, bool) {
	switch update.Op {
	case token.ADD:
		if update.X == induction {
			return coroSSAIntegerConstant(update.Y)
		}
		if update.Y == induction {
			return coroSSAIntegerConstant(update.X)
		}
	case token.SUB:
		if update.X == induction {
			step, ok := coroSSAIntegerConstant(update.Y)
			if ok {
				return step.Neg(step), true
			}
		}
	}
	return nil, false
}

func coroSwapComparison(operator token.Token) token.Token {
	switch operator {
	case token.LSS:
		return token.GTR
	case token.LEQ:
		return token.GEQ
	case token.GTR:
		return token.LSS
	case token.GEQ:
		return token.LEQ
	case token.EQL, token.NEQ:
		return operator
	default:
		return token.ILLEGAL
	}
}

func coroInvertComparison(operator token.Token) token.Token {
	switch operator {
	case token.LSS:
		return token.GEQ
	case token.LEQ:
		return token.GTR
	case token.GTR:
		return token.LEQ
	case token.GEQ:
		return token.LSS
	case token.EQL:
		return token.NEQ
	case token.NEQ:
		return token.EQL
	default:
		return token.ILLEGAL
	}
}

func coroSupportedLoopComparison(operator token.Token) bool {
	switch operator {
	case token.LSS, token.LEQ, token.GTR, token.GEQ, token.EQL, token.NEQ:
		return true
	default:
		return false
	}
}

func coroIntegerBounds(bits uint, unsigned bool) (*big.Int, *big.Int) {
	if unsigned {
		return new(big.Int), new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), bits), big.NewInt(1))
	}
	high := new(big.Int).Lsh(big.NewInt(1), bits-1)
	return new(big.Int).Neg(new(big.Int).Set(high)), new(big.Int).Sub(high, big.NewInt(1))
}

func coroIntegerFits(value, minimum, maximum *big.Int) bool {
	return value.Cmp(minimum) >= 0 && value.Cmp(maximum) <= 0
}

func coroEvaluateIntegerComparison(left, right *big.Int, operator token.Token) bool {
	comparison := left.Cmp(right)
	switch operator {
	case token.LSS:
		return comparison < 0
	case token.LEQ:
		return comparison <= 0
	case token.GTR:
		return comparison > 0
	case token.GEQ:
		return comparison >= 0
	case token.EQL:
		return comparison == 0
	case token.NEQ:
		return comparison != 0
	default:
		return false
	}
}

func coroConstantTripCount(
	start, step, bound *big.Int,
	operator token.Token,
	minimum, maximum *big.Int,
) (uint64, bool) {
	current := new(big.Int).Set(start)
	for iterations := uint64(0); iterations <= coroBoundedLoopTripProofLimit; iterations++ {
		if !coroEvaluateIntegerComparison(current, bound, operator) {
			return iterations, true
		}
		if iterations == coroBoundedLoopTripProofLimit {
			break
		}
		current.Add(current, step)
		if !coroIntegerFits(current, minimum, maximum) {
			return 0, false
		}
	}
	return 0, false
}
