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

const ssaDominatingNonZeroDivisorFixture = `package foo

var DivisorProbeSink int64

func GuardedZeroReturn(x, d int64) int64 {
	if d == 0 {
		return 0
	}
	return x / d
}

func GuardedZeroLeftNotEqual(x, d int64) int64 {
	if 0 != d {
		return x / d
	}
	return 0
}

func GuardedLowerBoundAndWiden(x uint64, d int) uint64 {
	if d < 2 {
		return 0
	}
	return x / uint64(d)
}

func NarrowingCanBecomeZero(x, d uint64) uint64 {
	if d == 0 {
		return 0
	}
	return x / uint64(uint8(d))
}

func NonDominatingSibling(x, d int64, left bool) int64 {
	if left {
		if d != 0 {
			DivisorProbeSink = d
		}
	} else {
		return x / d
	}
	return 0
}

func LoopBoundLen(value int, xs []int) int {
	result := 0
	for index := 0; index < len(xs); index++ {
		result += value / len(xs)
	}
	return result
}

func LoopBoundCachedLen(value int, xs []int) int {
	result := 0
	limit := len(xs)
	for index := 0; index < limit; index++ {
		result += value / limit
	}
	return result
}

func LoopBoundNegativeIndex(value int, xs []int) int {
	result := 0
	for index := -1; index < len(xs); index++ {
		result += value / len(xs)
	}
	return result
}
`

func TestSSAIntegerNonZeroDivisorDominanceProofIsExact(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, ssaDominatingNonZeroDivisorFixture)
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "GuardedZeroReturn", want: true},
		{name: "GuardedZeroLeftNotEqual", want: true},
		{name: "GuardedLowerBoundAndWiden", want: true},
		{name: "NarrowingCanBecomeZero", want: false},
		{name: "NonDominatingSibling", want: false},
		{name: "LoopBoundLen", want: true},
		{name: "LoopBoundCachedLen", want: true},
		{name: "LoopBoundNegativeIndex", want: false},
	} {
		division := onlySSAIntegerDivision(t, ssaPkg.Func(test.name))
		if got := ssaIntegerValueProvenNonZeroAt(division.Y, division); got != test.want {
			t.Errorf("%s dominated non-zero divisor proof = %t, want %t", test.name, got, test.want)
		}
	}
}

func TestSSAFloatDivisionNeedsNoIntegerDivisorProof(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func Divide(x, divisor float64) float64 { return x / divisor }
`)
	function := ssaPkg.Func("Divide")
	division := onlySSAIntegerDivision(t, function)
	audit := &coroPhysicalPureSSAAudit{
		fn:              function,
		reachableBlocks: coroPhysicalConstantReachableBlocks(function),
	}
	if reason := audit.validateBinOp(division); reason != "" {
		t.Fatalf("floating-point division rejected by physical SSA audit: %s", reason)
	}
}

func TestSSAUnprovenIntegerDivisionRequiresExplicitStatusFault(t *testing.T) {
	ssaPkg, _, _ := buildGoSSAPkg(t, `package foo
func Divide(x, divisor int64) int64 { return x / divisor }
`)
	function := ssaPkg.Func("Divide")
	division := onlySSAIntegerDivision(t, function)
	audit := &coroPhysicalPureSSAAudit{
		fn:              function,
		reachableBlocks: coroPhysicalConstantReachableBlocks(function),
	}
	if reason := audit.validateBinOp(division); !strings.Contains(reason, "requires the explicit-status panic ABI") {
		t.Fatalf("unproven integer division without explicit status reason = %q", reason)
	}
	audit.allowImplicitNilFault = true
	if reason := audit.validateBinOp(division); reason != "" {
		t.Fatalf("unproven integer division explicit-status fault rejected: %s", reason)
	}
}

func TestDominatingNonZeroDivisionMatchesCodegenAndEmissionFacts(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, ssaDominatingNonZeroDivisorFixture)
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
	}{
		{name: "GuardedZeroReturn"},
		{name: "GuardedZeroLeftNotEqual"},
		{name: "GuardedLowerBoundAndWiden"},
		{name: "NarrowingCanBecomeZero", wantHelper: true},
		{name: "NonDominatingSibling", wantHelper: true},
		{name: "LoopBoundLen"},
		{name: "LoopBoundCachedLen"},
		{name: "LoopBoundNegativeIndex", wantHelper: true},
	} {
		function := ssaPkg.Func(test.name)
		division := onlySSAIntegerDivision(t, function)
		ctx := &context{
			prog:   prog,
			goFn:   function,
			goProg: ssaPkg.Prog,
			goTyps: ssaPkg.Pkg,
			goPkg:  ssaPkg,
		}
		hasEmissionHelper := stringSliceContains(
			universe.loweredRuntimeHelpers(ctx, division),
			"AssertDivideByZero",
		)
		if hasEmissionHelper != test.wantHelper {
			t.Errorf("%s emission divide-by-zero helper = %t, want %t", test.name, hasEmissionHelper, test.wantHelper)
		}

		compiled := module.NamedFunction("foo." + test.name)
		if compiled.IsNil() {
			t.Fatalf("missing compiled function foo.%s", test.name)
		}
		ir := compiled.String()
		hasPhysicalHelper := strings.Contains(ir, "AssertDivideByZero")
		if hasPhysicalHelper != test.wantHelper {
			t.Errorf("%s physical divide-by-zero helper = %t, want %t:\n%s", test.name, hasPhysicalHelper, test.wantHelper, ir)
		}
	}

	// Proving d != 0 removes only the divide-by-zero edge. Signed min/-1 is
	// defined by Go and may trap in a raw LLVM sdiv, so its safe operands and
	// result select must remain in the guarded lowering.
	guarded := module.NamedFunction("foo.GuardedZeroReturn").String()
	for _, fragment := range []string{
		"sdiv i64",
		"-9223372036854775808",
		"-1",
	} {
		if !strings.Contains(guarded, fragment) {
			t.Errorf("guarded signed division lost %q overflow lowering:\n%s", fragment, guarded)
		}
	}
	if got := strings.Count(guarded, "select i1"); got < 3 {
		t.Errorf("guarded signed division has %d selects, want at least 3 for safe x, safe d, and Go result:\n%s", got, guarded)
	}
}

func onlySSAIntegerDivision(t *testing.T, function *ssa.Function) *ssa.BinOp {
	t.Helper()
	if function == nil {
		t.Fatal("missing SSA function")
	}
	var found *ssa.BinOp
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			operation, ok := instruction.(*ssa.BinOp)
			if !ok || operation.Op != token.QUO {
				continue
			}
			if found != nil {
				t.Fatalf("%s has more than one integer division", function.Name())
			}
			found = operation
		}
	}
	if found == nil {
		t.Fatalf("%s has no integer division", function.Name())
	}
	return found
}
