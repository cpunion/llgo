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

package cl

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroPlainDispatchVersion          = llssa.CoroPlainDispatchVersionV1
	coroPlainDispatchFlags            = llssa.CoroPlainDispatchFlagsV1
	coroPlainDispatchDescriptorPrefix = "__llgo_coro_func_descriptor_v1."
	coroPlainDispatchThunkPrefix      = "__llgo_coro_func_plain_v1."
	coroCoroDispatchThunkPrefix       = "__llgo_coro_func_coro_v1."
)

// coroPlainDispatchABI is deliberately target independent of the selected
// function body. Every function with the same canonical callable ABI receives
// the same hash, while its FunctionID digest is used only to make the descriptor
// and thunk symbols target-specific.
type coroPlainDispatchABI struct {
	hash           [16]byte
	signature      *types.Signature
	resultSlotType types.Type
}

// validateCoroDynamicDispatchTarget validates the single primary published by
// a v1 function descriptor. Capability and capture are properties of the
// descriptor/produced value, not reasons to clone the source body: a plain
// primary publishes HasPlain and a coroutine primary publishes HasCoro.
func validateCoroDynamicDispatchTarget(fn *ssa.Function, plan coro.FunctionPlan, universes ...*EmissionUniverse) error {
	var universe *EmissionUniverse
	if len(universes) != 0 {
		universe = universes[0]
	}
	fail := func(format string, args ...any) error {
		name := fmt.Sprint(plan.ID)
		if fn != nil {
			name = fn.String()
		}
		return fmt.Errorf("coroutine dynamic dispatch ABI: function %q (%s): %s", name, plan.ID, fmt.Sprintf(format, args...))
	}
	if fn == nil || plan.External != coro.Defined || len(fn.Blocks) == 0 {
		return fail("requires one defined SSA body")
	}
	rawPlainOnly := plan.RawPlainOnly && plan.ManagedDemand == coro.NoDemand && plan.RawPlainDemand &&
		plan.Emission == coro.EmitRawPlain && plan.Primary == coro.PrimaryPlain && plan.FuncRep == coro.DirectPlain
	if plan.FuncRep != coro.Dispatch && !rawPlainOnly {
		return fail("requires descriptor representation, got %s", plan.FuncRep)
	}
	if plan.Effect.IsOpaque() || plan.Exec.IsOpaque() {
		return fail("opaque effect/execution policy requires an open boundary, got effect=%s exec=%s", plan.Effect, plan.Exec)
	}
	switch plan.Emission {
	case coro.EmitPlain:
		if plan.Primary != coro.PrimaryPlain || plan.Effect != coro.NoSuspend {
			return fail("plain capability requires one exact non-suspending primary, got primary=%s effect=%s", plan.Primary, plan.Effect)
		}
	case coro.EmitCoroutine:
		if plan.Primary != coro.PrimaryCoroutine || !plan.Effect.MaySuspend() {
			return fail("coroutine capability requires one suspending primary, got primary=%s effect=%s", plan.Primary, plan.Effect)
		}
	case coro.EmitRawPlain:
		if !rawPlainOnly {
			return fail("raw-plain capability requires one exact raw-only primary, got raw-only=%t managed=%s raw=%t primary=%s representation=%s",
				plan.RawPlainOnly, plan.ManagedDemand, plan.RawPlainDemand, plan.Primary, plan.FuncRep)
		}
	default:
		return fail("requires one plain or coroutine primary, got emission=%s primary=%s", plan.Emission, plan.Primary)
	}
	if fn.Signature == nil || fn.Signature.Recv() != nil {
		return fail("methods require receiver-aware dispatch lowering")
	}
	if fn.Signature.Variadic() {
		return fail("variadic dispatch is not implemented")
	}
	directive := ""
	if universe == nil {
		directive = coroLeafABIDirective(fn)
	} else {
		var err error
		directive, err = coroRawABIDirective(fn, universe)
		if err != nil {
			return fail("classify ABI directive: %v", err)
		}
	}
	if directive != "" {
		return fail("ABI directive %q requires an explicit boundary adapter", directive)
	}
	if isCgoExternSymbol(fn) {
		return fail("cgo entry requires a foreign adapter")
	}
	genericInstance := coroMaterializedGenericInstance(fn)
	boundMethod := false
	if strings.HasPrefix(fn.Synthetic, "bound method wrapper for ") {
		if err := validateCoroExactBoundMethodWrapper(fn); err != nil {
			return fail("invalid bound method wrapper: %v", err)
		}
		boundMethod = true
	}
	methodExpression := false
	if strings.HasPrefix(fn.Synthetic, "thunk for ") {
		if err := validateCoroExactMethodExpressionThunk(fn); err != nil {
			return fail("invalid method-expression thunk: %v", err)
		}
		methodExpression = true
	}
	if fn.Synthetic != "" && !genericInstance && !boundMethod && !methodExpression {
		return fail("synthetic function %q is outside the plain dispatch ABI", fn.Synthetic)
	}
	if params := fn.TypeParams(); params != nil && params.Len() != 0 && !genericInstance {
		return fail("generic declarations are not materialized dispatch bodies")
	}
	if (len(fn.TypeArgs()) != 0 || fn.Origin() != nil) && !genericInstance {
		return fail("generic instances require a frozen instantiated dispatch ABI")
	}
	if err := validateCoroManagedDispatchSignatureShape(fn.Signature); err != nil {
		return fail("signature: %v", err)
	}
	return nil
}

// validateCoroPlainDispatchTarget is the first consumer slice's stricter
// contract. It deliberately remains no-capture/plain-only until ordinary
// dynamic call lowering is switched to the shared capability-aware API.
func validateCoroPlainDispatchTarget(fn *ssa.Function, plan coro.FunctionPlan, universes ...*EmissionUniverse) error {
	var universe *EmissionUniverse
	if len(universes) != 0 {
		universe = universes[0]
	}
	if err := validateCoroDynamicDispatchTarget(fn, plan, universe); err != nil {
		return err
	}
	fail := func(format string, args ...any) error {
		return fmt.Errorf("coroutine plain dispatch ABI: function %q (%s): %s", fn.String(), plan.ID, fmt.Sprintf(format, args...))
	}
	if plan.Emission != coro.EmitPlain || plan.Primary != coro.PrimaryPlain || plan.Effect != coro.NoSuspend {
		return fail("requires plain descriptor emission, got emission=%s primary=%s effect=%s", plan.Emission, plan.Primary, plan.Effect)
	}
	if plan.Exec.Contains(coro.NeedsPreempt) {
		return fail("execution flags %s require coroutine dispatch lowering", plan.Exec)
	}
	if len(fn.FreeVars) != 0 {
		return fail("captured closure requires the capability-aware dynamic call path")
	}
	if err := validateCoroPlainDispatchSignatureShape(fn.Signature); err != nil {
		return fail("signature: %v", err)
	}
	return nil
}

// validateCoroManagedDispatchSignatureShape is the source-shape boundary for
// the universal descriptor ABI. Unlike the legacy plain-only descriptor, the
// universal ABI uses LLGo's ordinary physical function declaration and a typed
// result slot, so strings, slices, interfaces, pointers and multiple results do
// not need a special scalar transport.
//
// LLGo's ordinary InGo conversion already lowers every inline function leaf
// recursively to the same two-pointer closure aggregate used by the universal
// descriptor ({descriptor, environment}). The whole-program FuncRepMap owns
// whether each such leaf contains a direct code pointer or a descriptor; this
// signature gate therefore accepts nested function parameters/results without
// inventing a second transport. Producers and consumers remain fail-closed at
// their exact ValuePlan/CallPlan boundaries.
func validateCoroManagedDispatchSignatureShape(sig *types.Signature) error {
	if sig == nil {
		return fmt.Errorf("missing signature")
	}
	return nil
}

// validateCoroPlainDispatchSignatureShape preserves the deliberately narrow
// legacy CallCoroPlainDispatch contract. Managed coroutine callers use the
// capability-aware universal descriptor path above.
func validateCoroPlainDispatchSignatureShape(sig *types.Signature) error {
	if sig == nil {
		return fmt.Errorf("missing signature")
	}
	if sig.Results().Len() > 1 {
		return fmt.Errorf("multiple results are not implemented")
	}
	for _, item := range []struct {
		role  string
		tuple *types.Tuple
	}{
		{"parameter", sig.Params()},
		{"result", sig.Results()},
	} {
		for i := 0; i < item.tuple.Len(); i++ {
			if !coroPlainDispatchSourceScalar(item.tuple.At(i).Type()) {
				return fmt.Errorf("%s %d type %s is not a supported scalar", item.role, i, item.tuple.At(i).Type())
			}
		}
	}
	return nil
}

func coroPlainDispatchSourceScalar(typ types.Type) bool {
	typ = types.Unalias(typ)
	if named, ok := typ.(*types.Named); ok {
		return coroPlainDispatchSourceScalar(named.Underlying())
	}
	switch value := typ.Underlying().(type) {
	case *types.Basic:
		info := value.Info()
		return value.Kind() == types.UnsafePointer || info&(types.IsBoolean|types.IsInteger|types.IsFloat) != 0
	case *types.Pointer, *types.Map, *types.Chan:
		return true
	default:
		return false
	}
}

// validateCoroCallableTransportValue proves the physical representation of
// every function-containing leaf copied through an interface boundary.
// Managed Go functions use the compilation-wide {descriptor, environment}
// closure, while an exact //llgo:type C function remains one raw code pointer.
// The two transports are orthogonal to their logical Go signature and must
// never be reinterpreted as one another while boxing or asserting a value.
func validateCoroCallableTransportValue(
	plan *coro.SSAPlan,
	owner *ssa.Function,
	value ssa.Value,
	universe *EmissionUniverse,
) error {
	ownerName := "<unknown>"
	if owner != nil {
		ownerName = owner.Name()
	}
	fail := func(format string, args ...any) error {
		return fmt.Errorf("coroutine callable transport ABI: function %q: %s", ownerName, fmt.Sprintf(format, args...))
	}
	if plan == nil {
		return fail("requires a compilation plan")
	}
	if owner == nil {
		return fail("requires an owning SSA function")
	}
	if value == nil || value.Type() == nil {
		return fail("value is not function-containing")
	}
	effectiveType := coroCallableEffectiveType(universe, owner, value.Type())
	schema := coroCallableTransportSchema(effectiveType)
	if len(schema) == 0 {
		return fail("value is not function-containing")
	}
	valuePlan, found := plan.ValuePlan(value)
	if !found || valuePlan.Value != value {
		return fail("value %q has no exact function ValuePlan", value.Name())
	}
	if len(valuePlan.Funcs) != len(schema) {
		return fail("value %q has %d planned function leaves, want %d", value.Name(), len(valuePlan.Funcs), len(schema))
	}
	for index, expected := range schema {
		leaf := valuePlan.Funcs[index]
		if !equalCoroCallablePath(leaf.Path, expected.path) {
			return fail("value %q function leaf %d has path %+v, want %+v", value.Name(), index, leaf.Path, expected.path)
		}
		transport, err := coroCallableLeafTransport(universe, expected.typ)
		if err != nil {
			return fail("value %q function leaf %d: %v", value.Name(), index, err)
		}
		if universe == nil {
			// Structural unit tests without a frontend universe can still prove
			// representation invariants, but cannot independently recover named
			// //llgo:type metadata. In production the frozen universe is mandatory.
			transport = leaf.Transport
		}
		if err := validateCoroInterfaceCallableLeaf(leaf, transport); err != nil {
			return fail("value %q function leaf %d: %v", value.Name(), index, err)
		}
		if transport != coro.ManagedTransport {
			continue
		}
		sig, ok := types.Unalias(expected.typ).Underlying().(*types.Signature)
		if !ok || sig.Recv() != nil || sig.Variadic() {
			return fail("value %q managed function leaf %d requires an ordinary non-variadic signature", value.Name(), index)
		}
		if params := sig.TypeParams(); params != nil && params.Len() != 0 {
			return fail("value %q managed function leaf %d has an unsupported generic signature", value.Name(), index)
		}
		if params := sig.RecvTypeParams(); params != nil && params.Len() != 0 {
			return fail("value %q managed function leaf %d has an unsupported generic receiver signature", value.Name(), index)
		}
		if err := validateCoroManagedDispatchSignatureShape(sig); err != nil {
			return fail("value %q managed function leaf %d signature: %v", value.Name(), index, err)
		}
	}
	if assertion, asserted := value.(*ssa.TypeAssert); asserted {
		// Type-assertion results are open values reconstructed from interface
		// data. Their exact target set is therefore empty at this boundary; the
		// subsequent dynamic call/spawn owns its independently frozen CallPlan.
		for index, leaf := range valuePlan.Funcs {
			if len(leaf.Targets) != 0 {
				return fail("function assertion %q leaf %d unexpectedly claims exact targets", value.Name(), index)
			}
			if assertion.CommaOk {
				if len(leaf.Path) == 0 || leaf.Path[0].Kind != coro.FuncPathTupleElement || leaf.Path[0].Index != 0 || !leaf.MayBeNil {
					return fail("comma-ok function assertion %q leaf %d has no exact nullable tuple[0] transport", value.Name(), index)
				}
			}
		}
	}
	if err := validateCoroPlainDispatchValue(plan, owner, value, universe); err != nil {
		return err
	}
	return nil
}

type coroCallableTransportLeaf struct {
	path []coro.FuncPathStep
	typ  types.Type
}

func coroCallableEffectiveType(universe *EmissionUniverse, owner *ssa.Function, typ types.Type) types.Type {
	if universe == nil || owner == nil || typ == nil {
		return typ
	}
	prepared := universe.ownerOf(owner)
	if prepared == nil {
		return typ
	}
	return universe.effectiveType(prepared, owner, typ)
}

func coroCallableTransportSchema(typ types.Type) []coroCallableTransportLeaf {
	var leaves []coroCallableTransportLeaf
	collectCoroCallableTransportSchema(typ, nil, make(map[types.Type]bool), &leaves)
	return leaves
}

func collectCoroCallableTransportSchema(
	typ types.Type,
	path []coro.FuncPathStep,
	visiting map[types.Type]bool,
	leaves *[]coroCallableTransportLeaf,
) {
	if typ == nil {
		return
	}
	key := types.Unalias(typ)
	if _, signature := key.Underlying().(*types.Signature); signature {
		*leaves = append(*leaves, coroCallableTransportLeaf{
			path: append([]coro.FuncPathStep(nil), path...),
			typ:  typ,
		})
		return
	}
	if visiting[key] {
		return
	}
	visiting[key] = true
	defer delete(visiting, key)
	appendPath := func(kind coro.FuncPathKind, index int) []coro.FuncPathStep {
		ret := make([]coro.FuncPathStep, len(path)+1)
		copy(ret, path)
		ret[len(path)] = coro.FuncPathStep{Kind: kind, Index: index}
		return ret
	}
	switch underlying := key.Underlying().(type) {
	case *types.Tuple:
		for index := 0; index < underlying.Len(); index++ {
			collectCoroCallableTransportSchema(underlying.At(index).Type(), appendPath(coro.FuncPathTupleElement, index), visiting, leaves)
		}
	case *types.Struct:
		for index := 0; index < underlying.NumFields(); index++ {
			collectCoroCallableTransportSchema(underlying.Field(index).Type(), appendPath(coro.FuncPathStructField, index), visiting, leaves)
		}
	case *types.Array:
		collectCoroCallableTransportSchema(underlying.Elem(), appendPath(coro.FuncPathArrayElement, -1), visiting, leaves)
	case *types.Slice:
		collectCoroCallableTransportSchema(underlying.Elem(), appendPath(coro.FuncPathSliceElement, -1), visiting, leaves)
	case *types.Map:
		collectCoroCallableTransportSchema(underlying.Key(), appendPath(coro.FuncPathMapKey, -1), visiting, leaves)
		collectCoroCallableTransportSchema(underlying.Elem(), appendPath(coro.FuncPathMapValue, -1), visiting, leaves)
	case *types.Chan:
		collectCoroCallableTransportSchema(underlying.Elem(), appendPath(coro.FuncPathChanElement, -1), visiting, leaves)
	}
}

func equalCoroCallablePath(left, right []coro.FuncPathStep) bool {
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

func coroCallableLeafTransport(universe *EmissionUniverse, typ types.Type) (coro.FuncTransport, error) {
	if typ == nil {
		return coro.ManagedTransport, fmt.Errorf("has no source type")
	}
	if universe == nil || universe.prog == nil || universe.prog.TypeBackground(typ) != llssa.InC {
		return coro.ManagedTransport, nil
	}
	if _, signature := types.Unalias(typ).Underlying().(*types.Signature); !signature {
		return coro.ManagedTransport, fmt.Errorf("frontend marked non-function type %s as raw C transport", typ)
	}
	return coro.RawCCodePointer, nil
}

func validateCoroInterfaceCallableLeaf(leaf coro.FuncRepLeaf, want coro.FuncTransport) error {
	if err := leaf.Transport.Validate(); err != nil {
		return err
	}
	if leaf.Transport != want {
		return fmt.Errorf("transport=%s, want %s from frozen frontend type metadata", leaf.Transport, want)
	}
	switch want {
	case coro.ManagedTransport:
		if leaf.Rep != coro.Dispatch {
			return fmt.Errorf("managed interface leaf requires Dispatch, got %s", leaf.Rep)
		}
	case coro.RawCCodePointer:
		if leaf.Rep != coro.DirectPlain {
			return fmt.Errorf("raw C interface leaf requires DirectPlain, got %s", leaf.Rep)
		}
	default:
		return fmt.Errorf("unsupported function transport %s", want)
	}
	return nil
}

func validateCoroPlainDispatchConsumers(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	interfacePlain *coroClosedInterfacePlainPlan,
	managedInterface *coroManagedInterfaceDispatchPlan,
) error {
	if plan == nil {
		return fmt.Errorf("coroutine plain dispatch ABI requires a compilation plan")
	}
	for _, function := range plan.Functions() {
		if function.Plan.Emission != coro.EmitPlain && function.Plan.Emission != coro.EmitCoroutine {
			continue
		}
		fn := function.Function
		for _, param := range fn.Params {
			if err := validateCoroPlainDispatchValue(plan, fn, param, universe); err != nil {
				return err
			}
		}
		for _, free := range fn.FreeVars {
			if err := validateCoroPlainDispatchValue(plan, fn, free, universe); err != nil {
				return err
			}
		}
		for _, block := range fn.Blocks {
			for _, instr := range block.Instrs {
				if store, ok := instr.(*ssa.Store); ok && plan.ElidesConditionalManagedStore(store) {
					// The complete closed-cell proof makes this exact descriptor
					// producer unobservable. Code generation omits it, so neither
					// its EmitNone target nor operand needs descriptor validation.
					continue
				}
				if boxed, ok := instr.(*ssa.MakeInterface); ok &&
					coroCompilerElidedFunctionAddressBox(plan, universe, fn, boxed) {
					// funcPCABI0/funcAddr consume the static SSA function directly;
					// neither the transient interface nor its function operand is a
					// descriptor producer/consumer.
					continue
				}
				if value, ok := instr.(ssa.Value); ok {
					if err := validateCoroPlainDispatchValue(plan, fn, value, universe); err != nil {
						return err
					}
				}
				for _, operand := range instr.Operands(nil) {
					if operand != nil && *operand != nil {
						if err := validateCoroPlainDispatchValue(plan, fn, *operand, universe); err != nil {
							return err
						}
					}
				}
				if boxed, ok := instr.(*ssa.MakeInterface); ok {
					if len(coroCallableTransportSchema(coroCallableEffectiveType(universe, fn, boxed.X.Type()))) != 0 {
						if err := validateCoroCallableTransportValue(plan, fn, boxed.X, universe); err != nil {
							return coroPlainDispatchInstructionError(fn, instr, err.Error())
						}
					}
				}
				call, ok := instr.(ssa.CallInstruction)
				if !ok || plan.ElidesCall(call) {
					continue
				}
				common := call.Common()
				if common != nil {
					if _, builtin := common.Value.(*ssa.Builtin); builtin {
						continue
					}
				}
				callPlan, found := plan.CallPlan(call)
				if !found {
					return coroPlainDispatchInstructionError(fn, instr, "call has no compilation CallPlan")
				}
				if callPlan.Rep != coro.Dispatch {
					continue
				}
				if callPlan.Transport != coro.ManagedTransport {
					return coroPlainDispatchInstructionError(fn, instr, fmt.Sprintf(
						"Dispatch CallPlan requires managed transport, got %s", callPlan.Transport,
					))
				}
				if managedInterface.acceptsCall(call) {
					if callPlan.Open {
						if err := validateCoroManagedInterfaceDispatchCall(plan, universe, fn, call, callPlan); err != nil {
							return err
						}
					}
					continue
				}
				if spawn, ok := call.(*ssa.Go); ok {
					if _, err := plan.ResolveManagedDispatchSpawn(spawn); err != nil {
						return coroPlainDispatchInstructionError(fn, instr, "invalid managed descriptor spawn: "+err.Error())
					}
					continue
				}
				if deferred, ok := call.(*ssa.Defer); ok {
					ownerPlan, planned := plan.FunctionPlan(fn)
					if !planned || ownerPlan.Emission != coro.EmitCoroutine || !ownerPlan.Exec.Contains(coro.NeedsCleanupFrame) {
						return coroPlainDispatchInstructionError(fn, instr,
							"managed descriptor defer requires one coroutine cleanup owner")
					}
					if err := validateCoroManagedDispatchDefer(plan, fn, deferred, callPlan, universe); err != nil {
						return err
					}
					continue
				}
				if callPlan.SyncDispatch {
					if err := validateCoroPlainDispatchCall(plan, fn, call, callPlan, universe); err != nil {
						return err
					}
					continue
				}
				managedDynamic := callPlan.Unresolved == coro.UnknownManagedDispatch
				if !managedDynamic {
					if ownerPlan, ok := plan.FunctionPlan(fn); ok && ownerPlan.Emission == coro.EmitCoroutine {
						if direct, ok := call.(*ssa.Call); ok {
							common := direct.Common()
							managedDynamic = common != nil && common.StaticCallee() == nil && !common.IsInvoke() && common.Method == nil
						}
					}
				}
				if managedDynamic {
					if err := validateCoroManagedDispatchCall(plan, fn, call, callPlan, universe); err != nil {
						return err
					}
					continue
				}
				if interfacePlain.acceptsCall(call) {
					continue
				}
				if ownerPlan, ok := plan.FunctionPlan(fn); ok && ownerPlan.Emission == coro.EmitCoroutine {
					if direct, ok := call.(*ssa.Call); ok {
						if dispatch, err := resolveCoroInterfaceDispatchPlan(plan, universe, direct); err == nil && coroInterfaceDispatchNeedsAwait(dispatch) {
							continue
						}
					}
				}
				if err := validateCoroPlainDispatchCall(plan, fn, call, callPlan, universe); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateCoroPlainDispatchValue(plan *coro.SSAPlan, owner *ssa.Function, value ssa.Value, universes ...*EmissionUniverse) error {
	var universe *EmissionUniverse
	if len(universes) != 0 {
		universe = universes[0]
	}
	valuePlan, found := plan.ValuePlan(value)
	if !found || !funcRepMapContains(valuePlan.Funcs, coro.Dispatch) {
		return nil
	}
	if len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 {
		// Aggregate storage preserves each leaf's independently planned physical
		// transport: managed functions are two-pointer descriptors, while exact
		// raw C functions remain one direct code pointer. Scalar producers and
		// consumers are validated separately. Interface boxing is still checked
		// at its instruction boundary below.
		for _, leaf := range valuePlan.Funcs {
			if leaf.Transport == coro.RawCCodePointer && leaf.Rep == coro.DirectPlain {
				continue
			}
			if leaf.Transport != coro.ManagedTransport || leaf.Rep != coro.Dispatch {
				return fmt.Errorf("coroutine plain dispatch ABI: function %q: aggregate value %q has invalid function leaf transport=%s representation=%s", owner.Name(), value.Name(), leaf.Transport, leaf.Rep)
			}
		}
		return nil
	}
	leaf := valuePlan.Funcs[0]
	if leaf.Transport != coro.ManagedTransport || leaf.Rep != coro.Dispatch {
		return fmt.Errorf("coroutine plain dispatch ABI: function %q: value %q has a mixed function representation", owner.Name(), value.Name())
	}
	if _, ok := types.Unalias(value.Type()).Underlying().(*types.Signature); !ok {
		return fmt.Errorf("coroutine plain dispatch ABI: function %q: value %q is not a scalar function value", owner.Name(), value.Name())
	}
	if len(leaf.Targets) == 0 {
		if !leaf.MayBeNil {
			return fmt.Errorf("coroutine plain dispatch ABI: function %q: value %q has no target and is not nil", owner.Name(), value.Name())
		}
		return nil
	}
	for _, targetID := range leaf.Targets {
		target, targetPlan, err := coroPlainDispatchPlanTarget(plan, targetID)
		if err != nil {
			return fmt.Errorf("coroutine plain dispatch ABI: function %q: value %q: %w", owner.Name(), value.Name(), err)
		}
		if err := validateCoroDynamicDispatchTarget(target, targetPlan, universe); err != nil {
			return err
		}
	}
	return nil
}

func validateCoroPlainDispatchCall(plan *coro.SSAPlan, owner *ssa.Function, call ssa.CallInstruction, callPlan coro.SSACallPlan, universes ...*EmissionUniverse) error {
	var universe *EmissionUniverse
	if len(universes) != 0 {
		universe = universes[0]
	}
	fail := func(format string, args ...any) error {
		return coroPlainDispatchInstructionError(owner, call, fmt.Sprintf(format, args...))
	}
	direct, ordinary := call.(*ssa.Call)
	if !ordinary || direct == nil || callPlan.Kind != coro.CallDirect ||
		callPlan.Transport != coro.ManagedTransport {
		return fail("descriptor dispatch is supported only for an ordinary direct call instruction")
	}
	common := direct.Common()
	if common == nil || common.StaticCallee() != nil || common.IsInvoke() || common.Method != nil {
		return fail("descriptor dispatch requires an ordinary dynamic function call")
	}
	if callPlan.Open || callPlan.Unresolved == coro.UnknownForeign {
		return fail("open or foreign descriptor dispatch is not implemented")
	}
	if callPlan.SyncDispatch {
		ownerPlan, ok := plan.FunctionPlan(owner)
		if !ok || ownerPlan.Emission == coro.EmitNone {
			return fail("synchronous descriptor dispatch owner has no emitted function plan")
		}
	}
	if len(callPlan.Targets) > 1 {
		return fail("multi-target descriptor dispatch is not implemented")
	}
	if len(callPlan.Targets) == 0 {
		if !callPlan.MayBeNil {
			return fail("closed descriptor call has no target and is not nil")
		}
	} else {
		targetFn, targetPlan, err := coroPlainDispatchPlanTarget(plan, callPlan.Targets[0])
		if err != nil {
			return fail("%v", err)
		}
		if err := validateCoroPlainDispatchTarget(targetFn, targetPlan, universe); err != nil {
			return fail("%v", err)
		}
		if !types.Identical(common.Signature(), targetFn.Signature) {
			return fail("call signature %s does not match target %q signature %s", common.Signature(), targetPlan.ID, targetFn.Signature)
		}
	}
	valuePlan, found := plan.ValuePlan(common.Value)
	if !found || len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 ||
		valuePlan.Funcs[0].Rep != coro.Dispatch || valuePlan.Funcs[0].Transport != coro.ManagedTransport {
		return fail("callee has no exact scalar Dispatch ValuePlan")
	}
	leaf := valuePlan.Funcs[0]
	if missing, ok := coroDispatchTargetsSubset(leaf.Targets, callPlan.Targets); !ok {
		return fail("callee ValuePlan target %q is absent from CallPlan", missing)
	}
	if leaf.MayBeNil != callPlan.MayBeNil {
		return fail("callee nilability %t conflicts with CallPlan nilability %t", leaf.MayBeNil, callPlan.MayBeNil)
	}
	return nil
}

func funcRepMapContains(reps coro.FuncRepMap, want coro.FuncRep) bool {
	for _, leaf := range reps {
		if leaf.Rep == want {
			return true
		}
	}
	return false
}

func coroPlainDispatchPlanTarget(plan *coro.SSAPlan, id coro.FunctionID) (*ssa.Function, coro.FunctionPlan, error) {
	target, found := plan.Function(id)
	if !found || target == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("target %q is absent from the compilation plan", id)
	}
	targetPlan, found := plan.FunctionPlan(target)
	if !found || targetPlan.ID != id {
		return nil, coro.FunctionPlan{}, fmt.Errorf("target %q has no canonical function plan", id)
	}
	return target, targetPlan, nil
}

func coroPlainDispatchInstructionError(fn *ssa.Function, instr ssa.Instruction, reason string) error {
	position := token.Position{}
	if fn != nil && fn.Prog != nil && fn.Prog.Fset != nil && instr != nil {
		position = fn.Prog.Fset.Position(instr.Pos())
	}
	return fmt.Errorf("coroutine plain dispatch ABI: function %q at %s: %s", fn.Name(), position, reason)
}

func nestedFunctionTypePath(typ types.Type) (string, bool) {
	seen := make(map[types.Type]bool)
	var visit func(types.Type, string, bool) (string, bool)
	visit = func(typ types.Type, path string, root bool) (string, bool) {
		if typ == nil {
			return "", false
		}
		typ = types.Unalias(typ)
		if seen[typ] {
			return "", false
		}
		seen[typ] = true
		switch value := typ.(type) {
		case *types.Signature:
			if !root {
				return path, true
			}
			for i := 0; i < value.Params().Len(); i++ {
				if found, ok := visit(value.Params().At(i).Type(), fmt.Sprintf("param[%d]", i), false); ok {
					return found, true
				}
			}
			for i := 0; i < value.Results().Len(); i++ {
				if found, ok := visit(value.Results().At(i).Type(), fmt.Sprintf("result[%d]", i), false); ok {
					return found, true
				}
			}
		case *types.Named:
			return visit(value.Underlying(), path+".underlying", false)
		case *types.Pointer:
			// Pointer identity is part of the canonical logical signature, while
			// its physical layout terminates at one opaque pointer.
			return "", false
		case *types.Array:
			return visit(value.Elem(), path+".elem", false)
		case *types.Slice, *types.Map, *types.Chan, *types.Interface:
			// These are reference/header-shaped physical values. Their logical
			// element or method signatures are not copied inline through this
			// call ABI, so they do not require recursive function-value lowering.
			return "", false
		case *types.Struct:
			for i := 0; i < value.NumFields(); i++ {
				if found, ok := visit(value.Field(i).Type(), fmt.Sprintf("%s.field[%d]", path, i), false); ok {
					return found, true
				}
			}
		}
		return "", false
	}
	return visit(typ, "signature", true)
}

func newCoroPlainDispatchABI(p *context, signature *types.Signature) (coroPlainDispatchABI, error) {
	if p == nil || p.prog == nil || signature == nil {
		return coroPlainDispatchABI{}, fmt.Errorf("coroutine plain dispatch ABI requires a program and signature")
	}
	patched, ok := p.patchType(signature).(*types.Signature)
	if !ok {
		return coroPlainDispatchABI{}, fmt.Errorf("patched dispatch signature is %T", p.patchType(signature))
	}
	patched = canonicalCoroPlainDispatchSignature(patched)
	physical := p.prog.PhysicalFuncDecl(patched, llssa.InGo)
	resultFields := make([]*types.Var, physical.Results().Len())
	for i := range resultFields {
		resultFields[i] = types.NewField(token.NoPos, nil, fmt.Sprintf("r%d", i), physical.Results().At(i).Type(), false)
	}
	resultSlot := types.NewStruct(resultFields, nil)

	qualified := func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return llssa.PathOf(pkg)
	}
	var key strings.Builder
	writeDispatchHashField(&key, "domain", "llgo.coro.func-dispatch.v1")
	writeDispatchHashField(&key, "version", strconv.FormatUint(uint64(coroPlainDispatchVersion), 10))
	// Capability and capture are runtime descriptor flags, not signature ABI.
	// An open caller cannot know whether its producer is plain/coroutine or
	// captured, so all compatible producers must share this hash.
	writeDispatchHashField(&key, "closure", "two-pointer:descriptor,env;plain=(env,args)->results;coro=(g,out,env,args)->handle")
	writeDispatchHashField(&key, "panic", activeCompilationABI(p.compilation, func(c *Compilation) string { return c.PanicABI }, coro.PanicLegacyABIV0))
	writeDispatchHashField(&key, "func-rep", activeCompilationABI(p.compilation, func(c *Compilation) string { return c.FuncRepABI }, coro.FuncRepABIV1))
	target := p.prog.TargetSpec()
	writeDispatchHashField(&key, "triple", target.Triple)
	writeDispatchHashField(&key, "cpu", target.CPU)
	writeDispatchHashField(&key, "features", target.Features)
	writeDispatchHashField(&key, "target-abi", target.TargetABI)
	writeDispatchHashField(&key, "data-layout", p.prog.DataLayout())
	writeDispatchHashField(&key, "pointer-bytes", strconv.Itoa(p.prog.PointerSize()))
	writeDispatchHashField(&key, "byte-order", strconv.Itoa(int(p.prog.TargetData().ByteOrder())))
	// The ABI identity is structural at every function nesting depth. Parameter
	// and result names are source decoration, including inside callback types;
	// they must not make an otherwise identical producer and consumer disagree.
	writeDispatchHashField(&key, "logical-signature", structuralEmissionABITypeKey(patched))
	writeDispatchHashField(&key, "physical-signature", structuralEmissionABITypeKey(physical))
	if err := appendCoroPlainDispatchTupleLayout(&key, p.prog, "params", physical.Params(), qualified); err != nil {
		return coroPlainDispatchABI{}, err
	}
	if err := appendCoroPlainDispatchTupleLayout(&key, p.prog, "results", physical.Results(), qualified); err != nil {
		return coroPlainDispatchABI{}, err
	}
	if err := appendCoroPlainDispatchTypeLayout(&key, p.prog, "result-slot", resultSlot, qualified, make(map[types.Type]bool)); err != nil {
		return coroPlainDispatchABI{}, err
	}
	sum := sha256.Sum256([]byte(key.String()))
	var hash [16]byte
	copy(hash[:], sum[:len(hash)])
	return coroPlainDispatchABI{hash: hash, signature: patched, resultSlotType: resultSlot}, nil
}

// canonicalCoroPlainDispatchSignature removes source parameter/result names.
// go/types identity ignores those names, and a target declaration commonly has
// them while a function-typed parameter at the exact dynamic call does not.
// Letting names enter the descriptor hash would make two ABI-identical sites
// disagree at runtime.
func canonicalCoroPlainDispatchSignature(sig *types.Signature) *types.Signature {
	params := make([]*types.Var, sig.Params().Len())
	for i := range params {
		params[i] = types.NewParam(token.NoPos, nil, "", sig.Params().At(i).Type())
	}
	results := make([]*types.Var, sig.Results().Len())
	for i := range results {
		results[i] = types.NewParam(token.NoPos, nil, "", sig.Results().At(i).Type())
	}
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), types.NewTuple(results...), false)
}

func activeCompilationABI(c *Compilation, value func(*Compilation) string, fallback string) string {
	if c != nil {
		if current := value(c); current != "" {
			return current
		}
	}
	return fallback
}

func writeDispatchHashField(builder *strings.Builder, name, value string) {
	builder.WriteString(strconv.Itoa(len(name)))
	builder.WriteByte(':')
	builder.WriteString(name)
	builder.WriteByte('=')
	builder.WriteString(strconv.Itoa(len(value)))
	builder.WriteByte(':')
	builder.WriteString(value)
	builder.WriteByte('\n')
}

func appendCoroPlainDispatchTupleLayout(builder *strings.Builder, prog llssa.Program, path string, tuple *types.Tuple, qualified types.Qualifier) error {
	writeDispatchHashField(builder, path+".count", strconv.Itoa(tuple.Len()))
	for i := 0; i < tuple.Len(); i++ {
		if err := appendCoroPlainDispatchTypeLayout(builder, prog, fmt.Sprintf("%s[%d]", path, i), tuple.At(i).Type(), qualified, make(map[types.Type]bool)); err != nil {
			return err
		}
	}
	return nil
}

func appendCoroPlainDispatchTypeLayout(builder *strings.Builder, prog llssa.Program, path string, typ types.Type, qualified types.Qualifier, visiting map[types.Type]bool) error {
	if typ == nil {
		return fmt.Errorf("coroutine plain dispatch ABI: nil type at %s", path)
	}
	typ = types.Unalias(typ)
	writeDispatchHashField(builder, path+".type", structuralEmissionABITypeKey(typ))
	physical := prog.Type(typ, llssa.InC)
	writeDispatchHashField(builder, path+".size", strconv.FormatUint(prog.SizeOf(physical), 10))
	writeDispatchHashField(builder, path+".align", strconv.FormatUint(prog.AlignOf(physical), 10))
	if visiting[typ] {
		writeDispatchHashField(builder, path+".cycle", "true")
		return nil
	}
	visiting[typ] = true
	defer delete(visiting, typ)
	switch value := typ.(type) {
	case *types.Named:
		return appendCoroPlainDispatchTypeLayout(builder, prog, path+".underlying", value.Underlying(), qualified, visiting)
	case *types.Pointer:
		writeDispatchHashField(builder, path+".pointer", "opaque")
	case *types.Struct:
		writeDispatchHashField(builder, path+".fields", strconv.Itoa(value.NumFields()))
		for i := 0; i < value.NumFields(); i++ {
			writeDispatchHashField(builder, fmt.Sprintf("%s.field[%d].offset", path, i), strconv.FormatUint(prog.OffsetOf(physical, i), 10))
			if err := appendCoroPlainDispatchTypeLayout(builder, prog, fmt.Sprintf("%s.field[%d]", path, i), value.Field(i).Type(), qualified, visiting); err != nil {
				return err
			}
		}
	case *types.Array:
		writeDispatchHashField(builder, path+".length", strconv.FormatInt(value.Len(), 10))
		return appendCoroPlainDispatchTypeLayout(builder, prog, path+".element", value.Elem(), qualified, visiting)
	case *types.Signature:
		// A signature here is the first field of LLGo's already-converted
		// two-pointer closure aggregate. LLVM opaque pointers make the code word
		// layout independent of its pointee declaration; the structural type key
		// above still commits the ABI hash to the complete, name-insensitive
		// callback signature.
		writeDispatchHashField(builder, path+".function-code", "opaque-pointer")
	}
	return nil
}

func (p *context) tryCompileCoroPlainDispatchFunctionValue(b llssa.Builder, value *ssa.Function) (llssa.Expr, bool) {
	if p.compilation == nil || !p.compilation.EnableCoroPlainDispatch || p.compilation.CoroPlan == nil {
		return llssa.Expr{}, false
	}
	valuePlan, found := p.compilation.CoroPlan.ValuePlan(value)
	if !found || len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 || valuePlan.Funcs[0].Rep != coro.Dispatch {
		return llssa.Expr{}, false
	}
	if valuePlan.Funcs[0].Transport != coro.ManagedTransport {
		panic(fmt.Errorf("coroutine dynamic dispatch ABI: function value %q has Dispatch representation with non-managed transport %s", value.Name(), valuePlan.Funcs[0].Transport))
	}
	return p.emitCoroDynamicDispatchValue(b, value, valuePlan.Funcs[0], nil), true
}

func (p *context) tryCompileCoroPlainDispatchClosure(b llssa.Builder, closure *ssa.MakeClosure) (llssa.Expr, bool) {
	if p.compilation == nil || !p.compilation.EnableCoroPlainDispatch || p.compilation.CoroPlan == nil {
		return llssa.Expr{}, false
	}
	valuePlan, found := p.compilation.CoroPlan.ValuePlan(closure)
	if !found || len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 || valuePlan.Funcs[0].Rep != coro.Dispatch {
		return llssa.Expr{}, false
	}
	if valuePlan.Funcs[0].Transport != coro.ManagedTransport {
		panic(fmt.Errorf("coroutine dynamic dispatch ABI: closure %q has Dispatch representation with non-managed transport %s", closure.Name(), valuePlan.Funcs[0].Transport))
	}
	target, ok := closure.Fn.(*ssa.Function)
	if !ok || len(closure.Bindings) != len(target.FreeVars) {
		panic(fmt.Errorf("coroutine dynamic dispatch ABI: closure %q has %d bindings for %d target free variables", closure.Name(), len(closure.Bindings), len(target.FreeVars)))
	}
	bindings := p.compileValues(b, closure.Bindings, 0)
	return p.emitCoroDynamicDispatchValue(b, target, valuePlan.Funcs[0], bindings), true
}

func (p *context) emitCoroDynamicDispatchValue(
	b llssa.Builder, target *ssa.Function, leaf coro.FuncRepLeaf, bindings []llssa.Expr,
) llssa.Expr {
	if leaf.Transport != coro.ManagedTransport || leaf.Rep != coro.Dispatch {
		panic(fmt.Errorf("coroutine dynamic dispatch ABI: target %q requires managed Dispatch transport, got transport=%s representation=%s", target.Name(), leaf.Transport, leaf.Rep))
	}
	entry := p.mustFunctionSymbol(target)
	plannedTarget := false
	for _, targetID := range leaf.Targets {
		if targetID == entry.plan.ID {
			plannedTarget = true
			break
		}
	}
	if !plannedTarget {
		panic(fmt.Errorf("coroutine dynamic dispatch ABI: exact producer %q target %q is absent from its %d planned targets", target.Name(), entry.plan.ID, len(leaf.Targets)))
	}
	if err := validateCoroDynamicDispatchTarget(entry.function, entry.plan, p.compilation.EmissionUniverse); err != nil {
		panic(err)
	}
	abi, err := newCoroPlainDispatchABI(p, entry.function.Signature)
	if err != nil {
		panic(err)
	}
	compile := p.compileFunction
	if entry.plan.Emission == coro.EmitRawPlain {
		compile = p.compileRawPlainFunction
	}
	physical, py, ftype := compile(entry.function)
	if ftype != goFunc || physical == nil || py != nil {
		panic(fmt.Errorf("coroutine dynamic dispatch ABI: target %q did not compile as one Go function", entry.plan.ID))
	}
	var rawPhysical llssa.Function
	if entry.plan.Emission == coro.EmitCoroutine && p.compilation.CoroPlan.HasRawPlainVariant(entry.function) {
		// The managed primary and legacy-stack variant are distinct physical
		// capabilities of the same frozen SSA target. Publish the latter only
		// when the whole-build plan proves that exact function has an
		// independently validated raw body.
		rawPhysical, py, ftype = p.compileRawPlainFunction(entry.function)
		if ftype != goFunc || rawPhysical == nil || py != nil {
			panic(fmt.Errorf("coroutine dynamic dispatch ABI: target %q did not compile its frozen raw-plain variant as one Go function", entry.plan.ID))
		}
	}
	captured := len(entry.function.FreeVars) != 0
	if len(bindings) != len(entry.function.FreeVars) {
		panic(fmt.Errorf("coroutine dynamic dispatch ABI: target %q has %d bindings for %d free variables", entry.plan.ID, len(bindings), len(entry.function.FreeVars)))
	}
	var env llssa.Expr
	var closureCtx types.Type
	if captured {
		// Reuse the canonical LLGo closure allocator/layout instead of creating a
		// second environment representation. The selected physical primary may
		// be a coroutine ramp, so retag its opaque code pointer with the source
		// closure signature solely while MakeClosure constructs {code,env}; only
		// the env word is retained in the descriptor value.
		ctx := makeClosureCtx(entry.pkgTypes, entry.function.FreeVars)
		carrierSig := p.prog.PhysicalFuncDecl(llssa.FuncAddCtx(ctx, abi.signature), llssa.InGo)
		// Retag as an opaque function pointer rather than a declaration type:
		// LLVM functions themselves have a pointer value while FuncDecl.Type is
		// the pointee signature. No call is emitted through this temporary view.
		carrier := b.ChangeType(p.prog.Type(carrierSig, llssa.InC), physical.Expr)
		closureCtx = carrier.RawType().(*types.Signature).Params().At(0).Type()
		legacy := b.MakeClosure(carrier, bindings)
		env = b.Field(legacy, 1)
	}
	targetHash := sha256.Sum256([]byte(entry.plan.ID))
	targetKey := hex.EncodeToString(targetHash[:8]) + "." + hex.EncodeToString(abi.hash[:])
	result := p.prog.Type(abi.resultSlotType, llssa.InC)
	descriptorName := coroPlainDispatchDescriptorPrefix + targetKey
	descriptor, found := p.coroPlainDescriptors[descriptorName]
	if !found {
		flags := uint32(0)
		var plainEntry, coroEntry llssa.Expr
		switch entry.plan.Emission {
		case coro.EmitPlain, coro.EmitRawPlain:
			flags |= llssa.CoroDispatchFlagHasPlain
			plainEntry = p.newCoroDynamicDispatchEntryThunk(
				coroPlainDispatchThunkPrefix+targetKey, physical.Expr, abi, entry.plan.Emission, closureCtx,
			)
		case coro.EmitCoroutine:
			flags |= llssa.CoroDispatchFlagHasCoro
			coroEntry = p.newCoroDynamicDispatchEntryThunk(
				coroCoroDispatchThunkPrefix+targetKey, physical.Expr, abi, entry.plan.Emission, closureCtx,
			)
			if rawPhysical != nil {
				flags |= llssa.CoroDispatchFlagHasPlain
				plainEntry = p.newCoroDynamicDispatchEntryThunk(
					coroPlainDispatchThunkPrefix+targetKey, rawPhysical.Expr, abi, coro.EmitRawPlain, closureCtx,
				)
			}
		default:
			panic(fmt.Errorf("coroutine dynamic dispatch ABI: target %q has unsupported emission %s", entry.plan.ID, entry.plan.Emission))
		}
		if !captured {
			flags |= llssa.CoroDispatchFlagNoCapture
		}
		descriptor = p.pkg.NewCoroDispatchDescriptor(descriptorName, llssa.CoroDispatchDescriptorOptions{
			Version:    coroPlainDispatchVersion,
			Flags:      flags,
			ABIHash:    abi.hash,
			Signature:  abi.signature,
			PlainEntry: plainEntry,
			CoroEntry:  coroEntry,
			Result:     result,
		})
		if p.coroPlainDescriptors == nil {
			p.coroPlainDescriptors = make(map[string]llssa.Expr)
		}
		p.coroPlainDescriptors[descriptorName] = descriptor
	}
	return b.MakeCoroDispatchValue(abi.signature, descriptor, env)
}

// newCoroDynamicDispatchEntryThunk adapts the stable descriptor ABI to the
// selected single primary. Descriptor entries always receive an opaque env at
// a fixed position. A captured LLGo body instead expects its typed leading
// closure context, so the thunk converts and inserts env without cloning the
// body. A no-capture thunk simply drops env.
func (p *context) newCoroDynamicDispatchEntryThunk(
	name string,
	target llssa.Expr,
	abi coroPlainDispatchABI,
	emission coro.BodyEmission,
	closureCtx types.Type,
) llssa.Expr {
	if name == "" || target.IsNil() {
		panic("coroutine dynamic dispatch ABI: entry thunk requires a name and target")
	}
	source := p.prog.PhysicalFuncDecl(abi.signature, llssa.InGo)
	var thunkSig *types.Signature
	switch emission {
	case coro.EmitPlain, coro.EmitRawPlain:
		thunkSig = p.prog.CoroDispatchPlainEntrySignature(abi.signature)
	case coro.EmitCoroutine:
		thunkSig = p.prog.CoroDispatchCoroEntrySignature(abi.signature)
	default:
		panic(fmt.Errorf("coroutine dynamic dispatch ABI: thunk %q has unsupported emission %s", name, emission))
	}
	thunk := p.pkg.FuncOf(name)
	if thunk == nil {
		thunk = p.pkg.NewFunc(name, thunkSig, llssa.InC)
	} else if !types.Identical(thunk.RawType(), thunkSig) {
		panic(fmt.Errorf("coroutine dynamic dispatch ABI: thunk %q conflicts with an existing signature", name))
	}
	if thunk.HasBody() {
		return thunk.Expr
	}

	targetSig, ok := target.RawType().(*types.Signature)
	if !ok || targetSig.Variadic() {
		panic(fmt.Errorf("coroutine dynamic dispatch ABI: thunk %q target has no ordinary physical signature", name))
	}
	targetParam := 0
	thunkSourceBase := 1
	if emission == coro.EmitCoroutine {
		if targetSig.Results().Len() != 1 || !types.Identical(targetSig.Results().At(0).Type(), types.Typ[types.UnsafePointer]) {
			panic(fmt.Errorf("coroutine dynamic dispatch ABI: thunk %q target does not return one handle", name))
		}
		for i := 0; i < 2; i++ {
			if targetSig.Params().Len() <= targetParam || !types.Identical(targetSig.Params().At(targetParam).Type(), types.Typ[types.UnsafePointer]) {
				panic(fmt.Errorf("coroutine dynamic dispatch ABI: thunk %q target hidden parameter %d is not unsafe.Pointer", name, i))
			}
			targetParam++
		}
		thunkSourceBase = 3
	} else if !types.Identical(targetSig.Results(), source.Results()) {
		panic(fmt.Errorf("coroutine dynamic dispatch ABI: thunk %q target result signature does not match the source ABI", name))
	}
	if closureCtx != nil {
		if targetSig.Params().Len() <= targetParam || !types.Identical(targetSig.Params().At(targetParam).Type(), closureCtx) {
			panic(fmt.Errorf("coroutine dynamic dispatch ABI: thunk %q target closure context is absent or has the wrong type", name))
		}
		targetParam++
	}
	if targetSig.Params().Len()-targetParam != source.Params().Len() {
		panic(fmt.Errorf("coroutine dynamic dispatch ABI: thunk %q target has %d source parameters, want %d", name, targetSig.Params().Len()-targetParam, source.Params().Len()))
	}
	for i := 0; i < source.Params().Len(); i++ {
		if !types.Identical(targetSig.Params().At(targetParam+i).Type(), source.Params().At(i).Type()) {
			panic(fmt.Errorf("coroutine dynamic dispatch ABI: thunk %q target source parameter %d has the wrong type", name, i))
		}
	}
	b := thunk.MakeBody(1)
	physicalArgs := make([]llssa.Expr, 0, source.Params().Len()+3)
	if emission == coro.EmitCoroutine {
		physicalArgs = append(physicalArgs, thunk.PhysicalParam(0), thunk.PhysicalParam(1))
	}
	if closureCtx != nil {
		envIndex := 0
		if emission == coro.EmitCoroutine {
			envIndex = 2
		}
		physicalArgs = append(physicalArgs, b.Convert(p.prog.Type(closureCtx, llssa.InC), thunk.PhysicalParam(envIndex)))
	}
	for i := 0; i < source.Params().Len(); i++ {
		physicalArgs = append(physicalArgs, thunk.PhysicalParam(thunkSourceBase+i))
	}
	ret := b.Call(target, physicalArgs...)
	if targetSig.Results().Len() == 0 {
		b.Return()
	} else {
		b.Return(ret)
	}
	b.EndBuild()
	b.Dispose()
	return thunk.Expr
}

func (p *context) tryCompileCoroPlainDispatchCall(b llssa.Builder, call *ssa.Call) (llssa.Expr, bool) {
	if p.compilation == nil || !p.compilation.EnableCoroPlainDispatch || p.compilation.CoroPlan == nil || call == nil {
		return llssa.Expr{}, false
	}
	callPlan, found := p.compilation.CoroPlan.CallPlan(call)
	if !found || callPlan.Rep != coro.Dispatch {
		return llssa.Expr{}, false
	}
	if callPlan.Transport != coro.ManagedTransport {
		panic(fmt.Errorf("coroutine dynamic dispatch ABI: call %q has Dispatch representation with non-managed transport %s", call.String(), callPlan.Transport))
	}
	if p.compilation.coroClosedInterfacePlain.acceptsCall(call) {
		// Preserve the ordinary LLGo itab invoke. The closed candidate proof is
		// a scheduling constraint, not a second function-value representation.
		return llssa.Expr{}, false
	}
	if err := validateCoroPlainDispatchCall(p.compilation.CoroPlan, call.Parent(), call, callPlan, p.compilation.EmissionUniverse); err != nil {
		panic(err)
	}
	p.recordCallerLocationForCall(b, &call.Call)
	p.emitPCLineLabel(b, call.Pos())
	fn := p.compileValue(b, call.Call.Value)
	args := p.compileValues(b, call.Call.Args, fnNormal)
	abi, err := newCoroPlainDispatchABI(p, call.Call.Signature())
	if err != nil {
		panic(err)
	}
	result := p.prog.Type(abi.resultSlotType, llssa.InC)
	opts := llssa.CoroPlainDispatchCallOptions{
		Version: coroPlainDispatchVersion,
		Flags:   coroPlainDispatchFlags,
		ABIHash: abi.hash,
		Result:  result,
	}
	// Go evaluates the callee and arguments before a nil-function panic. In a
	// physical coroutine, own that edge through the explicit-status fault ABI
	// so this compiler-generated descriptor operation cannot introduce a
	// hidden runtime.AssertNilDeref dependency after emission closure.
	if p.currentCoro != nil {
		p.compileCoroImplicitNilAccessGuard(b, b.Field(fn, 0))
		opts.DescriptorNonNil = true
	}
	return b.CallCoroPlainDispatch(fn, args, opts), true
}
