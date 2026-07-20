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
	addressTaken := restrictedSSAAddressTakenFunctions(functions)
	for _, fn := range functions {
		if fn == nil || fn.Signature == nil {
			continue
		}
		if fn.Signature.Recv() == nil {
			if fn.Name() == "init" && fn.Synthetic == "package initializer" {
				continue
			}
			// A scalar dynamic call can receive only a function that is actually
			// materialized as a first-class value in the frozen program. Indexing
			// every same-signature top-level function makes unrelated entry points
			// such as main.main descriptor-backed merely because some func() value
			// is open elsewhere. An external value with no frozen source remains
			// open; it does not authorize invented in-program targets.
			if !addressTaken[fn] {
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

func restrictedSSAAddressTakenFunctions(functions []*ssa.Function) map[*ssa.Function]bool {
	return restrictedSSAAddressTakenFunctionsExcluding(functions, nil)
}

// restrictedSSAAddressTakenFunctionsExcluding is the managed scalar-function
// publication inventory used by restricted CHA. An exact static-code-address
// operand is not a Go function value: the frontend proved that its transient
// MakeInterface has one structural address consumer and is never materialized.
// Exclusion is occurrence-local; any other publication of the same function
// still places it in the managed candidate set.
func restrictedSSAAddressTakenFunctionsExcluding(
	functions []*ssa.Function,
	codeAddressUses []ssaCallArgumentUse,
) map[*ssa.Function]bool {
	result := make(map[*ssa.Function]bool)
	codeAddressBoxes := make(map[*ssa.MakeInterface]struct{}, len(codeAddressUses))
	for _, use := range codeAddressUses {
		if use.call == nil || use.call.Common() == nil || use.argument < 0 || use.argument >= len(use.call.Common().Args) {
			continue
		}
		if boxed, ok := use.call.Common().Args[use.argument].(*ssa.MakeInterface); ok {
			codeAddressBoxes[boxed] = struct{}{}
		}
	}
	operands := make([]*ssa.Value, 0, 8)
	for _, owner := range functions {
		if owner == nil {
			continue
		}
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				if _, debug := instruction.(*ssa.DebugRef); debug {
					continue
				}
				operands = instruction.Operands(operands[:0])
				for _, operand := range operands {
					if operand == nil {
						continue
					}
					target, ok := (*operand).(*ssa.Function)
					if !ok || target == nil {
						continue
					}
					if call, ok := instruction.(ssa.CallInstruction); ok && operand == &call.Common().Value && call.Common().StaticCallee() == target {
						continue
					}
					if boxed, ok := instruction.(*ssa.MakeInterface); ok && operand == &boxed.X {
						if _, codeAddress := codeAddressBoxes[boxed]; codeAddress {
							continue
						}
					}
					result[target] = true
				}
			}
		}
	}
	return result
}

func restrictedCHATypeString(typ types.Type) string {
	return types.TypeString(typ, func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return pkg.Path()
	})
}
