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
	"golang.org/x/tools/go/types/typeutil"
)

// restrictedSSACHACandidates performs the candidate-discovery portion of CHA
// over exactly functions. Unlike cha.CallGraph, it never asks the SSA Program
// to enumerate or materialize functions outside that frozen set.
func restrictedSSACHACandidates(functions []*ssa.Function) map[ssa.CallInstruction]map[*ssa.Function]struct{} {
	return restrictedSSACHACandidatesWithImplements(functions, types.Implements)
}

func restrictedSSACHACandidatesWithImplements(
	functions []*ssa.Function,
	implements func(types.Type, *types.Interface) bool,
) map[ssa.CallInstruction]map[*ssa.Function]struct{} {
	result, err := restrictedSSACHACandidatesWithDynamicImplements(
		functions,
		func(candidate types.Type, iface *types.Interface) (bool, error) {
			return implements(candidate, iface), nil
		},
	)
	if err != nil {
		// The adapter above cannot return an error. Keep the bool-only helper for
		// tests and legacy internal callers without weakening the production
		// fail-closed path below.
		panic(err)
	}
	return result
}

func restrictedSSACHACandidatesWithDynamicImplements(
	functions []*ssa.Function,
	implements func(types.Type, *types.Interface) (bool, error),
) (map[ssa.CallInstruction]map[*ssa.Function]struct{}, error) {
	if implements == nil {
		return nil, fmt.Errorf("coro: restricted CHA has nil dynamic implements resolver")
	}
	var funcsBySignature typeutil.Map
	methodsByID := make(map[string][]*ssa.Function)
	for _, fn := range functions {
		if fn == nil || fn.Signature == nil {
			continue
		}
		if fn.Signature.Recv() == nil {
			if fn.Name() == "init" && fn.Synthetic == "package initializer" {
				continue
			}
			matches, _ := funcsBySignature.At(fn.Signature).([]*ssa.Function)
			funcsBySignature.Set(fn.Signature, append(matches, fn))
			continue
		}
		method, ok := fn.Object().(*types.Func)
		if !ok {
			continue
		}
		methodsByID[method.Id()] = append(methodsByID[method.Id()], fn)
	}
	type interfaceMethod struct {
		iface *types.Interface
		id    string
	}
	methodsMemo := make(map[interfaceMethod][]*ssa.Function)
	lookupMethods := func(iface *types.Interface, method *types.Func) ([]*ssa.Function, error) {
		key := interfaceMethod{iface: iface, id: method.Id()}
		if candidates, ok := methodsMemo[key]; ok {
			return candidates, nil
		}
		var candidates []*ssa.Function
		for _, candidate := range methodsByID[key.id] {
			receiver := candidate.Signature.Recv().Type()
			matches, err := implements(receiver, iface)
			if err != nil {
				return nil, fmt.Errorf(
					"coro: restricted CHA match candidate %q receiver %q to interface %q method %q: %w",
					candidate.String(), restrictedCHATypeString(receiver), restrictedCHATypeString(iface), key.id, err,
				)
			}
			if matches {
				candidates = append(candidates, candidate)
			}
		}
		methodsMemo[key] = candidates
		return candidates, nil
	}

	result := make(map[ssa.CallInstruction]map[*ssa.Function]struct{})
	for _, caller := range functions {
		if caller == nil {
			continue
		}
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || call.Common().StaticCallee() != nil {
					continue
				}
				common := call.Common()
				var candidates []*ssa.Function
				if common.IsInvoke() {
					iface, ok := common.Value.Type().Underlying().(*types.Interface)
					if !ok || common.Method == nil {
						continue
					}
					var err error
					candidates, err = lookupMethods(iface, common.Method)
					if err != nil {
						return nil, err
					}
				} else {
					if _, builtin := common.Value.(*ssa.Builtin); builtin {
						continue
					}
					candidates, _ = funcsBySignature.At(common.Signature()).([]*ssa.Function)
				}
				if len(candidates) == 0 {
					continue
				}
				set := make(map[*ssa.Function]struct{}, len(candidates))
				for _, candidate := range candidates {
					set[candidate] = struct{}{}
				}
				result[call] = set
			}
		}
	}
	return result, nil
}

func restrictedCHATypeString(typ types.Type) string {
	return types.TypeString(typ, func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return pkg.Path()
	})
}
