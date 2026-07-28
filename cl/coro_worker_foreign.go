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
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"strconv"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const coroWorkerForeignThunkPrefixV1 = "__llgo_coro_worker_foreign_thunk_v1_"

type coroForeignCallMode uint8

const (
	coroForeignCallModeWorker coroForeignCallMode = iota
	coroForeignCallModeSameM
)

type coroWorkerForeignCallShape struct {
	target       *ssa.Function
	calleeType   types.Type
	calleeField  int
	signature    *types.Signature
	record       *types.Struct
	argumentBase int
	arguments    []ssa.Value
	argc         int
	result       types.Type
	resultField  int
	rawCallbacks map[int]*ssa.Function
	// reentryCallbacks is frozen call-site information, not an address
	// registry. Each entry is one exact non-capturing Go target whose typed C
	// adapter is generated directly from this shape.
	reentryCallbacks map[int]*ssa.Function
	mode             coroForeignCallMode
	nilGuard         bool
	variadic         bool
}

func coroWorkerTypeParamLen(list *types.TypeParamList) int {
	if list == nil {
		return 0
	}
	return list.Len()
}

func coroWorkerWordType(typ types.Type, pointerSize int) bool {
	if typ == nil || pointerSize <= 0 {
		return false
	}
	underlying := types.Unalias(typ).Underlying()
	switch underlying := underlying.(type) {
	case *types.Pointer:
		return true
	case *types.Basic:
		if underlying.Kind() == types.UnsafePointer {
			return true
		}
		if underlying.Info()&types.IsInteger == 0 || underlying.Info()&types.IsUntyped != 0 {
			return false
		}
		sizes := &types.StdSizes{WordSize: int64(pointerSize), MaxAlign: int64(pointerSize)}
		size := sizes.Sizeof(typ)
		return size > 0 && size <= int64(pointerSize)
	default:
		return false
	}
}

// coroWorkerArgumentWordType additionally admits an explicitly C-background
// named callback. LLGo represents such a value as one raw C function pointer,
// not as a managed Go closure/descriptor. This is needed for registration APIs
// that transport—but do not invoke—the callback during the worker call. Plain
// Go function types remain rejected.
func coroWorkerArgumentWordType(universe *EmissionUniverse, typ types.Type, pointerSize int) bool {
	if coroWorkerWordType(typ, pointerSize) {
		return true
	}
	if universe == nil || universe.prog == nil || pointerSize <= 0 ||
		universe.prog.TypeBackground(typ) != llssa.InC {
		return false
	}
	signature, ok := types.Unalias(typ).Underlying().(*types.Signature)
	return ok && signature != nil && !signature.Variadic()
}

// coroWorkerResultWordType deliberately excludes pointer-shaped results from
// the address-only llgo.syscall transport. That path has no typed declaration
// whose contract can prove foreign-or-borrowed pointer provenance.
func coroWorkerResultWordType(typ types.Type, pointerSize int) bool {
	if typ == nil || pointerSize <= 0 {
		return false
	}
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	if !ok || basic.Info()&types.IsInteger == 0 || basic.Info()&types.IsUntyped != 0 {
		return false
	}
	sizes := &types.StdSizes{WordSize: int64(pointerSize), MaxAlign: int64(pointerSize)}
	size := sizes.Sizeof(typ)
	return size > 0 && size <= int64(pointerSize)
}

// coroWorkerForeignRecordValueType accepts values whose exact typed
// representation can live in the compiler-owned call record until the worker
// publishes completion. Ordinary Go descriptors (slice, bare string,
// interface, map, chan, or Go function values) are rejected: copying their
// bits into a C call would neither be a valid C ABI nor establish their
// managed lifetime. An explicit string type alias in an exact C declaration is
// different: bindings use that source form for two-word by-value C++ views
// such as std::string_view. The typed record preserves that already-supported
// direct-call ABI and stays live through the worker acknowledgement.
//
// Arguments may contain pointers because the record itself remains a typed,
// traced coroutine-frame object and the ordinary call-site retention proof
// keeps every independent owner live. Pointer-bearing results are admitted
// only by callers that independently proved an exact non-reentrant C
// declaration (or generated cgo adapter): such a result is foreign storage or
// borrowed from an input whose owner remains in the record until completion.
// Dynamic raw C pointers have no declaration provenance and pass argument=false.
func coroWorkerForeignRecordValueType(
	universe *EmissionUniverse,
	typ types.Type,
	argument bool,
	visiting map[types.Type]bool,
) bool {
	if universe == nil || typ == nil {
		return false
	}
	sourceType := typ
	typ = types.Unalias(typ)
	if visiting[typ] {
		// Recursive C values are necessarily recursive through a pointer, which
		// is handled before descending. Treat any other cycle as malformed.
		return false
	}
	switch underlying := typ.Underlying().(type) {
	case *types.Basic:
		if underlying.Info()&types.IsUntyped != 0 {
			return false
		}
		if underlying.Kind() == types.String {
			_, explicitForeignView := sourceType.(*types.Alias)
			return argument && explicitForeignView
		}
		if underlying.Kind() == types.UnsafePointer {
			return argument
		}
		return underlying.Info()&(types.IsBoolean|types.IsInteger|types.IsFloat|types.IsComplex) != 0
	case *types.Pointer:
		return argument
	case *types.Signature:
		return argument && universe.prog != nil &&
			universe.prog.TypeBackground(typ) == llssa.InC &&
			!underlying.Variadic()
	case *types.Array:
		visiting[typ] = true
		ok := coroWorkerForeignRecordValueType(universe, underlying.Elem(), argument, visiting)
		delete(visiting, typ)
		return ok
	case *types.Struct:
		visiting[typ] = true
		for index := 0; index < underlying.NumFields(); index++ {
			if !coroWorkerForeignRecordValueType(
				universe, underlying.Field(index).Type(), argument, visiting,
			) {
				delete(visiting, typ)
				return false
			}
		}
		delete(visiting, typ)
		return true
	default:
		return false
	}
}

// coroWorkerForeignRecordArgumentValue admits an ordinary Go function type
// only when whole-program planning has already converted this exact argument
// occurrence into a closed raw-C code pointer and retained its raw/plain body.
// This is the static-C-parameter analogue of an explicit //llgo:type C named
// callback; arbitrary funcvals, closures with an environment, and dynamic
// target sets remain rejected.
func coroWorkerForeignRawCallbackArgument(
	plan *coro.SSAPlan,
	value ssa.Value,
	typ types.Type,
) (rawTarget *ssa.Function, valid bool) {
	signature, functionType := types.Unalias(typ).Underlying().(*types.Signature)
	if !functionType || signature == nil || signature.Variadic() ||
		plan == nil || value == nil {
		return nil, false
	}
	valuePlan, planned := plan.ValuePlan(value)
	if !planned || len(valuePlan.Funcs) != 1 {
		return nil, false
	}
	leaf := valuePlan.Funcs[0]
	if len(leaf.Path) != 0 || leaf.Rep != coro.DirectPlain ||
		leaf.MayBeNil || len(leaf.Targets) != 1 {
		return nil, false
	}
	target, found := plan.Function(leaf.Targets[0])
	if !found || target == nil || len(target.FreeVars) != 0 {
		return nil, false
	}
	targetPlan, found := plan.FunctionPlan(target)
	if !found || targetPlan.External != coro.Defined ||
		!targetPlan.RawPlainDemand || !plan.HasRawPlainVariant(target) {
		return nil, false
	}
	return target, true
}

func coroWorkerForeignRecordType(
	signature *types.Signature,
	result types.Type,
) (record *types.Struct, resultField int) {
	record, resultField, _ = coroWorkerForeignRecordLayout(signature, result, nil)
	return record, resultField
}

func coroWorkerForeignRecordLayout(
	signature *types.Signature,
	result types.Type,
	calleeType types.Type,
) (record *types.Struct, resultField, argumentBase int) {
	if signature == nil {
		panic("coroutine worker foreign record requires a signature")
	}
	params := signature.Params()
	paramCount := 0
	if params != nil {
		paramCount = params.Len()
	}
	fields := make([]*types.Var, 0, paramCount+2)
	if calleeType != nil {
		fields = append(fields, types.NewField(token.NoPos, nil, "callee", calleeType, false))
	}
	argumentBase = len(fields)
	for index := 0; index < paramCount; index++ {
		fields = append(fields, types.NewField(
			token.NoPos, nil, fmt.Sprintf("a%d", index), params.At(index).Type(), false,
		))
	}
	resultField = -1
	if result != nil {
		resultField = len(fields)
		fields = append(fields, types.NewField(token.NoPos, nil, "result", result, false))
	}
	if len(fields) == 0 {
		// A concrete byte keeps the record address non-zero and stable even for
		// a void zero-argument target.
		fields = append(fields, types.NewField(token.NoPos, nil, "reserved", types.Typ[types.Uint8], false))
	}
	return types.NewStruct(fields, nil), resultField, argumentBase
}

// coroWorkerForeignVariadicValues mirrors compileVArg's frontend-owned
// __llgo_va_list expansion without constructing LLVM values. x/tools lowers
// every concrete variadic list to one synthetic [N]any allocation followed by
// constant-index stores in the call block. Freeze those unboxed operands into
// the call-site-specific typed worker record; arbitrary Go []any values remain
// unsupported because neither ordinary LLGo C lowering nor this adapter can
// recover their dynamic ABI safely.
func coroWorkerForeignVariadicValues(
	ctx *context,
	call *ssa.Call,
	value ssa.Value,
) ([]ssa.Value, error) {
	switch value := value.(type) {
	case *ssa.Const:
		if value.Value == nil {
			return nil, nil
		}
	case *ssa.Parameter:
		if value.Parent() != nil && llssa.HasNameValist(value.Parent().Signature) {
			return nil, nil
		}
	case *ssa.Slice:
		alloc, ok := value.X.(*ssa.Alloc)
		if !ok || !emissionIsVargsAlloc(ctx, alloc) {
			break
		}
		pointer, ok := types.Unalias(alloc.Type()).Underlying().(*types.Pointer)
		if !ok {
			break
		}
		array, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Array)
		if !ok || array.Len() < 0 || uint64(array.Len()) > uint64(^uint(0)>>1) {
			break
		}
		if call == nil || call.Block() == nil || alloc.Parent() != call.Parent() {
			return nil, fmt.Errorf("synthetic varargs allocation and call have different owners")
		}
		values := make([]ssa.Value, int(array.Len()))
		for _, instruction := range call.Block().Instrs {
			if instruction == call {
				break
			}
			store, ok := instruction.(*ssa.Store)
			if !ok {
				continue
			}
			address, ok := store.Addr.(*ssa.IndexAddr)
			if !ok || address.X != alloc {
				continue
			}
			index, ok := address.Index.(*ssa.Const)
			if !ok || index.Value == nil {
				return nil, fmt.Errorf("synthetic varargs store has a non-constant index")
			}
			slot, exact := constant.Int64Val(index.Value)
			if !exact || slot < 0 || slot >= int64(len(values)) {
				return nil, fmt.Errorf("synthetic varargs store index %v is outside [0,%d)", index.Value, len(values))
			}
			if values[slot] != nil {
				return nil, fmt.Errorf("synthetic varargs slot %d has multiple stores", slot)
			}
			concrete := store.Val
			if boxed, ok := concrete.(*ssa.MakeInterface); ok {
				concrete = boxed.X
			}
			if concrete == nil {
				return nil, fmt.Errorf("synthetic varargs slot %d has no concrete operand", slot)
			}
			values[slot] = concrete
		}
		for index, concrete := range values {
			if concrete == nil {
				return nil, fmt.Errorf("synthetic varargs slot %d has no dominating store", index)
			}
		}
		return values, nil
	}
	return nil, fmt.Errorf("unsupported __llgo_va_list lowering shape %T", value)
}

func coroWorkerForeignSpecializedSignature(
	ctx *context,
	call *ssa.Call,
	target *ssa.Function,
	signature *types.Signature,
) (*types.Signature, []ssa.Value, bool, error) {
	if ctx == nil || call == nil || call.Common() == nil || target == nil || signature == nil {
		return nil, nil, false, fmt.Errorf("variadic specialization requires an exact context, call, target, and signature")
	}
	common := call.Common()
	if !target.Signature.Variadic() {
		return signature, common.Args, false, nil
	}
	if !llssa.HasNameValist(target.Signature) {
		return nil, nil, false, fmt.Errorf(
			"variadic C declaration requires the trailing %s ...any ABI",
			llssa.NameValist,
		)
	}
	if signature.Params() == nil || signature.Params().Len() == 0 ||
		len(common.Args) != signature.Params().Len() {
		return nil, nil, false, fmt.Errorf("variadic C declaration and call have different fixed SSA shapes")
	}
	fixed := signature.Params().Len() - 1
	varargs, err := coroWorkerForeignVariadicValues(ctx, call, common.Args[fixed])
	if err != nil {
		return nil, nil, false, err
	}
	arguments := make([]ssa.Value, 0, fixed+len(varargs))
	arguments = append(arguments, common.Args[:fixed]...)
	arguments = append(arguments, varargs...)
	params := make([]*types.Var, 0, len(arguments))
	for index := 0; index < fixed; index++ {
		parameter := signature.Params().At(index)
		params = append(params, types.NewParam(
			parameter.Pos(), parameter.Pkg(), parameter.Name(), parameter.Type(),
		))
	}
	for index, argument := range varargs {
		params = append(params, types.NewParam(
			token.NoPos, nil, fmt.Sprintf("va%d", index), ctx.patchType(argument.Type()),
		))
	}
	return types.NewSignatureType(
		nil, nil, nil, types.NewTuple(params...), signature.Results(), false,
	), arguments, true, nil
}

// coroStaticForeignCallAuthority is the one frozen-plan read capability for a
// static foreign edge. Worker routing, managed callback routing, and callback
// target recovery must agree here instead of independently re-reading compiler
// state.
type coroStaticForeignCallAuthority struct {
	plan           *coro.SSAPlan
	universe       *EmissionUniverse
	libraryForeign map[*ssa.Function]coro.LibraryEffectForeignCallable
}

type coroStaticForeignCallAuthorization struct {
	mode    coroForeignCallMode
	reentry coro.ReentryClass
}

// authorize accepts exactly one of the legacy worker certificate and the
// target-neutral callable declaration contract. Plan and frontend
// certificates are compared as complete values; an ID match alone cannot hide
// stale behavior or physical-ABI fields.
func (a coroStaticForeignCallAuthority) authorize(
	target *ssa.Function,
) (coroStaticForeignCallAuthorization, error) {
	reject := coroStaticForeignCallAuthorization{}
	if a.plan == nil || a.universe == nil || target == nil {
		return reject, fmt.Errorf(
			"requires an exact coroutine plan, emission universe, and foreign target",
		)
	}

	planLegacy, planLegacyCertified := a.plan.ForeignWorkerCertificate(target)
	universeLegacy, universeLegacyCertified, legacyErr :=
		a.universe.CoroForeignWorkerCertificate(target)
	if legacyErr != nil {
		return reject, fmt.Errorf(
			"resolve frozen legacy worker-safe certificate: %w", legacyErr,
		)
	}
	planCallable, planCallableCertified :=
		a.plan.CallableContractCertificate(target)
	universeCallable, universeCallableCertified, callableErr :=
		a.universe.CoroCallableContractCertificate(target)
	if callableErr != nil {
		return reject, fmt.Errorf(
			"resolve frozen callable contract certificate: %w", callableErr,
		)
	}
	if imported, ok := a.libraryForeign[target]; ok {
		if err := imported.Validate(); err != nil {
			return reject, fmt.Errorf(
				"resolve imported library foreign callable: %w", err,
			)
		}
		planIdentity, identityPlanned := a.plan.CallableIdentityCertificate(target)
		if !identityPlanned || planIdentity != imported.Identity {
			return reject, fmt.Errorf(
				"imported library foreign callable identity differs from the coroutine plan",
			)
		}
		if imported.HasContract {
			universeCallable = imported.Contract
			universeCallableCertified = true
		} else {
			universeCallable = CoroCallableContractCertificate{}
			universeCallableCertified = false
		}
	}

	legacyPresent := planLegacyCertified || planLegacy != "" ||
		universeLegacyCertified ||
		universeLegacy != (CoroForeignWorkerCertificate{})
	callablePresent := planCallableCertified || !planCallable.IsZero() ||
		universeCallableCertified || !universeCallable.IsZero()
	if legacyPresent && callablePresent {
		return reject, fmt.Errorf(
			"generic callable contract and legacy worker-safe certificates are mutually exclusive",
		)
	}
	if legacyPresent {
		if !planLegacyCertified || planLegacy == "" {
			return reject, fmt.Errorf(
				"target has no exact legacy worker-safe certificate in the coroutine plan",
			)
		}
		if !universeLegacyCertified || universeLegacy.ID == "" ||
			universeLegacy.PhysicalSymbol == "" ||
			universeLegacy.ABISignature == "" {
			return reject, fmt.Errorf(
				"target has no exact legacy worker-safe certificate in the frozen emission universe",
			)
		}
		if planLegacy != universeLegacy.ID {
			return reject, fmt.Errorf(
				"legacy worker-safe certificate identity differs between the coroutine plan and frozen emission universe",
			)
		}
		return coroStaticForeignCallAuthorization{
			mode: coroForeignCallModeWorker, reentry: coro.ReentryNone,
		}, nil
	}

	if !callablePresent {
		return reject, fmt.Errorf(
			"target has no exact worker-safe certificate or compatible callable declaration contract",
		)
	}
	if !planCallableCertified || planCallable.IsZero() {
		return reject, fmt.Errorf(
			"target has no exact callable contract certificate in the coroutine plan",
		)
	}
	if !universeCallableCertified || universeCallable.IsZero() {
		return reject, fmt.Errorf(
			"target has no exact callable contract certificate in the frozen emission universe",
		)
	}
	if planCallable != universeCallable {
		return reject, fmt.Errorf(
			"callable contract certificate differs between the coroutine plan and frozen emission universe",
		)
	}
	if err := universeCallable.Validate(); err != nil {
		return reject, fmt.Errorf(
			"invalid callable contract certificate: %w", err,
		)
	}
	if universeCallable.Scope != coro.CallableContractScopeDeclaration {
		return reject, fmt.Errorf(
			"callable contract scope %q does not authorize a typed C declaration",
			universeCallable.Scope,
		)
	}
	if universeCallable.CallableABIExplicit {
		if _, addressOnly := parseCoroWorkerWordCallableABI(
			universeCallable.CallableABI,
		); addressOnly {
			return reject, fmt.Errorf(
				"callable ABI %q is address-only and may be consumed only by the FuncPCABI0-to-llgo.syscall worker path, not an ordinary typed foreign call",
				universeCallable.CallableABI,
			)
		}
	}
	contract := universeCallable.Contract
	if contract.Progress != coro.ProgressMayBlock {
		return reject, fmt.Errorf(
			"callable progress %q does not authorize synchronous blocking foreign lowering; require %q",
			contract.Progress, coro.ProgressMayBlock,
		)
	}
	switch contract.Memory {
	case coro.MemoryByValue, coro.MemoryBorrowUntilReturn,
		coro.MemoryBorrowUntilComplete:
	default:
		return reject, fmt.Errorf(
			"callable memory lifetime %q does not authorize synchronous foreign transport",
			contract.Memory,
		)
	}
	switch contract.Affinity {
	case coro.AffinityAnyThread:
		switch contract.Reentry {
		case coro.ReentryNone:
			return coroStaticForeignCallAuthorization{
				mode: coroForeignCallModeWorker, reentry: contract.Reentry,
			}, nil
		case coro.ReentryManagedCallback:
			// A synchronous managed callback needs the parent M's native
			// stack and replacement-M handoff even though the C implementation
			// itself is otherwise thread-independent.
			return coroStaticForeignCallAuthorization{
				mode: coroForeignCallModeSameM, reentry: contract.Reentry,
			}, nil
		}
	case coro.AffinityCallerThread:
		switch contract.Reentry {
		case coro.ReentryNone, coro.ReentryManagedCallback:
			return coroStaticForeignCallAuthorization{
				mode: coroForeignCallModeSameM, reentry: contract.Reentry,
			}, nil
		}
	default:
		return reject, fmt.Errorf(
			"callable affinity %q does not identify a supported synchronous foreign route",
			contract.Affinity,
		)
	}
	return reject, fmt.Errorf(
		"callable reentry %q does not identify a supported synchronous foreign route",
		contract.Reentry,
	)
}

// managedCallbackArgument recovers one exact callback from the frozen value
// plan. No function pointer is reverse-mapped at runtime.
func (a coroStaticForeignCallAuthority) managedCallbackArgument(
	value ssa.Value,
	typ types.Type,
) (target *ssa.Function, valid bool, err error) {
	signature, functionType := types.Unalias(typ).Underlying().(*types.Signature)
	if !functionType || signature == nil || signature.Variadic() ||
		a.plan == nil || a.universe == nil || value == nil {
		return nil, false, nil
	}
	source := value
	for {
		switch converted := source.(type) {
		case *ssa.ChangeType:
			source = converted.X
		case *ssa.Convert:
			source = converted.X
		default:
			goto unwrapped
		}
	}

unwrapped:
	static, exact := source.(*ssa.Function)
	if !exact || static == nil || len(static.FreeVars) != 0 {
		if closure, ok := source.(*ssa.MakeClosure); ok &&
			len(closure.Bindings) == 0 {
			static, exact = closure.Fn.(*ssa.Function)
		}
	}
	if !exact || static == nil || len(static.FreeVars) != 0 {
		return nil, false, nil
	}
	canonical, resolved := a.universe.Resolve(static)
	if !resolved || canonical == nil || len(canonical.FreeVars) != 0 {
		return nil, false, nil
	}
	targetID, identified := a.plan.FunctionID(canonical)
	valuePlan, planned := a.plan.ValuePlan(source)
	if !identified || !planned || len(valuePlan.Funcs) != 1 {
		return nil, false, nil
	}
	leaf := valuePlan.Funcs[0]
	if len(leaf.Path) != 0 || leaf.Transport != coro.ManagedTransport ||
		leaf.MayBeNil || len(leaf.Targets) != 1 || leaf.Targets[0] != targetID {
		return nil, false, nil
	}
	targetPlan, planned := a.plan.FunctionPlan(canonical)
	if !planned || targetPlan.External != coro.Defined ||
		targetPlan.ManagedDemand == coro.NoDemand {
		return nil, false, fmt.Errorf(
			"callback target %q has no managed callback entry demand (external=%s emission=%s primary=%s demand=%s effect=%s)",
			targetID,
			targetPlan.External,
			targetPlan.Emission,
			targetPlan.Primary,
			targetPlan.ManagedDemand,
			targetPlan.Effect,
		)
	}
	if targetPlan.Effect.MaySuspend() {
		if targetPlan.Emission != coro.EmitCoroutine ||
			targetPlan.Primary != coro.PrimaryCoroutine {
			return nil, false, fmt.Errorf(
				"callback target %q has no inferred coroutine primary (emission=%s primary=%s effect=%s)",
				targetID, targetPlan.Emission, targetPlan.Primary,
				targetPlan.Effect,
			)
		}
	} else {
		const unsupportedPlain = coro.BlockForeign | coro.ThreadAffine |
			coro.NeedsPreempt | coro.MayUnwind | coro.NoReturn |
			coro.PanicOnly | coro.OpaqueExec
		if targetPlan.Emission != coro.EmitPlain ||
			targetPlan.Primary != coro.PrimaryPlain ||
			targetPlan.Exec&unsupportedPlain != 0 {
			return nil, false, fmt.Errorf(
				"plain callback target %q cannot use the thin reentry ramp (emission=%s primary=%s exec=%s)",
				targetID, targetPlan.Emission, targetPlan.Primary,
				targetPlan.Exec,
			)
		}
	}
	targetSignature, signatureErr :=
		a.universe.coroPhysicalSourceSignature(canonical)
	if signatureErr != nil {
		return nil, false, fmt.Errorf(
			"derive callback target %q signature: %w", targetID, signatureErr,
		)
	}
	if targetSignature == nil || targetSignature.Recv() != nil ||
		!types.Identical(
			coroPhysicalNormalizeSourceSignature(signature),
			coroPhysicalNormalizeSourceSignature(targetSignature),
		) {
		return nil, false, fmt.Errorf(
			"callback target %q and C parameter signatures differ", targetID,
		)
	}
	results := targetSignature.Results()
	if results != nil && results.Len() > 1 {
		return nil, false, fmt.Errorf(
			"callback target %q requires zero or one C result", targetID,
		)
	}
	return canonical, true, nil
}

// validateCoroWorkerForeignCall recognizes either an ordinary closed
// CallForeign edge to one exact frontend C declaration or an ordinary dynamic
// RawCCodePointer call. Both use the same typed record and bounded worker
// protocol. A dynamic raw pointer carries no declaration to authorize; its
// frontend-frozen //llgo:type C transport is the exact capability, and its
// conservative WaitForeign effect remains unchanged.
func validateCoroWorkerForeignCall(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	call *ssa.Call,
	pointerSize int,
) (shape coroWorkerForeignCallShape, recognized bool, err error) {
	return validateCoroWorkerForeignCallWithAuthority(
		coroStaticForeignCallAuthority{plan: plan, universe: universe},
		call, pointerSize,
	)
}

func validateCoroWorkerForeignCallWithAuthority(
	authority coroStaticForeignCallAuthority,
	call *ssa.Call,
	pointerSize int,
) (shape coroWorkerForeignCallShape, recognized bool, err error) {
	plan, universe := authority.plan, authority.universe
	shape.resultField = -1
	if plan == nil || universe == nil || call == nil || call.Common() == nil {
		return shape, false, nil
	}
	callPlan, planned := plan.CallPlan(call)
	if !planned || callPlan.Kind != coro.CallForeign {
		return shape, false, nil
	}
	recognized = true
	if pointerSize <= 0 {
		return shape, true, fmt.Errorf("target pointer width is unavailable")
	}
	common := call.Common()
	if call.Parent() == nil {
		return shape, true, fmt.Errorf("call has no exact SSA owner")
	}
	if callPlan.Transport == coro.RawCCodePointer {
		return validateCoroWorkerDynamicForeignCall(plan, universe, call, callPlan)
	}
	raw := common.StaticCallee()
	if raw == nil || common.IsInvoke() || common.Method != nil {
		return shape, true, fmt.Errorf("requires one exact static ordinary call")
	}
	if callPlan.Open || callPlan.MayBeNil || callPlan.Rep != coro.DirectPlain || len(callPlan.Targets) != 1 {
		return shape, true, fmt.Errorf(
			"requires one closed non-nil direct-plain target, got open=%t may-be-nil=%t representation=%s targets=%d",
			callPlan.Open, callPlan.MayBeNil, callPlan.Rep, len(callPlan.Targets),
		)
	}
	target, frozen := universe.Resolve(raw)
	if !frozen || target == nil {
		return shape, true, fmt.Errorf("static target is absent from the frozen emission universe")
	}
	plannedTarget, ok := plan.Function(callPlan.Targets[0])
	if !ok || plannedTarget == nil || plannedTarget != target {
		return shape, true, fmt.Errorf("call target %q does not identify the frozen static declaration", callPlan.Targets[0])
	}
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.ID != callPlan.Targets[0] {
		return shape, true, fmt.Errorf("target has no canonical function plan")
	}
	background, classified, backgroundErr := universe.FunctionBackground(target)
	if backgroundErr != nil {
		return shape, true, fmt.Errorf("classify target frontend ABI: %w", backgroundErr)
	}
	if !classified || background != llssa.InC {
		return shape, true, fmt.Errorf("target is not one exact frontend C declaration")
	}
	authorization, authorizationErr := authority.authorize(target)
	if authorizationErr != nil {
		return shape, true, authorizationErr
	}
	mode := authorization.mode
	if targetPlan.External != coro.ExternalUnknownForeign || targetPlan.Emission != coro.EmitExternal ||
		targetPlan.Effect != coro.NoSuspend || targetPlan.Exec != coro.BlockForeign|coro.IRQUnsafe {
		return shape, true, fmt.Errorf(
			"target %q is not an exact blocking foreign declaration (external=%s emission=%s effect=%s exec=%s)",
			targetPlan.ID, targetPlan.External, targetPlan.Emission, targetPlan.Effect, targetPlan.Exec,
		)
	}
	if target.Signature == nil ||
		coroWorkerTypeParamLen(target.Signature.TypeParams()) != 0 ||
		coroWorkerTypeParamLen(target.Signature.RecvTypeParams()) != 0 ||
		len(target.FreeVars) != 0 || target.Origin() != nil || len(target.TypeArgs()) != 0 {
		return shape, true, fmt.Errorf("target is not a non-variadic, non-generic C declaration")
	}
	signature, signatureErr := universe.coroPhysicalSourceSignature(target)
	if signatureErr != nil {
		return shape, true, fmt.Errorf("derive target effective signature: %w", signatureErr)
	}
	if signature == nil || signature.Recv() != nil || signature.Variadic() {
		return shape, true, fmt.Errorf("requires a non-variadic signature")
	}
	owner := universe.ownerOf(call.Parent())
	ownerContext, contextErr := universe.functionABIContext(call.Parent(), owner)
	if contextErr != nil {
		return shape, true, fmt.Errorf("derive call-site effective signature: %w", contextErr)
	}
	callSignature, ok := ownerContext.patchType(common.Signature()).(*types.Signature)
	if !ok || !types.Identical(coroPhysicalNormalizeSourceSignature(callSignature), signature) {
		return shape, true, fmt.Errorf("call-site and target effective C signatures differ")
	}
	recordSignature, arguments, variadic, specializationErr := coroWorkerForeignSpecializedSignature(
		ownerContext, call, target, signature,
	)
	if specializationErr != nil {
		return shape, true, specializationErr
	}
	shape.argc = len(arguments)
	shape.variadic = variadic
	parameterCount := 0
	if recordSignature.Params() != nil {
		parameterCount = recordSignature.Params().Len()
	}
	if shape.argc != parameterCount {
		return shape, true, fmt.Errorf("specialized worker signature argument count differs from the call site")
	}
	for index, argument := range arguments {
		if argument == nil {
			return shape, true, fmt.Errorf("argument %d is nil", index)
		}
		argumentType := ownerContext.patchType(argument.Type())
		parameterType := recordSignature.Params().At(index).Type()
		if !types.Identical(argumentType, parameterType) {
			return shape, true, fmt.Errorf("argument %d type does not match the effective C parameter", index)
		}
		_, callbackParameter := types.Unalias(parameterType).Underlying().(*types.Signature)
		if mode == coroForeignCallModeSameM &&
			authorization.reentry == coro.ReentryManagedCallback &&
			callbackParameter {
			callback, valid, callbackErr :=
				authority.managedCallbackArgument(argument, parameterType)
			if callbackErr != nil {
				return shape, true, fmt.Errorf("argument %d managed callback: %w", index, callbackErr)
			}
			if !valid {
				return shape, true, fmt.Errorf(
					"argument %d requires one exact non-capturing managed callback target",
					index,
				)
			}
			if shape.reentryCallbacks == nil {
				shape.reentryCallbacks = make(map[int]*ssa.Function)
			}
			shape.reentryCallbacks[index] = callback
			continue
		}
		argumentOK := coroWorkerForeignRecordValueType(
			universe, parameterType, true, make(map[types.Type]bool),
		)
		var rawCallback *ssa.Function
		if !argumentOK && mode == coroForeignCallModeWorker {
			rawCallback, argumentOK = coroWorkerForeignRawCallbackArgument(
				plan, argument, parameterType,
			)
		}
		if !argumentOK {
			valuePlan, valuePlanned := plan.ValuePlan(argument)
			return shape, true, fmt.Errorf(
				"argument %d type %s cannot be represented in a typed worker call record (value-plan=%+v present=%t)",
				index, parameterType, valuePlan.Funcs, valuePlanned,
			)
		}
		if rawCallback != nil {
			if shape.rawCallbacks == nil {
				shape.rawCallbacks = make(map[int]*ssa.Function)
			}
			shape.rawCallbacks[index] = rawCallback
		}
	}
	if mode == coroForeignCallModeSameM &&
		authorization.reentry == coro.ReentryManagedCallback &&
		len(shape.reentryCallbacks) == 0 {
		return shape, true, fmt.Errorf(
			"managed callback declaration has no exact function-typed callback argument",
		)
	}
	results := recordSignature.Results()
	if results != nil && results.Len() > 1 {
		return shape, true, fmt.Errorf("requires zero or one result")
	}
	if results != nil && results.Len() == 1 {
		shape.result = results.At(0).Type()
		// Authorization above proves one exact declaration and a bounded
		// argument lifetime. The typed record and call-site retention proof
		// keep every input owner live through the physical call and result
		// reload, including a synchronous managed-callback boundary.
		if !coroWorkerForeignRecordValueType(
			universe, shape.result, true, make(map[types.Type]bool),
		) {
			return coroWorkerForeignCallShape{}, true, fmt.Errorf(
				"result type %s cannot be represented in a declaration-authorized typed worker call record",
				shape.result,
			)
		}
	}
	shape.target = target
	shape.mode = mode
	shape.arguments = append([]ssa.Value(nil), arguments...)
	shape.calleeField = -1
	shape.signature = recordSignature
	shape.record, shape.resultField, shape.argumentBase =
		coroWorkerForeignRecordLayout(recordSignature, shape.result, nil)
	return shape, true, nil
}

func validateCoroWorkerDynamicForeignCall(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	call *ssa.Call,
	callPlan coro.SSACallPlan,
) (shape coroWorkerForeignCallShape, recognized bool, err error) {
	shape.calleeField = -1
	shape.resultField = -1
	if plan == nil || universe == nil || call == nil || call.Common() == nil {
		return shape, true, fmt.Errorf("dynamic raw C worker call requires an exact plan, universe, and call")
	}
	common := call.Common()
	if common.StaticCallee() != nil || common.IsInvoke() || common.Method != nil ||
		callPlan.Kind != coro.CallForeign || callPlan.Rep != coro.DirectPlain ||
		callPlan.Transport != coro.RawCCodePointer || !callPlan.Open ||
		callPlan.Unresolved != coro.UnknownForeign || callPlan.SyncDispatch {
		return shape, true, fmt.Errorf(
			"requires one open raw-C DirectPlain foreign call, got kind=%v representation=%s transport=%s open=%t unresolved=%v",
			callPlan.Kind, callPlan.Rep, callPlan.Transport, callPlan.Open, callPlan.Unresolved,
		)
	}
	if err := validateCoroCallableTransportValue(plan, call.Parent(), common.Value, universe); err != nil {
		return shape, true, fmt.Errorf("dynamic raw C callee: %w", err)
	}
	owner := universe.ownerOf(call.Parent())
	ownerContext, contextErr := universe.functionABIContext(call.Parent(), owner)
	if contextErr != nil {
		return shape, true, fmt.Errorf("derive dynamic call-site ABI: %w", contextErr)
	}
	calleeType := ownerContext.patchType(common.Value.Type())
	signature, ok := types.Unalias(calleeType).Underlying().(*types.Signature)
	if !ok || signature == nil || signature.Recv() != nil || signature.Variadic() ||
		coroWorkerTypeParamLen(signature.TypeParams()) != 0 ||
		coroWorkerTypeParamLen(signature.RecvTypeParams()) != 0 {
		return shape, true, fmt.Errorf("dynamic raw C callee is not a receiver-free, non-variadic, non-generic function pointer")
	}
	callSignature, ok := ownerContext.patchType(common.Signature()).(*types.Signature)
	if !ok || !types.Identical(
		coroPhysicalNormalizeSourceSignature(callSignature),
		coroPhysicalNormalizeSourceSignature(signature),
	) {
		return shape, true, fmt.Errorf("dynamic callee and call-site effective C signatures differ")
	}
	shape.argc = signature.Params().Len()
	if shape.argc != len(common.Args) {
		return shape, true, fmt.Errorf("effective signature argument count differs from the call site")
	}
	for index, argument := range common.Args {
		if argument == nil {
			return shape, true, fmt.Errorf("argument %d is nil", index)
		}
		argumentType := ownerContext.patchType(argument.Type())
		parameterType := signature.Params().At(index).Type()
		if !types.Identical(argumentType, parameterType) {
			return shape, true, fmt.Errorf("argument %d type does not match the effective C parameter", index)
		}
		if !coroWorkerForeignRecordValueType(
			universe, parameterType, true, make(map[types.Type]bool),
		) {
			return shape, true, fmt.Errorf(
				"argument %d type %s cannot be represented in a typed worker call record",
				index, parameterType,
			)
		}
	}
	results := signature.Results()
	if results != nil && results.Len() > 1 {
		return shape, true, fmt.Errorf("requires zero or one result")
	}
	if results != nil && results.Len() == 1 {
		shape.result = results.At(0).Type()
		if !coroWorkerForeignRecordValueType(
			universe, shape.result, false, make(map[types.Type]bool),
		) {
			return coroWorkerForeignCallShape{}, true, fmt.Errorf(
				"result type %s cannot be represented in a pointer-free typed worker call record",
				shape.result,
			)
		}
	}
	shape.calleeType = calleeType
	shape.calleeField = 0
	shape.arguments = append([]ssa.Value(nil), common.Args...)
	shape.signature = signature
	shape.mode = coroForeignCallModeWorker
	shape.nilGuard = callPlan.MayBeNil &&
		!ssaFunctionValueProvenNonNilAt(common.Value, call)
	shape.record, shape.resultField, shape.argumentBase =
		coroWorkerForeignRecordLayout(signature, shape.result, calleeType)
	return shape, true, nil
}

func coroWorkerForeignThunkSignature() *types.Signature {
	params := []*types.Var{
		types.NewParam(token.NoPos, nil, "record", types.Typ[types.Uintptr]),
	}
	result := types.NewVar(token.NoPos, nil, "result", types.Typ[types.Uintptr])
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), types.NewTuple(result), false)
}

func (p *context) coroWorkerForeignThunk(shape coroWorkerForeignCallShape, target llssa.Function) llssa.Function {
	dynamic := shape.calleeType != nil
	if p == nil || shape.signature == nil || shape.record == nil ||
		dynamic && (shape.target != nil || shape.calleeField < 0 || target != nil) ||
		!dynamic && (shape.target == nil || target == nil) {
		panic("coroutine foreign worker thunk requires an exact target, signature, and call record")
	}
	targetName := "<dynamic>"
	if target != nil {
		targetName = target.Name()
	}
	key := framedEmissionKey(
		"cl-coro-worker-foreign-thunk-v1",
		targetName,
		structuralEmissionABITypeKey(shape.calleeType),
		structuralEmissionABITypeKey(shape.signature),
		strconv.Itoa(p.prog.PointerSize()),
	)
	name := coroWorkerForeignThunkPrefixV1 + emissionDigest(key)
	thunk := p.pkg.NewFuncEx(name, coroWorkerForeignThunkSignature(), llssa.InC, false, true)
	if thunk.HasBody() {
		return thunk
	}
	b := thunk.MakeBody(1)
	recordPointer := b.Convert(
		p.type_(types.NewPointer(shape.record), llssa.InC),
		thunk.Param(0),
	)
	targetExpr := llssa.Expr{}
	if dynamic {
		targetExpr = b.LoadKnownNonNil(b.FieldAddr(recordPointer, shape.calleeField))
	} else {
		targetExpr = target.Expr
	}
	args := make([]llssa.Expr, shape.argc)
	for index := range args {
		args[index] = b.LoadKnownNonNil(b.FieldAddr(recordPointer, shape.argumentBase+index))
	}
	ret := b.Call(targetExpr, args...)
	if shape.result != nil {
		if shape.resultField < 0 {
			panic("coroutine foreign worker thunk lost its result record field")
		}
		b.Store(b.FieldAddr(recordPointer, shape.resultField), ret)
	}
	b.Return(p.prog.Zero(p.prog.Uintptr()))
	b.EndBuild()
	b.Dispose()
	return thunk
}

func (p *context) compileCoroWorkerForeignCall(
	b llssa.Builder, call *ssa.Call, shape coroWorkerForeignCallShape,
) llssa.Expr {
	dynamic := shape.calleeType != nil
	if p == nil || !p.hasCoroPhysicalBody() || call == nil ||
		shape.signature == nil || shape.record == nil ||
		dynamic && (shape.target != nil || shape.calleeField < 0) ||
		!dynamic && shape.target == nil {
		panic("coroutine foreign worker lowering escaped its frozen physical operation recipe")
	}
	var target llssa.Function
	if !dynamic {
		var kind int
		target, _, kind = p.compileFunction(shape.target)
		if kind != cFunc || target == nil {
			panic("coroutine foreign worker lowering lost its exact C target")
		}
	}
	thunk := p.coroWorkerForeignThunk(shape, target)
	oldInCFunc := p.inCFunc
	p.inCFunc = true
	callee := llssa.Expr{}
	if dynamic {
		callee = p.compileValue(b, call.Common().Value)
	}
	if len(shape.arguments) != shape.argc {
		panic("coroutine foreign worker lowering disagrees with its frozen variadic arguments")
	}
	compiled := make([]llssa.Expr, shape.argc)
	for index, argument := range shape.arguments {
		if callback := shape.rawCallbacks[index]; callback != nil {
			entry, _, kind := p.compileRawPlainFunction(callback)
			if entry == nil || kind != goFunc {
				panic("coroutine foreign worker lowering lost a raw/plain C callback entry")
			}
			compiled[index] = entry.Expr
			continue
		}
		compiled[index] = p.compileValue(b, argument)
	}
	p.inCFunc = oldInCFunc
	if shape.nilGuard {
		p.compileCoroImplicitNilAccessGuard(b, callee)
	}
	record := p.coroFrameAlloc(p.type_(shape.record, llssa.InC))
	if dynamic {
		b.Store(b.FieldAddr(record, shape.calleeField), callee)
	}
	for index, argument := range compiled {
		b.Store(b.FieldAddr(record, shape.argumentBase+index), argument)
	}
	function := b.Convert(p.prog.Uintptr(), thunk.Expr)
	keepaliveSlots := p.compileCoroCallKeepaliveSlots(b, call)
	p.compileCoroWorkerWordCall(
		b,
		function,
		[]llssa.Expr{b.Convert(p.prog.Uintptr(), record)},
		keepaliveSlots,
		nil,
	)
	// The native queue carries the record address as an opaque uintptr. This
	// post-acknowledgement use forces CoroSplit to retain the complete typed
	// record—including nested pointer owners—until the worker can no longer
	// dereference or write it.
	b.KeepAlive(record)
	if shape.result == nil {
		return llssa.Expr{}
	}
	if shape.resultField < 0 {
		panic("coroutine foreign worker lowering lost its result record field")
	}
	return b.LoadKnownNonNil(b.FieldAddr(record, shape.resultField))
}
