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
	"go/types"
	"sort"

	"golang.org/x/tools/go/ssa"
)

// FuncPathKind identifies one structural step to a function-valued leaf.
// Container steps use Index=-1 because they describe an element schema rather
// than one runtime element. V0 only keeps scalarized function values direct;
// every aggregate leaf is conservatively canonicalized to Dispatch.
type FuncPathKind uint8

const (
	FuncPathTupleElement FuncPathKind = iota
	FuncPathStructField
	FuncPathArrayElement
	FuncPathSliceElement
	FuncPathMapKey
	FuncPathMapValue
	FuncPathChanElement
)

// FuncPathStep is one stable type-structural step to a function leaf.
type FuncPathStep struct {
	Kind  FuncPathKind
	Index int
}

// FuncRepLeaf describes the representation and known targets of one function
// leaf. Targets are sorted FunctionIDs and are a conservative subset when a
// value or call is open.
type FuncRepLeaf struct {
	Path    []FuncPathStep
	Rep     FuncRep
	Targets []FunctionID
	// MayBeNil requires consumers to preserve Go's nil-call check even when
	// the non-nil target set is closed and direct.
	MayBeNil bool
}

// FuncRepMap is an ordered representation schema for every function leaf in a
// value. An empty Path denotes a scalar function value.
type FuncRepMap []FuncRepLeaf

// SSAValuePlan binds a representation schema to one SSA value.
type SSAValuePlan struct {
	Value ssa.Value
	Funcs FuncRepMap
}

// SSACallPlan records the representation required at one call site. A static
// call remains direct even when the same target publishes a descriptor for a
// different escaping value.
type SSACallPlan struct {
	Call    ssa.CallInstruction
	Kind    CallKind
	Rep     FuncRep
	Targets []FunctionID
	Open    bool
	// Unresolved identifies the fallback execution domain when Open is true.
	// It does not change the representation of the callee operand.
	Unresolved UnknownTarget
	// MayBeNil requires a nil check before either direct or dispatch invoke.
	MayBeNil bool
}

// ValuePlan returns the immutable representation plan for value.
func (p *SSAPlan) ValuePlan(value ssa.Value) (SSAValuePlan, bool) {
	if p == nil {
		return SSAValuePlan{}, false
	}
	plan, ok := p.valuePlans[value]
	if !ok {
		return SSAValuePlan{}, false
	}
	return cloneSSAValuePlan(plan), true
}

// CallPlan returns the immutable representation plan for call.
func (p *SSAPlan) CallPlan(call ssa.CallInstruction) (SSACallPlan, bool) {
	if p == nil {
		return SSACallPlan{}, false
	}
	plan, ok := p.callPlans[call]
	if !ok {
		return SSACallPlan{}, false
	}
	plan.Targets = append([]FunctionID(nil), plan.Targets...)
	return plan, true
}

func cloneSSAValuePlan(plan SSAValuePlan) SSAValuePlan {
	plan.Funcs = cloneFuncRepMap(plan.Funcs)
	return plan
}

func cloneFuncRepMap(reps FuncRepMap) FuncRepMap {
	if reps == nil {
		return nil
	}
	cloned := make(FuncRepMap, len(reps))
	for i, leaf := range reps {
		cloned[i] = FuncRepLeaf{
			Path:     append([]FuncPathStep(nil), leaf.Path...),
			Rep:      leaf.Rep,
			Targets:  append([]FunctionID(nil), leaf.Targets...),
			MayBeNil: leaf.MayBeNil,
		}
	}
	return cloned
}

type ssaFuncFlow struct {
	values            []ssa.Value
	allValues         map[ssa.Value]struct{}
	index             map[ssa.Value]int
	parent            []int
	rank              []uint8
	canonical         []bool
	unknown           []bool
	mayBeNil          []bool
	targets           []map[*ssa.Function]struct{}
	typePaths         map[types.Type][][]FuncPathStep
	included          map[*ssa.Function]bool
	ids               map[*ssa.Function]FunctionID
	dynamicCandidates map[ssa.CallInstruction]map[*ssa.Function]struct{}
	dynamicResolution DynamicResolution
	canonicalizer     *ssaFunctionCanonicalizer
}

func analyzeSSAFunctionFlow(
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	ids map[*ssa.Function]FunctionID,
	dynamicCandidates map[ssa.CallInstruction]map[*ssa.Function]struct{},
	dynamicResolution DynamicResolution,
	canonicalizer *ssaFunctionCanonicalizer,
) (*ssaFuncFlow, error) {
	flow := &ssaFuncFlow{
		allValues:         make(map[ssa.Value]struct{}),
		index:             make(map[ssa.Value]int),
		typePaths:         make(map[types.Type][][]FuncPathStep),
		included:          included,
		ids:               ids,
		dynamicCandidates: dynamicCandidates,
		dynamicResolution: dynamicResolution,
		canonicalizer:     canonicalizer,
	}

	for _, fn := range functions {
		for _, param := range fn.Params {
			flow.recordValue(param)
		}
		for _, freeVar := range fn.FreeVars {
			flow.recordValue(freeVar)
		}
		operands := make([]*ssa.Value, 0, 8)
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if _, debug := instruction.(*ssa.DebugRef); debug {
					continue
				}
				if value, ok := instruction.(ssa.Value); ok {
					flow.recordValue(value)
				}
				operands = instruction.Operands(operands[:0])
				for _, operand := range operands {
					if operand == nil {
						continue
					}
					value := *operand
					if call, ok := instruction.(ssa.CallInstruction); ok && operand == &call.Common().Value {
						if _, builtin := value.(*ssa.Builtin); builtin {
							continue
						}
						if call.Common().StaticCallee() != nil {
							if _, function := value.(*ssa.Function); function {
								// A bare static callee is not materialized as a
								// first-class value. Other uses still record it.
								continue
							}
						}
					}
					flow.recordValue(value)
				}
			}
		}
	}

	// First join representation-preserving SSA transfers. Seeding boundaries
	// afterwards makes the result independent of instruction enumeration order.
	for value := range flow.allValues {
		switch value := value.(type) {
		case *ssa.Phi:
			if isScalarFuncType(value.Type()) {
				for _, edge := range value.Edges {
					flow.unionValues(value, edge)
				}
			}
		case *ssa.ChangeType:
			flow.unionValues(value, value.X)
		case *ssa.Convert:
			flow.unionValues(value, value.X)
		}
	}

	for value := range flow.allValues {
		if !isScalarFuncType(value.Type()) {
			continue
		}
		switch value := value.(type) {
		case *ssa.Function:
			if err := flow.addTarget(value, value); err != nil {
				return nil, fmt.Errorf("resolve function-value target %q: %w", value.Name(), err)
			}
		case *ssa.MakeClosure:
			if target, ok := value.Fn.(*ssa.Function); ok {
				if err := flow.addTarget(value, target); err != nil {
					return nil, fmt.Errorf("resolve closure target %q: %w", target.Name(), err)
				}
			} else {
				flow.markUnknown(value)
			}
		case *ssa.Const:
			if value.IsNil() {
				flow.markMayBeNil(value)
			} else {
				flow.markUnknown(value)
			}
		case *ssa.Phi, *ssa.ChangeType, *ssa.Convert:
			// Facts arrive through the joined operands.
		default:
			// Parameters, free variables, loads, receives, assertions, call
			// results, and aggregate extracts are open until interprocedural
			// or memory flow proves otherwise.
			flow.markUnknown(value)
		}
	}

	for _, fn := range functions {
		for _, param := range fn.Params {
			flow.markBoundary(param)
		}
		for _, freeVar := range fn.FreeVars {
			flow.markBoundary(freeVar)
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if _, debug := instruction.(*ssa.DebugRef); debug {
					continue
				}
				flow.seedInstruction(instruction)
			}
		}
	}
	return flow, nil
}

func (f *ssaFuncFlow) recordValue(value ssa.Value) {
	if value == nil || value.Type() == nil {
		return
	}
	scalar := isScalarFuncType(value.Type())
	if !scalar && len(f.pathsForType(value.Type())) == 0 {
		return
	}
	f.allValues[value] = struct{}{}
	if !scalar {
		return
	}
	if _, ok := f.index[value]; ok {
		return
	}
	i := len(f.values)
	f.index[value] = i
	f.values = append(f.values, value)
	f.parent = append(f.parent, i)
	f.rank = append(f.rank, 0)
	f.canonical = append(f.canonical, false)
	f.unknown = append(f.unknown, false)
	f.mayBeNil = append(f.mayBeNil, false)
	f.targets = append(f.targets, nil)
}

func (f *ssaFuncFlow) pathsForType(typ types.Type) [][]FuncPathStep {
	if typ == nil {
		return nil
	}
	key := types.Unalias(typ)
	if paths, ok := f.typePaths[key]; ok {
		return paths
	}
	paths := funcLeafPaths(key)
	f.typePaths[key] = paths
	return paths
}

func (f *ssaFuncFlow) ensureScalar(value ssa.Value) (int, bool) {
	if value == nil || value.Type() == nil || !isScalarFuncType(value.Type()) {
		return 0, false
	}
	f.recordValue(value)
	return f.index[value], true
}

func (f *ssaFuncFlow) root(index int) int {
	for f.parent[index] != index {
		f.parent[index] = f.parent[f.parent[index]]
		index = f.parent[index]
	}
	return index
}

func (f *ssaFuncFlow) unionValues(left, right ssa.Value) {
	leftIndex, leftOK := f.ensureScalar(left)
	rightIndex, rightOK := f.ensureScalar(right)
	if !leftOK || !rightOK {
		return
	}
	leftRoot := f.root(leftIndex)
	rightRoot := f.root(rightIndex)
	if leftRoot == rightRoot {
		return
	}
	if f.rank[leftRoot] < f.rank[rightRoot] {
		leftRoot, rightRoot = rightRoot, leftRoot
	}
	f.parent[rightRoot] = leftRoot
	if f.rank[leftRoot] == f.rank[rightRoot] {
		f.rank[leftRoot]++
	}
	f.canonical[leftRoot] = f.canonical[leftRoot] || f.canonical[rightRoot]
	f.unknown[leftRoot] = f.unknown[leftRoot] || f.unknown[rightRoot]
	f.mayBeNil[leftRoot] = f.mayBeNil[leftRoot] || f.mayBeNil[rightRoot]
	if len(f.targets[rightRoot]) != 0 {
		if f.targets[leftRoot] == nil {
			f.targets[leftRoot] = make(map[*ssa.Function]struct{}, len(f.targets[rightRoot]))
		}
		for target := range f.targets[rightRoot] {
			f.targets[leftRoot][target] = struct{}{}
		}
	}
}

func (f *ssaFuncFlow) addTarget(value ssa.Value, target *ssa.Function) error {
	index, ok := f.ensureScalar(value)
	if !ok {
		return nil
	}
	root := f.root(index)
	canonical, resolved, err := f.resolveTarget(target)
	if err != nil {
		return err
	}
	if !resolved || !f.included[canonical] {
		f.unknown[root] = true
		return nil
	}
	if f.targets[root] == nil {
		f.targets[root] = make(map[*ssa.Function]struct{})
	}
	f.targets[root][canonical] = struct{}{}
	return nil
}

func (f *ssaFuncFlow) resolveTarget(target *ssa.Function) (*ssa.Function, bool, error) {
	if f.canonicalizer == nil {
		return target, target != nil, nil
	}
	return f.canonicalizer.resolve(target)
}

func (f *ssaFuncFlow) markBoundary(value ssa.Value) {
	f.recordValue(value)
	index, ok := f.ensureScalar(value)
	if !ok {
		return
	}
	f.canonical[f.root(index)] = true
}

func (f *ssaFuncFlow) markUnknown(value ssa.Value) {
	index, ok := f.ensureScalar(value)
	if !ok {
		return
	}
	root := f.root(index)
	f.unknown[root] = true
	f.mayBeNil[root] = true
}

func (f *ssaFuncFlow) markMayBeNil(value ssa.Value) {
	index, ok := f.ensureScalar(value)
	if !ok {
		return
	}
	f.mayBeNil[f.root(index)] = true
}

func (f *ssaFuncFlow) seedInstruction(instruction ssa.Instruction) {
	switch instruction := instruction.(type) {
	case *ssa.Store:
		f.markBoundary(instruction.Val)
	case *ssa.MapUpdate:
		f.markBoundary(instruction.Key)
		f.markBoundary(instruction.Value)
	case *ssa.Send:
		f.markBoundary(instruction.X)
	case *ssa.Select:
		for _, state := range instruction.States {
			if state.Send != nil {
				f.markBoundary(state.Send)
			}
		}
	case *ssa.MakeInterface:
		f.markBoundary(instruction.X)
	case *ssa.MakeClosure:
		for _, binding := range instruction.Bindings {
			f.markBoundary(binding)
		}
	case *ssa.Return:
		for _, result := range instruction.Results {
			f.markBoundary(result)
		}
	case ssa.CallInstruction:
		common := instruction.Common()
		if common.StaticCallee() == nil {
			if _, builtin := common.Value.(*ssa.Builtin); !builtin {
				f.recordValue(common.Value)
			}
			switch instruction.(type) {
			case *ssa.Go, *ssa.Defer:
				// The callee value is copied into a scheduler/defer record.
				f.markBoundary(common.Value)
			}
		}
		if _, builtin := common.Value.(*ssa.Builtin); builtin {
			// append is the only builtin that can store a scalar function
			// value. Marking every function-valued argument is harmless and
			// keeps this rule robust to future builtins.
			for _, argument := range common.Args {
				f.markBoundary(argument)
			}
			return
		}
		for _, argument := range common.Args {
			f.markBoundary(argument)
		}
	}
}

func (f *ssaFuncFlow) descriptorTargets(unknownTargets map[ssa.CallInstruction]UnknownTarget) map[*ssa.Function]bool {
	result := make(map[*ssa.Function]bool)
	seenRoots := make(map[int]bool)
	for i := range f.values {
		root := f.root(i)
		if seenRoots[root] {
			continue
		}
		seenRoots[root] = true
		if !f.requiresDispatch(root) {
			continue
		}
		for target := range f.targets[root] {
			result[target] = true
		}
	}
	for call, candidates := range f.dynamicCandidates {
		if unknownTargets[call] == UnknownForeign {
			continue
		}
		if !call.Common().IsInvoke() {
			if _, complete := f.scalarCallTargets(call); complete {
				// The component loop above already projected the exact known
				// targets when this closed value requires Dispatch.
				continue
			}
		}
		if call.Common().IsInvoke() || f.callRequiresDispatch(call) {
			for target := range candidates {
				if f.included[target] {
					result[target] = true
				}
			}
		}
	}
	return result
}

func (f *ssaFuncFlow) requiresDispatch(root int) bool {
	return f.canonical[root] || f.unknown[root] || len(f.targets[root]) != 1
}

func (f *ssaFuncFlow) callRequiresDispatch(call ssa.CallInstruction) bool {
	if call == nil || call.Common().StaticCallee() != nil {
		return false
	}
	if call.Common().IsInvoke() {
		return true
	}
	index, ok := f.index[call.Common().Value]
	if !ok {
		return true
	}
	return f.requiresDispatch(f.root(index))
}

// scalarCallTargets returns the structurally known targets of a dynamic
// function call. complete is true when the flow contains no unknown source;
// a complete empty set represents an always-nil callee.
func (f *ssaFuncFlow) scalarCallTargets(call ssa.CallInstruction) (targets map[*ssa.Function]struct{}, complete bool) {
	if call == nil || call.Common().StaticCallee() != nil || call.Common().IsInvoke() {
		return nil, false
	}
	index, ok := f.index[call.Common().Value]
	if !ok {
		return nil, false
	}
	root := f.root(index)
	return f.targets[root], !f.unknown[root]
}

func (f *ssaFuncFlow) finalize(
	base *Plan,
	callKinds map[ssa.CallInstruction]CallKind,
	unknownTargets map[ssa.CallInstruction]UnknownTarget,
) (map[ssa.Value]SSAValuePlan, map[ssa.CallInstruction]SSACallPlan, error) {
	valuePlans := make(map[ssa.Value]SSAValuePlan, len(f.allValues))
	for value := range f.allValues {
		paths := f.pathsForType(value.Type())
		if len(paths) == 0 {
			continue
		}
		if isScalarFuncType(value.Type()) {
			index := f.index[value]
			root := f.root(index)
			targets := f.sortedTargetIDs(f.targets[root])
			rep := Dispatch
			if !f.requiresDispatch(root) {
				rep = directRepForTargets(base, targets)
			}
			valuePlans[value] = SSAValuePlan{Value: value, Funcs: FuncRepMap{{
				Rep:      rep,
				Targets:  targets,
				MayBeNil: f.mayBeNil[root],
			}}}
			continue
		}
		leaves := make(FuncRepMap, len(paths))
		for i, path := range paths {
			leaves[i] = FuncRepLeaf{Path: path, Rep: Dispatch, MayBeNil: true}
		}
		valuePlans[value] = SSAValuePlan{Value: value, Funcs: leaves}
	}

	callPlans := make(map[ssa.CallInstruction]SSACallPlan, len(callKinds))
	for call, kind := range callKinds {
		common := call.Common()
		if _, builtin := common.Value.(*ssa.Builtin); builtin {
			continue
		}
		plan := SSACallPlan{Call: call, Kind: kind, Rep: Dispatch}
		if rawCallee := common.StaticCallee(); rawCallee != nil {
			callee, resolved, err := f.resolveTarget(rawCallee)
			if err != nil {
				caller := "<unknown>"
				if call.Parent() != nil {
					caller = call.Parent().Name()
				}
				return nil, nil, fmt.Errorf("resolve static callee %q in %q while finalizing CallPlan: %w", rawCallee.Name(), caller, err)
			}
			if id, ok := f.ids[callee]; resolved && ok {
				plan.Targets = []FunctionID{id}
				plan.Rep = directRepForTargets(base, plan.Targets)
			} else {
				plan.Open = true
				plan.Unresolved = unknownTargets[call]
				if plan.Unresolved == UnknownForeign {
					plan.Rep = DirectPlain
				}
			}
			callPlans[call] = plan
			continue
		}

		if target, classified := unknownTargets[call]; classified && target == UnknownForeign {
			// The classifier selects the foreign execution/thunk domain, not the
			// operand ABI. An unresolved Go function value still uses Dispatch.
			plan.Open = true
			plan.Unresolved = target
			if !common.IsInvoke() {
				if index, ok := f.index[common.Value]; ok {
					root := f.root(index)
					plan.Targets = f.sortedTargetIDs(f.targets[root])
					plan.MayBeNil = f.mayBeNil[root]
				} else {
					plan.MayBeNil = true
				}
			} else {
				plan.MayBeNil = true
			}
			callPlans[call] = plan
			continue
		}

		targetSet := make(map[*ssa.Function]struct{})
		if !common.IsInvoke() {
			if index, ok := f.index[common.Value]; ok {
				root := f.root(index)
				plan.MayBeNil = f.mayBeNil[root]
				if flowTargets, complete := f.scalarCallTargets(call); complete {
					// Closed scalar flow is more precise than CHA, including for
					// canonical Dispatch values and mixed but closed target sets.
					plan.Targets = f.sortedTargetIDs(flowTargets)
					if !f.requiresDispatch(root) {
						plan.Rep = directRepForTargets(base, plan.Targets)
					}
					callPlans[call] = plan
					continue
				}
				plan.Open = true
				for target := range f.targets[root] {
					targetSet[target] = struct{}{}
				}
			} else {
				plan.Open = true
				plan.MayBeNil = true
			}
		} else {
			plan.Open = !f.dynamicCallClosed(call)
			plan.MayBeNil = true
		}
		if candidates := f.dynamicCandidates[call]; candidates != nil {
			for target := range candidates {
				if f.included[target] {
					targetSet[target] = struct{}{}
				}
			}
		}
		plan.Targets = f.sortedTargetIDs(targetSet)
		closed := f.dynamicCallClosed(call)
		if closed {
			plan.Open = false
		}
		if len(plan.Targets) == 0 && !closed {
			plan.Open = true
		}
		if plan.Open {
			plan.Unresolved = unknownTargets[call]
		}
		callPlans[call] = plan
	}
	return valuePlans, callPlans, nil
}

func (f *ssaFuncFlow) dynamicCallClosed(call ssa.CallInstruction) bool {
	candidates := f.dynamicCandidates[call]
	if f.dynamicResolution != DynamicCHAClosed || len(candidates) == 0 {
		return false
	}
	for target := range candidates {
		if !f.included[target] {
			return false
		}
	}
	return true
}

func (f *ssaFuncFlow) sortedTargetIDs(targets map[*ssa.Function]struct{}) []FunctionID {
	result := make([]FunctionID, 0, len(targets))
	for target := range targets {
		if id, ok := f.ids[target]; ok {
			result = append(result, id)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func directRepForTargets(base *Plan, targets []FunctionID) FuncRep {
	if len(targets) != 1 || base == nil {
		return Dispatch
	}
	function, ok := base.Lookup(targets[0])
	if !ok {
		return Dispatch
	}
	switch function.External {
	case ExternalUnknownManaged:
		return Dispatch
	case ExternalUnknownForeign:
		return DirectPlain
	case ExternalKnown:
		if function.Effect.MaySuspend() {
			return DirectCoro
		}
		return DirectPlain
	case Defined:
		switch function.Primary {
		case PrimaryPlain:
			return DirectPlain
		case PrimaryCoroutine:
			return DirectCoro
		}
	}
	return Dispatch
}

func isScalarFuncType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	_, ok := types.Unalias(typ).Underlying().(*types.Signature)
	return ok
}

func funcLeafPaths(typ types.Type) [][]FuncPathStep {
	if typ == nil {
		return nil
	}
	var paths [][]FuncPathStep
	collectFuncLeafPaths(types.Unalias(typ), nil, make(map[types.Type]bool), &paths)
	sort.Slice(paths, func(i, j int) bool { return lessFuncPath(paths[i], paths[j]) })
	return paths
}

func collectFuncLeafPaths(typ types.Type, path []FuncPathStep, visiting map[types.Type]bool, paths *[][]FuncPathStep) {
	if typ == nil {
		return
	}
	typ = types.Unalias(typ)
	if _, ok := typ.Underlying().(*types.Signature); ok {
		*paths = append(*paths, append([]FuncPathStep(nil), path...))
		return
	}
	if visiting[typ] {
		return
	}
	visiting[typ] = true

	switch underlying := typ.Underlying().(type) {
	case *types.Tuple:
		for i := 0; i < underlying.Len(); i++ {
			collectFuncLeafPaths(underlying.At(i).Type(), appendFuncPath(path, FuncPathTupleElement, i), visiting, paths)
		}
	case *types.Struct:
		for i := 0; i < underlying.NumFields(); i++ {
			collectFuncLeafPaths(underlying.Field(i).Type(), appendFuncPath(path, FuncPathStructField, i), visiting, paths)
		}
	case *types.Array:
		collectFuncLeafPaths(underlying.Elem(), appendFuncPath(path, FuncPathArrayElement, -1), visiting, paths)
	case *types.Slice:
		collectFuncLeafPaths(underlying.Elem(), appendFuncPath(path, FuncPathSliceElement, -1), visiting, paths)
	case *types.Map:
		collectFuncLeafPaths(underlying.Key(), appendFuncPath(path, FuncPathMapKey, -1), visiting, paths)
		collectFuncLeafPaths(underlying.Elem(), appendFuncPath(path, FuncPathMapValue, -1), visiting, paths)
	case *types.Chan:
		collectFuncLeafPaths(underlying.Elem(), appendFuncPath(path, FuncPathChanElement, -1), visiting, paths)
	}
	delete(visiting, typ)
}

func appendFuncPath(path []FuncPathStep, kind FuncPathKind, index int) []FuncPathStep {
	result := make([]FuncPathStep, len(path)+1)
	copy(result, path)
	result[len(path)] = FuncPathStep{Kind: kind, Index: index}
	return result
}

func lessFuncPath(left, right []FuncPathStep) bool {
	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i].Kind != right[i].Kind {
			return left[i].Kind < right[i].Kind
		}
		if left[i].Index != right[i].Index {
			return left[i].Index < right[i].Index
		}
	}
	return len(left) < len(right)
}
