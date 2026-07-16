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

package ssa

import (
	"encoding/binary"
	"fmt"
	"go/types"
	"strconv"
	"strings"

	"github.com/xgo-dev/llvm"
)

// CoroFrameOps emits target-independent coroutine frame allocation calls.
//
// The callbacks run at the builder's current insertion point. They deliberately
// receive both llvm.coro.size and the effective required allocation alignment
// so a later runtime can capture a frame descriptor without this package fixing
// that runtime's ABI. The alignment is at least llvm.coro.align and at least the
// guarantee declared by CoroOptions.AllocationAlign. Free is called only when
// llvm.coro.free returns a non-null allocation pointer. Each callback may append
// instructions but must leave the builder in the same unterminated basic block;
// CoroBuilder appends the required control-flow edge immediately afterwards.
// When the llvm.coro.alloc path executes, Alloc must return a non-null pointer;
// a target runtime must handle allocation failure before returning to the ramp.
type CoroFrameOps struct {
	Alloc func(b Builder, size, align Expr) Expr
	Free  func(b Builder, frame, size, align Expr)
}

// CoroOptions configures one LLVM switched-resume coroutine.
//
// Promise may be Nil when no promise is required. A non-Nil Promise must point
// to the alloca designated as the LLVM coroutine promise.
//
// AllocationAlign is the alignment guarantee passed to llvm.coro.id for memory
// returned by Frame.Alloc. Zero uses LLVM's default guarantee of twice the
// target pointer size. A non-zero value must be a power of two. Frame.Alloc is
// always passed an effective alignment that satisfies this guarantee as well as
// llvm.coro.align.
type CoroOptions struct {
	Promise Expr
	Frame   CoroFrameOps
	// BeforeInitialSuspend runs after llvm.coro.begin has produced the handle
	// and before the initial suspend is published. storage is the allocation
	// pointer passed to coro.begin (and may be null when allocation was elided).
	// The callback may initialize the promise/header and register the
	// handle/storage pair, but must leave the builder in the same unterminated
	// insertion block.
	BeforeInitialSuspend func(b Builder, handle, storage Expr)
	AllocationAlign      uint32
}

// CoroFrameDescriptorOptions describes the target-specific constant passed to
// the coroutine frame allocator and deallocator. ABIHash is computed by the
// frontend from the complete logical/physical function ABI. Result is the
// external result-slot payload type and must be non-nil.
type CoroFrameDescriptorOptions struct {
	Version uint32
	ABIHash [16]byte
	Flags   uint32
	Result  Type
}

// CoroRootFactoryDescriptorOptions describes the target-specific constant
// used to create a root coroutine. ABIHash is computed by the frontend from
// the complete logical/physical root ABI. Factory must be a constant function
// declaration or function pointer in the same package module with the fixed
// (unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) -> unsafe.Pointer ABI.
// Startup and Result are payload types from the package Program, not pointer
// types, and must be non-nil concrete types.
type CoroRootFactoryDescriptorOptions struct {
	Version uint32
	ABIHash [16]byte
	Flags   uint32
	Factory Expr
	Startup Type
	Result  Type
}

// NewCoroFrameDescriptor defines a link-once constant descriptor with layout:
//
//	{ version i32, flags i32, hashLo i64, hashHi i64,
//	  resultSize uintptr, resultAlign uintptr }
//
// The returned expression points at the descriptor. The hash words use big
// endian byte order so their textual IR form is deterministic across hosts.
func (p Package) NewCoroFrameDescriptor(name string, opts CoroFrameDescriptorOptions) Expr {
	if name == "" {
		panic("ssa: coroutine frame descriptor requires a name")
	}
	if opts.Result == nil {
		panic("ssa: coroutine frame descriptor requires a result type")
	}
	prog := p.Prog
	descriptorType := prog.Struct(
		prog.Uint32(),
		prog.Uint32(),
		prog.Uint64(),
		prog.Uint64(),
		prog.Uintptr(),
		prog.Uintptr(),
	)
	descriptor := p.NewVarEx(name, prog.Pointer(descriptorType))
	fields := []llvm.Value{
		prog.IntVal(uint64(opts.Version), prog.Uint32()).impl,
		prog.IntVal(uint64(opts.Flags), prog.Uint32()).impl,
		prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[:8]), prog.Uint64()).impl,
		prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[8:]), prog.Uint64()).impl,
		prog.IntVal(prog.SizeOf(opts.Result), prog.Uintptr()).impl,
		prog.IntVal(uint64(prog.td.ABITypeAlignment(opts.Result.ll)), prog.Uintptr()).impl,
	}
	descriptor.impl.SetInitializer(prog.ctx.ConstStruct(fields, false))
	descriptor.impl.SetGlobalConstant(true)
	descriptor.impl.SetLinkage(llvm.LinkOnceODRLinkage)
	descriptor.impl.SetUnnamedAddr(true)
	return descriptor.Expr
}

// NewCoroRootFactoryDescriptor defines a link-once constant descriptor with
// layout:
//
//	{ version i32, flags i32, hashLo i64, hashHi i64, factory ptr,
//	  startupSize uintptr, startupAlign uintptr,
//	  resultSize uintptr, resultAlign uintptr }
//
// The returned expression points at the descriptor. Size, alignment, and
// uintptr fields follow the package target data layout. The hash words use big
// endian byte order so their textual IR form is deterministic across hosts.
func (p Package) NewCoroRootFactoryDescriptor(
	name string, opts CoroRootFactoryDescriptorOptions,
) Expr {
	if name == "" {
		panic("ssa: coroutine root factory descriptor requires a name")
	}
	if opts.Factory.IsNil() ||
		(opts.Factory.kind != vkFuncDecl && opts.Factory.kind != vkFuncPtr) ||
		opts.Factory.impl.IsAConstant().IsNil() ||
		!opts.Factory.impl.IsAConstantPointerNull().IsNil() {
		panic("ssa: coroutine root factory descriptor requires a non-null constant function factory")
	}
	factoryFunction := coroRootFactoryFunction(opts.Factory.impl)
	if factoryFunction.IsNil() || factoryFunction.GlobalParent().C != p.mod.C {
		panic("ssa: coroutine root factory descriptor requires a factory from the same package module")
	}
	if !isCoroRootFactorySignature(opts.Factory.RawType()) {
		panic("ssa: coroutine root factory descriptor requires factory signature (unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) -> unsafe.Pointer")
	}
	if opts.Startup == nil || opts.Startup.kind == vkInvalid {
		panic("ssa: coroutine root factory descriptor requires a concrete startup type")
	}
	if opts.Result == nil || opts.Result.kind == vkInvalid {
		panic("ssa: coroutine root factory descriptor requires a concrete result type")
	}

	prog := p.Prog
	if opts.Startup.ll.Context().C != prog.ctx.C {
		panic("ssa: coroutine root factory descriptor startup type belongs to another program")
	}
	if opts.Result.ll.Context().C != prog.ctx.C {
		panic("ssa: coroutine root factory descriptor result type belongs to another program")
	}
	descriptorType := prog.Struct(
		prog.Uint32(),
		prog.Uint32(),
		prog.Uint64(),
		prog.Uint64(),
		prog.VoidPtr(),
		prog.Uintptr(),
		prog.Uintptr(),
		prog.Uintptr(),
		prog.Uintptr(),
	)
	descriptor := p.NewVarEx(name, prog.Pointer(descriptorType))
	factory := opts.Factory.impl
	if factory.Type().C != prog.VoidPtr().ll.C {
		factory = llvm.ConstBitCast(factory, prog.VoidPtr().ll)
	}
	fields := []llvm.Value{
		prog.IntVal(uint64(opts.Version), prog.Uint32()).impl,
		prog.IntVal(uint64(opts.Flags), prog.Uint32()).impl,
		prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[:8]), prog.Uint64()).impl,
		prog.IntVal(binary.BigEndian.Uint64(opts.ABIHash[8:]), prog.Uint64()).impl,
		factory,
		prog.IntVal(prog.SizeOf(opts.Startup), prog.Uintptr()).impl,
		prog.IntVal(uint64(prog.td.ABITypeAlignment(opts.Startup.ll)), prog.Uintptr()).impl,
		prog.IntVal(prog.SizeOf(opts.Result), prog.Uintptr()).impl,
		prog.IntVal(uint64(prog.td.ABITypeAlignment(opts.Result.ll)), prog.Uintptr()).impl,
	}
	descriptor.impl.SetInitializer(prog.ctx.ConstStruct(fields, false))
	descriptor.impl.SetGlobalConstant(true)
	descriptor.impl.SetLinkage(llvm.LinkOnceODRLinkage)
	descriptor.impl.SetUnnamedAddr(true)
	// Root descriptors are runtime/linker discovery points and otherwise have
	// no ordinary IR user. llvm.used preserves the descriptor through final-link
	// dead stripping; its initializer keeps the typed wrapper reachable.
	p.markLLVMRetained(descriptor.impl)
	return descriptor.Expr
}

func coroRootFactoryFunction(value llvm.Value) llvm.Value {
	for !value.IsAConstantExpr().IsNil() && value.OperandsCount() == 1 {
		value = value.Operand(0)
	}
	return value.IsAFunction()
}

func isCoroRootFactorySignature(typ types.Type) bool {
	sig, ok := typ.(*types.Signature)
	if !ok || sig.Recv() != nil || sig.Variadic() || sig.Params().Len() != 3 || sig.Results().Len() != 1 {
		return false
	}
	pointer := types.Typ[types.UnsafePointer]
	for i := 0; i < sig.Params().Len(); i++ {
		if !types.Identical(sig.Params().At(i).Type(), pointer) {
			return false
		}
	}
	return types.Identical(sig.Results().At(0).Type(), pointer)
}

// CoroBuilder owns the structured presplit control flow for one coroutine.
// It does not define the promise, result, scheduler, or runtime frame ABI.
type CoroBuilder struct {
	b Builder

	id     llvm.Value
	handle Expr
	frame  CoroFrameOps
	// allocationAlign is the literal guarantee supplied to llvm.coro.id. Zero
	// retains LLVM's target-dependent 2*pointer default.
	allocationAlign uint32

	suspendBlk       BasicBlock
	cleanupBlk       BasicBlock
	initialResumeBlk BasicBlock
	finished         bool
}

// BeginCoro emits the coroutine allocation prologue and initial suspend. The
// enclosing function must return exactly one unsafe.Pointer coroutine handle.
// On return, b is positioned at the initial-resume body block.
func (b Builder) BeginCoro(opts CoroOptions) *CoroBuilder {
	validateCoroOptions(b, opts)
	markPresplitCoroutine(b.Func)

	prog := b.Prog
	fn := b.Func
	entryBlk := b.blk
	allocBlk := fn.MakeBlock()
	beginBlk := fn.MakeBlock()
	suspendBlk := fn.MakeBlock()
	cleanupBlk := fn.MakeBlock()

	promise := prog.Nil(prog.VoidPtr())
	if !opts.Promise.IsNil() {
		promise = b.Convert(prog.VoidPtr(), opts.Promise)
	}
	null := prog.Nil(prog.VoidPtr())
	align := prog.IntVal(uint64(opts.AllocationAlign), prog.Int32())
	id := b.coroIntrinsic(
		"llvm.coro.id",
		prog.ctx.TokenType(),
		[]llvm.Value{align.impl, promise.impl, null.impl, null.impl},
		"coro.id",
	)
	needAlloc := b.coroIntrinsic(
		"llvm.coro.alloc",
		prog.Bool().ll,
		[]llvm.Value{id},
		"coro.alloc",
	)
	b.If(Expr{needAlloc, prog.Bool()}, allocBlk, beginBlk)

	b.SetBlock(allocBlk)
	size, frameAlign := b.coroFrameLayout(opts.AllocationAlign)
	allocCallbackPoint := captureCoroFrameCallbackPoint(b)
	allocated := opts.Frame.Alloc(b, size, frameAlign)
	allocCallbackPoint.ensureContinuation(b, "allocator")
	if allocated.IsNil() || allocated.kind != vkPtr {
		panic("ssa: coroutine frame allocator returned a non-pointer expression")
	}
	allocated = b.Convert(prog.VoidPtr(), allocated)
	b.Jump(beginBlk)

	b.SetBlock(beginBlk)
	storage := b.Phi(prog.VoidPtr())
	storage.AddIncoming(b, []BasicBlock{entryBlk, allocBlk}, func(i int, _ BasicBlock) Expr {
		if i == 0 {
			return null
		}
		return allocated
	})
	handleValue := b.coroIntrinsic(
		"llvm.coro.begin",
		prog.VoidPtr().ll,
		[]llvm.Value{id, storage.impl},
		"coro.handle",
	)

	coro := &CoroBuilder{
		b:               b,
		id:              id,
		handle:          Expr{handleValue, prog.VoidPtr()},
		frame:           opts.Frame,
		allocationAlign: opts.AllocationAlign,
		suspendBlk:      suspendBlk,
		cleanupBlk:      cleanupBlk,
	}
	if callback := opts.BeforeInitialSuspend; callback != nil {
		callbackPoint := captureCoroFrameCallbackPoint(b)
		callback(b, coro.handle, storage.Expr)
		callbackPoint.ensureContinuation(b, "before-initial-suspend")
	}
	coro.initialResumeBlk = coro.emitSuspend(false)
	return coro
}

// Handle returns the coroutine handle produced by llvm.coro.begin.
func (c *CoroBuilder) Handle() Expr {
	if c == nil {
		return Nil
	}
	return c.handle
}

// InitialResumeBlock returns the block in which the source coroutine body must
// begin. It is distinct from the ramp entry block, which has already emitted
// allocation, coro.begin, and the initial suspend.
func (c *CoroBuilder) InitialResumeBlock() BasicBlock {
	if c == nil {
		return nil
	}
	return c.initialResumeBlk
}

// Suspend emits a non-final stack cut and positions the builder at the newly
// created resume block. Scheduler state and suspend reasons must be published
// by the caller before invoking Suspend.
func (c *CoroBuilder) Suspend() BasicBlock {
	c.requireActive("suspend")
	return c.emitSuspend(false)
}

// Finish emits the final suspend and completes the shared cleanup/return
// blocks. No further instructions may be emitted through c afterwards.
func (c *CoroBuilder) Finish() {
	c.requireActive("finish")
	c.finished = true

	b := c.b
	prog := b.Prog
	fn := b.Func
	finalResult := c.suspendIntrinsic(true)
	invalidResumeBlk := fn.MakeBlock()
	switchValue := b.impl.CreateSwitch(finalResult, c.suspendBlk.first, 2)
	switchValue.AddCase(llvm.ConstInt(prog.tyInt8(), 0, false), invalidResumeBlk.first)
	switchValue.AddCase(llvm.ConstInt(prog.tyInt8(), 1, false), c.cleanupBlk.first)

	b.SetBlock(invalidResumeBlk)
	b.coroIntrinsic("llvm.trap", prog.Void().ll, nil, "")
	b.Unreachable()

	b.SetBlock(c.cleanupBlk)
	frameValue := b.coroIntrinsic(
		"llvm.coro.free",
		prog.VoidPtr().ll,
		[]llvm.Value{c.id, c.handle.impl},
		"coro.frame",
	)
	frame := Expr{frameValue, prog.VoidPtr()}
	freeBlk := fn.MakeBlock()
	afterFreeBlk := fn.MakeBlock()
	nonNull := llvm.CreateICmp(b.impl, llvm.IntNE, frame.impl, prog.Nil(prog.VoidPtr()).impl)
	b.If(Expr{nonNull, prog.Bool()}, freeBlk, afterFreeBlk)

	b.SetBlock(freeBlk)
	size, align := b.coroFrameLayout(c.allocationAlign)
	freeCallbackPoint := captureCoroFrameCallbackPoint(b)
	c.frame.Free(b, frame, size, align)
	freeCallbackPoint.ensureContinuation(b, "free")
	b.Jump(afterFreeBlk)

	b.SetBlock(afterFreeBlk)
	b.Jump(c.suspendBlk)

	// LLVM's canonical switched-resume shape sends every suspend default edge
	// and the cleanup edge through one coro.end block. CoroSplit keeps the
	// following handle return in the ramp and replaces coro.end with ret void in
	// the resume/destroy functions.
	b.SetBlock(c.suspendBlk)
	b.coroEnd(c.handle)
	b.Return(c.handle)
}

func (c *CoroBuilder) emitSuspend(final bool) BasicBlock {
	b := c.b
	prog := b.Prog
	resumeBlk := b.Func.MakeBlock()
	result := c.suspendIntrinsic(final)
	switchValue := b.impl.CreateSwitch(result, c.suspendBlk.first, 2)
	switchValue.AddCase(llvm.ConstInt(prog.tyInt8(), 0, false), resumeBlk.first)
	switchValue.AddCase(llvm.ConstInt(prog.tyInt8(), 1, false), c.cleanupBlk.first)
	b.SetBlock(resumeBlk)
	return resumeBlk
}

func (c *CoroBuilder) suspendIntrinsic(final bool) llvm.Value {
	b := c.b
	return b.coroIntrinsic(
		"llvm.coro.suspend",
		b.Prog.Byte().ll,
		[]llvm.Value{b.Prog.ctx.ConstTokenNone(), b.Prog.BoolVal(final).impl},
		"coro.suspend",
	)
}

func (c *CoroBuilder) requireActive(operation string) {
	if c == nil {
		panic("ssa: " + operation + " nil coroutine builder")
	}
	if c.finished {
		panic("ssa: cannot " + operation + " finished coroutine")
	}
}

// CoroPromise returns a typed pointer to the promise associated with handle.
//
// promise is the promise payload type, not a pointer type. The generated
// llvm.coro.promise call uses the target ABI alignment of that payload and the
// handle-to-promise direction (from=false). The handle must be a pointer-valued
// expression produced by llvm.coro.begin or otherwise supplied by the
// coroutine runtime.
func (b Builder) CoroPromise(handle Expr, promise Type) Expr {
	b.requireCoroHandle("get promise for", handle)
	if promise == nil || promise.kind == vkInvalid {
		panic("ssa: coroutine promise requires a concrete payload type")
	}

	prog := b.Prog
	promisePtr := prog.Pointer(promise)
	value := b.coroIntrinsic(
		"llvm.coro.promise",
		promisePtr.ll,
		[]llvm.Value{
			b.Convert(prog.VoidPtr(), handle).impl,
			prog.IntVal(uint64(prog.td.ABITypeAlignment(promise.ll)), prog.Int32()).impl,
			prog.BoolVal(false).impl,
		},
		"coro.promise",
	)
	return Expr{value, promisePtr}
}

// CoroDone reports whether a suspended coroutine is at its final suspend.
// Calling it for a running coroutine or a coroutine without a final suspend is
// invalid according to LLVM's coroutine contract.
func (b Builder) CoroDone(handle Expr) Expr {
	b.requireCoroHandle("query done for", handle)
	prog := b.Prog
	value := b.coroIntrinsic(
		"llvm.coro.done",
		prog.Bool().ll,
		[]llvm.Value{b.Convert(prog.VoidPtr(), handle).impl},
		"coro.done",
	)
	return Expr{value, prog.Bool()}
}

// CoroResume resumes a suspended coroutine. A final-suspended coroutine must
// be destroyed instead and must never be resumed.
func (b Builder) CoroResume(handle Expr) {
	b.requireCoroHandle("resume", handle)
	b.coroIntrinsic(
		"llvm.coro.resume",
		b.Prog.Void().ll,
		[]llvm.Value{b.Convert(b.Prog.VoidPtr(), handle).impl},
		"",
	)
}

// CoroDestroy destroys a suspended coroutine exactly once.
func (b Builder) CoroDestroy(handle Expr) {
	b.requireCoroHandle("destroy", handle)
	b.coroIntrinsic(
		"llvm.coro.destroy",
		b.Prog.Void().ll,
		[]llvm.Value{b.Convert(b.Prog.VoidPtr(), handle).impl},
		"",
	)
}

func (b Builder) requireCoroHandle(operation string, handle Expr) {
	if b == nil || b.Func == nil || b.blk == nil {
		panic("ssa: cannot " + operation + " coroutine without an active function block")
	}
	if handle.IsNil() || handle.kind != vkPtr {
		panic("ssa: coroutine handle must be a pointer")
	}
}

func validateCoroOptions(b Builder, opts CoroOptions) {
	if b == nil || b.Func == nil || b.blk == nil {
		panic("ssa: begin coroutine without an active function block")
	}
	sig, ok := b.Func.raw.Type.(*types.Signature)
	if !ok || sig.Results().Len() != 1 ||
		!types.Identical(sig.Results().At(0).Type(), types.Typ[types.UnsafePointer]) {
		panic("ssa: coroutine function must return exactly one unsafe.Pointer handle")
	}
	if opts.Frame.Alloc == nil || opts.Frame.Free == nil {
		panic("ssa: coroutine frame allocator and free callbacks are required")
	}
	if opts.Promise.IsNil() {
		// A nil promise is valid independently of the frame allocation guarantee.
	} else if opts.Promise.kind != vkPtr {
		panic("ssa: coroutine promise must be a pointer")
	}
	if opts.AllocationAlign != 0 && opts.AllocationAlign&(opts.AllocationAlign-1) != 0 {
		panic("ssa: coroutine allocation alignment must be zero or a power of two")
	}
}

type coroFrameCallbackPoint struct {
	blk          BasicBlock
	insert       llvm.BasicBlock
	instructions []llvm.Value
}

func captureCoroFrameCallbackPoint(b Builder) coroFrameCallbackPoint {
	insert := b.impl.GetInsertBlock()
	return coroFrameCallbackPoint{
		blk:          b.blk,
		insert:       insert,
		instructions: coroBlockInstructions(insert),
	}
}

func (p coroFrameCallbackPoint) ensureContinuation(b Builder, callback string) {
	if b.blk != p.blk || b.impl.GetInsertBlock().C != p.insert.C {
		panic("ssa: coroutine frame " + callback + " callback changed insertion block")
	}
	current := coroBlockInstructions(p.insert)
	if len(current) < len(p.instructions) {
		panic("ssa: coroutine frame " + callback + " callback modified instructions before append point")
	}
	for i, instruction := range p.instructions {
		if current[i].C != instruction.C {
			panic("ssa: coroutine frame " + callback + " callback modified instructions before append point")
		}
	}
	for _, inst := range current {
		switch inst.InstructionOpcode() {
		case llvm.Ret, llvm.Br, llvm.Switch, llvm.IndirectBr, llvm.Invoke,
			llvm.Unreachable, llvm.Resume, llvm.CleanupRet, llvm.CatchRet,
			llvm.CatchSwitch:
			panic("ssa: coroutine frame " + callback + " callback terminated insertion block")
		}
	}
	// The callbacks are append-only. Re-establish the insertion point at the
	// end before CoroBuilder emits its own control-flow edge.
	b.impl.SetInsertPointAtEnd(p.insert)
}

func coroBlockInstructions(block llvm.BasicBlock) []llvm.Value {
	var instructions []llvm.Value
	for inst := block.FirstInstruction(); !inst.IsNil(); inst = llvm.NextInstruction(inst) {
		instructions = append(instructions, inst)
	}
	return instructions
}

func markPresplitCoroutine(fn Function) {
	major := llvmMajorVersion()
	ctx := fn.Pkg.mod.Context()
	if major == 14 {
		// LLVM 14's string attribute encodes a legacy state machine. Frontends
		// must emit the unprepared "0" state before CoroEarly; "1" is reserved
		// for a coroutine already prepared for a direct CoroSplit invocation.
		fn.impl.AddFunctionAttr(ctx.CreateStringAttribute("coroutine.presplit", "0"))
		return
	}
	kind := llvm.AttributeKindID("presplitcoroutine")
	if kind == 0 {
		panic(fmt.Sprintf("ssa: LLVM %s has no presplitcoroutine attribute", llvm.Version))
	}
	fn.impl.AddFunctionAttr(ctx.CreateEnumAttribute(kind, 0))
}

func (b Builder) coroFrameLayout(allocationAlign uint32) (size, align Expr) {
	typ := b.Prog.Uintptr()
	sizeValue := b.coroIntrinsic("llvm.coro.size", typ.ll, nil, "coro.size")
	alignValue := b.coroIntrinsic("llvm.coro.align", typ.ll, nil, "coro.align")
	minimum := uint64(allocationAlign)
	if minimum == 0 {
		minimum = uint64(2 * b.Prog.PointerSize())
	}
	minimumValue := llvm.ConstInt(typ.ll, minimum, false)
	belowMinimum := llvm.CreateICmp(b.impl, llvm.IntULT, alignValue, minimumValue)
	effectiveAlign := b.impl.CreateSelect(belowMinimum, minimumValue, alignValue, "coro.alloc.align")
	return Expr{sizeValue, typ}, Expr{effectiveAlign, typ}
}

func (b Builder) coroEnd(handle Expr) {
	major := llvmMajorVersion()
	args := []llvm.Value{handle.impl, b.Prog.BoolVal(false).impl}
	if major >= 18 {
		args = append(args, b.Prog.ctx.ConstTokenNone())
	}
	ret := b.Prog.Bool().ll
	name := "coro.end"
	if major >= 22 {
		ret = b.Prog.Void().ll
		name = ""
	}
	b.coroIntrinsic("llvm.coro.end", ret, args, name)
}

func (b Builder) coroIntrinsic(name string, ret llvm.Type, args []llvm.Value, resultName string) llvm.Value {
	id := llvm.LookupIntrinsicID(name)
	if id == 0 {
		panic(fmt.Sprintf("ssa: LLVM %s has no %s intrinsic", llvm.Version, name))
	}
	value := b.impl.CreateIntrinsic(ret, id, args, resultName)
	if value.IsNil() {
		panic(fmt.Sprintf("ssa: LLVM %s rejected %s intrinsic signature", llvm.Version, name))
	}
	return value
}

func llvmMajorVersion() int {
	text, _, _ := strings.Cut(llvm.Version, ".")
	major, err := strconv.Atoi(text)
	if err != nil {
		panic(fmt.Sprintf("ssa: parse LLVM version %q: %v", llvm.Version, err))
	}
	return major
}
