//go:build !llgo

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package cl

import (
	"go/token"
	"go/types"
	"testing"

	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

// Compiler-owned guards cannot require a syntactic source defer just to get
// an SSA Recover block. Exercise the real shared completion dispatcher, not a
// separate delivery state machine or a fabricated source recovery target.
func TestCoroCleanupCompletionWithoutSourceRecover(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, "package foo\nfunc Root() {}\n")
	owner := ssaPkg.Func("Root")
	if owner.Recover != nil {
		t.Fatal("fixture unexpectedly acquired a source Recover block")
	}
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	pkg := prog.NewPackage("cleanup", "cleanup")
	defer pkg.Module().Dispose()
	parameters := types.NewTuple(
		types.NewParam(token.NoPos, nil, "panicking", types.Typ[types.Bool]),
		types.NewParam(token.NoPos, nil, "continuation", types.Typ[types.Uint32]),
	)
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "", types.Typ[types.Uint32]))
	fn := pkg.NewFunc("cleanup", types.NewSignatureType(nil, nil, nil, parameters, results, false), llssa.InGo)
	b := fn.MakeBody(1)
	defer b.Dispose()
	ctx := &context{prog: prog, pkg: pkg, fn: fn, goFn: owner}
	state := &coroStaticCleanupState{
		panicActive: b.AllocaT(prog.Bool()), continuation: b.AllocaT(prog.Uint32()),
		panic: fn.MakeBlock(), complete: fn.MakeBlock(),
	}
	b.Store(state.panicActive, fn.Param(0))
	b.Store(state.continuation, fn.Param(1))
	state.emitCompletionDispatch(ctx, b)
	b.SetBlock(state.panic)
	b.Return(prog.IntVal(11, prog.Uint32()))
	b.SetBlock(state.complete)
	b.Return(prog.IntVal(22, prog.Uint32()))
	b.EndBuild()
	module := pkg.Module()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify implicit cleanup completion: %v\n%s", err, module.String())
	}
	switches, traps := 0, 0
	for _, block := range module.NamedFunction("cleanup").BasicBlocks() {
		end := block.LastInstruction()
		switch end.InstructionOpcode() {
		case llvm.Switch:
			switches++
			if end.SuccessorsCount() != 2 || end.GetSwitchCaseValue(1).ZExtValue() != uint64(coroStaticCleanupContinueComplete) {
				t.Fatalf("implicit cleanup switch must have only complete=%d and the invalid default:\n%s", coroStaticCleanupContinueComplete, end.String())
			}
		case llvm.Unreachable:
			traps++
		}
	}
	if switches != 1 || traps != 1 {
		t.Fatalf("completion switches=%d traps=%d, want one exact complete case and one invalid-continuation trap", switches, traps)
	}
}
