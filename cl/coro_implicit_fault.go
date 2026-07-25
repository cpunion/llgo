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
	coroFaultPrepareHookV2 = "__llgo_coro_fault_prepare_v2"
	coroFaultPayloadHookV2 = "__llgo_coro_fault_payload_v2"
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
	coroFaultLimitV1
)

const (
	// V2 bounds kinds encode the runtime boundsError code in the upper bits
	// and the original operand signedness in the low bit. Even values are
	// signed, odd values are unsigned.
	coroFaultBoundsBaseV2  uint32 = coroFaultLimitV1
	coroFaultBoundsLimitV2        = coroFaultBoundsBaseV2 + 2*8
)

type coroBoundsFaultCode uint32

const (
	coroBoundsFaultIndex coroBoundsFaultCode = iota
	coroBoundsFaultSliceAlen
	coroBoundsFaultSliceAcap
	coroBoundsFaultSliceB
	coroBoundsFaultSlice3Alen
	coroBoundsFaultSlice3Acap
	coroBoundsFaultSlice3B
	coroBoundsFaultSlice3C
)

func coroBoundsFaultKind(code coroBoundsFaultCode, signed bool) uint32 {
	if code > coroBoundsFaultSlice3C {
		panic("invalid coroutine bounds fault code")
	}
	kind := coroFaultBoundsBaseV2 + uint32(code)*2
	if !signed {
		kind++
	}
	return kind
}

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

func coroFaultPrepareSignatureV2() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "g", pointer),
		types.NewParam(token.NoPos, nil, "handle", pointer),
		types.NewParam(token.NoPos, nil, "header", pointer),
		types.NewParam(token.NoPos, nil, "kind", types.Typ[types.Uint32]),
		types.NewParam(token.NoPos, nil, "arg0", types.Typ[types.Uint64]),
		types.NewParam(token.NoPos, nil, "arg1", types.Typ[types.Uintptr]),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroFaultPayloadSignatureV2() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(token.NoPos, nil, "kind", types.Typ[types.Uint32]),
		types.NewParam(token.NoPos, nil, "arg0", types.Typ[types.Uint64]),
		types.NewParam(token.NoPos, nil, "arg1", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "typeOut", pointer),
		types.NewParam(token.NoPos, nil, "dataOut", pointer),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

// coroFaultOperands are the exact scalar words accepted by the parameterized
// V2 fault ABI. arg0 is a 64-bit integer bit pattern so uint64 bounds remain
// observable on wasm32; arg1 is a non-negative target-width len/cap/bound.
// A nil pointer selects the allocation-free V1 path.
type coroFaultOperands struct {
	arg0 llssa.Expr
	arg1 llssa.Expr
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
	body := p.coroBody()
	if body == nil || field == nil || field.X == nil || b == nil || b.Func != p.fn {
		panic("implicit nil FieldAddr guard escaped its physical coroutine body")
	}
	if !p.coroEmissionExplicitStatus() ||
		body.abi.version < coroPhysicalABIVersionV1 {
		panic("implicit nil FieldAddr guard requires the PhysicalABIV1 explicit-status panic ABI")
	}
	if _, ok := types.Unalias(field.X.Type()).Underlying().(*types.Pointer); !ok {
		panic(fmt.Sprintf("implicit nil FieldAddr base %T is not pointer-shaped", field.X.Type()))
	}
	return p.compileCoroPlannedNilAccessGuard(b, field, base)
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
	if p == nil || p.coroBody() == nil || deref == nil || deref.Op != token.MUL || deref.X == nil ||
		b == nil || b.Func != p.fn {
		panic("implicit nil typed-load guard escaped its physical coroutine body")
	}
	if _, ok := types.Unalias(deref.X.Type()).Underlying().(*types.Pointer); !ok {
		panic(fmt.Sprintf("implicit nil typed-load base %T is not pointer-shaped", deref.X.Type()))
	}
	return p.compileCoroPlannedNilAccessGuard(b, deref, base)
}

func (p *context) compileCoroPlannedNilAccessGuard(
	b llssa.Builder,
	instruction ssa.Instruction,
	base llssa.Expr,
) llssa.Expr {
	p.observeCoroPhysicalNilGuard(instruction)
	return p.compileCoroImplicitNilAccessGuard(b, base)
}

func (p *context) compileCoroImplicitNilAccessGuard(b llssa.Builder, base llssa.Expr) llssa.Expr {
	body := p.coroBody()
	if body == nil || b == nil || b.Func != p.fn {
		panic("implicit nil access guard escaped its physical coroutine body")
	}
	if !p.coroEmissionExplicitStatus() ||
		body.abi.version < coroPhysicalABIVersionV1 {
		panic("implicit nil access guard requires the PhysicalABIV1 explicit-status panic ABI")
	}

	isNil := b.BinOp(token.EQL, base, b.Prog.Nil(base.Type))
	p.compileCoroFaultConditionGuard(b, isNil, coroFaultNilV1)
	return base
}

func (p *context) compileCoroImplicitNilStoreGuard(
	b llssa.Builder,
	store *ssa.Store,
	base llssa.Expr,
) llssa.Expr {
	if p == nil || !p.hasCoroPhysicalBody() || store == nil || store.Addr == nil ||
		b == nil || b.Func != p.fn {
		panic("implicit nil Store guard escaped its physical coroutine body")
	}
	if _, ok := types.Unalias(store.Addr.Type()).Underlying().(*types.Pointer); !ok {
		panic(fmt.Sprintf("implicit nil Store address %T is not pointer-shaped", store.Addr.Type()))
	}
	return p.compileCoroPlannedNilAccessGuard(b, store, base)
}

// compileCoroIndexBoundsGuard routes an out-of-range predicate through the
// target-neutral explicit-status fault ABI. The caller emits an unchecked GEP
// or load only in the normal continuation block.
func (p *context) compileCoroIndexBoundsGuard(
	b llssa.Builder,
	outOfRange, index, limit llssa.Expr,
) {
	if outOfRange.IsNil() {
		return
	}
	x, signed := b.BoundsOperand(index)
	p.compileCoroFaultConditionGuardWithOperands(
		b,
		outOfRange,
		coroBoundsFaultKind(coroBoundsFaultIndex, signed),
		&coroFaultOperands{arg0: x, arg1: limit},
	)
}

func (p *context) compileCoroPlannedIndexBoundsGuard(
	b llssa.Builder,
	instruction ssa.Instruction,
	outOfRange, index, limit llssa.Expr,
) {
	p.observeCoroPhysicalBoundsGuard(instruction)
	p.compileCoroIndexBoundsGuard(b, outOfRange, index, limit)
}

type coroPlannedBoundsFault struct {
	condition llssa.Expr
	kind      uint32
	operands  coroFaultOperands
}

// compileCoroPlannedBoundsFaultSet is the shared observer/emitter for one
// source operation with an ordered family of bounds failures. Slice and
// slice-to-array lowering differ only in their frozen fault records; neither
// owns another physical-plan observation path.
func (p *context) compileCoroPlannedBoundsFaultSet(
	b llssa.Builder,
	instruction ssa.Instruction,
	faults []coroPlannedBoundsFault,
) {
	if len(faults) == 0 {
		panic("planned coroutine bounds fault set is empty")
	}
	p.observeCoroPhysicalBoundsGuard(instruction)
	for _, fault := range faults {
		p.compileCoroFaultConditionGuardWithOperands(
			b,
			fault.condition,
			fault.kind,
			&fault.operands,
		)
	}
}

func (p *context) compileCoroIndexAddrPlanned(
	b llssa.Builder,
	operation *ssa.IndexAddr,
	base, index llssa.Expr,
	plan coroPhysicalInstructionPlan,
) llssa.Expr {
	body := p.coroBody()
	if body == nil || operation == nil || operation.X == nil ||
		b == nil || b.Func != p.fn {
		panic("structured coroutine IndexAddr escaped its physical body")
	}
	if plan.recipe != coroPhysicalInstructionIndexAddr {
		panic("structured coroutine IndexAddr has the wrong physical recipe")
	}
	var limit llssa.Expr
	switch plan.container {
	case coroPhysicalContainerSlice:
		if plan.boundsGuard {
			limit = b.SliceLen(base)
		}
	case coroPhysicalContainerArrayPointer:
		if plan.boundsGuard {
			limit = b.Prog.IntVal(uint64(plan.bound), b.Prog.Int())
		}
	default:
		panic("structured coroutine IndexAddr has an invalid frozen container")
	}
	// Indexing a pointer-to-array first implicitly dereferences the pointer.
	// Preserve Go's fault order: nil pointer before index bounds. A nil slice,
	// by contrast, is a valid length-zero slice and therefore takes only the
	// ordinary bounds fault for any element access.
	if plan.nilGuard {
		base = p.compileCoroPlannedNilAccessGuard(b, operation, base)
	}
	normalized := index
	if plan.boundsGuard {
		var outOfRange llssa.Expr
		normalized, outOfRange = b.IndexBounds(index, limit)
		p.compileCoroPlannedIndexBoundsGuard(b, operation, outOfRange, index, limit)
	}
	return b.IndexAddrUnchecked(base, normalized)
}

// compileCoroIndexGuarded implements every concrete x/tools Index container
// shape without calling the native-stack CheckIndexRange helper. String and
// array values use LLSSA's unchecked value load; slice and *array values use
// the corresponding unchecked address followed by a typed load. In all cases
// the address/load is emitted only in the continuation dominated by the Go
// bounds check. A nullable *array gets the same structured nil-fault edge as
// IndexAddr after its bounds check.
func (p *context) compileCoroIndexPlanned(
	b llssa.Builder,
	operation *ssa.Index,
	base, index llssa.Expr,
	takeArrayAddr func() (addr llssa.Expr, zero bool),
	plan coroPhysicalInstructionPlan,
) llssa.Expr {
	if p == nil || p.coroBody() == nil || operation == nil || operation.X == nil ||
		operation.Index == nil || b == nil || b.Func != p.fn {
		panic("structured coroutine Index escaped its physical body")
	}

	if plan.recipe != coroPhysicalInstructionIndex {
		panic("structured coroutine Index has the wrong physical recipe")
	}
	var limit llssa.Expr
	if plan.boundsGuard {
		switch plan.container {
		case coroPhysicalContainerString:
			limit = b.StringLen(base)
		case coroPhysicalContainerArray, coroPhysicalContainerArrayPointer:
			limit = b.Prog.IntVal(uint64(plan.bound), b.Prog.Int())
		case coroPhysicalContainerSlice:
			limit = b.SliceLen(base)
		default:
			panic("structured coroutine Index has an invalid frozen container")
		}
	} else if plan.container != coroPhysicalContainerArray && plan.container != coroPhysicalContainerArrayPointer {
		panic("unchecked coroutine Index is not a fixed-array recipe")
	}
	if plan.nilGuard {
		base = p.compileCoroPlannedNilAccessGuard(b, operation, base)
	}

	normalized := index
	if plan.boundsGuard {
		var outOfRange llssa.Expr
		normalized, outOfRange = b.IndexBounds(index, limit)
		p.compileCoroPlannedIndexBoundsGuard(b, operation, outOfRange, index, limit)
	}

	switch plan.container {
	case coroPhysicalContainerString, coroPhysicalContainerArray:
		return b.IndexUnchecked(base, normalized, takeArrayAddr)
	case coroPhysicalContainerSlice, coroPhysicalContainerArrayPointer:
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
func (p *context) compileCoroSlicePlanned(
	b llssa.Builder,
	operation *ssa.Slice,
	base, low, high, max llssa.Expr,
	plan coroPhysicalInstructionPlan,
) llssa.Expr {
	body := p.coroBody()
	if body == nil || operation == nil || operation.X == nil ||
		b == nil || b.Func != p.fn {
		panic("structured coroutine Slice escaped its physical body")
	}
	if !p.coroEmissionExplicitStatus() ||
		body.abi.version < coroPhysicalABIVersionV1 {
		panic("structured coroutine Slice requires the PhysicalABIV1 explicit-status panic ABI")
	}
	if plan.recipe != coroPhysicalInstructionSlice || !plan.boundsGuard {
		panic("structured coroutine Slice has the wrong physical recipe")
	}

	zero := b.Prog.IntVal(0, b.Prog.Int())
	if low.IsNil() {
		low = zero
	}
	var limit llssa.Expr
	switch plan.container {
	case coroPhysicalContainerString:
		if !max.IsNil() {
			panic("structured coroutine Slice basic base is not a two-index string")
		}
		limit = b.StringLen(base)
		if high.IsNil() {
			high = limit
		}
	case coroPhysicalContainerSlice:
		limit = b.SliceCap(base)
		if high.IsNil() {
			high = b.SliceLen(base)
		}
	case coroPhysicalContainerArrayPointer:
		if plan.nilGuard {
			base = p.compileCoroPlannedNilAccessGuard(b, operation, base)
		}
		limit = b.Prog.IntVal(uint64(plan.bound), b.Prog.Int())
		if high.IsNil() {
			high = limit
		}
	default:
		panic(fmt.Sprintf("structured coroutine Slice has invalid frozen container %d", plan.container))
	}

	threeIndex := !max.IsNil()
	low, high, max, checks := b.SliceBoundsChecks(low, high, max, limit)
	var codes []coroBoundsFaultCode
	if threeIndex {
		upper := coroBoundsFaultSlice3Alen
		if plan.container == coroPhysicalContainerSlice {
			upper = coroBoundsFaultSlice3Acap
		}
		codes = []coroBoundsFaultCode{
			upper,
			coroBoundsFaultSlice3B,
			coroBoundsFaultSlice3C,
		}
	} else {
		upper := coroBoundsFaultSliceAlen
		if plan.container == coroPhysicalContainerSlice {
			upper = coroBoundsFaultSliceAcap
		}
		codes = []coroBoundsFaultCode{upper, coroBoundsFaultSliceB}
	}
	if len(checks) != len(codes) {
		panic("structured coroutine Slice lost its ordered bounds checks")
	}
	faults := make([]coroPlannedBoundsFault, len(checks))
	for i, check := range checks {
		faults[i] = coroPlannedBoundsFault{
			condition: check.OutOfRange,
			kind:      coroBoundsFaultKind(codes[i], check.Signed),
			operands:  coroFaultOperands{arg0: check.X, arg1: check.Y},
		}
	}
	p.compileCoroPlannedBoundsFaultSet(b, operation, faults)
	return b.SliceUnchecked(base, low, high, max)
}

func (p *context) compileCoroFaultConditionGuard(b llssa.Builder, condition llssa.Expr, kind uint32) {
	p.compileCoroFaultConditionGuardWithOperands(b, condition, kind, nil)
}

func (p *context) compileCoroFaultConditionGuardWithOperands(
	b llssa.Builder,
	condition llssa.Expr,
	kind uint32,
	operands *coroFaultOperands,
) {
	if p == nil || p.coroBody() == nil || b == nil || b.Func != p.fn || condition.IsNil() {
		panic("structured coroutine fault guard escaped its physical body")
	}
	if operands != nil && (operands.arg0.IsNil() || operands.arg1.IsNil()) {
		panic("parameterized coroutine fault guard has an incomplete operand pair")
	}
	if operands != nil &&
		(p.prog.SizeOf(operands.arg0.Type) != 8 ||
			p.prog.SizeOf(operands.arg1.Type) != uint64(p.prog.PointerSize())) {
		panic("parameterized coroutine fault guard has the wrong operand widths")
	}
	fault := b.Func.MakeBlock()
	normal := b.Func.MakeBlock()
	b.If(condition, fault, normal)

	b.SetBlockEx(fault, llssa.AtEnd, false)
	p.compileCoroTerminalFaultWithOperands(b, kind, operands)

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
	p.compileCoroTerminalFaultWithOperands(b, kind, nil)
}

func (p *context) compileCoroTerminalFaultWithOperands(
	b llssa.Builder,
	kind uint32,
	operands *coroFaultOperands,
) {
	body := p.coroBody()
	if body == nil || b == nil || b.Func != p.fn {
		panic("coroutine terminal fault escaped its physical body")
	}
	if cleanup := body.cleanup; cleanup != nil {
		cleanup.enterFaultWithOperands(p, b, kind, operands)
	} else {
		body.implicitFaultWithOperands(p, b, kind, operands)
	}
}

func (c *coroBodyContext) implicitFault(p *context, b llssa.Builder, kind uint32) {
	c.implicitFaultWithOperands(p, b, kind, nil)
}

func (c *coroBodyContext) implicitFaultWithOperands(
	p *context,
	b llssa.Builder,
	kind uint32,
	operands *coroFaultOperands,
) {
	if c == nil || p == nil || b == nil || c.abi.version < coroPhysicalABIVersionV1 || c.finalSuspend == nil {
		panic("implicit nil fault requires a PhysicalABIV1 body and shared final suspend")
	}
	c.publishState(b, coroSuspendPanic, coroLifecycleFinalSuspended, c.terminalStateID())
	args := []llssa.Expr{
		c.task,
		c.coro.Handle(),
		b.Convert(b.Prog.VoidPtr(), c.header),
		b.Prog.IntVal(uint64(kind), b.Prog.Uint32()),
	}
	prepareHook := coroFaultPrepareHookV1
	prepareSignature := coroFaultPrepareSignature()
	if operands != nil {
		if operands.arg0.IsNil() || operands.arg1.IsNil() {
			panic("parameterized coroutine fault preparation has an incomplete operand pair")
		}
		prepareHook = coroFaultPrepareHookV2
		prepareSignature = coroFaultPrepareSignatureV2()
		args = append(args, operands.arg0, operands.arg1)
	}
	prepare := p.pkg.NewFunc(prepareHook, prepareSignature, llssa.InC)
	b.Call(prepare.Expr, args...)
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
	return p.materializeCoroFaultPayloadWithOperands(b, kind, nil)
}

func (p *context) materializeCoroFaultPayloadWithOperands(
	b llssa.Builder,
	kind uint32,
	operands *coroFaultOperands,
) (typeWord, dataWord llssa.Expr) {
	body := p.coroBody()
	if body == nil || b == nil || b.Func != p.fn ||
		!p.coroEmissionExplicitStatus() ||
		body.abi.version < coroPhysicalABIVersionV1 {
		panic("coroutine fault payload materialization requires an explicit-status PhysicalABIV1 body")
	}
	typeSlot := p.coroFrameAlloca(p.prog.VoidPtr())
	dataSlot := p.coroFrameAlloca(p.prog.VoidPtr())
	b.Store(typeSlot, p.prog.Nil(p.prog.VoidPtr()))
	b.Store(dataSlot, p.prog.Nil(p.prog.VoidPtr()))
	args := []llssa.Expr{p.prog.IntVal(uint64(kind), p.prog.Uint32())}
	payloadHook := coroFaultPayloadHookV1
	payloadSignature := coroFaultPayloadSignature()
	if operands != nil {
		if operands.arg0.IsNil() || operands.arg1.IsNil() {
			panic("parameterized coroutine fault payload has an incomplete operand pair")
		}
		payloadHook = coroFaultPayloadHookV2
		payloadSignature = coroFaultPayloadSignatureV2()
		args = append(args, operands.arg0, operands.arg1)
	}
	args = append(args,
		b.Convert(p.prog.VoidPtr(), typeSlot),
		b.Convert(p.prog.VoidPtr(), dataSlot),
	)
	payload := p.pkg.NewFunc(payloadHook, payloadSignature, llssa.InC)
	b.Call(payload.Expr, args...)
	return b.Load(typeSlot), b.Load(dataSlot)
}

// enterFault turns a source-body implicit fault into the same recoverable
// panic overlay as an explicit panic.  The canonical Recover continuation is
// retained as the base; if no direct deferred child recovers the payload, the
// shared cleanup panic block publishes it through panic_prepare_v1.
func (s *coroStaticCleanupState) enterFault(p *context, b llssa.Builder, kind uint32) {
	s.enterFaultWithOperands(p, b, kind, nil)
}

func (s *coroStaticCleanupState) enterFaultWithOperands(
	p *context,
	b llssa.Builder,
	kind uint32,
	operands *coroFaultOperands,
) {
	if s == nil || p == nil || p.coroBody() == nil || b == nil {
		panic("implicit nil fault cleanup has no active coroutine state")
	}
	typeWord, dataWord := p.materializeCoroFaultPayloadWithOperands(b, kind, operands)
	s.enterPanic(b, typeWord, dataWord)
}

// replaceFault is the cleanup-internal counterpart used by operations such as
// invoking a nil deferred function descriptor.  The popped record has already
// become at-most-once; preserve its existing normal/RunDefers/cancel base while
// replacing any older panic with the newer implicit fault.
func (s *coroStaticCleanupState) replaceFault(p *context, b llssa.Builder, kind uint32) {
	if s == nil || p == nil || p.coroBody() == nil || b == nil {
		panic("implicit cleanup fault has no active coroutine state")
	}
	typeWord, dataWord := p.materializeCoroFaultPayload(b, kind)
	s.replacePanic(b, typeWord, dataWord)
}
