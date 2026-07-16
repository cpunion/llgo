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
	"go/token"
	"go/types"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func coroSpawnBeginSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	return types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "parent", pointer)),
		types.NewTuple(types.NewParam(token.NoPos, nil, "child", pointer)),
		false,
	)
}

func coroSpawnCommitSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	return types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "parent", pointer),
			types.NewParam(token.NoPos, nil, "child", pointer),
			types.NewParam(token.NoPos, nil, "handle", pointer),
		), nil, false,
	)
}

// tryCompileCoroClosedStaticSpawn creates exactly one child root to its LLVM
// initial suspend and commits it to the scheduler. Arguments are fully
// materialized before begin mutates scheduler state. The parent then reaches
// an explicit safepoint using its physical G; there is no TLS/current-G
// fallback anywhere in this path.
func (p *context) tryCompileCoroClosedStaticSpawn(b llssa.Builder, spawn *ssa.Go) bool {
	if p.compilation == nil || !p.compilation.EnableCoroClosedStaticSpawn || spawn == nil {
		return false
	}
	if p.currentCoro == nil || p.compilation.CoroPlan == nil || b.Func != p.fn {
		panic("closed static spawn requires an active planned physical coroutine body")
	}
	target, targetPlan, err := p.compilation.CoroPlan.ResolveClosedStaticSpawn(spawn)
	if err != nil {
		caller, _ := p.compilation.CoroPlan.FunctionPlan(p.goFn)
		panic(fmt.Sprintf("closed static spawn: function %q: %v", caller.ID, err))
	}

	p.recordCallerLocationForCall(b, &spawn.Call)
	p.emitPCLineLabel(b, spawn.Pos())
	// Go SSA already sequences argument-producing instructions. Re-materialize
	// every exact operand here, in source order, before the begin transaction.
	args := p.compileValues(b, spawn.Call.Args, fnNormal)

	parent := p.currentCoro.task
	begin := p.pkg.NewFunc(coroSpawnBeginHookV1, coroSpawnBeginSignature(), llssa.InC)
	childG := b.Call(begin.Expr, parent)
	null := p.prog.Nil(p.prog.VoidPtr())
	physicalArgs := make([]llssa.Expr, 0, len(args)+2)
	physicalArgs = append(physicalArgs, childG, null)
	physicalArgs = append(physicalArgs, args...)

	root, _, kind := p.compileFunction(target)
	if kind != goFunc {
		panic(fmt.Sprintf("closed static spawn: target %q did not resolve to a Go coroutine entry", targetPlan.ID))
	}
	if root == nil {
		panic(fmt.Sprintf("closed static spawn: target %q has no physical root", targetPlan.ID))
	}
	handle := b.Call(root.Expr, physicalArgs...)
	commit := p.pkg.NewFunc(coroSpawnCommitHookV1, coroSpawnCommitSignature(), llssa.InC)
	b.Call(commit.Expr, parent, childG, handle)
	p.currentCoro.pollAndSuspendForPreempt(b)
	return true
}
