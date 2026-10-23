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

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
)

func TestCoroOutcomeChainLocalStorageBudget(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	const source = `package foo
func Leaf(i int, large bool) uint64 {
	if i < 0 { panic("negative") }
	if large {
		var local [65536]byte
		local[i & 65535] = byte(i)
		return uint64(local[(i+1) & 65535])
	}
	return 1
}
func Middle(i int, large bool) uint64 {
	if large {
		var local [65536]byte
		local[i & 65535] = byte(i)
		return Leaf(i, large) + uint64(local[(i+1) & 65535])
	}
	return Leaf(i, large)
}
func Parent(i int, large bool) uint64 { return Middle(i, large) }
`
	// The second form exercises x/tools Heap allocations which ProgramIR's
	// frozen borrowed-argument proof moves back to native local storage.
	for _, borrowed := range []bool{false, true} {
		fixture := source
		if borrowed {
			fixture += "\nfunc Consume(local *[65536]byte) uint64 { return uint64(local[0]) }\n"
			fixture = strings.ReplaceAll(fixture, "return uint64(local[(i+1) & 65535])", "return Consume(&local)")
			fixture = strings.ReplaceAll(fixture, "return Leaf(i, large) + uint64(local[(i+1) & 65535])", "return Leaf(i, large) + Consume(&local)")
		}
		for _, budget := range []int{128, -1} {
			prog, pkg, plan, ssaPkg := compileCoroOutcomePlainSource(t, nil, fixture, "Parent", budget)
			leaf, ok := plan.FunctionPlan(ssaPkg.Func("Leaf"))
			if !ok || !leaf.HasStaticOutcome() {
				t.Fatalf("budget %d: single-local leaf lost outcome entry: %+v", budget, leaf)
			}
			middle, ok := plan.FunctionPlan(ssaPkg.Func("Middle"))
			if !ok || middle.HasStaticOutcome() || middle.Emission != coro.EmitCoroutine {
				t.Fatalf("budget %d: chain exceeding local-storage budget must retain coroutine: %+v", budget, middle)
			}
			runCoroABITestPipeline(t, prog, pkg.Module())
			pkg.Module().Dispose()
			prog.Dispose()
		}
	}
}
