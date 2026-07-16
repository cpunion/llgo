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
	// IgnoreBody states that the frontend does not emit this SSA body's Go
	// instructions because the function is an external declaration in the
	// frozen physical ABI. AnalyzeSSA excludes that body from value flow, calls,
	// references, recursion, local-body identity, Call/Value plans, and digest
	// sites. The same trusted policy must explicitly override External to a
	// non-Defined kind.
	IgnoreBody bool
	// TrustedNoPreempt clears only the scanner's local CFG/instruction-budget
	// NeedsPreempt seed. It is reserved for bounded compiler/runtime islands
	// that execute on the scheduler stack and therefore must retain a plain ABI.
	// It does not clear recursion, suspend effects, or any other execution flag.
	TrustedNoPreempt bool

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

// SSAClosedDynamicCallCertificate is a trusted frontend proof for one exact
// ordinary dynamic call. V0 intentionally accepts at most one non-nil target:
// the narrow form is sufficient for fields whose whole-program writes are
// proven to contain either nil or one descriptor-backed function value.
//
// Targets is copied and validated before analysis. An empty Targets slice is a
// closed nil-only value and therefore requires MayBeNil. A singleton may be
// either nullable or non-null. The target must be an exact canonical, owned Go
// body in the effective emission universe with the call's exact signature.
type SSAClosedDynamicCallCertificate struct {
	Targets  []*ssa.Function
	MayBeNil bool
}

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

	// ClassifyElidedCall identifies a direct static call for which the frontend
	// emits no callable function edge: either the call is omitted entirely or a
	// proven no-suspend compiler intrinsic is lowered inline in the caller. Such
	// a site contributes no graph edge and has no CallPlan, but remains in the
	// plan/digest. The callback is trusted frontend policy, not an effect
	// summary: AnalyzeSSA rejects attempts to elide go, defer, or dynamic calls.
	// Argument-producing SSA instructions remain analyzed independently.
	ClassifyElidedCall func(caller *ssa.Function, call ssa.CallInstruction) (bool, error)

	// ClassifyDirectPlainCallArgument identifies one exact static-call argument
	// use whose frontend ABI is a synchronously invoked raw function pointer
	// rather than a Go closure/dispatch value. The exemption applies only to
	// that (call, argument-index) boundary: any store, interface conversion,
	// ordinary Go argument, open flow, or multi-target flow in the same value
	// component still requires Dispatch and makes the trusted claim fail closed.
	// The callback must not classify go, defer, dynamic, builtin, or non-function
	// arguments. Frontends should reserve it for source-level ABI facts such as
	// a named //llgo:type C callback parameter.
	ClassifyDirectPlainCallArgument func(caller *ssa.Function, call ssa.CallInstruction, argument int) (bool, error)

	// ClassifyClosedDynamicCall supplies a frozen whole-program proof for one
	// exact ordinary dynamic *ssa.Call whose callee value crosses descriptor
	// storage but has a closed nil-or-singleton target set. This is not a general
	// points-to hint: AnalyzeSSA rejects static calls, invokes, go/defer sites,
	// multiple targets, captured functions, signature mismatches, aliases,
	// external declarations, and targets outside the effective universe.
	//
	// A certified callee remains Dispatch because it crossed canonical storage;
	// the certificate only closes its graph edge and CallPlan target set. The
	// callback is trusted to have rejected every unknown physical write or escape
	// that could reach the exact value loaded at call.
	ClassifyClosedDynamicCall func(caller *ssa.Function, call ssa.CallInstruction) (SSAClosedDynamicCallCertificate, bool, error)

	// ClassifyDemandReferences supplies exact function addresses that the
	// frontend implicitly embeds while lowering one function body, even though
	// they are not operands in that body's SSA instructions. Runtime ABI method
	// tables are the canonical example. These references propagate entry demand
	// only; they do not propagate suspend effects or execution flags.
	//
	// Every returned target must be a non-nil exact canonical member of the
	// effective emission universe. AnalyzeSSA calls the classifier only for
	// owned, non-ignored bodies and copies the returned slice before use.
	ClassifyDemandReferences func(owner *ssa.Function) ([]*ssa.Function, error)
}

// SSAFunctionPlan binds an immutable FunctionPlan back to its SSA function.
type SSAFunctionPlan struct {
	Function *ssa.Function
	Plan     FunctionPlan
}

// SSARootPlan records one canonical externally established entry demand.
// Duplicate and aliased input roots are joined before this record is created.
type SSARootPlan struct {
	Function *ssa.Function
	ID       FunctionID
	Demand   Demand
}

// SSAPlan is the compilation-scoped whole-program result. Its maps remain
// private so consumers cannot reconstruct identities from display strings.
type SSAPlan struct {
	plan          *Plan
	roots         []SSARootPlan
	functions     []SSAFunctionPlan
	byFunction    map[*ssa.Function]FunctionID
	byID          map[FunctionID]*ssa.Function
	ignoredBodies map[*ssa.Function]struct{}
	valuePlans    map[ssa.Value]SSAValuePlan
	callPlans     map[ssa.CallInstruction]SSACallPlan
	elidedCalls   map[ssa.CallInstruction]struct{}
	functionIDs   FunctionIDConfig
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

// Roots returns canonical joined explicit roots in strict FunctionID order.
// The returned slice is a defensive copy.
func (p *SSAPlan) Roots() []SSARootPlan {
	if p == nil {
		return nil
	}
	return append([]SSARootPlan(nil), p.roots...)
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

// IgnoresBody reports whether trusted frontend policy declared that fn's SSA
// body is not part of the physically emitted program. Such a body contributes
// no flow, calls, references, effects, recursion, or digest sites.
func (p *SSAPlan) IgnoresBody(fn *ssa.Function) bool {
	if p == nil || fn == nil {
		return false
	}
	_, ok := p.ignoredBodies[fn]
	return ok
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

	// Freeze trusted per-function policy before inspecting any body. In
	// particular, a frontend-owned external declaration may retain an SSA stub
	// body even though lowering emits no Go instructions for it. Such a body is
	// outside the physical program and must be absent from every downstream
	// analysis, not merely have its scanner effects discarded afterwards.
	trustedPolicies := make(map[*ssa.Function]SSAFunctionPolicy, len(included))
	ignoredBodies := make(map[*ssa.Function]struct{})
	bodyFunctions := make([]*ssa.Function, 0, len(included))
	bodyFunctionSet := make(map[*ssa.Function]bool, len(included))
	for _, fn := range included {
		trusted := SSAFunctionPolicy{}
		if config.ClassifyFunction != nil {
			trusted, err = config.ClassifyFunction(fn)
			if err != nil {
				return nil, fmt.Errorf("coro: classify SSA function %q: %w", fn.Name(), err)
			}
		}
		if trusted.IgnoreBody {
			if !trusted.OverrideExternal || trusted.External == Defined {
				return nil, fmt.Errorf("coro: classify SSA function %q: IgnoreBody requires an explicit non-defined external classification", fn.Name())
			}
			ignoredBodies[fn] = struct{}{}
		} else {
			bodyFunctions = append(bodyFunctions, fn)
			bodyFunctionSet[fn] = true
		}
		trustedPolicies[fn] = trusted
	}
	dynamicCandidates, err = filterSSADynamicCandidateSites(dynamicCandidates, bodyFunctionSet, canonicalizer)
	if err != nil {
		return nil, err
	}
	dynamicCandidates, err = canonicalizeSSADynamicCandidates(dynamicCandidates, canonicalizer)
	if err != nil {
		return nil, err
	}

	ids := make(map[*ssa.Function]FunctionID, len(included))
	byID := make(map[FunctionID]*ssa.Function, len(included))
	idBuilder := functionIDBuilder{
		config:                 config.FunctionIDs,
		localTypeCandidates:    allFunctions,
		localTypeIgnoredBodies: ignoredBodies,
	}
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
	canonicalRoots := make([]SSARootPlan, 0, len(rootDemand))
	for fn, demand := range rootDemand {
		id, ok := ids[fn]
		if !ok {
			return nil, fmt.Errorf("coro: canonical root function %q has no FunctionID", fn.Name())
		}
		canonicalRoots = append(canonicalRoots, SSARootPlan{Function: fn, ID: id, Demand: demand})
	}
	sort.Slice(canonicalRoots, func(i, j int) bool { return canonicalRoots[i].ID < canonicalRoots[j].ID })

	directPlainCallArguments, err := classifySSADirectPlainCallArguments(bodyFunctions, config)
	if err != nil {
		return nil, err
	}
	closedDynamicCalls, err := classifySSAClosedDynamicCalls(bodyFunctions, includedSet, bodyFunctionSet, trustedPolicies, canonicalizer, config)
	if err != nil {
		return nil, err
	}
	flow, err := analyzeSSAFunctionFlow(bodyFunctions, includedSet, ids, dynamicCandidates, config.DynamicResolution, canonicalizer, directPlainCallArguments, closedDynamicCalls)
	if err != nil {
		return nil, fmt.Errorf("coro: analyze SSA function-value flow: %w", err)
	}
	if err := flow.validateDirectPlainCallArguments(); err != nil {
		return nil, fmt.Errorf("coro: validate trusted direct-plain call arguments: %w", err)
	}
	elidedCalls, err := classifySSAElidedCalls(bodyFunctions, config)
	if err != nil {
		return nil, err
	}
	unknownTargets, err := classifySSAUnknownCalls(bodyFunctions, includedSet, flow, elidedCalls, config)
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
		trusted := trustedPolicies[fn]
		if _, ignored := ignoredBodies[fn]; !ignored {
			bodyEffect, bodyExec := scanSSAFunctionBody(fn, maxPlain)
			policy.Effect = policy.Effect.Join(bodyEffect)
			policy.Exec = policy.Exec.Join(bodyExec)
			if trusted.TrustedNoPreempt {
				policy.Exec &^= NeedsPreempt
			}
		}
		policy.Effect = policy.Effect.Join(trusted.Effect)
		// TrustedNoPreempt suppresses only the scanner's local budget/CFG
		// seed above. An explicit trusted NeedsPreempt declaration remains
		// authoritative and is joined only after that suppression.
		policy.Exec = policy.Exec.Join(trusted.Exec)
		policy.NeedsDispatch = policy.NeedsDispatch || trusted.NeedsDispatch
		if trusted.OverrideExternal {
			policy.External = trusted.External
			policy.OverrideExternal = true
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
	for _, caller := range bodyFunctions {
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				if elidedCalls[call] {
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
	if err := addSSAReferenceEdges(graph, bodyFunctions, includedSet, ids, flow); err != nil {
		return nil, err
	}
	if err := addSSAClassifiedDemandReferences(graph, bodyFunctions, includedSet, ids, canonicalizer, config); err != nil {
		return nil, err
	}

	base, err := graph.Analyze()
	if err != nil {
		return nil, err
	}
	valuePlans, callPlans, err := flow.finalize(base, callKinds, unknownTargets)
	if err != nil {
		return nil, fmt.Errorf("coro: finalize SSA value and call plans: %w", err)
	}
	elidedCallSet := make(map[ssa.CallInstruction]struct{}, len(elidedCalls))
	for call, elided := range elidedCalls {
		if elided {
			elidedCallSet[call] = struct{}{}
		}
	}
	result := &SSAPlan{
		plan:          base,
		roots:         canonicalRoots,
		functions:     make([]SSAFunctionPlan, 0, len(included)),
		byFunction:    ids,
		byID:          byID,
		ignoredBodies: ignoredBodies,
		valuePlans:    valuePlans,
		callPlans:     callPlans,
		elidedCalls:   elidedCallSet,
		functionIDs:   config.FunctionIDs,
	}
	for _, functionPlan := range base.Functions() {
		result.functions = append(result.functions, SSAFunctionPlan{
			Function: byID[functionPlan.ID],
			Plan:     functionPlan,
		})
	}
	return result, nil
}

func addSSAClassifiedDemandReferences(
	graph *Graph,
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	ids map[*ssa.Function]FunctionID,
	canonicalizer *ssaFunctionCanonicalizer,
	config SSAConfig,
) error {
	if config.ClassifyDemandReferences == nil {
		return nil
	}
	for _, owner := range functions {
		targets, err := config.ClassifyDemandReferences(owner)
		if err != nil {
			return fmt.Errorf("coro: classify demand-only references in %q: %w", owner.Name(), err)
		}
		// The classifier owns its backing storage. Copy before validation so
		// analysis never retains a frontend-owned slice.
		targets = append([]*ssa.Function(nil), targets...)
		for index, target := range targets {
			if target == nil {
				return fmt.Errorf("coro: demand-only reference %d in %q has a nil target", index, owner.Name())
			}
			if target.Prog != owner.Prog {
				return fmt.Errorf("coro: demand-only reference %d in %q targets function %q from another SSA program", index, owner.Name(), target.Name())
			}
			canonical, resolved, resolveErr := canonicalizer.resolve(target)
			if resolveErr != nil {
				return fmt.Errorf("coro: resolve demand-only target %q in %q: %w", target.Name(), owner.Name(), resolveErr)
			}
			if !resolved || canonical == nil || !included[canonical] {
				return fmt.Errorf("coro: demand-only target %q in %q is outside the effective emission universe", target.Name(), owner.Name())
			}
			if canonical != target {
				return fmt.Errorf("coro: demand-only target %q in %q is not the exact canonical function", target.Name(), owner.Name())
			}
			if err := graph.AddReference(ReferenceEdge{Owner: ids[owner], Target: ids[target]}); err != nil {
				return fmt.Errorf("coro: add demand-only function reference from %q to %q: %w", owner.Name(), target.Name(), err)
			}
		}
	}
	return nil
}

// addSSAReferenceEdges projects known function values used by demanded bodies
// into demand-only graph edges. Every CallInstruction callee operand is skipped:
// static and dynamic invocation are already represented by CallEdge and must
// not be mistaken for first-class publication. All other operands remain
// eligible, covering arguments, boxing, stores, returns, and closure bindings.
func addSSAReferenceEdges(
	graph *Graph,
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	ids map[*ssa.Function]FunctionID,
	flow *ssaFuncFlow,
) error {
	operands := make([]*ssa.Value, 0, 8)
	for _, owner := range functions {
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				if _, debug := instruction.(*ssa.DebugRef); debug {
					continue
				}
				operands = instruction.Operands(operands[:0])
				var calleeOperand *ssa.Value
				if call, ok := instruction.(ssa.CallInstruction); ok {
					calleeOperand = &call.Common().Value
				}
				for _, operand := range operands {
					if operand == nil || *operand == nil || operand == calleeOperand {
						continue
					}
					for _, target := range sortedSSACandidates(flow.materializedTargets(*operand), ids, included) {
						if err := graph.AddReference(ReferenceEdge{Owner: ids[owner], Target: ids[target]}); err != nil {
							return fmt.Errorf("coro: add SSA function reference from %q to %q: %w", owner.Name(), target.Name(), err)
						}
					}
				}
			}
		}
	}
	return nil
}

func classifySSADirectPlainCallArguments(functions []*ssa.Function, config SSAConfig) ([]ssaCallArgumentUse, error) {
	var result []ssaCallArgumentUse
	if config.ClassifyDirectPlainCallArgument == nil {
		return nil, nil
	}
	for _, caller := range functions {
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common() == nil {
					continue
				}
				for argument, value := range call.Common().Args {
					directPlain, err := config.ClassifyDirectPlainCallArgument(caller, call, argument)
					if err != nil {
						return nil, fmt.Errorf("coro: classify trusted direct-plain call argument %d in %q: %w", argument, caller.Name(), err)
					}
					if !directPlain {
						continue
					}
					if _, direct := call.(*ssa.Call); !direct || call.Common().StaticCallee() == nil {
						return nil, fmt.Errorf("coro: trusted direct-plain call argument %d in %q must belong to a direct static call", argument, caller.Name())
					}
					if _, builtin := call.Common().Value.(*ssa.Builtin); builtin {
						return nil, fmt.Errorf("coro: trusted direct-plain call argument %d in %q cannot belong to a builtin call", argument, caller.Name())
					}
					if value == nil || !isScalarFuncType(value.Type()) {
						return nil, fmt.Errorf("coro: trusted direct-plain call argument %d in %q must be a scalar function value", argument, caller.Name())
					}
					result = append(result, ssaCallArgumentUse{call: call, argument: argument})
				}
			}
		}
	}
	return result, nil
}

func classifySSAClosedDynamicCalls(
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	bodyFunctions map[*ssa.Function]bool,
	policies map[*ssa.Function]SSAFunctionPolicy,
	canonicalizer *ssaFunctionCanonicalizer,
	config SSAConfig,
) (map[ssa.CallInstruction]SSAClosedDynamicCallCertificate, error) {
	result := make(map[ssa.CallInstruction]SSAClosedDynamicCallCertificate)
	if config.ClassifyClosedDynamicCall == nil {
		return result, nil
	}
	for _, caller := range functions {
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				certificate, certified, err := config.ClassifyClosedDynamicCall(caller, call)
				if err != nil {
					return nil, fmt.Errorf("coro: classify closed dynamic call in %q: %w", caller.Name(), err)
				}
				if !certified {
					if len(certificate.Targets) != 0 || certificate.MayBeNil {
						return nil, fmt.Errorf("coro: unclassified dynamic call in %q returned non-empty certificate facts", caller.Name())
					}
					continue
				}
				common := call.Common()
				if _, direct := call.(*ssa.Call); !direct || common == nil || call.Parent() != caller {
					return nil, fmt.Errorf("coro: closed dynamic call certificate in %q must identify an exact ordinary *ssa.Call", caller.Name())
				}
				if common.StaticCallee() != nil {
					return nil, fmt.Errorf("coro: closed dynamic call certificate in %q cannot identify a static call", caller.Name())
				}
				if common.IsInvoke() {
					return nil, fmt.Errorf("coro: closed dynamic call certificate in %q cannot identify an interface invoke", caller.Name())
				}
				if _, builtin := common.Value.(*ssa.Builtin); builtin || common.Value == nil || !isScalarFuncType(common.Value.Type()) {
					return nil, fmt.Errorf("coro: closed dynamic call certificate in %q requires a scalar Go function callee", caller.Name())
				}
				if len(certificate.Targets) > 1 {
					return nil, fmt.Errorf("coro: closed dynamic call certificate in %q has %d targets; only nil or one exact target is supported", caller.Name(), len(certificate.Targets))
				}
				if len(certificate.Targets) == 0 && !certificate.MayBeNil {
					return nil, fmt.Errorf("coro: closed dynamic call certificate in %q has neither a target nor nil", caller.Name())
				}

				cloned := SSAClosedDynamicCallCertificate{MayBeNil: certificate.MayBeNil}
				if len(certificate.Targets) == 1 {
					target := certificate.Targets[0]
					if target == nil {
						return nil, fmt.Errorf("coro: closed dynamic call certificate in %q has a nil target entry", caller.Name())
					}
					if target.Prog != caller.Prog {
						return nil, fmt.Errorf("coro: closed dynamic call certificate in %q targets function %q from another SSA program", caller.Name(), target.Name())
					}
					canonical, resolved, resolveErr := canonicalizer.resolve(target)
					if resolveErr != nil {
						return nil, fmt.Errorf("coro: resolve closed dynamic target %q in %q: %w", target.Name(), caller.Name(), resolveErr)
					}
					if !resolved || canonical == nil || !included[canonical] {
						return nil, fmt.Errorf("coro: closed dynamic target %q in %q is outside the effective emission universe", target.Name(), caller.Name())
					}
					if canonical != target {
						return nil, fmt.Errorf("coro: closed dynamic target %q in %q is not the exact canonical function", target.Name(), caller.Name())
					}
					policy := policies[target]
					if !bodyFunctions[target] || len(target.Blocks) == 0 || policy.IgnoreBody || (policy.OverrideExternal && policy.External != Defined) {
						return nil, fmt.Errorf("coro: closed dynamic target %q in %q must be an owned emitted Go body, not an external target", target.Name(), caller.Name())
					}
					if len(target.FreeVars) != 0 {
						return nil, fmt.Errorf("coro: closed dynamic target %q in %q has %d captured variables", target.Name(), caller.Name(), len(target.FreeVars))
					}
					callSignature := common.Signature()
					if callSignature == nil || target.Signature == nil || !types.Identical(callSignature, target.Signature) {
						return nil, fmt.Errorf("coro: closed dynamic target %q in %q has signature %v, want %v", target.Name(), caller.Name(), target.Signature, callSignature)
					}
					cloned.Targets = []*ssa.Function{target}
				}
				result[call] = cloned
			}
		}
	}
	return result, nil
}

func classifySSAElidedCalls(functions []*ssa.Function, config SSAConfig) (map[ssa.CallInstruction]bool, error) {
	result := make(map[ssa.CallInstruction]bool)
	if config.ClassifyElidedCall == nil {
		return result, nil
	}
	for _, caller := range functions {
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok {
					continue
				}
				elided, err := config.ClassifyElidedCall(caller, call)
				if err != nil {
					return nil, fmt.Errorf("coro: classify frontend-elided call in %q: %w", caller.Name(), err)
				}
				if !elided {
					continue
				}
				if call.Common() == nil || call.Common().StaticCallee() == nil || ssaCallKind(call) != CallDirect {
					return nil, fmt.Errorf("coro: frontend-elided call in %q must be a direct static call", caller.Name())
				}
				result[call] = true
			}
		}
	}
	return result, nil
}

func classifySSAUnknownCalls(
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	flow *ssaFuncFlow,
	elided map[ssa.CallInstruction]bool,
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
				if elided[call] {
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

// filterSSADynamicCandidateSites removes CHA sites belonging to SSA stub bodies
// that the frontend does not physically emit. Candidate target functions remain
// untouched: a real emitted call may still dispatch to an external declaration.
func filterSSADynamicCandidateSites(
	candidates map[ssa.CallInstruction]map[*ssa.Function]struct{},
	bodyFunctions map[*ssa.Function]bool,
	canonicalizer *ssaFunctionCanonicalizer,
) (map[ssa.CallInstruction]map[*ssa.Function]struct{}, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}
	result := make(map[ssa.CallInstruction]map[*ssa.Function]struct{}, len(candidates))
	for call, targets := range candidates {
		if call == nil || call.Parent() == nil {
			continue
		}
		owner, ok, err := canonicalizer.resolve(call.Parent())
		if err != nil {
			return nil, fmt.Errorf("coro: resolve dynamic-call owner %q before candidate classification: %w", call.Parent().Name(), err)
		}
		if !ok || !bodyFunctions[owner] {
			continue
		}
		result[call] = targets
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
				if _, debug := instruction.(*ssa.DebugRef); debug {
					continue
				}
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
