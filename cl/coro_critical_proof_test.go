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

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

const coroCriticalProofPreamble = `package foo
import _ "unsafe"
//go:linkname enter llgo.coroCriticalEnter
func enter()
//go:linkname exit llgo.coroCriticalExit
func exit()
var cell uint32
var sink *uint32
`

func TestCoroCriticalRegionProofRejectsInvalidCFGAndOperations(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "underflow",
			body: `func Root() { exit() }`,
			want: "underflows depth zero",
		},
		{
			name: "unbalanced return",
			body: `func Root() { enter() }`,
			want: "function exit or panic is forbidden",
		},
		{
			name: "depth join mismatch",
			body: `func Root(flag bool) {
	if flag { enter() }
	cell = 1
	if flag { exit() }
}`,
			want: "critical depth join mismatch",
		},
		{
			name: "masked cycle",
			body: `func Root(n uint32) {
	enter()
	for n != 0 { cell = n; n-- }
	exit()
}`,
			want: "cyclic CFG path",
		},
		{
			name: "ordinary call",
			body: `func helper() { cell = 1 }
func Root() { enter(); helper(); exit() }`,
			want: "ordinary or non-atomic call",
		},
		{
			name: "allocation",
			body: `func Root() { enter(); sink = new(uint32); exit() }`,
			want: "outside the bounded critical-region allowlist",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := proveCoroCriticalFixture(t, coroCriticalProofPreamble+test.body)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("critical proof error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestCoroCriticalRegionProofAcceptsBalancedBranchAndDepthZeroLoop(t *testing.T) {
	for _, body := range []string{
		`func Root(flag bool) {
	enter()
	if flag { cell = 1 } else { cell = 2 }
	exit()
}`,
		`func Root(n uint32) {
	for n != 0 {
		enter()
		cell = n
		exit()
		n--
	}
}`,
		`func Root() {
	enter()
	enter()
	cell = 1
	exit()
	exit()
}`,
	} {
		proof, err := proveCoroCriticalFixture(t, coroCriticalProofPreamble+body)
		if err != nil || proof == nil {
			t.Fatalf("balanced critical proof = %v, %v", proof, err)
		}
	}
}

func TestCoroCriticalRegionProofRejectsOverBudgetPath(t *testing.T) {
	var source strings.Builder
	source.WriteString(coroCriticalProofPreamble)
	source.WriteString("func Root(v uint32) { enter();\n")
	for index := 0; index < coroPreemptInstructionBudget+1; index++ {
		source.WriteString("cell = v\n")
	}
	source.WriteString("exit() }")
	_, err := proveCoroCriticalFixture(t, source.String())
	if err == nil || !strings.Contains(err.Error(), "exceeds the 64-instruction preemption budget") {
		t.Fatalf("over-budget critical proof error = %v", err)
	}
}

func TestCoroCriticalMarkerCannotBeMaterializedAsFunctionValue(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, coroCriticalProofPreamble+`
var marker = enter
func Root() { marker() }
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	_, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err == nil || !strings.Contains(err.Error(), "critical marker") || !strings.Contains(err.Error(), "cannot be materialized as a function value") {
		t.Fatalf("critical marker materialization error = %v", err)
	}
}

func proveCoroCriticalFixture(t *testing.T, source string) (*coroCriticalProof, error) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == root {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, Exec: coro.NeedsPreempt}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callee := call.Common().StaticCallee()
			if callee != nil && callee.Pkg != nil && callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init" {
				return true, nil
			}
			semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
			return intrinsic && semantics.ElidesManagedCall(), err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	audit, err := newCoroPhysicalPureSSAAudit(universe, plan, root, "")
	if err != nil {
		t.Fatal(err)
	}
	return proveCoroCriticalRegions(universe, plan, audit)
}
