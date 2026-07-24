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
	"sort"
)

// OutcomeMode selects whether a demanded managed body may transport terminal
// Return/Panic outcomes through its coroutine frame. Legacy mode preserves the
// stack-unwind planner. ExplicitStatus mode colors only MayUnwind bodies that
// are reached in an asynchronous physical context; synchronous-only islands
// retain their plain ABI.
type OutcomeMode uint8

const (
	OutcomeLegacy OutcomeMode = iota
	OutcomeExplicitStatus
)

func (m OutcomeMode) validate() error {
	if m > OutcomeExplicitStatus {
		return fmt.Errorf("coro: invalid outcome mode %d", uint8(m))
	}
	return nil
}

// GraphAnalysisConfig controls target-independent graph fixed points.
type GraphAnalysisConfig struct {
	OutcomeMode OutcomeMode
}

// CallKind controls effect propagation across a statically known edge.
type CallKind uint8

const (
	// CallDirect is a normal managed call and transparent await.
	CallDirect CallKind = iota
	// CallDefer executes in the same logical G and propagates effect.
	CallDefer
	// CallSpawn creates another G and does not taint the caller.
	CallSpawn
	// CallForeign stack-cuts the caller and contributes WaitForeign directly.
	CallForeign
	// CallUnwind is an exact compiler-lowered call reachable only on a path
	// that cannot return normally from the caller. It keeps the callee demanded
	// for emission and panic-ABI verification, but its suspend effect and
	// execution constraints do not describe the caller's normal-return body.
	CallUnwind
	// CallExplicitStatusElided is an exact terminal source-panic helper use that
	// a physical ExplicitStatus coroutine replaces with current-frame outcome
	// publication. A managed plain owner retains synchronous demand; a managed
	// coroutine contributes no demand because the call is absent; a raw owner
	// retains raw provenance because its legacy body still calls the helper.
	CallExplicitStatusElided
	// CallTrustedInline is an exact frontend-certified invocation of an
	// otherwise conservative ExternalUnknownForeign target. It suppresses only
	// that invocation's default blocking/wait classification and replaces the
	// target's default callable-contract execution projection with the selected
	// refinement. Independent IRQ, unwind, and other execution constraints still
	// propagate.
	CallTrustedInline
	// CallDirectNoUnwind is an ordinary managed direct call whose exact
	// occurrence has a context-sensitive no-unwind proof. Suspend effects,
	// demand, affinity, IRQ, and every other execution property propagate
	// normally; only the callee's conservative MayUnwind bit is suppressed.
	CallDirectNoUnwind
)

func (k CallKind) validate() error {
	if k > CallDirectNoUnwind {
		return fmt.Errorf("coro: invalid call kind %d", uint8(k))
	}
	return nil
}

// callableContractExecFlags is the complete execution-flag image currently
// produced by CallableContractExecConstraints. Keeping this narrow is what
// prevents an invocation refinement from suppressing independent IRQUnsafe,
// MayUnwind, or future non-contract constraints.
const callableContractExecFlags = ThreadAffine | OpaqueExec

// CallEdge is a statically resolved call graph edge.
type CallEdge struct {
	Caller FunctionID
	Callee FunctionID
	Kind   CallKind
	// DefaultContractExec and SelectedContractExec are meaningful only for an
	// exact CallTrustedInline edge. They replace the target declaration's
	// default callable-contract projection for this invocation; they never
	// replace the target's complete execution flags.
	DefaultContractExec  ExecFlags
	SelectedContractExec ExecFlags
}

// ReferenceEdge records that a demanded owner materializes or publishes a
// reference to target. It propagates entry demand only: taking a function value
// neither calls the target nor inherits its suspend effect or execution flags.
// SSA value-flow uses this edge for function values crossing boxing, aggregate,
// or other dynamically consumed boundaries.
type ReferenceEdge struct {
	Owner  FunctionID
	Target FunctionID
	// SyncOnly is an exact frontend ABI proof that this reference is consumed
	// only through a managed synchronous descriptor/plain entry. It retains the
	// target's managed plain demand without inheriting the owner's coroutine
	// execution context.
	SyncOnly bool
	// RawPlain is an exact legacy-stack publication. It propagates only raw
	// provenance, never managed synchronous demand. RawPlain and SyncOnly are
	// mutually exclusive: the latter remains a managed descriptor boundary.
	RawPlain bool
}

// UnknownTarget describes an unresolved call target.
type UnknownTarget uint8

const (
	// UnknownManaged conservatively contributes OpaqueSuspend.
	UnknownManaged UnknownTarget = iota
	// UnknownForeign conservatively contributes WaitForeign.
	UnknownForeign
	// UnknownManagedDispatch is an open managed call whose callee operand is
	// already carried by the universal coroutine descriptor. The descriptor
	// performs a structured await, so direct and deferred calls contribute only
	// AwaitStructured instead of an opaque suspend/execution domain.
	UnknownManagedDispatch
	// UnknownManagedInterfaceDispatch is an open interface invoke whose itab
	// method word is carried by the universal coroutine descriptor ABI. This is
	// distinct from function-value descriptor transport: raw itab entries and
	// foreign methods must remain fail-closed.
	UnknownManagedInterfaceDispatch
)

func (k UnknownTarget) validate() error {
	if k > UnknownManagedInterfaceDispatch {
		return fmt.Errorf("coro: invalid unknown target kind %d", uint8(k))
	}
	return nil
}

func (k UnknownTarget) managedDispatch() bool {
	return k == UnknownManagedDispatch || k == UnknownManagedInterfaceDispatch
}

// UnknownCall describes an unresolved call site. Spawn calls do not propagate
// effect to the caller even when their target is unknown.
type UnknownCall struct {
	Caller FunctionID
	Kind   CallKind
	Target UnknownTarget
}

type edgeKey struct {
	caller FunctionID
	callee FunctionID
	kind   CallKind
}

type referenceKey struct {
	owner    FunctionID
	target   FunctionID
	syncOnly bool
	rawPlain bool
}

type unknownKey struct {
	caller FunctionID
	kind   CallKind
	target UnknownTarget
}

// Graph is a target-independent function call graph.
type Graph struct {
	functions  map[FunctionID]FunctionSpec
	edges      map[edgeKey]CallEdge
	references map[referenceKey]ReferenceEdge
	unknown    map[unknownKey]UnknownCall
}

// NewGraph creates an empty call graph.
func NewGraph() *Graph {
	return &Graph{
		functions:  make(map[FunctionID]FunctionSpec),
		edges:      make(map[edgeKey]CallEdge),
		references: make(map[referenceKey]ReferenceEdge),
		unknown:    make(map[unknownKey]UnknownCall),
	}
}

// AddFunction adds a function description. Function IDs must be unique.
func (g *Graph) AddFunction(spec FunctionSpec) error {
	if g == nil {
		return fmt.Errorf("coro: add function to nil graph")
	}
	if g.functions == nil {
		g.functions = make(map[FunctionID]FunctionSpec)
	}
	if err := spec.ID.validate(); err != nil {
		return err
	}
	if err := spec.Seed.Validate(); err != nil {
		return fmt.Errorf("coro: function %q: %w", spec.ID, err)
	}
	if err := spec.Exec.Validate(); err != nil {
		return fmt.Errorf("coro: function %q: %w", spec.ID, err)
	}
	if err := spec.Demand.Validate(); err != nil {
		return fmt.Errorf("coro: function %q: %w", spec.ID, err)
	}
	if err := spec.ManagedDemand.Validate(); err != nil {
		return fmt.Errorf("coro: function %q managed demand: %w", spec.ID, err)
	}
	if err := spec.External.validate(); err != nil {
		return fmt.Errorf("coro: function %q: %w", spec.ID, err)
	}
	if spec.RawPlainEntry && spec.External != Defined {
		return fmt.Errorf("coro: function %q: raw plain entry requires a defined body", spec.ID)
	}
	if _, exists := g.functions[spec.ID]; exists {
		return fmt.Errorf("coro: duplicate function %q", spec.ID)
	}
	spec.Seed = spec.Seed.Normalize()
	g.functions[spec.ID] = spec
	return nil
}

// AddCall adds a statically resolved call edge. Identical duplicate edges are
// ignored; duplicate endpoint/kind records with different invocation facts are
// rejected rather than being silently overwritten.
// Endpoints may be added after the edge; Analyze validates the complete graph.
func (g *Graph) AddCall(edge CallEdge) error {
	if g == nil {
		return fmt.Errorf("coro: add call to nil graph")
	}
	if g.edges == nil {
		g.edges = make(map[edgeKey]CallEdge)
	}
	if err := edge.Caller.validate(); err != nil {
		return err
	}
	if err := edge.Callee.validate(); err != nil {
		return err
	}
	if err := edge.Kind.validate(); err != nil {
		return err
	}
	if err := edge.DefaultContractExec.Validate(); err != nil {
		return fmt.Errorf("coro: call default contract execution projection: %w", err)
	}
	if err := edge.SelectedContractExec.Validate(); err != nil {
		return fmt.Errorf("coro: call selected contract execution projection: %w", err)
	}
	contractExec := edge.DefaultContractExec | edge.SelectedContractExec
	if edge.Kind != CallTrustedInline {
		if contractExec != 0 {
			return fmt.Errorf("coro: call kind %d carries trusted-inline contract execution projections", edge.Kind)
		}
	} else {
		if unsupported := contractExec &^ callableContractExecFlags; unsupported != 0 {
			return fmt.Errorf("coro: trusted-inline contract execution projection contains non-contract flags %s", unsupported)
		}
		if widening := edge.SelectedContractExec &^ edge.DefaultContractExec; widening != 0 {
			return fmt.Errorf("coro: trusted-inline selected execution projection widens default by %s", widening)
		}
	}
	key := edgeKey{caller: edge.Caller, callee: edge.Callee, kind: edge.Kind}
	if previous, exists := g.edges[key]; exists && previous != edge {
		return fmt.Errorf("coro: conflicting duplicate call edge from %q to %q with kind %d", edge.Caller, edge.Callee, edge.Kind)
	}
	g.edges[key] = edge
	return nil
}

// AddReference adds a demand-only owner-to-target function-value edge.
// Duplicate references are ignored. Endpoints may be added later; Analyze
// validates the complete graph deterministically.
func (g *Graph) AddReference(edge ReferenceEdge) error {
	if g == nil {
		return fmt.Errorf("coro: add reference to nil graph")
	}
	if g.references == nil {
		g.references = make(map[referenceKey]ReferenceEdge)
	}
	if err := edge.Owner.validate(); err != nil {
		return err
	}
	if err := edge.Target.validate(); err != nil {
		return err
	}
	if edge.SyncOnly && edge.RawPlain {
		return fmt.Errorf("coro: reference from %q to %q is both managed sync-only and raw-plain", edge.Owner, edge.Target)
	}
	key := referenceKey{owner: edge.Owner, target: edge.Target, syncOnly: edge.SyncOnly, rawPlain: edge.RawPlain}
	g.references[key] = edge
	return nil
}

// AddUnknownCall adds an unresolved call site. Duplicate descriptions are
// ignored.
func (g *Graph) AddUnknownCall(call UnknownCall) error {
	if g == nil {
		return fmt.Errorf("coro: add unknown call to nil graph")
	}
	if g.unknown == nil {
		g.unknown = make(map[unknownKey]UnknownCall)
	}
	if err := call.Caller.validate(); err != nil {
		return err
	}
	if err := call.Kind.validate(); err != nil {
		return err
	}
	if call.Kind == CallUnwind || call.Kind == CallExplicitStatusElided ||
		call.Kind == CallTrustedInline || call.Kind == CallDirectNoUnwind {
		return fmt.Errorf("coro: call kind %d requires an exact target", call.Kind)
	}
	if err := call.Target.validate(); err != nil {
		return err
	}
	key := unknownKey{caller: call.Caller, kind: call.Kind, target: call.Target}
	g.unknown[key] = call
	return nil
}

// Analyze computes the least suspend-effect, execution-flag, and entry-demand
// fixed points, then derives one physical body emission per function. Traversal
// and output are deterministic regardless of graph insertion order.
func (g *Graph) Analyze() (*Plan, error) {
	return g.AnalyzeWithConfig(GraphAnalysisConfig{})
}

// AnalyzeWithConfig computes the graph plan under config. Explicit outcome
// coloring participates in the same monotone effect/demand fixed point as
// ordinary suspension; it is not a post-hoc emission rewrite.
func (g *Graph) AnalyzeWithConfig(config GraphAnalysisConfig) (*Plan, error) {
	if g == nil {
		return nil, fmt.Errorf("coro: analyze nil graph")
	}
	if err := config.OutcomeMode.validate(); err != nil {
		return nil, err
	}
	ids := make([]FunctionID, 0, len(g.functions))
	for id := range g.functions {
		ids = append(ids, id)
	}
	sortFunctionIDs(ids)

	edges, err := g.sortedEdges()
	if err != nil {
		return nil, err
	}
	references, err := g.sortedReferences()
	if err != nil {
		return nil, err
	}
	unknown, err := g.sortedUnknownCalls()
	if err != nil {
		return nil, err
	}
	recursive, recursiveSCCs := recursiveFunctions(ids, edges)
	trustedBoundedRecursion := make(map[FunctionID]bool, len(recursive))
	for _, component := range recursiveSCCs {
		trusted := true
		for _, member := range component {
			if !g.functions[member].TrustedBoundedRecursion {
				trusted = false
				break
			}
		}
		if trusted {
			for _, member := range component {
				trustedBoundedRecursion[member] = true
			}
		}
	}

	local := make(map[FunctionID]Effect, len(ids))
	effects := make(map[FunctionID]Effect, len(ids))
	localExec := make(map[FunctionID]ExecFlags, len(ids))
	execFlags := make(map[FunctionID]ExecFlags, len(ids))
	managedDemands := make(map[FunctionID]Demand, len(ids))
	rawPlainDemands := make(map[FunctionID]bool, len(ids))
	for _, id := range ids {
		spec := g.functions[id]
		effect := spec.Seed
		exec := spec.Exec
		switch spec.External {
		case ExternalUnknownManaged:
			effect = effect.Join(OpaqueSuspend)
			exec = exec.Join(OpaqueExec)
		case ExternalUnknownForeign:
			exec = exec.Join(BlockForeign | IRQUnsafe)
		}
		if exec.Contains(NeedsPreempt) {
			effect = effect.Join(YieldOnly)
		}
		if recursive[id] && !trustedBoundedRecursion[id] {
			effect = effect.Join(YieldOnly)
			exec = exec.Join(NeedsPreempt)
		}
		local[id] = effect
		effects[id] = effect
		localExec[id] = exec
		execFlags[id] = exec
		managedDemands[id] = spec.Demand.Join(spec.ManagedDemand)
		rawPlainDemands[id] = spec.RawPlainDemand || spec.RawPlainEntry
	}

	for _, call := range unknown {
		if call.Kind == CallSpawn || ((call.Kind == CallUnwind || call.Kind == CallExplicitStatusElided) && call.Target.managedDispatch()) {
			continue
		}
		var effect Effect
		if call.Kind == CallForeign || call.Target == UnknownForeign {
			effect = WaitForeign
			// The managed caller is stack-cut before the opaque operation, so
			// it is not itself BlockForeign. It is nevertheless unsafe in an
			// interrupt-reachable graph unless a trusted foreign summary proves
			// otherwise.
			localExec[call.Caller] = localExec[call.Caller].Join(IRQUnsafe)
			execFlags[call.Caller] = execFlags[call.Caller].Join(IRQUnsafe)
		} else if call.Target.managedDispatch() {
			effect = AwaitStructured
		} else {
			effect = OpaqueSuspend
			localExec[call.Caller] = localExec[call.Caller].Join(OpaqueExec)
			execFlags[call.Caller] = execFlags[call.Caller].Join(OpaqueExec)
		}
		local[call.Caller] = local[call.Caller].Join(effect)
		effects[call.Caller] = effects[call.Caller].Join(effect)
	}

	// Propagate effects and inheritable execution constraints from callee to
	// caller with a reverse worklist. Every update only adds finite lattice bits,
	// so this is O(E * lattice-height), including for adversarial long chains.
	callers := make(map[FunctionID][]CallEdge, len(ids))
	for _, edge := range edges {
		callers[edge.Callee] = append(callers[edge.Callee], edge)
	}
	queue := append([]FunctionID(nil), ids...)
	queued := make(map[FunctionID]bool, len(ids))
	for _, id := range queue {
		queued[id] = true
	}
	for head := 0; head < len(queue); head++ {
		callee := queue[head]
		queued[callee] = false
		for _, edge := range callers[callee] {
			var effectContribution Effect
			var execContribution ExecFlags
			switch edge.Kind {
			case CallDirect, CallDefer, CallDirectNoUnwind:
				effectContribution = managedCallEffect(effects[callee])
				if localExec[callee].Contains(BlockForeign) {
					effectContribution = effectContribution.Join(WaitForeign)
				}
				execContribution = execFlags[callee] & propagatedExecFlags
				if edge.Kind == CallDirectNoUnwind {
					execContribution &^= MayUnwind
				}
			case CallSpawn:
				continue
			case CallForeign:
				effectContribution = WaitForeign
				execContribution = execFlags[callee] & propagatedExecFlags
			case CallTrustedInline:
				// The occurrence certificate replaces only the execution bits
				// owned by the target's default callable contract. IRQUnsafe,
				// MayUnwind, and every other independent may-property remain.
				execContribution = execFlags[callee] & propagatedExecFlags
				execContribution &^= edge.DefaultContractExec
				execContribution |= edge.SelectedContractExec
			case CallUnwind, CallExplicitStatusElided:
				// The exact target remains in the graph and is demanded below.
				// Its behavior is confined to a path that cannot return normally,
				// so it does not constrain the caller's normal-return body.
				continue
			}
			nextEffect := effects[edge.Caller].Join(effectContribution)
			nextExec := execFlags[edge.Caller].Join(execContribution)
			if nextEffect == effects[edge.Caller] && nextExec == execFlags[edge.Caller] {
				continue
			}
			effects[edge.Caller] = nextEffect
			execFlags[edge.Caller] = nextExec
			if !queued[edge.Caller] {
				queue = append(queue, edge.Caller)
				queued[edge.Caller] = true
			}
		}
	}
	for _, edge := range edges {
		if edge.Kind != CallForeign {
			continue
		}
		if effects[edge.Callee].MaySuspend() {
			return nil, fmt.Errorf("coro: foreign call target %q has suspend effect %s", edge.Callee, effects[edge.Callee])
		}
		if !execFlags[edge.Callee].Contains(BlockForeign) {
			return nil, fmt.Errorf("coro: foreign call target %q lacks block-foreign flag", edge.Callee)
		}
	}
	for _, edge := range edges {
		if edge.Kind != CallTrustedInline {
			continue
		}
		spec := g.functions[edge.Callee]
		if spec.External != ExternalUnknownForeign || effects[edge.Callee] != NoSuspend ||
			!localExec[edge.Callee].Contains(BlockForeign) {
			return nil, fmt.Errorf(
				"coro: trusted-inline target %q is not one conservative no-suspend unknown-foreign callable",
				edge.Callee,
			)
		}
		declared := spec.Exec & callableContractExecFlags
		localLane := localExec[edge.Callee] & callableContractExecFlags
		finalLane := execFlags[edge.Callee] & callableContractExecFlags
		if declared != edge.DefaultContractExec || localLane != edge.DefaultContractExec || finalLane != edge.DefaultContractExec {
			return nil, fmt.Errorf(
				"coro: trusted-inline target %q default contract execution projection is %s, lanes are declared=%s local=%s final=%s",
				edge.Callee, edge.DefaultContractExec, declared, localLane, finalLane,
			)
		}
	}

	// Demand follows reachable call edges, but it does not select a second
	// primary body. Once effects are known, a bounded helper is consumed through
	// its plain entry and a suspendable child through its coroutine entry. This
	// also converts the body of a suspendable hard-sync root to asynchronous mode
	// after its boundary adapter. A spawn always creates an asynchronous root and
	// a foreign thunk has a plain ABI.
	outgoing := make(map[FunctionID][]CallEdge, len(ids))
	for _, edge := range edges {
		outgoing[edge.Caller] = append(outgoing[edge.Caller], edge)
	}
	referenced := make(map[FunctionID][]ReferenceEdge, len(ids))
	for _, edge := range references {
		referenced[edge.Owner] = append(referenced[edge.Owner], edge)
	}
	unknownByCaller := make(map[FunctionID][]UnknownCall, len(ids))
	for _, call := range unknown {
		unknownByCaller[call.Caller] = append(unknownByCaller[call.Caller], call)
	}

	// Establish reachability in the two independent entry domains before
	// demand-sensitive outcome coloring. Exact static calls preserve their
	// current domain. Raw publications enter the raw domain; ordinary
	// first-class transport always enters the managed domain and therefore can
	// never manufacture a RawPlainOnly target.
	queue = queue[:0]
	clear(queued)
	enqueue := func(id FunctionID) {
		if !queued[id] {
			queued[id] = true
			queue = append(queue, id)
		}
	}
	joinManaged := func(id FunctionID, contribution Demand) {
		next := managedDemands[id].Join(contribution)
		if next != managedDemands[id] {
			managedDemands[id] = next
			enqueue(id)
		}
	}
	joinRaw := func(id FunctionID) {
		if !rawPlainDemands[id] {
			rawPlainDemands[id] = true
			enqueue(id)
		}
	}
	if config.OutcomeMode == OutcomeLegacy {
		for _, id := range ids {
			if managedDemands[id] != NoDemand || rawPlainDemands[id] {
				enqueue(id)
			}
		}
		for head := 0; head < len(queue); head++ {
			owner := queue[head]
			queued[owner] = false
			managed := managedDemands[owner] != NoDemand
			raw := rawPlainDemands[owner]
			if raw && g.functions[owner].NeedsDispatch {
				contribution := SyncDemand
				if effects[owner].MaySuspend() {
					contribution = AsyncDemand
				}
				joinManaged(owner, contribution)
				managed = true
			}
			for _, edge := range outgoing[owner] {
				if managed {
					contribution := SyncDemand
					switch edge.Kind {
					case CallSpawn:
						contribution = AsyncDemand
					case CallDirect, CallDefer, CallDirectNoUnwind:
						if effects[edge.Callee].MaySuspend() {
							contribution = AsyncDemand
						}
					}
					joinManaged(edge.Callee, contribution)
				}
				if raw {
					if edge.Kind == CallSpawn {
						joinManaged(edge.Callee, AsyncDemand)
					} else {
						joinRaw(edge.Callee)
					}
				}
			}
			for _, edge := range referenced[owner] {
				if edge.RawPlain {
					if managed || raw {
						joinRaw(edge.Target)
					}
					continue
				}
				if !managed && !raw {
					continue
				}
				contribution := SyncDemand
				if !edge.SyncOnly && effects[edge.Target].MaySuspend() {
					contribution = AsyncDemand
				}
				joinManaged(edge.Target, contribution)
			}
			if raw {
				for _, call := range unknownByCaller[owner] {
					if call.Target != UnknownForeign {
						joinManaged(owner, AsyncDemand)
						break
					}
				}
			}
		}
	}

	queue = queue[:0]
	clear(queued)
	for _, id := range ids {
		if managedDemands[id] != NoDemand {
			queue = append(queue, id)
			queued[id] = true
		}
	}
	if config.OutcomeMode == OutcomeLegacy {
		for head := 0; head < len(queue); head++ {
			caller := queue[head]
			queued[caller] = false
			for _, edge := range outgoing[caller] {
				var contribution Demand
				switch edge.Kind {
				case CallSpawn:
					contribution = AsyncDemand
				case CallForeign, CallTrustedInline:
					contribution = SyncDemand
				case CallUnwind, CallExplicitStatusElided:
					// Legacy panic lowering is a synchronous boundary. A suspendable
					// target is still emitted as a coroutine and must be rejected by
					// the panic-ABI/lowering verifier until such an adapter exists.
					contribution = SyncDemand
				case CallDirect, CallDefer, CallDirectNoUnwind:
					contribution = SyncDemand
					if effects[edge.Callee].MaySuspend() {
						contribution = AsyncDemand
					}
				}
				next := managedDemands[edge.Callee].Join(contribution)
				if next != managedDemands[edge.Callee] {
					managedDemands[edge.Callee] = next
					if !queued[edge.Callee] {
						queue = append(queue, edge.Callee)
						queued[edge.Callee] = true
					}
				}
			}
			for _, edge := range referenced[caller] {
				if edge.RawPlain {
					continue
				}
				contribution := SyncDemand
				if !edge.SyncOnly && effects[edge.Target].MaySuspend() {
					contribution = AsyncDemand
				}
				next := managedDemands[edge.Target].Join(contribution)
				if next != managedDemands[edge.Target] {
					managedDemands[edge.Target] = next
					if !queued[edge.Target] {
						queue = append(queue, edge.Target)
						queued[edge.Target] = true
					}
				}
			}
		}
	}

	if config.OutcomeMode == OutcomeExplicitStatus {
		// Explicit outcomes make effect and demand mutually dependent: an async
		// MayUnwind body needs a coroutine outcome frame, while reaching that
		// coroutine body makes its exact MayUnwind children async managed demands.
		// Drive both monotone lattices with event queues instead of rewriting emission
		// after the ordinary fixed point.
		referencingOwners := make(map[FunctionID][]FunctionID, len(ids))
		for _, edge := range references {
			referencingOwners[edge.Target] = append(referencingOwners[edge.Target], edge.Owner)
		}

		effectQueue := make([]FunctionID, 0, len(ids))
		demandQueue := make([]FunctionID, 0, len(ids))
		effectQueued := make(map[FunctionID]bool, len(ids))
		demandQueued := make(map[FunctionID]bool, len(ids))
		enqueueEffect := func(id FunctionID) {
			if !effectQueued[id] {
				effectQueued[id] = true
				effectQueue = append(effectQueue, id)
			}
		}
		enqueueDemand := func(id FunctionID) {
			// Effect changes only alter the entry mode selected by an already
			// reachable owner. They must not turn a dormant reverse caller or
			// function-reference owner into a source of demand. If the owner is
			// reached later, the edge that joins its demand enqueues it with all
			// current callee effects already available.
			if (managedDemands[id] != NoDemand || rawPlainDemands[id]) && !demandQueued[id] {
				demandQueued[id] = true
				demandQueue = append(demandQueue, id)
			}
		}
		joinManagedDemand := func(id FunctionID, contribution Demand) {
			next := managedDemands[id].Join(contribution)
			if next != managedDemands[id] {
				managedDemands[id] = next
				enqueueDemand(id)
			}
		}
		joinRawDemand := func(id FunctionID) {
			if !rawPlainDemands[id] {
				rawPlainDemands[id] = true
				enqueueDemand(id)
			}
		}
		addOutcome := func(id FunctionID) bool {
			spec := g.functions[id]
			if spec.External != Defined || managedDemands[id] == NoDemand || !execFlags[id].Contains(MayUnwind) ||
				(!managedDemands[id].Contains(AsyncDemand) && !effects[id].MaySuspend()) ||
				local[id].Contains(OutcomeStructured) {
				return false
			}
			local[id] = local[id].Join(OutcomeStructured)
			next := effects[id].Join(OutcomeStructured)
			if next != effects[id] {
				effects[id] = next
				enqueueEffect(id)
			}
			return true
		}
		calleeNeedsOutcomeChild := func(callee FunctionID) bool {
			return g.functions[callee].External == Defined && execFlags[callee].Contains(MayUnwind)
		}
		for _, id := range ids {
			if managedDemands[id] != NoDemand || rawPlainDemands[id] {
				enqueueDemand(id)
			}
		}

		effectHead, demandHead := 0, 0
		for effectHead < len(effectQueue) || demandHead < len(demandQueue) {
			for demandHead < len(demandQueue) {
				caller := demandQueue[demandHead]
				demandHead++
				demandQueued[caller] = false
				if rawPlainDemands[caller] {
					if g.functions[caller].NeedsDispatch {
						contribution := SyncDemand
						if effects[caller].MaySuspend() {
							contribution = AsyncDemand
						}
						joinManagedDemand(caller, contribution)
					}
					for _, edge := range outgoing[caller] {
						if edge.Kind == CallSpawn {
							joinManagedDemand(edge.Callee, AsyncDemand)
						} else {
							joinRawDemand(edge.Callee)
						}
					}
					for _, edge := range referenced[caller] {
						if edge.RawPlain {
							joinRawDemand(edge.Target)
							continue
						}
						contribution := SyncDemand
						if !edge.SyncOnly && effects[edge.Target].MaySuspend() {
							contribution = AsyncDemand
						}
						joinManagedDemand(edge.Target, contribution)
					}
					for _, call := range unknownByCaller[caller] {
						if call.Target != UnknownForeign {
							joinManagedDemand(caller, AsyncDemand)
							break
						}
					}
				}
				if managedDemands[caller] == NoDemand {
					continue
				}
				addOutcome(caller)
				physicalCoroutine := effects[caller].MaySuspend()

				for _, edge := range outgoing[caller] {
					var contribution Demand
					switch edge.Kind {
					case CallSpawn:
						contribution = AsyncDemand
					case CallForeign, CallTrustedInline:
						contribution = SyncDemand
					case CallExplicitStatusElided:
						if physicalCoroutine {
							continue
						}
						// A source panic that remains in a physical plain body
						// invokes the legacy native-stack helper. That is raw
						// reachability, not a managed entry request. If this
						// owner later acquires OutcomeStructured, the edge is
						// physically elided; keeping it in the raw domain avoids
						// an irreversible stale managed SyncDemand.
						joinRawDemand(edge.Callee)
						continue
					case CallUnwind:
						contribution = SyncDemand
						if physicalCoroutine && calleeNeedsOutcomeChild(edge.Callee) {
							contribution = AsyncDemand
						}
					case CallDirect, CallDefer, CallDirectNoUnwind:
						contribution = SyncDemand
						if effects[edge.Callee].MaySuspend() || physicalCoroutine && calleeNeedsOutcomeChild(edge.Callee) {
							contribution = AsyncDemand
						}
					}
					joinManagedDemand(edge.Callee, contribution)
				}
				for _, edge := range referenced[caller] {
					if edge.RawPlain {
						joinRawDemand(edge.Target)
						continue
					}
					contribution := SyncDemand
					if !edge.SyncOnly && (effects[edge.Target].MaySuspend() || physicalCoroutine && calleeNeedsOutcomeChild(edge.Target)) {
						contribution = AsyncDemand
					}
					joinManagedDemand(edge.Target, contribution)
				}
			}

			for effectHead < len(effectQueue) {
				callee := effectQueue[effectHead]
				effectHead++
				effectQueued[callee] = false
				for _, edge := range callers[callee] {
					// A changed callee effect can also change the demand mode
					// selected at the caller's exact call site.
					enqueueDemand(edge.Caller)
					var contribution Effect
					switch edge.Kind {
					case CallDirect, CallDefer, CallDirectNoUnwind:
						contribution = managedCallEffect(effects[callee])
						if localExec[callee].Contains(BlockForeign) {
							contribution = contribution.Join(WaitForeign)
						}
					case CallForeign:
						contribution = WaitForeign
					case CallTrustedInline:
						continue
					case CallSpawn, CallUnwind, CallExplicitStatusElided:
						continue
					}
					next := effects[edge.Caller].Join(contribution)
					if next != effects[edge.Caller] {
						effects[edge.Caller] = next
						enqueueEffect(edge.Caller)
						enqueueDemand(edge.Caller)
					}
				}
				for _, owner := range referencingOwners[callee] {
					enqueueDemand(owner)
				}
			}
		}
	}

	for _, id := range ids {
		spec := g.functions[id]
		rawPlainOnly := spec.External == Defined && rawPlainDemands[id] && managedDemands[id] == NoDemand
		if execFlags[id].Contains(BlockForeign) && effects[id].MaySuspend() && !rawPlainOnly {
			return nil, fmt.Errorf("coro: blocking foreign function %q also has suspend effect %s", id, effects[id])
		}
	}
	for _, edge := range edges {
		if edge.Kind == CallForeign && effects[edge.Callee].MaySuspend() {
			return nil, fmt.Errorf("coro: foreign call target %q has suspend effect %s", edge.Callee, effects[edge.Callee])
		}
	}

	plan := &Plan{
		functions: make([]FunctionPlan, 0, len(ids)),
		byID:      make(map[FunctionID]int, len(ids)),
	}
	for _, id := range ids {
		spec := g.functions[id]
		rawPlainOnly := spec.External == Defined && rawPlainDemands[id] && managedDemands[id] == NoDemand
		rep := DirectPlain
		if effects[id].MaySuspend() && !rawPlainOnly {
			rep = DirectCoro
		}
		if !rawPlainOnly && (spec.NeedsDispatch || spec.External == ExternalUnknownManaged) {
			rep = Dispatch
		}
		primary := PrimaryExternal
		if spec.External == Defined {
			primary = PrimaryPlain
			if effects[id].MaySuspend() && !rawPlainOnly {
				primary = PrimaryCoroutine
			}
		}
		emission := bodyEmissionFor(managedDemands[id], rawPlainDemands[id], effects[id], spec.External)
		plan.byID[id] = len(plan.functions)
		plan.functions = append(plan.functions, FunctionPlan{
			ID:                      id,
			DeclaredEffect:          spec.Seed,
			LocalEffect:             local[id],
			Effect:                  effects[id],
			DeclaredExec:            spec.Exec,
			LocalExec:               localExec[id],
			Exec:                    execFlags[id],
			Demand:                  aggregateDemand(managedDemands[id], rawPlainDemands[id]),
			ManagedDemand:           managedDemands[id],
			RawPlainDemand:          rawPlainDemands[id],
			Emission:                emission,
			FuncRep:                 rep,
			External:                spec.External,
			Recursive:               recursive[id],
			TrustedBoundedRecursion: trustedBoundedRecursion[id],
			Primary:                 primary,
			RawPlainOnly:            rawPlainOnly,
			RawPlainEntry:           spec.RawPlainEntry,
		})
	}
	return plan, nil
}

func (g *Graph) sortedReferences() ([]ReferenceEdge, error) {
	references := make([]ReferenceEdge, 0, len(g.references))
	for _, edge := range g.references {
		references = append(references, edge)
	}
	sort.Slice(references, func(i, j int) bool {
		if references[i].Owner != references[j].Owner {
			return references[i].Owner < references[j].Owner
		}
		if references[i].Target != references[j].Target {
			return references[i].Target < references[j].Target
		}
		if references[i].RawPlain != references[j].RawPlain {
			return !references[i].RawPlain && references[j].RawPlain
		}
		return !references[i].SyncOnly && references[j].SyncOnly
	})
	for _, edge := range references {
		if _, ok := g.functions[edge.Owner]; !ok {
			return nil, fmt.Errorf("coro: reference has unknown owner %q", edge.Owner)
		}
		if _, ok := g.functions[edge.Target]; !ok {
			return nil, fmt.Errorf("coro: reference from %q has unknown target %q", edge.Owner, edge.Target)
		}
	}
	return references, nil
}

func (g *Graph) sortedEdges() ([]CallEdge, error) {
	edges := make([]CallEdge, 0, len(g.edges))
	for _, edge := range g.edges {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Caller != edges[j].Caller {
			return edges[i].Caller < edges[j].Caller
		}
		if edges[i].Callee != edges[j].Callee {
			return edges[i].Callee < edges[j].Callee
		}
		return edges[i].Kind < edges[j].Kind
	})
	for _, edge := range edges {
		if _, ok := g.functions[edge.Caller]; !ok {
			return nil, fmt.Errorf("coro: call has unknown caller %q", edge.Caller)
		}
		if _, ok := g.functions[edge.Callee]; !ok {
			return nil, fmt.Errorf("coro: call from %q has unknown callee %q", edge.Caller, edge.Callee)
		}
	}
	return edges, nil
}

func (g *Graph) sortedUnknownCalls() ([]UnknownCall, error) {
	calls := make([]UnknownCall, 0, len(g.unknown))
	for _, call := range g.unknown {
		calls = append(calls, call)
	}
	sort.Slice(calls, func(i, j int) bool {
		if calls[i].Caller != calls[j].Caller {
			return calls[i].Caller < calls[j].Caller
		}
		if calls[i].Kind != calls[j].Kind {
			return calls[i].Kind < calls[j].Kind
		}
		return calls[i].Target < calls[j].Target
	})
	for _, call := range calls {
		if _, ok := g.functions[call.Caller]; !ok {
			return nil, fmt.Errorf("coro: unknown call has unknown caller %q", call.Caller)
		}
	}
	return calls, nil
}

func sortFunctionIDs(ids []FunctionID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}

// recursiveFunctions computes deterministic strongly connected components over
// effect-propagating managed edges. Spawn and foreign edges do not retain a
// managed call chain and therefore do not make the caller recursive.
func recursiveFunctions(ids []FunctionID, edges []CallEdge) (map[FunctionID]bool, [][]FunctionID) {
	adj := make(map[FunctionID][]FunctionID, len(ids))
	selfEdge := make(map[FunctionID]bool)
	for _, edge := range edges {
		if edge.Kind != CallDirect && edge.Kind != CallDefer {
			continue
		}
		adj[edge.Caller] = append(adj[edge.Caller], edge.Callee)
		if edge.Caller == edge.Callee {
			selfEdge[edge.Caller] = true
		}
	}
	for id := range adj {
		sortFunctionIDs(adj[id])
	}

	index := 0
	indices := make(map[FunctionID]int, len(ids))
	lowlink := make(map[FunctionID]int, len(ids))
	onStack := make(map[FunctionID]bool, len(ids))
	stack := make([]FunctionID, 0, len(ids))
	ret := make(map[FunctionID]bool)
	components := make([][]FunctionID, 0)

	var visit func(FunctionID)
	visit = func(id FunctionID) {
		indices[id] = index
		lowlink[id] = index
		index++
		stack = append(stack, id)
		onStack[id] = true

		for _, callee := range adj[id] {
			calleeIndex, seen := indices[callee]
			if !seen {
				visit(callee)
				if lowlink[callee] < lowlink[id] {
					lowlink[id] = lowlink[callee]
				}
			} else if onStack[callee] && calleeIndex < lowlink[id] {
				lowlink[id] = calleeIndex
			}
		}

		if lowlink[id] != indices[id] {
			return
		}
		component := make([]FunctionID, 0, 1)
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			component = append(component, member)
			if member == id {
				break
			}
		}
		if len(component) > 1 {
			sortFunctionIDs(component)
			for _, member := range component {
				ret[member] = true
			}
			components = append(components, component)
		} else if selfEdge[component[0]] {
			ret[component[0]] = true
			components = append(components, component)
		}
	}

	for _, id := range ids {
		if _, seen := indices[id]; !seen {
			visit(id)
		}
	}
	return ret, components
}
