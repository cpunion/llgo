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
	"fmt"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// frozenSafeFixedArrayIndex consumes the per-instruction plan fact used to
// remove one redundant bounds helper. Re-running the shared proof here is only
// a consistency check against mutated SSA/frontend type projection; the plan
// fact is the sole authority for selecting unchecked code generation.
func (p *context) frozenSafeFixedArrayIndex(
	operation ssa.Instruction,
	collection, index ssa.Value,
) bool {
	if p == nil || operation == nil || collection == nil || index == nil ||
		p.compilation == nil || !p.compilation.EnableCoroEntryResolution ||
		p.compilation.CoroPlan == nil || p.emissionUniverse == nil {
		return false
	}
	if p.goFn == nil || operation.Parent() != p.goFn {
		panic(fmt.Errorf("safe fixed-array index escaped its exact SSA owner"))
	}
	plannedBound, planned := p.compilation.CoroPlan.ExactSafeFixedArrayIndex(operation)
	actualBound, fixedArray := emissionFixedArrayBound(p, collection)
	recomputed := fixedArray && coro.ProveSSAExactSafeFixedArrayIndex(
		operation.Parent(), index, actualBound, operation,
	)
	if planned != recomputed || planned && plannedBound != actualBound {
		panic(fmt.Errorf(
			"safe fixed-array index in %q disagrees between frozen plan and frontend proof (planned=%t bound=%d recomputed=%t bound=%d)",
			p.goFn.Name(), planned, plannedBound, recomputed, actualBound,
		))
	}
	return planned
}
