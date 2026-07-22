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

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// compileCoroExplicitStatusPanic owns a terminal source instruction selected
// by the frozen physical outcome recipe. Preflight has
// already proved that X is one empty-interface value whose type/data words
// remain valid after this coroutine frame is destroyed; reaching this path
// with any other shape is a compiler-plan violation, never permission to fall
// back to the legacy runtime.Panic call.
func (p *context) compileCoroExplicitStatusPanic(b llssa.Builder, instruction *ssa.Panic) {
	body := p.coroBody()
	if instruction == nil || body == nil || !p.coroEmissionExplicitStatus() || b == nil || b.Func != p.fn {
		goName, llvmName := "<nil>", "<nil>"
		if p.goFn != nil {
			goName = p.goFn.String()
		}
		if p.fn != nil {
			llvmName = p.fn.Name()
		}
		panic(fmt.Errorf(
			"explicit-status panic in %q (%s) escaped its exact physical coroutine body (active=%t builder-matches=%t)",
			llvmName, goName, body != nil, b != nil && b.Func == p.fn,
		))
	}
	value := p.compileValue(b, instruction.X)
	typeWord := b.EfaceType(value)
	dataWord := b.InterfaceData(value)
	if body.cleanup == nil {
		body.panic(b, typeWord, dataWord)
	} else {
		body.cleanup.enterPanic(b, typeWord, dataWord)
	}
}
