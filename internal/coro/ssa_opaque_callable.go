/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package coro

import (
	"fmt"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/types/typeutil"
)

// retainOpaqueCallableDomains bounds CHA's closed-world assumption. Source
// function declarations enumerate neither runtime-created reflection entries
// nor values loaded through unsafe storage or returned by an unavailable body.
// A matching literal therefore cannot close that signature's unknown values.
// Exact scalar flow and explicit closed-call certificates remain authoritative
// at their own occurrences; only signature-only CHA closure is disabled.
func (f *ssaFuncFlow) retainOpaqueCallableDomains(
	functions []*ssa.Function,
	project func(*ssa.Function, *types.Signature) (*types.Signature, error),
) error {
	if f.dynamicResolution != DynamicCHAClosed || len(f.dynamicCandidates) == 0 {
		return nil
	}
	var opaque typeutil.Map
	mark := func(owner *ssa.Function, typ types.Type) error {
		signature, callable := types.Unalias(typ).Underlying().(*types.Signature)
		if !callable {
			return nil
		}
		signature, err := projectSSACallableSignature(project, owner, signature)
		if err != nil {
			return fmt.Errorf("opaque callable source in %q: %w", owner.Name(), err)
		}
		opaque.Set(signature, true)
		return nil
	}
	body := make(map[*ssa.Function]bool, len(functions))
	for _, function := range functions {
		body[function] = true
	}
	for _, owner := range functions {
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				switch source := instruction.(type) {
				case *ssa.TypeAssert:
					if err := mark(owner, source.AssertedType); err != nil {
						return err
					}
				case *ssa.UnOp:
					if source.Op == token.MUL {
						if _, exact := exactSSAContextFreeFunctionValue(source); !exact {
							if err := mark(owner, source.Type()); err != nil {
								return err
							}
						}
					}
				case *ssa.Call:
					if _, builtin := source.Common().Value.(*ssa.Builtin); builtin {
						continue
					}
					callee := source.Common().StaticCallee()
					if callee != nil && body[callee] && len(callee.Blocks) != 0 {
						continue
					}
					results := source.Common().Signature().Results()
					for index := 0; index < results.Len(); index++ {
						if err := mark(owner, results.At(index).Type()); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	if opaque.Len() == 0 {
		return nil
	}
	f.opaqueDynamicCalls = make(map[ssa.CallInstruction]bool)
	for call := range f.dynamicCandidates {
		if call.Common().IsInvoke() {
			continue
		}
		signature, err := projectSSACallableSignature(project, call.Parent(), call.Common().Signature())
		if err != nil {
			return fmt.Errorf("opaque callable consumer in %q: %w", call.Parent().Name(), err)
		}
		if opaque.At(signature) != nil {
			f.opaqueDynamicCalls[call] = true
		}
	}
	return nil
}
