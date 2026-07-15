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
)

func (k CallKind) validate() error {
	if k > CallForeign {
		return fmt.Errorf("coro: invalid call kind %d", uint8(k))
	}
	return nil
}

// CallEdge is a statically resolved call graph edge.
type CallEdge struct {
	Caller FunctionID
	Callee FunctionID
	Kind   CallKind
}

// UnknownTarget describes an unresolved call target.
type UnknownTarget uint8

const (
	// UnknownManaged conservatively contributes OpaqueSuspend.
	UnknownManaged UnknownTarget = iota
	// UnknownForeign conservatively contributes WaitForeign.
	UnknownForeign
)

func (k UnknownTarget) validate() error {
	if k > UnknownForeign {
		return fmt.Errorf("coro: invalid unknown target kind %d", uint8(k))
	}
	return nil
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

type unknownKey struct {
	caller FunctionID
	kind   CallKind
	target UnknownTarget
}

// Graph is a target-independent function call graph.
type Graph struct {
	functions map[FunctionID]FunctionSpec
	edges     map[edgeKey]CallEdge
	unknown   map[unknownKey]UnknownCall
}

// NewGraph creates an empty call graph.
func NewGraph() *Graph {
	return &Graph{
		functions: make(map[FunctionID]FunctionSpec),
		edges:     make(map[edgeKey]CallEdge),
		unknown:   make(map[unknownKey]UnknownCall),
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
	if err := spec.External.validate(); err != nil {
		return fmt.Errorf("coro: function %q: %w", spec.ID, err)
	}
	if _, exists := g.functions[spec.ID]; exists {
		return fmt.Errorf("coro: duplicate function %q", spec.ID)
	}
	spec.Seed = spec.Seed.Normalize()
	g.functions[spec.ID] = spec
	return nil
}

// AddCall adds a statically resolved call edge. Duplicate edges are ignored.
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
	key := edgeKey{caller: edge.Caller, callee: edge.Callee, kind: edge.Kind}
	g.edges[key] = edge
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
	if err := call.Target.validate(); err != nil {
		return err
	}
	key := unknownKey{caller: call.Caller, kind: call.Kind, target: call.Target}
	g.unknown[key] = call
	return nil
}

// Analyze computes the least suspend-effect fixed point. Traversal and output
// are deterministic regardless of graph insertion order.
func (g *Graph) Analyze() (*Plan, error) {
	if g == nil {
		return nil, fmt.Errorf("coro: analyze nil graph")
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
	unknown, err := g.sortedUnknownCalls()
	if err != nil {
		return nil, err
	}
	recursive := recursiveFunctions(ids, edges)

	local := make(map[FunctionID]Effect, len(ids))
	effects := make(map[FunctionID]Effect, len(ids))
	localExec := make(map[FunctionID]ExecFlags, len(ids))
	execFlags := make(map[FunctionID]ExecFlags, len(ids))
	demands := make(map[FunctionID]Demand, len(ids))
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
		if recursive[id] {
			effect = effect.Join(YieldOnly)
			exec = exec.Join(NeedsPreempt)
		}
		local[id] = effect
		effects[id] = effect
		localExec[id] = exec
		execFlags[id] = exec
		demands[id] = spec.Demand
	}

	for _, call := range unknown {
		if call.Kind == CallSpawn {
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
			case CallDirect, CallDefer:
				effectContribution = managedCallEffect(effects[callee])
				if localExec[callee].Contains(BlockForeign) {
					effectContribution = effectContribution.Join(WaitForeign)
				}
				execContribution = execFlags[callee] & propagatedExecFlags
			case CallSpawn:
				continue
			case CallForeign:
				effectContribution = WaitForeign
				execContribution = execFlags[callee] & propagatedExecFlags
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
	for _, id := range ids {
		if execFlags[id].Contains(BlockForeign) && effects[id].MaySuspend() {
			return nil, fmt.Errorf("coro: blocking foreign function %q also has suspend effect %s", id, effects[id])
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
	queue = queue[:0]
	clear(queued)
	for _, id := range ids {
		if demands[id] != NoDemand {
			queue = append(queue, id)
			queued[id] = true
		}
	}
	for head := 0; head < len(queue); head++ {
		caller := queue[head]
		queued[caller] = false
		for _, edge := range outgoing[caller] {
			var contribution Demand
			switch edge.Kind {
			case CallSpawn:
				contribution = AsyncDemand
			case CallForeign:
				contribution = SyncDemand
			case CallDirect, CallDefer:
				contribution = SyncDemand
				if effects[edge.Callee].MaySuspend() {
					contribution = AsyncDemand
				}
			}
			next := demands[edge.Callee].Join(contribution)
			if next != demands[edge.Callee] {
				demands[edge.Callee] = next
				if !queued[edge.Callee] {
					queue = append(queue, edge.Callee)
					queued[edge.Callee] = true
				}
			}
		}
	}

	plan := &Plan{
		functions: make([]FunctionPlan, 0, len(ids)),
		byID:      make(map[FunctionID]int, len(ids)),
	}
	for _, id := range ids {
		spec := g.functions[id]
		rep := DirectPlain
		if effects[id].MaySuspend() {
			rep = DirectCoro
		}
		if spec.NeedsDispatch || spec.External == ExternalUnknownManaged {
			rep = Dispatch
		}
		primary := PrimaryExternal
		if spec.External == Defined {
			primary = PrimaryPlain
			if effects[id].MaySuspend() {
				primary = PrimaryCoroutine
			}
		}
		plan.byID[id] = len(plan.functions)
		plan.functions = append(plan.functions, FunctionPlan{
			ID:             id,
			DeclaredEffect: spec.Seed,
			LocalEffect:    local[id],
			Effect:         effects[id],
			DeclaredExec:   spec.Exec,
			LocalExec:      localExec[id],
			Exec:           execFlags[id],
			Demand:         demands[id],
			FuncRep:        rep,
			External:       spec.External,
			Recursive:      recursive[id],
			Primary:        primary,
		})
	}
	return plan, nil
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
func recursiveFunctions(ids []FunctionID, edges []CallEdge) map[FunctionID]bool {
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
			for _, member := range component {
				ret[member] = true
			}
		} else if selfEdge[component[0]] {
			ret[component[0]] = true
		}
	}

	for _, id := range ids {
		if _, seen := indices[id]; !seen {
			visit(id)
		}
	}
	return ret
}
