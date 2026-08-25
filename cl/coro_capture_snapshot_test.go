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

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

const coroImmutableCaptureSnapshotFixture = `package foo

var Sink uint32

func Root(value uint32) {
	defer func() {
		Sink = value
	}()
}
`

func TestCoroImmutableCaptureSnapshotNativeAndWasm32CoroSplit(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, _, _, target, _ := compileCoroCapturedStaticCleanupFixtureSource(
				t, test.target, coroImmutableCaptureSnapshotFixture, true,
			)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			proofs := coro.ProveSSAImmutableCaptureSnapshots(target)
			if len(proofs) != 1 || proofs[0].Index != 0 || len(proofs[0].Loads) != 1 {
				t.Fatalf("immutable capture proof = %+v, want one exact source load", proofs)
			}
			targetPlan, planned := plan.FunctionPlan(target)
			if !planned || targetPlan.Emission != coro.EmitCoroutine {
				t.Fatalf("captured target plan = %+v, present=%t", targetPlan, planned)
			}
			ramp := requireCoroPhysicalFunction(t, module, target.String())
			rampIR := ramp.String()
			if strings.Contains(rampIR, "AssertNilDeref") ||
				!strings.Contains(rampIR, "llvm.coro.suspend") {
				t.Fatalf("immutable capture ramp retained a cell nil guard or lost its initial suspend:\n%s", rampIR)
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify immutable capture before CoroSplit: %v\n%s", err, module.String())
			}
			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction(target.String() + "$coro.resume")
			if resume.IsNil() || strings.Contains(resume.String(), "AssertNilDeref") {
				t.Fatalf("immutable capture resume retained a shared-cell nil guard:\n%s", module.String())
			}
		})
	}
}
