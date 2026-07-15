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
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// DefaultMaxPlainInstructions is the initial static cost bound used when
// SSAConfig.MaxPlainInstructions is zero. A negative value disables this seed.
const DefaultMaxPlainInstructions = 128

// DynamicResolution selects how AnalyzeSSA resolves interface and function
// value calls. The default avoids a potentially quadratic CHA graph and keeps
// every dynamic call conservatively open.
type DynamicResolution uint8

const (
	// DynamicUnknownOnly emits a conservative unknown edge for every dynamic
	// call and does not use CHA candidates.
	DynamicUnknownOnly DynamicResolution = iota
	// DynamicCHAOpen emits known CHA candidates and retains an unknown edge.
	DynamicCHAOpen
	// DynamicCHAClosed removes the unknown edge only when CHA reports a nonempty
	// candidate set and every candidate remains in the effective program. The
	// caller is responsible for establishing the closed-world assumption.
	DynamicCHAClosed
)

func (r DynamicResolution) validate() error {
	if r > DynamicCHAClosed {
		return fmt.Errorf("coro: invalid dynamic resolution mode %d", uint8(r))
	}
	return nil
}

// Root is an externally established entry demand. Hard synchronous crossings
// use SyncDemand; main, init, and goroutine roots use AsyncDemand. Duplicate
// roots are joined, so the same function may become BothDemand.
type Root struct {
	Function *ssa.Function
	Demand   Demand
}

// Roots is a set of externally established SSA entry demands.
type Roots []Root

// SSAFunctionPolicy adds trusted frontend or imported-summary facts to the
// conservative facts inferred from a function body.
type SSAFunctionPolicy struct {
	Effect Effect
	Exec   ExecFlags

	External         ExternalKind
	OverrideExternal bool
	NeedsDispatch    bool
}

// SSAFunctionResolver maps an SSA function referenced by an effective body to
// the exact canonical function analyzed and emitted by the frontend. ok=false
// means that the reference has no managed target in the effective compilation.
// A successful result must be non-nil, belong to the analyzed Program, and,
// when EmissionUniverse is set, be an exact member of that universe.
//
// AnalyzeSSA memoizes results by input pointer, so the resolver must be pure.
// Frontends use this hook for patch aliases whose unchanged caller bodies still
// refer to the replaced SSA declaration.
type SSAFunctionResolver func(fn *ssa.Function) (canonical *ssa.Function, ok bool, err error)

// SSAConfig controls the SSA-to-Graph analysis bridge. It deliberately has no
// lowering or runtime switches.
type SSAConfig struct {
	FunctionIDs FunctionIDConfig

	// EmissionUniverse restricts analysis to the exact SSA function objects
	// materialized for this frontend compilation. When non-nil, AnalyzeSSA does
	// not add package members, static callees, or CHA nodes outside the universe,
	// and every root must be a universe member. A nil universe preserves the
	// legacy whole-Program enumeration.
	EmissionUniverse *SSAEmissionUniverse

	// ResolveFunction canonicalizes patched or otherwise aliased function
	// pointers before every graph, function-value-flow, CHA, and CallPlan use.
	// Nil is the identity resolver. With EmissionUniverse, successful results
	// must be exact universe members.
	ResolveFunction SSAFunctionResolver

	// MaxPlainInstructions seeds NeedsPreempt on a longer body. Zero selects
	// DefaultMaxPlainInstructions; a negative value disables the cost seed.
	// This is an early heuristic, not the final cross-call MaxAtomicCost proof.
	MaxPlainInstructions int

	// DynamicResolution defaults to DynamicUnknownOnly. AnalyzeSSA's function
	// enumeration may lazily materialize method wrappers in legacy whole-Program
	// mode. With EmissionUniverse, CHA candidate discovery examines only the
	// frozen functions and does not enumerate the Program.
	DynamicResolution DynamicResolution

	// Include filters the effective program (for example, after patch/skip
	// resolution). A static edge to an excluded target becomes an unknown call.
	Include func(*ssa.Function) (bool, error)

	// ClassifyFunction supplies trusted effect, execution, external, and value
	// representation facts. A nil callback leaves bodyless functions as
	// ExternalUnknownManaged; the scanner never guesses C/assembly by name.
	ClassifyFunction func(*ssa.Function) (SSAFunctionPolicy, error)

	// ClassifyUnknownCall distinguishes explicitly known foreign execution
	// domains when static or structural function-value flow cannot completely
	// resolve the managed targets. It does not change the operand ABI or value
	// representation. Exact Go targets use ClassifyFunction instead. The default
	// is UnknownManaged.
	ClassifyUnknownCall func(caller *ssa.Function, call ssa.CallInstruction) (UnknownTarget, error)
}

// SSAFunctionPlan binds an immutable FunctionPlan back to its SSA function.
type SSAFunctionPlan struct {
	Function *ssa.Function
	Plan     FunctionPlan
}

// SSAPlan is the compilation-scoped whole-program result. Its maps remain
// private so consumers cannot reconstruct identities from display strings.
type SSAPlan struct {
	plan       *Plan
	functions  []SSAFunctionPlan
	byFunction map[*ssa.Function]FunctionID
	byID       map[FunctionID]*ssa.Function
	valuePlans map[ssa.Value]SSAValuePlan
	callPlans  map[ssa.CallInstruction]SSACallPlan
}

type ssaFunctionResolution struct {
	canonical *ssa.Function
	ok        bool
	err       error
}

type ssaFunctionCanonicalizer struct {
	prog     *ssa.Program
	universe *SSAEmissionUniverse
	callback SSAFunctionResolver
	memo     map[*ssa.Function]ssaFunctionResolution
	active   map[*ssa.Function]bool
}

func newSSAFunctionCanonicalizer(prog *ssa.Program, config SSAConfig) *ssaFunctionCanonicalizer {
	return &ssaFunctionCanonicalizer{
		prog:     prog,
		universe: config.EmissionUniverse,
		callback: config.ResolveFunction,
		memo:     make(map[*ssa.Function]ssaFunctionResolution),
		active:   make(map[*ssa.Function]bool),
	}
}

func (r *ssaFunctionCanonicalizer) resolve(fn *ssa.Function) (*ssa.Function, bool, error) {
	if fn == nil {
		return nil, false, nil
	}
	if result, ok := r.memo[fn]; ok {
		return result.canonical, result.ok, result.err
	}
	if r.active[fn] {
		return nil, false, fmt.Errorf("resolver cycle at function %q", fn.Name())
	}
	r.active[fn] = true
	defer delete(r.active, fn)

	canonical, ok, err := fn, true, error(nil)
	if r.callback != nil {
		canonical, ok, err = r.callback(fn)
	} else if r.universe != nil && !r.universe.Contains(fn) {
		ok = false
	}
	if err == nil && ok && canonical == nil {
		err = fmt.Errorf("resolver returned a nil canonical function")
	}
	if err == nil && ok && canonical.Prog != r.prog {
		err = fmt.Errorf("resolver returned function %q from another SSA program", canonical.Name())
	}
	if err == nil && r.universe != nil {
		switch {
		case r.universe.Contains(fn) && !ok:
			err = fmt.Errorf("resolver rejected exact emission-universe member %q", fn.Name())
		case r.universe.Contains(fn) && canonical != fn:
			err = fmt.Errorf("resolver remapped exact emission-universe member %q", fn.Name())
		case ok && !r.universe.Contains(canonical):
			err = fmt.Errorf("resolver returned function %q outside the SSA emission universe", canonical.Name())
		}
	}
	if err == nil && ok && r.callback != nil && canonical != fn {
		// The callback contract requires a final canonical pointer. Verify that
		// successful aliases do not form chains or depend on the call site.
		final, finalOK, finalErr := r.resolve(canonical)
		switch {
		case finalErr != nil:
			err = fmt.Errorf("verify canonical function %q: %w", canonical.Name(), finalErr)
		case !finalOK || final == nil:
			err = fmt.Errorf("canonical function %q does not resolve to itself", canonical.Name())
		case final != canonical:
			err = fmt.Errorf("canonical function %q resolves to a different function", canonical.Name())
		default:
			r.memo[canonical] = ssaFunctionResolution{canonical: canonical, ok: true}
		}
	}
	result := ssaFunctionResolution{canonical: canonical, ok: ok, err: err}
	r.memo[fn] = result
	return result.canonical, result.ok, result.err
}

// BasePlan returns the target-independent immutable fixed-point plan.
func (p *SSAPlan) BasePlan() *Plan {
	if p == nil {
		return nil
	}
	return p.plan
}

// Functions returns SSA/function-plan pairs in FunctionID order.
func (p *SSAPlan) Functions() []SSAFunctionPlan {
	if p == nil {
		return nil
	}
	return append([]SSAFunctionPlan(nil), p.functions...)
}

// FunctionID returns the stable identity assigned to fn.
func (p *SSAPlan) FunctionID(fn *ssa.Function) (FunctionID, bool) {
	if p == nil {
		return "", false
	}
	id, ok := p.byFunction[fn]
	return id, ok
}

// FunctionPlan returns the immutable plan assigned to the exact SSA function
// object fn. It does not derive or match an identity for a function from a
// different SSA program.
func (p *SSAPlan) FunctionPlan(fn *ssa.Function) (FunctionPlan, bool) {
	if p == nil {
		return FunctionPlan{}, false
	}
	id, ok := p.byFunction[fn]
	if !ok {
		return FunctionPlan{}, false
	}
	return p.plan.Lookup(id)
}

// Function returns the SSA function assigned to id.
func (p *SSAPlan) Function(id FunctionID) (*ssa.Function, bool) {
	if p == nil {
		return nil, false
	}
	fn, ok := p.byID[id]
	return fn, ok
}

// AnalyzeSSA scans a built x/tools SSA program, constructs a conservative
// target-independent Graph, and computes its least fixed point. It emits and
// updates no build, LLVM, cache, archive, or runtime artifacts. x/tools function
// enumeration and opt-in CHA may materialize lazy wrapper objects in prog when
// EmissionUniverse is nil.
func AnalyzeSSA(prog *ssa.Program, roots Roots, config SSAConfig) (*SSAPlan, error) {
	if prog == nil {
		return nil, fmt.Errorf("coro: analyze nil SSA program")
	}
	universe := config.EmissionUniverse
	if universe != nil && universe.Program() != prog {
		return nil, fmt.Errorf("coro: SSA emission universe belongs to another program")
	}
	canonicalizer := newSSAFunctionCanonicalizer(prog, config)
	identityConfig, err := config.FunctionIDs.normalized()
	if err != nil {
		return nil, err
	}
	config.FunctionIDs = identityConfig
	if err := config.DynamicResolution.validate(); err != nil {
		return nil, err
	}
	maxPlain := config.MaxPlainInstructions
	if maxPlain == 0 {
		maxPlain = DefaultMaxPlainInstructions
	}

	rootDemand := make(map[*ssa.Function]Demand, len(roots))
	for i, root := range roots {
		if root.Function == nil {
			return nil, fmt.Errorf("coro: root %d has nil SSA function", i)
		}
		if root.Function.Prog != prog {
			return nil, fmt.Errorf("coro: root %d function %q belongs to another SSA program", i, root.Function.Name())
		}
		canonical, ok, err := canonicalizer.resolve(root.Function)
		if err != nil {
			return nil, fmt.Errorf("coro: resolve root %d function %q: %w", i, root.Function.Name(), err)
		}
		if !ok {
			if universe != nil {
				return nil, fmt.Errorf("coro: root %d function %q is absent from the SSA emission universe", i, root.Function.Name())
			}
			return nil, fmt.Errorf("coro: root %d function %q has no canonical managed target", i, root.Function.Name())
		}
		if err := root.Demand.Validate(); err != nil {
			return nil, fmt.Errorf("coro: root %d function %q: %w", i, root.Function.Name(), err)
		}
		if root.Demand == NoDemand {
			return nil, fmt.Errorf("coro: root %d function %q has no demand", i, root.Function.Name())
		}
		rootDemand[canonical] = rootDemand[canonical].Join(root.Demand)
	}

	dynamicCandidates := make(map[ssa.CallInstruction]map[*ssa.Function]struct{})
	functionSet := make(map[*ssa.Function]struct{})
	var allFunctions []*ssa.Function
	if universe != nil {
		// Preserve the frozen frontend order. Round-tripping through a map
		// makes equal raw presentation keys nondeterministic (notably distinct
		// generic instances over local named types) before structural IDs are
		// available to order the included functions.
		allFunctions = append([]*ssa.Function(nil), universe.functions...)
		if config.DynamicResolution != DynamicUnknownOnly {
			dynamicCandidates = restrictedSSACHACandidates(universe.functions)
		}
	} else if config.DynamicResolution == DynamicUnknownOnly {
		for fn := range ssautil.AllFunctions(prog) {
			if fn != nil && fn.Prog == prog {
				functionSet[fn] = struct{}{}
			}
		}
	} else {
		callGraph := cha.CallGraph(prog)
		for fn, node := range callGraph.Nodes {
			if fn == nil || fn.Prog != prog || node == nil {
				continue
			}
			functionSet[fn] = struct{}{}
			for _, edge := range node.Out {
				if edge.Site == nil || edge.Callee == nil || edge.Callee.Func == nil {
					continue
				}
				if edge.Site.Common().StaticCallee() != nil {
					continue
				}
				candidates := dynamicCandidates[edge.Site]
				if candidates == nil {
					candidates = make(map[*ssa.Function]struct{})
					dynamicCandidates[edge.Site] = candidates
				}
				candidates[edge.Callee.Func] = struct{}{}
				if edge.Callee.Func.Prog == prog {
					functionSet[edge.Callee.Func] = struct{}{}
				}
			}
		}
	}
	if universe == nil {
		for _, pkg := range prog.AllPackages() {
			for _, member := range pkg.Members {
				if fn, ok := member.(*ssa.Function); ok {
					functionSet[fn] = struct{}{}
				}
			}
		}
		for fn := range rootDemand {
			functionSet[fn] = struct{}{}
		}
		closeStaticFunctions(functionSet, prog)
	}

	if universe == nil {
		type keyedFunction struct {
			function *ssa.Function
			key      string
		}
		keyedFunctions := make([]keyedFunction, 0, len(functionSet))
		for fn := range functionSet {
			keyedFunctions = append(keyedFunctions, keyedFunction{
				function: fn,
				key:      rawSSAFunctionKey(fn),
			})
		}
		sort.Slice(keyedFunctions, func(i, j int) bool {
			return keyedFunctions[i].key < keyedFunctions[j].key
		})
		allFunctions = make([]*ssa.Function, len(keyedFunctions))
		for i, keyed := range keyedFunctions {
			allFunctions[i] = keyed.function
		}
	}

	canonicalFunctions := make([]*ssa.Function, 0, len(allFunctions))
	seenCanonical := make(map[*ssa.Function]struct{}, len(allFunctions))
	for _, fn := range allFunctions {
		canonical, ok, resolveErr := canonicalizer.resolve(fn)
		if resolveErr != nil {
			return nil, fmt.Errorf("coro: resolve enumerated SSA function %q: %w", fn.Name(), resolveErr)
		}
		if !ok {
			continue
		}
		if _, seen := seenCanonical[canonical]; seen {
			continue
		}
		seenCanonical[canonical] = struct{}{}
		canonicalFunctions = append(canonicalFunctions, canonical)
	}
	if universe == nil {
		canonicalFunctions, err = closeCanonicalStaticFunctions(canonicalFunctions, prog, canonicalizer)
		if err != nil {
			return nil, err
		}
	}
	allFunctions = canonicalFunctions
	dynamicCandidates, err = canonicalizeSSADynamicCandidates(dynamicCandidates, canonicalizer)
	if err != nil {
		return nil, err
	}

	included := make([]*ssa.Function, 0, len(allFunctions))
	includedSet := make(map[*ssa.Function]bool, len(allFunctions))
	for _, fn := range allFunctions {
		keep := !isUninstantiatedGeneric(fn)
		if config.Include != nil {
			requested, includeErr := config.Include(fn)
			err = includeErr
			if err != nil {
				return nil, fmt.Errorf("coro: include SSA function %q: %w", fn.Name(), err)
			}
			keep = keep && requested
		}
		if keep {
			included = append(included, fn)
			includedSet[fn] = true
		}
	}
	for fn := range rootDemand {
		if !includedSet[fn] {
			return nil, fmt.Errorf("coro: root function %q is excluded", fn.Name())
		}
	}

	ids := make(map[*ssa.Function]FunctionID, len(included))
	byID := make(map[FunctionID]*ssa.Function, len(included))
	idBuilder := functionIDBuilder{config: config.FunctionIDs, localTypeCandidates: allFunctions}
	for _, fn := range included {
		id, err := idBuilder.stableFunctionID(fn)
		if err != nil {
			return nil, fmt.Errorf("coro: identify SSA function %q: %w", fn.Name(), err)
		}
		if previous, exists := byID[id]; exists && previous != fn {
			return nil, fmt.Errorf("coro: FunctionID collision between %q and %q", previous.Name(), fn.Name())
		}
		ids[fn] = id
		byID[id] = fn
	}
	sort.Slice(included, func(i, j int) bool { return ids[included[i]] < ids[included[j]] })

	flow, err := analyzeSSAFunctionFlow(included, includedSet, ids, dynamicCandidates, config.DynamicResolution, canonicalizer)
	if err != nil {
		return nil, fmt.Errorf("coro: analyze SSA function-value flow: %w", err)
	}
	unknownTargets, err := classifySSAUnknownCalls(included, includedSet, flow, config)
	if err != nil {
		return nil, err
	}
	policies := make(map[*ssa.Function]SSAFunctionPolicy, len(included))
	needsDispatch := flow.descriptorTargets(unknownTargets)
	for _, fn := range included {
		policy := SSAFunctionPolicy{}
		if fn.Blocks == nil {
			policy.External = ExternalUnknownManaged
			policy.OverrideExternal = true
		}
		bodyEffect, bodyExec := scanSSAFunctionBody(fn, maxPlain)
		policy.Effect = policy.Effect.Join(bodyEffect)
		policy.Exec = policy.Exec.Join(bodyExec)
		if config.ClassifyFunction != nil {
			trusted, err := config.ClassifyFunction(fn)
			if err != nil {
				return nil, fmt.Errorf("coro: classify SSA function %q: %w", fn.Name(), err)
			}
			policy.Effect = policy.Effect.Join(trusted.Effect)
			policy.Exec = policy.Exec.Join(trusted.Exec)
			policy.NeedsDispatch = policy.NeedsDispatch || trusted.NeedsDispatch
			if trusted.OverrideExternal {
				policy.External = trusted.External
				policy.OverrideExternal = true
			}
		}
		policy.NeedsDispatch = policy.NeedsDispatch || needsDispatch[fn]
		if !policy.OverrideExternal {
			policy.External = Defined
		}
		if fn.Blocks == nil && policy.External == Defined {
			return nil, fmt.Errorf("coro: bodyless SSA function %q classified as defined", fn.Name())
		}
		policies[fn] = policy
	}

	graph := NewGraph()
	for _, fn := range included {
		policy := policies[fn]
		if err := graph.AddFunction(FunctionSpec{
			ID:            ids[fn],
			Seed:          policy.Effect,
			Exec:          policy.Exec,
			Demand:        rootDemand[fn],
			External:      policy.External,
			NeedsDispatch: policy.NeedsDispatch,
		}); err != nil {
			return nil, err
		}
	}

	callKinds := make(map[ssa.CallInstruction]CallKind)
	for _, caller := range included {
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				common := call.Common()
				if _, builtin := common.Value.(*ssa.Builtin); builtin {
					continue
				}
				kind := ssaCallKind(call)
				if rawCallee := common.StaticCallee(); rawCallee != nil {
					callee, resolved, resolveErr := flow.resolveTarget(rawCallee)
					if resolveErr != nil {
						return nil, fmt.Errorf("coro: resolve static callee %q in %q while building graph: %w", rawCallee.Name(), caller.Name(), resolveErr)
					}
					if resolved && includedSet[callee] {
						edgeKind := staticCallKind(kind, policies[callee])
						callKinds[call] = edgeKind
						if err := graph.AddCall(CallEdge{Caller: ids[caller], Callee: ids[callee], Kind: edgeKind}); err != nil {
							return nil, err
						}
					} else {
						target := unknownTargets[call]
						callKinds[call] = unknownCallKind(kind, target)
						if err := addSSAUnknownCall(graph, ids[caller], kind, target); err != nil {
							return nil, err
						}
					}
					continue
				}

				flowTargets, flowComplete := flow.scalarCallTargets(call)
				if flowComplete {
					candidates := sortedSSACandidates(flowTargets, ids, includedSet)
					callKinds[call] = kind
					for _, callee := range candidates {
						edgeKind := staticCallKind(kind, policies[callee])
						if len(candidates) == 1 {
							callKinds[call] = edgeKind
						}
						if err := graph.AddCall(CallEdge{Caller: ids[caller], Callee: ids[callee], Kind: edgeKind}); err != nil {
							return nil, err
						}
					}
					continue
				}
				// Structural flow may know a strict subset even when another source
				// remains unresolved. Preserve those real Go edges regardless of the
				// fallback execution domain selected below.
				for _, callee := range sortedSSACandidates(flowTargets, ids, includedSet) {
					edgeKind := staticCallKind(kind, policies[callee])
					if err := graph.AddCall(CallEdge{Caller: ids[caller], Callee: ids[callee], Kind: edgeKind}); err != nil {
						return nil, err
					}
				}

				target := unknownTargets[call]
				// An explicitly classified foreign function value has a different
				// invocation domain from CHA's managed Go candidates. Preserve the
				// foreign boundary in every resolution mode.
				if target == UnknownForeign {
					callKinds[call] = kind
					// A mixed dispatch keeps the syntax kind for known managed
					// descriptors; Unresolved selects the foreign fallback only.
					if len(flowTargets) == 0 {
						callKinds[call] = unknownCallKind(kind, target)
					}
					if err := addSSAUnknownCall(graph, ids[caller], kind, target); err != nil {
						return nil, err
					}
					continue
				}
				callKinds[call] = kind

				rawCandidates := dynamicCandidates[call]
				candidates := sortedSSACandidates(rawCandidates, ids, includedSet)
				for _, callee := range candidates {
					edgeKind := staticCallKind(kind, policies[callee])
					if err := graph.AddCall(CallEdge{Caller: ids[caller], Callee: ids[callee], Kind: edgeKind}); err != nil {
						return nil, err
					}
				}
				closedWorldResolved := config.DynamicResolution == DynamicCHAClosed && len(rawCandidates) != 0
				if closedWorldResolved {
					for candidate := range rawCandidates {
						if !includedSet[candidate] {
							closedWorldResolved = false
							break
						}
					}
				}
				if !closedWorldResolved {
					if err := addSSAUnknownCall(graph, ids[caller], kind, target); err != nil {
						return nil, err
					}
				}
			}
		}
	}

	base, err := graph.Analyze()
	if err != nil {
		return nil, err
	}
	valuePlans, callPlans, err := flow.finalize(base, callKinds, unknownTargets)
	if err != nil {
		return nil, fmt.Errorf("coro: finalize SSA value and call plans: %w", err)
	}
	result := &SSAPlan{
		plan:       base,
		functions:  make([]SSAFunctionPlan, 0, len(included)),
		byFunction: ids,
		byID:       byID,
		valuePlans: valuePlans,
		callPlans:  callPlans,
	}
	for _, functionPlan := range base.Functions() {
		result.functions = append(result.functions, SSAFunctionPlan{
			Function: byID[functionPlan.ID],
			Plan:     functionPlan,
		})
	}
	return result, nil
}

func classifySSAUnknownCalls(
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	flow *ssaFuncFlow,
	config SSAConfig,
) (map[ssa.CallInstruction]UnknownTarget, error) {
	result := make(map[ssa.CallInstruction]UnknownTarget)
	for _, caller := range functions {
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				common := call.Common()
				if _, builtin := common.Value.(*ssa.Builtin); builtin {
					continue
				}
				if callee := common.StaticCallee(); callee != nil {
					canonical, ok, err := flow.resolveTarget(callee)
					if err != nil {
						return nil, fmt.Errorf("coro: resolve static callee %q in %q while classifying unknown calls: %w", callee.Name(), caller.Name(), err)
					}
					if ok && included[canonical] {
						continue
					}
				}
				if _, complete := flow.scalarCallTargets(call); complete {
					continue
				}
				target, err := classifyUnknownCall(config, caller, call)
				if err != nil {
					return nil, err
				}
				result[call] = target
			}
		}
	}
	return result, nil
}

func canonicalizeSSADynamicCandidates(
	candidates map[ssa.CallInstruction]map[*ssa.Function]struct{},
	canonicalizer *ssaFunctionCanonicalizer,
) (map[ssa.CallInstruction]map[*ssa.Function]struct{}, error) {
	result := make(map[ssa.CallInstruction]map[*ssa.Function]struct{}, len(candidates))
	for call, rawTargets := range candidates {
		canonicalTargets := make(map[*ssa.Function]struct{}, len(rawTargets))
		for raw := range rawTargets {
			canonical, ok, err := canonicalizer.resolve(raw)
			if err != nil {
				caller := "<unknown>"
				if call != nil && call.Parent() != nil {
					caller = call.Parent().Name()
				}
				return nil, fmt.Errorf("coro: resolve dynamic candidate %q for call in %q: %w", raw.Name(), caller, err)
			}
			if ok {
				canonicalTargets[canonical] = struct{}{}
			} else {
				// Retain an excluded sentinel. Dropping it would let CHAClosed
				// incorrectly treat a partially unresolved candidate set as closed.
				canonicalTargets[raw] = struct{}{}
			}
		}
		if len(canonicalTargets) != 0 {
			result[call] = canonicalTargets
		}
	}
	return result, nil
}

func closeCanonicalStaticFunctions(
	functions []*ssa.Function,
	prog *ssa.Program,
	canonicalizer *ssaFunctionCanonicalizer,
) ([]*ssa.Function, error) {
	result := append([]*ssa.Function(nil), functions...)
	seen := make(map[*ssa.Function]struct{}, len(result))
	for _, fn := range result {
		seen[fn] = struct{}{}
	}
	add := func(raw, caller *ssa.Function) error {
		if raw == nil || raw.Prog != prog {
			return nil
		}
		canonical, ok, err := canonicalizer.resolve(raw)
		if err != nil {
			return fmt.Errorf("coro: resolve static function %q reached from %q: %w", raw.Name(), caller.Name(), err)
		}
		if !ok {
			return nil
		}
		if _, exists := seen[canonical]; !exists {
			seen[canonical] = struct{}{}
			result = append(result, canonical)
		}
		return nil
	}
	for head := 0; head < len(result); head++ {
		fn := result[head]
		for _, child := range fn.AnonFuncs {
			if err := add(child, fn); err != nil {
				return nil, err
			}
		}
		operands := make([]*ssa.Value, 0, 8)
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				operands = instruction.Operands(operands[:0])
				for _, operand := range operands {
					if operand == nil {
						continue
					}
					if target, ok := (*operand).(*ssa.Function); ok {
						if err := add(target, fn); err != nil {
							return nil, err
						}
					}
				}
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				if err := add(call.Common().StaticCallee(), fn); err != nil {
					return nil, err
				}
			}
		}
	}
	return result, nil
}

func closeStaticFunctions(functions map[*ssa.Function]struct{}, prog *ssa.Program) {
	queue := make([]*ssa.Function, 0, len(functions))
	for fn := range functions {
		queue = append(queue, fn)
	}
	for head := 0; head < len(queue); head++ {
		fn := queue[head]
		for _, child := range fn.AnonFuncs {
			if child != nil && child.Prog == prog {
				if _, ok := functions[child]; !ok {
					functions[child] = struct{}{}
					queue = append(queue, child)
				}
			}
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				callee := call.Common().StaticCallee()
				if callee != nil && callee.Prog == prog {
					if _, ok := functions[callee]; !ok {
						functions[callee] = struct{}{}
						queue = append(queue, callee)
					}
				}
			}
		}
	}
}

func rawSSAFunctionKey(fn *ssa.Function) string {
	pkgPath := ""
	if fn.Pkg != nil && fn.Pkg.Pkg != nil {
		pkgPath = fn.Pkg.Pkg.Path()
	} else if obj := fn.Object(); obj != nil && obj.Pkg() != nil {
		pkgPath = obj.Pkg().Path()
	}
	objectID := ""
	if obj := fn.Object(); obj != nil {
		objectID = obj.Id()
	}
	signature := ""
	if fn.Signature != nil {
		signature = types.TypeString(fn.Signature, func(pkg *types.Package) string {
			if pkg == nil {
				return ""
			}
			return pkg.Path()
		})
	}
	var typeArgs strings.Builder
	for _, arg := range fn.TypeArgs() {
		appendIdentityField(&typeArgs, "arg", types.TypeString(arg, func(pkg *types.Package) string {
			if pkg == nil {
				return ""
			}
			return pkg.Path()
		}))
	}
	parent := ""
	if fn.Parent() != nil {
		parent = fn.Parent().Name() + fmt.Sprintf("@%020d", int(fn.Parent().Pos()))
	}
	return fmt.Sprintf("%s\x00%s\x00%020d\x00%s\x00%s\x00%s\x00%s\x00%s",
		pkgPath, fn.Name(), int(fn.Pos()), fn.Synthetic, objectID, signature, typeArgs.String(), parent)
}

func isUninstantiatedGeneric(fn *ssa.Function) bool {
	params := fn.TypeParams()
	return params != nil && params.Len() != 0 && len(fn.TypeArgs()) == 0
}

func scanSSAFunctionBody(fn *ssa.Function, maxPlain int) (Effect, ExecFlags) {
	if fn == nil || fn.Blocks == nil {
		return NoSuspend, 0
	}
	effect := NoSuspend
	// SSA operations may panic implicitly (bounds, nil dereference, division,
	// type assertion, send/close, allocation, and more). Until a complete
	// no-unwind proof exists, every defined body conservatively carries this
	// independent execution flag. It does not itself force coroutine lowering.
	exec := MayUnwind
	instructions := 0
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			if _, debug := instruction.(*ssa.DebugRef); debug {
				continue
			}
			instructions++
			switch instruction := instruction.(type) {
			case *ssa.Send:
				effect = effect.Join(MayPark)
			case *ssa.UnOp:
				if instruction.Op == token.ARROW {
					effect = effect.Join(MayPark)
				}
			case *ssa.Select:
				if instruction.Blocking {
					effect = effect.Join(MayPark)
				}
			case *ssa.Defer, *ssa.RunDefers:
				exec = exec.Join(NeedsCleanupFrame)
			case *ssa.Panic:
				exec = exec.Join(MayUnwind)
			case *ssa.Call:
				if builtin, ok := instruction.Common().Value.(*ssa.Builtin); ok && builtin.Name() == "panic" {
					exec = exec.Join(MayUnwind)
				}
			}
		}
	}
	if cfgHasCycle(fn.Blocks) || maxPlain >= 0 && instructions > maxPlain {
		exec = exec.Join(NeedsPreempt)
	}
	return effect, exec
}

func cfgHasCycle(blocks []*ssa.BasicBlock) bool {
	if len(blocks) == 0 {
		return false
	}
	indegree := make([]int, len(blocks))
	for _, block := range blocks {
		for _, successor := range block.Succs {
			indegree[successor.Index]++
		}
	}
	queue := make([]*ssa.BasicBlock, 0, len(blocks))
	for _, block := range blocks {
		if indegree[block.Index] == 0 {
			queue = append(queue, block)
		}
	}
	visited := 0
	for head := 0; head < len(queue); head++ {
		block := queue[head]
		visited++
		for _, successor := range block.Succs {
			indegree[successor.Index]--
			if indegree[successor.Index] == 0 {
				queue = append(queue, successor)
			}
		}
	}
	return visited != len(blocks)
}

func ssaCallKind(call ssa.CallInstruction) CallKind {
	switch call.(type) {
	case *ssa.Go:
		return CallSpawn
	case *ssa.Defer:
		return CallDefer
	default:
		return CallDirect
	}
}

func staticCallKind(syntax CallKind, policy SSAFunctionPolicy) CallKind {
	if syntax == CallDirect && (policy.External == ExternalUnknownForeign || policy.Exec.Contains(BlockForeign)) {
		return CallForeign
	}
	return syntax
}

func classifyUnknownCall(config SSAConfig, caller *ssa.Function, call ssa.CallInstruction) (UnknownTarget, error) {
	target := UnknownManaged
	if config.ClassifyUnknownCall != nil {
		var err error
		target, err = config.ClassifyUnknownCall(caller, call)
		if err != nil {
			return 0, fmt.Errorf("coro: classify unknown call in %q: %w", caller.Name(), err)
		}
	}
	if err := target.validate(); err != nil {
		return 0, fmt.Errorf("coro: unknown call in %q: %w", caller.Name(), err)
	}
	return target, nil
}

func addSSAUnknownCall(graph *Graph, caller FunctionID, syntax CallKind, target UnknownTarget) error {
	kind := unknownCallKind(syntax, target)
	return graph.AddUnknownCall(UnknownCall{Caller: caller, Kind: kind, Target: target})
}

func unknownCallKind(syntax CallKind, target UnknownTarget) CallKind {
	if syntax == CallDirect && target == UnknownForeign {
		return CallForeign
	}
	return syntax
}

func sortedSSACandidates(candidates map[*ssa.Function]struct{}, ids map[*ssa.Function]FunctionID, included map[*ssa.Function]bool) []*ssa.Function {
	result := make([]*ssa.Function, 0, len(candidates))
	for candidate := range candidates {
		if included[candidate] {
			result = append(result, candidate)
		}
	}
	sort.Slice(result, func(i, j int) bool { return ids[result[i]] < ids[result[j]] })
	return result
}
