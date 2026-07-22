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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

// CoroOverlaySchemaV1 is the first sparse physical-lowering overlay schema.
// The overlay identifies source ranges and control cuts; it deliberately does
// not copy Go SSA instructions, operands, types, or its value graph.
const CoroOverlaySchemaV1 = "llgo.coro.lowering-overlay.v1"

// CoroSpan identifies a half-open range in one Go SSA basic block. Indices are
// resolved against the immutable source function by the planner and emitter.
// A span contains no instruction copies.
type CoroSpan struct {
	Block int `json:"block"`
	Begin int `json:"begin"`
	End   int `json:"end"`
}

// CoroContinuationID is a stable frontend identity for the one normal
// continuation of a physical region. It is not an LLVM basic-block number.
type CoroContinuationID string

// CoroPhysicalExitID identifies the canonical generated tail which emits a
// source CFG edge. The emitter resolves it to an LLSSA block only while
// emitting a function.
type CoroPhysicalExitID string

// CoroProtocolID selects one closed emitter template. Source-specific event
// kinds (timer, fd, socket, IRQ, worker job) are operation recipes, not new
// protocol opcodes.
type CoroProtocolID string

// CoroOperationRecipeID binds a protocol template to a versioned runtime
// operation contract such as RegisteredEventWait or ForeignWait.
type CoroOperationRecipeID string

// CoroRegionContractID identifies a verified source-region contract, for
// example a no-preempt frame-borrow from prepare through retire.
type CoroRegionContractID string

// CoroStorageID identifies explicit compiler/runtime shared storage. Ordinary
// SSA values which LLVM CoroSplit spills do not receive one.
type CoroStorageID string

// PhysicalRegionKind is language/control intent. Protocol identifies the
// concrete expansion, so adding a timer or file descriptor source does not add
// another value here.
type PhysicalRegionKind string

const (
	PhysicalRegionPoll     PhysicalRegionKind = "poll"
	PhysicalRegionSuspend  PhysicalRegionKind = "suspend"
	PhysicalRegionAwait    PhysicalRegionKind = "await"
	PhysicalRegionSpawn    PhysicalRegionKind = "spawn"
	PhysicalRegionTerminal PhysicalRegionKind = "terminal"
)

// CoroSuspendMode describes whether a region contains a stack cut. A final
// cut is terminal and therefore has no normal continuation.
type CoroSuspendMode string

const (
	CoroSuspendNone        CoroSuspendMode = "none"
	CoroSuspendAlways      CoroSuspendMode = "always"
	CoroSuspendConditional CoroSuspendMode = "conditional"
	CoroSuspendFinal       CoroSuspendMode = "final"
)

// CoroOutcomeKind preserves semantically different resume and termination
// outcomes. In particular task abort and shutdown are not operation cancel.
type CoroOutcomeKind string

const (
	CoroOutcomeFast      CoroOutcomeKind = "fast"
	CoroOutcomeNormal    CoroOutcomeKind = "normal"
	CoroOutcomeSelected  CoroOutcomeKind = "selected"
	CoroOutcomeCanceled  CoroOutcomeKind = "canceled"
	CoroOutcomeTaskAbort CoroOutcomeKind = "task-abort"
	CoroOutcomeShutdown  CoroOutcomeKind = "shutdown"
	CoroOutcomePanic     CoroOutcomeKind = "panic"
	CoroOutcomeGoexit    CoroOutcomeKind = "goexit"
	CoroOutcomeTrap      CoroOutcomeKind = "trap"
)

// CoroOutcomeTargetKind names the small set of control destinations shared by
// all closed protocol templates.
type CoroOutcomeTargetKind string

const (
	CoroTargetContinuation CoroOutcomeTargetKind = "continuation"
	CoroTargetCompletion   CoroOutcomeTargetKind = "completion"
	CoroTargetCleanup      CoroOutcomeTargetKind = "cleanup"
	CoroTargetFinalSuspend CoroOutcomeTargetKind = "final-suspend"
	CoroTargetTrap         CoroOutcomeTargetKind = "trap"
)

// CoroOutcomeEdge binds one protocol outcome to control. Continuation is set
// only for CoroTargetContinuation and must equal the owning region's unique
// continuation.
type CoroOutcomeEdge struct {
	Kind         CoroOutcomeKind       `json:"kind"`
	Target       CoroOutcomeTargetKind `json:"target"`
	Continuation CoroContinuationID    `json:"continuation,omitempty"`
}

// PhysicalRegion is one template-expanded physical control region anchored at
// an exact frozen emission site. Scope is optional source metadata for a
// contract which extends beyond the consumed/synthetic anchor; it still stores
// ranges only, never instructions. A timer prepare/park/retire transaction and
// a registered fd wait can therefore share PhysicalRegionSuspend plus the
// same stack-cut protocol while selecting different Recipe identities.
type PhysicalRegion struct {
	Site           EmissionSiteID        `json:"site"`
	Kind           PhysicalRegionKind    `json:"kind"`
	Protocol       CoroProtocolID        `json:"protocol"`
	Recipe         CoroOperationRecipeID `json:"recipe,omitempty"`
	Contract       CoroRegionContractID  `json:"contract,omitempty"`
	Scope          []CoroSpan            `json:"scope,omitempty"`
	ConsumesSource bool                  `json:"consumes_source,omitempty"`
	Suspend        CoroSuspendMode       `json:"suspend"`
	Continuation   CoroContinuationID    `json:"continuation,omitempty"`
	Outcomes       []CoroOutcomeEdge     `json:"outcomes"`
	Storage        []CoroStorageID       `json:"storage,omitempty"`
}

// EmissionStep is an intentionally narrow tagged union. Exactly one of Span
// and Region is present. Source spans use the mature ordinary instruction
// emitter; regions use the closed coroutine template emitter.
type EmissionStep struct {
	Span   *CoroSpan       `json:"span,omitempty"`
	Region *EmissionSiteID `json:"region,omitempty"`
}

// BlockEmission is the ordered emission ledger for one source SSA block. Exit
// is the sole physical predecessor identity used by every outgoing source
// edge, regardless of how many cuts changed the block's LLVM tail internally.
type BlockEmission struct {
	Block int                `json:"block"`
	Steps []EmissionStep     `json:"steps"`
	Exit  CoroPhysicalExitID `json:"exit"`
}

// EmissionLedger orders source spans and physical regions without introducing
// a second instruction IR.
type EmissionLedger struct {
	Blocks []BlockEmission `json:"blocks"`
}

// ValueResidencyKind describes only exceptional materialization. Absence from
// CoroOverlay.Values means ordinary single-emission LLVM SSA residency.
type ValueResidencyKind string

const (
	// ValueResidencyCoroSplit keeps one LLVM SSA definition and lets LLVM
	// CoroSplit decide its physical spill offset.
	ValueResidencyCoroSplit ValueResidencyKind = "coro-split"
	// ValueResidencyFrameAddress is source allocation storage whose stable
	// address is shared with a runtime operation while the frame is suspended.
	ValueResidencyFrameAddress ValueResidencyKind = "frame-address"
	// ValueResidencyRegionResult is a source SSA value defined by region-local
	// reconciliation at the unique continuation rather than by an ordinary
	// source-span instruction.
	ValueResidencyRegionResult ValueResidencyKind = "region-result"
	// ValueResidencyRematerialize is reserved for a certified effect-free
	// source value. The SSA-aware verifier must reject instruction producers
	// without a matching pure recipe.
	ValueResidencyRematerialize ValueResidencyKind = "rematerialize"
)

// ValueResidency records a source value identity and lifetime facts only. Site
// points back into LoweringFacts; no type, producer instruction, or operands
// are copied. Region is required for frame addresses and region results.
type ValueResidency struct {
	Site    EmissionSiteID     `json:"site"`
	Kind    ValueResidencyKind `json:"kind"`
	Region  *EmissionSiteID    `json:"region,omitempty"`
	Crosses []EmissionSiteID   `json:"crosses,omitempty"`
	Storage CoroStorageID      `json:"storage,omitempty"`
}

// SourceEdgeID identifies one incoming slot in a successor's Go SSA
// predecessor list. PredIndex disambiguates structurally repeated edges.
type SourceEdgeID struct {
	From      int `json:"from"`
	To        int `json:"to"`
	PredIndex int `json:"pred_index"`
}

// PhiEdgeBinding maps a source edge to its canonical physical predecessor.
// The phi value itself remains phi.Edges[PredIndex] in Go SSA and is not
// repeated here.
type PhiEdgeBinding struct {
	Edge SourceEdgeID       `json:"edge"`
	Exit CoroPhysicalExitID `json:"exit"`
}

// CoroOverlay is the per-emission-instance sparse physical control model.
// Plan and LoweringFacts remain separate immutable inputs.
type CoroOverlay struct {
	Schema   string             `json:"schema"`
	Function FunctionID         `json:"function"`
	Instance EmissionInstanceID `json:"instance"`
	Emission EmissionLedger     `json:"emission"`
	Regions  []PhysicalRegion   `json:"regions"`
	Values   []ValueResidency   `json:"values,omitempty"`
	PhiEdges []PhiEdgeBinding   `json:"phi_edges,omitempty"`
}

// Verify checks target-independent structural invariants. The planner's
// SSA-aware verifier additionally checks source bounds, exact CallPlan modes,
// liveness, dominance, pure rematerialization, and contract primitive roles.
func (o CoroOverlay) Verify() error {
	if o.Schema != CoroOverlaySchemaV1 {
		return fmt.Errorf("coro overlay: schema %q, want %q", o.Schema, CoroOverlaySchemaV1)
	}
	if o.Function == "" {
		return fmt.Errorf("coro overlay: empty function identity")
	}
	if o.Instance.Function != o.Function {
		return fmt.Errorf("coro overlay: instance function %q does not match overlay function %q", o.Instance.Function, o.Function)
	}
	if err := verifyEmissionInstanceID(o.Instance); err != nil {
		return fmt.Errorf("coro overlay: instance: %w", err)
	}

	regions := make(map[string]PhysicalRegion, len(o.Regions))
	continuations := make(map[CoroContinuationID]EmissionSiteID)
	storageOwner := make(map[CoroStorageID]EmissionSiteID)
	for i := range o.Regions {
		region := o.Regions[i]
		key, err := verifyOverlaySite(o, region.Site)
		if err != nil {
			return fmt.Errorf("coro overlay: region %d: %w", i, err)
		}
		if _, exists := regions[key]; exists {
			return fmt.Errorf("coro overlay: duplicate physical region site %s", key)
		}
		if err := verifyPhysicalRegion(region); err != nil {
			return fmt.Errorf("coro overlay: region %s: %w", key, err)
		}
		if region.Continuation != "" {
			if prior, exists := continuations[region.Continuation]; exists {
				return fmt.Errorf("coro overlay: continuation %q is shared by regions %s and %s",
					region.Continuation, overlayCanonicalKey(prior), key)
			}
			continuations[region.Continuation] = region.Site
		}
		for _, slot := range region.Storage {
			if prior, exists := storageOwner[slot]; exists {
				return fmt.Errorf("coro overlay: storage %q is owned by regions %s and %s",
					slot, overlayCanonicalKey(prior), key)
			}
			storageOwner[slot] = region.Site
		}
		regions[key] = region
	}

	blocks := make(map[int]BlockEmission, len(o.Emission.Blocks))
	exits := make(map[CoroPhysicalExitID]int, len(o.Emission.Blocks))
	usedRegions := make(map[string]bool, len(regions))
	for i := range o.Emission.Blocks {
		block := o.Emission.Blocks[i]
		if block.Block < 0 {
			return fmt.Errorf("coro overlay: emission block %d has negative source index %d", i, block.Block)
		}
		if _, exists := blocks[block.Block]; exists {
			return fmt.Errorf("coro overlay: duplicate emission block %d", block.Block)
		}
		if block.Exit == "" {
			return fmt.Errorf("coro overlay: emission block %d has empty physical exit", block.Block)
		}
		if prior, exists := exits[block.Exit]; exists {
			return fmt.Errorf("coro overlay: physical exit %q is shared by source blocks %d and %d", block.Exit, prior, block.Block)
		}
		exits[block.Exit] = block.Block
		lastSpanEnd := -1
		for stepIndex := range block.Steps {
			step := block.Steps[stepIndex]
			switch {
			case step.Span != nil && step.Region == nil:
				span := *step.Span
				if err := verifyCoroSpan(span); err != nil {
					return fmt.Errorf("coro overlay: block %d step %d: %w", block.Block, stepIndex, err)
				}
				if span.Block != block.Block {
					return fmt.Errorf("coro overlay: block %d step %d span belongs to block %d", block.Block, stepIndex, span.Block)
				}
				if lastSpanEnd > span.Begin {
					return fmt.Errorf("coro overlay: block %d source spans overlap or run backwards at step %d", block.Block, stepIndex)
				}
				lastSpanEnd = span.End
			case step.Span == nil && step.Region != nil:
				key, err := verifyOverlaySite(o, *step.Region)
				if err != nil {
					return fmt.Errorf("coro overlay: block %d step %d region: %w", block.Block, stepIndex, err)
				}
				region, exists := regions[key]
				if !exists {
					return fmt.Errorf("coro overlay: block %d step %d references unknown region %s", block.Block, stepIndex, key)
				}
				if region.Site.Source.Block != block.Block {
					return fmt.Errorf("coro overlay: block %d step %d references region anchored in block %d",
						block.Block, stepIndex, region.Site.Source.Block)
				}
				if usedRegions[key] {
					return fmt.Errorf("coro overlay: physical region %s is emitted more than once", key)
				}
				usedRegions[key] = true
			default:
				return fmt.Errorf("coro overlay: block %d step %d must contain exactly one span or region", block.Block, stepIndex)
			}
		}
		blocks[block.Block] = block
	}
	if len(blocks) == 0 {
		return fmt.Errorf("coro overlay: empty emission ledger")
	}
	for key := range regions {
		if !usedRegions[key] {
			return fmt.Errorf("coro overlay: physical region %s is never emitted", key)
		}
	}

	values := make(map[string]bool, len(o.Values))
	for i := range o.Values {
		value := o.Values[i]
		key, err := verifyOverlaySite(o, value.Site)
		if err != nil {
			return fmt.Errorf("coro overlay: value residency %d: %w", i, err)
		}
		if values[key] {
			return fmt.Errorf("coro overlay: duplicate value residency site %s", key)
		}
		values[key] = true
		if err := verifyValueResidency(o, value, regions, storageOwner); err != nil {
			return fmt.Errorf("coro overlay: value residency %s: %w", key, err)
		}
	}

	edges := make(map[SourceEdgeID]bool, len(o.PhiEdges))
	for i := range o.PhiEdges {
		binding := o.PhiEdges[i]
		if binding.Edge.From < 0 || binding.Edge.To < 0 || binding.Edge.PredIndex < 0 {
			return fmt.Errorf("coro overlay: phi edge %d has a negative source index", i)
		}
		if edges[binding.Edge] {
			return fmt.Errorf("coro overlay: duplicate phi edge binding %+v", binding.Edge)
		}
		edges[binding.Edge] = true
		from, exists := blocks[binding.Edge.From]
		if !exists {
			return fmt.Errorf("coro overlay: phi edge %d references missing predecessor block %d", i, binding.Edge.From)
		}
		if _, exists := blocks[binding.Edge.To]; !exists {
			return fmt.Errorf("coro overlay: phi edge %d references missing successor block %d", i, binding.Edge.To)
		}
		if binding.Exit == "" || binding.Exit != from.Exit {
			return fmt.Errorf("coro overlay: phi edge %d exit %q does not match predecessor block %d exit %q",
				i, binding.Exit, binding.Edge.From, from.Exit)
		}
	}
	return nil
}

func verifyPhysicalRegion(region PhysicalRegion) error {
	switch region.Kind {
	case PhysicalRegionPoll, PhysicalRegionSuspend, PhysicalRegionAwait, PhysicalRegionSpawn, PhysicalRegionTerminal:
	default:
		return fmt.Errorf("unknown kind %q", region.Kind)
	}
	if region.Protocol == "" {
		return fmt.Errorf("empty protocol")
	}
	switch region.Suspend {
	case CoroSuspendNone, CoroSuspendAlways, CoroSuspendConditional, CoroSuspendFinal:
	default:
		return fmt.Errorf("unknown suspend mode %q", region.Suspend)
	}
	switch region.Kind {
	case PhysicalRegionPoll:
		if region.Suspend != CoroSuspendConditional || region.ConsumesSource {
			return fmt.Errorf("poll must be a synthetic conditional suspend")
		}
	case PhysicalRegionAwait:
		if region.Suspend != CoroSuspendAlways || !region.ConsumesSource {
			return fmt.Errorf("await must consume one source site and always suspend")
		}
	case PhysicalRegionSpawn:
		if region.Suspend != CoroSuspendNone || !region.ConsumesSource {
			return fmt.Errorf("spawn must consume one source site without embedding a suspend")
		}
	case PhysicalRegionTerminal:
		if region.Suspend != CoroSuspendFinal || !region.ConsumesSource {
			return fmt.Errorf("terminal region must consume one source site and final-suspend")
		}
	case PhysicalRegionSuspend:
		if region.Suspend != CoroSuspendAlways && region.Suspend != CoroSuspendConditional {
			return fmt.Errorf("suspend region must always or conditionally suspend")
		}
	}
	terminal := region.Kind == PhysicalRegionTerminal
	if terminal && region.Continuation != "" {
		return fmt.Errorf("terminal region has continuation %q", region.Continuation)
	}
	if !terminal && region.Continuation == "" {
		return fmt.Errorf("nonterminal region has no continuation")
	}
	if region.Contract == "" && len(region.Scope) != 0 {
		return fmt.Errorf("source scope has no region contract")
	}
	if region.Contract != "" && len(region.Scope) == 0 {
		return fmt.Errorf("region contract %q has no source scope", region.Contract)
	}
	for i, span := range region.Scope {
		if err := verifyCoroSpan(span); err != nil {
			return fmt.Errorf("scope %d: %w", i, err)
		}
		if i > 0 {
			prior := region.Scope[i-1]
			if prior.Block > span.Block || prior.Block == span.Block && prior.End > span.Begin {
				return fmt.Errorf("scope spans overlap or run backwards at index %d", i)
			}
		}
	}
	seenStorage := make(map[CoroStorageID]bool, len(region.Storage))
	for _, storage := range region.Storage {
		if storage == "" {
			return fmt.Errorf("empty storage identity")
		}
		if seenStorage[storage] {
			return fmt.Errorf("duplicate storage identity %q", storage)
		}
		seenStorage[storage] = true
	}
	if len(region.Outcomes) == 0 {
		return fmt.Errorf("region has no outcomes")
	}
	seenOutcomes := make(map[CoroOutcomeKind]bool, len(region.Outcomes))
	hasContinuation := false
	for i, outcome := range region.Outcomes {
		if !validCoroOutcome(outcome.Kind) {
			return fmt.Errorf("outcome %d has unknown kind %q", i, outcome.Kind)
		}
		if seenOutcomes[outcome.Kind] {
			return fmt.Errorf("duplicate outcome kind %q", outcome.Kind)
		}
		seenOutcomes[outcome.Kind] = true
		switch outcome.Target {
		case CoroTargetContinuation:
			if terminal || outcome.Continuation == "" || outcome.Continuation != region.Continuation {
				return fmt.Errorf("outcome %q has invalid continuation %q", outcome.Kind, outcome.Continuation)
			}
			hasContinuation = true
		case CoroTargetCompletion, CoroTargetCleanup, CoroTargetFinalSuspend, CoroTargetTrap:
			if outcome.Continuation != "" {
				return fmt.Errorf("outcome %q target %q carries continuation %q", outcome.Kind, outcome.Target, outcome.Continuation)
			}
		default:
			return fmt.Errorf("outcome %q has unknown target %q", outcome.Kind, outcome.Target)
		}
	}
	if terminal && hasContinuation {
		return fmt.Errorf("terminal region exposes a normal continuation")
	}
	if !terminal && !hasContinuation {
		return fmt.Errorf("nonterminal region has no outcome reaching its continuation")
	}
	return nil
}

func verifyValueResidency(
	o CoroOverlay,
	value ValueResidency,
	regions map[string]PhysicalRegion,
	storageOwner map[CoroStorageID]EmissionSiteID,
) error {
	regionKey := ""
	if value.Region != nil {
		var err error
		regionKey, err = verifyOverlaySite(o, *value.Region)
		if err != nil {
			return fmt.Errorf("region: %w", err)
		}
		if _, exists := regions[regionKey]; !exists {
			return fmt.Errorf("references unknown region %s", regionKey)
		}
	}
	seenCuts := make(map[string]bool, len(value.Crosses))
	for i, cut := range value.Crosses {
		key, err := verifyOverlaySite(o, cut)
		if err != nil {
			return fmt.Errorf("crossing %d: %w", i, err)
		}
		region, exists := regions[key]
		if !exists {
			return fmt.Errorf("crossing %d references unknown region %s", i, key)
		}
		if region.Suspend == CoroSuspendNone || region.Suspend == CoroSuspendFinal {
			return fmt.Errorf("crossing %d references non-resumable region %s", i, key)
		}
		if seenCuts[key] {
			return fmt.Errorf("duplicate crossing region %s", key)
		}
		seenCuts[key] = true
	}
	if value.Storage != "" {
		owner, exists := storageOwner[value.Storage]
		if !exists {
			return fmt.Errorf("references unowned storage %q", value.Storage)
		}
		if value.Region == nil || overlayCanonicalKey(owner) != regionKey {
			return fmt.Errorf("storage %q is not owned by the value's region", value.Storage)
		}
	}
	switch value.Kind {
	case ValueResidencyCoroSplit:
		if value.Region != nil || value.Storage != "" || len(value.Crosses) == 0 {
			return fmt.Errorf("coro-split residency requires crossings and no explicit region/storage")
		}
	case ValueResidencyFrameAddress:
		if value.Region == nil || len(value.Crosses) == 0 {
			return fmt.Errorf("frame-address residency requires an owning region and crossing")
		}
		if !seenCuts[regionKey] {
			return fmt.Errorf("frame-address owning region is absent from crossings")
		}
		if regions[regionKey].Contract == "" {
			return fmt.Errorf("frame-address owning region has no lifetime contract")
		}
	case ValueResidencyRegionResult:
		if value.Region == nil || len(value.Crosses) != 0 {
			return fmt.Errorf("region-result residency requires one producer region and no pre-definition crossings")
		}
	case ValueResidencyRematerialize:
		if value.Region != nil || value.Storage != "" || len(value.Crosses) != 0 {
			return fmt.Errorf("rematerialize residency cannot carry region, storage, or crossings")
		}
	default:
		return fmt.Errorf("unknown kind %q", value.Kind)
	}
	return nil
}

func verifyCoroSpan(span CoroSpan) error {
	if span.Block < 0 || span.Begin < 0 || span.End <= span.Begin {
		return fmt.Errorf("invalid half-open source span %+v", span)
	}
	return nil
}

func validCoroOutcome(outcome CoroOutcomeKind) bool {
	switch outcome {
	case CoroOutcomeFast, CoroOutcomeNormal, CoroOutcomeSelected, CoroOutcomeCanceled,
		CoroOutcomeTaskAbort, CoroOutcomeShutdown, CoroOutcomePanic, CoroOutcomeGoexit, CoroOutcomeTrap:
		return true
	}
	return false
}

func verifyOverlaySite(o CoroOverlay, site EmissionSiteID) (string, error) {
	if err := verifyEmissionSiteID(site); err != nil {
		return "", err
	}
	if site.Instance != o.Instance {
		return "", fmt.Errorf("site instance does not match overlay instance")
	}
	if site.Source.Function != o.Function {
		return "", fmt.Errorf("site source function %q does not match overlay function %q", site.Source.Function, o.Function)
	}
	return overlayCanonicalKey(site), nil
}

// CanonicalJSON validates and serializes the overlay with deterministic
// definition-table ordering. Emission step order is semantic and is therefore
// preserved.
func (o CoroOverlay) CanonicalJSON() ([]byte, error) {
	if err := o.Verify(); err != nil {
		return nil, err
	}
	canonical := cloneCoroOverlay(o)
	sort.Slice(canonical.Emission.Blocks, func(i, j int) bool {
		return canonical.Emission.Blocks[i].Block < canonical.Emission.Blocks[j].Block
	})
	sort.Slice(canonical.Regions, func(i, j int) bool {
		return overlayCanonicalKey(canonical.Regions[i].Site) < overlayCanonicalKey(canonical.Regions[j].Site)
	})
	for i := range canonical.Regions {
		region := &canonical.Regions[i]
		sort.Slice(region.Scope, func(i, j int) bool {
			if region.Scope[i].Block != region.Scope[j].Block {
				return region.Scope[i].Block < region.Scope[j].Block
			}
			if region.Scope[i].Begin != region.Scope[j].Begin {
				return region.Scope[i].Begin < region.Scope[j].Begin
			}
			return region.Scope[i].End < region.Scope[j].End
		})
		sort.Slice(region.Outcomes, func(i, j int) bool {
			left, right := region.Outcomes[i], region.Outcomes[j]
			if left.Kind != right.Kind {
				return left.Kind < right.Kind
			}
			if left.Target != right.Target {
				return left.Target < right.Target
			}
			return left.Continuation < right.Continuation
		})
		sort.Slice(region.Storage, func(i, j int) bool { return region.Storage[i] < region.Storage[j] })
	}
	sort.Slice(canonical.Values, func(i, j int) bool {
		return overlayCanonicalKey(canonical.Values[i].Site) < overlayCanonicalKey(canonical.Values[j].Site)
	})
	for i := range canonical.Values {
		sort.Slice(canonical.Values[i].Crosses, func(left, right int) bool {
			return overlayCanonicalKey(canonical.Values[i].Crosses[left]) < overlayCanonicalKey(canonical.Values[i].Crosses[right])
		})
	}
	sort.Slice(canonical.PhiEdges, func(i, j int) bool {
		left, right := canonical.PhiEdges[i], canonical.PhiEdges[j]
		if left.Edge.From != right.Edge.From {
			return left.Edge.From < right.Edge.From
		}
		if left.Edge.To != right.Edge.To {
			return left.Edge.To < right.Edge.To
		}
		if left.Edge.PredIndex != right.Edge.PredIndex {
			return left.Edge.PredIndex < right.Edge.PredIndex
		}
		return left.Exit < right.Exit
	})
	data, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("coro overlay: marshal canonical: %w", err)
	}
	return append(data, '\n'), nil
}

// Digest returns the SHA-256 digest of CanonicalJSON.
func (o CoroOverlay) Digest() ([sha256.Size]byte, error) {
	data, err := o.CanonicalJSON()
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(data), nil
}

func cloneCoroOverlay(source CoroOverlay) CoroOverlay {
	clone := source
	clone.Emission.Blocks = append([]BlockEmission(nil), source.Emission.Blocks...)
	for i := range clone.Emission.Blocks {
		clone.Emission.Blocks[i].Steps = append([]EmissionStep(nil), source.Emission.Blocks[i].Steps...)
		for j := range clone.Emission.Blocks[i].Steps {
			step := &clone.Emission.Blocks[i].Steps[j]
			if step.Span != nil {
				value := *step.Span
				step.Span = &value
			}
			if step.Region != nil {
				value := *step.Region
				step.Region = &value
			}
		}
	}
	clone.Regions = append([]PhysicalRegion(nil), source.Regions...)
	for i := range clone.Regions {
		clone.Regions[i].Scope = append([]CoroSpan(nil), source.Regions[i].Scope...)
		clone.Regions[i].Outcomes = append([]CoroOutcomeEdge(nil), source.Regions[i].Outcomes...)
		clone.Regions[i].Storage = append([]CoroStorageID(nil), source.Regions[i].Storage...)
	}
	clone.Values = append([]ValueResidency(nil), source.Values...)
	for i := range clone.Values {
		if source.Values[i].Region != nil {
			value := *source.Values[i].Region
			clone.Values[i].Region = &value
		}
		clone.Values[i].Crosses = append([]EmissionSiteID(nil), source.Values[i].Crosses...)
	}
	clone.PhiEdges = append([]PhiEdgeBinding(nil), source.PhiEdges...)
	return clone
}

func overlayCanonicalKey(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("coro overlay: canonical key for %T: %v", value, err))
	}
	return string(data)
}
