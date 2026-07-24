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
	Path      []FuncPathStep
	Rep       FuncRep
	Transport FuncTransport
	Targets   []FunctionID
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
	Call      ssa.CallInstruction
	Kind      CallKind
	Rep       FuncRep
	Transport FuncTransport
	Targets   []FunctionID
	Open      bool
	// Unresolved identifies the fallback execution domain when Open is true.
	// UnknownManagedDispatch records the universal function-value descriptor;
	// UnknownManagedInterfaceDispatch records the separate universal itab
	// method descriptor. Neither certifies an unrelated raw/foreign fallback.
	Unresolved UnknownTarget
	// MayBeNil requires a nil check before either direct or dispatch invoke.
	MayBeNil bool
	// SyncDispatch is an exact frontend/build proof that this closed descriptor
	// call invokes only a synchronous plain primary. It changes the call-site
	// execution protocol, so it is retained independently from target/value
	// representation and included in the compilation digest.
	SyncDispatch bool
	// RawPlain selects one exact target's native-stack variant. It is valid only
	// for a closed ordinary static call certified by the frontend's
	// RawCritical policy; it never publishes a raw address.
	RawPlain            bool
	RawPlainCertificate string
	// InvocationPolicy is empty for ordinary calls. Trusted-inline calls retain
	// the exact target-neutral contract, structural ABI, and frozen certificate
	// identity that authorized this edge's foreign-wait suppression and selected
	// callable-contract execution projection.
	InvocationPolicy      InvocationPolicy
	InvocationContract    ContractID
	InvocationABI         string
	InvocationCertificate string
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

// ResolveClosedStaticSpawn proves the exact source and whole-plan shape used
// by the first stackless goroutine-spawn lowering. The target is selected by
// the immutable CallPlan, never by a display name or a runtime callback. The
// target must be context-free: either a package function or an exact source
// literal with no captured variables. It has one coroutine primary even when
// its source body is bounded:
// this preserves preemption if that goroutine becomes CPU-heavy and lets sync
// callers reuse the same body through ordinary async-effect propagation.
func (p *SSAPlan) ResolveClosedStaticSpawn(call *ssa.Go) (*ssa.Function, FunctionPlan, error) {
	if p == nil || call == nil || call.Common() == nil {
		return nil, FunctionPlan{}, fmt.Errorf("requires a compilation CallPlan")
	}
	common := call.Common()
	raw, direct := common.Value.(*ssa.Function)
	if !direct || raw == nil || common.IsInvoke() || common.Method != nil || common.StaticCallee() != raw {
		return nil, FunctionPlan{}, fmt.Errorf("requires an exact static top-level function operand")
	}
	callPlan, ok := p.CallPlan(call)
	if !ok {
		return nil, FunctionPlan{}, fmt.Errorf("spawn has no compilation CallPlan")
	}
	if callPlan.Kind != CallSpawn || callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 {
		return nil, FunctionPlan{}, fmt.Errorf(
			"requires one closed non-nil spawn target, got kind=%v open=%t may-be-nil=%t targets=%d",
			callPlan.Kind, callPlan.Open, callPlan.MayBeNil, len(callPlan.Targets),
		)
	}
	target, ok := p.Function(callPlan.Targets[0])
	if !ok || target == nil {
		return nil, FunctionPlan{}, fmt.Errorf("spawn target %q is absent from the compilation plan", callPlan.Targets[0])
	}
	targetPlan, ok := p.FunctionPlan(target)
	if !ok || targetPlan.ID != callPlan.Targets[0] {
		return nil, FunctionPlan{}, fmt.Errorf("spawn target %q has no canonical function plan", callPlan.Targets[0])
	}
	redirected := target != raw
	if len(target.FreeVars) != 0 || !redirected && target.Synthetic != "" ||
		redirected && target.Synthetic == "" ||
		target.Origin() != nil || len(target.TypeArgs()) != 0 {
		return nil, FunctionPlan{}, fmt.Errorf("target %q is not an exact non-capturing context-free function", targetPlan.ID)
	}
	if params := target.TypeParams(); params != nil && params.Len() != 0 {
		return nil, FunctionPlan{}, fmt.Errorf("target %q is a generic declaration", targetPlan.ID)
	}
	sig := target.Signature
	if sig == nil || sig.Recv() != nil || sig.Variadic() ||
		(sig.TypeParams() != nil && sig.TypeParams().Len() != 0) ||
		(sig.RecvTypeParams() != nil && sig.RecvTypeParams().Len() != 0) {
		return nil, FunctionPlan{}, fmt.Errorf("target %q must have one non-method, non-variadic, non-generic signature", targetPlan.ID)
	}
	if redirected && !types.Identical(common.Signature(), sig) {
		return nil, FunctionPlan{}, fmt.Errorf("target %q does not preserve the source spawn signature", targetPlan.ID)
	}
	if targetPlan.External != Defined || targetPlan.Demand != AsyncDemand {
		return nil, FunctionPlan{}, fmt.Errorf(
			"target %q is not one demanded defined async root (external=%s demand=%s)",
			targetPlan.ID, targetPlan.External, targetPlan.Demand,
		)
	}
	if targetPlan.Emission != EmitCoroutine || targetPlan.Primary != PrimaryCoroutine || targetPlan.FuncRep != DirectCoro ||
		!targetPlan.Effect.Contains(YieldOnly) || callPlan.Rep != DirectCoro {
		return nil, FunctionPlan{}, fmt.Errorf(
			"target %q is not one preemptible direct coroutine primary (emission=%s primary=%s representation=%s effect=%s call-representation=%s)",
			targetPlan.ID, targetPlan.Emission, targetPlan.Primary, targetPlan.FuncRep, targetPlan.Effect, callPlan.Rep,
		)
	}
	caller := call.Parent()
	callerPlan, ok := p.FunctionPlan(caller)
	if !ok || callerPlan.Emission != EmitCoroutine || callerPlan.Primary != PrimaryCoroutine ||
		callerPlan.Demand != AsyncDemand || !callerPlan.Effect.Contains(YieldOnly) {
		return nil, FunctionPlan{}, fmt.Errorf(
			"spawn owner is not one async-only coroutine primary (emission=%s primary=%s representation=%s demand=%s effect=%s)",
			callerPlan.Emission, callerPlan.Primary, callerPlan.FuncRep, callerPlan.Demand, callerPlan.Effect,
		)
	}
	return target, targetPlan, nil
}

// ResolveManagedDispatchSpawn proves the immutable source/plan contract for a
// stackless `go fn(args...)` whose callee uses the universal
// {descriptor, environment} representation. Unlike ResolveClosedStaticSpawn,
// this accepts captured closures and ordinary function values, including an
// open value certified as UnknownManagedDispatch. Every exact target must
// publish a coroutine entry: a plain-only descriptor is never adapted into a
// goroutine root.
func (p *SSAPlan) ResolveManagedDispatchSpawn(call *ssa.Go) (SSACallPlan, error) {
	if p == nil || call == nil || call.Common() == nil {
		return SSACallPlan{}, fmt.Errorf("requires a compilation CallPlan")
	}
	common := call.Common()
	if common.IsInvoke() || common.Method != nil {
		return SSACallPlan{}, fmt.Errorf("interface and method spawns require receiver-aware descriptor lowering")
	}
	if _, builtin := common.Value.(*ssa.Builtin); builtin {
		return SSACallPlan{}, fmt.Errorf("builtin spawns are outside the managed descriptor ABI")
	}
	sig := common.Signature()
	if sig == nil || sig.Recv() != nil || sig.Variadic() ||
		typeParamListLen(sig.TypeParams()) != 0 || typeParamListLen(sig.RecvTypeParams()) != 0 {
		return SSACallPlan{}, fmt.Errorf("managed descriptor spawn requires a receiver-free, non-variadic, non-generic signature")
	}
	callPlan, ok := p.CallPlan(call)
	if !ok {
		return SSACallPlan{}, fmt.Errorf("spawn has no compilation CallPlan")
	}
	if callPlan.Kind != CallSpawn || callPlan.Rep != Dispatch {
		return SSACallPlan{}, fmt.Errorf("requires a Dispatch spawn CallPlan, got kind=%v representation=%s", callPlan.Kind, callPlan.Rep)
	}
	if callPlan.Open && callPlan.Unresolved != UnknownManagedDispatch {
		return SSACallPlan{}, fmt.Errorf(
			"open Dispatch spawn is not certified as UnknownManagedDispatch (unresolved=%v)",
			callPlan.Unresolved,
		)
	}
	valuePlan, ok := p.ValuePlan(common.Value)
	if !ok || len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 || valuePlan.Funcs[0].Rep != Dispatch {
		return SSACallPlan{}, fmt.Errorf("callee has no exact scalar Dispatch ValuePlan")
	}
	leaf := valuePlan.Funcs[0]
	if leaf.MayBeNil != callPlan.MayBeNil {
		return SSACallPlan{}, fmt.Errorf(
			"callee ValuePlan nilability %t conflicts with spawn CallPlan nilability %t",
			leaf.MayBeNil, callPlan.MayBeNil,
		)
	}
	// A scalar ValuePlan records targets proven by structural value flow. An
	// open value (notably a function parameter) can therefore have an empty or
	// strict-subset target list even when DynamicCHAClosed gives this exact call
	// site a closed whole-program target domain. The physical descriptor is the
	// same in both cases: every structurally known value target must be admitted
	// by the CallPlan, while additional call-site candidates are valid and must
	// still publish coroutine entries below.
	valueTarget := 0
	for _, callTarget := range callPlan.Targets {
		if valueTarget < len(leaf.Targets) && leaf.Targets[valueTarget] == callTarget {
			valueTarget++
		}
	}
	if valueTarget != len(leaf.Targets) {
		return SSACallPlan{}, fmt.Errorf(
			"callee ValuePlan target %q is absent from spawn CallPlan",
			leaf.Targets[valueTarget],
		)
	}
	for _, targetID := range callPlan.Targets {
		target, found := p.Function(targetID)
		if !found || target == nil {
			return SSACallPlan{}, fmt.Errorf("spawn target %q is absent from the compilation plan", targetID)
		}
		targetPlan, found := p.FunctionPlan(target)
		if !found || targetPlan.ID != targetID {
			return SSACallPlan{}, fmt.Errorf("spawn target %q has no canonical function plan", targetID)
		}
		if targetPlan.External != Defined || targetPlan.Emission != EmitCoroutine ||
			targetPlan.Primary != PrimaryCoroutine || targetPlan.FuncRep != Dispatch ||
			!targetPlan.Effect.Contains(YieldOnly) || !targetPlan.Demand.Contains(AsyncDemand) {
			return SSACallPlan{}, fmt.Errorf(
				"spawn target %q is not one demanded preemptible coroutine descriptor (external=%s emission=%s primary=%s representation=%s effect=%s demand=%s)",
				targetID, targetPlan.External, targetPlan.Emission, targetPlan.Primary,
				targetPlan.FuncRep, targetPlan.Effect, targetPlan.Demand,
			)
		}
		if target.Signature == nil || !types.Identical(sig, target.Signature) {
			return SSACallPlan{}, fmt.Errorf("spawn signature %s does not match target %q signature %s", sig, targetID, target.Signature)
		}
	}
	owner := call.Parent()
	ownerPlan, ok := p.FunctionPlan(owner)
	if !ok || ownerPlan.Emission != EmitCoroutine || ownerPlan.Primary != PrimaryCoroutine ||
		!ownerPlan.Effect.Contains(YieldOnly) || ownerPlan.Demand != AsyncDemand {
		return SSACallPlan{}, fmt.Errorf(
			"spawn owner is not one demanded preemptible coroutine primary (emission=%s primary=%s representation=%s demand=%s effect=%s)",
			ownerPlan.Emission, ownerPlan.Primary, ownerPlan.FuncRep, ownerPlan.Demand, ownerPlan.Effect,
		)
	}
	return callPlan, nil
}

func typeParamListLen(params *types.TypeParamList) int {
	if params == nil {
		return 0
	}
	return params.Len()
}

// ElidesCall reports whether trusted frontend policy proved that the exact SSA
// declaration call emits no callable edge. The source operation may be omitted,
// lowered inline, or replaced by separately frozen lowered calls. Elided calls
// deliberately have no CallPlan and must not be treated as DirectPlain or
// another callable ABI edge; replacement edges retain their own effects.
func (p *SSAPlan) ElidesCall(call ssa.CallInstruction) bool {
	if p == nil || call == nil {
		return false
	}
	_, ok := p.elidedCalls[call]
	return ok
}

// ElidedCallCertificate returns the optional opaque capability frozen for an
// exact frontend-elided call. ok is false when the call has no certificate.
func (p *SSAPlan) ElidedCallCertificate(call ssa.CallInstruction) (certificate string, ok bool) {
	if p == nil || call == nil {
		return "", false
	}
	certificate, ok = p.elidedCallCertificates[call]
	return certificate, ok
}

// RawFunctionAddressArgument reports whether the exact call argument is
// lowered as a raw static function entry rather than as a Go interface or
// descriptor value.
func (p *SSAPlan) RawFunctionAddressArgument(call ssa.CallInstruction, argument int) bool {
	if p == nil || call == nil || argument < 0 {
		return false
	}
	_, ok := p.rawAddressArgs[ssaCallArgumentUse{call: call, argument: argument}]
	return ok
}

// StaticCodeAddressArgument reports whether the exact call argument is lowered
// as an observation of a selected function entry address. It is intentionally
// distinct from RawFunctionAddressArgument: observing a PC never authorizes a
// synchronous call through that pointer.
func (p *SSAPlan) StaticCodeAddressArgument(call ssa.CallInstruction, argument int) bool {
	if p == nil || call == nil || argument < 0 {
		return false
	}
	_, ok := p.codeAddressArgs[ssaCallArgumentUse{call: call, argument: argument}]
	return ok
}

// ConditionalManagedStoreTarget reports the exact target of a frozen managed
// descriptor publication. Publication alone does not demand the target; an
// active reader or another ordinary managed consumer must do so. Consumers may
// elide the Store while this exact target has no managed demand in the same
// plan, even if an independent raw-only use retains another physical entry.
func (p *SSAPlan) ConditionalManagedStoreTarget(store *ssa.Store) (target *ssa.Function, ok bool) {
	if p == nil || store == nil {
		return nil, false
	}
	target, ok = p.conditionalStores[store]
	return target, ok
}

// ElidesConditionalManagedStore reports whether one frozen descriptor Store is
// unobservable in the closed whole-program plan. An independently raw-only
// target does not make a managed descriptor observable.
func (p *SSAPlan) ElidesConditionalManagedStore(store *ssa.Store) bool {
	target, ok := p.ConditionalManagedStoreTarget(store)
	if !ok || target == nil {
		return false
	}
	plan, planned := p.FunctionPlan(target)
	return planned && plan.ManagedDemand == NoDemand
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
			Path:      append([]FuncPathStep(nil), leaf.Path...),
			Rep:       leaf.Rep,
			Transport: leaf.Transport,
			Targets:   append([]FunctionID(nil), leaf.Targets...),
			MayBeNil:  leaf.MayBeNil,
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
	rawC              []bool
	targets           []map[*ssa.Function]struct{}
	typePaths         map[types.Type][][]FuncPathStep
	rawCTypes         map[types.Type]bool
	included          map[*ssa.Function]bool
	ids               map[*ssa.Function]FunctionID
	dynamicCandidates map[ssa.CallInstruction]map[*ssa.Function]struct{}
	dynamicResolution DynamicResolution
	canonicalizer     *ssaFunctionCanonicalizer
	directPlainArgs   map[ssaCallArgumentUse]struct{}
	directPlainOrder  []ssaCallArgumentUse
	rawAddressArgs    map[ssaCallArgumentUse]struct{}
	rawAddressBoxes   map[*ssa.MakeInterface]ssaCallArgumentUse
	closedValues      map[ssa.Value]SSAClosedDynamicCallCertificate
	closedCalls       map[ssa.CallInstruction]SSAClosedDynamicCallCertificate
}

type ssaCallArgumentUse struct {
	call     ssa.CallInstruction
	argument int
}

func analyzeSSAFunctionFlow(
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	ids map[*ssa.Function]FunctionID,
	dynamicCandidates map[ssa.CallInstruction]map[*ssa.Function]struct{},
	dynamicResolution DynamicResolution,
	canonicalizer *ssaFunctionCanonicalizer,
	directPlainArgs []ssaCallArgumentUse,
	rawAddressArgs []ssaCallArgumentUse,
	closedDynamicCalls map[ssa.CallInstruction]SSAClosedDynamicCallCertificate,
	classifyRawCFunctionType func(types.Type) (bool, error),
) (*ssaFuncFlow, error) {
	directPlainSet := make(map[ssaCallArgumentUse]struct{}, len(directPlainArgs))
	for _, use := range directPlainArgs {
		directPlainSet[use] = struct{}{}
	}
	rawAddressSet := make(map[ssaCallArgumentUse]struct{}, len(rawAddressArgs))
	rawAddressBoxes := make(map[*ssa.MakeInterface]ssaCallArgumentUse, len(rawAddressArgs))
	for _, use := range rawAddressArgs {
		rawAddressSet[use] = struct{}{}
		if use.call != nil && use.call.Common() != nil && use.argument >= 0 && use.argument < len(use.call.Common().Args) {
			if boxed, ok := use.call.Common().Args[use.argument].(*ssa.MakeInterface); ok {
				rawAddressBoxes[boxed] = use
			}
		}
	}
	flow := &ssaFuncFlow{
		allValues:         make(map[ssa.Value]struct{}),
		index:             make(map[ssa.Value]int),
		typePaths:         make(map[types.Type][][]FuncPathStep),
		rawCTypes:         make(map[types.Type]bool),
		included:          included,
		ids:               ids,
		dynamicCandidates: dynamicCandidates,
		dynamicResolution: dynamicResolution,
		canonicalizer:     canonicalizer,
		directPlainArgs:   directPlainSet,
		directPlainOrder:  append([]ssaCallArgumentUse(nil), directPlainArgs...),
		rawAddressArgs:    rawAddressSet,
		rawAddressBoxes:   rawAddressBoxes,
		closedValues:      make(map[ssa.Value]SSAClosedDynamicCallCertificate, len(closedDynamicCalls)),
		closedCalls:       make(map[ssa.CallInstruction]SSAClosedDynamicCallCertificate, len(closedDynamicCalls)),
	}
	for call, certificate := range closedDynamicCalls {
		flow.closedCalls[call] = SSAClosedDynamicCallCertificate{
			Targets:      append([]*ssa.Function(nil), certificate.Targets...),
			MayBeNil:     certificate.MayBeNil,
			SyncDispatch: certificate.SyncDispatch,
		}
		value := call.Common().Value
		if previous, exists := flow.closedValues[value]; exists {
			if !sameSSAClosedDynamicCallCertificate(previous, certificate) {
				return nil, fmt.Errorf("conflicting closed dynamic call certificates for callee value in %q", call.Parent().Name())
			}
			continue
		}
		flow.closedValues[value] = SSAClosedDynamicCallCertificate{
			Targets:  append([]*ssa.Function(nil), certificate.Targets...),
			MayBeNil: certificate.MayBeNil,
		}
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

	// Freeze the physical function transport before joining SSA values. A
	// ChangeType between an ordinary Go func and a //llgo:type C func is an ABI
	// crossing, not a representation-preserving union: the former is a managed
	// closure while the latter is one raw code pointer.
	for value := range flow.allValues {
		for _, path := range flow.pathsForType(value.Type()) {
			leafType, ok := funcLeafTypeAtPath(value.Type(), path)
			if !ok {
				return nil, fmt.Errorf("resolve function leaf type for %q at path %+v", value.Name(), path)
			}
			if _, classified := flow.rawCTypes[leafType]; classified {
				continue
			}
			rawC := false
			if classifyRawCFunctionType != nil {
				var err error
				rawC, err = classifyRawCFunctionType(leafType)
				if err != nil {
					return nil, fmt.Errorf("classify raw C function type %s: %w", leafType, err)
				}
			}
			if rawC {
				if _, signature := types.Unalias(leafType).Underlying().(*types.Signature); !signature {
					return nil, fmt.Errorf("raw C function transport classified non-function type %s", leafType)
				}
			}
			flow.rawCTypes[leafType] = rawC
		}
		if index, scalar := flow.index[value]; scalar {
			flow.rawC[index] = flow.rawCTypes[value.Type()]
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
		if flow.rawCValue(value) {
			switch value := value.(type) {
			case *ssa.Const:
				if value.IsNil() {
					flow.markMayBeNil(value)
				} else {
					flow.markUnknown(value)
				}
			case *ssa.Phi:
				// Facts arrive through same-transport incoming values.
			case *ssa.ChangeType:
				if !flow.rawCValue(value.X) {
					if target, exact := exactSSAContextFreeFunctionValue(value.X); exact {
						if err := flow.addTarget(value, target); err != nil {
							return nil, fmt.Errorf("resolve raw C function-value target %q: %w", target.Name(), err)
						}
					} else {
						flow.markUnknown(value)
					}
				}
			case *ssa.Convert:
				if !flow.rawCValue(value.X) {
					if target, exact := exactSSAContextFreeFunctionValue(value.X); exact {
						if err := flow.addTarget(value, target); err != nil {
							return nil, fmt.Errorf("resolve raw C function-value target %q: %w", target.Name(), err)
						}
					} else {
						flow.markUnknown(value)
					}
				}
			default:
				// Parameters, loads, results, and foreign outputs are opaque raw
				// code pointers. They never acquire managed descriptor targets.
				flow.markUnknown(value)
			}
			continue
		}
		if certificate, certified := flow.closedValues[value]; certified {
			for _, target := range certificate.Targets {
				if err := flow.addTarget(value, target); err != nil {
					return nil, fmt.Errorf("resolve certified function-value target %q: %w", target.Name(), err)
				}
			}
			if certificate.MayBeNil {
				flow.markMayBeNil(value)
			}
			// The proof closes the target set, not the physical representation:
			// this value crossed canonical storage and must retain Dispatch.
			flow.markBoundary(value)
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

func exactSSAContextFreeFunctionValue(value ssa.Value) (*ssa.Function, bool) {
	for value != nil {
		switch current := value.(type) {
		case *ssa.Function:
			return current, len(current.FreeVars) == 0
		case *ssa.MakeClosure:
			if len(current.Bindings) != 0 {
				return nil, false
			}
			function, ok := current.Fn.(*ssa.Function)
			return function, ok && function != nil && len(function.FreeVars) == 0
		case *ssa.ChangeType:
			value = current.X
		case *ssa.Convert:
			value = current.X
		default:
			return nil, false
		}
	}
	return nil, false
}

func sameSSAClosedDynamicCallCertificate(left, right SSAClosedDynamicCallCertificate) bool {
	if left.MayBeNil != right.MayBeNil || len(left.Targets) != len(right.Targets) {
		return false
	}
	for i := range left.Targets {
		if left.Targets[i] != right.Targets[i] {
			return false
		}
	}
	return true
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
	f.rawC = append(f.rawC, false)
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
	if f.rawC[leftRoot] != f.rawC[rightRoot] {
		return
	}
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
	f.rawC[leftRoot] = f.rawC[leftRoot] || f.rawC[rightRoot]
	if len(f.targets[rightRoot]) != 0 {
		if f.targets[leftRoot] == nil {
			f.targets[leftRoot] = make(map[*ssa.Function]struct{}, len(f.targets[rightRoot]))
		}
		for target := range f.targets[rightRoot] {
			f.targets[leftRoot][target] = struct{}{}
		}
	}
}

func (f *ssaFuncFlow) rawCValue(value ssa.Value) bool {
	if f == nil || value == nil {
		return false
	}
	index, ok := f.index[value]
	return ok && f.rawC[f.root(index)]
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
		if _, rawAddress := f.rawAddressBoxes[instruction]; !rawAddress {
			f.markBoundary(instruction.X)
		}
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
		_, builtin := common.Value.(*ssa.Builtin)
		// A captured target can use the context-first DirectCoro ABI only while
		// the exact MakeClosure producer is still the call operand. Once a Phi,
		// conversion, parameter, or another value-flow join selects that closure,
		// its environment is part of the runtime callable value and must travel
		// through the canonical {descriptor, environment} representation.
		//
		// Keep context-free singleton values direct: nullable/direct lowering can
		// preserve their nil-call check without manufacturing a descriptor.
		if !builtin && f.capturedCallNeedsDispatch(instruction) {
			f.markBoundary(common.Value)
		}
		// x/tools/ssa reports the nested body of a MakeClosure as a static
		// callee. A spawned captured closure nevertheless crosses the scheduler
		// as its full {descriptor, environment} value.
		if ssaSpawnNeedsDispatch(instruction) {
			f.recordValue(common.Value)
			f.markBoundary(common.Value)
		}
		if common.StaticCallee() == nil {
			if !builtin {
				f.recordValue(common.Value)
				switch instruction.(type) {
				case *ssa.Go, *ssa.Defer:
					// Only an ordinary callee value is copied into a
					// scheduler/defer record. Builtins are compiler operations
					// and cannot be materialized as first-class function values.
					f.markBoundary(common.Value)
				}
			}
		}
		if builtin {
			// append is the only builtin that can store a scalar function
			// value. Marking every function-valued argument is harmless and
			// keeps this rule robust to future builtins.
			for _, argument := range common.Args {
				f.markBoundary(argument)
			}
			return
		}
		for argument, value := range common.Args {
			if _, directPlain := f.directPlainArgs[ssaCallArgumentUse{call: instruction, argument: argument}]; directPlain {
				continue
			}
			if _, rawAddress := f.rawAddressArgs[ssaCallArgumentUse{call: instruction, argument: argument}]; rawAddress {
				continue
			}
			f.markBoundary(value)
		}
	}
}

func (f *ssaFuncFlow) capturedCallNeedsDispatch(call ssa.CallInstruction) bool {
	if f == nil || call == nil || call.Common() == nil {
		return false
	}
	common := call.Common()
	if common.StaticCallee() != nil || common.IsInvoke() || f.rawCValue(common.Value) {
		return false
	}
	if _, exact := common.Value.(*ssa.MakeClosure); exact {
		return false
	}
	index, ok := f.index[common.Value]
	if !ok {
		return false
	}
	root := f.root(index)
	if len(f.targets[root]) != 1 {
		return false
	}
	for target := range f.targets[root] {
		return target != nil && len(target.FreeVars) != 0
	}
	return false
}

func (f *ssaFuncFlow) validateDirectPlainCallArguments() error {
	for _, use := range f.directPlainOrder {
		if use.call == nil || use.call.Common() == nil || use.argument < 0 || use.argument >= len(use.call.Common().Args) {
			return fmt.Errorf("invalid classified call argument index %d", use.argument)
		}
		value := use.call.Common().Args[use.argument]
		index, ok := f.index[value]
		if !ok {
			return fmt.Errorf("call argument %d in %q has no scalar function-value flow component", use.argument, use.call.Parent().Name())
		}
		root := f.root(index)
		if f.unknown[root] || f.mayBeNil[root] || len(f.targets[root]) != 1 || f.requiresDispatch(root) {
			return fmt.Errorf("call argument %d in %q is not a closed non-nil singleton without another canonical boundary (unknown=%t nil=%t targets=%d canonical=%t)",
				use.argument, use.call.Parent().Name(), f.unknown[root], f.mayBeNil[root], len(f.targets[root]), f.canonical[root])
		}
	}
	return nil
}

func (f *ssaFuncFlow) validateRawFunctionAddressCallArguments(uses []ssaCallArgumentUse) error {
	for _, use := range uses {
		if use.call == nil || use.call.Common() == nil || use.argument < 0 || use.argument >= len(use.call.Common().Args) {
			return fmt.Errorf("invalid raw function-address call argument index %d", use.argument)
		}
		boxed, ok := use.call.Common().Args[use.argument].(*ssa.MakeInterface)
		if !ok {
			return fmt.Errorf("raw function-address call argument %d in %q is not a MakeInterface", use.argument, use.call.Parent().Name())
		}
		target, ok := boxed.X.(*ssa.Function)
		if !ok {
			return fmt.Errorf("raw function-address call argument %d in %q does not contain a static function", use.argument, use.call.Parent().Name())
		}
		index, ok := f.index[target]
		if !ok {
			return fmt.Errorf("raw function-address target %q in %q has no function-value flow component", target.Name(), use.call.Parent().Name())
		}
		root := f.root(index)
		canonical, resolved, err := f.resolveTarget(target)
		if err != nil {
			return fmt.Errorf("resolve raw function-address target %q in %q: %w", target.Name(), use.call.Parent().Name(), err)
		}
		if !resolved || canonical == nil || !f.included[canonical] || f.unknown[root] || f.mayBeNil[root] || len(f.targets[root]) != 1 {
			return fmt.Errorf("raw function-address target %q in %q is not a closed non-nil singleton in the emission universe", target.Name(), use.call.Parent().Name())
		}
		if _, present := f.targets[root][canonical]; !present {
			return fmt.Errorf("raw function-address target %q in %q disagrees with canonical function-value flow", target.Name(), use.call.Parent().Name())
		}
	}
	return nil
}

func (f *ssaFuncFlow) validateSyncOnlyDescriptorCallArguments(
	certificates map[ssa.CallInstruction]SSAClosedDynamicCallCertificate,
) ([]ssaCallArgumentUse, error) {
	uses := make([]ssaCallArgumentUse, 0)
	seen := make(map[ssaCallArgumentUse]struct{})
	for descriptorCall, certificate := range certificates {
		for _, publication := range certificate.SyncOnlyCallArguments {
			use := ssaCallArgumentUse{call: publication.Call, argument: publication.Argument}
			if _, duplicate := seen[use]; duplicate {
				return nil, fmt.Errorf("call argument %d in %q is certified by more than one synchronous descriptor publication", publication.Argument, publication.Call.Parent().Name())
			}
			seen[use] = struct{}{}
			value := publication.Call.Common().Args[publication.Argument]
			index, ok := f.index[value]
			if !ok {
				return nil, fmt.Errorf("call argument %d in %q has no scalar function-value flow component", publication.Argument, publication.Call.Parent().Name())
			}
			root := f.root(index)
			if f.unknown[root] || len(f.targets[root]) > 1 {
				return nil, fmt.Errorf(
					"call argument %d in %q is not a closed nil-or-singleton function publication (unknown=%t targets=%d)",
					publication.Argument, publication.Call.Parent().Name(), f.unknown[root], len(f.targets[root]),
				)
			}
			if len(f.targets[root]) == 0 && !f.mayBeNil[root] {
				return nil, fmt.Errorf("call argument %d in %q has neither a target nor nil", publication.Argument, publication.Call.Parent().Name())
			}
			certificateTargets := make(map[*ssa.Function]struct{}, len(certificate.Targets))
			for _, target := range certificate.Targets {
				certificateTargets[target] = struct{}{}
			}
			for target := range f.targets[root] {
				if _, certified := certificateTargets[target]; !certified {
					return nil, fmt.Errorf(
						"call argument %d in %q publishes target %q outside synchronous descriptor call in %q",
						publication.Argument, publication.Call.Parent().Name(), target.Name(), descriptorCall.Parent().Name(),
					)
				}
			}
			uses = append(uses, use)
		}
	}
	sort.Slice(uses, func(i, j int) bool {
		leftOwner := f.ids[uses[i].call.Parent()]
		rightOwner := f.ids[uses[j].call.Parent()]
		if leftOwner != rightOwner {
			return leftOwner < rightOwner
		}
		left := uses[i].call.String()
		right := uses[j].call.String()
		if left != right {
			return left < right
		}
		return uses[i].argument < uses[j].argument
	})
	return uses, nil
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
		if f.rawC[root] {
			continue
		}
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
	if f.rawC[root] {
		return false
	}
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

// materializedTargets returns the statically known function targets carried by
// value. Callers use it only for non-callee SSA operands: a call edge already
// accounts for invoking its callee, while arguments, stores, boxing, returns,
// closure bindings, and direct function-value operations materialize function
// references independently. Body demand is deliberately independent from
// whether the value representation requires Dispatch.
func (f *ssaFuncFlow) materializedTargets(value ssa.Value) map[*ssa.Function]struct{} {
	if f == nil || value == nil {
		return nil
	}
	index, ok := f.index[value]
	if !ok {
		return nil
	}
	return f.targets[f.root(index)]
}

func (f *ssaFuncFlow) finalize(
	base *Plan,
	callKinds map[ssa.CallInstruction]CallKind,
	unknownTargets map[ssa.CallInstruction]UnknownTarget,
	staticSpawnTargets map[*ssa.Go]*ssa.Function,
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
			if f.rawC[root] {
				valuePlans[value] = SSAValuePlan{Value: value, Funcs: FuncRepMap{{
					Rep:       DirectPlain,
					Transport: RawCCodePointer,
					Targets:   targets,
					MayBeNil:  f.mayBeNil[root],
				}}}
				continue
			}
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
			leafType, ok := funcLeafTypeAtPath(value.Type(), path)
			if !ok {
				return nil, nil, fmt.Errorf("resolve function leaf type for %q at path %+v", value.Name(), path)
			}
			transport := ManagedTransport
			rep := Dispatch
			if f.rawCTypes[leafType] {
				transport = RawCCodePointer
				rep = DirectPlain
			}
			leaves[i] = FuncRepLeaf{Path: path, Rep: rep, Transport: transport, MayBeNil: true}
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
		if certificate, certified := f.closedCalls[call]; certified {
			plan.SyncDispatch = certificate.SyncDispatch
		}
		if rawCallee := common.StaticCallee(); rawCallee != nil {
			callee, resolved, err := resolveSSAStaticCallTarget(f, call, rawCallee, staticSpawnTargets)
			if err != nil {
				caller := "<unknown>"
				if call.Parent() != nil {
					caller = call.Parent().Name()
				}
				return nil, nil, fmt.Errorf("resolve static callee %q in %q while finalizing CallPlan: %w", rawCallee.Name(), caller, err)
			}
			if id, ok := f.ids[callee]; resolved && ok {
				plan.Targets = []FunctionID{id}
				if !ssaSpawnNeedsDispatch(call) {
					plan.Rep = directRepForTargets(base, plan.Targets)
				}
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

		if f.rawCValue(common.Value) {
			target, classified := unknownTargets[call]
			if !classified || target != UnknownForeign || common.IsInvoke() || common.Method != nil {
				return nil, nil, fmt.Errorf("raw C code-pointer call in %q lost its exact foreign invocation domain", call.Parent().Name())
			}
			plan.Kind = unknownCallKind(kind, target)
			plan.Rep = DirectPlain
			plan.Transport = RawCCodePointer
			plan.Open = true
			plan.Unresolved = target
			index := f.index[common.Value]
			root := f.root(index)
			plan.Targets = f.sortedTargetIDs(f.targets[root])
			plan.MayBeNil = f.mayBeNil[root]
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

// ssaSpawnNeedsDispatch distinguishes a true static function operand from a
// MakeClosure or bound value for which x/tools/ssa still reports StaticCallee.
// Only the former can use the context-free DirectCoro spawn ABI.
func ssaSpawnNeedsDispatch(call ssa.CallInstruction) bool {
	if call == nil {
		return false
	}
	if _, spawn := call.(*ssa.Go); !spawn || call.Common() == nil || call.Common().StaticCallee() == nil {
		return false
	}
	_, exactFunction := call.Common().Value.(*ssa.Function)
	return !exactFunction
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

func funcLeafTypeAtPath(typ types.Type, path []FuncPathStep) (types.Type, bool) {
	if typ == nil {
		return nil, false
	}
	current := typ
	for _, step := range path {
		underlying := types.Unalias(current).Underlying()
		switch step.Kind {
		case FuncPathTupleElement:
			tuple, ok := underlying.(*types.Tuple)
			if !ok || step.Index < 0 || step.Index >= tuple.Len() {
				return nil, false
			}
			current = tuple.At(step.Index).Type()
		case FuncPathStructField:
			structure, ok := underlying.(*types.Struct)
			if !ok || step.Index < 0 || step.Index >= structure.NumFields() {
				return nil, false
			}
			current = structure.Field(step.Index).Type()
		case FuncPathArrayElement:
			array, ok := underlying.(*types.Array)
			if !ok || step.Index != -1 {
				return nil, false
			}
			current = array.Elem()
		case FuncPathSliceElement:
			slice, ok := underlying.(*types.Slice)
			if !ok || step.Index != -1 {
				return nil, false
			}
			current = slice.Elem()
		case FuncPathMapKey:
			mapping, ok := underlying.(*types.Map)
			if !ok || step.Index != -1 {
				return nil, false
			}
			current = mapping.Key()
		case FuncPathMapValue:
			mapping, ok := underlying.(*types.Map)
			if !ok || step.Index != -1 {
				return nil, false
			}
			current = mapping.Elem()
		case FuncPathChanElement:
			channel, ok := underlying.(*types.Chan)
			if !ok || step.Index != -1 {
				return nil, false
			}
			current = channel.Elem()
		default:
			return nil, false
		}
	}
	if _, ok := types.Unalias(current).Underlying().(*types.Signature); !ok {
		return nil, false
	}
	return current, true
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
