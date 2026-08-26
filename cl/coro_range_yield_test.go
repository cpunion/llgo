//go:build !llgo
// +build !llgo

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

import "testing"

func TestValidateCoroExactRangeYieldAcceptsZeroArity(t *testing.T) {
	ssaPkg := buildSSAPackage(t, `package foo

func seq(yield func() bool) { _ = yield() }

func run() {
	for range seq {
	}
}
`)
	run := ssaPkg.Func("run")
	if run == nil {
		t.Fatal("run function is absent")
	}
	if len(run.AnonFuncs) != 1 {
		t.Fatalf("run range-yield closures = %d, want 1", len(run.AnonFuncs))
	}
	yield := run.AnonFuncs[0]
	if yield.Signature == nil || yield.Signature.Params() != nil {
		t.Fatalf("zero-arity range-yield parameters = %v, want nil tuple", yield.Signature)
	}
	if err := validateCoroExactRangeYield(yield); err != nil {
		t.Fatalf("validate zero-arity range-yield: %v", err)
	}
}
