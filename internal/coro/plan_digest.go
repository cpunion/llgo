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
	"unicode/utf8"

	"golang.org/x/tools/go/ssa"
)

// PlanDigestSchema is the independent canonical schema used for archive cache
// identity. It is deliberately separate from SummarySchema: summaries remain
// diagnostic snapshots, while this document covers every lowering plan site.
const PlanDigestSchema = "llgo.coro.plan-digest.v5"

// Current experimental ABI identities. Keeping these in the analysis package
// gives build, cache, and lowering code one version source of truth.
const (
	EntryResolutionABIV0 = "llgo.coro.entry-resolution.v0"
	PhysicalABIV0        = "llgo.coro.physical.v0"
	PhysicalABIV1        = "llgo.coro.physical.v1"
	SchedulerNoneABIV0   = "llgo.coro.scheduler.none.v0"
	// SchedulerChildAwaitABIV0 identifies the first scheduler handoff contract:
	// a coroutine parent may publish one initial-suspended static child and cut
	// its stack, but only the scheduler may subsequently resume or destroy either
	// frame. It deliberately does not claim spawn, park, preemption, or roots.
	SchedulerChildAwaitABIV0 = "llgo.coro.scheduler.child-await.v0"
	// SchedulerProgramBootstrapABIV1 extends child-await with one
	// compiler-owned stackless program root and the runtime's static single-P
	// prepare/adopt/run driver. It still does not claim spawn, park, timers, or
	// preemption.
	SchedulerProgramBootstrapABIV1 = "llgo.coro.scheduler.program-bootstrap.v1"
	PanicLegacyABIV0               = "llgo.coro.panic.legacy.v0"
	FuncRepABIV0                   = "llgo.coro.func-rep.v0"
	// FuncRepABIV1 introduces an explicit descriptor/context representation for
	// dynamically consumed Go function values. The first producer/consumer slice
	// supports only one no-capture, non-suspending plain body; unsupported value
	// shapes and call capabilities remain fail-closed.
	FuncRepABIV1 = "llgo.coro.func-rep.v1"
)

// PlanDigestMetadata contains every effective ABI and target input that may
// affect coroutine lowering. TargetABI, TargetCPU, and TargetFeatures use the
// empty string for the target's canonical default.
type PlanDigestMetadata struct {
	CoroABI        string `json:"coro_abi"`
	SchedulerABI   string `json:"scheduler_abi"`
	PanicABI       string `json:"panic_abi"`
	FuncRepABI     string `json:"func_rep_abi"`
	TargetTriple   string `json:"target_triple"`
	TargetCPU      string `json:"target_cpu"`
	TargetFeatures string `json:"target_features"`
	TargetABI      string `json:"target_abi"`
	PointerBits    int    `json:"pointer_bits"`
	Endianness     string `json:"endianness"`
	DataLayout     string `json:"data_layout"`
}

type planDigestDocument struct {
	Schema           string                  `json:"schema"`
	FunctionIDSchema string                  `json:"function_id_schema"`
	Metadata         PlanDigestMetadata      `json:"metadata"`
	Roots            []planDigestRoot        `json:"roots"`
	Functions        []planDigestFunction    `json:"functions"`
	Calls            []planDigestCall        `json:"calls"`
	LoweredCalls     []planDigestLoweredCall `json:"lowered_calls"`
	ElidedCalls      []planDigestElidedCall  `json:"elided_calls,omitempty"`
	Values           []planDigestValue       `json:"values"`
}

type planDigestRoot struct {
	Function FunctionID `json:"function"`
	Demand   uint8      `json:"demand"`
}

type planDigestFunction struct {
	ID             FunctionID `json:"id"`
	IgnoredBody    bool       `json:"ignored_body"`
	DeclaredEffect uint16     `json:"declared_effect"`
	LocalEffect    uint16     `json:"local_effect"`
	Effect         uint16     `json:"effect"`
	DeclaredExec   uint16     `json:"declared_exec"`
	LocalExec      uint16     `json:"local_exec"`
	Exec           uint16     `json:"exec"`
	Demand         uint8      `json:"demand"`
	Emission       uint8      `json:"emission"`
	FuncRep        uint8      `json:"func_rep"`
	External       uint8      `json:"external"`
	Recursive      bool       `json:"recursive"`
	Primary        uint8      `json:"primary"`
}

type planDigestCall struct {
	Function    FunctionID   `json:"function"`
	Block       int          `json:"block"`
	Instruction int          `json:"instruction"`
	Kind        uint8        `json:"kind"`
	Rep         uint8        `json:"rep"`
	Targets     []FunctionID `json:"targets"`
	Open        bool         `json:"open"`
	Unresolved  uint8        `json:"unresolved"`
	MayBeNil    bool         `json:"may_be_nil"`
}

type planDigestLoweredCall struct {
	Owner       FunctionID `json:"owner"`
	LogicalName string     `json:"logical_name"`
	Target      FunctionID `json:"target"`
}

type planDigestElidedCall struct {
	Function    FunctionID `json:"function"`
	Block       int        `json:"block"`
	Instruction int        `json:"instruction"`
	Elided      bool       `json:"elided"`
}

type planDigestValue struct {
	Site  planDigestValueSite  `json:"site"`
	Funcs []planDigestFuncLeaf `json:"funcs"`
}

type planDigestValueSite struct {
	Function    FunctionID `json:"function"`
	Kind        string     `json:"kind"`
	Index       int        `json:"index"`
	Block       int        `json:"block"`
	Instruction int        `json:"instruction"`
	Operand     int        `json:"operand"`
}

type planDigestFuncLeaf struct {
	Path     []planDigestPathStep `json:"path"`
	Rep      uint8                `json:"rep"`
	Targets  []FunctionID         `json:"targets"`
	MayBeNil bool                 `json:"may_be_nil"`
}

type planDigestPathStep struct {
	Kind  uint8 `json:"kind"`
	Index int   `json:"index"`
}

// CoroPlanDigest returns a domain-separated SHA-256 digest of the complete
// pointer-free plan. Archive-ready identities are mandatory: report-only SSA
// identities must never become cross-compilation cache keys.
func (p *SSAPlan) CoroPlanDigest(metadata PlanDigestMetadata) (string, error) {
	document, err := p.canonicalPlanDigest(metadata)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("coro: marshal canonical plan digest: %w", err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(PlanDigestSchema))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (p *SSAPlan) canonicalPlanDigest(metadata PlanDigestMetadata) (planDigestDocument, error) {
	if p == nil {
		return planDigestDocument{}, fmt.Errorf("coro: digest nil SSA plan")
	}
	if err := metadata.validate(); err != nil {
		return planDigestDocument{}, err
	}
	identity, err := p.functionIDs.normalized()
	if err != nil {
		return planDigestDocument{}, fmt.Errorf("coro: validate plan FunctionID configuration: %w", err)
	}
	if !identity.ArchiveReady {
		return planDigestDocument{}, fmt.Errorf("coro: CoroPlanDigest requires archive-ready FunctionIDs")
	}
	if metadata.CoroABI != identity.CoroABI {
		return planDigestDocument{}, fmt.Errorf("coro: plan digest coroutine ABI %q does not match FunctionID ABI %q", metadata.CoroABI, identity.CoroABI)
	}
	if metadata.SchedulerABI != identity.SchedulerABI {
		return planDigestDocument{}, fmt.Errorf("coro: plan digest scheduler ABI %q does not match FunctionID ABI %q", metadata.SchedulerABI, identity.SchedulerABI)
	}

	roots, err := p.canonicalDigestRoots()
	if err != nil {
		return planDigestDocument{}, err
	}
	functions, err := p.canonicalDigestFunctions()
	if err != nil {
		return planDigestDocument{}, err
	}
	definitions, err := p.digestValueDefinitions()
	if err != nil {
		return planDigestDocument{}, err
	}

	loweredCalls, err := p.canonicalDigestLoweredCalls()
	if err != nil {
		return planDigestDocument{}, err
	}

	document := planDigestDocument{
		Schema:           PlanDigestSchema,
		FunctionIDSchema: FunctionIDSchema,
		Metadata:         metadata,
		Roots:            roots,
		Functions:        functions,
		Calls:            make([]planDigestCall, 0, len(p.callPlans)),
		LoweredCalls:     loweredCalls,
		ElidedCalls:      make([]planDigestElidedCall, 0, len(p.elidedCalls)),
		Values:           make([]planDigestValue, 0, len(p.valuePlans)),
	}
	seenCalls := make(map[ssa.CallInstruction]struct{}, len(p.callPlans))
	seenElidedCalls := make(map[ssa.CallInstruction]struct{}, len(p.elidedCalls))
	coveredValues := make(map[ssa.Value]struct{}, len(p.valuePlans))
	for _, function := range p.functions {
		fn := function.Function
		id := function.Plan.ID
		if p.IgnoresBody(fn) {
			continue
		}
		for index, value := range fn.Params {
			site := planDigestValueSite{Function: id, Kind: "param", Index: index, Block: -1, Instruction: -1, Operand: -1}
			if err := p.appendDigestValue(&document.Values, coveredValues, value, site, true); err != nil {
				return planDigestDocument{}, err
			}
		}
		for index, value := range fn.FreeVars {
			site := planDigestValueSite{Function: id, Kind: "freevar", Index: index, Block: -1, Instruction: -1, Operand: -1}
			if err := p.appendDigestValue(&document.Values, coveredValues, value, site, true); err != nil {
				return planDigestDocument{}, err
			}
		}
		operands := make([]*ssa.Value, 0, 8)
		for blockIndex, block := range fn.Blocks {
			if block == nil {
				return planDigestDocument{}, fmt.Errorf("coro: function %q has nil SSA block %d", id, blockIndex)
			}
			semanticIndex := 0
			for _, instruction := range block.Instrs {
				if instruction == nil {
					return planDigestDocument{}, fmt.Errorf("coro: function %q block %d has nil SSA instruction", id, blockIndex)
				}
				if _, debug := instruction.(*ssa.DebugRef); debug {
					continue
				}
				if value, ok := instruction.(ssa.Value); ok {
					site := planDigestValueSite{Function: id, Kind: "instruction", Index: -1, Block: blockIndex, Instruction: semanticIndex, Operand: -1}
					if err := p.appendDigestValue(&document.Values, coveredValues, value, site, true); err != nil {
						return planDigestDocument{}, err
					}
				}
				if call, ok := instruction.(ssa.CallInstruction); ok {
					if _, builtin := call.Common().Value.(*ssa.Builtin); !builtin {
						if p.ElidesCall(call) {
							if _, planned := p.callPlans[call]; planned {
								return planDigestDocument{}, fmt.Errorf("coro: function %q block %d instruction %d is both elided and assigned a CallPlan", id, blockIndex, semanticIndex)
							}
							if _, duplicate := seenElidedCalls[call]; duplicate {
								return planDigestDocument{}, fmt.Errorf("coro: duplicate elided SSA call occurrence for function %q block %d instruction %d", id, blockIndex, semanticIndex)
							}
							seenElidedCalls[call] = struct{}{}
							document.ElidedCalls = append(document.ElidedCalls, planDigestElidedCall{
								Function: id, Block: blockIndex, Instruction: semanticIndex, Elided: true,
							})
						} else {
							plan, ok := p.callPlans[call]
							if !ok {
								return planDigestDocument{}, fmt.Errorf("coro: missing CallPlan for function %q block %d instruction %d", id, blockIndex, semanticIndex)
							}
							entry, err := p.canonicalDigestCall(id, blockIndex, semanticIndex, call, plan)
							if err != nil {
								return planDigestDocument{}, err
							}
							if _, duplicate := seenCalls[call]; duplicate {
								return planDigestDocument{}, fmt.Errorf("coro: duplicate SSA call occurrence for function %q block %d instruction %d", id, blockIndex, semanticIndex)
							}
							seenCalls[call] = struct{}{}
							document.Calls = append(document.Calls, entry)
						}
					}
				}

				operands = instruction.Operands(operands[:0])
				for operandIndex, operand := range operands {
					if operand == nil || *operand == nil || skipDigestOperand(instruction, operand) {
						continue
					}
					value := *operand
					if _, defined := definitions[value]; defined {
						continue
					}
					site := planDigestValueSite{Function: id, Kind: "operand", Index: -1, Block: blockIndex, Instruction: semanticIndex, Operand: operandIndex}
					if err := p.appendDigestValue(&document.Values, coveredValues, value, site, true); err != nil {
						return planDigestDocument{}, err
					}
				}
				semanticIndex++
			}
		}
	}
	if len(seenCalls) != len(p.callPlans) {
		return planDigestDocument{}, fmt.Errorf("coro: CallPlan coverage mismatch: projected %d of %d plans", len(seenCalls), len(p.callPlans))
	}
	if len(seenElidedCalls) != len(p.elidedCalls) {
		return planDigestDocument{}, fmt.Errorf("coro: elided-call coverage mismatch: projected %d of %d calls", len(seenElidedCalls), len(p.elidedCalls))
	}
	if len(coveredValues) != len(p.valuePlans) {
		return planDigestDocument{}, fmt.Errorf("coro: SSAValuePlan coverage mismatch: projected %d of %d plans", len(coveredValues), len(p.valuePlans))
	}
	return document, nil
}

func (p *SSAPlan) canonicalDigestLoweredCalls() ([]planDigestLoweredCall, error) {
	ret := make([]planDigestLoweredCall, 0)
	for owner, calls := range p.loweredCalls {
		ownerID, ok := p.byFunction[owner]
		if !ok {
			return nil, fmt.Errorf("coro: lowered-call owner %q is absent from the plan", owner.Name())
		}
		previous := ""
		for index, call := range calls {
			if call.LogicalName == "" || !utf8.ValidString(call.LogicalName) || strings.IndexByte(call.LogicalName, 0) >= 0 {
				return nil, fmt.Errorf("coro: lowered call %d in %q has invalid logical name %q", index, ownerID, call.LogicalName)
			}
			if index != 0 && previous >= call.LogicalName {
				return nil, fmt.Errorf("coro: lowered calls in %q are not in strict logical-name order", ownerID)
			}
			previous = call.LogicalName
			targetID, ok := p.byFunction[call.Target]
			if !ok {
				return nil, fmt.Errorf("coro: lowered call %q in %q targets a function outside the plan", call.LogicalName, ownerID)
			}
			ret = append(ret, planDigestLoweredCall{Owner: ownerID, LogicalName: call.LogicalName, Target: targetID})
		}
	}
	sort.Slice(ret, func(i, j int) bool {
		if ret[i].Owner != ret[j].Owner {
			return ret[i].Owner < ret[j].Owner
		}
		return ret[i].LogicalName < ret[j].LogicalName
	})
	return ret, nil
}

func (m PlanDigestMetadata) validate() error {
	required := []struct {
		name  string
		value string
	}{
		{"coroutine ABI", m.CoroABI},
		{"scheduler ABI", m.SchedulerABI},
		{"panic ABI", m.PanicABI},
		{"function representation ABI", m.FuncRepABI},
		{"target triple", m.TargetTriple},
		{"data layout", m.DataLayout},
	}
	for _, field := range required {
		if err := validatePlanDigestText(field.name, field.value, false); err != nil {
			return err
		}
	}
	optional := []struct {
		name  string
		value string
	}{
		{"target CPU", m.TargetCPU},
		{"target features", m.TargetFeatures},
		{"target ABI", m.TargetABI},
	}
	for _, field := range optional {
		if err := validatePlanDigestText(field.name, field.value, true); err != nil {
			return err
		}
	}
	if m.PointerBits <= 0 || m.PointerBits%8 != 0 {
		return fmt.Errorf("coro: plan digest pointer width %d is not a positive multiple of 8", m.PointerBits)
	}
	if m.Endianness != "little" && m.Endianness != "big" {
		return fmt.Errorf("coro: plan digest endianness %q is not little or big", m.Endianness)
	}
	return nil
}

func validatePlanDigestText(name, value string, allowEmpty bool) error {
	if value == "" && !allowEmpty {
		return fmt.Errorf("coro: plan digest %s is empty", name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("coro: plan digest %s is not valid UTF-8", name)
	}
	if strings.IndexByte(value, 0) >= 0 {
		return fmt.Errorf("coro: plan digest %s contains NUL", name)
	}
	return nil
}

func (p *SSAPlan) canonicalDigestRoots() ([]planDigestRoot, error) {
	if p.plan == nil {
		return nil, fmt.Errorf("coro: CoroPlanDigest requires a base plan")
	}
	ret := make([]planDigestRoot, 0, len(p.roots))
	var previous FunctionID
	for index, root := range p.roots {
		if root.Function == nil {
			return nil, fmt.Errorf("coro: SSA root plan %d has nil function", index)
		}
		if err := validateDigestFunctionID(root.ID); err != nil {
			return nil, fmt.Errorf("coro: validate SSA root plan %d: %w", index, err)
		}
		if err := root.Demand.Validate(); err != nil {
			return nil, fmt.Errorf("coro: validate SSA root plan %d demand: %w", index, err)
		}
		if root.Demand == NoDemand {
			return nil, fmt.Errorf("coro: SSA root plan %d has no demand", index)
		}
		if index != 0 && previous >= root.ID {
			return nil, fmt.Errorf("coro: SSA root plans are not in strict FunctionID order")
		}
		previous = root.ID
		if got, ok := p.byFunction[root.Function]; !ok || got != root.ID {
			return nil, fmt.Errorf("coro: missing forward root mapping for %q", root.ID)
		}
		if got, ok := p.byID[root.ID]; !ok || got != root.Function {
			return nil, fmt.Errorf("coro: missing reverse root mapping for %q", root.ID)
		}
		plan, ok := p.plan.Lookup(root.ID)
		if !ok {
			return nil, fmt.Errorf("coro: root %q is absent from the base plan", root.ID)
		}
		if !plan.Demand.Contains(root.Demand) {
			return nil, fmt.Errorf("coro: root %q demand %s is not contained in function demand %s", root.ID, root.Demand, plan.Demand)
		}
		ret = append(ret, planDigestRoot{Function: root.ID, Demand: uint8(root.Demand)})
	}
	return ret, nil
}

func (p *SSAPlan) canonicalDigestFunctions() ([]planDigestFunction, error) {
	if p.plan == nil {
		return nil, fmt.Errorf("coro: CoroPlanDigest requires a base plan")
	}
	baseFunctions := p.plan.Functions()
	if len(p.functions) != len(baseFunctions) || len(p.functions) != len(p.byFunction) || len(p.functions) != len(p.byID) {
		return nil, fmt.Errorf("coro: SSA function-plan coverage mismatch")
	}
	ret := make([]planDigestFunction, 0, len(p.functions))
	var previous FunctionID
	for index, function := range p.functions {
		if function.Function == nil {
			return nil, fmt.Errorf("coro: SSA function plan %d has nil function", index)
		}
		plan := function.Plan
		if err := validateDigestFunctionPlan(plan); err != nil {
			return nil, fmt.Errorf("coro: validate function plan %d: %w", index, err)
		}
		if err := validateDigestFunctionID(plan.ID); err != nil {
			return nil, err
		}
		if index != 0 && previous >= plan.ID {
			return nil, fmt.Errorf("coro: SSA function plans are not in strict FunctionID order")
		}
		previous = plan.ID
		if baseFunctions[index] != plan {
			return nil, fmt.Errorf("coro: SSA function plan %q differs from the base plan", plan.ID)
		}
		if got, ok := p.byFunction[function.Function]; !ok || got != plan.ID {
			return nil, fmt.Errorf("coro: missing forward function mapping for %q", plan.ID)
		}
		if got, ok := p.byID[plan.ID]; !ok || got != function.Function {
			return nil, fmt.Errorf("coro: missing reverse function mapping for %q", plan.ID)
		}
		ret = append(ret, planDigestFunction{
			ID:             plan.ID,
			IgnoredBody:    p.IgnoresBody(function.Function),
			DeclaredEffect: uint16(plan.DeclaredEffect),
			LocalEffect:    uint16(plan.LocalEffect),
			Effect:         uint16(plan.Effect),
			DeclaredExec:   uint16(plan.DeclaredExec),
			LocalExec:      uint16(plan.LocalExec),
			Exec:           uint16(plan.Exec),
			Demand:         uint8(plan.Demand),
			Emission:       uint8(plan.Emission),
			FuncRep:        uint8(plan.FuncRep),
			External:       uint8(plan.External),
			Recursive:      plan.Recursive,
			Primary:        uint8(plan.Primary),
		})
	}
	return ret, nil
}

func validateDigestFunctionPlan(plan FunctionPlan) error {
	if err := plan.ID.validate(); err != nil {
		return err
	}
	effects := []struct {
		name  string
		value Effect
	}{
		{"declared effect", plan.DeclaredEffect},
		{"local effect", plan.LocalEffect},
		{"effect", plan.Effect},
	}
	for _, effect := range effects {
		if err := effect.value.Validate(); err != nil {
			return fmt.Errorf("%s: %w", effect.name, err)
		}
	}
	flags := []struct {
		name  string
		value ExecFlags
	}{
		{"declared execution flags", plan.DeclaredExec},
		{"local execution flags", plan.LocalExec},
		{"execution flags", plan.Exec},
	}
	for _, flag := range flags {
		if err := flag.value.Validate(); err != nil {
			return fmt.Errorf("%s: %w", flag.name, err)
		}
	}
	if err := plan.Demand.Validate(); err != nil {
		return err
	}
	if err := plan.Emission.Validate(); err != nil {
		return err
	}
	if err := plan.FuncRep.Validate(); err != nil {
		return err
	}
	if err := plan.External.validate(); err != nil {
		return err
	}
	if err := plan.Primary.validate(); err != nil {
		return err
	}
	expectedEmission := bodyEmissionFor(plan.Demand, plan.Effect, plan.External)
	if plan.Emission != expectedEmission {
		return fmt.Errorf("coro: function %q emission %s does not match demand %s, effect %s, and external kind %s (want %s)", plan.ID, plan.Emission, plan.Demand, plan.Effect, plan.External, expectedEmission)
	}
	return nil
}

func validateDigestFunctionID(id FunctionID) error {
	prefix := FunctionIDSchema + ":"
	text := string(id)
	if !strings.HasPrefix(text, prefix) {
		return fmt.Errorf("coro: archive function ID %q does not use schema %q", id, FunctionIDSchema)
	}
	encoded := text[len(prefix):]
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != encoded {
		return fmt.Errorf("coro: archive function ID %q does not contain a canonical SHA-256 digest", id)
	}
	return nil
}

func (p *SSAPlan) digestValueDefinitions() (map[ssa.Value]struct{}, error) {
	definitions := make(map[ssa.Value]struct{})
	add := func(value ssa.Value, description string) error {
		if value == nil {
			return fmt.Errorf("coro: nil SSA value definition at %s", description)
		}
		if _, exists := definitions[value]; exists {
			return fmt.Errorf("coro: duplicate SSA value definition at %s", description)
		}
		definitions[value] = struct{}{}
		return nil
	}
	for _, function := range p.functions {
		id := function.Plan.ID
		if p.IgnoresBody(function.Function) {
			continue
		}
		for index, value := range function.Function.Params {
			if err := add(value, fmt.Sprintf("function %q parameter %d", id, index)); err != nil {
				return nil, err
			}
		}
		for index, value := range function.Function.FreeVars {
			if err := add(value, fmt.Sprintf("function %q free variable %d", id, index)); err != nil {
				return nil, err
			}
		}
		for blockIndex, block := range function.Function.Blocks {
			if block == nil {
				return nil, fmt.Errorf("coro: function %q has nil SSA block %d", id, blockIndex)
			}
			semanticIndex := 0
			for _, instruction := range block.Instrs {
				if instruction == nil {
					return nil, fmt.Errorf("coro: function %q block %d has nil SSA instruction", id, blockIndex)
				}
				if _, debug := instruction.(*ssa.DebugRef); debug {
					continue
				}
				if value, ok := instruction.(ssa.Value); ok {
					if err := add(value, fmt.Sprintf("function %q block %d instruction %d", id, blockIndex, semanticIndex)); err != nil {
						return nil, err
					}
				}
				semanticIndex++
			}
		}
	}
	return definitions, nil
}

func skipDigestOperand(instruction ssa.Instruction, operand *ssa.Value) bool {
	call, ok := instruction.(ssa.CallInstruction)
	if !ok || operand != &call.Common().Value {
		return false
	}
	value := *operand
	if _, builtin := value.(*ssa.Builtin); builtin {
		return true
	}
	if call.Common().StaticCallee() != nil {
		_, function := value.(*ssa.Function)
		return function
	}
	return false
}

func requiresDigestValuePlan(value ssa.Value) bool {
	return value != nil && value.Type() != nil && len(funcLeafPaths(value.Type())) != 0
}

func (p *SSAPlan) appendDigestValue(output *[]planDigestValue, covered map[ssa.Value]struct{}, value ssa.Value, site planDigestValueSite, required bool) error {
	if !requiresDigestValuePlan(value) {
		return nil
	}
	plan, ok := p.valuePlans[value]
	if !ok {
		if required {
			return fmt.Errorf("coro: missing SSAValuePlan at %s", formatDigestValueSite(site))
		}
		return nil
	}
	entry, err := p.canonicalDigestValue(value, plan, site)
	if err != nil {
		return err
	}
	// Values with SSA definitions are visited once at that definition. Constants,
	// globals, and function values have no instruction definition, so the caller
	// deliberately projects every stable operand occurrence. Do not deduplicate
	// those occurrences by pointer: covered only proves that every map plan was
	// represented at least once in the pointer-free document.
	covered[value] = struct{}{}
	*output = append(*output, entry)
	return nil
}

func formatDigestValueSite(site planDigestValueSite) string {
	switch site.Kind {
	case "param", "freevar":
		return fmt.Sprintf("function %q %s %d", site.Function, site.Kind, site.Index)
	case "instruction":
		return fmt.Sprintf("function %q block %d instruction %d result", site.Function, site.Block, site.Instruction)
	default:
		return fmt.Sprintf("function %q block %d instruction %d operand %d", site.Function, site.Block, site.Instruction, site.Operand)
	}
}

func (p *SSAPlan) canonicalDigestCall(id FunctionID, block, instruction int, call ssa.CallInstruction, plan SSACallPlan) (planDigestCall, error) {
	if plan.Call != call {
		return planDigestCall{}, fmt.Errorf("coro: CallPlan at function %q block %d instruction %d references a different SSA call", id, block, instruction)
	}
	if err := plan.Kind.validate(); err != nil {
		return planDigestCall{}, err
	}
	if err := plan.Rep.Validate(); err != nil {
		return planDigestCall{}, err
	}
	if err := plan.Unresolved.validate(); err != nil {
		return planDigestCall{}, err
	}
	targets, err := p.canonicalDigestTargets(plan.Targets)
	if err != nil {
		return planDigestCall{}, fmt.Errorf("coro: CallPlan at function %q block %d instruction %d: %w", id, block, instruction, err)
	}
	return planDigestCall{
		Function:    id,
		Block:       block,
		Instruction: instruction,
		Kind:        uint8(plan.Kind),
		Rep:         uint8(plan.Rep),
		Targets:     targets,
		Open:        plan.Open,
		Unresolved:  uint8(plan.Unresolved),
		MayBeNil:    plan.MayBeNil,
	}, nil
}

func (p *SSAPlan) canonicalDigestValue(value ssa.Value, plan SSAValuePlan, site planDigestValueSite) (planDigestValue, error) {
	if plan.Value != value {
		return planDigestValue{}, fmt.Errorf("coro: SSAValuePlan at %s references a different SSA value", formatDigestValueSite(site))
	}
	expectedPaths := funcLeafPaths(value.Type())
	if len(plan.Funcs) != len(expectedPaths) {
		return planDigestValue{}, fmt.Errorf("coro: SSAValuePlan at %s has %d function leaves, want %d", formatDigestValueSite(site), len(plan.Funcs), len(expectedPaths))
	}
	leaves := append(FuncRepMap(nil), plan.Funcs...)
	sort.SliceStable(leaves, func(i, j int) bool { return lessFuncPath(leaves[i].Path, leaves[j].Path) })
	ret := planDigestValue{Site: site, Funcs: make([]planDigestFuncLeaf, 0, len(leaves))}
	for index, leaf := range leaves {
		if !equalDigestFuncPath(leaf.Path, expectedPaths[index]) {
			return planDigestValue{}, fmt.Errorf("coro: SSAValuePlan at %s has a noncanonical function path", formatDigestValueSite(site))
		}
		if err := leaf.Rep.Validate(); err != nil {
			return planDigestValue{}, err
		}
		targets, err := p.canonicalDigestTargets(leaf.Targets)
		if err != nil {
			return planDigestValue{}, fmt.Errorf("coro: SSAValuePlan at %s: %w", formatDigestValueSite(site), err)
		}
		path := make([]planDigestPathStep, len(leaf.Path))
		for pathIndex, step := range leaf.Path {
			if err := validateDigestPathStep(step); err != nil {
				return planDigestValue{}, fmt.Errorf("coro: SSAValuePlan at %s path step %d: %w", formatDigestValueSite(site), pathIndex, err)
			}
			path[pathIndex] = planDigestPathStep{Kind: uint8(step.Kind), Index: step.Index}
		}
		ret.Funcs = append(ret.Funcs, planDigestFuncLeaf{
			Path:     path,
			Rep:      uint8(leaf.Rep),
			Targets:  targets,
			MayBeNil: leaf.MayBeNil,
		})
	}
	return ret, nil
}

func validateDigestPathStep(step FuncPathStep) error {
	if step.Kind > FuncPathChanElement {
		return fmt.Errorf("invalid function path kind %d", uint8(step.Kind))
	}
	switch step.Kind {
	case FuncPathTupleElement, FuncPathStructField:
		if step.Index < 0 {
			return fmt.Errorf("function path kind %d requires a nonnegative index", step.Kind)
		}
	default:
		if step.Index != -1 {
			return fmt.Errorf("function container path kind %d requires index -1", step.Kind)
		}
	}
	return nil
}

func equalDigestFuncPath(left, right []FuncPathStep) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (p *SSAPlan) canonicalDigestTargets(targets []FunctionID) ([]FunctionID, error) {
	ret := append([]FunctionID(nil), targets...)
	sortFunctionIDs(ret)
	canonical := make([]FunctionID, 0, len(ret))
	for _, target := range ret {
		if err := target.validate(); err != nil {
			return nil, err
		}
		if _, ok := p.byID[target]; !ok {
			return nil, fmt.Errorf("target function %q is absent from the SSA plan", target)
		}
		if len(canonical) == 0 || canonical[len(canonical)-1] != target {
			canonical = append(canonical, target)
		}
	}
	return canonical, nil
}
