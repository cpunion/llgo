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
)

const coroMinMaxBuiltinFixture = `package foo
type Count int
func MinInt(a, b, c int) int { return min(a, b, c) }
func MaxFloat(a, b float64) float64 { return max(a, b) }
func MinNamed(a, b Count) Count { return min(a, b) }
func MaxString(a, b string) string { return max(a, b) }
`

func TestCoroMinMaxNumericBuiltinsArePureSelects(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, coroMinMaxBuiltinFixture)
	for _, test := range []struct {
		function string
		builtin  string
	}{
		{function: "MinInt", builtin: "min"},
		{function: "MaxFloat", builtin: "max"},
		{function: "MinNamed", builtin: "min"},
	} {
		t.Run(test.function, func(t *testing.T) {
			fn := ssaPkg.Func(test.function)
			call := coroComplexBuiltinCall(t, fn, test.builtin)
			audit := &coroPhysicalPureSSAAudit{fn: fn, reachableBlocks: coroPhysicalConstantReachableBlocks(fn)}
			if reason := audit.validateBuiltin(call); reason != "" {
				t.Fatalf("%s rejected: %s", test.builtin, reason)
			}
		})
	}
}

func TestCoroMinMaxStringBuiltinFreezesStringLess(t *testing.T) {
	prog, _, universe, root, audit, _ := prepareCoroFrameRootAudit(
		t, coroMinMaxBuiltinFixture, "MaxString", EmissionUniverseOptions{},
	)
	defer prog.Dispose()
	call := coroComplexBuiltinCall(t, root, "max")
	if got := strings.Join(universe.loweredRuntimeHelpers(audit.ctx, call), ","); got != "StringLess" {
		t.Fatalf("max string helpers = %q, want StringLess", got)
	}
	if reason := audit.validateBuiltin(call); reason != "runtime helper capability validation requires a frozen emission universe" {
		t.Fatalf("max string validation = %q", reason)
	}
}

func TestCoroMinMaxBuiltinRejectsMalformedShape(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, coroMinMaxBuiltinFixture)
	fn := ssaPkg.Func("MinInt")
	call := coroComplexBuiltinCall(t, fn, "min")
	args := call.Call.Args
	call.Call.Args = nil
	defer func() { call.Call.Args = args }()
	audit := &coroPhysicalPureSSAAudit{fn: fn, reachableBlocks: coroPhysicalConstantReachableBlocks(fn)}
	if reason := audit.validateBuiltin(call); !strings.Contains(reason, "invalid argument/result shape") {
		t.Fatalf("malformed min rejection = %q", reason)
	}
}
