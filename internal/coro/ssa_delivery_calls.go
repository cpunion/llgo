/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package coro

import (
	"fmt"

	"golang.org/x/tools/go/ssa"
)

func validateSSADeliveryCallCondition(owner *ssa.Function, call ssa.CallInstruction) error {
	if call == nil || call.Parent() != owner || call.Block() == nil || call.Common() == nil ||
		call.Common().StaticCallee() != nil || call.Common().Signature() == nil || call.Common().Signature().Results().Len() == 0 {
		return fmt.Errorf("coro: result-delivery helper requires an exact owner-local dynamic result call")
	}
	if _, builtin := call.Common().Value.(*ssa.Builtin); builtin {
		return fmt.Errorf("coro: result-delivery helper cannot depend on a builtin")
	}
	switch call := call.(type) {
	case *ssa.Call:
	case *ssa.Defer:
		if call.DeferStack != nil {
			return fmt.Errorf("coro: result-delivery helper requires a bound external defer owner")
		}
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
