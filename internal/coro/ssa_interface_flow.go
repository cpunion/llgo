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
	"go/types"

	"golang.org/x/tools/go/ssa"
)

// refineSSAExactInterfaceCallCandidates resolves an ordinary invoke directly
// when its receiver can be traced to one exact concrete SSA value. This is an
// occurrence-local language proof, not a closed-world or type-wide method
// claim: a different interface value containing *T may still select the
// nullable *T wrapper even when this call contains an exact T.
//
// ChangeInterface preserves the concrete payload. A Phi is accepted only when
// every edge carries the same exact SSA value; synthesizing a concrete Phi or
// rebuilding a value from interface data belongs to a later optimization.
// Parameters, loads, assertions, calls, and cycles fail closed.
//
// The exact receiver's Go method set determines the target independently of
// DynamicResolution. Existing CHA candidates are used as a consistency gate
// when present. The target must already belong to the frozen effective program;
// this pass never expands the emission universe after identities are frozen.
func refineSSAExactInterfaceCallCandidates(
	prog *ssa.Program,
	functions []*ssa.Function,
	included map[*ssa.Function]bool,
	candidates map[ssa.CallInstruction]map[*ssa.Function]struct{},
	canonicalizer *ssaFunctionCanonicalizer,
) (map[*ssa.Call]ssa.Value, error) {
	exact := make(map[*ssa.Call]ssa.Value)
	if prog == nil || canonicalizer == nil {
		return exact, nil
	}

	for _, fn := range functions {
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				call, ordinary := instruction.(*ssa.Call)
				if !ordinary || call == nil || call.Common() == nil ||
					!call.Common().IsInvoke() ||
					call.Common().StaticCallee() != nil ||
					call.Common().Method == nil {
					continue
				}
				source, ok := exactSSAInterfaceConcreteSource(call.Common().Value)
				if !ok || source == nil || source.Type() == nil {
					continue
				}
				if _, stillInterface := types.Unalias(source.Type()).Underlying().(*types.Interface); stillInterface {
					continue
				}
				selection := prog.MethodSets.MethodSet(source.Type()).Lookup(
					call.Common().Method.Pkg(), call.Common().Method.Name(),
				)
				if selection == nil || selection.Obj() == nil ||
					selection.Obj().Id() != call.Common().Method.Id() {
					continue
				}
				target := prog.MethodValue(selection)
				if target == nil || target.Signature == nil ||
					target.Signature.Recv() == nil ||
					!types.Identical(target.Signature.Recv().Type(), source.Type()) {
					continue
				}
				canonical, resolved, err := canonicalizer.resolve(target)
				if err != nil {
					return nil, fmt.Errorf(
						"coro: resolve exact interface target %q in %q: %w",
						target.Name(), fn.Name(), err,
					)
				}
				if !resolved || canonical == nil || !included[canonical] ||
					canonical.Signature == nil ||
					canonical.Signature.Recv() == nil ||
					!types.Identical(canonical.Signature.Recv().Type(), source.Type()) {
					continue
				}

				// A custom DynamicImplements resolver may intentionally expose
				// a patched candidate set. When such a set exists, require the
				// language-selected canonical target to agree with it.
				original := candidates[call]
				if len(original) != 0 {
					if _, consistent := original[canonical]; !consistent {
						continue
					}
				}
				candidates[call] = map[*ssa.Function]struct{}{canonical: {}}
				exact[call] = source
			}
		}
	}
	return exact, nil
}

type ssaExactInterfaceSourceState uint8

const (
	ssaExactInterfaceSourceVisiting ssaExactInterfaceSourceState = iota + 1
	ssaExactInterfaceSourceFailed
	ssaExactInterfaceSourceComplete
)

type ssaExactInterfaceSourceMemo struct {
	state  ssaExactInterfaceSourceState
	source ssa.Value
}

func exactSSAInterfaceConcreteSource(value ssa.Value) (ssa.Value, bool) {
	memo := make(map[ssa.Value]ssaExactInterfaceSourceMemo)
	return exactSSAInterfaceConcreteSourceMemo(value, memo)
}

func exactSSAInterfaceConcreteSourceMemo(
	value ssa.Value,
	memo map[ssa.Value]ssaExactInterfaceSourceMemo,
) (ssa.Value, bool) {
	if value == nil {
		return nil, false
	}
	if cached, found := memo[value]; found {
		return cached.source, cached.state == ssaExactInterfaceSourceComplete
	}
	memo[value] = ssaExactInterfaceSourceMemo{state: ssaExactInterfaceSourceVisiting}

	var source ssa.Value
	complete := false
	switch value := value.(type) {
	case *ssa.MakeInterface:
		source, complete = value.X, value.X != nil && value.X.Type() != nil
	case *ssa.ChangeInterface:
		source, complete = exactSSAInterfaceConcreteSourceMemo(value.X, memo)
	case *ssa.Phi:
		if len(value.Edges) != 0 {
			for index, edge := range value.Edges {
				candidate, ok := exactSSAInterfaceConcreteSourceMemo(edge, memo)
				if !ok {
					complete = false
					break
				}
				if index == 0 {
					source, complete = candidate, true
					continue
				}
				if candidate != source {
					complete = false
					break
				}
			}
		}
	}
	if !complete || source == nil {
		memo[value] = ssaExactInterfaceSourceMemo{state: ssaExactInterfaceSourceFailed}
		return nil, false
	}
	memo[value] = ssaExactInterfaceSourceMemo{
		state:  ssaExactInterfaceSourceComplete,
		source: source,
	}
	return source, true
}
