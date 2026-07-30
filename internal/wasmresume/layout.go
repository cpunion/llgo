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

const frameHeaderFields = 3

type frameLayout struct {
	plan      framePlan
	typ       llvm.Type
	size      uint64
	alignment int
}

func layoutFrames(mod llvm.Module, targetData llvm.TargetData) ([]frameLayout, error) {
	plans, err := planFrames(mod)
	if err != nil {
		return nil, err
	}
	ctx := mod.Context()
	ptr := llvm.PointerType(ctx.Int8Type(), 0)
	header := []llvm.Type{ptr, ptr, ctx.Int32Type()}

	layouts := make([]frameLayout, len(plans))
	for i, plan := range plans {
		fields := make([]llvm.Type, frameHeaderFields, frameHeaderFields+len(plan.slots))
		copy(fields, header)
		for _, slot := range plan.slots {
			fields = append(fields, slot.typ)
		}
		typ := ctx.StructType(fields, false)
		layouts[i] = frameLayout{
			plan:      plan,
			typ:       typ,
			size:      targetData.TypeAllocSize(typ),
			alignment: targetData.ABITypeAlignment(typ),
		}
	}
	return layouts, nil
}

func (l frameLayout) fieldIndex(slotID uint32) int {
	if slotID == 0 || int(slotID) > len(l.plan.slots) {
		return -1
	}
	return frameHeaderFields + int(slotID) - 1
}
