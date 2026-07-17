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
	"strings"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// coroPhysicalPureSSAAudit is the deliberately small proof boundary for SSA
// operations that remain ordinary LLVM values across a coro suspend. It is not
// a general instruction allowlist. Every accepted case below mirrors the
// corresponding compileInstr/compileInstrOrValue and LLSSA Builder lowering.
// An operation that can call a runtime helper, perform dynamic dispatch, or
// introduce a new panic edge is rejected here even when that helper currently
// happens to be classified NoSuspend.
//
// PhysicalABIV1's current frame allocator profiles are conservative or
// non-collecting. Pointer/interface/slice values may therefore live in the LLVM
// coroutine frame, but this slice does not claim a precise frame root map or a
// moving-GC write barrier. A future precise collector must add those two ABI
// capabilities before enabling the same local-frame operations for that
// profile.
type coroPhysicalPureSSAAudit struct {
	universe                 *EmissionUniverse
	ctx                      *context
	fn                       *ssa.Function
	frameRetentionABI        string
	frameRetentionBuilt      bool
	frameRetentionProofCache *coroFrameRetentionProof
}

func newCoroPhysicalPureSSAAudit(universe *EmissionUniverse, fn *ssa.Function, frameRetentionABI string) (*coroPhysicalPureSSAAudit, error) {
	audit := &coroPhysicalPureSSAAudit{universe: universe, fn: fn, frameRetentionABI: frameRetentionABI}
	if universe == nil {
		// Structural unit tests may call the validator directly. Active
		// Compilation paths always supply their prepared emission universe.
		return audit, nil
	}
	if fn == nil {
		return nil, fmt.Errorf("nil function")
	}
	if canonical := universe.canonicalAlias(fn); canonical == nil || canonical != fn {
		return nil, fmt.Errorf("function %q is not the exact canonical emission owner", fn.Name())
	}
	if _, frozen := universe.required[fn]; !frozen {
		return nil, fmt.Errorf("function %q is outside the prepared emission universe", fn.Name())
	}
	owner := universe.ownerOf(fn)
	ctx, err := universe.functionABIContext(fn, owner)
	if err != nil {
		return nil, err
	}
	audit.ctx = ctx
	return audit, nil
}

func (a *coroPhysicalPureSSAAudit) validate(instr ssa.Instruction) (handled bool, reason string) {
	switch instr := instr.(type) {
	case *ssa.Alloc:
		return true, a.validateAlloc(instr)
	case *ssa.FieldAddr:
		return true, a.validateFieldAddr(instr)
	case *ssa.IndexAddr:
		return true, a.validateIndexAddr(instr)
	case *ssa.Index:
		return true, a.validateIndex(instr)
	case *ssa.Slice:
		return true, a.validateSlice(instr)
	case *ssa.Extract:
		return true, a.validateExtract(instr)
	case *ssa.Field:
		return true, a.validateField(instr)
	case *ssa.MakeInterface:
		return true, a.validateMakeInterface(instr)
	case *ssa.ChangeType:
		return true, a.validateChangeType(instr)
	case *ssa.Convert:
		return true, a.validateConvert(instr)
	case *ssa.Phi:
		return true, a.validatePhi(instr)
	case *ssa.BinOp:
		return true, a.validateBinOp(instr)
	case *ssa.UnOp:
		if instr.Op == token.MUL || instr.Op == token.SUB || instr.Op == token.XOR || instr.Op == token.NOT {
			return true, a.validateUnOp(instr)
		}
	case *ssa.Store:
		return true, a.validateStore(instr)
	case *ssa.Call:
		if _, builtin := instr.Call.Value.(*ssa.Builtin); builtin {
			return true, a.validateBuiltin(instr)
		}
		if recognized, reason := a.validateFrameRetentionOwnerCall(instr); recognized && reason != "" {
			return true, reason
		}
	}
	return false, ""
}

func (a *coroPhysicalPureSSAAudit) validateFrameRetentionOwnerCall(call *ssa.Call) (bool, string) {
	if a == nil || a.frameRetentionABI != CoroFrameRetentionTimerABIV1 || a.universe == nil || call == nil {
		return false, ""
	}
	kind, recognized := a.universe.coroFrameRetentionOwnerCallSite(call)
	if !recognized {
		return false, ""
	}
	want := coroFrameRetentionInstructionNone
	switch kind {
	case coroFrameRetentionCallPrepare:
		want = coroFrameRetentionInstructionPrepare
	case coroFrameRetentionCallRetire:
		want = coroFrameRetentionInstructionRetire
	default:
		return true, "exact frame-retention owner call has an unknown compiler role"
	}
	proof := a.currentFrameRetentionProof()
	if proof == nil || proof.roles[call] != want {
		return true, "exact frame-retention owner call is outside a certified prepare/park/retire transaction"
	}
	// A certified owner call still passes through the ordinary CallPlan/direct-
	// plain validation below. The retention proof changes pointer lifetime and
	// poll placement only; it does not manufacture a callable edge.
	return true, ""
}

func (a *coroPhysicalPureSSAAudit) validateAlloc(alloc *ssa.Alloc) string {
	if alloc == nil {
		return "heap allocation requires managed allocation and coroutine GC-root lowering"
	}
	if alloc.Heap {
		if !a.frameRetainsAllocation(alloc) {
			return "heap allocation requires managed allocation and coroutine GC-root lowering"
		}
		// The complete address-use proof changes this exact lowering from
		// runtime.AllocZ to an LLVM alloca in the current coroutine frame. Do
		// not consult the ordinary Heap helper-demand table for that allocation.
		return ""
	}
	if a.ctx != nil && (a.ctx.skipSyntheticMakeSliceAlloc(alloc) || isEmissionVargsAlloc(a.ctx, alloc)) {
		return "synthetic slice/varargs allocation belongs to a non-pure enclosing lowering"
	}
	pointer, ok := types.Unalias(a.typeOf(alloc.Type())).Underlying().(*types.Pointer)
	if !ok {
		return "local allocation does not have a pointer type"
	}
	if err := validateCoroPhysicalSSAValueType(pointer.Elem()); err != nil {
		return "local allocation has unsupported value type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(alloc)
}

func (a *coroPhysicalPureSSAAudit) validateFieldAddr(field *ssa.FieldAddr) string {
	if field == nil {
		return "nil field address"
	}
	if _, reason := a.stableAddress(field, make(map[ssa.Value]bool)); reason != "" {
		return reason
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(field.Type())); err != nil {
		return "field address has unsupported type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(field)
}

func (a *coroPhysicalPureSSAAudit) validateIndexAddr(index *ssa.IndexAddr) string {
	if index == nil {
		return "nil index address"
	}
	if _, reason := a.stableAddress(index, make(map[ssa.Value]bool)); reason != "" {
		return reason
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(index.Type())); err != nil {
		return "index address has unsupported type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(index)
}

func (a *coroPhysicalPureSSAAudit) validateIndex(index *ssa.Index) string {
	if index == nil || index.X == nil || index.Index == nil {
		return "incomplete index operation"
	}
	array, ok := types.Unalias(a.typeOf(index.X.Type())).Underlying().(*types.Array)
	if !ok || !coroConstantIndexInBounds(index.Index, array.Len()) {
		return "index may panic; pure coroutine indexing requires a compile-time in-range fixed-array index"
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(index.Type())); err != nil {
		return "array index has unsupported result type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(index)
}

func (a *coroPhysicalPureSSAAudit) validateSlice(slice *ssa.Slice) string {
	if slice == nil || slice.X == nil || slice.Low != nil || slice.High != nil || slice.Max != nil {
		return "slice bounds require runtime validation; only a complete fixed-array view is pure"
	}
	pointer, ok := types.Unalias(a.typeOf(slice.X.Type())).Underlying().(*types.Pointer)
	if !ok {
		return "pure slice view requires a pointer to a fixed array"
	}
	if _, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Array); !ok {
		return "pure slice view requires a pointer to a fixed array"
	}
	if _, reason := a.stableAddress(slice.X, make(map[ssa.Value]bool)); reason != "" {
		return "slice base: " + reason
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(slice.Type())); err != nil {
		return "slice view has unsupported type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(slice)
}

func (a *coroPhysicalPureSSAAudit) validateExtract(extract *ssa.Extract) string {
	if extract == nil || extract.Tuple == nil {
		return "incomplete tuple extract"
	}
	tuple, ok := types.Unalias(a.typeOf(extract.Tuple.Type())).Underlying().(*types.Tuple)
	if !ok || extract.Index < 0 || extract.Index >= tuple.Len() {
		return "tuple extract index is outside its frozen aggregate shape"
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(extract.Type())); err != nil {
		return "tuple extract has unsupported result type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(extract)
}

func (a *coroPhysicalPureSSAAudit) validateField(field *ssa.Field) string {
	if field == nil || field.X == nil {
		return "incomplete aggregate field extraction"
	}
	structure, ok := types.Unalias(a.typeOf(field.X.Type())).Underlying().(*types.Struct)
	if !ok || field.Field < 0 || field.Field >= structure.NumFields() {
		return "aggregate field index is outside its frozen struct shape"
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(field.Type())); err != nil {
		return "aggregate field has unsupported result type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(field)
}

func (a *coroPhysicalPureSSAAudit) validateMakeInterface(box *ssa.MakeInterface) string {
	if box == nil || box.X == nil {
		return "incomplete interface construction"
	}
	target, ok := types.Unalias(a.typeOf(box.Type())).Underlying().(*types.Interface)
	if !ok {
		return "MakeInterface target is not an interface"
	}
	target.Complete()
	if !target.Empty() {
		return "non-empty interface construction requires itab/runtime lowering"
	}
	source := a.typeOf(box.X.Type())
	if coroPhysicalTypeContainsFunctionValue(source, make(map[types.Type]bool)) {
		return "boxing a function value requires canonical dynamic-dispatch descriptor validation"
	}
	if !emissionDirectIfaceType(source) {
		return "interface construction requires managed backing allocation for this value representation"
	}
	if err := validateCoroPhysicalSSAValueType(source); err != nil {
		return "interface payload has unsupported type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(box)
}

func (a *coroPhysicalPureSSAAudit) validateChangeType(change *ssa.ChangeType) string {
	if change == nil || change.X == nil {
		return "incomplete value-preserving type change"
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(change.X.Type())); err != nil {
		return "type-change source is unsupported: " + err.Error()
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(change.Type())); err != nil {
		return "type-change result is unsupported: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(change)
}

func (a *coroPhysicalPureSSAAudit) validateConvert(convert *ssa.Convert) string {
	if convert == nil || convert.X == nil {
		return "incomplete conversion"
	}
	source, target := a.typeOf(convert.X.Type()), a.typeOf(convert.Type())
	if !coroPureConversion(source, target) {
		return "conversion may allocate or call the runtime; pure coroutine conversion supports only numeric and pointer/unsafe-pointer representations"
	}
	if err := validateCoroPhysicalSSAValueType(source); err != nil {
		return "conversion source is unsupported: " + err.Error()
	}
	if err := validateCoroPhysicalSSAValueType(target); err != nil {
		return "conversion result is unsupported: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(convert)
}

func (a *coroPhysicalPureSSAAudit) validatePhi(phi *ssa.Phi) string {
	if phi == nil {
		return "nil phi"
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(phi.Type())); err != nil {
		return "phi has unsupported value type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(phi)
}

func (a *coroPhysicalPureSSAAudit) validateBinOp(op *ssa.BinOp) string {
	if op == nil || op.X == nil || op.Y == nil {
		return "incomplete binary operation"
	}
	if op.Op == token.QUO || op.Op == token.REM || op.Op == token.SHL || op.Op == token.SHR ||
		!coroPureBasicScalar(a.typeOf(op.Type())) || !coroPureBasicScalar(a.typeOf(op.X.Type())) || !coroPureBasicScalar(a.typeOf(op.Y.Type())) {
		return "potentially panicking or non-scalar binary operation"
	}
	return a.requireNoRuntimeHelpers(op)
}

func (a *coroPhysicalPureSSAAudit) validateUnOp(op *ssa.UnOp) string {
	if op == nil || op.X == nil {
		return "incomplete unary operation"
	}
	if op.Op != token.MUL {
		if !coroPureBasicScalar(a.typeOf(op.Type())) {
			return "unsupported unary operation"
		}
		return a.requireNoRuntimeHelpers(op)
	}
	if _, reason := a.stableAddress(op.X, make(map[ssa.Value]bool)); reason != "" {
		return "typed load: " + reason
	}
	if !a.nonZeroPhysicalType(op.Type()) {
		return "zero-sized typed load lowers through an explicit nil-check helper"
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(op.Type())); err != nil {
		return "typed load has unsupported value type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(op)
}

func (a *coroPhysicalPureSSAAudit) validateStore(store *ssa.Store) string {
	if store == nil || store.Addr == nil || store.Val == nil {
		return "incomplete typed store"
	}
	root, reason := a.stableAddress(store.Addr, make(map[ssa.Value]bool))
	if reason != "" {
		return "typed store: " + reason
	}
	pointer, ok := types.Unalias(a.typeOf(store.Addr.Type())).Underlying().(*types.Pointer)
	if !ok || !types.Identical(pointer.Elem(), a.typeOf(store.Val.Type())) {
		return "typed store address/value types do not match"
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(store.Val.Type())); err != nil {
		return "typed store has unsupported value type: " + err.Error()
	}
	if root == coroPhysicalAddressGlobal && coroTypeContainsGCPointer(a.typeOf(store.Val.Type()), make(map[types.Type]bool)) {
		return "global typed store of a pointer-containing value requires explicit write-barrier lowering"
	}
	// A pointer-containing local store is accepted only under PhysicalABIV1's
	// current conservative/non-collecting frame profiles described above. It is
	// not evidence that precise frame maps or barriers have been implemented.
	return a.requireNoRuntimeHelpers(store)
}

func (a *coroPhysicalPureSSAAudit) validateBuiltin(call *ssa.Call) string {
	if call == nil || call.Call.Value == nil || len(call.Call.Args) != 1 {
		return "unsupported builtin call in pure coroutine body"
	}
	builtin, ok := call.Call.Value.(*ssa.Builtin)
	if !ok {
		return "dynamic/non-builtin call is outside pure SSA lowering"
	}
	operand := types.Unalias(a.typeOf(call.Call.Args[0].Type())).Underlying()
	switch builtin.Name() {
	case "len":
		switch operand.(type) {
		case *types.Slice, *types.Basic:
			if basic, ok := operand.(*types.Basic); ok && basic.Kind() != types.String {
				return "len builtin is pure here only for slices and strings"
			}
		default:
			return "len builtin is pure here only for slices and strings"
		}
	case "cap":
		if _, ok := operand.(*types.Slice); !ok {
			return "cap builtin is pure here only for slices"
		}
	default:
		return fmt.Sprintf("builtin %q is outside the pure coroutine lowering slice", builtin.Name())
	}
	if err := validateCoroPhysicalSSAValueType(a.typeOf(call.Type())); err != nil {
		return "builtin result has unsupported type: " + err.Error()
	}
	return a.requireNoRuntimeHelpers(call)
}

type coroPhysicalAddressRoot uint8

const (
	coroPhysicalAddressInvalid coroPhysicalAddressRoot = iota
	coroPhysicalAddressLocal
	coroPhysicalAddressGlobal
)

// stableAddress accepts only statically non-nil storage owned by the current
// frame or package. Parameter/heap/foreign pointers remain fail-closed even if
// a particular host would merely trap on nil.
func (a *coroPhysicalPureSSAAudit) stableAddress(value ssa.Value, visiting map[ssa.Value]bool) (coroPhysicalAddressRoot, string) {
	if value == nil {
		return coroPhysicalAddressInvalid, "nil address"
	}
	if visiting[value] {
		return coroPhysicalAddressInvalid, "cyclic address expression"
	}
	visiting[value] = true
	defer delete(visiting, value)
	switch value := value.(type) {
	case *ssa.Global:
		if _, ok := types.Unalias(a.typeOf(value.Type())).Underlying().(*types.Pointer); !ok {
			return coroPhysicalAddressInvalid, "global address does not have pointer type"
		}
		return coroPhysicalAddressGlobal, ""
	case *ssa.Alloc:
		if value.Heap && !a.frameRetainsAllocation(value) {
			return coroPhysicalAddressInvalid, "heap allocation requires managed allocation/root lowering"
		}
		if a.ctx != nil && (a.ctx.skipSyntheticMakeSliceAlloc(value) || isEmissionVargsAlloc(a.ctx, value)) {
			return coroPhysicalAddressInvalid, "synthetic slice/varargs storage is not a standalone local address"
		}
		return coroPhysicalAddressLocal, ""
	case *ssa.FieldAddr:
		pointer, ok := types.Unalias(a.typeOf(value.X.Type())).Underlying().(*types.Pointer)
		if !ok {
			return coroPhysicalAddressInvalid, "field base is not a pointer"
		}
		structure, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Struct)
		if !ok || value.Field < 0 || value.Field >= structure.NumFields() {
			return coroPhysicalAddressInvalid, "field address is outside its frozen struct shape"
		}
		return a.stableAddress(value.X, visiting)
	case *ssa.IndexAddr:
		pointer, ok := types.Unalias(a.typeOf(value.X.Type())).Underlying().(*types.Pointer)
		if !ok {
			return coroPhysicalAddressInvalid, "index base is not a fixed-array pointer"
		}
		array, ok := types.Unalias(pointer.Elem()).Underlying().(*types.Array)
		if !ok || !coroConstantIndexInBounds(value.Index, array.Len()) {
			return coroPhysicalAddressInvalid, "index may panic; address indexing requires a compile-time in-range fixed-array index"
		}
		return a.stableAddress(value.X, visiting)
	default:
		return coroPhysicalAddressInvalid, fmt.Sprintf("address root %T is not statically non-nil local/global storage", value)
	}
}

func (a *coroPhysicalPureSSAAudit) requireNoRuntimeHelpers(instr ssa.Instruction) string {
	if a == nil || a.ctx == nil || a.universe == nil {
		return ""
	}
	helpers := a.universe.loweredRuntimeHelpers(a.ctx, instr)
	if len(helpers) == 0 {
		return ""
	}
	return "operation lowers through managed runtime helper(s) " + strings.Join(helpers, ", ")
}

func (a *coroPhysicalPureSSAAudit) typeOf(typ types.Type) types.Type {
	if typ == nil || a == nil || a.ctx == nil {
		return typ
	}
	return a.ctx.patchType(typ)
}

func (a *coroPhysicalPureSSAAudit) nonZeroPhysicalType(typ types.Type) bool {
	if typ == nil {
		return false
	}
	if a != nil && a.ctx != nil {
		return a.ctx.prog.SizeOf(a.ctx.type_(typ, llssa.InGo)) != 0
	}
	return coroTypeDefinitelyNonZero(typ, make(map[types.Type]bool))
}

func validateCoroPhysicalSSAValueType(typ types.Type) error {
	if typ == nil {
		return fmt.Errorf("nil type")
	}
	if tuple, ok := types.Unalias(typ).Underlying().(*types.Tuple); ok {
		for i := 0; i < tuple.Len(); i++ {
			if err := validateCoroPhysicalValueType(tuple.At(i).Type(), make(map[types.Type]bool)); err != nil {
				return fmt.Errorf("tuple field %d: %w", i, err)
			}
		}
		return nil
	}
	return validateCoroPhysicalValueType(typ, make(map[types.Type]bool))
}

func coroConstantIndexInBounds(index ssa.Value, bound int64) bool {
	if index == nil || bound < 0 {
		return false
	}
	value, ok := index.(*ssa.Const)
	if !ok || value.Value == nil {
		return false
	}
	basic, ok := types.Unalias(value.Type()).Underlying().(*types.Basic)
	if !ok || basic.Info()&types.IsInteger == 0 {
		return false
	}
	if basic.Info()&types.IsUnsigned == 0 && constant.Sign(value.Value) < 0 {
		return false
	}
	integer, exact := constant.Uint64Val(value.Value)
	return exact && integer < uint64(bound)
}

func coroPureBasicScalar(typ types.Type) bool {
	basic, ok := types.Unalias(typ).Underlying().(*types.Basic)
	if !ok {
		return false
	}
	return basic.Info()&(types.IsBoolean|types.IsInteger|types.IsFloat) != 0
}

func coroPureConversion(source, target types.Type) bool {
	if source == nil || target == nil {
		return false
	}
	sourceUnderlying := types.Unalias(source).Underlying()
	targetUnderlying := types.Unalias(target).Underlying()
	if types.Identical(sourceUnderlying, targetUnderlying) {
		return true
	}
	sourceBasic, sourceIsBasic := sourceUnderlying.(*types.Basic)
	targetBasic, targetIsBasic := targetUnderlying.(*types.Basic)
	if sourceIsBasic && targetIsBasic {
		if sourceBasic.Kind() == types.String || targetBasic.Kind() == types.String {
			return false
		}
		sourceNumeric := sourceBasic.Info()&(types.IsInteger|types.IsFloat|types.IsComplex) != 0
		targetNumeric := targetBasic.Info()&(types.IsInteger|types.IsFloat|types.IsComplex) != 0
		if sourceNumeric && targetNumeric {
			return true
		}
		return (sourceBasic.Kind() == types.UnsafePointer && targetBasic.Kind() == types.Uintptr) ||
			(sourceBasic.Kind() == types.Uintptr && targetBasic.Kind() == types.UnsafePointer)
	}
	_, sourcePointer := sourceUnderlying.(*types.Pointer)
	_, targetPointer := targetUnderlying.(*types.Pointer)
	if sourcePointer && targetPointer {
		return true
	}
	return (sourcePointer && targetIsBasic && targetBasic.Kind() == types.UnsafePointer) ||
		(targetPointer && sourceIsBasic && sourceBasic.Kind() == types.UnsafePointer)
}

func coroTypeContainsGCPointer(typ types.Type, visiting map[types.Type]bool) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if visiting[typ] {
		return false
	}
	visiting[typ] = true
	defer delete(visiting, typ)
	switch typ := typ.(type) {
	case *types.Named:
		return coroTypeContainsGCPointer(typ.Underlying(), visiting)
	case *types.Pointer, *types.Map, *types.Chan, *types.Signature, *types.Interface, *types.Slice:
		return true
	case *types.Basic:
		return typ.Kind() == types.String || typ.Kind() == types.UnsafePointer
	case *types.Array:
		return coroTypeContainsGCPointer(typ.Elem(), visiting)
	case *types.Struct:
		for i := 0; i < typ.NumFields(); i++ {
			if coroTypeContainsGCPointer(typ.Field(i).Type(), visiting) {
				return true
			}
		}
	}
	return false
}

func coroTypeDefinitelyNonZero(typ types.Type, visiting map[types.Type]bool) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if visiting[typ] {
		return false
	}
	visiting[typ] = true
	defer delete(visiting, typ)
	switch typ := typ.(type) {
	case *types.Named:
		return coroTypeDefinitelyNonZero(typ.Underlying(), visiting)
	case *types.Basic, *types.Pointer, *types.Map, *types.Chan, *types.Signature, *types.Interface, *types.Slice:
		return true
	case *types.Array:
		return typ.Len() > 0 && coroTypeDefinitelyNonZero(typ.Elem(), visiting)
	case *types.Struct:
		for i := 0; i < typ.NumFields(); i++ {
			if coroTypeDefinitelyNonZero(typ.Field(i).Type(), visiting) {
				return true
			}
		}
	}
	return false
}
