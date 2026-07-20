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
	"fmt"
	"unicode/utf8"
)

// FunctionID is an opaque, compilation-stable function identity. The frontend
// is responsible for including final link identity, receiver shape, generic
// type arguments, lexical identity, and ABI version in this value.
type FunctionID string

func (id FunctionID) validate() error {
	if id == "" {
		return fmt.Errorf("coro: empty function ID")
	}
	if !utf8.ValidString(string(id)) {
		return fmt.Errorf("coro: function ID is not valid UTF-8")
	}
	return nil
}

// ExternalKind describes how much is known about a function without a body in
// the current SSA program.
type ExternalKind uint8

const (
	// Defined means the current compilation owns the function body.
	Defined ExternalKind = iota
	// ExternalKnown means a compatible summary or trusted effect seed exists.
	ExternalKnown
	// ExternalUnknownManaged is an open Go target without a compatible summary.
	ExternalUnknownManaged
	// ExternalUnknownForeign is an opaque C, assembly, syscall, or host target.
	ExternalUnknownForeign
)

func (k ExternalKind) String() string {
	switch k {
	case Defined:
		return "defined"
	case ExternalKnown:
		return "external-known"
	case ExternalUnknownManaged:
		return "external-unknown-managed"
	case ExternalUnknownForeign:
		return "external-unknown-foreign"
	default:
		return fmt.Sprintf("external-kind(%d)", uint8(k))
	}
}

func (k ExternalKind) validate() error {
	if k > ExternalUnknownForeign {
		return fmt.Errorf("coro: invalid external kind %d", uint8(k))
	}
	return nil
}

// MarshalText implements encoding.TextMarshaler for stable summaries.
func (k ExternalKind) MarshalText() ([]byte, error) {
	if err := k.validate(); err != nil {
		return nil, err
	}
	return []byte(k.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler for stable summaries.
func (k *ExternalKind) UnmarshalText(text []byte) error {
	if k == nil {
		return fmt.Errorf("coro: cannot unmarshal external kind into nil receiver")
	}
	switch string(text) {
	case "defined":
		*k = Defined
	case "external-known":
		*k = ExternalKnown
	case "external-unknown-managed":
		*k = ExternalUnknownManaged
	case "external-unknown-foreign":
		*k = ExternalUnknownForeign
	default:
		return fmt.Errorf("coro: unknown external kind %q", text)
	}
	return nil
}

// FunctionSpec is the frontend-provided description of one graph node.
// NeedsDispatch is set only after function-value flow proves that this value
// crosses an open storage or dynamic call boundary.
type FunctionSpec struct {
	ID   FunctionID
	Seed Effect
	Exec ExecFlags
	// Demand is the compatibility managed-demand seed. New producers should
	// populate ManagedDemand explicitly; both fields are joined.
	Demand Demand
	// ManagedDemand is demand for the ordinary Go plain/coroutine entry model.
	ManagedDemand Demand
	// RawPlainDemand is exact reachability from a legacy-stack root or raw-only
	// reference. RawPlainEntry also seeds this provenance.
	RawPlainDemand bool
	External       ExternalKind
	// TrustedBoundedRecursion is a frontend proof that this function's
	// recursion depth is bounded independently of scheduler preemption. It
	// suppresses the automatic recursive-SCC preemption seed only when every
	// member of the complete recursive SCC carries the same proof.
	TrustedBoundedRecursion bool
	NeedsDispatch           bool
	// RawPlainEntry is an exact frontend ABI requirement for a legacy Go-ABI
	// entry in addition to the managed primary. When the managed primary is
	// already plain the raw entry aliases it; a coroutine primary requires a
	// separately lowered plain body. This capability never changes the managed
	// effect, execution, demand, or representation fixed points.
	RawPlainEntry bool
}

// PrimaryKind is the single primary implementation selected for a function.
type PrimaryKind uint8

const (
	PrimaryPlain PrimaryKind = iota
	PrimaryCoroutine
	PrimaryExternal
)

func (k PrimaryKind) String() string {
	switch k {
	case PrimaryPlain:
		return "plain"
	case PrimaryCoroutine:
		return "coroutine"
	case PrimaryExternal:
		return "external"
	default:
		return fmt.Sprintf("primary-kind(%d)", uint8(k))
	}
}

func (k PrimaryKind) validate() error {
	if k > PrimaryExternal {
		return fmt.Errorf("coro: invalid primary kind %d", uint8(k))
	}
	return nil
}

// MarshalText implements encoding.TextMarshaler for stable summaries.
func (k PrimaryKind) MarshalText() ([]byte, error) {
	if err := k.validate(); err != nil {
		return nil, err
	}
	return []byte(k.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler for stable summaries.
func (k *PrimaryKind) UnmarshalText(text []byte) error {
	if k == nil {
		return fmt.Errorf("coro: cannot unmarshal primary kind into nil receiver")
	}
	switch string(text) {
	case "plain":
		*k = PrimaryPlain
	case "coroutine":
		*k = PrimaryCoroutine
	case "external":
		*k = PrimaryExternal
	default:
		return fmt.Errorf("coro: unknown primary kind %q", text)
	}
	return nil
}

// FunctionPlan is the immutable analysis and emission result for one function.
type FunctionPlan struct {
	ID FunctionID

	// DeclaredEffect is the trusted frontend or imported-summary seed.
	DeclaredEffect Effect
	// LocalEffect additionally includes conservative unknown-call and recursive
	// SCC seeds, before effects from callees are propagated.
	LocalEffect Effect
	// Effect is the least fixed point over all propagating call edges.
	Effect Effect

	// DeclaredExec is the trusted frontend or imported-summary seed.
	DeclaredExec ExecFlags
	// LocalExec additionally includes conservative unknown-target and recursive
	// SCC constraints, before inheritable callee constraints are propagated.
	LocalExec ExecFlags
	// Exec is the least fixed point of inheritable execution constraints and is
	// not part of Effect.
	Exec ExecFlags
	// Demand is the entry-capability fixed point from hard-sync, managed, and
	// spawn roots, plus SyncDemand when RawPlainDemand is true. It remains as a
	// compatibility aggregate; lowering decisions must use the two explicit
	// demand dimensions below.
	Demand Demand
	// ManagedDemand is the ordinary Go entry-capability fixed point.
	ManagedDemand Demand
	// RawPlainDemand records exact legacy-stack reachability independently of
	// managed entry demand.
	RawPlainDemand bool
	// Emission is the one physical body required by this closed-world plan.
	// NoDemand functions use EmitNone without changing their logical Primary,
	// External, or FuncRep selection.
	Emission BodyEmission
	// FuncRep is direct unless value-flow requested an open dispatch boundary.
	FuncRep   FuncRep
	External  ExternalKind
	Recursive bool
	// TrustedBoundedRecursion records an effective whole-SCC proof. Recursive
	// remains true; this bit only explains why recursion itself did not add
	// YieldOnly/NeedsPreempt to the local plan.
	TrustedBoundedRecursion bool
	Primary                 PrimaryKind
	// RawPlainOnly means this function is reached exclusively through exact raw
	// provenance. Its single physical body is EmitRawPlain/PrimaryPlain and its
	// function representation is DirectPlain even though Effect/Exec retain the
	// unmodified managed analysis facts.
	RawPlainOnly bool
	// RawPlainEntry records that code generation must expose the exact legacy
	// Go-ABI entry required by a frozen raw-address consumer. It is independent
	// of Primary: EmitCoroutine therefore denotes a managed coroutine primary
	// plus a separately lowered raw plain entry, not a weakened managed body.
	RawPlainEntry bool
}

// bodyEmissionFor derives the physical body independently from logical
// PrimaryKind and function-value representation. No-demand nodes materialize
// no symbol; a demanded external node retains a declaration, while a demanded
// owned body selects plain or coroutine lowering from its effect.
func bodyEmissionFor(managedDemand Demand, rawPlainDemand bool, effect Effect, external ExternalKind) BodyEmission {
	if managedDemand == NoDemand && !rawPlainDemand {
		return EmitNone
	}
	if external != Defined {
		return EmitExternal
	}
	if managedDemand == NoDemand {
		return EmitRawPlain
	}
	if effect.MaySuspend() {
		return EmitCoroutine
	}
	return EmitPlain
}

func aggregateDemand(managed Demand, rawPlain bool) Demand {
	if rawPlain {
		return managed.Join(SyncDemand)
	}
	return managed
}

// Plan is an immutable, deterministically ordered collection of function
// plans. Use Functions to obtain a defensive copy.
type Plan struct {
	functions []FunctionPlan
	byID      map[FunctionID]int
}

// Functions returns all function plans in FunctionID order.
func (p *Plan) Functions() []FunctionPlan {
	if p == nil {
		return nil
	}
	return append([]FunctionPlan(nil), p.functions...)
}

// Lookup returns the plan for id.
func (p *Plan) Lookup(id FunctionID) (FunctionPlan, bool) {
	if p == nil {
		return FunctionPlan{}, false
	}
	i, ok := p.byID[id]
	if !ok {
		return FunctionPlan{}, false
	}
	return p.functions[i], true
}
