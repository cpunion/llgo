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

package build

import (
	"testing"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func TestCoroRawABIPlainClosureUsesOnlyLiveSynchronousReferences(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, llssa.PkgRuntime, `package runtime
func LiveOwner() {}
func DeadOwner() {}

func rawHash(n int) uintptr {
	if n <= 0 { return 1 }
	return staticHelper(n - 1)
}
func staticHelper(n int) uintptr {
	if n <= 0 { return 2 }
	return staticHelper(n - 1)
}
func loweredHelper(n int) uintptr {
	if n <= 0 { return 3 }
	return loweredHelper(n - 1)
}
func ordinaryReferenced(n int) uintptr {
	if n <= 0 { return 5 }
	return ordinaryReferenced(n - 1)
}
func deadHash(n int) uintptr {
	if n <= 0 { return 4 }
	return deadHash(n - 1)
}
`, nil)
	liveOwner := ssaPkg.Func("LiveOwner")
	deadOwner := ssaPkg.Func("DeadOwner")
	rawHash := ssaPkg.Func("rawHash")
	staticHelper := ssaPkg.Func("staticHelper")
	loweredHelper := ssaPkg.Func("loweredHelper")
	ordinaryReferenced := ssaPkg.Func("ordinaryReferenced")
	deadHash := ssaPkg.Func("deadHash")
	universe, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, []*ssa.Function{
		liveOwner, deadOwner, rawHash, staticHelper, loweredHelper, ordinaryReferenced, deadHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := CoroPlanInput{
		Program:          ssaPkg.Prog,
		EmissionUniverse: universe,
		functionBackground: func(*ssa.Function) (llssa.Background, bool, error) {
			return llssa.InGo, true, nil
		},
		demandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
			switch fn {
			case liveOwner:
				return []*ssa.Function{rawHash, ordinaryReferenced}, nil
			case deadOwner:
				return []*ssa.Function{deadHash}, nil
			default:
				return nil, nil
			}
		},
		syncDemandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
			switch fn {
			case liveOwner:
				return []*ssa.Function{rawHash}, nil
			case deadOwner:
				return []*ssa.Function{deadHash}, nil
			default:
				return nil, nil
			}
		},
		loweredCalls: func(fn *ssa.Function) ([]coro.SSALoweredCall, error) {
			if fn == rawHash {
				return []coro.SSALoweredCall{{LogicalName: "runtime.loweredHash", Target: loweredHelper}}, nil
			}
			return nil, nil
		},
	}
	plan, err := input.Analyze(coro.Roots{{Function: liveOwner, Demand: coro.SyncDemand}}, coro.SSAConfig{
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanForBuildTest(t, plan, rawHash); got.Demand != coro.SyncDemand || got.ManagedDemand != coro.NoDemand ||
		!got.RawPlainDemand || !got.RawPlainOnly || !got.RawPlainEntry || got.Emission != coro.EmitRawPlain || got.Primary != coro.PrimaryPlain ||
		!got.Effect.Contains(coro.YieldOnly|coro.AwaitStructured) {
		t.Fatalf("live rawHash plan = %+v; want one exact raw-only plain entry with preserved effect facts", got)
	}
	for _, helper := range []*ssa.Function{staticHelper, loweredHelper} {
		got := functionPlanForBuildTest(t, plan, helper)
		if got.ManagedDemand != coro.NoDemand || !got.RawPlainDemand || !got.RawPlainOnly || got.RawPlainEntry ||
			!plan.HasRawPlainVariant(helper) || got.Emission != coro.EmitRawPlain || got.Primary != coro.PrimaryPlain ||
			!got.Effect.Contains(coro.YieldOnly) || !got.Exec.Contains(coro.NeedsPreempt) || got.TrustedBoundedRecursion {
			t.Fatalf("live raw helper %s plan = %+v; exact static/lowered closure did not become raw-only", helper.Name(), got)
		}
	}
	ordinary := functionPlanForBuildTest(t, plan, ordinaryReferenced)
	if ordinary.Demand != coro.AsyncDemand || !ordinary.Effect.Contains(coro.YieldOnly) || ordinary.RawPlainEntry ||
		ordinary.TrustedBoundedRecursion || ordinary.Emission != coro.EmitCoroutine {
		t.Fatalf("ordinary receiver-less demand reference plan = %+v; non-synchronous use incorrectly received the raw ABI certificate", ordinary)
	}
	dead := functionPlanForBuildTest(t, plan, deadHash)
	if dead.Demand != coro.NoDemand || !dead.Effect.Contains(coro.YieldOnly) || !dead.Exec.Contains(coro.NeedsPreempt) ||
		dead.TrustedBoundedRecursion || dead.RawPlainEntry || dead.Emission != coro.EmitNone {
		t.Fatalf("dead rawHash plan = %+v; dead owner must not grant a bounded plain certificate", dead)
	}
}
