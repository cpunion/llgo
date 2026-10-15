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

type ArrayValue struct { N int }

var Sink bool
var ArrayValues [4]ArrayValue

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

func GlobalArrayIndex(index int) *int {
	return &ArrayValues[index].N
}

func PointerArrayIndex(values *[4]ArrayValue, index int) *int {
	return &values[index].N
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

const ssaCompletedFieldAddressFixture = `package foo
import "unsafe"
type Value struct { A, B int }
var P *int
var N int
func SameBlock(p *Value) { P = &p.A; P = &p.B }
func StrictBlock(p *Value, yes bool) { P = &p.A; if yes { P = &p.B } }
func Converted(p *Value) { P = &p.A; q := (*Value)(unsafe.Pointer(p)); P = &q.B }
func DifferentBase(p, q *Value) { P = &p.A; P = &q.B }
func NonDominating(p *Value, yes bool) { if yes { P = &p.A }; P = &p.B }
func Implicit(p *Value) { N = p.A; P = &p.B }
func UnusedExplicit(p *Value) { _ = &p.A; P = &p.B }
func AddressResult(p *Value) { q := &p.A; _ = *q }
func AddressResultConverted(p *Value) { q := (*int)(unsafe.Pointer(&p.A)); _ = *q }
`

func TestSSADominatingCompletedFieldAddressIsExact(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, ssaCompletedFieldAddressFixture)
	fields := collectAddrOfFieldSelectors(files)
	for _, test := range []struct {
		name string
		want bool
	}{
		{"SameBlock", true}, {"StrictBlock", true}, {"Converted", true},
		{"UnusedExplicit", true}, {"DifferentBase", false},
		{"NonDominating", false}, {"Implicit", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			function := ssaPkg.Func(test.name)
			var addresses []*ssa.FieldAddr
			for _, block := range function.Blocks {
				for _, instruction := range block.Instrs {
					if field, ok := instruction.(*ssa.FieldAddr); ok {
						addresses = append(addresses, field)
					}
				}
			}
			if len(addresses) != 2 {
				t.Fatalf("FieldAddr count = %d, want 2", len(addresses))
			}
			first, second := addresses[0], addresses[1]
			if ssaDominatingFieldAddressNonNil(first.X, first, fields, nil) {
				t.Fatal("first check proved itself using a same/later instruction")
			}
			if got := ssaDominatingFieldAddressNonNil(second.X, second, fields, nil); got != test.want {
				t.Fatalf("completed field check = %v, want %v", got, test.want)
			}
			wantResult := test.name != "NonDominating" && test.name != "Implicit"
			if got := ssaDominatingFieldAddressNonNil(first, second, fields, nil); got != wantResult {
				t.Fatalf("completed address result = %v, want %v", got, wantResult)
			}
			if ssaDominatingFieldAddressNonNil(second, first, fields, nil) {
				t.Fatal("later field-address result proved an earlier instruction")
			}
			if ssaDominatingFieldAddressNonNil(second.X, second, nil, nil) {
				t.Fatal("missing source &field evidence manufactured a successful check")
			}
			unevaluated := map[ssa.Instruction]none{first: {}}
			if ssaDominatingFieldAddressNonNil(second.X, second, fields, unevaluated) ||
				ssaDominatingFieldAddressNonNil(first, second, fields, unevaluated) {
				t.Fatal("unevaluated FieldAddr manufactured a successful check")
			}
			delete(fields, first.Pos())
			if ssaDominatingFieldAddressNonNil(second.X, second, fields, nil) {
				t.Fatal("source-unconfirmed first FieldAddr manufactured a successful check")
			}
		})
	}
	for _, name := range []string{"AddressResult", "AddressResultConverted"} {
		function := ssaPkg.Func(name)
		deref := onlySSADeref(t, function)
		if !ssaAddressValueProvenNonNilAt(deref.X, deref, fields, nil) {
			t.Fatalf("%s completed address result retained a redundant nil check", name)
		}
		if ssaAddressValueProvenNonNilAt(deref.X, deref, nil, nil) {
			t.Fatalf("%s result was accepted without source check evidence", name)
		}
	}
}

func TestCompletedFieldAddressChecksMatchCodegenAndEmissionFacts(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, ssaCompletedFieldAddressFixture)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	pkg, err := NewPackage(prog, ssaPkg, files)
	if err != nil {
		t.Fatal(err)
	}
	universe := &EmissionUniverse{prog: prog}
	for _, test := range []struct {
		name   string
		checks int
	}{
		{"SameBlock", 1}, {"StrictBlock", 1}, {"Converted", 1},
		{"UnusedExplicit", 1}, {"DifferentBase", 2}, {"NonDominating", 2},
		{"AddressResult", 1}, {"AddressResultConverted", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			function := ssaPkg.Func(test.name)
			ctx := &context{prog: prog, goFn: function, goProg: ssaPkg.Prog,
				goTyps: ssaPkg.Pkg, goPkg: ssaPkg, addrOfFieldAddrs: collectAddrOfFieldSelectors(files)}
			planned := 0
			for _, block := range function.Blocks {
				for _, instruction := range block.Instrs {
					if stringSliceContains(universe.loweredRuntimeHelpers(ctx, instruction), "AssertNilDeref") {
						planned++
					}
				}
			}
			compiled := pkg.Module().NamedFunction("foo." + test.name)
			if compiled.IsNil() {
				t.Fatal("missing compiled function")
			}
			emitted := 0
			for _, line := range strings.Split(compiled.String(), "\n") {
				if strings.Contains(line, "call ") && strings.Contains(line, "AssertNilDeref") {
					emitted++
				}
			}
			if planned != test.checks || emitted != test.checks {
				t.Fatalf("nil guards planned=%d emitted=%d, want %d:\n%s", planned, emitted, test.checks, compiled.String())
			}
		})
	}
}

func TestUnevaluatedFieldAddressDoesNotRemoveLaterNilCheck(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package foo
import "unsafe"
type Value struct { A, B int }
func SizeOnly[T any](p *Value) *int {
	_ = unsafe.Sizeof(struct { Field *int; Extra T }{Field: &p.A})
	return &p.B
}
func Use(p *Value) *int { return SizeOnly[int](p) }
`)
	function := unsafeSizeAlignStaticCallee(t, ssaPkg.Func("Use"), "SizeOnly")
	fields := collectAddrOfFieldSelectors(files)
	unevaluated := collectUnsafeLayoutUnevaluatedSSA(function)
	var omitted, evaluated *ssa.FieldAddr
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			field, ok := instruction.(*ssa.FieldAddr)
			if !ok || ssaCanonicalPointerValue(field.X) != function.Params[0] {
				continue
			}
			if _, skip := unevaluated[field]; skip {
				omitted = field
			} else {
				evaluated = field
			}
		}
	}
	if omitted == nil || evaluated == nil {
		t.Fatalf("fixture lacks both field addresses: omitted=%v evaluated=%v", omitted, evaluated)
	}
	if !ssaDominatingFieldAddressNonNil(evaluated.X, evaluated, fields, nil) {
		t.Fatal("fixture does not expose the false proof when omission facts are missing")
	}
	if ssaAddressValueProvenNonNilAt(evaluated.X, evaluated, fields, unevaluated) {
		t.Fatal("unevaluated source &field incorrectly removed the actual nil check")
	}
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	pkg, err := NewPackage(prog, ssaPkg, files)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &context{prog: prog, goFn: function, goProg: ssaPkg.Prog,
		goTyps: ssaPkg.Pkg, goPkg: ssaPkg, addrOfFieldAddrs: fields, unevaluatedSSA: unevaluated}
	if !stringSliceContains((&EmissionUniverse{prog: prog}).loweredRuntimeHelpers(ctx, evaluated), "AssertNilDeref") {
		t.Fatal("helper inventory lost the actual nil check")
	}
	compiled := unsafeSizeAlignFunctionWithPrefix(t, pkg.Module(), "foo.SizeOnly[")
	guards := 0
	for _, line := range strings.Split(compiled.String(), "\n") {
		if strings.Contains(line, "call ") && strings.Contains(line, "AssertNilDeref") {
			guards++
		}
	}
	if guards != 1 {
		t.Fatalf("emitted guards = %d, want exactly the actual field's guard:\n%s", guards, compiled.String())
	}
}

func TestSSACompletedIndexAddrPreservesBaseNonNilProof(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, ssaDominatingNonNilFixture)
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "GlobalArrayIndex", want: true},
		{name: "PointerArrayIndex"},
	} {
		function := ssaPkg.Func(test.name)
		field := onlySSAFieldAddr(t, function)
		index, ok := field.X.(*ssa.IndexAddr)
		if !ok {
			t.Fatalf("%s FieldAddr base is %T, want *ssa.IndexAddr", test.name, field.X)
		}
		if got := ssaAddressValueProvenNonNilAt(index, field, nil, nil); got != test.want {
			t.Errorf("%s completed IndexAddr non-nil proof = %t, want %t", test.name, got, test.want)
		}
	}
}

func TestSSASourceClosureFreeVarCellIsStructurallyNonNil(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
var Sink int
func Owner(value int) func() {
	return func() { _ = value; Sink++ }
}
`)
	owner := ssaPkg.Func("Owner")
	if owner == nil {
		t.Fatal("SSA package has no Owner")
	}
	if len(owner.AnonFuncs) != 1 {
		t.Fatalf("Owner anonymous functions = %v, want one", owner.AnonFuncs)
	}
	closure := owner.AnonFuncs[0]
	if closure.Parent() != owner || closure.Synthetic != "" || len(closure.FreeVars) != 1 {
		t.Fatalf("source closure shape: parent=%v synthetic=%q freevars=%d", closure.Parent(), closure.Synthetic, len(closure.FreeVars))
	}
	free := closure.FreeVars[0]
	var deref *ssa.UnOp
	for _, block := range closure.Blocks {
		for _, instruction := range block.Instrs {
			candidate, ok := instruction.(*ssa.UnOp)
			if ok && candidate.Op == token.MUL && candidate.X == free {
				deref = candidate
			}
		}
	}
	if deref == nil || !isKnownNonNilAddr(free) || !ssaAddressValueProvenNonNilAt(free, deref, nil, nil) {
		t.Fatalf("source closure cell lacks structural non-nil proof: free=%v deref=%v", free, deref)
	}
	if audit := (&coroPhysicalPureSSAAudit{fn: closure}); audit.derefRequiresImplicitNilFault(deref) {
		t.Fatal("physical coroutine planning reintroduced a nil fault for a proven lexical capture cell")
	}

	closure.Synthetic = "wrapper"
	if isKnownNonNilAddr(free) || ssaAddressValueProvenNonNilAt(free, deref, nil, nil) {
		t.Fatal("synthetic captured value received lexical-cell non-nil authority")
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
