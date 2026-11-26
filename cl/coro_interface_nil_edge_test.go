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
	"slices"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestCoroInterfaceNilComparisonElidesOnlyExactHelpers(t *testing.T) {
	pkg, _, _ := buildGoSSAPkg(t, `package foo
func ErrorNil(value error) bool { return value == nil }
func ErrorNilReverse(value error) bool { return nil != value }
func ErrorNilConst() bool { return error(nil) != nil }
func AnyNil(value any) bool { return value != nil }
func AnyNilConst() bool { return any(nil) == nil }
func Dynamic(left,right any) bool { return left == right }
func Slice(value []byte) bool { return value == nil }
func Generic[T ~*int](value T) bool { return value == nil }
`)
	for _, test := range []struct {
		name string
		want bool
	}{
		{"ErrorNil", true},
		{"ErrorNilReverse", true},
		{"ErrorNilConst", true},
		{"AnyNil", true},
		{"AnyNilConst", true},
		{"Dynamic", false},
		{"Slice", false},
		{"Generic", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fn := pkg.Func(test.name)
			found := false
			for _, block := range fn.Blocks {
				for _, inst := range block.Instrs {
					op, ok := inst.(*ssa.BinOp)
					if !ok {
						continue
					}
					found = true
					for _, helper := range []string{"EfaceEqual", "IfaceType"} {
						if got := coroCompilerElidesImplicitFaultRuntimeHelper(op, helper); got != test.want {
							t.Errorf("%s: elides %s=%t want %t", op, helper, got, test.want)
						}
					}
					if coroCompilerElidesImplicitFaultRuntimeHelper(op, "AllocZ") {
						t.Error("nil comparison elided unrelated helper")
					}
				}
			}
			if !found {
				t.Fatal("missing SSA compare")
			}
		})
	}
}

func TestCoroInterfaceNilComparisonPlainHelpersElided(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/ifacenil", `package ifacenil
func ErrorNil(value error) bool { return value == nil }
func ErrorNilReverse(value error) bool { return nil == value }
func AnyNil(value any) bool { return value != nil }
func DynamicError(left, right error) bool { return left == right }
func DynamicAny(left, right any) bool { return left != right }
`)
	testProg.ssa.Build()
	universe, owner := newEmissionABIDemandTestUniverse(testProg, pkg)
	for _, test := range []struct {
		name      string
		wantEface bool
		wantIface bool
	}{
		{"ErrorNil", false, false},
		{"ErrorNilReverse", false, false},
		{"AnyNil", false, false},
		{"DynamicError", true, true},
		{"DynamicAny", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fn := pkg.ssa.Func(test.name)
			ctx, err := universe.functionABIContext(fn, owner)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, block := range fn.Blocks {
				for _, inst := range block.Instrs {
					op, ok := inst.(*ssa.BinOp)
					if !ok {
						continue
					}
					found = true
					helpers := universe.plainRepresentationRuntimeHelpers(ctx, op)
					if got := slices.Contains(helpers, "EfaceEqual"); got != test.wantEface {
						t.Errorf("%s: plain EfaceEqual=%t want %t (helpers=%v)", op, got, test.wantEface, helpers)
					}
					if got := slices.Contains(helpers, "IfaceType"); got != test.wantIface {
						t.Errorf("%s: plain IfaceType=%t want %t (helpers=%v)", op, got, test.wantIface, helpers)
					}
				}
			}
			if !found {
				t.Fatal("missing SSA compare")
			}
		})
	}
}
