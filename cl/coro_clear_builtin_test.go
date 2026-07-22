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

func TestCoroClearBuiltinRequiresExactManagedHelper(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func ClearSlice(values []uintptr) { clear(values) }
func ClearMap(values map[uintptr]uintptr) { clear(values) }
`)
	for _, test := range []struct {
		name   string
		helper string
	}{
		{name: "ClearSlice", helper: "SliceClear"},
		{name: "ClearMap", helper: "MapClear"},
	} {
		function := ssaPkg.Func(test.name)
		var call *ssa.Call
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				candidate, ok := instruction.(*ssa.Call)
				if !ok || candidate.Common() == nil {
					continue
				}
				builtin, ok := candidate.Common().Value.(*ssa.Builtin)
				if ok && builtin.Name() == "clear" {
					call = candidate
				}
			}
		}
		if call == nil {
			t.Fatalf("%s has no clear builtin", test.name)
		}
		audit, err := newCoroPhysicalPureSSAAudit(nil, nil, function, "")
		if err != nil {
			t.Fatal(err)
		}
		handled, reason := audit.validate(call)
		if !handled || !strings.Contains(reason, "runtime helper capability validation requires a frozen emission universe") {
			t.Fatalf("%s audit = handled %t, reason %q; want exact %s helper gate", test.name, handled, reason, test.helper)
		}
	}
}
