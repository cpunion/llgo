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

// affectedOperationResolveResult is the owner-side result of visiting one
// source-local affected operation after the complete SourceSet quiet cut.
// AlreadyResolved is normal when two source-local entries belong to the same
// logical wait-set: the first entry resolves the complete sticky snapshot and
// changes every candidate disposition, so the later entry requires no central
// wait-set hash or per-G affected-list field.
type affectedOperationResolveResult uint8

const (
	affectedOperationResolveInvalid affectedOperationResolveResult = iota
	affectedOperationResolved
	affectedOperationAlreadyResolved
)

// resolveAffectedOperationAfterQuietCut resolves the logical wait-set reached
// through one exact source-owned OperationRecord. The source must call it only
// while enumerating entries retained by its publish pass and only after the
// executor has established the complete publish/ack/full-recheck quiet cut.
// It deliberately cannot infer that cross-source barrier from one record.
//
// Source-local enumeration must finish before source resolution is applied or
// detached. An attached terminal record in a detaching ParkState is the normal
// duplicate shape; a detached record or any other lifecycle mismatch fails
// closed. A successful first visit always resolves because an affected entry
// necessarily carries a sticky completion fact.
func resolveAffectedOperationAfterQuietCut(record *OperationRecord, id OperationID) (CompletionResolution, affectedOperationResolveResult) {
	if record == nil || !record.Matches(id) || record.phase != operationActive || !record.completionPublished ||
		record.link.park == nil || record.link.operation != record || !validParkTicket(record.link.ticket) {
		return CompletionResolution{}, affectedOperationResolveInvalid
	}

	state, ticket := record.link.park, record.link.ticket
	if !validParkState(state) || ticket != state.ticket {
		return CompletionResolution{}, affectedOperationResolveInvalid
	}
	if record.disposition != OperationDispositionPending {
		if state.phase != parkDetaching || state.outcome == ParkOutcomePending {
			return CompletionResolution{}, affectedOperationResolveInvalid
		}
		return CompletionResolution{}, affectedOperationAlreadyResolved
	}
	if state.phase != parkParked {
		return CompletionResolution{}, affectedOperationResolveInvalid
	}

	resolution, ok := ResolveParkSnapshot(state, ticket)
	if !ok || resolution.WaitSets != 1 || resolution.Completed+resolution.Canceled != 1 {
		return CompletionResolution{}, affectedOperationResolveInvalid
	}
	return resolution, affectedOperationResolved
}
