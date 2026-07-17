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

package runtime

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

type coroRunDecisionOutputModeV1 uint8

const (
	coroRunDecisionOutputInvalidV1 coroRunDecisionOutputModeV1 = iota
	coroRunDecisionOutputNormalOnlyV1
	coroRunDecisionOutputWordsV1
)

func coroRunDecisionOutputModeOfV1(
	g unsafe.Pointer,
	outcome, caseID, taskKind, operationSourceSlot, operationGeneration *uint32,
) coroRunDecisionOutputModeV1 {
	if g == nil {
		return coroRunDecisionOutputInvalidV1
	}
	allNil := outcome == nil && caseID == nil && taskKind == nil && operationSourceSlot == nil && operationGeneration == nil
	if allNil {
		return coroRunDecisionOutputNormalOnlyV1
	}
	if outcome == nil || caseID == nil || taskKind == nil || operationSourceSlot == nil || operationGeneration == nil {
		return coroRunDecisionOutputInvalidV1
	}
	words := [5]*uint32{outcome, caseID, taskKind, operationSourceSlot, operationGeneration}
	for index, word := range words {
		if unsafe.Pointer(word) == g {
			return coroRunDecisionOutputInvalidV1
		}
		for prior := 0; prior < index; prior++ {
			if word == words[prior] {
				return coroRunDecisionOutputInvalidV1
			}
		}
	}
	return coroRunDecisionOutputWordsV1
}

func normalCoroRunDecisionWordsV1(
	outcome, caseID, taskKind, operationSourceSlot, operationGeneration uint32,
	ok bool,
) bool {
	return ok && outcome == 0 && caseID == 0 && taskKind == 0 && operationSourceSlot == 0 && operationGeneration == 0
}

// __llgo_coro_run_decision_take_v1 is the compiler resume-prologue gate. Its
// ABI contains only the current G pointer, the expected logical ticket's two
// uint32 words, and either five distinct uint32 output addresses or five nil
// addresses selecting the normal-only zero-ticket gate. No Go aggregate,
// ParkTicket, result lease, operation record, or LLVM coroutine handle crosses
// this boundary. The normal-only form is used until compiler cleanup/select
// lowering can consume non-normal decisions; observing one aborts rather than
// silently continuing user code.
//
// A stale ticket, wrong G, duplicate take, or malformed output tuple is an
// unrecoverable compiler/runtime protocol violation. In words mode, outputs
// are cleared before taking the decision so a non-returning failure cannot
// expose a partially initialized result to a broken exit shim.
//
//export __llgo_coro_run_decision_take_v1
func __llgo_coro_run_decision_take_v1(
	g unsafe.Pointer,
	expectedEpoch, expectedGeneration uint32,
	outcome, caseID, taskKind, operationSourceSlot, operationGeneration *uint32,
) {
	mode := coroRunDecisionOutputModeOfV1(g, outcome, caseID, taskKind, operationSourceSlot, operationGeneration)
	if mode == coroRunDecisionOutputInvalidV1 ||
		mode == coroRunDecisionOutputNormalOnlyV1 && (expectedEpoch != 0 || expectedGeneration != 0) {
		coroRuntimeAbort("invalid coroutine run-decision output")
		return
	}
	if mode == coroRunDecisionOutputNormalOnlyV1 {
		decisionOutcome, selectedCase, cancelKind, sourceSlot, generation, ok := coro.TakeRunDecisionWords((*coro.G)(g), 0, 0)
		if !normalCoroRunDecisionWordsV1(decisionOutcome, selectedCase, cancelKind, sourceSlot, generation, ok) {
			coroRuntimeAbort("unsupported non-normal coroutine run decision")
		}
		return
	}
	*outcome = 0
	*caseID = 0
	*taskKind = 0
	*operationSourceSlot = 0
	*operationGeneration = 0
	decisionOutcome, selectedCase, cancelKind, sourceSlot, generation, ok := coro.TakeRunDecisionWords(
		(*coro.G)(g), expectedEpoch, expectedGeneration,
	)
	if !ok {
		coroRuntimeAbort("invalid coroutine run-decision take")
		return
	}
	*outcome = decisionOutcome
	*caseID = selectedCase
	*taskKind = cancelKind
	*operationSourceSlot = sourceSlot
	*operationGeneration = generation
}
