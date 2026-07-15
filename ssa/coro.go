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
	Promise         Expr
	AllocationAlign uint32
	Frame           CoroFrameOps
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

	suspendBlk BasicBlock
	cleanupBlk BasicBlock
	finished   bool
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
	coro.emitSuspend(false)
	return coro
}

// Handle returns the coroutine handle produced by llvm.coro.begin.
func (c *CoroBuilder) Handle() Expr {
	if c == nil {
		return Nil
	}
	return c.handle
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
