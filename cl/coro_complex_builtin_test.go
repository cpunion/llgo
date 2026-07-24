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
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

const coroComplexBuiltinFixture = `package foo

type C64 complex64
type C128 complex128

func Real64(value complex64) float32 { return real(value) }
func Imag64(value C64) float32 { return imag(value) }
func Real128(value C128) float64 { return real(value) }
func Imag128(value complex128) float64 { return imag(value) }
func Complex64(real, imag float32) complex64 { return complex(real, imag) }
func Complex128(real, imag float64) C128 { return C128(complex(real, imag)) }
`

func TestCoroComplexComponentBuiltins(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, coroComplexBuiltinFixture)
	for _, test := range []struct {
		function string
		builtin  string
	}{
		{function: "Real64", builtin: "real"},
		{function: "Imag64", builtin: "imag"},
		{function: "Real128", builtin: "real"},
		{function: "Imag128", builtin: "imag"},
	} {
		t.Run(test.function, func(t *testing.T) {
			fn := ssaPkg.Func(test.function)
			call := coroComplexBuiltinCall(t, fn, test.builtin)
			audit := &coroPhysicalPureSSAAudit{
				fn:              fn,
				reachableBlocks: coroPhysicalConstantReachableBlocks(fn),
			}
			if reason := audit.validateBuiltin(call); reason != "" {
				t.Fatalf("%s rejected: %s", test.builtin, reason)
			}
		})
	}
}

func TestCoroComplexConstructionBuiltins(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, coroComplexBuiltinFixture)
	for _, name := range []string{"Complex64", "Complex128"} {
		t.Run(name, func(t *testing.T) {
			fn := ssaPkg.Func(name)
			call := coroComplexBuiltinCall(t, fn, "complex")
			audit := &coroPhysicalPureSSAAudit{
				fn:              fn,
				reachableBlocks: coroPhysicalConstantReachableBlocks(fn),
			}
			if reason := audit.validateBuiltin(call); reason != "" {
				t.Fatalf("complex rejected: %s", reason)
			}
		})
	}
}

func TestCoroComplexComponentBuiltinFailsClosed(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, coroComplexBuiltinFixture)
	fn := ssaPkg.Func("Real64")
	call := coroComplexBuiltinCall(t, fn, "real")
	audit := &coroPhysicalPureSSAAudit{
		fn:              fn,
		reachableBlocks: coroPhysicalConstantReachableBlocks(fn),
	}
	args := call.Call.Args
	call.Call.Args = nil
	defer func() { call.Call.Args = args }()
	if reason := audit.validateBuiltin(call); !strings.Contains(reason, "requires one complex argument") {
		t.Fatalf("malformed real rejection = %q", reason)
	}
}

func coroComplexBuiltinCall(t *testing.T, fn *ssa.Function, name string) *ssa.Call {
	t.Helper()
	var found *ssa.Call
	for _, block := range fn.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok {
				continue
			}
			builtin, ok := call.Call.Value.(*ssa.Builtin)
			if !ok || builtin.Name() != name {
				continue
			}
			if found != nil {
				t.Fatalf("%s has more than one %s builtin call", fn, name)
			}
			found = call
		}
	}
	if found == nil {
		t.Fatalf("%s has no %s builtin call", fn, name)
	}
	return found
}
