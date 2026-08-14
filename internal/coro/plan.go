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
	// ManagedEntry is supplied only for an ExternalKnown producer. Defined
	// bodies derive it from their final emission; unknown external declarations
	// have no trusted managed entry ABI.
	ManagedEntry          ManagedEntryKind
	AtomicCost            uint64
	AtomicCostProof       AtomicCostProof
	AtomicCostCertificate string
	// StaticOutcome is producer-owned only for an ExternalKnown declaration.
	// Defined bodies derive it after the complete SSA effect/call fixed point.
	StaticOutcome bool
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
	// ManagedEntry is the exact physical ABI invoked by managed callers. Unlike
	// Emission it remains meaningful for an imported EmitExternal declaration.
	ManagedEntry ManagedEntryKind
	// AtomicCost is the longest path-sensitive semantic work bound of the
	// optional synchronous outcome entry. It is meaningful only when
	// AtomicCostProof is not AtomicCostUnproven. The ordinary ManagedEntry may
	// remain a coroutine when dynamic/function-value consumers also exist; exact
	// static calls can still select the separately emitted outcome entry.
	AtomicCost uint64
	// AtomicCostProof records the closed-world proof class which authorized the
	// bound. EmitOutcomePlain requires a proof and makes that entry primary; an
	// EmitCoroutine plan with a proof emits both the coroutine primary and the
	// static outcome entry. Declared/local effects remain unchanged; the final
	// effect may drop AwaitStructured only when the outcome entry is primary.
	AtomicCostProof AtomicCostProof
	// AtomicCostCertificate is the content-addressed path proof. Imported and
	// local outcome entries require it; unproven functions must leave it empty.
	AtomicCostCertificate string
	// StaticOutcome records a separately emitted synchronous outcome entry for
	// exact static calls. Unlike AtomicCostProof it makes no finite scheduler-gap
	// claim: source loops may execute synchronously. It is therefore only a twin
	// of a coroutine primary, never the managed primary itself.
	StaticOutcome bool
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

// HasStaticOutcome reports that an exact managed static call may use the
// synchronous outcome ABI. An atomic-cost proof is the bounded form of this
// capability; StaticOutcome is the explicitly unbounded form.
func (plan FunctionPlan) HasStaticOutcome() bool {
	return plan.AtomicCostProof.ProvesOutcomePlain() || plan.StaticOutcome
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

// validateManagedEntryPlan keeps the physical entry/cost capability orthogonal
// to logical primary selection. In particular an imported outcome-plain
// declaration remains EmitExternal/PrimaryExternal in the consumer while its
// ManagedEntry records the producer ABI exactly.
func validateManagedEntryPlan(plan FunctionPlan) error {
	if err := plan.ManagedEntry.Validate(); err != nil {
		return err
	}
	if err := plan.AtomicCostProof.Validate(); err != nil {
		return err
	}
	if plan.External == Defined {
		expected := ManagedEntryNone
		switch plan.Emission {
		case EmitPlain, EmitRawPlain:
			expected = ManagedEntryPlain
		case EmitCoroutine:
			expected = ManagedEntryCoroutine
		case EmitOutcomePlain:
			expected = ManagedEntryOutcomePlain
		}
		if plan.ManagedEntry != expected {
			return fmt.Errorf(
				"coro: function %q managed entry %s does not match owned emission %s (want %s)",
				plan.ID, plan.ManagedEntry, plan.Emission, expected,
			)
		}
	} else if plan.External == ExternalKnown {
		if plan.ManagedEntry == ManagedEntryNone {
			return fmt.Errorf("coro: external-known function %q has no producer managed entry", plan.ID)
		}
	} else if plan.ManagedEntry != ManagedEntryNone {
		return fmt.Errorf(
			"coro: function %q has managed entry %s with external kind %s",
			plan.ID, plan.ManagedEntry, plan.External,
		)
	}

	switch plan.ManagedEntry {
	case ManagedEntryPlain:
		if plan.Effect.MaySuspend() && !plan.RawPlainOnly {
			return fmt.Errorf("coro: function %q exports a plain managed entry for suspend effect %s", plan.ID, plan.Effect)
		}
	case ManagedEntryCoroutine:
		if !plan.Effect.MaySuspend() {
			return fmt.Errorf("coro: function %q exports a coroutine managed entry without a suspend effect", plan.ID)
		}
	case ManagedEntryOutcomePlain:
		if plan.Effect != OutcomeStructured || plan.Exec&^MayUnwind != 0 ||
			plan.FuncRep != DirectCoro || plan.Recursive || plan.RawPlainDemand ||
			plan.RawPlainEntry || plan.RawPlainOnly {
			return fmt.Errorf(
				"coro: function %q has invalid outcome-plain managed entry (effect=%s exec=%s representation=%s recursive=%t raw=%t)",
				plan.ID, plan.Effect, plan.Exec, plan.FuncRep, plan.Recursive, plan.RawPlainDemand,
			)
		}
		if !plan.AtomicCostProof.ProvesOutcomePlain() {
			return fmt.Errorf("coro: function %q has an outcome-plain primary without a static outcome proof", plan.ID)
		}
	}

	if plan.StaticOutcome {
		if plan.AtomicCostProof.ProvesOutcomePlain() || plan.AtomicCost != 0 || plan.AtomicCostCertificate != "" {
			return fmt.Errorf("coro: function %q mixes bounded and unbounded static outcome capabilities", plan.ID)
		}
		// The outcome twin is also useful for a no-unwind native block. Such a
		// function always returns the success status, but exact static callers can
		// still erase its coroutine frame. OutcomeStructured is therefore optional;
		// functions which can unwind acquire it through the ordinary effect solver.
		if plan.Recursive || plan.Exec&(BlockForeign|ThreadAffine|NeedsCleanupFrame|OpaqueExec) != 0 ||
			plan.Effect&^(AwaitStructured|OutcomeStructured|MayPark) != 0 {
			return fmt.Errorf(
				"coro: function %q has an invalid unbounded static outcome capability (effect=%s exec=%s recursive=%t)",
				plan.ID, plan.Effect, plan.Exec, plan.Recursive,
			)
		}
		if plan.External == Defined {
			if plan.Emission != EmitCoroutine || plan.ManagedEntry != ManagedEntryCoroutine ||
				plan.Primary != PrimaryCoroutine {
				return fmt.Errorf("coro: function %q has an unbounded static outcome without a coroutine primary twin", plan.ID)
			}
		} else if plan.External == ExternalKnown {
			if plan.ManagedEntry != ManagedEntryCoroutine || plan.Primary != PrimaryExternal {
				return fmt.Errorf(
					"coro: external function %q has an unbounded static outcome with managed entry %s",
					plan.ID, plan.ManagedEntry,
				)
			}
		} else {
			return fmt.Errorf("coro: function %q has an unbounded static outcome with external kind %s", plan.ID, plan.External)
		}
	}

	switch plan.AtomicCostProof {
	case AtomicCostUnproven:
		if plan.AtomicCost != 0 || plan.AtomicCostCertificate != "" || plan.ManagedEntry == ManagedEntryOutcomePlain {
			return fmt.Errorf("coro: function %q has outcome entry/cost without an atomic-cost proof", plan.ID)
		}
	case AtomicCostLeaf, AtomicCostDAG:
		if plan.AtomicCost == 0 || (plan.External != Defined && plan.External != ExternalKnown) {
			return fmt.Errorf("coro: function %q has an invalid atomic-cost capability", plan.ID)
		}
		if plan.External == Defined {
			primaryOutcome := plan.Emission == EmitOutcomePlain && plan.ManagedEntry == ManagedEntryOutcomePlain
			dualOutcome := plan.Emission == EmitCoroutine && plan.ManagedEntry == ManagedEntryCoroutine
			if !primaryOutcome && !dualOutcome {
				return fmt.Errorf(
					"coro: function %q has an atomic outcome capability without an outcome primary or coroutine primary twin (emission=%s managed=%s)",
					plan.ID, plan.Emission, plan.ManagedEntry,
				)
			}
		} else if plan.ManagedEntry != ManagedEntryCoroutine && plan.ManagedEntry != ManagedEntryOutcomePlain {
			return fmt.Errorf(
				"coro: external function %q has an atomic outcome capability with managed entry %s",
				plan.ID, plan.ManagedEntry,
			)
		}
		if err := validateSHA256Hex("atomic-cost certificate", plan.AtomicCostCertificate); err != nil {
			return fmt.Errorf("coro: function %q: %w", plan.ID, err)
		}
	}
	return nil
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
