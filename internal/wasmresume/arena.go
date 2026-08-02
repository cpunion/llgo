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

package wasmresume

import "github.com/xgo-dev/llvm"

// Fast paths know only the pointer-sized frameBlock prefix shared with the
// runtime. Block creation, retained-block selection, and final reclamation stay
// behind the runtime ABI.
const (
	frameAllocFastName        = "__llgo_wasm_resume_alloc.fast"
	frameDynamicAllocFastName = "__llgo_wasm_resume_alloc_dynamic.fast"
	frameFreeFastName         = "__llgo_wasm_resume_free.fast"
)

func declareFrameAllocator(mod llvm.Module, abi resumeABI) llvm.Value {
	return defineFastFrameAllocator(mod, abi, frameAllocFastName, frameAllocName)
}

func declareDynamicAllocator(mod llvm.Module, abi resumeABI) llvm.Value {
	return defineFastFrameAllocator(
		mod, abi, frameDynamicAllocFastName, frameDynamicAllocName,
	)
}

func declareFrameFree(mod llvm.Module, abi resumeABI) llvm.Value {
	fn := mod.NamedFunction(frameFreeFastName)
	if !fn.IsNil() {
		return fn
	}

	ctx := mod.Context()
	fnType := llvm.FunctionType(ctx.VoidType(), []llvm.Type{abi.ptr, abi.ptr}, false)
	fn = llvm.AddFunction(mod, frameFreeFastName, fnType)
	fn.SetLinkage(llvm.InternalLinkage)
	fn.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("noinline"), 0))

	slow := mod.NamedFunction(frameFreeName)
	if slow.IsNil() {
		slow = llvm.AddFunction(mod, frameFreeName, fnType)
	}
	blockType := frameBlockType(abi)
	entry := ctx.AddBasicBlock(fn, "entry")
	bounds := ctx.AddBasicBlock(fn, "bounds")
	header := ctx.AddBasicBlock(fn, "header")
	fast := ctx.AddBasicBlock(fn, "fast")
	slowPath := ctx.AddBasicBlock(fn, "slow")

	builder := ctx.NewBuilder()
	defer builder.Dispose()
	builder.SetInsertPointAtEnd(entry)
	currentField := builder.CreateStructGEP(abi.contextType, fn.Param(0), 2, "")
	block := builder.CreateLoad(abi.ptr, currentField, "frame.block")
	builder.CreateCondBr(
		builder.CreateICmp(llvm.IntNE, block, llvm.ConstNull(abi.ptr), ""),
		bounds,
		slowPath,
	)

	builder.SetInsertPointAtEnd(bounds)
	begin := builder.CreateLoad(
		abi.uintptrType, builder.CreateStructGEP(blockType, block, 2, ""), "frame.begin",
	)
	stackPointerField := builder.CreateStructGEP(blockType, block, 4, "")
	stackPointer := builder.CreateLoad(abi.uintptrType, stackPointerField, "frame.sp")
	frameAddress := builder.CreatePtrToInt(fn.Param(1), abi.uintptrType, "frame.address")
	pointerSize := llvm.ConstInt(abi.uintptrType, uint64(abi.uintptrType.IntTypeWidth()/8), false)
	minimum := builder.CreateAdd(begin, pointerSize, "frame.minimum")
	inBounds := builder.CreateAnd(
		builder.CreateICmp(llvm.IntUGE, frameAddress, minimum, ""),
		builder.CreateICmp(llvm.IntULT, frameAddress, stackPointer, ""),
		"frame.in.bounds",
	)
	builder.CreateCondBr(inBounds, header, slowPath)

	builder.SetInsertPointAtEnd(header)
	headerAddress := builder.CreateSub(frameAddress, pointerSize, "frame.header")
	previous := builder.CreateLoad(
		abi.uintptrType,
		builder.CreateIntToPtr(headerAddress, abi.ptr, "frame.header.ptr"),
		"frame.previous",
	)
	previousBlock := builder.CreateLoad(
		abi.ptr, builder.CreateStructGEP(blockType, block, 0, ""), "frame.previous.block",
	)
	validHeader := builder.CreateAnd(
		builder.CreateICmp(llvm.IntUGE, previous, begin, ""),
		builder.CreateICmp(llvm.IntULT, previous, frameAddress, ""),
		"frame.valid.header",
	)
	needsBlockPop := builder.CreateAnd(
		builder.CreateICmp(llvm.IntEQ, previous, begin, ""),
		builder.CreateICmp(llvm.IntNE, previousBlock, llvm.ConstNull(abi.ptr), ""),
		"frame.needs.block.pop",
	)
	builder.CreateCondBr(
		builder.CreateAnd(validHeader, builder.CreateNot(needsBlockPop, ""), "frame.fast"),
		fast,
		slowPath,
	)

	builder.SetInsertPointAtEnd(fast)
	builder.CreateIntrinsic(ctx.VoidType(), llvm.LookupIntrinsicID("llvm.memset"), []llvm.Value{
		builder.CreateIntToPtr(previous, abi.ptr, "frame.clear.begin"),
		llvm.ConstInt(ctx.Int8Type(), 0, false),
		builder.CreateSub(stackPointer, previous, "frame.clear.size"),
		llvm.ConstInt(ctx.Int1Type(), 0, false),
	}, "")
	builder.CreateStore(previous, stackPointerField)
	builder.CreateRetVoid()

	builder.SetInsertPointAtEnd(slowPath)
	builder.CreateCall(slow.GlobalValueType(), slow, []llvm.Value{fn.Param(0), fn.Param(1)}, "")
	builder.CreateRetVoid()
	return fn
}

func defineFastFrameAllocator(
	mod llvm.Module,
	abi resumeABI,
	fastName, slowName string,
) llvm.Value {
	fn := mod.NamedFunction(fastName)
	if !fn.IsNil() {
		return fn
	}

	ctx := mod.Context()
	fnType := llvm.FunctionType(
		abi.ptr, []llvm.Type{abi.ptr, abi.uintptrType, abi.uintptrType}, false,
	)
	fn = llvm.AddFunction(mod, fastName, fnType)
	fn.SetLinkage(llvm.InternalLinkage)
	fn.AddFunctionAttr(ctx.CreateEnumAttribute(llvm.AttributeKindID("noinline"), 0))

	slow := mod.NamedFunction(slowName)
	if slow.IsNil() {
		slow = llvm.AddFunction(mod, slowName, fnType)
	}
	blockType := frameBlockType(abi)
	entry := ctx.AddBasicBlock(fn, "entry")
	check := ctx.AddBasicBlock(fn, "check")
	fast := ctx.AddBasicBlock(fn, "fast")
	slowPath := ctx.AddBasicBlock(fn, "slow")

	builder := ctx.NewBuilder()
	defer builder.Dispose()
	builder.SetInsertPointAtEnd(entry)
	currentField := builder.CreateStructGEP(abi.contextType, fn.Param(0), 2, "")
	block := builder.CreateLoad(abi.ptr, currentField, "frame.block")
	builder.CreateCondBr(
		builder.CreateICmp(llvm.IntNE, block, llvm.ConstNull(abi.ptr), ""),
		check,
		slowPath,
	)

	builder.SetInsertPointAtEnd(check)
	stackPointerField := builder.CreateStructGEP(blockType, block, 4, "")
	stackPointer := builder.CreateLoad(abi.uintptrType, stackPointerField, "frame.sp")
	end := builder.CreateLoad(
		abi.uintptrType, builder.CreateStructGEP(blockType, block, 3, ""), "frame.end",
	)
	pointerSize := llvm.ConstInt(abi.uintptrType, uint64(abi.uintptrType.IntTypeWidth()/8), false)
	header := builder.CreateAdd(stackPointer, pointerSize, "frame.header")
	alignMask := builder.CreateSub(
		fn.Param(2), llvm.ConstInt(abi.uintptrType, 1, false), "frame.align.mask",
	)
	padded := builder.CreateAdd(header, alignMask, "frame.padded")
	negativeAlign := builder.CreateSub(
		llvm.ConstNull(abi.uintptrType), fn.Param(2), "frame.negative.align",
	)
	frameAddress := builder.CreateAnd(padded, negativeAlign, "frame.address")
	next := builder.CreateAdd(frameAddress, fn.Param(1), "frame.next")
	fits := builder.CreateAnd(
		builder.CreateAnd(
			builder.CreateICmp(llvm.IntUGE, header, stackPointer, ""),
			builder.CreateICmp(llvm.IntUGE, padded, header, ""),
			"frame.header.valid",
		),
		builder.CreateAnd(
			builder.CreateICmp(llvm.IntUGE, frameAddress, header, ""),
			builder.CreateAnd(
				builder.CreateICmp(llvm.IntUGE, next, frameAddress, ""),
				builder.CreateICmp(llvm.IntULE, next, end, ""),
				"frame.end.valid",
			),
			"frame.bounds.valid",
		),
		"frame.fits",
	)
	builder.CreateCondBr(fits, fast, slowPath)

	builder.SetInsertPointAtEnd(fast)
	headerAddress := builder.CreateSub(frameAddress, pointerSize, "frame.header.address")
	builder.CreateStore(
		stackPointer, builder.CreateIntToPtr(headerAddress, abi.ptr, "frame.header.ptr"),
	)
	builder.CreateStore(next, stackPointerField)
	builder.CreateRet(builder.CreateIntToPtr(frameAddress, abi.ptr, "frame.ptr"))

	builder.SetInsertPointAtEnd(slowPath)
	allocated := builder.CreateCall(
		slow.GlobalValueType(), slow,
		[]llvm.Value{fn.Param(0), fn.Param(1), fn.Param(2)},
		"frame.slow",
	)
	builder.CreateRet(allocated)
	return fn
}

func frameBlockType(abi resumeABI) llvm.Type {
	return abi.ctx.StructType([]llvm.Type{
		abi.ptr,
		abi.ptr,
		abi.uintptrType,
		abi.uintptrType,
		abi.uintptrType,
	}, false)
}
