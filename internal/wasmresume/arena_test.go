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
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestFrameArenaABILayout(t *testing.T) {
	for _, layout := range []string{
		"e-m:e-p:32:32-i64:64-n32:64-S128",
		"e-m:e-p:64:64-i64:64-n32:64-S128",
	} {
		t.Run(layout, func(t *testing.T) {
			ctx := llvm.NewContext()
			defer ctx.Dispose()
			targetData := llvm.NewTargetData(layout)
			defer targetData.Dispose()
			abi := newResumeABI(ctx, targetData)
			pointerSize := uint64(targetData.PointerSize())

			if got, want := targetData.TypeAllocSize(abi.contextType), 3*pointerSize; got != want {
				t.Fatalf("Context size = %d, want %d", got, want)
			}
			if got, want := targetData.ElementOffset(abi.contextType, 2), 2*pointerSize; got != want {
				t.Fatalf("Context storage offset = %d, want %d", got, want)
			}

			block := frameBlockType(abi)
			if got, want := targetData.TypeAllocSize(block), 5*pointerSize; got != want {
				t.Fatalf("frameBlock size = %d, want %d", got, want)
			}
			for field := range 5 {
				if got, want := targetData.ElementOffset(block, field), uint64(field)*pointerSize; got != want {
					t.Fatalf("frameBlock field %d offset = %d, want %d", field, got, want)
				}
			}
		})
	}
}

func TestFrameArenaFastPathsAreNotInlined(t *testing.T) {
	ctx := llvm.NewContext()
	defer ctx.Dispose()
	mod := ctx.NewModule("frame-arena-fast-paths")
	defer mod.Dispose()
	targetData := llvm.NewTargetData("e-m:e-p:32:32-i64:64-n32:64-S128")
	defer targetData.Dispose()
	abi := newResumeABI(ctx, targetData)

	functions := []llvm.Value{
		declareFrameAllocator(mod, abi),
		declareDynamicAllocator(mod, abi),
		declareFrameFree(mod, abi),
	}
	kind := llvm.AttributeKindID("noinline")
	for _, fn := range functions {
		if fn.GetEnumFunctionAttribute(kind).IsNil() {
			t.Errorf("%s is missing the noinline attribute", fn.Name())
		}
	}
}
