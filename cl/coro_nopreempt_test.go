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
	"go/ast"
	"strings"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestCoroNoPreemptOutcomeCycleRequiresFrozenCertificate(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{{name: "native"}, {name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			pkg, _, files := buildGoSSAPkg(t, `package foo
type link struct { next *link }
//llgo:nopreempt
func Walk(node *link) *link {
    for node.next != nil { node = node.next }
    return node
}
func Root(node *link) *link { return Walk(node) }
//llgo:nopreempt
func Other() {}
`)
			prog := newLLSSAProgForTarget(t, target.arch)
			defer prog.Dispose()
			u, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg, Files: files}})
			if err != nil {
				t.Fatal(err)
			}
			inventory, err := coro.NewSSAEmissionUniverse(pkg.Prog, u.Functions())
			if err != nil {
				t.Fatal(err)
			}
			ids := u.FunctionIDConfig()
			ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = coro.PhysicalABIV1, coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0, true
			plan, err := coro.AnalyzeSSA(pkg.Prog, coro.Roots{{Function: pkg.Func("Root"), Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse: inventory, FunctionIDs: ids, MaxPlainInstructions: -1,
				OutcomeMode: coro.OutcomeExplicitStatus, ClassifyLocalBody: u.CoroLocalBodyFacts,
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					_, present, err := u.CoroNoPreemptCertificate(fn)
					return coro.SSAFunctionPolicy{TrustedNoPreempt: present}, err
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			walk := pkg.Func("Walk")
			fact, present := plan.FunctionPlan(walk)
			if !present || fact.Emission != coro.EmitOutcomePlain || fact.Exec.Contains(coro.NeedsPreempt) {
				t.Fatalf("certified metadata walk did not select a synchronous outcome entry: %+v", fact)
			}
			validate := func() error {
				return validateCoroPhysicalABIWithUniverseCapabilitiesAndFrameRetention(
					walk, fact, plan, u, true, true, false, true, coro.FrameRetentionParkABIV2,
				)
			}
			if err := validate(); err != nil {
				t.Fatalf("physical validation lost the frozen no-preempt certificate: %v", err)
			}
			delete(u.noPreempt, walk) // A stale/absent proof must not authorize a cyclic outcome body.
			if _, present, err := u.CoroNoPreemptCertificate(pkg.Func("Other")); err != nil || !present {
				t.Fatalf("unrelated certificate disappeared: present=%t error=%v", present, err)
			}
			if err := validate(); err == nil || !strings.Contains(err.Error(), "cyclic CFG requires needs-preempt") {
				t.Fatalf("uncertified cyclic outcome body escaped physical validation: %v", err)
			}
			u.noPreempt[walk] = ""
			if err := validate(); err == nil || !strings.Contains(err.Error(), "cyclic CFG requires needs-preempt") {
				t.Fatalf("empty no-preempt certificate authorized a cyclic outcome body: %v", err)
			}
		})
	}
}

func TestEmissionUniverseFreezesExactNoPreemptDirective(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/nopreempt", `package nopreempt
//llgo:nopreempt
func Loop() { for {} }
func Plain() {}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	certificate, certified, err := universe.CoroNoPreemptCertificate(pkg.ssa.Func("Loop"))
	if err != nil || !certified || len(certificate) != 64 {
		t.Fatalf("Loop no-preempt certificate = %q, %t, %v", certificate, certified, err)
	}
	if certificate, certified, err := universe.CoroNoPreemptCertificate(pkg.ssa.Func("Plain")); err != nil || certified || certificate != "" {
		t.Fatalf("Plain no-preempt certificate = %q, %t, %v; want absent", certificate, certified, err)
	}
}

func TestEmissionUniverseFreezesExactNoUnwindDirective(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/nounwind", `package nounwind
//llgo:nounwind
func Load(value *int) int { return *value }
func Plain() {}
`)
	testProg.ssa.Build()
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	certificate, certified, err := universe.CoroNoUnwindCertificate(pkg.ssa.Func("Load"))
	if err != nil || !certified || len(certificate) != 64 {
		t.Fatalf("Load no-unwind certificate = %q, %t, %v", certificate, certified, err)
	}
	if certificate, certified, err := universe.CoroNoUnwindCertificate(pkg.ssa.Func("Plain")); err != nil || certified || certificate != "" {
		t.Fatalf("Plain no-unwind certificate = %q, %t, %v; want absent", certificate, certified, err)
	}
}

func TestEmissionUniverseDoesNotPublishRawCriticalForConstantDeadCall(t *testing.T) {
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, "example.com/emission/rawcriticaldead", `package rawcriticaldead
//llgo:rawcritical
func Raw() {}
func Root() {
	switch "darwin" {
	case "darwin":
		return
	default:
		Raw()
	}
}
`)
	testProg.ssa.Build()
	root := pkg.ssa.Func("Root")
	var dead *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common().StaticCallee() == pkg.ssa.Func("Raw") {
				dead = call
			}
		}
	}
	if dead == nil || coroPhysicalConstantReachableBlocks(root)[dead.Block()] {
		t.Fatalf("raw-critical fixture call = %v, want one constant-dead occurrence", dead)
	}

	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, frozen, err := universe.CoroCallSitePlan(dead)
	if err != nil || !frozen || plan.Elision != CoroCallElidedFrontendUnevaluated || plan.RawPlain {
		t.Fatalf("constant-dead raw-critical SitePlan = %+v, %t, %v; want frontend-unevaluated without raw/plain demand", plan, frozen, err)
	}
}

func TestEmissionUniverseRejectsMalformedNoPreemptDirective(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "arguments",
			source: `package nopreemptbad
//llgo:nopreempt unsafe
func Bad() {}
`,
			want: "accepts no arguments",
		},
		{
			name: "bodyless",
			source: `package nopreemptbad
//llgo:nopreempt
func Bad()
`,
			want: "bodyful Go function",
		},
		{
			name: "nounwind arguments",
			source: `package nopreemptbad
//llgo:nounwind unsafe
func Bad() {}
`,
			want: "accepts no arguments",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			pkg := testProg.addPackage(t, "example.com/emission/nopreemptbad"+test.name, test.source)
			testProg.ssa.Build()
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			_, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("PrepareEmissionUniverse error = %v; want %q", err, test.want)
			}
		})
	}
}
