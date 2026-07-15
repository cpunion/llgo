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

package coro

import (
	"fmt"
	"sort"

	"golang.org/x/tools/go/ssa"
)

// SSAEmissionUniverse is an immutable set of exact SSA function objects that
// one frontend compilation may emit or reference while lowering functions. It
// belongs to exactly one SSA Program: functions from another program never
// match, even when they have the same source-level identity.
//
// Functions returns a defensively copied snapshot in stable frontend order.
// Callers should create the universe only after all frontend-specific lazy
// functions have been materialized and then share it for analysis and
// lowering. AnalyzeSSA applies its configured structural FunctionID resolver
// before producing an externally stable plan order.
type SSAEmissionUniverse struct {
	prog      *ssa.Program
	functions []*ssa.Function
	set       map[*ssa.Function]struct{}
}

// NewSSAEmissionUniverse validates and freezes functions as the exact emission
// universe of prog. Duplicate pointers are ignored. The input slice is not
// retained.
func NewSSAEmissionUniverse(prog *ssa.Program, functions []*ssa.Function) (*SSAEmissionUniverse, error) {
	if prog == nil {
		return nil, fmt.Errorf("coro: create SSA emission universe for nil program")
	}
	set := make(map[*ssa.Function]struct{}, len(functions))
	ordered := make([]*ssa.Function, 0, len(functions))
	for i, fn := range functions {
		if fn == nil {
			return nil, fmt.Errorf("coro: SSA emission universe function %d is nil", i)
		}
		if fn.Prog != prog {
			return nil, fmt.Errorf("coro: SSA emission universe function %q belongs to another program", fn.Name())
		}
		if _, exists := set[fn]; exists {
			continue
		}
		set[fn] = struct{}{}
		ordered = append(ordered, fn)
	}
	// Construction freezes membership only. In particular, it must not apply a
	// default FunctionIDConfig: frontends may add valid synthetic functions or
	// substituted local generic types that require their own structural
	// provenance callbacks. Equal raw keys are legal here; AnalyzeSSA later
	// detects real FunctionID collisions with the caller's complete identity
	// config.
	sort.SliceStable(ordered, func(i, j int) bool {
		return rawSSAFunctionKey(ordered[i]) < rawSSAFunctionKey(ordered[j])
	})
	return &SSAEmissionUniverse{
		prog:      prog,
		functions: ordered,
		set:       set,
	}, nil
}

// Program returns the SSA Program that owns every function in the universe.
func (u *SSAEmissionUniverse) Program() *ssa.Program {
	if u == nil {
		return nil
	}
	return u.prog
}

// Functions returns the exact functions in stable frontend order.
func (u *SSAEmissionUniverse) Functions() []*ssa.Function {
	if u == nil {
		return nil
	}
	return append([]*ssa.Function(nil), u.functions...)
}

// Contains reports whether fn is one of the exact SSA function pointers in the
// universe. A logically identical function from another SSA Program does not
// match.
func (u *SSAEmissionUniverse) Contains(fn *ssa.Function) bool {
	if u == nil || fn == nil || fn.Prog != u.prog {
		return false
	}
	_, ok := u.set[fn]
	return ok
}
