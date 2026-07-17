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

// CompletionResolution is intentionally a value summary rather than an event
// buffer. Source-owned OperationRecord storage retains every completion as a
// sticky fact until the owner P resolves the corresponding logical park.
// WaitSets is one for every valid snapshot examined, including one that is
// still pending; Completed+Canceled says whether that snapshot was resolved.
type CompletionResolution struct {
	WaitSets  uint32
	Completed uint32
	Canceled  uint32
	Winners   uint32
	Losers    uint32
}

// ResolveParkSnapshot resolves one logical wait-set after the executor has
// completely drained every source in its SourceSet. No per-P fact array is
// needed: completionPublished and cancelKind are the durable snapshot.
//
// A valid snapshot without a completion or cancellation returns
// {WaitSets: 1}, true and leaves the park untouched. Ordinary operation
// cancellation still loses to a completion published in the same complete
// source snapshot. Task abort and shutdown suppress every completion.
//
// Calling this function before a complete SourceSet drain is a caller error:
// the resolver deliberately has no second source-specific bookkeeping layer
// with which to detect a partial drain.
func ResolveParkSnapshot(state *ParkState, ticket ParkTicket) (resolution CompletionResolution, ok bool) {
	if !validParkState(state) || state.phase != parkParked || ticket != state.ticket {
		return CompletionResolution{}, false
	}
	resolution.WaitSets = 1

	var winner *OperationRecord
	for link := state.head; link != nil; link = link.next {
		if !link.operation.completionPublished {
			continue
		}
		if winner == nil || link.rank < winner.link.rank {
			winner = link.operation
		}
	}
	if state.cancelKind == ParkCancelTaskAbort || state.cancelKind == ParkCancelShutdown {
		winner = nil
	}
	if winner == nil && state.cancelKind == ParkCancelNone {
		return resolution, true
	}
	if !resolveParkSet(state, ticket, winner) {
		return CompletionResolution{}, false
	}

	if winner == nil {
		resolution.Canceled = 1
		resolution.Losers = state.attached
	} else {
		resolution.Completed = 1
		resolution.Winners = 1
		resolution.Losers = state.attached - 1
	}
	return resolution, true
}
