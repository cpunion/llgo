//go:build !llgo

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package ssa

import (
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/xgo-dev/llvm"
)

func TestInterfaceEqualityDynamicNilGuard(t *testing.T) {
	for _, operand := range []struct {
		name string
		typ  types.Type
	}{
		{"empty", types.NewInterfaceType(nil, nil).Complete()},
		{"error", types.Universe.Lookup("error").Type()},
	} {
		for _, op := range []token.Token{token.EQL, token.NEQ} {
			t.Run(operand.name+op.String(), func(t *testing.T) {
				prog := NewProgram(nil)
				defer prog.Dispose()
				prog.SetRuntime(RuntimeTypesForTest(t))
				pkg := prog.NewPackage("compare", "compare")
				module := pkg.Module()
				defer module.Dispose()
				signature := types.NewSignatureType(nil, nil, nil, types.NewTuple(types.NewParam(0, nil, "a", operand.typ), types.NewParam(0, nil, "b", operand.typ)), types.NewTuple(types.NewParam(0, nil, "", types.Typ[types.Bool])), false)
				fn := pkg.NewFunc("compare", signature, InGo)
				b := fn.MakeBody(1)
				b.Return(b.BinOp(op, fn.Param(0), fn.Param(1)))
				b.EndBuild()
				b.Dispose()
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatalf("verify: %v\n%s", err, module.String())
				}
				body := module.NamedFunction("compare")
				entry := body.FirstBasicBlock()
				for instruction := entry.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
					if !instruction.IsACallInst().IsNil() {
						t.Fatalf("nil guard performs an unconditional helper call:\n%s", body.String())
					}
				}
				ir := body.String()
				if !strings.Contains(ir, "br i1") || !strings.Contains(ir, "phi i1") || !strings.Contains(ir, ".EfaceEqual") || strings.Contains(ir, "alloca") {
					t.Fatalf("comparison lost guarded fallback or introduced stack scratch:\n%s", ir)
				}
			})
		}
	}
}
