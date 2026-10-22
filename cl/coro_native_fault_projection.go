// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package cl

import (
	"fmt"

	"golang.org/x/tools/go/ssa"
)

// The existing capability fixed point carries this second projection on the
// same frozen edges. Program-wide service demand remains fully transitive;
// one native activation needs no duplicate landing for an independently
// protected physical coroutine child. Plain/outcome/raw/unknown calls keep
// propagating demand. Dynamic panic-on-fault still enables every boundary.
type coroNativeFaultProjection struct {
	required      map[*ssa.Function]bool
	isolatedCalls map[ssa.CallInstruction]bool
}

func (ir *coroProgramIR) nativeFaultIsolatedCalls() map[ssa.CallInstruction]bool {
	isolated := make(map[ssa.CallInstruction]bool)
	seen := make(map[ssa.CallInstruction]bool)
	defined := make(map[*ssa.Function]bool)
	for _, function := range ir.physicalPlans {
		defined[function.function] = function.function != nil && len(function.function.Blocks) != 0
	}
	for _, function := range ir.physicalPlans {
		for instruction, physical := range function.instructions {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok {
				continue
			}
			separate := physical.control == coroPhysicalControlDirectAwait && defined[physical.controlTarget]
			// All ownership variants must agree before an occurrence is isolated.
			if seen[call] {
				isolated[call] = isolated[call] && separate
			} else {
				isolated[call] = separate
				seen[call] = true
			}
		}
	}
	return isolated
}

func (c *Compilation) coroFunctionNativeFaultBoundary(function *ssa.Function) (bool, error) {
	if function == nil {
		return true, fmt.Errorf("native fault projection requires an exact function")
	}
	if _, err := c.coroFunctionProgramCapabilities(); err != nil {
		return true, err
	}
	required, ok := c.coroNativeFaultByFunc[function]
	if !ok {
		return true, fmt.Errorf("function %q is absent from native fault projection", function.Name())
	}
	return required, nil
}
