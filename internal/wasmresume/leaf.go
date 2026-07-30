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

import (
	"fmt"

	"github.com/xgo-dev/llvm"
)

const (
	resumeEntryPrefix = "__llgo_wasm_resume."
	descriptorPrefix  = "__llgo_wasm_resume_desc."
	actionReturn      = 1
)

type loweredLeaf struct {
	layout     frameLayout
	entry      llvm.Value
	descriptor llvm.Value
}

// emitLeafEntries emits the descriptor ABI for functions which cannot suspend
// below their own frame. Non-leaf state-machine lowering is a later stage.
func emitLeafEntries(mod llvm.Module, targetData llvm.TargetData) ([]loweredLeaf, error) {
	layouts, err := layoutFrames(mod, targetData)
	if err != nil {
		return nil, err
	}

	ctx := mod.Context()
	ptr := llvm.PointerType(ctx.Int8Type(), 0)
	uintptrType := ctx.IntType(targetData.PointerSize() * 8)
	entryType := llvm.FunctionType(ctx.Int8Type(), []llvm.Type{ptr, ptr}, false)
	descriptorType := ctx.StructType([]llvm.Type{ptr, uintptrType, uintptrType}, false)

	var lowered []loweredLeaf
	for _, layout := range layouts {
		fn := layout.plan.function
		if fn.IsDeclaration() || len(layout.plan.calls) != 0 {
			continue
		}
		entryName := resumeEntryPrefix + fn.Name()
		descriptorName := descriptorPrefix + fn.Name()
		if !mod.NamedFunction(entryName).IsNil() || !mod.NamedGlobal(descriptorName).IsNil() {
			return nil, fmt.Errorf("%s: duplicate resumable descriptor", fn.Name())
		}

		entry := llvm.AddFunction(mod, entryName, entryType)
		entry.SetLinkage(llvm.InternalLinkage)
		block := ctx.AddBasicBlock(entry, "entry")
		builder := ctx.NewBuilder()
		builder.SetInsertPointAtEnd(block)

		rawFrame := entry.Param(1)
		params := make([]llvm.Value, 0, fn.ParamsCount())
		for _, slot := range layout.plan.slots {
			if slot.kind != slotParameter {
				continue
			}
			field := builder.CreateStructGEP(layout.typ, rawFrame, layout.fieldIndex(slot.id), "")
			params = append(params, builder.CreateLoad(slot.typ, field, slot.value.Name()))
		}
		call := builder.CreateCall(fn.GlobalValueType(), fn, params, "")
		call.SetInstructionCallConv(fn.FunctionCallConv())
		if layout.plan.resultSlot != 0 {
			field := builder.CreateStructGEP(
				layout.typ, rawFrame, layout.fieldIndex(layout.plan.resultSlot), "",
			)
			builder.CreateStore(call, field)
		}
		builder.CreateRet(llvm.ConstInt(ctx.Int8Type(), actionReturn, false))
		builder.Dispose()

		descriptor := llvm.AddGlobal(mod, descriptorType, descriptorName)
		descriptor.SetGlobalConstant(true)
		descriptor.SetInitializer(ctx.ConstStruct([]llvm.Value{
			entry,
			llvm.ConstInt(uintptrType, layout.size, false),
			llvm.ConstInt(uintptrType, uint64(layout.alignment), false),
		}, false))

		lowered = append(lowered, loweredLeaf{
			layout: layout, entry: entry, descriptor: descriptor,
		})
	}
	return lowered, nil
}
