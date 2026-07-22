//go:build !llgo

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

package cl

import (
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestCoroLenBuiltinGenericMapRequiresExactMapLenHelper(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func MakeLen[K comparable, V any](values map[K]V) func() int {
	return func() int { return len(values) }
}
func Root(values map[int]string) int { return MakeLen(values)() }
`)
	origin := ssaPkg.Func("MakeLen")
	if origin == nil || len(origin.AnonFuncs) != 1 {
		t.Fatalf("generic MakeLen anonymous functions = %d, want one", len(origin.AnonFuncs))
	}
	closure := origin.AnonFuncs[0]
	call := coroLenBuiltinCall(t, closure)
	operand := call.Common().Args[0].Type()
	if _, ok := types.Unalias(operand).Underlying().(*types.Map); !ok {
		t.Fatalf("generic len operand = %T %v, want map[K]V", types.Unalias(operand).Underlying(), operand)
	}
	if got := coroPhysicalLenKind(operand); got != coroPhysicalLenMap {
		t.Fatalf("generic map len kind = %d, want exact MapLen lowering", got)
	}
	audit, err := newCoroPhysicalPureSSAAudit(nil, nil, closure, "")
	if err != nil {
		t.Fatal(err)
	}
	if reason := audit.validateLenBuiltin(call); reason != "runtime helper capability validation requires a frozen emission universe" {
		t.Fatalf("generic map len validation = %q, want exact MapLen helper-plan gate", reason)
	}
}

func TestCoroLenBuiltinDoesNotInferLoweringFromUnknownTypeParameter(t *testing.T) {
	constraint := types.NewInterfaceType(nil, nil).Complete()
	parameter := types.NewTypeParam(types.NewTypeName(token.NoPos, nil, "T", nil), constraint)
	if got := coroPhysicalLenKind(parameter); got != coroPhysicalLenUnsupported {
		t.Fatalf("bare type parameter len kind = %d, want fail-closed", got)
	}
	if got := coroPhysicalLenKind(types.NewMap(parameter, types.Typ[types.Int])); got != coroPhysicalLenMap {
		t.Fatalf("map[T]int len kind = %d, want exact map-header lowering", got)
	}
	if got := coroPhysicalLenKind(types.NewSlice(parameter)); got != coroPhysicalLenInline {
		t.Fatalf("[]T len kind = %d, want exact inline slice-header lowering", got)
	}
	if got := coroPhysicalLenKind(constraint); got != coroPhysicalLenUnsupported {
		t.Fatalf("interface len kind = %d, want fail-closed", got)
	}
}

func TestCoroLenAndCapBuiltinChannelDirectionsRequireExactHelpers(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func Ops[T any](recv <-chan T, send chan<- T, both chan T, values []T) int {
	return len(recv) + len(send) + len(both) + cap(recv) + cap(send) + cap(both) + cap(values)
}
`)
	function := ssaPkg.Func("Ops")
	if function == nil {
		t.Fatal("missing generic Ops function")
	}
	audit, err := newCoroPhysicalPureSSAAudit(nil, nil, function, "")
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil {
				continue
			}
			builtin, ok := call.Common().Value.(*ssa.Builtin)
			if !ok || (builtin.Name() != "len" && builtin.Name() != "cap") {
				continue
			}
			counts[builtin.Name()]++
			_, channel := types.Unalias(call.Common().Args[0].Type()).Underlying().(*types.Chan)
			var reason string
			if builtin.Name() == "len" {
				reason = audit.validateLenBuiltin(call)
			} else {
				reason = audit.validateCapBuiltin(call)
			}
			if channel {
				if !strings.Contains(reason, "runtime helper capability validation requires a frozen emission universe") {
					t.Errorf("%s(%s) validation = %q, want exact channel-helper gate", builtin.Name(), call.Common().Args[0].Type(), reason)
				}
			} else if builtin.Name() != "cap" || reason != "" {
				t.Errorf("non-channel builtin %s(%s) validation = %q, want inline cap(slice)", builtin.Name(), call.Common().Args[0].Type(), reason)
			}
		}
	}
	if counts["len"] != 3 || counts["cap"] != 4 {
		t.Fatalf("builtin counts = %+v, want three channel len and three channel plus one slice cap", counts)
	}

	parameter := types.NewTypeParam(
		types.NewTypeName(token.NoPos, nil, "T", nil),
		types.NewInterfaceType(nil, nil).Complete(),
	)
	for _, direction := range []types.ChanDir{types.SendRecv, types.RecvOnly, types.SendOnly} {
		channel := types.NewChan(direction, parameter)
		if got := coroPhysicalLenKind(channel); got != coroPhysicalLenChan {
			t.Errorf("%s len kind = %d, want exact ChanLen lowering", channel, got)
		}
		if got := coroPhysicalCapKind(channel); got != coroPhysicalCapChan {
			t.Errorf("%s cap kind = %d, want exact ChanCap lowering", channel, got)
		}
	}
	if got := coroPhysicalCapKind(types.NewSlice(parameter)); got != coroPhysicalCapInline {
		t.Fatalf("[]T cap kind = %d, want exact inline slice-header lowering", got)
	}
	if got := coroPhysicalCapKind(parameter); got != coroPhysicalCapUnsupported {
		t.Fatalf("bare type parameter cap kind = %d, want fail-closed", got)
	}
	if got := coroPhysicalCapKind(parameter.Constraint()); got != coroPhysicalCapUnsupported {
		t.Fatalf("interface cap kind = %d, want fail-closed", got)
	}
}

func coroLenBuiltinCall(t *testing.T, fn *ssa.Function) *ssa.Call {
	t.Helper()
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil {
				continue
			}
			builtin, ok := call.Common().Value.(*ssa.Builtin)
			if ok && builtin.Name() == "len" {
				return call
			}
		}
	}
	t.Fatalf("%s has no len builtin", fn.Name())
	return nil
}
