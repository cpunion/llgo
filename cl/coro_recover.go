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
	"go/types"

	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func isCoroRecoverBuiltinCall(call *ssa.Call) bool {
	if call == nil || call.Common() == nil {
		return false
	}
	builtin, ok := call.Common().Value.(*ssa.Builtin)
	return ok && builtin.Name() == "recover"
}

// compileCoroRecover replaces LLGo's legacy pthread-TLS Recover helper inside
// an explicit-status physical coroutine. The runtime validates the current
// frame against the exact parent-owned deferred-child scope and writes either
// the retained panic pair or two nil words. Constructing the empty interface
// directly keeps this operation allocation-free on every target.
func (p *context) compileCoroRecover(b llssa.Builder, call *ssa.CallCommon) llssa.Expr {
	recovery, ok := p.activeCoroRecoveryEmission()
	if !ok || !p.coroEmissionExplicitStatus() ||
		b.Func != p.fn || call == nil || len(call.Args) != 0 || recovery.takeHook == "" {
		panic("coroutine recover requires an exact explicit-status physical call")
	}
	result := call.Signature().Results()
	if result == nil || result.Len() != 1 {
		panic("coroutine recover requires one empty-interface result")
	}
	resultType := p.patchType(result.At(0).Type())
	iface, ok := types.Unalias(resultType).Underlying().(*types.Interface)
	if !ok || !iface.Empty() {
		panic("coroutine recover result is not an empty interface")
	}

	typeWord := p.coroFrameAlloca(p.prog.VoidPtr())
	dataWord := p.coroFrameAlloca(p.prog.VoidPtr())
	b.Store(typeWord, p.prog.Nil(p.prog.VoidPtr()))
	b.Store(dataWord, p.prog.Nil(p.prog.VoidPtr()))
	take := p.pkg.NewFunc(recovery.takeHook, coroRecoverTakeSignature(), llssa.InC)
	b.Call(
		take.Expr,
		recovery.task,
		recovery.handle,
		b.Convert(p.prog.VoidPtr(), typeWord),
		b.Convert(p.prog.VoidPtr(), dataWord),
	)
	return b.Aggregate(
		p.type_(resultType, llssa.InGo),
		b.Convert(p.prog.AbiTypePtr(), b.Load(typeWord)),
		b.Load(dataWord),
	)
}

func coroRecoverTakeSignature() *types.Signature {
	const noPos = 0
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(noPos, nil, "g", pointer),
		types.NewParam(noPos, nil, "child", pointer),
		types.NewParam(noPos, nil, "typeOut", pointer),
		types.NewParam(noPos, nil, "dataOut", pointer),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}

func coroRecoverAliasBeginSignature() *types.Signature {
	const noPos = 0
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(noPos, nil, "g", pointer),
		types.NewParam(noPos, nil, "expected", pointer),
		types.NewParam(noPos, nil, "token", pointer),
		types.NewParam(noPos, nil, "active", types.Typ[types.Bool]),
	)
	results := types.NewTuple(types.NewParam(noPos, nil, "previous", pointer))
	return types.NewSignatureType(nil, nil, nil, params, results, false)
}

func (p *context) beginCoroRecoverAliasScope(
	b llssa.Builder, expected, token, active llssa.Expr,
) func(llssa.Builder) {
	recovery, ok := p.activeCoroRecoveryEmission()
	if !ok || b == nil || b.Func != p.fn || token.IsNil() || active.IsNil() ||
		recovery.aliasBeginHook == "" || recovery.aliasEndHook == "" {
		panic("coroutine recover-alias scope requires an exact physical ABI")
	}
	if expected.IsNil() {
		expected = p.prog.Nil(p.prog.VoidPtr())
	}
	begin := p.pkg.NewFunc(
		recovery.aliasBeginHook, coroRecoverAliasBeginSignature(), llssa.InC,
	)
	previous := b.Call(
		begin.Expr,
		recovery.task,
		b.Convert(p.prog.VoidPtr(), expected),
		b.Convert(p.prog.VoidPtr(), token),
		active,
	)
	end := p.pkg.NewFunc(
		recovery.aliasEndHook, coroRecoverAliasEndSignature(), llssa.InC,
	)
	return func(done llssa.Builder) {
		if done == nil || done.Func != p.fn {
			panic("coroutine recover-alias scope ended outside its physical body")
		}
		done.Call(end.Expr, recovery.task, previous)
	}
}

// beginCoroTransparentRecoverAliasScope transfers direct-recover permission
// from the active wrapper activation to one exact target token. The caller has
// already consumed the source instruction's frozen recoverAlias fact; this
// helper owns only the stackless runtime scope and may therefore be used once
// in each mutually exclusive dispatch branch.
func (p *context) beginCoroTransparentRecoverAliasScope(
	b llssa.Builder, token llssa.Expr,
) func(llssa.Builder) {
	recovery, ok := p.activeCoroRecoveryEmission()
	if !ok || token.IsNil() {
		panic("transparent coroutine recover alias requires an active handle and target token")
	}
	return p.beginCoroRecoverAliasScope(
		b, recovery.handle, token, p.prog.BoolVal(true),
	)
}

func (p *context) callCoroTransparentRecoverAlias(
	b llssa.Builder, token llssa.Expr, call func() llssa.Expr,
) llssa.Expr {
	if call == nil {
		panic("transparent coroutine recover alias requires a call")
	}
	end := p.beginCoroTransparentRecoverAliasScope(b, token)
	result := call()
	end(b)
	return result
}

func coroRecoverAliasEndSignature() *types.Signature {
	const noPos = 0
	pointer := types.Typ[types.UnsafePointer]
	params := types.NewTuple(
		types.NewParam(noPos, nil, "g", pointer),
		types.NewParam(noPos, nil, "previous", pointer),
	)
	return types.NewSignatureType(nil, nil, nil, params, nil, false)
}
