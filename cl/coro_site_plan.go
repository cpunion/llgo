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
		!p.emissionUniverse.CompleteRuntimeABI() || !p.coroPhysicalEmission || p.rawPlainBody {
		return func() {}
	}
	plan, err := p.emissionUniverse.coroProgramIR.sitePlan(p, instruction)
	if err != nil {
		panic(fmt.Errorf("coroutine emission site %q: %w", instruction.String(), err))
	}
	helpers := plan.managedRuntimeHelpersAt(placement)
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
		instruction: instruction,
		expected:    make(map[string]none, len(helpers)),
		seen:        make(map[string]none, len(helpers)),
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
	}
}

func (p *context) observeCoroCallElision(actual CoroCallElisionKind) {
	if p == nil || p.currentCoroSite == nil {
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
	if p == nil || p.currentCoroSite == nil {
		return CoroCallNotElided, false
	}
	return p.currentCoroSite.expectedElision, true
}

func (p *context) plannedCoroIntrinsicCall(opcode int) (CoroIntrinsicCallSemantics, bool) {
	if p == nil || p.currentCoroSite == nil || !p.currentCoroSite.expectedIntrinsic ||
		p.currentCoroSite.expectedIntrinsicOpcode != opcode {
		return CoroIntrinsicCallUnsupported, false
	}
	return p.currentCoroSite.expectedIntrinsicSemantics, true
}

func (p *context) observeCoroIntrinsicCallEmission(opcode int, actual CoroIntrinsicCallSemantics) {
	if p == nil || p.currentCoroSite == nil || !isLLGoIntrinsicInstructionOpcode(opcode) {
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
	if p == nil || p.currentCoroSite == nil {
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
