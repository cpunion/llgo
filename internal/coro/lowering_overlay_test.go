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

package coro

import (
	"bytes"
	"strings"
	"testing"
)

func TestCoroOverlayCanonicalRegisteredEventWait(t *testing.T) {
	overlay := testCoroOverlay()
	if err := overlay.Verify(); err != nil {
		t.Fatal(err)
	}
	first, err := overlay.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, err := overlay.Digest()
	if err != nil {
		t.Fatal(err)
	}

	// Definition-table order is not semantic. The canonical encoding sorts it,
	// while preserving each source block's emission steps.
	reordered := cloneCoroOverlay(overlay)
	reversePhysicalRegions(reordered.Regions)
	reverseValueResidencies(reordered.Values)
	reverseBlockEmissions(reordered.Emission.Blocks)
	second, err := reordered.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := reordered.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || firstDigest != secondDigest {
		t.Fatalf("canonical overlay changed after definition reordering:\n%s\n%s", first, second)
	}

	text := string(first)
	for _, want := range []string{
		`"kind":"suspend"`,
		`"protocol":"llgo.coro.inline-park.v1"`,
		`"recipe":"registered-event-wait.v1"`,
		`"kind":"frame-address"`,
		`"phi_edges"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("canonical overlay is missing %s:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"timer-opcode", "fd-opcode", "network-opcode"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("canonical overlay contains source-specific opcode %q:\n%s", forbidden, text)
		}
	}
}

func TestCoroOverlayEmissionOrderIsSemantic(t *testing.T) {
	overlay := testCoroOverlay()
	baseline, err := overlay.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	changed := cloneCoroOverlay(overlay)
	steps := changed.Emission.Blocks[0].Steps
	steps[0], steps[1] = steps[1], steps[0]
	other, err := changed.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(baseline, other) {
		t.Fatal("canonical encoding erased semantic emission-step order")
	}
}

func TestCoroOverlayRejectsImplicitPhysicalControl(t *testing.T) {
	tests := []struct {
		name string
		edit func(*CoroOverlay)
		want string
	}{
		{
			name: "duplicate continuation",
			edit: func(overlay *CoroOverlay) {
				overlay.Regions[1].Continuation = overlay.Regions[0].Continuation
				for index := range overlay.Regions[1].Outcomes {
					if overlay.Regions[1].Outcomes[index].Target == CoroTargetContinuation {
						overlay.Regions[1].Outcomes[index].Continuation = overlay.Regions[0].Continuation
					}
				}
			},
			want: "continuation",
		},
		{
			name: "region not emitted",
			edit: func(overlay *CoroOverlay) {
				overlay.Emission.Blocks[0].Steps = overlay.Emission.Blocks[0].Steps[:2]
			},
			want: "never emitted",
		},
		{
			name: "phi predecessor tail mismatch",
			edit: func(overlay *CoroOverlay) {
				overlay.PhiEdges[0].Exit = "replayed-logical-block-head"
			},
			want: "does not match predecessor",
		},
		{
			name: "frame address without lifetime contract",
			edit: func(overlay *CoroOverlay) {
				overlay.Regions[1].Contract = ""
				overlay.Regions[1].Scope = nil
			},
			want: "no lifetime contract",
		},
		{
			name: "region result crosses its producer cut",
			edit: func(overlay *CoroOverlay) {
				overlay.Values[2].Crosses = []EmissionSiteID{overlay.Regions[1].Site}
			},
			want: "no pre-definition crossings",
		},
		{
			name: "conditional fast path has no continuation",
			edit: func(overlay *CoroOverlay) {
				for index := range overlay.Regions[0].Outcomes {
					overlay.Regions[0].Outcomes[index].Target = CoroTargetCompletion
					overlay.Regions[0].Outcomes[index].Continuation = ""
				}
			},
			want: "no outcome reaching",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			overlay := testCoroOverlay()
			test.edit(&overlay)
			err := overlay.Verify()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Verify() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCoroOverlayRejectsInvalidResidencyAndRegionShape(t *testing.T) {
	tests := []struct {
		name string
		edit func(*CoroOverlay)
		want string
	}{
		{
			name: "coro split without cut",
			edit: func(overlay *CoroOverlay) { overlay.Values[0].Crosses = nil },
			want: "requires crossings",
		},
		{
			name: "poll consumes source",
			edit: func(overlay *CoroOverlay) { overlay.Regions[0].ConsumesSource = true },
			want: "synthetic conditional suspend",
		},
		{
			name: "event region has no recipe-independent protocol",
			edit: func(overlay *CoroOverlay) { overlay.Regions[1].Protocol = "" },
			want: "empty protocol",
		},
		{
			name: "shared explicit storage",
			edit: func(overlay *CoroOverlay) {
				overlay.Regions[0].Storage = []CoroStorageID{"wait-token"}
			},
			want: "owned by regions",
		},
		{
			name: "source spans overlap",
			edit: func(overlay *CoroOverlay) {
				overlay.Emission.Blocks[0].Steps = append(overlay.Emission.Blocks[0].Steps,
					EmissionStep{Span: coroSpanPtr(CoroSpan{Block: 0, Begin: 2, End: 5})})
			},
			want: "overlap or run backwards",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			overlay := testCoroOverlay()
			test.edit(&overlay)
			err := overlay.Verify()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Verify() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func testCoroOverlay() CoroOverlay {
	function := FunctionID("example.com/overlay.Sleep")
	instance := EmissionInstanceID{Function: function, Owner: "example.com/overlay", Context: "native-amd64"}
	pollSite := testOverlayBlockSite(instance, 0, RolePoll, 0)
	parkSite := testOverlayInstructionSite(instance, 0, 3, RolePark, 0)
	valueAcross := testOverlayInstructionSite(instance, 0, 1, RolePrimary, 0)
	frameAddress := testOverlayInstructionSite(instance, 0, 2, RolePrimary, 0)
	regionResult := testOverlayInstructionSite(instance, 0, 3, RoleOutcome, 0)
	pollContinuation := CoroContinuationID("b0.poll.cont")
	parkContinuation := CoroContinuationID("b0.park.cont")
	poll := PhysicalRegion{
		Site:         pollSite,
		Kind:         PhysicalRegionPoll,
		Protocol:     "llgo.coro.preempt-poll.v1",
		Suspend:      CoroSuspendConditional,
		Continuation: pollContinuation,
		Outcomes: []CoroOutcomeEdge{
			{Kind: CoroOutcomeFast, Target: CoroTargetContinuation, Continuation: pollContinuation},
			{Kind: CoroOutcomeNormal, Target: CoroTargetContinuation, Continuation: pollContinuation},
			{Kind: CoroOutcomeTaskAbort, Target: CoroTargetCompletion},
			{Kind: CoroOutcomeShutdown, Target: CoroTargetCompletion},
		},
	}
	park := PhysicalRegion{
		Site:           parkSite,
		Kind:           PhysicalRegionSuspend,
		Protocol:       "llgo.coro.inline-park.v1",
		Recipe:         "registered-event-wait.v1",
		Contract:       "frame-borrow.prepare-park-retire.v1",
		Scope:          []CoroSpan{{Block: 0, Begin: 1, End: 5}},
		ConsumesSource: true,
		Suspend:        CoroSuspendAlways,
		Continuation:   parkContinuation,
		Outcomes: []CoroOutcomeEdge{
			{Kind: CoroOutcomeNormal, Target: CoroTargetContinuation, Continuation: parkContinuation},
			{Kind: CoroOutcomeCanceled, Target: CoroTargetCleanup},
			{Kind: CoroOutcomeTaskAbort, Target: CoroTargetCompletion},
			{Kind: CoroOutcomeShutdown, Target: CoroTargetCompletion},
			{Kind: CoroOutcomeTrap, Target: CoroTargetTrap},
		},
		Storage: []CoroStorageID{"wait-token"},
	}
	return CoroOverlay{
		Schema:   CoroOverlaySchemaV1,
		Function: function,
		Instance: instance,
		Emission: EmissionLedger{Blocks: []BlockEmission{
			{
				Block: 0,
				Steps: []EmissionStep{
					{Region: emissionSitePtr(pollSite)},
					{Span: coroSpanPtr(CoroSpan{Block: 0, Begin: 0, End: 3})},
					{Region: emissionSitePtr(parkSite)},
					{Span: coroSpanPtr(CoroSpan{Block: 0, Begin: 4, End: 6})},
				},
				Exit: "b0.source-exit",
			},
			{Block: 1, Steps: []EmissionStep{{Span: coroSpanPtr(CoroSpan{Block: 1, Begin: 0, End: 2})}}, Exit: "b1.source-exit"},
		}},
		Regions: []PhysicalRegion{poll, park},
		Values: []ValueResidency{
			{Site: valueAcross, Kind: ValueResidencyCoroSplit, Crosses: []EmissionSiteID{pollSite, parkSite}},
			{Site: frameAddress, Kind: ValueResidencyFrameAddress, Region: emissionSitePtr(parkSite), Crosses: []EmissionSiteID{parkSite}},
			{Site: regionResult, Kind: ValueResidencyRegionResult, Region: emissionSitePtr(parkSite), Storage: "wait-token"},
		},
		PhiEdges: []PhiEdgeBinding{{
			Edge: SourceEdgeID{From: 0, To: 1, PredIndex: 0},
			Exit: "b0.source-exit",
		}},
	}
}

func testOverlayInstructionSite(instance EmissionInstanceID, block, instruction int, role SiteRole, ordinal int) EmissionSiteID {
	return EmissionSiteID{
		Instance: instance,
		Source: SourceSiteID{
			Function:    instance.Function,
			Kind:        SourceInstruction,
			Block:       block,
			Instruction: instruction,
			Successor:   -1,
			Role:        role,
			Ordinal:     ordinal,
		},
	}
}

func testOverlayBlockSite(instance EmissionInstanceID, block int, role SiteRole, ordinal int) EmissionSiteID {
	return EmissionSiteID{
		Instance: instance,
		Source: SourceSiteID{
			Function:    instance.Function,
			Kind:        SourceBlockEntry,
			Block:       block,
			Instruction: -1,
			Successor:   -1,
			Role:        role,
			Ordinal:     ordinal,
		},
	}
}

func coroSpanPtr(value CoroSpan) *CoroSpan { return &value }

func emissionSitePtr(value EmissionSiteID) *EmissionSiteID { return &value }

func reversePhysicalRegions(values []PhysicalRegion) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseValueResidencies(values []ValueResidency) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseBlockEmissions(values []BlockEmission) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
