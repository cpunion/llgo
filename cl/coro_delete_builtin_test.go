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

func TestCoroDeleteBuiltinRequiresFrozenManagedHelpers(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func Root(values map[uint32]uint64, key uint32) { delete(values, key) }
`)
	root := ssaPkg.Func("Root")
	var call *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			candidate, ok := instruction.(*ssa.Call)
			if !ok || candidate.Common() == nil {
				continue
			}
			builtin, ok := candidate.Common().Value.(*ssa.Builtin)
			if ok && builtin.Name() == "delete" {
				call = candidate
			}
		}
	}
	if call == nil {
		t.Fatal("fixture has no delete builtin")
	}
	audit, err := newCoroPhysicalPureSSAAudit(nil, nil, root, "")
	if err != nil {
		t.Fatal(err)
	}
	handled, reason := audit.validate(call)
	if !handled || !strings.Contains(reason, "structured runtime helper validation requires a frozen emission universe") {
		t.Fatalf("delete audit = handled %t, reason %q; want exact managed-helper gate", handled, reason)
	}
}
