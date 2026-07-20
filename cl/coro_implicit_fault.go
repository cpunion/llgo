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
	"go/token"
	"go/types"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroFaultPrepareHookV1 = "__llgo_coro_fault_prepare_v1"
	coroFaultPayloadHookV1 = "__llgo_coro_fault_payload_v1"
)

const (
	coroFaultNilV1 uint32 = iota + 1
	coroFaultIndexBoundsV1
	coroFaultChannelSendClosedV1
	coroFaultUnsafeSliceLenV1
	coroFaultUnsafeSliceNilV1
	coroFaultChannelCloseNilV1
	coroFaultChannelCloseClosedV1
	coroFaultUnsafeStringLenV1
	coroFaultUnsafeStringNilV1
	coroFaultSliceConvertV1
)

func coroFaultPrepareSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "handle", pointer),
		types.NewParam(token.NoPos, nil, "header", pointer),
		types.NewParam(token.NoPos, nil, "kind", types.Typ[types.Uint32]),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroFaultPayloadSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "kind", types.Typ[types.Uint32]),
		types.NewParam(token.NoPos, nil, "typeOut", pointer),
		types.NewParam(token.NoPos, nil, "dataOut", pointer),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

// compileCoroImplicitNilFieldAddrGuard splits the current source block before
// LLVM forms the GEP. A nullable Go pointer is retained as an exact coroutine-
// frame root, but its access semantics never rely on a native signal or wasm
// trap: nil takes the compiler-owned explicit-status terminal edge and only
// the non-nil block may construct the field address.
func (p *context) compileCoroImplicitNilFieldAddrGuard(
	b llssa.Builder,
	field *ssa.FieldAddr,
	base llssa.Expr,
) llssa.Expr {
	if p == nil || p.currentCoro == nil || field == nil || field.X == nil || b == nil || b.Func != p.fn {
		panic("implicit nil FieldAddr guard escaped its physical coroutine body")
	}
	if p.compilation == nil || !p.compilation.EnableCoroExplicitStatusPanicABI ||
		p.currentCoro.abi.version < coroPhysicalABIVersionV1 {
		panic("implicit nil FieldAddr guard requires the PhysicalABIV1 explicit-status panic ABI")
	}
	if _, ok := types.Unalias(field.X.Type()).Underlying().(*types.Pointer); !ok {
		panic(fmt.Sprintf("implicit nil FieldAddr base %T is not pointer-shaped", field.X.Type()))
	}
	return p.compileCoroImplicitNilAccessGuard(b, base)
}

// compileCoroImplicitNilDerefGuard gives an ordinary typed load the same
// platform-independent explicit-status nil semantics as FieldAddr. Lifetime
// was certified separately by the exact frame-retention proof; this guard does
// not infer non-nil from a pointer type or from closure capture.
func (p *context) compileCoroImplicitNilDerefGuard(
	b llssa.Builder,
	deref *ssa.UnOp,
	base llssa.Expr,
) llssa.Expr {
	if p == nil || p.currentCoro == nil || deref == nil || deref.Op != token.MUL || deref.X == nil ||
		b == nil || b.Func != p.fn {
		panic("implicit nil typed-load guard escaped its physical coroutine body")
	}
	if _, ok := types.Unalias(deref.X.Type()).Underlying().(*types.Pointer); !ok {
		panic(fmt.Sprintf("implicit nil typed-load base %T is not pointer-shaped", deref.X.Type()))
	}
	return p.compileCoroImplicitNilAccessGuard(b, base)
}

func (p *context) compileCoroImplicitNilAccessGuard(b llssa.Builder, base llssa.Expr) llssa.Expr {
	if p == nil || p.currentCoro == nil || b == nil || b.Func != p.fn {
		panic("implicit nil access guard escaped its physical coroutine body")
	}
	if p.compilation == nil || !p.compilation.EnableCoroExplicitStatusPanicABI ||
		p.currentCoro.abi.version < coroPhysicalABIVersionV1 {
		panic("implicit nil access guard requires the PhysicalABIV1 explicit-status panic ABI")
	}

	isNil := b.BinOp(token.EQL, base, b.Prog.Nil(base.Type))
	p.compileCoroFaultConditionGuard(b, isNil, coroFaultNilV1)
	return base
}

// compileCoroIndexBoundsGuard routes an out-of-range predicate through the
// target-neutral explicit-status fault ABI. The caller emits an unchecked GEP
// or load only in the normal continuation block.
func (p *context) compileCoroIndexBoundsGuard(b llssa.Builder, outOfRange llssa.Expr) {
	if outOfRange.IsNil() {
		return
	}
	p.compileCoroFaultConditionGuard(b, outOfRange, coroFaultIndexBoundsV1)
}

func (p *context) compileCoroIndexAddrGuarded(
	b llssa.Builder,
	operation *ssa.IndexAddr,
	base, index llssa.Expr,
) llssa.Expr {
	if p == nil || p.currentCoro == nil || operation == nil || operation.X == nil ||
		b == nil || b.Func != p.fn {
		panic("structured coroutine IndexAddr escaped its physical body")
	}
	var limit llssa.Expr
	pointerBase := false
	switch container := types.Unalias(p.patchType(operation.X.Type())).Underlying().(type) {
	case *types.Slice:
		limit = b.SliceLen(base)
	case *types.Pointer:
		pointerBase = true
		array, ok := types.Unalias(container.Elem()).Underlying().(*types.Array)
		if !ok {
			panic("structured coroutine IndexAddr pointer base is not an array")
		}
		limit = b.Prog.IntVal(uint64(array.Len()), b.Prog.Int())
	default:
		panic("structured coroutine IndexAddr has unsupported base")
	}
	// Indexing a pointer-to-array first implicitly dereferences the pointer.
	// Preserve Go's fault order: nil pointer before index bounds. A nil slice,
	// by contrast, is a valid length-zero slice and therefore takes only the
	// ordinary bounds fault for any element access.
	if pointerBase &&
		!isKnownNonNilAddr(operation.X) && !ssaValueProvenNonNilAt(operation.X, operation) {
		base = p.compileCoroImplicitNilAccessGuard(b, base)
	}
	normalized, outOfRange := b.IndexBounds(index, limit)
	p.compileCoroIndexBoundsGuard(b, outOfRange)
	return b.IndexAddrUnchecked(base, normalized)
}

// compileCoroIndexGuarded implements every concrete x/tools Index container
// shape without calling the native-stack CheckIndexRange helper. String and
// array values use LLSSA's unchecked value load; slice and *array values use
// the corresponding unchecked address followed by a typed load. In all cases
// the address/load is emitted only in the continuation dominated by the Go
// bounds check. A nullable *array gets the same structured nil-fault edge as
// IndexAddr after its bounds check.
func (p *context) compileCoroIndexGuarded(
	b llssa.Builder,
	operation *ssa.Index,
	base, index llssa.Expr,
	takeArrayAddr func() (addr llssa.Expr, zero bool),
) llssa.Expr {
	if p == nil || p.currentCoro == nil || operation == nil || operation.X == nil ||
		operation.Index == nil || b == nil || b.Func != p.fn {
		panic("structured coroutine Index escaped its physical body")
	}

	container := types.Unalias(p.patchType(operation.X.Type())).Underlying()
	var limit llssa.Expr
	switch container := container.(type) {
	case *types.Basic:
		if !coroPhysicalStringBasic(container) {
			panic("structured coroutine Index basic base is not a string")
		}
		limit = b.StringLen(base)
	case *types.Array:
		limit = b.Prog.IntVal(uint64(container.Len()), b.Prog.Int())
	case *types.Slice:
		limit = b.SliceLen(base)
	case *types.Pointer:
		array, ok := types.Unalias(container.Elem()).Underlying().(*types.Array)
		if !ok {
			panic("structured coroutine Index pointer base is not an array")
		}
		limit = b.Prog.IntVal(uint64(array.Len()), b.Prog.Int())
	default:
		panic(fmt.Sprintf("structured coroutine Index has unsupported base %T", container))
	}
	if _, pointer := container.(*types.Pointer); pointer &&
		!isKnownNonNilAddr(operation.X) && !ssaValueProvenNonNilAt(operation.X, operation) {
		// Go evaluates an implicit *array dereference before applying the index
		// operation, so nil wins over an otherwise out-of-range index.
		base = p.compileCoroImplicitNilAccessGuard(b, base)
	}

	normalized, outOfRange := b.IndexBounds(index, limit)
	p.compileCoroIndexBoundsGuard(b, outOfRange)

	switch container.(type) {
	case *types.Basic, *types.Array:
		return b.IndexUnchecked(base, normalized, takeArrayAddr)
	case *types.Slice:
		return b.Load(b.IndexAddrUnchecked(base, normalized))
	case *types.Pointer:
		return b.Load(b.IndexAddrUnchecked(base, normalized))
	default:
		panic("structured coroutine Index lost its validated container shape")
	}
}

// compileCoroSliceGuarded implements two- and three-index Go slicing without
// calling the native-stack StringSlice2/NewSlice2/NewSlice3Bounds helpers.
// Operand evaluation has already happened in source order. A nullable *array
// then takes the structured nil edge, followed by the exact inclusive slice
// bounds predicate; only the dominated continuation constructs the aggregate.
func (p *context) compileCoroSliceGuarded(
	b llssa.Builder,
	operation *ssa.Slice,
	base, low, high, max llssa.Expr,
) llssa.Expr {
	if p == nil || p.currentCoro == nil || operation == nil || operation.X == nil ||
		b == nil || b.Func != p.fn {
		panic("structured coroutine Slice escaped its physical body")
	}
	if p.compilation == nil || !p.compilation.EnableCoroExplicitStatusPanicABI ||
		p.currentCoro.abi.version < coroPhysicalABIVersionV1 {
		panic("structured coroutine Slice requires the PhysicalABIV1 explicit-status panic ABI")
	}

	zero := b.Prog.IntVal(0, b.Prog.Int())
	if low.IsNil() {
		low = zero
	}
	var limit llssa.Expr
	switch container := types.Unalias(p.patchType(operation.X.Type())).Underlying().(type) {
	case *types.Basic:
		if !coroPhysicalStringBasic(container) || !max.IsNil() {
			panic("structured coroutine Slice basic base is not a two-index string")
		}
		limit = b.StringLen(base)
		if high.IsNil() {
			high = limit
		}
	case *types.Slice:
		limit = b.SliceCap(base)
		if high.IsNil() {
			high = b.SliceLen(base)
		}
	case *types.Pointer:
		array, ok := types.Unalias(container.Elem()).Underlying().(*types.Array)
		if !ok {
			panic("structured coroutine Slice pointer base is not an array")
		}
		if !isKnownNonNilAddr(operation.X) && !ssaValueProvenNonNilAt(operation.X, operation) {
			base = p.compileCoroImplicitNilAccessGuard(b, base)
		}
		limit = b.Prog.IntVal(uint64(array.Len()), b.Prog.Int())
		if high.IsNil() {
			high = limit
		}
	default:
		panic(fmt.Sprintf("structured coroutine Slice has unsupported base %T", container))
	}

	low, high, max, outOfRange := b.SliceBounds(low, high, max, limit)
	p.compileCoroIndexBoundsGuard(b, outOfRange)
	return b.SliceUnchecked(base, low, high, max)
}

func (p *context) compileCoroFaultConditionGuard(b llssa.Builder, condition llssa.Expr, kind uint32) {
	if p == nil || p.currentCoro == nil || b == nil || b.Func != p.fn || condition.IsNil() {
		panic("structured coroutine fault guard escaped its physical body")
	}
	fault := b.Func.MakeBlock()
	normal := b.Func.MakeBlock()
	b.If(condition, fault, normal)

	b.SetBlockEx(fault, llssa.AtEnd, false)
	p.compileCoroTerminalFault(b, kind)

	// The fault path is terminal (possibly after the static drainer). Continue
	// source lowering only in the block dominated by base != nil.
	b.SetBlockContinuation(normal)
}

// compileCoroTerminalFault enters the one target-neutral explicit-status
// fault path shared by implicit language faults and typed runtime outcomes
// such as send-on-closed-channel. Static defers drain before publication; a
// body without cleanup publishes immediately. The call never returns to the
// source continuation.
func (p *context) compileCoroTerminalFault(b llssa.Builder, kind uint32) {
	if p == nil || p.currentCoro == nil || b == nil || b.Func != p.fn {
		panic("coroutine terminal fault escaped its physical body")
	}
	if cleanup := p.currentCoro.cleanup; cleanup != nil {
		cleanup.enterFault(p, b, kind)
	} else {
		p.currentCoro.implicitFault(p, b, kind)
	}
}

func (c *coroBodyContext) implicitFault(p *context, b llssa.Builder, kind uint32) {
	if c == nil || p == nil || b == nil || c.abi.version < coroPhysicalABIVersionV1 || c.finalSuspend == nil {
		panic("implicit nil fault requires a PhysicalABIV1 body and shared final suspend")
	}
	c.publishState(b, coroSuspendPanic, coroLifecycleFinalSuspended, c.terminalStateID())
	prepare := p.pkg.NewFunc(coroFaultPrepareHookV1, coroFaultPrepareSignature(), llssa.InC)
	b.Call(
		prepare.Expr,
		c.task,
		c.coro.Handle(),
		b.Convert(b.Prog.VoidPtr(), c.header),
		b.Prog.IntVal(uint64(kind), b.Prog.Uint32()),
	)
	b.Jump(c.finalSuspend)
}

// materializeCoroFaultPayload loads the stable Go panic pair for one structured
// language fault without publishing a terminal scheduler outcome.  A cleanup
// drainer must expose that pair to each direct deferred child before deciding
// whether the panic remains terminal, so the older fault_prepare hook is too
// late for this path.  The output cells live in the LLVM coroutine ramp: a
// source fault may be emitted in a resume-only block where a local alloca would
// not dominate CoroSplit's generated resume function.
func (p *context) materializeCoroFaultPayload(
	b llssa.Builder, kind uint32,
) (typeWord, dataWord llssa.Expr) {
	if p == nil || p.currentCoro == nil || b == nil || b.Func != p.fn ||
		p.compilation == nil || !p.compilation.EnableCoroExplicitStatusPanicABI ||
		p.currentCoro.abi.version < coroPhysicalABIVersionV1 {
		panic("coroutine fault payload materialization requires an explicit-status PhysicalABIV1 body")
	}
	typeSlot := p.coroFrameAlloca(p.prog.VoidPtr())
	dataSlot := p.coroFrameAlloca(p.prog.VoidPtr())
	b.Store(typeSlot, p.prog.Nil(p.prog.VoidPtr()))
	b.Store(dataSlot, p.prog.Nil(p.prog.VoidPtr()))
	payload := p.pkg.NewFunc(coroFaultPayloadHookV1, coroFaultPayloadSignature(), llssa.InC)
	b.Call(
		payload.Expr,
		p.prog.IntVal(uint64(kind), p.prog.Uint32()),
		b.Convert(p.prog.VoidPtr(), typeSlot),
		b.Convert(p.prog.VoidPtr(), dataSlot),
	)
	return b.Load(typeSlot), b.Load(dataSlot)
}

// enterFault turns a source-body implicit fault into the same recoverable
// panic overlay as an explicit panic.  The canonical Recover continuation is
// retained as the base; if no direct deferred child recovers the payload, the
// shared cleanup panic block publishes it through panic_prepare_v1.
func (s *coroStaticCleanupState) enterFault(p *context, b llssa.Builder, kind uint32) {
	if s == nil || p == nil || p.currentCoro == nil || b == nil {
		panic("implicit nil fault cleanup has no active coroutine state")
	}
	typeWord, dataWord := p.materializeCoroFaultPayload(b, kind)
	s.enterPanic(b, typeWord, dataWord)
}

// replaceFault is the cleanup-internal counterpart used by operations such as
// invoking a nil deferred function descriptor.  The popped record has already
// become at-most-once; preserve its existing normal/RunDefers/cancel base while
// replacing any older panic with the newer implicit fault.
func (s *coroStaticCleanupState) replaceFault(p *context, b llssa.Builder, kind uint32) {
	if s == nil || p == nil || p.currentCoro == nil || b == nil {
		panic("implicit cleanup fault has no active coroutine state")
	}
	typeWord, dataWord := p.materializeCoroFaultPayload(b, kind)
	s.replacePanic(b, typeWord, dataWord)
}

func (p *context) coroFieldAddrRequiresImplicitNilFault(field *ssa.FieldAddr) bool {
	if p == nil || p.currentCoro == nil || field == nil || p.currentCoro.frameRetention == nil {
		return false
	}
	if ssaAddressValueProvenNonNilAt(field.X, field) {
		return false
	}
	if !p.currentCoro.frameRetention.requiresImplicitNilFault(field, field) {
		return false
	}
	if p.compilation == nil || !p.compilation.EnableCoroExplicitStatusPanicABI {
		panic("nullable physical coroutine FieldAddr escaped explicit-status preflight")
	}
	return true
}

func (p *context) coroDerefRequiresImplicitNilFault(deref *ssa.UnOp) bool {
	if p == nil || p.currentCoro == nil || deref == nil || deref.Op != token.MUL ||
		p.currentCoro.frameRetention == nil {
		return false
	}
	if ssaValueProvenNonNilAt(deref.X, deref) {
		return false
	}
	if _, _, synthetic := coroSliceToArrayValueDeref(deref, p.patchType); synthetic {
		// The conversion owns the N>0 length fault. N==0 array-value
		// conversion is the zero value and must remain legal for a nil slice.
		return false
	}
	if field, ok := deref.X.(*ssa.FieldAddr); ok &&
		p.currentCoro.frameRetention.requiresImplicitNilFault(field, field) {
		// FieldAddr lowering already split the block and constructed this GEP
		// only on its non-nil edge. Do not add a redundant guard to the derived
		// typed load.
		return false
	}
	if _, indexed := deref.X.(*ssa.IndexAddr); indexed {
		// ExplicitStatus IndexAddr lowering owns both its bounds branch and a
		// possible *array nil branch before it forms the address.
		return false
	}
	if !p.currentCoro.frameRetention.requiresImplicitNilFault(deref.X, deref) {
		return false
	}
	if p.compilation == nil || !p.compilation.EnableCoroExplicitStatusPanicABI {
		panic("nullable physical coroutine typed load escaped explicit-status preflight")
	}
	return true
}
