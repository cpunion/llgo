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
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

const ssaDominatingNonNilFixture = `package foo

import "unsafe"

type Value struct { N int }

var Sink bool

func GuardedEqual(value *Value) *int {
	if value == nil { return nil }
	return &value.N
}

func GuardedNotEqual(value *Value) *int {
	if value != nil { return &value.N }
	return nil
}

func GuardedNilLeft(value *Value) *int {
	if nil != value { return &value.N }
	return nil
}

func GuardedConvert(raw unsafe.Pointer) *int {
	if raw == nil { return nil }
	value := (*Value)(raw)
	return &value.N
}

func Unguarded(value *Value) *int {
	return &value.N
}

func Bypass(value *Value) *int {
	if value != nil { Sink = true }
	return &value.N
}

func LoadGuarded(value *int) int {
	if value == nil { return 0 }
	return *value
}

func LoadUnguarded(value *int) int {
	return *value
}

func LoadBypass(value *int) int {
	if value != nil { Sink = true }
	return *value
}

func CallGuarded(fn func()) {
	if fn != nil { fn() }
}

func CallGuardedNilLeft(fn func()) {
	if nil == fn { return }
	fn()
}

func CallUnguarded(fn func()) { fn() }

func CallBypass(fn func()) {
	if fn != nil { Sink = true }
	fn()
}
`

func TestSSADominatingNonNilProofIsExact(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, ssaDominatingNonNilFixture)
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "GuardedEqual", want: true},
		{name: "GuardedNotEqual", want: true},
		{name: "GuardedNilLeft", want: true},
		{name: "GuardedConvert", want: true},
		{name: "Unguarded"},
		{name: "Bypass"},
	} {
		function := ssaPkg.Func(test.name)
		field := onlySSAFieldAddr(t, function)
		proof, got := proveSSADominatingNonNil(field.X, field)
		if got != test.want {
			t.Errorf("%s dominated non-nil proof = %t, want %t", test.name, got, test.want)
		}
		if got && (proof.Comparison == nil || proof.Branch == nil || proof.Successor == nil ||
			!proof.Successor.Dominates(field.Block())) {
			t.Errorf("%s returned incomplete dominance evidence: %+v", test.name, proof)
		}
	}

	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "LoadGuarded", want: true},
		{name: "LoadUnguarded"},
		{name: "LoadBypass"},
	} {
		function := ssaPkg.Func(test.name)
		deref := onlySSADeref(t, function)
		if got := ssaValueProvenNonNilAt(deref.X, deref); got != test.want {
			t.Errorf("%s dominated direct-deref proof = %t, want %t", test.name, got, test.want)
		}
	}
}

func TestSSAFunctionDominatingNonNilProofIsExact(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, ssaDominatingNonNilFixture)
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "CallGuarded", want: true},
		{name: "CallGuardedNilLeft", want: true},
		{name: "CallUnguarded"},
		{name: "CallBypass"},
	} {
		function := ssaPkg.Func(test.name)
		call := onlySSADynamicCall(t, function)
		if got := ssaFunctionValueProvenNonNilAt(call.Common().Value, call); got != test.want {
			t.Errorf("%s dominated function non-nil proof = %t, want %t", test.name, got, test.want)
		}
	}
}

func TestDominatedNilChecksMatchCodegenAndEmissionFacts(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, ssaDominatingNonNilFixture)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	pkg, err := NewPackage(prog, ssaPkg, files)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	universe := &EmissionUniverse{prog: prog}

	for _, test := range []struct {
		name       string
		wantHelper bool
		field      bool
	}{
		{name: "GuardedEqual", field: true},
		{name: "GuardedNotEqual", field: true},
		{name: "GuardedNilLeft", field: true},
		{name: "GuardedConvert", field: true},
		{name: "Unguarded", field: true, wantHelper: true},
		{name: "Bypass", field: true, wantHelper: true},
		{name: "LoadGuarded"},
		{name: "LoadUnguarded", wantHelper: true},
		{name: "LoadBypass", wantHelper: true},
	} {
		function := ssaPkg.Func(test.name)
		ctx := &context{
			prog:                 prog,
			goFn:                 function,
			goProg:               ssaPkg.Prog,
			goTyps:               ssaPkg.Pkg,
			goPkg:                ssaPkg,
			methodNilDerefChecks: collectMethodNilDerefChecks(function),
			addrOfFieldAddrs:     collectAddrOfFieldSelectors(files),
		}
		var instruction ssa.Instruction
		if test.field {
			instruction = onlySSAFieldAddr(t, function)
		} else {
			instruction = onlySSADeref(t, function)
		}
		hasEmissionHelper := stringSliceContains(universe.loweredRuntimeHelpers(ctx, instruction), "AssertNilDeref") ||
			stringSliceContains(universe.loweredRuntimeHelpers(ctx, instruction), "AssertNilDerefPtr")
		if hasEmissionHelper != test.wantHelper {
			t.Errorf("%s emission nil helper = %t, want %t", test.name, hasEmissionHelper, test.wantHelper)
		}

		compiled := module.NamedFunction("foo." + test.name)
		if compiled.IsNil() {
			t.Fatalf("missing compiled function foo.%s", test.name)
		}
		hasPhysicalHelper := strings.Contains(compiled.String(), "AssertNilDeref")
		if hasPhysicalHelper != test.wantHelper {
			t.Errorf("%s physical nil helper = %t, want %t:\n%s", test.name, hasPhysicalHelper, test.wantHelper, compiled.String())
		}
	}
}

func onlySSAFieldAddr(t *testing.T, function *ssa.Function) *ssa.FieldAddr {
	t.Helper()
	var found *ssa.FieldAddr
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			if field, ok := instruction.(*ssa.FieldAddr); ok {
				if found != nil {
					t.Fatalf("%s has multiple FieldAddr instructions", function.Name())
				}
				found = field
			}
		}
	}
	if found == nil {
		t.Fatalf("%s has no FieldAddr instruction", function.Name())
	}
	return found
}

func onlySSADeref(t *testing.T, function *ssa.Function) *ssa.UnOp {
	t.Helper()
	var found *ssa.UnOp
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			if deref, ok := instruction.(*ssa.UnOp); ok && deref.Op == token.MUL {
				if found != nil {
					t.Fatalf("%s has multiple dereference instructions", function.Name())
				}
				found = deref
			}
		}
	}
	if found == nil {
		t.Fatalf("%s has no dereference instruction", function.Name())
	}
	return found
}

func onlySSADynamicCall(t *testing.T, function *ssa.Function) *ssa.Call {
	t.Helper()
	var found *ssa.Call
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common() == nil || call.Common().StaticCallee() != nil {
				continue
			}
			if found != nil {
				t.Fatalf("%s has multiple dynamic calls", function.Name())
			}
			found = call
		}
	}
	if found == nil {
		t.Fatalf("%s has no dynamic call", function.Name())
	}
	return found
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
