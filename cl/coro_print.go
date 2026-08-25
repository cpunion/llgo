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
	"go/types"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func (p *context) compileCoroPrintBuiltin(
	b llssa.Builder,
	name string,
	arguments []ssa.Value,
) llssa.Expr {
	return p.compileCoroPrintValues(b, name, p.compileValues(b, arguments, fnNormal))
}

// compileCoroPrintValues reuses one function-wide descriptor array. Parent
// execution cannot reach another print site until the current PrintBatchV1
// child completes, so all source sites and deferred cleanup sites have
// non-overlapping ownership of this storage.
func (p *context) compileCoroPrintValues(
	b llssa.Builder,
	name string,
	arguments []llssa.Expr,
) llssa.Expr {
	if p == nil || b == nil || !p.hasCoroPhysicalBody() || b.Func != p.fn ||
		name != "print" && name != "println" {
		panic("coroutine print lowering requires one active physical owner and exact builtin")
	}
	argType := p.prog.RuntimeType("PrintArgV1")
	storage := p.prog.Nil(p.prog.Pointer(argType))
	capacity := 0
	if len(arguments) != 0 {
		if p.coroPrintScratchCap < 0 {
			p.coroPrintScratchCap = coroPrintBuiltinMaxArgs(p.goFn)
		}
		capacity = p.coroPrintScratchCap
		if capacity < len(arguments) {
			panic(fmt.Sprintf(
				"coroutine print scratch capacity %d is smaller than %d operands",
				capacity, len(arguments),
			))
		}
		if p.coroPrintScratch.IsNil() {
			array := p.prog.Type(
				types.NewArray(argType.RawType(), int64(capacity)),
				llssa.InGo,
			)
			frameArray := p.coroFrameAlloca(array)
			storage = b.Convert(
				p.prog.Pointer(argType),
				b.Convert(p.prog.VoidPtr(), frameArray),
			)
			p.coroPrintScratch = storage
			p.coroPrintScratchCap = capacity
		} else {
			if p.coroPrintScratchCap != capacity {
				panic("coroutine print scratch capacity changed within one physical body")
			}
			storage = p.coroPrintScratch
		}
	}
	return b.PrintExInStorage(storage, capacity, name == "println", arguments...)
}

func coroPrintBuiltinMaxArgs(function *ssa.Function) int {
	maximum := 0
	if function == nil {
		return maximum
	}
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok || call.Common() == nil {
				continue
			}
			builtin, ok := call.Common().Value.(*ssa.Builtin)
			if !ok || builtin.Name() != "print" && builtin.Name() != "println" {
				continue
			}
			if count := len(call.Common().Args); count > maximum {
				maximum = count
			}
		}
	}
	return maximum
}
