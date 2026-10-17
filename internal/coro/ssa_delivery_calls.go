/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package coro

import (
	"fmt"
	"go/ast"

	"golang.org/x/tools/go/ssa"
)

// SSADeferStackOwner follows only compiler-created range yield children. An
// ordinary function literal owns its own defer stack, even inside a range.
// Keep this projection shared by effect analysis and physical cleanup lowering.
func SSADeferStackOwner(fn *ssa.Function) *ssa.Function {
	for fn != nil && fn.Synthetic == "range-over-func yield" {
		parent := fn.Parent()
		statement, rangeSyntax := fn.Syntax().(*ast.RangeStmt)
		if parent == nil || !rangeSyntax || statement.X == nil || statement.Body == nil ||
			fn.Prog != parent.Prog || fn.Pkg != parent.Pkg {
			return nil
		}
		children := 0
		for _, child := range parent.AnonFuncs {
			if child == fn {
				children++
			}
		}
		if children != 1 {
			return nil
		}
		fn = parent
	}
	return fn
}

// SSAResultDeliveryOwner is the function that executes a call and owns its
// temporary result. External range defers register in a yield but execute on
// the source owner's existing cleanup drainer. A fabricated external stack on
// an ordinary function is not an ownership certificate.
func SSAResultDeliveryOwner(call ssa.CallInstruction) *ssa.Function {
	if call == nil || call.Parent() == nil {
		return nil
	}
	if deferred, ok := call.(*ssa.Defer); ok && deferred.DeferStack != nil {
		owner := SSADeferStackOwner(call.Parent())
		if owner == call.Parent() {
			return nil
		}
		return owner
	}
	return call.Parent()
}

func validateSSADeliveryCallCondition(owner *ssa.Function, call ssa.CallInstruction) error {
	if owner == nil || call == nil || SSAResultDeliveryOwner(call) != owner || call.Block() == nil || call.Common() == nil ||
		call.Common().StaticCallee() != nil || call.Common().Signature() == nil || call.Common().Signature().Results().Len() == 0 {
		return fmt.Errorf("coro: result-delivery helper requires an exact owner-local dynamic result call")
	}
	if _, builtin := call.Common().Value.(*ssa.Builtin); builtin {
		return fmt.Errorf("coro: result-delivery helper cannot depend on a builtin")
	}
	switch call.(type) {
	case *ssa.Call:
	case *ssa.Defer:
	default:
		return fmt.Errorf("coro: result-delivery helper requires an ordinary or deferred invocation, not spawn")
	}
	for _, instruction := range call.Block().Instrs {
		if instruction == call {
			return nil
		}
	}
	return fmt.Errorf("coro: result-delivery helper condition is absent from its source block")
}

// ssaCallNeedsResultDelivery consumes the existing exact scalar/CHA proofs.
// It is resolved before local cleanup and helper edges enter the sole effect
// fixed point; physical lowering never guesses ownership from an int/pointer.
func ssaCallNeedsResultDelivery(call ssa.CallInstruction, flow *ssaFuncFlow,
	unknown map[ssa.CallInstruction]UnknownTarget, closed map[ssa.CallInstruction]SSAClosedDynamicCallCertificate,
	producers map[*ssa.Function]bool) bool {
	if certificate, present := closed[call]; present {
		for _, target := range certificate.Targets {
			if producers[target] {
				return true
			}
		}
		return false
	}
	if targets, complete := flow.scalarCallTargets(call); complete {
		for target := range targets {
			if producers[target] {
				return true
			}
		}
		return false
	}
	if flow.dynamicCallClosed(call) {
		for target := range flow.dynamicCandidates[call] {
			if producers[target] {
				return true
			}
		}
		return false
	}
	return unknown[call].managedDispatch()
}
