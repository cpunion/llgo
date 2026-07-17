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

package coro

// TakeRunDecisionWords is the scalar compiler-ABI adapter for
// TakeRunDecision. ParkTicket and OperationResultLease remain private Go
// values; only their explicit uint32 identity words cross into the runtime
// wrapper. A zero epoch/generation pair denotes a non-park resume point.
//
// Failure has the same exact-take semantics as TakeRunDecision: a stale ticket
// or wrong G does not consume the retained decision, while a duplicate take is
// rejected after the first successful take.
func TakeRunDecisionWords(
	g *G,
	expectedEpoch, expectedGeneration uint32,
) (
	outcome, caseID, taskKind, operationSourceSlot, operationGeneration uint32,
	ok bool,
) {
	if expectedGeneration == 0 && expectedEpoch != 0 {
		return 0, 0, 0, 0, 0, false
	}
	expected := ParkTicket{epoch: expectedEpoch, generation: expectedGeneration}
	parkOutcome, selectedCase, lease, task, taken := TakeRunDecision(g, expected)
	if !taken {
		return 0, 0, 0, 0, 0, false
	}
	var operation OperationID
	if lease != (OperationResultLease{}) {
		var valid bool
		operation, valid = lease.ID()
		if !valid {
			return 0, 0, 0, 0, 0, false
		}
	}
	return uint32(parkOutcome), selectedCase, uint32(task), operation.SourceSlot, operation.Generation, true
}
