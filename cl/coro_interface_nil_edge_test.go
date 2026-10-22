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
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestCoroInterfaceNilComparisonElidesOnlyExactHelpers(t *testing.T) {
	pkg, _, _ := buildGoSSAPkg(t, `package foo
func ErrorNil(value error) bool { return value == nil }
func ErrorNilReverse(value error) bool { return nil != value }
func AnyNil(value any) bool { return value != nil }
func Dynamic(left,right any) bool { return left == right }
func Slice(value []byte) bool { return value == nil }
func Generic[T ~*int](value T) bool { return value == nil }
`)
	for _, test := range []struct {
		name string
		want bool
	}{{"ErrorNil", true}, {"ErrorNilReverse", true}, {"AnyNil", true}, {"Dynamic", false}, {"Slice", false}, {"Generic", false}} {
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
