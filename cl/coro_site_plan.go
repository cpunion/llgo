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
	instruction ssa.Instruction
	expected    map[string]none
	seen        map[string]none
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
	}
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
