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
		// Opaque producers can expose pointer-backed callable storage too.
		seen := make(map[types.Type]bool)
		for !seen[typ] {
			seen[typ] = true
			pointer, indirect := types.Unalias(typ).Underlying().(*types.Pointer)
			if !indirect {
				break
			}
			typ = pointer.Elem()
		}
		// Reuse the representation schema: an opaque struct/container can
		// carry a callable just as an opaque scalar can. Only looking at the
		// outer type would incorrectly close a later Field/Index/receive.
		for _, path := range f.pathsForType(typ) {
			leaf, found := funcLeafTypeAtPath(typ, path)
			if !found {
				return fmt.Errorf("opaque callable source in %q has an invalid function leaf", owner.Name())
			}
			signature := types.Unalias(leaf).Underlying().(*types.Signature)
			signature, err := projectSSACallableSignature(project, owner, signature)
			if err != nil {
				return fmt.Errorf("opaque callable source in %q: %w", owner.Name(), err)
			}
			opaque.Set(signature, true)
		}
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
					if source.Op == token.MUL && opaqueSSACallableStorage(source.X, make(map[ssa.Value]bool)) {
						if err := mark(owner, source.Type()); err != nil {
							return err
						}
					}
				case *ssa.Convert:
					if basic, raw := types.Unalias(source.X.Type()).Underlying().(*types.Basic); raw && basic.Kind() == types.UnsafePointer {
						if err := mark(owner, source.Type()); err != nil {
							return err
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

// Ordinary typed Go field loads do not manufacture entries outside the source
// domain. Preserve their CHA candidates (notably syscall's immutable mmapper
// callbacks); only an unsafe address or an opaque returned address adds a new
// domain. This is a source proof, never reverse lookup from a native PC.
func opaqueSSACallableStorage(value ssa.Value, seen map[ssa.Value]bool) bool {
	if value == nil || seen[value] {
		return false
	}
	seen[value] = true
	if basic, raw := types.Unalias(value.Type()).Underlying().(*types.Basic); raw && basic.Kind() == types.UnsafePointer {
		return true
	}
	switch value := value.(type) {
	case *ssa.Convert:
		return opaqueSSACallableStorage(value.X, seen)
	case *ssa.ChangeType:
		return opaqueSSACallableStorage(value.X, seen)
	case *ssa.FieldAddr:
		return opaqueSSACallableStorage(value.X, seen)
	case *ssa.IndexAddr:
		return opaqueSSACallableStorage(value.X, seen)
	case *ssa.UnOp:
		return value.Op == token.MUL && opaqueSSACallableStorage(value.X, seen)
	case *ssa.Phi:
		for _, edge := range value.Edges {
			if opaqueSSACallableStorage(edge, seen) {
				return true
			}
		}
	case *ssa.Call:
		return true
	}
	return false
}
