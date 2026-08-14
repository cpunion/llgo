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
	"sort"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// coroProgramIR owns production SitePlan state. Later replacement cohorts add
// global summaries, physical control and storage projections to this same
// object rather than creating independently-versioned lowering documents.
type coroProgramIR struct {
	sitePlans                map[emissionFunctionOwnerKey]map[ssa.Instruction]coroEmissionSitePlan
	siteOwners               map[emissionFunctionOwnerKey]none
	functionPreambles        map[emissionFunctionOwnerKey]coroFunctionPreamblePlan
	semanticPlans            map[emissionFunctionOwnerKey]map[ssa.Instruction]coroSemanticInstructionPlan
	localBodyFacts           map[*ssa.Function]coro.SSAFunctionBodyFacts
	callPlans                map[ssa.CallInstruction]coroFrozenCallSitePlan
	erasedFunctionInterfaces map[*ssa.MakeInterface]none
	callsFrozen              bool
	wasmImports              map[*ssa.Function]wasmImportSpec
	cgoDirectReturns         map[*ssa.Return]ssa.Value
	physicalPlans            map[emissionFunctionOwnerKey]*coroPhysicalFunctionPlan
	physicalPlansSealed      bool
}

// coroProgramIRBuilder is the sole mutable construction capability for hidden
// function- and instruction-level recipes. It reuses the canonical index so
// planning cannot acquire a second function-identity authority.
type coroProgramIRBuilder struct {
	canonical emissionCanonicalIndex
}

func newCoroProgramIR() *coroProgramIR {
	return &coroProgramIR{
		sitePlans:                make(map[emissionFunctionOwnerKey]map[ssa.Instruction]coroEmissionSitePlan),
		siteOwners:               make(map[emissionFunctionOwnerKey]none),
		functionPreambles:        make(map[emissionFunctionOwnerKey]coroFunctionPreamblePlan),
		semanticPlans:            make(map[emissionFunctionOwnerKey]map[ssa.Instruction]coroSemanticInstructionPlan),
		localBodyFacts:           make(map[*ssa.Function]coro.SSAFunctionBodyFacts),
		callPlans:                make(map[ssa.CallInstruction]coroFrozenCallSitePlan),
		erasedFunctionInterfaces: make(map[*ssa.MakeInterface]none),
		wasmImports:              make(map[*ssa.Function]wasmImportSpec),
		cgoDirectReturns:         make(map[*ssa.Return]ssa.Value),
		physicalPlans:            make(map[emissionFunctionOwnerKey]*coroPhysicalFunctionPlan),
	}
}

func (ir *coroProgramIR) erasedFunctionInterface(box *ssa.MakeInterface) (bool, error) {
	if ir == nil || !ir.callsFrozen {
		return false, fmt.Errorf("erased function-interface facts are not frozen")
	}
	if box == nil || box.Parent() == nil {
		return false, fmt.Errorf("erased function-interface lookup requires one exact SSA box")
	}
	_, found := ir.erasedFunctionInterfaces[box]
	return found, nil
}

func (ir *coroProgramIR) wasmImport(function *ssa.Function) (wasmImportSpec, bool, error) {
	var zero wasmImportSpec
	if ir == nil || !ir.callsFrozen {
		return zero, false, fmt.Errorf("wasm import facts are not frozen")
	}
	if function == nil {
		return zero, false, fmt.Errorf("wasm import lookup requires one exact function")
	}
	spec, present := ir.wasmImports[function]
	return spec, present, nil
}

func (ir *coroProgramIR) freezeFunctionPreamble(
	function *ssa.Function,
	owner *preparedEmissionPackage,
	plan coroFunctionPreamblePlan,
) error {
	if ir == nil || function == nil || owner == nil {
		return fmt.Errorf("function preamble requires one exact program IR, function, and owner")
	}
	key := emissionFunctionOwnerKey{function: function, owner: owner}
	if _, sealed := ir.siteOwners[key]; sealed {
		return fmt.Errorf("function preamble for %q was added after owner freeze", function.Name())
	}
	if err := plan.validate(); err != nil {
		return err
	}
	plan = cloneCoroFunctionPreamblePlan(plan)
	if previous, exists := ir.functionPreambles[key]; exists {
		if !sameCoroFunctionPreamblePlan(previous, plan) {
			return fmt.Errorf("function %q acquired conflicting preamble plans", function.Name())
		}
		return fmt.Errorf("function preamble for %q was frozen more than once", function.Name())
	}
	ir.functionPreambles[key] = plan
	return nil
}

func (ir *coroProgramIR) freezeCgoDirectReturns(plan *emissionCgoLoweringPlan) error {
	if ir == nil || plan == nil {
		return fmt.Errorf("cgo direct-return plan requires one exact program IR and lowering plan")
	}
	for ret, value := range plan.directReturns {
		if ret == nil || value == nil || ret.Parent() == nil || value.Parent() != ret.Parent() {
			return fmt.Errorf("cgo direct-return recipe has an invalid source value")
		}
		if previous, exists := ir.cgoDirectReturns[ret]; exists && previous != value {
			return fmt.Errorf("cgo return in %q acquired owner-dependent direct values", ret.Parent().Name())
		}
		ir.cgoDirectReturns[ret] = value
	}
	return nil
}

func (ir *coroProgramIR) cgoDirectReturn(ret *ssa.Return) (ssa.Value, bool, error) {
	if ir == nil || !ir.callsFrozen {
		return nil, false, fmt.Errorf("cgo direct-return recipes are not frozen")
	}
	if ret == nil || ret.Parent() == nil {
		return nil, false, fmt.Errorf("cgo direct-return lookup requires one exact return")
	}
	value, found := ir.cgoDirectReturns[ret]
	return value, found, nil
}

func (ir *coroProgramIR) freezeSemanticInstruction(
	function *ssa.Function,
	owner *preparedEmissionPackage,
	instruction ssa.Instruction,
	evaluated bool,
) error {
	if ir == nil || function == nil || owner == nil || instruction == nil || instruction.Parent() != function {
		return fmt.Errorf("semantic SitePlan requires one exact program IR, owner, and source instruction")
	}
	key := emissionFunctionOwnerKey{function: function, owner: owner}
	if _, sealed := ir.siteOwners[key]; sealed {
		return fmt.Errorf("semantic SitePlan for function %q owner %q was added after owner freeze", function.Name(), owner.identity)
	}
	plan, err := planCoroSemanticInstruction(instruction)
	if err != nil {
		return err
	}
	if evaluated {
		plan.evaluated = true
	} else {
		plan = coroSemanticInstructionPlan{
			class:  coro.OpPure,
			recipe: coro.RecipeID("cl.ssa.frontend-unevaluated.v0"),
			effect: coro.NoSuspend,
		}
	}
	byInstruction := ir.semanticPlans[key]
	if byInstruction == nil {
		byInstruction = make(map[ssa.Instruction]coroSemanticInstructionPlan)
		ir.semanticPlans[key] = byInstruction
	}
	if previous, exists := byInstruction[instruction]; exists {
		if previous != plan {
			return fmt.Errorf("source instruction acquired conflicting semantic recipes")
		}
		return nil
	}
	byInstruction[instruction] = plan
	return nil
}

func (ir *coroProgramIR) freezeSite(function *ssa.Function, owner *preparedEmissionPackage, instruction ssa.Instruction, plan coroEmissionSitePlan) error {
	if ir == nil || function == nil || owner == nil || instruction == nil || instruction.Parent() != function {
		return fmt.Errorf("requires one exact program IR, owner, and source instruction")
	}
	plan = cloneCoroEmissionSitePlan(plan)
	key := emissionFunctionOwnerKey{function: function, owner: owner}
	byInstruction := ir.sitePlans[key]
	if byInstruction == nil {
		byInstruction = make(map[ssa.Instruction]coroEmissionSitePlan)
		ir.sitePlans[key] = byInstruction
	}
	if previous, exists := byInstruction[instruction]; exists {
		if !sameCoroEmissionSitePlan(previous, plan) {
			return fmt.Errorf("source instruction acquired conflicting helper plans")
		}
		return nil
	}
	if len(plan.managedRuntimeHelpers) != 0 || len(plan.plainRuntimeHelpers) != 0 ||
		plan.plainAllocation.borrowed() ||
		len(plan.localityDispatchers) != 0 {
		byInstruction[instruction] = plan
	}
	return nil
}

func (ir *coroProgramIR) freezeSiteOwner(
	prog llssa.Program,
	function *ssa.Function,
	owner *preparedEmissionPackage,
) error {
	if ir == nil || prog == nil || function == nil || owner == nil {
		return fmt.Errorf("coroutine site plan owner requires an exact program IR, function, and owner")
	}
	key := emissionFunctionOwnerKey{function: function, owner: owner}
	if _, frozen := ir.siteOwners[key]; frozen {
		return fmt.Errorf("coroutine site plan owner %q was frozen more than once", function.Name())
	}
	if _, frozen := ir.functionPreambles[key]; !frozen {
		return fmt.Errorf("coroutine site plan owner %q has no frozen function preamble", function.Name())
	}
	semantic := ir.semanticPlans[key]
	preamble := ir.functionPreambles[key]
	facts, err := deriveCoroLocalBodyFacts(prog, function, semantic, preamble.emitsGoBody)
	if err != nil {
		return err
	}
	if previous, exists := ir.localBodyFacts[function]; exists && !previous.Same(facts) {
		return fmt.Errorf("function %q acquired owner-dependent local semantic facts", function.Name())
	}
	ir.localBodyFacts[function] = facts
	ir.siteOwners[key] = none{}
	return nil
}

// deriveCoroLocalBodyFacts projects one already-frozen semantic instruction
// table into the analyzer-owned local body summary. Call-site finalization may
// invoke it a second time after replacing an exact compiler-elided intrinsic
// call with its narrower semantic recipe. Keeping the projection here avoids
// an analysis-time raw SSA rescan and keeps ProgramIR as the sole authority.
func deriveCoroLocalBodyFacts(
	prog llssa.Program,
	function *ssa.Function,
	semantic map[ssa.Instruction]coroSemanticInstructionPlan,
	outcomePlainEligible bool,
) (coro.SSAFunctionBodyFacts, error) {
	if prog == nil || function == nil {
		return coro.SSAFunctionBodyFacts{}, fmt.Errorf("local body facts require one exact program and function")
	}
	facts := coro.SSAFunctionBodyFacts{
		Effect:             coro.NoSuspend,
		OutcomePlainLeaf:   function.Blocks != nil,
		OutcomePlainDAG:    function.Blocks != nil,
		StaticOutcomeLocal: function.Blocks != nil,
	}
	if function.Blocks != nil {
		facts.Exec = coro.MayUnwind
	}
	if coroRuntimeContextPrimitive(function) {
		// These are the only bottom-level runtime operations which observe or
		// replace the ambient logical G. All ordinary Go wrappers acquire this
		// bit through the normal call fixed point, including across imported
		// library summaries; source annotations are neither required nor read.
		facts.Exec = facts.Exec.Join(coro.NeedsRuntimeContext)
	}
	atomicBlocks := make([]coro.SSAAtomicBlockFacts, len(function.Blocks))
	hasEvaluatedDefer := false
	for _, block := range function.Blocks {
		blockFacts := coro.SSAAtomicBlockFacts{Index: block.Index}
		for _, successor := range block.Succs {
			blockFacts.Successors = append(blockFacts.Successors, successor.Index)
		}
		sort.Ints(blockFacts.Successors)
		for instructionIndex, instruction := range block.Instrs {
			plan, ok := semantic[instruction]
			if !ok {
				return coro.SSAFunctionBodyFacts{}, fmt.Errorf("coroutine semantic SitePlan owner %q omitted source instruction %q", function.Name(), instruction.String())
			}
			if !plan.evaluated {
				facts.OutcomePlainLeaf = false
				facts.OutcomePlainDAG = false
				continue
			}
			if _, deferInstruction := instruction.(*ssa.Defer); deferInstruction {
				hasEvaluatedDefer = true
			}
			if !plan.staticOutcome {
				facts.StaticOutcomeLocal = false
			}
			if !coroOutcomePlainLeafSemanticRecipe(plan) {
				facts.OutcomePlainLeaf = false
			}
			if !coroOutcomePlainDAGSemanticRecipe(plan) {
				facts.OutcomePlainDAG = false
			}
			if plan.recipe == coro.RecipeID("cl.ssa.call.v1") {
				facts.OutcomePlainCallCount++
				call, ok := instruction.(*ssa.Call)
				if !ok {
					return coro.SSAFunctionBodyFacts{}, fmt.Errorf("coroutine call recipe in %q is attached to %T", function.Name(), instruction)
				}
				blockFacts.Calls = append(blockFacts.Calls, coro.SSAAtomicCallSiteFacts{
					Instruction:      call,
					InstructionIndex: instructionIndex,
				})
				if call.Common() == nil || call.Common().Signature() == nil ||
					prog.LocalGoTypeExceedsNativeStack(
						newOutcomePlainPhysicalABI(call.Common().Signature()).resultSlotType,
					) {
					// A full coroutine caller can move a large child result into
					// managed frame storage. A synchronous DAG body cannot acquire
					// that hidden allocation, so reject the optimization while the
					// ordinary coroutine fallback is still available.
					facts.OutcomePlainDAG = false
					// An unbounded static-outcome body has the same synchronous
					// caller-owned result-slot constraint. Its full coroutine primary
					// may use a managed frame slot, but the native twin must not place
					// an oversized child result on a fixed stack.
					facts.StaticOutcomeLocal = false
				}
			}
			facts.Effect = facts.Effect.Join(plan.effect)
			facts.Exec = facts.Exec.Join(plan.exec)
			if !plan.debug {
				facts.InstructionCount++
				blockFacts.LocalCost++
			}
		}
		atomicBlocks[block.Index] = blockFacts
	}
	facts.Effect = facts.Effect.Normalize()
	if !hasEvaluatedDefer {
		// x/tools retains RunDefers on normal returns whenever the function has
		// any syntactic defer, including one behind a constant-dead branch. With
		// no evaluated registration site it is semantically a no-op and does not
		// require a cleanup frame.
		facts.Exec &^= coro.NeedsCleanupFrame
		for instruction, plan := range semantic {
			if _, runDefers := instruction.(*ssa.RunDefers); !runDefers || !plan.evaluated {
				continue
			}
			plan.exec &^= coro.NeedsCleanupFrame
			plan.materialized = false
			semantic[instruction] = plan
		}
	}
	facts.HasCycle = coroSemanticEvaluatedCFGHasCycle(function.Blocks, semantic)
	if len(function.Blocks) != 0 {
		atomicPath, err := coro.NewSSAAtomicPathFacts(function, atomicBlocks)
		if err != nil {
			return coro.SSAFunctionBodyFacts{}, fmt.Errorf("function %q atomic path projection: %w", function.Name(), err)
		}
		facts.AtomicPath = atomicPath
	} else {
		facts.OutcomePlainLeaf = false
		facts.OutcomePlainDAG = false
		facts.StaticOutcomeLocal = false
	}
	if !outcomePlainEligible {
		facts.OutcomePlainLeaf = false
		facts.OutcomePlainDAG = false
	}
	return facts, nil
}

func coroRuntimeContextPrimitive(function *ssa.Function) bool {
	if function == nil || function.Pkg == nil || function.Pkg.Pkg == nil ||
		llssa.PathOf(function.Pkg.Pkg) != "github.com/goplus/llgo/runtime/internal/runtime" {
		return false
	}
	switch function.Name() {
	case "getg", "getgIfPresent", "setg", "setgRaw":
		return true
	default:
		return false
	}
}

// coroOutcomePlainLeafSemanticRecipe consumes the capability already frozen by
// the sole raw-SSA semantic classifier. Analysis and emission therefore cannot
// independently reinterpret a source operation. Calls, allocation,
// implicit-fault-capable values, defer, spawn, waits and every platform
// operation fail closed.
func coroOutcomePlainLeafSemanticRecipe(plan coroSemanticInstructionPlan) bool {
	return plan.debug || plan.outcomePlainLeaf
}

// coroOutcomePlainDAGSemanticRecipe is the deliberately small extension of the
// V0 leaf language. Call target/transport semantics are not inferred here;
// AnalyzeSSA must match every counted call against its frozen CallPlan before
// this local candidate can acquire a physical outcome entry.
func coroOutcomePlainDAGSemanticRecipe(plan coroSemanticInstructionPlan) bool {
	if coroOutcomePlainLeafSemanticRecipe(plan) {
		return true
	}
	switch plan.recipe {
	case coro.RecipeID("cl.ssa.call.v1"):
		return true
	default:
		return false
	}
}

// finalizeOutcomePlainIntrinsicSemantics narrows exact compiler-elided source
// calls after the call-site table is complete. Raw SSA alone cannot know that
// a declaration call will become one helper-free LLVM operation, so the first
// semantic pass deliberately classifies it as an ordinary call. This final
// ProgramIR builder step admits only operations whose frozen call recipe is
// independently shape-checked and allocation-free.
//
// Atomic intrinsics acquire their bounded leaf proof here. A real inline yield
// is also recorded here as an evaluated local effect: this distinguishes an
// explicit scheduler handoff from the synthetic YieldOnly/NeedsPreempt seed
// added later for an otherwise synchronous CFG loop. Static outcome twins may
// omit the latter under the current compute-blocking policy, but must never
// erase the former. Other InlineNoSuspend intrinsics include asm, dynamic
// alloca, control transfer, and target-specific operations; the broad enum is
// therefore not by itself an outcome-plain proof.
func (ir *coroProgramIR) finalizeOutcomePlainIntrinsicSemantics(
	prog llssa.Program,
	functions []*ssa.Function,
	sortedUseOwners func(*ssa.Function) []*preparedEmissionPackage,
) error {
	if ir == nil || prog == nil || sortedUseOwners == nil {
		return fmt.Errorf("outcome-plain intrinsic finalization requires one exact ProgramIR input")
	}
	if ir.callsFrozen {
		return fmt.Errorf("outcome-plain intrinsic finalization occurred after call SitePlan freeze")
	}
	for _, function := range functions {
		if function == nil || len(function.Blocks) == 0 {
			continue
		}
		refined := false
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				call, ordinary := instruction.(*ssa.Call)
				if !ordinary {
					continue
				}
				frozen, found := ir.callPlans[call]
				if !found || frozen.failure != "" || !frozen.plan.Intrinsic ||
					frozen.plan.Elision != CoroCallElidedIntrinsic {
					continue
				}
				atomic := frozen.plan.IntrinsicSemantics == CoroIntrinsicCallInlineNoSuspend &&
					isCoroAtomicIntrinsic(frozen.opcode)
				realYield := frozen.plan.IntrinsicSemantics == CoroIntrinsicCallInlineYield
				if !atomic && !realYield {
					continue
				}
				for _, owner := range sortedUseOwners(function) {
					key := emissionFunctionOwnerKey{function: function, owner: owner}
					if _, sealed := ir.siteOwners[key]; !sealed {
						return fmt.Errorf("atomic intrinsic in %q has no frozen semantic owner %q", function.Name(), owner.identity)
					}
					semantic, present := ir.semanticPlans[key][call]
					if !present || !semantic.evaluated || semantic.recipe != coro.RecipeID("cl.ssa.call.v1") ||
						semantic.effect != coro.NoSuspend || semantic.exec != 0 {
						return fmt.Errorf("atomic intrinsic call %q has an incompatible preliminary semantic recipe", call.String())
					}
					if atomic {
						semantic.recipe = coro.RecipeID("cl.intrinsic.atomic.inline-nosuspend.v1")
						semantic.outcomePlainLeaf = true
						semantic.staticOutcome = true
					} else {
						semantic.recipe, semantic.effect = coroIntrinsicLoweringRecipe(CoroIntrinsicCallInlineYield)
						semantic.materialized = true
						semantic.outcomePlainLeaf = false
						semantic.staticOutcome = false
					}
					ir.semanticPlans[key][call] = semantic
				}
				refined = true
			}
		}
		if !refined {
			continue
		}
		var final coro.SSAFunctionBodyFacts
		for index, owner := range sortedUseOwners(function) {
			key := emissionFunctionOwnerKey{function: function, owner: owner}
			preamble, present := ir.functionPreambles[key]
			if !present {
				return fmt.Errorf("finalize atomic intrinsic body %q: owner %q has no function preamble", function.Name(), owner.identity)
			}
			facts, err := deriveCoroLocalBodyFacts(prog, function, ir.semanticPlans[key], preamble.emitsGoBody)
			if err != nil {
				return fmt.Errorf("finalize atomic intrinsic body %q: %w", function.Name(), err)
			}
			if index != 0 && !final.Same(facts) {
				return fmt.Errorf("function %q acquired owner-dependent finalized local semantic facts", function.Name())
			}
			final = facts
		}
		ir.localBodyFacts[function] = final
	}
	return nil
}

func coroSemanticEvaluatedCFGHasCycle(
	blocks []*ssa.BasicBlock,
	semantic map[ssa.Instruction]coroSemanticInstructionPlan,
) bool {
	evaluated := make(map[*ssa.BasicBlock]bool)
	for _, block := range blocks {
		for _, instruction := range block.Instrs {
			if semantic[instruction].evaluated {
				evaluated[block] = true
				break
			}
		}
	}
	state := make(map[*ssa.BasicBlock]uint8, len(evaluated))
	var visit func(*ssa.BasicBlock) bool
	visit = func(block *ssa.BasicBlock) bool {
		switch state[block] {
		case 1:
			return true
		case 2:
			return false
		}
		state[block] = 1
		for _, successor := range block.Succs {
			if evaluated[successor] && visit(successor) {
				return true
			}
		}
		state[block] = 2
		return false
	}
	for block := range evaluated {
		if state[block] == 0 && visit(block) {
			return true
		}
	}
	return false
}

func (ir *coroProgramIR) semanticInstructionPlan(
	function *ssa.Function,
	owner *preparedEmissionPackage,
	instruction ssa.Instruction,
) (coroSemanticInstructionPlan, error) {
	if ir == nil || function == nil || owner == nil || instruction == nil || instruction.Parent() != function {
		return coroSemanticInstructionPlan{}, fmt.Errorf("semantic SitePlan lookup requires one exact function owner and source instruction")
	}
	key := emissionFunctionOwnerKey{function: function, owner: owner}
	if _, frozen := ir.siteOwners[key]; !frozen {
		return coroSemanticInstructionPlan{}, fmt.Errorf("semantic SitePlan owner is not frozen")
	}
	plan, ok := ir.semanticPlans[key][instruction]
	if !ok {
		return coroSemanticInstructionPlan{}, fmt.Errorf("source instruction %q has no frozen semantic recipe", instruction.String())
	}
	return plan, nil
}

func (ir *coroProgramIR) functionLocalBodyFacts(function *ssa.Function) (coro.SSAFunctionBodyFacts, error) {
	if ir == nil || !ir.callsFrozen || function == nil {
		return coro.SSAFunctionBodyFacts{}, fmt.Errorf("local body facts require one exact function")
	}
	facts, ok := ir.localBodyFacts[function]
	if !ok {
		return coro.SSAFunctionBodyFacts{}, fmt.Errorf("function %q has no frozen local body facts", function.Name())
	}
	return facts.Clone(), nil
}

func (ir *coroProgramIR) sitePlan(ctx *context, instruction ssa.Instruction) (coroEmissionSitePlan, error) {
	if ir == nil || ctx == nil || ctx.emissionOwner == nil || instruction == nil || instruction.Parent() == nil {
		return coroEmissionSitePlan{}, fmt.Errorf("coroutine site plan lookup requires an exact program IR, owner context, and source instruction")
	}
	owner := ctx.emissionOwner
	if physical := ctx.coroEmissionPlan(); physical != nil {
		if physical.function != instruction.Parent() || physical.owner == nil {
			return coroEmissionSitePlan{}, fmt.Errorf("coroutine physical emission plan does not own the requested source instruction")
		}
		owner = physical.owner
	}
	return ir.sitePlanForOwner(owner, instruction)
}

func (ir *coroProgramIR) functionPreamble(ctx *context) (coroFunctionPreamblePlan, error) {
	if ir == nil || ctx == nil || ctx.goFn == nil || ctx.emissionOwner == nil {
		return coroFunctionPreamblePlan{}, fmt.Errorf("function preamble lookup requires an exact program IR, owner context, and function")
	}
	function, owner := ctx.goFn, ctx.emissionOwner
	if physical := ctx.coroEmissionPlan(); physical != nil {
		if physical.function != function || physical.owner == nil {
			return coroFunctionPreamblePlan{}, fmt.Errorf("coroutine physical emission plan does not own the requested function preamble")
		}
		owner = physical.owner
	}
	plan, err := ir.functionPreambleForOwner(function, owner)
	if err == nil {
		return plan, nil
	}
	available := make([]string, 0)
	for key := range ir.siteOwners {
		if key.function == function && key.owner != nil {
			available = append(available, key.owner.identity)
		}
	}
	sort.Strings(available)
	current := "<nil>"
	if owner != nil {
		current = owner.identity
	}
	return coroFunctionPreamblePlan{}, fmt.Errorf(
		"%w (function=%s, current owner=%q, frozen owners=%v)",
		err, coroEntryFunctionDiagnostic(function), current, available,
	)
}

func (ir *coroProgramIR) functionPreambleForOwner(
	function *ssa.Function,
	owner *preparedEmissionPackage,
) (coroFunctionPreamblePlan, error) {
	if ir == nil || function == nil || owner == nil {
		return coroFunctionPreamblePlan{}, fmt.Errorf("function preamble lookup requires an exact program IR, owner, and function")
	}
	key := emissionFunctionOwnerKey{function: function, owner: owner}
	if _, frozen := ir.siteOwners[key]; !frozen {
		return coroFunctionPreamblePlan{}, fmt.Errorf("function preamble has no frozen site-plan owner")
	}
	plan, frozen := ir.functionPreambles[key]
	if !frozen {
		return coroFunctionPreamblePlan{}, fmt.Errorf("function preamble is absent from its frozen owner")
	}
	if err := plan.validate(); err != nil {
		return coroFunctionPreamblePlan{}, err
	}
	return cloneCoroFunctionPreamblePlan(plan), nil
}

// functionPreambleDuringFreeze exposes the already-frozen function-owned
// operations while source SitePlans for that same owner are still being built.
// It deliberately does not require siteOwners to be sealed yet.
func (ir *coroProgramIR) functionPreambleDuringFreeze(
	function *ssa.Function,
	owner *preparedEmissionPackage,
) (coroFunctionPreamblePlan, error) {
	if ir == nil || function == nil || owner == nil {
		return coroFunctionPreamblePlan{}, fmt.Errorf("function preamble build lookup requires one exact program IR, owner, and function")
	}
	key := emissionFunctionOwnerKey{function: function, owner: owner}
	if _, sealed := ir.siteOwners[key]; sealed {
		return coroFunctionPreamblePlan{}, fmt.Errorf("function preamble build lookup for %q occurred after owner freeze", function.Name())
	}
	plan, frozen := ir.functionPreambles[key]
	if !frozen {
		return coroFunctionPreamblePlan{}, fmt.Errorf("function preamble build lookup for %q occurred before preamble freeze", function.Name())
	}
	if err := plan.validate(); err != nil {
		return coroFunctionPreamblePlan{}, err
	}
	return cloneCoroFunctionPreamblePlan(plan), nil
}

func (ir *coroProgramIR) sitePlanForOwner(
	owner *preparedEmissionPackage,
	instruction ssa.Instruction,
) (coroEmissionSitePlan, error) {
	if ir == nil || owner == nil || instruction == nil || instruction.Parent() == nil {
		return coroEmissionSitePlan{}, fmt.Errorf("coroutine site plan lookup requires an exact program IR, owner, and source instruction")
	}
	key := emissionFunctionOwnerKey{function: instruction.Parent(), owner: owner}
	if _, frozen := ir.siteOwners[key]; !frozen {
		return coroEmissionSitePlan{}, fmt.Errorf("coroutine source instruction has no frozen site-plan owner")
	}
	return cloneCoroEmissionSitePlan(ir.sitePlans[key][instruction]), nil
}

func (ir *coroProgramIR) plannedRuntimeHelpers(ctx *context, instruction ssa.Instruction) ([]string, error) {
	plan, err := ir.sitePlan(ctx, instruction)
	if err != nil {
		return nil, err
	}
	helpers := make([]string, len(plan.managedRuntimeHelpers))
	for index, helper := range plan.managedRuntimeHelpers {
		helpers[index] = helper.name
	}
	return helpers, nil
}

func (ir *coroProgramIR) callSitePlan(call ssa.CallInstruction) (coroFrozenCallSitePlan, bool, error) {
	if ir == nil || !ir.callsFrozen {
		return coroFrozenCallSitePlan{}, false, fmt.Errorf("coroutine call SitePlan is not frozen")
	}
	if call == nil || call.Common() == nil || call.Parent() == nil {
		return coroFrozenCallSitePlan{}, false, fmt.Errorf("coroutine call SitePlan lookup requires an exact SSA call")
	}
	plan, ok := ir.callPlans[call]
	return plan, ok, nil
}

func (plan coroEmissionSitePlan) managedRuntimeHelperNames() []string {
	helpers := make([]string, len(plan.managedRuntimeHelpers))
	for index, helper := range plan.managedRuntimeHelpers {
		helpers[index] = helper.name
	}
	return helpers
}

func (plan coroEmissionSitePlan) managedRuntimeHelpersAt(placement coroRuntimeHelperPlacement) []string {
	helpers := make([]string, 0, len(plan.managedRuntimeHelpers))
	for _, helper := range plan.managedRuntimeHelpers {
		if helper.placement == placement {
			helpers = append(helpers, helper.name)
		}
	}
	return helpers
}

func (plan coroEmissionSitePlan) hasManagedRuntimeHelper(name string) bool {
	for _, helper := range plan.managedRuntimeHelpers {
		if helper.name == name {
			return true
		}
	}
	return false
}

func cloneCoroEmissionSitePlan(plan coroEmissionSitePlan) coroEmissionSitePlan {
	plan.managedRuntimeHelpers = append([]coroPlannedRuntimeHelper(nil), plan.managedRuntimeHelpers...)
	plan.plainRuntimeHelpers = append([]string(nil), plan.plainRuntimeHelpers...)
	plan.localityDispatchers = append([]*ssa.Function(nil), plan.localityDispatchers...)
	return plan
}

func sameCoroEmissionSitePlan(first, second coroEmissionSitePlan) bool {
	return slices.Equal(first.managedRuntimeHelpers, second.managedRuntimeHelpers) &&
		slices.Equal(first.plainRuntimeHelpers, second.plainRuntimeHelpers) &&
		first.plainAllocation == second.plainAllocation &&
		slices.Equal(first.localityDispatchers, second.localityDispatchers) &&
		first.hasCallPlan == second.hasCallPlan && sameCoroFrozenCallSitePlan(first.callPlan, second.callPlan)
}
