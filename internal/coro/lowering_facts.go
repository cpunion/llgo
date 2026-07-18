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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/ssa"
)

// LoweringFactsSchema identifies the pointer-free sparse lowering-fact wire
// format. It is intentionally independent from PlanDigestSchema: integrating
// this digest into the build cache is a separate, fail-closed migration step.
const LoweringFactsSchema = "llgo.coro.lowering-facts.v0"

// LoweringFactsDigestDomain separates lowering-fact hashes from source,
// function identity, plan, and future overlay hashes that happen to contain
// the same bytes.
const LoweringFactsDigestDomain = "llgo.coro.lowering-facts.digest.v0"

// EmissionInstanceID identifies one physical owner/patch/ABI context for a
// logical function. Owner and Context must be canonical, checkout-independent
// keys supplied by the frontend; pointer addresses and diagnostic SSA strings
// are not valid inputs.
//
// The value is deliberately structural and comparable. In particular it can
// be used as a map key during verification and contains no Go or LLVM pointer.
type EmissionInstanceID struct {
	Function FunctionID `json:"function"`
	Owner    string     `json:"owner"`
	Context  string     `json:"context"`
}

// NewEmissionInstanceID validates and returns a frozen physical instance ID.
// It does not attempt to canonicalize owner or context: doing that here would
// hide missing patch, package-variant, target, or effective-type information.
func NewEmissionInstanceID(function FunctionID, owner, context string) (EmissionInstanceID, error) {
	id := EmissionInstanceID{Function: function, Owner: owner, Context: context}
	if err := verifyEmissionInstanceID(id); err != nil {
		return EmissionInstanceID{}, err
	}
	return id, nil
}

// Validate checks that id is a frozen, pointer-free instance identity.
func (id EmissionInstanceID) Validate() error { return verifyEmissionInstanceID(id) }

func verifyEmissionInstanceID(id EmissionInstanceID) error {
	if err := id.Function.validate(); err != nil {
		return fmt.Errorf("coro: emission instance: %w", err)
	}
	if err := validateStableIdentityText("emission owner", id.Owner); err != nil {
		return err
	}
	if err := validateStableIdentityText("emission context", id.Context); err != nil {
		return err
	}
	return nil
}

// SourceSiteKind selects the structural source anchor used by a lowering
// fact. Generated LLVM block or instruction numbers are never source anchors.
type SourceSiteKind string

const (
	SourceInstruction SourceSiteKind = "instruction"
	SourceBlockEntry  SourceSiteKind = "block-entry"
	SourceEdge        SourceSiteKind = "edge"
	SourceFunction    SourceSiteKind = "function"
)

func (kind SourceSiteKind) validate() error {
	switch kind {
	case SourceInstruction, SourceBlockEntry, SourceEdge, SourceFunction:
		return nil
	default:
		return fmt.Errorf("coro: invalid source site kind %q", kind)
	}
}

// SiteRole is a typed subsite or outcome role. Stable roles are preferable to
// generated block numbers; Ordinal disambiguates repeated occurrences of the
// same role at one source anchor.
type SiteRole string

const (
	RolePrimary       SiteRole = "primary"
	RoleNilCheck      SiteRole = "nil-check"
	RoleBoundsCheck   SiteRole = "bounds-check"
	RoleFastTry       SiteRole = "fast-try"
	RoleCall          SiteRole = "call"
	RoleHelper        SiteRole = "helper"
	RolePark          SiteRole = "park"
	RoleResume        SiteRole = "resume"
	RolePoll          SiteRole = "poll"
	RolePanic         SiteRole = "panic"
	RoleOutcome       SiteRole = "outcome"
	RoleFunctionValue SiteRole = "function-value"
	RoleRegionBegin   SiteRole = "region-begin"
	RoleRegionEnd     SiteRole = "region-end"
)

func (role SiteRole) validate() error {
	return validateStableToken("site role", string(role))
}

// SourceSiteID is the owner-independent source anchor of one lowering fact.
// Unused integer coordinates are always -1; constructors below enforce the
// canonical representation.
type SourceSiteID struct {
	Function    FunctionID     `json:"function"`
	Kind        SourceSiteKind `json:"kind"`
	Block       int            `json:"block"`
	Instruction int            `json:"instruction"`
	Successor   int            `json:"successor"`
	Role        SiteRole       `json:"role"`
	Ordinal     int            `json:"ordinal"`
}

// Validate checks the source anchor's canonical coordinates.
func (id SourceSiteID) Validate() error { return verifySourceSiteID(id) }

func verifySourceSiteID(id SourceSiteID) error {
	if err := id.Function.validate(); err != nil {
		return fmt.Errorf("coro: source site: %w", err)
	}
	if err := id.Kind.validate(); err != nil {
		return err
	}
	if err := id.Role.validate(); err != nil {
		return err
	}
	if id.Ordinal < 0 {
		return fmt.Errorf("coro: source site has negative role ordinal %d", id.Ordinal)
	}
	switch id.Kind {
	case SourceInstruction:
		if id.Block < 0 || id.Instruction < 0 || id.Successor != -1 {
			return fmt.Errorf("coro: instruction site has noncanonical coordinates block=%d instruction=%d successor=%d", id.Block, id.Instruction, id.Successor)
		}
	case SourceBlockEntry:
		if id.Block < 0 || id.Instruction != -1 || id.Successor != -1 {
			return fmt.Errorf("coro: block-entry site has noncanonical coordinates block=%d instruction=%d successor=%d", id.Block, id.Instruction, id.Successor)
		}
	case SourceEdge:
		if id.Block < 0 || id.Instruction != -1 || id.Successor < 0 {
			return fmt.Errorf("coro: edge site has noncanonical coordinates block=%d instruction=%d successor=%d", id.Block, id.Instruction, id.Successor)
		}
	case SourceFunction:
		if id.Block != -1 || id.Instruction != -1 || id.Successor != -1 {
			return fmt.Errorf("coro: function site has noncanonical coordinates block=%d instruction=%d successor=%d", id.Block, id.Instruction, id.Successor)
		}
	}
	return nil
}

// EmissionSiteID combines an owner-independent source anchor with the exact
// physical emission instance in which it is lowered.
type EmissionSiteID struct {
	Instance EmissionInstanceID `json:"instance"`
	Source   SourceSiteID       `json:"source"`
}

// Validate checks the instance, source coordinates, and logical-function
// ownership of id.
func (id EmissionSiteID) Validate() error { return verifyEmissionSiteID(id) }

func verifyEmissionSiteID(id EmissionSiteID) error {
	if err := verifyEmissionInstanceID(id.Instance); err != nil {
		return err
	}
	if err := verifySourceSiteID(id.Source); err != nil {
		return err
	}
	if id.Instance.Function != id.Source.Function {
		return fmt.Errorf("coro: emission site function %q does not match instance function %q", id.Source.Function, id.Instance.Function)
	}
	return nil
}

// SemanticInstructionOrdinal returns instruction's zero-based ordinal among
// non-DebugRef instructions in its source basic block. It is stable under
// enabling or disabling x/tools debug refs for an otherwise identical CFG.
func SemanticInstructionOrdinal(instruction ssa.Instruction) (int, error) {
	if instruction == nil {
		return 0, fmt.Errorf("coro: cannot identify nil SSA instruction")
	}
	if _, debug := instruction.(*ssa.DebugRef); debug {
		return 0, fmt.Errorf("coro: DebugRef has no semantic instruction ordinal")
	}
	block := instruction.Block()
	if block == nil {
		return 0, fmt.Errorf("coro: SSA instruction has no basic block")
	}
	ordinal := 0
	for _, candidate := range block.Instrs {
		if _, debug := candidate.(*ssa.DebugRef); debug {
			continue
		}
		if candidate == instruction {
			return ordinal, nil
		}
		ordinal++
	}
	return 0, fmt.Errorf("coro: SSA instruction is not present in its reported basic block")
}

// NewInstructionEmissionSiteID freezes one SSA instruction and typed subsite
// into a pointer-free site identity.
func NewInstructionEmissionSiteID(instance EmissionInstanceID, instruction ssa.Instruction, role SiteRole, ordinal int) (EmissionSiteID, error) {
	semantic, err := SemanticInstructionOrdinal(instruction)
	if err != nil {
		return EmissionSiteID{}, err
	}
	if instruction.Block() == nil {
		return EmissionSiteID{}, fmt.Errorf("coro: SSA instruction has no basic block")
	}
	id := EmissionSiteID{
		Instance: instance,
		Source: SourceSiteID{
			Function:    instance.Function,
			Kind:        SourceInstruction,
			Block:       instruction.Block().Index,
			Instruction: semantic,
			Successor:   -1,
			Role:        role,
			Ordinal:     ordinal,
		},
	}
	if err := verifyEmissionSiteID(id); err != nil {
		return EmissionSiteID{}, err
	}
	return id, nil
}

// NewBlockEntryEmissionSiteID constructs a synthetic block-entry anchor, for
// example a compiler-inserted preemption poll.
func NewBlockEntryEmissionSiteID(instance EmissionInstanceID, block int, role SiteRole, ordinal int) (EmissionSiteID, error) {
	return newSyntheticEmissionSiteID(instance, SourceBlockEntry, block, -1, role, ordinal)
}

// NewEdgeEmissionSiteID constructs a synthetic source-CFG edge anchor.
func NewEdgeEmissionSiteID(instance EmissionInstanceID, fromBlock, successorBlock int, role SiteRole, ordinal int) (EmissionSiteID, error) {
	return newSyntheticEmissionSiteID(instance, SourceEdge, fromBlock, successorBlock, role, ordinal)
}

// NewFunctionEmissionSiteID constructs a function-level synthetic anchor.
func NewFunctionEmissionSiteID(instance EmissionInstanceID, role SiteRole, ordinal int) (EmissionSiteID, error) {
	return newSyntheticEmissionSiteID(instance, SourceFunction, -1, -1, role, ordinal)
}

func newSyntheticEmissionSiteID(instance EmissionInstanceID, kind SourceSiteKind, block, successor int, role SiteRole, ordinal int) (EmissionSiteID, error) {
	id := EmissionSiteID{
		Instance: instance,
		Source: SourceSiteID{
			Function:    instance.Function,
			Kind:        kind,
			Block:       block,
			Instruction: -1,
			Successor:   successor,
			Role:        role,
			Ordinal:     ordinal,
		},
	}
	if err := verifyEmissionSiteID(id); err != nil {
		return EmissionSiteID{}, err
	}
	return id, nil
}

// OpClass is the deliberately small semantic class of a sparse lowering fact.
type OpClass string

const (
	OpPure      OpClass = "pure"
	OpLowered   OpClass = "lowered"
	OpCall      OpClass = "call"
	OpIntrinsic OpClass = "intrinsic"
	OpSpawn     OpClass = "spawn"
	OpChannel   OpClass = "channel"
	OpSelect    OpClass = "select"
	OpControl   OpClass = "control"
)

func (class OpClass) validate() error {
	switch class {
	case OpPure, OpLowered, OpCall, OpIntrinsic, OpSpawn, OpChannel, OpSelect, OpControl:
		return nil
	default:
		return fmt.Errorf("coro: invalid lowering op class %q", class)
	}
}

// RecipeID identifies one versioned lowering recipe without embedding its
// physical LLVM expansion in the sparse facts.
type RecipeID string

// ContractID identifies an optional versioned suspend/lifetime contract.
type ContractID string

// BackendFootprint records backend-visible behavior that cannot be inferred
// merely from the Go SSA opcode.
type BackendFootprint uint16

const (
	FootprintManagedCall BackendFootprint = 1 << iota
	FootprintSuspend
	FootprintPanic
	FootprintUnwind
	FootprintAllocation
	FootprintBarrier
	FootprintUnbounded
)

const validBackendFootprint = FootprintManagedCall | FootprintSuspend | FootprintPanic |
	FootprintUnwind | FootprintAllocation | FootprintBarrier | FootprintUnbounded

// Contains reports whether footprint includes every bit in other.
func (footprint BackendFootprint) Contains(other BackendFootprint) bool {
	return footprint&other == other
}

func (footprint BackendFootprint) validate() error {
	if unknown := footprint &^ validBackendFootprint; unknown != 0 {
		return fmt.Errorf("coro: unknown backend footprint bits %#x", uint16(unknown))
	}
	return nil
}

// ManagedEdge is one ordered, exact compiler-inserted managed helper edge.
// Role+Ordinal preserves distinct subsites; repeated targets are not folded.
type ManagedEdge struct {
	Order       int        `json:"order"`
	Role        SiteRole   `json:"role"`
	Ordinal     int        `json:"ordinal"`
	LogicalName string     `json:"logical_name"`
	Target      FunctionID `json:"target"`
	UnwindOnly  bool       `json:"unwind_only"`
}

// ImplicitPanicFact records one ordered implicit nil/bounds/divide/assertion
// failure path that lowering must preserve.
type ImplicitPanicFact struct {
	Order   int      `json:"order"`
	Role    SiteRole `json:"role"`
	Ordinal int      `json:"ordinal"`
	Kind    string   `json:"kind"`
}

// FunctionValueFact records one ordered function-value use without retaining
// an ssa.Value. Targets form a set and are canonicalized by FunctionID.
type FunctionValueFact struct {
	Order    int          `json:"order"`
	Role     SiteRole     `json:"role"`
	Ordinal  int          `json:"ordinal"`
	Targets  []FunctionID `json:"targets"`
	Open     bool         `json:"open"`
	MayBeNil bool         `json:"may_be_nil"`
}

// LoweringFact is one nontrivial source or synthetic site. Ordinary SSA values,
// Phi nodes, terminators, and complete CFG edges remain owned by Go SSA.
type LoweringFact struct {
	Site          EmissionSiteID      `json:"site"`
	Class         OpClass             `json:"class"`
	Recipe        RecipeID            `json:"recipe"`
	Effect        Effect              `json:"effect"`
	Exec          ExecFlags           `json:"exec"`
	Footprint     BackendFootprint    `json:"footprint"`
	Helpers       []ManagedEdge       `json:"helpers"`
	ImplicitPanic []ImplicitPanicFact `json:"implicit_panic"`
	FunctionUses  []FunctionValueFact `json:"function_uses"`
	Contract      ContractID          `json:"contract,omitempty"`
}

// FunctionLoweringFacts is the sparse, owner-scoped local fact projection for
// one physical emission instance.
type FunctionLoweringFacts struct {
	Instance    EmissionInstanceID `json:"instance"`
	LocalEffect Effect             `json:"local_effect"`
	LocalExec   ExecFlags          `json:"local_exec"`
	Sites       []LoweringFact     `json:"sites"`
}

// LoweringFacts is a deterministic, pointer-free lowering ledger. It is not an
// executable CFG and intentionally contains no SSA or LLVM object.
type LoweringFacts struct {
	Schema    string                  `json:"schema"`
	Functions []FunctionLoweringFacts `json:"functions"`
}

// NewLoweringFacts constructs a ledger with the current schema. The input is
// copied by CanonicalJSON; callers may continue assembling it before Verify.
func NewLoweringFacts(functions []FunctionLoweringFacts) LoweringFacts {
	return LoweringFacts{Schema: LoweringFactsSchema, Functions: functions}
}

// Verify checks structural ownership, uniqueness, canonical local lattices,
// ordered subfacts, and conservative function-local effect/exec projections.
func (facts LoweringFacts) Verify() error {
	if facts.Schema != LoweringFactsSchema {
		return fmt.Errorf("coro: lowering facts schema %q, want %q", facts.Schema, LoweringFactsSchema)
	}
	instances := make(map[EmissionInstanceID]struct{}, len(facts.Functions))
	for functionIndex, function := range facts.Functions {
		if err := verifyEmissionInstanceID(function.Instance); err != nil {
			return fmt.Errorf("coro: lowering function %d: %w", functionIndex, err)
		}
		if _, duplicate := instances[function.Instance]; duplicate {
			return fmt.Errorf("coro: duplicate lowering function instance %+v", function.Instance)
		}
		instances[function.Instance] = struct{}{}
		if err := function.LocalEffect.Validate(); err != nil {
			return fmt.Errorf("coro: lowering function %+v local effect: %w", function.Instance, err)
		}
		if function.LocalEffect != function.LocalEffect.Normalize() {
			return fmt.Errorf("coro: lowering function %+v local effect is not normalized", function.Instance)
		}
		if err := function.LocalExec.Validate(); err != nil {
			return fmt.Errorf("coro: lowering function %+v local exec: %w", function.Instance, err)
		}
		sites := make(map[EmissionSiteID]struct{}, len(function.Sites))
		var siteEffect Effect
		var siteExec ExecFlags
		for siteIndex, fact := range function.Sites {
			if err := verifyLoweringFact(function.Instance, fact); err != nil {
				return fmt.Errorf("coro: lowering function %+v site %d: %w", function.Instance, siteIndex, err)
			}
			if _, duplicate := sites[fact.Site]; duplicate {
				return fmt.Errorf("coro: duplicate lowering site %+v", fact.Site)
			}
			sites[fact.Site] = struct{}{}
			siteEffect = siteEffect.Join(fact.Effect)
			siteExec = siteExec.Join(fact.Exec)
		}
		if !function.LocalEffect.Contains(siteEffect) {
			return fmt.Errorf("coro: lowering function %+v local effect %s does not cover site effect %s", function.Instance, function.LocalEffect, siteEffect)
		}
		if !function.LocalExec.Contains(siteExec) {
			return fmt.Errorf("coro: lowering function %+v local exec %s does not cover site exec %s", function.Instance, function.LocalExec, siteExec)
		}
	}
	return nil
}

func verifyLoweringFact(instance EmissionInstanceID, fact LoweringFact) error {
	if err := verifyEmissionSiteID(fact.Site); err != nil {
		return err
	}
	if fact.Site.Instance != instance {
		return fmt.Errorf("coro: site instance %+v does not match containing instance %+v", fact.Site.Instance, instance)
	}
	if err := fact.Class.validate(); err != nil {
		return err
	}
	if err := validateStableToken("lowering recipe", string(fact.Recipe)); err != nil {
		return err
	}
	if err := fact.Effect.Validate(); err != nil {
		return err
	}
	if fact.Effect != fact.Effect.Normalize() {
		return fmt.Errorf("coro: lowering site effect %s is not normalized", fact.Effect)
	}
	if err := fact.Exec.Validate(); err != nil {
		return err
	}
	if err := fact.Footprint.validate(); err != nil {
		return err
	}
	if fact.Effect.MaySuspend() != fact.Footprint.Contains(FootprintSuspend) {
		return fmt.Errorf("coro: lowering site suspend effect and backend footprint disagree")
	}
	if fact.Exec.Contains(MayUnwind) && !fact.Footprint.Contains(FootprintUnwind) {
		return fmt.Errorf("coro: lowering site may unwind without unwind footprint")
	}
	if len(fact.Helpers) != 0 && !fact.Footprint.Contains(FootprintManagedCall) {
		return fmt.Errorf("coro: lowering site has managed helpers without managed-call footprint")
	}
	if len(fact.ImplicitPanic) != 0 && !fact.Footprint.Contains(FootprintPanic) {
		return fmt.Errorf("coro: lowering site has implicit panic without panic footprint")
	}
	if fact.Class == OpPure {
		forbidden := FootprintManagedCall | FootprintSuspend | FootprintPanic | FootprintUnwind | FootprintUnbounded
		if fact.Effect != NoSuspend || fact.Exec.Contains(MayUnwind) || fact.Footprint.Contains(forbidden) || len(fact.Helpers) != 0 || len(fact.ImplicitPanic) != 0 {
			return fmt.Errorf("coro: pure lowering site has non-pure behavior")
		}
	}
	if fact.Contract != "" {
		if err := validateStableToken("lowering contract", string(fact.Contract)); err != nil {
			return err
		}
	}
	if err := verifyManagedEdges(fact.Helpers); err != nil {
		return err
	}
	if err := verifyImplicitPanics(fact.ImplicitPanic); err != nil {
		return err
	}
	if err := verifyFunctionValueFacts(fact.FunctionUses); err != nil {
		return err
	}
	return nil
}

func verifyManagedEdges(edges []ManagedEdge) error {
	roles := make(map[struct {
		role    SiteRole
		ordinal int
	}]struct{}, len(edges))
	for index, edge := range edges {
		if edge.Order != index {
			return fmt.Errorf("coro: managed helper %d has order %d", index, edge.Order)
		}
		if err := edge.Role.validate(); err != nil {
			return err
		}
		if edge.Ordinal < 0 {
			return fmt.Errorf("coro: managed helper %d has negative ordinal %d", index, edge.Ordinal)
		}
		key := struct {
			role    SiteRole
			ordinal int
		}{edge.Role, edge.Ordinal}
		if _, duplicate := roles[key]; duplicate {
			return fmt.Errorf("coro: duplicate managed helper role %q ordinal %d", edge.Role, edge.Ordinal)
		}
		roles[key] = struct{}{}
		if err := validateStableIdentityText("managed helper logical name", edge.LogicalName); err != nil {
			return err
		}
		if err := edge.Target.validate(); err != nil {
			return fmt.Errorf("coro: managed helper %q: %w", edge.LogicalName, err)
		}
	}
	return nil
}

func verifyImplicitPanics(panics []ImplicitPanicFact) error {
	roles := make(map[struct {
		role    SiteRole
		ordinal int
	}]struct{}, len(panics))
	for index, panicFact := range panics {
		if panicFact.Order != index {
			return fmt.Errorf("coro: implicit panic %d has order %d", index, panicFact.Order)
		}
		if err := panicFact.Role.validate(); err != nil {
			return err
		}
		if panicFact.Ordinal < 0 {
			return fmt.Errorf("coro: implicit panic %d has negative ordinal %d", index, panicFact.Ordinal)
		}
		key := struct {
			role    SiteRole
			ordinal int
		}{panicFact.Role, panicFact.Ordinal}
		if _, duplicate := roles[key]; duplicate {
			return fmt.Errorf("coro: duplicate implicit panic role %q ordinal %d", panicFact.Role, panicFact.Ordinal)
		}
		roles[key] = struct{}{}
		if err := validateStableToken("implicit panic kind", panicFact.Kind); err != nil {
			return err
		}
	}
	return nil
}

func verifyFunctionValueFacts(uses []FunctionValueFact) error {
	roles := make(map[struct {
		role    SiteRole
		ordinal int
	}]struct{}, len(uses))
	for index, use := range uses {
		if use.Order != index {
			return fmt.Errorf("coro: function-value fact %d has order %d", index, use.Order)
		}
		if err := use.Role.validate(); err != nil {
			return err
		}
		if use.Ordinal < 0 {
			return fmt.Errorf("coro: function-value fact %d has negative ordinal %d", index, use.Ordinal)
		}
		key := struct {
			role    SiteRole
			ordinal int
		}{use.Role, use.Ordinal}
		if _, duplicate := roles[key]; duplicate {
			return fmt.Errorf("coro: duplicate function-value role %q ordinal %d", use.Role, use.Ordinal)
		}
		roles[key] = struct{}{}
		if len(use.Targets) == 0 && !use.Open && !use.MayBeNil {
			return fmt.Errorf("coro: closed non-nil function-value fact %d has no target", index)
		}
		targets := make(map[FunctionID]struct{}, len(use.Targets))
		for _, target := range use.Targets {
			if err := target.validate(); err != nil {
				return fmt.Errorf("coro: function-value fact %d: %w", index, err)
			}
			if _, duplicate := targets[target]; duplicate {
				return fmt.Errorf("coro: function-value fact %d has duplicate target %q", index, target)
			}
			targets[target] = struct{}{}
		}
	}
	return nil
}

// CanonicalJSON returns a compact deterministic dump. Function and site order,
// and unordered function-value target sets, do not depend on worklist or map
// iteration order. Ordered helper/panic/use sequences retain their exact order.
func (facts LoweringFacts) CanonicalJSON() ([]byte, error) {
	canonical, err := facts.canonical()
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("coro: marshal canonical lowering facts: %w", err)
	}
	return payload, nil
}

// Digest returns a domain-separated SHA-256 of CanonicalJSON.
func (facts LoweringFacts) Digest() (string, error) {
	payload, err := facts.CanonicalJSON()
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(LoweringFactsDigestDomain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (facts LoweringFacts) canonical() (LoweringFacts, error) {
	if err := facts.Verify(); err != nil {
		return LoweringFacts{}, err
	}
	ret := LoweringFacts{Schema: LoweringFactsSchema, Functions: make([]FunctionLoweringFacts, len(facts.Functions))}
	copy(ret.Functions, facts.Functions)
	sort.Slice(ret.Functions, func(i, j int) bool {
		return compareEmissionInstanceID(ret.Functions[i].Instance, ret.Functions[j].Instance) < 0
	})
	for functionIndex := range ret.Functions {
		function := &ret.Functions[functionIndex]
		function.Sites = append([]LoweringFact(nil), function.Sites...)
		if function.Sites == nil {
			function.Sites = make([]LoweringFact, 0)
		}
		sort.Slice(function.Sites, func(i, j int) bool {
			return compareEmissionSiteID(function.Sites[i].Site, function.Sites[j].Site) < 0
		})
		for siteIndex := range function.Sites {
			fact := &function.Sites[siteIndex]
			fact.Helpers = append([]ManagedEdge(nil), fact.Helpers...)
			if fact.Helpers == nil {
				fact.Helpers = make([]ManagedEdge, 0)
			}
			fact.ImplicitPanic = append([]ImplicitPanicFact(nil), fact.ImplicitPanic...)
			if fact.ImplicitPanic == nil {
				fact.ImplicitPanic = make([]ImplicitPanicFact, 0)
			}
			fact.FunctionUses = append([]FunctionValueFact(nil), fact.FunctionUses...)
			if fact.FunctionUses == nil {
				fact.FunctionUses = make([]FunctionValueFact, 0)
			}
			for useIndex := range fact.FunctionUses {
				use := &fact.FunctionUses[useIndex]
				use.Targets = append([]FunctionID(nil), use.Targets...)
				if use.Targets == nil {
					use.Targets = make([]FunctionID, 0)
				}
				sort.Slice(use.Targets, func(i, j int) bool { return use.Targets[i] < use.Targets[j] })
			}
		}
	}
	return ret, nil
}

func compareEmissionInstanceID(left, right EmissionInstanceID) int {
	if left.Function != right.Function {
		if left.Function < right.Function {
			return -1
		}
		return 1
	}
	if left.Owner != right.Owner {
		return strings.Compare(left.Owner, right.Owner)
	}
	return strings.Compare(left.Context, right.Context)
}

func compareEmissionSiteID(left, right EmissionSiteID) int {
	if compared := compareEmissionInstanceID(left.Instance, right.Instance); compared != 0 {
		return compared
	}
	if left.Source.Function != right.Source.Function {
		if left.Source.Function < right.Source.Function {
			return -1
		}
		return 1
	}
	if left.Source.Kind != right.Source.Kind {
		return strings.Compare(string(left.Source.Kind), string(right.Source.Kind))
	}
	if left.Source.Block != right.Source.Block {
		if left.Source.Block < right.Source.Block {
			return -1
		}
		return 1
	}
	if left.Source.Instruction != right.Source.Instruction {
		if left.Source.Instruction < right.Source.Instruction {
			return -1
		}
		return 1
	}
	if left.Source.Successor != right.Source.Successor {
		if left.Source.Successor < right.Source.Successor {
			return -1
		}
		return 1
	}
	if left.Source.Role != right.Source.Role {
		return strings.Compare(string(left.Source.Role), string(right.Source.Role))
	}
	if left.Source.Ordinal < right.Source.Ordinal {
		return -1
	}
	if left.Source.Ordinal > right.Source.Ordinal {
		return 1
	}
	return 0
}

func validateStableIdentityText(name, value string) error {
	if value == "" {
		return fmt.Errorf("coro: empty %s", name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("coro: %s is not valid UTF-8", name)
	}
	for _, char := range value {
		if char == 0 || unicode.IsControl(char) {
			return fmt.Errorf("coro: %s contains a control character", name)
		}
	}
	return nil
}

func validateStableToken(name, value string) error {
	if err := validateStableIdentityText(name, value); err != nil {
		return err
	}
	for _, char := range value {
		if unicode.IsSpace(char) {
			return fmt.Errorf("coro: %s %q contains whitespace", name, value)
		}
	}
	return nil
}
