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
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// coroSiteEmissionObserver binds actual LLSSA runtime-helper emission back to
// the exact pre-analysis SitePlan. It is a compile-time verifier only; no
// observer state enters generated code or the runtime ABI.
type coroSiteEmissionObserver struct {
	instruction                ssa.Instruction
	placement                  coroRuntimeHelperPlacement
	expected                   map[string]none
	seen                       map[string]none
	expectedIntrinsic          bool
	seenIntrinsic              bool
	expectedIntrinsicOpcode    int
	expectedIntrinsicSemantics CoroIntrinsicCallSemantics
	expectedElision            CoroCallElisionKind
	seenElision                bool
	expectedPhysical           coroPhysicalInstructionPlan
	hasExpectedPhysical        bool
	seenSemantic               bool
	seenPhysical               bool
	seenPhysicalControl        bool
	seenPhysicalOperation      bool
	seenPhysicalOutcome        bool
	seenPhysicalNilGuard       bool
	seenPhysicalBoundsGuard    bool
	observeFrozenSite          bool
}

func (p *context) beginCoroSiteEmission(instruction ssa.Instruction) func() {
	return p.beginCoroSiteEmissionMode(instruction, coroRuntimeHelperAtSource, true, true)
}

func (p *context) beginCoroRelocatedSiteEmission(instruction ssa.Instruction, placement coroRuntimeHelperPlacement) func() {
	if placement == coroRuntimeHelperAtSource {
		panic("relocated coroutine SitePlan emission requires a non-source placement")
	}
	return p.beginCoroSiteEmissionMode(instruction, placement, true, false)
}

func (p *context) beginCoroRelocatedIntrinsicEmission(
	instruction ssa.Instruction,
	placement coroRuntimeHelperPlacement,
) func() {
	if placement == coroRuntimeHelperAtSource {
		panic("relocated coroutine intrinsic emission requires a non-source placement")
	}
	return p.beginCoroSiteEmissionMode(instruction, placement, false, true)
}

func (p *context) beginCoroSiteEmissionMode(
	instruction ssa.Instruction,
	placement coroRuntimeHelperPlacement,
	observeHelpers bool,
	observeCall bool,
) func() {
	if p == nil || instruction == nil || p.compilation == nil || p.emissionUniverse == nil ||
		!p.hasCoroPhysicalEmission() || p.rawPlainBody {
		return func() {}
	}
	plan := coroEmissionSitePlan{}
	helpers := []string(nil)
	if p.emissionUniverse.CompleteRuntimeABI() {
		var err error
		physical := p.coroEmissionPlan()
		if placement != coroRuntimeHelperAtSource && physical != nil &&
			physical.function != instruction.Parent() && physical.owner != nil {
			plan, err = p.emissionUniverse.coroProgramIR.sitePlanForOwner(physical.owner, instruction)
		} else {
			plan, err = p.emissionUniverse.coroProgramIR.sitePlan(p, instruction)
		}
		if err != nil {
			panic(fmt.Errorf("coroutine emission site %q: %w", instruction.String(), err))
		}
		if observeHelpers {
			helpers = plan.managedRuntimeHelpersAt(placement)
		}
	}
	physical := coroPhysicalInstructionPlan{}
	hasPhysical := false
	if physicalPlan := p.coroEmissionPlan(); placement == coroRuntimeHelperAtSource && physicalPlan != nil {
		var err error
		physical, err = physicalPlan.instructionPlan(instruction)
		if err != nil {
			panic(fmt.Errorf("coroutine emission site %q: %w", instruction.String(), err))
		}
		hasPhysical = true
	}
	filtered := helpers[:0]
	for _, helper := range helpers {
		if hasPhysical && physical.elidesRuntimeHelper(helper) {
			continue
		}
		filtered = append(filtered, helper)
	}
	helpers = filtered
	observer := &coroSiteEmissionObserver{
		instruction:       instruction,
		placement:         placement,
		expected:          make(map[string]none, len(helpers)),
		seen:              make(map[string]none, len(helpers)),
		observeFrozenSite: p.emissionUniverse.CompleteRuntimeABI(),
	}
	if hasPhysical {
		observer.expectedPhysical = physical
		observer.hasExpectedPhysical = true
		observer.seenPhysical = physical.recipe == coroPhysicalInstructionOrdinary
		observer.seenPhysicalControl = physical.control == coroPhysicalControlNone
		observer.seenPhysicalOperation = physical.operation == coroPhysicalOperationNone
		observer.seenPhysicalOutcome = physical.outcome == coroPhysicalOutcomeNone
	}
	if observeCall && plan.hasCallPlan {
		if plan.callPlan.failure != "" {
			panic(fmt.Errorf("coroutine emission site %q has an invalid frozen call SitePlan: %s", instruction.String(), plan.callPlan.failure))
		}
		callPlan := plan.callPlan.plan
		observer.expectedIntrinsic = callPlan.Intrinsic &&
			callPlan.Elision == CoroCallElidedIntrinsic &&
			plan.callPlan.intrinsicPlacement == placement
		observer.expectedIntrinsicOpcode = plan.callPlan.opcode
		observer.expectedIntrinsicSemantics = callPlan.IntrinsicSemantics
		if placement == coroRuntimeHelperAtSource {
			observer.expectedElision = callPlan.Elision
		}
	}
	for _, helper := range helpers {
		observer.expected[helper] = none{}
	}
	previous := p.coroEmissionSite()
	p.setCoroEmissionSite(observer)
	return func() {
		recovered := recover()
		p.setCoroEmissionSite(previous)
		if recovered != nil {
			panic(recovered)
		}
		if missing := observer.missing(); len(missing) != 0 {
			function := "<unknown>"
			if instruction.Parent() != nil {
				function = instruction.Parent().String()
			}
			physical := "none"
			if observer.hasExpectedPhysical {
				physical = observer.expectedPhysical.recipe.String()
			}
			panic(fmt.Errorf(
				"coroutine emission site %q in %q with physical recipe %s omitted frozen runtime helper(s) %s",
				instruction.String(), function, physical, strings.Join(missing, ", "),
			))
		}
		if observer.expectedIntrinsic && !observer.seenIntrinsic {
			panic(fmt.Errorf(
				"coroutine emission site %q omitted frozen intrinsic recipe %d",
				instruction.String(), observer.expectedIntrinsicSemantics,
			))
		}
		if observer.expectedElision != CoroCallNotElided && !observer.seenElision {
			panic(fmt.Errorf(
				"coroutine emission site %q omitted frozen call elision %d",
				instruction.String(), observer.expectedElision,
			))
		}
		if observer.hasExpectedPhysical && !observer.seenSemantic {
			panic(fmt.Errorf(
				"coroutine emission site %q omitted frozen semantic recipe %s",
				instruction.String(), observer.expectedPhysical.semantic.recipe,
			))
		}
		if observer.hasExpectedPhysical && !observer.seenPhysical {
			panic(fmt.Errorf(
				"coroutine emission site %q omitted frozen physical recipe %s",
				instruction.String(), observer.expectedPhysical.recipe,
			))
		}
		if observer.hasExpectedPhysical && !observer.seenPhysicalControl {
			panic(fmt.Errorf(
				"coroutine emission site %q omitted frozen physical control recipe %s",
				instruction.String(), observer.expectedPhysical.control,
			))
		}
		if observer.hasExpectedPhysical && !observer.seenPhysicalOperation {
			panic(fmt.Errorf(
				"coroutine emission site %q omitted frozen physical operation recipe %s",
				instruction.String(), observer.expectedPhysical.operation,
			))
		}
		if observer.hasExpectedPhysical && !observer.seenPhysicalOutcome {
			panic(fmt.Errorf(
				"coroutine emission site %q omitted frozen physical outcome recipe %s",
				instruction.String(), observer.expectedPhysical.outcome,
			))
		}
		if observer.hasExpectedPhysical && observer.expectedPhysical.nilGuard != observer.seenPhysicalNilGuard {
			panic(fmt.Errorf(
				"coroutine emission site %q physical nil-guard emission=%t, frozen SitePlan requires %t",
				instruction.String(), observer.seenPhysicalNilGuard, observer.expectedPhysical.nilGuard,
			))
		}
		if observer.hasExpectedPhysical && observer.expectedPhysical.boundsGuard != observer.seenPhysicalBoundsGuard {
			panic(fmt.Errorf(
				"coroutine emission site %q physical bounds-guard emission=%t, frozen SitePlan requires %t",
				instruction.String(), observer.seenPhysicalBoundsGuard, observer.expectedPhysical.boundsGuard,
			))
		}
	}
}

func (p *context) observeCoroSemanticInstruction(instruction ssa.Instruction) {
	observer := p.coroEmissionSite()
	if observer == nil {
		return
	}
	if !observer.hasExpectedPhysical || observer.instruction != instruction || observer.expectedPhysical.semantic.recipe == "" {
		panic("coroutine semantic recipe emission has no exact physical SitePlan")
	}
	if observer.seenSemantic {
		panic(fmt.Errorf("coroutine emission site %q emitted its semantic recipe more than once", instruction.String()))
	}
	observer.seenSemantic = true
}

func (p *context) observeCoroPhysicalNilGuard(instruction ssa.Instruction) {
	p.observeCoroPhysicalGuard(instruction, true)
}

func (p *context) observeCoroPhysicalBoundsGuard(instruction ssa.Instruction) {
	p.observeCoroPhysicalGuard(instruction, false)
}

func (p *context) observeCoroPhysicalGuard(instruction ssa.Instruction, nilGuard bool) {
	current := p.coroEmissionSite()
	if current == nil || !current.hasExpectedPhysical || current.instruction != instruction {
		panic("coroutine physical guard emission has no exact source SitePlan")
	}
	observer := current
	expected, seen, name := observer.expectedPhysical.boundsGuard, &observer.seenPhysicalBoundsGuard, "bounds"
	if nilGuard {
		expected, seen, name = observer.expectedPhysical.nilGuard, &observer.seenPhysicalNilGuard, "nil"
	}
	if !expected {
		panic(fmt.Errorf("coroutine emission site %q emitted an unplanned physical %s guard", instruction.String(), name))
	}
	if *seen {
		panic(fmt.Errorf("coroutine emission site %q emitted its physical %s guard more than once", instruction.String(), name))
	}
	*seen = true
}

func (p *context) plannedCoroPhysicalInstruction(instruction ssa.Instruction) (coroPhysicalInstructionPlan, bool) {
	if p == nil || p.coroEmissionPlan() == nil {
		return coroPhysicalInstructionPlan{}, false
	}
	current := p.coroEmissionSite()
	if current == nil || !current.hasExpectedPhysical || current.instruction != instruction {
		panic("coroutine physical recipe selection has no exact source SitePlan")
	}
	return current.expectedPhysical, true
}

func (p *context) plannedCoroPhysicalControl(instruction ssa.Instruction) (coroPhysicalInstructionPlan, bool) {
	if p == nil || p.coroEmissionPlan() == nil {
		return coroPhysicalInstructionPlan{}, false
	}
	current := p.coroEmissionSite()
	if current == nil || !current.hasExpectedPhysical || current.instruction != instruction {
		panic("coroutine physical control selection has no exact source SitePlan")
	}
	return current.expectedPhysical, true
}

func (p *context) plannedCoroPhysicalOperation(instruction ssa.Instruction) (coroPhysicalInstructionPlan, bool) {
	if p == nil || p.coroEmissionPlan() == nil {
		return coroPhysicalInstructionPlan{}, false
	}
	current := p.coroEmissionSite()
	if current == nil || !current.hasExpectedPhysical || current.instruction != instruction {
		panic("coroutine physical operation selection has no exact source SitePlan")
	}
	return current.expectedPhysical, true
}

// selectCoroPhysicalOperation is the shared source-emission gate for inline
// operations which may retain a synchronous fallback. It reads and consumes
// the frozen recipe exactly once; callers decide only whether None is legal.
func (p *context) selectCoroPhysicalOperation(
	instruction ssa.Instruction,
	expected coroPhysicalOperationRecipe,
) (operation coroPhysicalInstructionPlan, selected, planned bool) {
	operation, planned = p.plannedCoroPhysicalOperation(instruction)
	if !planned {
		return coroPhysicalInstructionPlan{}, false, false
	}
	if operation.operation == expected {
		p.observeCoroPhysicalOperation(instruction, expected)
		return operation, true, true
	}
	if operation.operation != coroPhysicalOperationNone {
		panic(fmt.Sprintf(
			"operation selected incompatible frozen physical recipe %s, expected %s",
			operation.operation, expected,
		))
	}
	return operation, false, true
}

func (p *context) plannedCoroPhysicalOutcome(instruction ssa.Instruction) (coroPhysicalInstructionPlan, bool) {
	if p == nil || p.coroEmissionPlan() == nil {
		return coroPhysicalInstructionPlan{}, false
	}
	current := p.coroEmissionSite()
	if current == nil || !current.hasExpectedPhysical || current.instruction != instruction {
		panic("coroutine physical outcome selection has no exact source SitePlan")
	}
	return current.expectedPhysical, true
}

func (p *context) observeCoroPhysicalInstruction(instruction ssa.Instruction, actual coroPhysicalInstructionRecipe) {
	current := p.coroEmissionSite()
	if current == nil || !current.hasExpectedPhysical || current.instruction != instruction {
		panic("coroutine physical recipe emission has no exact source SitePlan")
	}
	observer := current
	if observer.expectedPhysical.recipe != actual {
		panic(fmt.Errorf(
			"coroutine emission site %q emitted physical recipe %s, frozen SitePlan requires %s",
			instruction.String(), actual, observer.expectedPhysical.recipe,
		))
	}
	if observer.seenPhysical {
		panic(fmt.Errorf("coroutine emission site %q emitted its physical recipe more than once", instruction.String()))
	}
	observer.seenPhysical = true
}

func (p *context) observeCoroPhysicalControl(instruction ssa.Instruction, actual coroPhysicalControlRecipe) {
	current := p.coroEmissionSite()
	if current == nil || !current.hasExpectedPhysical || current.instruction != instruction {
		panic("coroutine physical control emission has no exact source SitePlan")
	}
	observer := current
	if actual == coroPhysicalControlNone || observer.expectedPhysical.control != actual {
		panic(fmt.Errorf(
			"coroutine emission site %q emitted physical control recipe %s, frozen SitePlan requires %s",
			instruction.String(), actual, observer.expectedPhysical.control,
		))
	}
	if observer.seenPhysicalControl {
		panic(fmt.Errorf("coroutine emission site %q emitted its physical control recipe more than once", instruction.String()))
	}
	observer.seenPhysicalControl = true
}

func (p *context) observeCoroPhysicalOperation(instruction ssa.Instruction, actual coroPhysicalOperationRecipe) {
	current := p.coroEmissionSite()
	if current == nil || !current.hasExpectedPhysical || current.instruction != instruction {
		panic("coroutine physical operation emission has no exact source SitePlan")
	}
	observer := current
	if actual == coroPhysicalOperationNone || observer.expectedPhysical.operation != actual {
		panic(fmt.Errorf(
			"coroutine emission site %q emitted physical operation recipe %s, frozen SitePlan requires %s",
			instruction.String(), actual, observer.expectedPhysical.operation,
		))
	}
	if observer.seenPhysicalOperation {
		panic(fmt.Errorf("coroutine emission site %q emitted its physical operation recipe more than once", instruction.String()))
	}
	observer.seenPhysicalOperation = true
	if actual == coroPhysicalOperationWorkerCgo {
		p.recordCoroCallElision(CoroCallElidedCgoWorker)
	}
}

func (p *context) observeCoroPhysicalOutcome(instruction ssa.Instruction, actual coroPhysicalOutcomeRecipe) {
	current := p.coroEmissionSite()
	if current == nil || !current.hasExpectedPhysical || current.instruction != instruction {
		panic("coroutine physical outcome emission has no exact source SitePlan")
	}
	observer := current
	if actual == coroPhysicalOutcomeNone || observer.expectedPhysical.outcome != actual {
		panic(fmt.Errorf(
			"coroutine emission site %q emitted physical outcome recipe %s, frozen SitePlan requires %s",
			instruction.String(), actual, observer.expectedPhysical.outcome,
		))
	}
	if observer.seenPhysicalOutcome {
		panic(fmt.Errorf("coroutine emission site %q emitted its physical outcome recipe more than once", instruction.String()))
	}
	observer.seenPhysicalOutcome = true
}

func (p *context) observeCoroCallElision(actual CoroCallElisionKind) {
	p.recordCoroCallElision(actual)
}

func (p *context) observeCoroDeferredIntrinsicCapture() {
	observer := p.coroEmissionSite()
	if observer == nil || !observer.observeFrozenSite {
		return
	}
	if _, deferred := observer.instruction.(*ssa.Defer); !deferred ||
		observer.expectedElision != CoroCallElidedIntrinsic ||
		observer.expectedIntrinsic {
		panic("deferred intrinsic capture has no exact relocated SitePlan")
	}
	p.recordCoroCallElision(CoroCallElidedIntrinsic)
}

func (p *context) observeCoroDeferredCgoWorkerCapture() {
	observer := p.coroEmissionSite()
	if observer == nil || !observer.observeFrozenSite {
		return
	}
	if _, deferred := observer.instruction.(*ssa.Defer); !deferred ||
		observer.expectedElision != CoroCallElidedCgoWorker ||
		observer.expectedIntrinsic {
		panic("deferred cgo worker capture has no exact relocated SitePlan")
	}
	p.recordCoroCallElision(CoroCallElidedCgoWorker)
}

// recordCoroCallElision is shared by source-level frontend elisions and
// physical operations whose recipe replaces the source call. Keeping the
// mutation here makes one SitePlan observer authoritative for both views.
func (p *context) recordCoroCallElision(actual CoroCallElisionKind) {
	observer := p.coroEmissionSite()
	if observer == nil || !observer.observeFrozenSite {
		return
	}
	if observer.expectedElision == CoroCallNotElided || observer.expectedElision != actual {
		panic(fmt.Errorf(
			"coroutine emission site %q emitted call elision %d, frozen SitePlan requires %d",
			observer.instruction.String(), actual, observer.expectedElision,
		))
	}
	if observer.seenElision {
		panic(fmt.Errorf("coroutine emission site %q emitted its call elision more than once", observer.instruction.String()))
	}
	observer.seenElision = true
}

func (p *context) plannedCoroCallElision() (CoroCallElisionKind, bool) {
	observer := p.coroEmissionSite()
	if observer == nil || !observer.observeFrozenSite {
		return CoroCallNotElided, false
	}
	return observer.expectedElision, true
}

func (p *context) plannedCoroIntrinsicCall(opcode int) (CoroIntrinsicCallSemantics, bool) {
	observer := p.coroEmissionSite()
	if observer == nil || !observer.observeFrozenSite || !observer.expectedIntrinsic ||
		observer.expectedIntrinsicOpcode != opcode {
		return CoroIntrinsicCallUnsupported, false
	}
	return observer.expectedIntrinsicSemantics, true
}

func (p *context) observeCoroIntrinsicCallEmission(opcode int, actual CoroIntrinsicCallSemantics) {
	observer := p.coroEmissionSite()
	if observer == nil || !observer.observeFrozenSite || !isLLGoIntrinsicInstructionOpcode(opcode) {
		return
	}
	if !observer.expectedIntrinsic {
		panic(fmt.Errorf(
			"coroutine emission site %q emitted an intrinsic recipe absent from its frozen SitePlan",
			observer.instruction.String(),
		))
	}
	if opcode != observer.expectedIntrinsicOpcode {
		panic(fmt.Errorf(
			"coroutine emission site %q emitted intrinsic opcode %d, frozen SitePlan requires %d",
			observer.instruction.String(), opcode, observer.expectedIntrinsicOpcode,
		))
	}
	if observer.expectedElision == CoroCallElidedIntrinsic {
		p.observeCoroCallElision(CoroCallElidedIntrinsic)
	}
	if actual != observer.expectedIntrinsicSemantics {
		panic(fmt.Errorf(
			"coroutine emission site %q emitted intrinsic recipe %d, frozen SitePlan requires %d",
			observer.instruction.String(), actual, observer.expectedIntrinsicSemantics,
		))
	}
	if observer.seenIntrinsic {
		panic(fmt.Errorf("coroutine emission site %q emitted its intrinsic recipe more than once", observer.instruction.String()))
	}
	observer.seenIntrinsic = true
}

func isLLGoIntrinsicInstructionOpcode(opcode int) bool {
	for _, candidate := range llgoInstrs {
		if candidate == opcode {
			return true
		}
	}
	return false
}

func (p *context) observeCoroSiteRuntimeHelper(helper string) {
	observer := p.coroEmissionSite()
	if observer == nil || !observer.observeFrozenSite {
		return
	}
	if _, expected := observer.expected[helper]; !expected {
		function := "<unknown>"
		if observer.instruction.Parent() != nil {
			function = observer.instruction.Parent().String()
		}
		physical := "none"
		if observer.hasExpectedPhysical {
			physical = observer.expectedPhysical.recipe.String()
		}
		expected := make([]string, 0, len(observer.expected))
		for name := range observer.expected {
			expected = append(expected, name)
		}
		sort.Strings(expected)
		panic(fmt.Errorf(
			"coroutine emission site %q (%T) in %q with physical recipe %s emitted runtime helper %q absent from its frozen SitePlan %v",
			observer.instruction.String(), observer.instruction, function, physical, helper, expected,
		))
	}
	observer.seen[helper] = none{}
}

func (o *coroSiteEmissionObserver) missing() []string {
	if o == nil {
		return nil
	}
	missing := make([]string, 0, len(o.expected))
	for helper := range o.expected {
		if _, seen := o.seen[helper]; !seen {
			missing = append(missing, helper)
		}
	}
	sort.Strings(missing)
	return missing
}
