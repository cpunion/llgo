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

// tryCompileCoroExplicitStatusPanic owns the terminal source instruction when
// the compilation-wide ExplicitStatus identity is active. Preflight has
// already proved that X is one pure, concrete empty-interface construction;
// reaching this path with any other shape is a compiler-plan violation, never
// permission to fall back to the legacy runtime.Panic call.
func (p *context) tryCompileCoroExplicitStatusPanic(b llssa.Builder, instruction *ssa.Panic) bool {
	if p.compilation == nil || !p.compilation.EnableCoroExplicitStatusPanicABI {
		return false
	}
	if instruction == nil || p.currentCoro == nil || b.Func != p.fn {
		panic(fmt.Errorf("explicit-status panic escaped its exact physical coroutine body"))
	}
	if _, ok := instruction.X.(*ssa.MakeInterface); !ok {
		panic(fmt.Errorf("explicit-status panic operand escaped its concrete MakeInterface preflight"))
	}
	value := p.compileValue(b, instruction.X)
	typeWord := b.EfaceType(value)
	dataWord := b.InterfaceData(value)
	if p.currentCoro.cleanup == nil {
		p.currentCoro.panic(b, typeWord, dataWord)
	} else {
		p.currentCoro.cleanup.enterPanic(b, typeWord, dataWord)
	}
	return true
}
