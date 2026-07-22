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
	seenPhysical               bool
	seenPhysicalNilGuard       bool
	seenPhysicalBoundsGuard    bool
	observeFrozenSite          bool
}

func (p *context) beginCoroSiteEmission(instruction ssa.Instruction) func() {
	return p.beginCoroSiteEmissionMode(instruction, coroRuntimeHelperAtSource)
}

func (p *context) beginCoroRelocatedSiteEmission(instruction ssa.Instruction, placement coroRuntimeHelperPlacement) func() {
	if placement == coroRuntimeHelperAtSource {
		panic("relocated coroutine SitePlan emission requires a non-source placement")
	}
	return p.beginCoroSiteEmissionMode(instruction, placement)
}

func (p *context) beginCoroSiteEmissionMode(instruction ssa.Instruction, placement coroRuntimeHelperPlacement) func() {
	if p == nil || instruction == nil || p.compilation == nil || p.emissionUniverse == nil ||
		!p.coroPhysicalEmission || p.rawPlainBody {
		return func() {}
	}
	plan := coroEmissionSitePlan{}
	helpers := []string(nil)
	if p.emissionUniverse.CompleteRuntimeABI() {
		var err error
		plan, err = p.emissionUniverse.coroProgramIR.sitePlan(p, instruction)
		if err != nil {
			panic(fmt.Errorf("coroutine emission site %q: %w", instruction.String(), err))
		}
		helpers = plan.managedRuntimeHelpersAt(placement)
	}
	filtered := helpers[:0]
	for _, helper := range helpers {
		if coroCompilerElidesImplicitFaultRuntimeHelper(instruction, helper) ||
			(p.coroExplicitStatus &&
				coroLoweredCallExplicitStatusElided(instruction, helper)) {
			continue
		}
		filtered = append(filtered, helper)
	}
	helpers = filtered
	observer := &coroSiteEmissionObserver{
		instruction:       instruction,
		expected:          make(map[string]none, len(helpers)),
		seen:              make(map[string]none, len(helpers)),
		observeFrozenSite: p.emissionUniverse.CompleteRuntimeABI(),
	}
	if placement == coroRuntimeHelperAtSource && p.coroPhysicalPlan != nil {
		physical, err := p.coroPhysicalPlan.instructionPlan(instruction)
		if err != nil {
			panic(fmt.Errorf("coroutine emission site %q: %w", instruction.String(), err))
		}
		observer.expectedPhysical = physical
		observer.hasExpectedPhysical = true
		observer.seenPhysical = physical.recipe == coroPhysicalInstructionOrdinary
	}
	if plan.hasCallPlan {
		if plan.callPlan.failure != "" {
			panic(fmt.Errorf("coroutine emission site %q has an invalid frozen call SitePlan: %s", instruction.String(), plan.callPlan.failure))
		}
		callPlan := plan.callPlan.plan
		observer.expectedIntrinsic = callPlan.Intrinsic && callPlan.Elision == CoroCallElidedIntrinsic
		observer.expectedIntrinsicOpcode = plan.callPlan.opcode
		observer.expectedIntrinsicSemantics = callPlan.IntrinsicSemantics
		observer.expectedElision = callPlan.Elision
	}
	for _, helper := range helpers {
		observer.expected[helper] = none{}
	}
	previous := p.currentCoroSite
	p.currentCoroSite = observer
	return func() {
		recovered := recover()
		p.currentCoroSite = previous
		if recovered != nil {
			panic(recovered)
		}
		if missing := observer.missing(); len(missing) != 0 {
			panic(fmt.Errorf(
				"coroutine emission site %q omitted frozen runtime helper(s) %s",
				instruction.String(), strings.Join(missing, ", "),
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
		if observer.hasExpectedPhysical && !observer.seenPhysical {
			panic(fmt.Errorf(
				"coroutine emission site %q omitted frozen physical recipe %s",
				instruction.String(), observer.expectedPhysical.recipe,
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

func (p *context) observeCoroPhysicalNilGuard(instruction ssa.Instruction) {
	p.observeCoroPhysicalGuard(instruction, true)
}

func (p *context) observeCoroPhysicalBoundsGuard(instruction ssa.Instruction) {
	p.observeCoroPhysicalGuard(instruction, false)
}

func (p *context) observeCoroPhysicalGuard(instruction ssa.Instruction, nilGuard bool) {
	if p == nil || p.currentCoroSite == nil || !p.currentCoroSite.hasExpectedPhysical ||
		p.currentCoroSite.instruction != instruction {
		panic("coroutine physical guard emission has no exact source SitePlan")
	}
	observer := p.currentCoroSite
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
	if p == nil || p.coroPhysicalPlan == nil {
		return coroPhysicalInstructionPlan{}, false
	}
	if p.currentCoroSite == nil || !p.currentCoroSite.hasExpectedPhysical ||
		p.currentCoroSite.instruction != instruction {
		panic("coroutine physical recipe selection has no exact source SitePlan")
	}
	return p.currentCoroSite.expectedPhysical, true
}

func (p *context) observeCoroPhysicalInstruction(instruction ssa.Instruction, actual coroPhysicalInstructionRecipe) {
	if p == nil || p.currentCoroSite == nil || !p.currentCoroSite.hasExpectedPhysical ||
		p.currentCoroSite.instruction != instruction {
		panic("coroutine physical recipe emission has no exact source SitePlan")
	}
	observer := p.currentCoroSite
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

func (p *context) observeCoroCallElision(actual CoroCallElisionKind) {
	if p == nil || p.currentCoroSite == nil || !p.currentCoroSite.observeFrozenSite {
		return
	}
	observer := p.currentCoroSite
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
	if p == nil || p.currentCoroSite == nil || !p.currentCoroSite.observeFrozenSite {
		return CoroCallNotElided, false
	}
	return p.currentCoroSite.expectedElision, true
}

func (p *context) plannedCoroIntrinsicCall(opcode int) (CoroIntrinsicCallSemantics, bool) {
	if p == nil || p.currentCoroSite == nil || !p.currentCoroSite.observeFrozenSite || !p.currentCoroSite.expectedIntrinsic ||
		p.currentCoroSite.expectedIntrinsicOpcode != opcode {
		return CoroIntrinsicCallUnsupported, false
	}
	return p.currentCoroSite.expectedIntrinsicSemantics, true
}

func (p *context) observeCoroIntrinsicCallEmission(opcode int, actual CoroIntrinsicCallSemantics) {
	if p == nil || p.currentCoroSite == nil || !p.currentCoroSite.observeFrozenSite || !isLLGoIntrinsicInstructionOpcode(opcode) {
		return
	}
	observer := p.currentCoroSite
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
	p.observeCoroCallElision(CoroCallElidedIntrinsic)
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
	if p == nil || p.currentCoroSite == nil || !p.currentCoroSite.observeFrozenSite {
		return
	}
	observer := p.currentCoroSite
	if _, expected := observer.expected[helper]; !expected {
		panic(fmt.Errorf(
			"coroutine emission site %q emitted runtime helper %q absent from its frozen SitePlan",
			observer.instruction.String(), helper,
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
