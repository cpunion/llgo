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
	"go/token"
	"go/types"

	llssa "github.com/goplus/llgo/ssa"
)

// compileCoroFFICall is the managed reflection stack cut. Stock ffi_call
// invokes the descriptor's typed coroutine entry only until that entry returns
// its initially suspended child handle. No libffi-owned frame survives the
// suspension: the ordinary scheduler child transaction begins only after the
// C call below has returned.
func (p *context) compileCoroFFICall(b llssa.Builder, args []llssa.Expr) {
	task := p.coroTask()
	if task.IsNil() || b.Func != p.fn || len(args) != 5 {
		panic("llgo.coroFFICall requires five arguments in the active coroutine function")
	}
	cif, entry, out, gslot, argv := args[0], args[1], args[2], args[3], args[4]
	b.Store(gslot, task)

	childSlot := p.coroFrameAlloca(p.prog.VoidPtr())
	ffiCall := p.pkg.NewFunc("ffi_call", coroFFIRawCallSignature(), llssa.InC)
	b.Call(
		ffiCall.Expr,
		b.Convert(p.prog.VoidPtr(), cif),
		b.Convert(p.prog.VoidPtr(), entry),
		b.Convert(p.prog.VoidPtr(), childSlot),
		b.Convert(p.prog.VoidPtr(), argv),
	)
	child := b.Load(childSlot)
	p.awaitCoroChild(b, child, b.Convert(p.prog.VoidPtr(), out), nil)
}

func coroFFIRawCallSignature() *types.Signature {
	params := make([]*types.Var, 4)
	for index := range params {
		params[index] = types.NewParam(token.NoPos, nil, "", types.Typ[types.UnsafePointer])
	}
	return types.NewSignatureType(
		nil,
		nil,
		nil,
		types.NewTuple(params...),
		types.NewTuple(),
		false,
	)
}
