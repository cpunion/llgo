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

// CompletionSinkOperationCapacity is the Phase 23 host profile: it covers the
// current wait+timer static catalog with headroom for the first manual source.
// A cancellation is merged into an existing fact for the same wait-set;
// cancel-only wait-sets use the second half. SourceSet and command-queue binding
// must prove both admission bounds. Embedded/baremetal and future multi-P
// profiles must generate these capacities with their source catalog rather
// than silently inheriting this memory footprint.
const (
	CompletionSinkOperationCapacity  = WaitRegistrationCapacity + TimerRegistrationCapacity + 64
	CompletionSinkCancelOnlyCapacity = CompletionSinkOperationCapacity
	CompletionSinkCapacity           = CompletionSinkOperationCapacity + CompletionSinkCancelOnlyCapacity
)

type completionFactKind uint8

const (
	completionFactInvalid completionFactKind = iota
	completionFactOperation
	completionFactCancel
)

type completionFact struct {
	kind      completionFactKind
	operation *OperationRecord
	park      *ParkState
	ticket    ParkTicket
	cancel    bool
}

type completionSinkPhase uint8

const (
	completionSinkIdle completionSinkPhase = iota
	completionSinkCollecting
	completionSinkSealed
	completionSinkResolved
)

type CompletionCollectResult uint8

const (
	CompletionCollectInvalid CompletionCollectResult = iota
	CompletionCollectAccepted
	CompletionCollectDuplicate
	CompletionCollectLost
	CompletionCollectOverflow
)

type CompletionResolution struct {
	WaitSets  uint32
	Completed uint32
	Canceled  uint32
	Winners   uint32
	Losers    uint32
}

// CompletionSink is P-owned scratch storage. Sources only append durable
// facts; no ParkState or OperationRecord is mutated until the complete batch
// has been sealed and validated. Overflow makes Resolve fail without partial
// decisions, so facts remain replayable from source-owned records.
type CompletionSink struct {
	phase           completionSinkPhase
	count           uint32
	operationFacts  uint32
	cancelOnlyFacts uint32
	overflow        bool
	facts           [CompletionSinkCapacity]completionFact
}

func validCompletionSinkCounts(sink *CompletionSink) bool {
	return sink != nil && sink.count <= CompletionSinkCapacity &&
		sink.operationFacts <= CompletionSinkOperationCapacity && sink.cancelOnlyFacts <= CompletionSinkCancelOnlyCapacity &&
		sink.count == sink.operationFacts+sink.cancelOnlyFacts
}

func BeginCompletionBatch(sink *CompletionSink) bool {
	if !validCompletionSinkCounts(sink) ||
		(sink.phase != completionSinkIdle && sink.phase != completionSinkResolved) ||
		(sink.phase == completionSinkIdle && (sink.count != 0 || sink.overflow)) ||
		(sink.phase == completionSinkResolved && (sink.count == 0 || sink.overflow)) {
		return false
	}
	for index := uint32(0); index < sink.count; index++ {
		sink.facts[index] = completionFact{}
	}
	sink.phase = completionSinkCollecting
	sink.count = 0
	sink.operationFacts = 0
	sink.cancelOnlyFacts = 0
	sink.overflow = false
	return true
}

func completionSinkHasOperation(sink *CompletionSink, record *OperationRecord) bool {
	for index := uint32(0); index < sink.count; index++ {
		if sink.facts[index].kind == completionFactOperation && sink.facts[index].operation == record {
			return true
		}
	}
	return false
}

func CollectOperationCompletion(sink *CompletionSink, record *OperationRecord, id OperationID) CompletionCollectResult {
	if !validCompletionSinkCounts(sink) || sink.phase != completionSinkCollecting || record == nil || !record.Matches(id) {
		return CompletionCollectInvalid
	}
	if sink.overflow {
		return CompletionCollectOverflow
	}
	if record.disposition != OperationDispositionPending {
		return CompletionCollectLost
	}
	if record.phase != operationActive || !record.completionPublished || record.link.park == nil || record.link.operation != record ||
		record.link.ticket == (ParkTicket{}) {
		return CompletionCollectInvalid
	}
	if completionSinkHasOperation(sink, record) {
		return CompletionCollectDuplicate
	}
	for index := uint32(0); index < sink.count; index++ {
		fact := &sink.facts[index]
		if fact.kind == completionFactCancel && fact.park == record.link.park && fact.ticket == record.link.ticket {
			if sink.operationFacts == CompletionSinkOperationCapacity || sink.cancelOnlyFacts == 0 {
				sink.overflow = true
				return CompletionCollectOverflow
			}
			fact.kind = completionFactOperation
			fact.operation = record
			sink.operationFacts++
			sink.cancelOnlyFacts--
			return CompletionCollectAccepted
		}
	}
	if sink.count == CompletionSinkCapacity || sink.operationFacts == CompletionSinkOperationCapacity {
		sink.overflow = true
		return CompletionCollectOverflow
	}
	sink.facts[sink.count] = completionFact{
		kind:      completionFactOperation,
		operation: record,
		park:      record.link.park,
		ticket:    record.link.ticket,
	}
	sink.count++
	sink.operationFacts++
	return CompletionCollectAccepted
}

func CollectParkCancellation(sink *CompletionSink, state *ParkState, ticket ParkTicket) CompletionCollectResult {
	if !validCompletionSinkCounts(sink) || sink.phase != completionSinkCollecting || !validParkState(state) ||
		state.phase != parkParked || ticket != state.ticket || state.cancelKind == ParkCancelNone {
		return CompletionCollectInvalid
	}
	if sink.overflow {
		return CompletionCollectOverflow
	}
	for index := uint32(0); index < sink.count; index++ {
		fact := &sink.facts[index]
		if fact.park == state && fact.ticket == ticket {
			if fact.cancel {
				return CompletionCollectDuplicate
			}
			fact.cancel = true
			return CompletionCollectAccepted
		}
	}
	if sink.count == CompletionSinkCapacity || sink.cancelOnlyFacts == CompletionSinkCancelOnlyCapacity {
		sink.overflow = true
		return CompletionCollectOverflow
	}
	sink.facts[sink.count] = completionFact{kind: completionFactCancel, park: state, ticket: ticket, cancel: true}
	sink.count++
	sink.cancelOnlyFacts++
	return CompletionCollectAccepted
}

func SealCompletionBatch(sink *CompletionSink) bool {
	if !validCompletionSinkCounts(sink) || sink.phase != completionSinkCollecting || sink.overflow || sink.count == 0 {
		return false
	}
	sink.phase = completionSinkSealed
	return true
}

func validCompletionFact(fact *completionFact) bool {
	if fact == nil || fact.park == nil || fact.ticket == (ParkTicket{}) || !validParkState(fact.park) ||
		fact.park.phase != parkParked || fact.ticket != fact.park.ticket ||
		(fact.cancel && fact.park.cancelKind == ParkCancelNone) {
		return false
	}
	switch fact.kind {
	case completionFactOperation:
		return fact.operation != nil && fact.operation.phase == operationActive &&
			fact.operation.disposition == OperationDispositionPending && fact.operation.completionPublished &&
			fact.operation.link.park == fact.park && fact.operation.link.ticket == fact.ticket
	case completionFactCancel:
		return fact.operation == nil && fact.cancel && fact.park.cancelKind != ParkCancelNone
	default:
		return false
	}
}

func sameCompletionWaitSet(left, right *completionFact) bool {
	return left != nil && right != nil && left.park == right.park && left.ticket == right.ticket
}

func completionSinkContainsOperation(sink *CompletionSink, state *ParkState, ticket ParkTicket, record *OperationRecord) bool {
	for index := uint32(0); index < sink.count; index++ {
		fact := &sink.facts[index]
		if fact.kind == completionFactOperation && fact.park == state && fact.ticket == ticket && fact.operation == record {
			return true
		}
	}
	return false
}

func completionSinkContainsCancel(sink *CompletionSink, state *ParkState, ticket ParkTicket) bool {
	for index := uint32(0); index < sink.count; index++ {
		fact := &sink.facts[index]
		if fact.cancel && fact.park == state && fact.ticket == ticket {
			return true
		}
	}
	return false
}

func ResolveCompletionBatch(sink *CompletionSink) (resolution CompletionResolution, ok bool) {
	if !validCompletionSinkCounts(sink) || sink.phase != completionSinkSealed || sink.overflow || sink.count == 0 {
		return CompletionResolution{}, false
	}
	// Validate the entire snapshot before the first logical decision. This is
	// what makes an invalid or incomplete batch fail without partial resolve.
	for index := uint32(0); index < sink.count; index++ {
		if !validCompletionFact(&sink.facts[index]) {
			return CompletionResolution{}, false
		}
		for prior := uint32(0); prior < index; prior++ {
			if sink.facts[index].kind == completionFactOperation && sink.facts[prior].kind == completionFactOperation &&
				sink.facts[index].operation == sink.facts[prior].operation {
				return CompletionResolution{}, false
			}
			if sink.facts[index].cancel && sink.facts[prior].cancel && sameCompletionWaitSet(&sink.facts[index], &sink.facts[prior]) {
				return CompletionResolution{}, false
			}
		}
	}
	// A source-set snapshot is indivisible. Every completion already published
	// in an attached operation, and every sticky cancel request, must appear in
	// this batch before rank-based winner selection starts.
	for index := uint32(0); index < sink.count; index++ {
		fact := &sink.facts[index]
		firstForSet := true
		for prior := uint32(0); prior < index; prior++ {
			if sameCompletionWaitSet(fact, &sink.facts[prior]) {
				firstForSet = false
				break
			}
		}
		if !firstForSet {
			continue
		}
		for link := fact.park.head; link != nil; link = link.next {
			if link.operation.completionPublished && !completionSinkContainsOperation(sink, fact.park, fact.ticket, link.operation) {
				return CompletionResolution{}, false
			}
		}
		if fact.park.cancelKind != ParkCancelNone && !completionSinkContainsCancel(sink, fact.park, fact.ticket) {
			return CompletionResolution{}, false
		}
	}

	for index := uint32(0); index < sink.count; index++ {
		first := &sink.facts[index]
		alreadyResolved := false
		for prior := uint32(0); prior < index; prior++ {
			if sameCompletionWaitSet(first, &sink.facts[prior]) {
				alreadyResolved = true
				break
			}
		}
		if alreadyResolved {
			continue
		}

		var winner *OperationRecord
		for candidateIndex := index; candidateIndex < sink.count; candidateIndex++ {
			candidate := &sink.facts[candidateIndex]
			if !sameCompletionWaitSet(first, candidate) || candidate.kind != completionFactOperation {
				continue
			}
			if winner == nil || candidate.operation.link.rank < winner.link.rank {
				winner = candidate.operation
			}
		}
		if first.park.cancelKind == ParkCancelTaskAbort || first.park.cancelKind == ParkCancelShutdown {
			winner = nil
		}
		if !resolveParkSet(first.park, first.ticket, winner) {
			return CompletionResolution{}, false
		}
		resolution.WaitSets++
		if winner == nil {
			resolution.Canceled++
		} else {
			resolution.Completed++
			resolution.Winners++
		}
		for link := first.park.head; link != nil; link = link.next {
			if link.operation != winner {
				resolution.Losers++
			}
		}
	}
	sink.phase = completionSinkResolved
	return resolution, true
}

// ResetCompletionBatch releases only P-owned scratch facts. It is safe after
// overflow or prevalidation failure because neither case mutates a wait-set;
// after successful resolution it does not undo the already durable decision.
func ResetCompletionBatch(sink *CompletionSink) bool {
	if !validCompletionSinkCounts(sink) || sink.phase == completionSinkIdle {
		return false
	}
	for index := uint32(0); index < sink.count; index++ {
		sink.facts[index] = completionFact{}
	}
	*sink = CompletionSink{}
	return true
}
