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

// ownerLocalCompletionCursor is the scheduler-owned fast lane for a physical
// completion produced by the G which currently owns this exact P. The producer
// publishes the typed source result while it already owns the source's runtime
// synchronization domain, then appends only the affected logical wait here.
// The scheduler resolves it after llvm.coro.resume returns; it never resumes a
// peer recursively or performs typed cleanup inside the producer's lock.
//
// Cross-P, callback, timer, poller, and foreign-thread producers cannot enter
// this queue. They retain the durable source pending + executor request +
// A/ack/B protocol. workNext is sufficient because a locally queued record was
// required to be owner-idle before publication.
type ownerLocalCompletionCursor struct {
	head    *WaitSetRecord
	tail    *WaitSetRecord
	resolve publishedEpochResolveCursor
}

func validOwnerLocalCompletionHeader(local *ownerLocalCompletionCursor, p *P) bool {
	if local == nil || p == nil || (local.head == nil) != (local.tail == nil) ||
		local.tail != nil && local.tail.workNext != nil {
		return false
	}
	if local.resolve.phase == publishedEpochResolveIdle {
		if local.resolve != (publishedEpochResolveCursor{}) {
			return false
		}
	} else if local.head != nil || !validPublishedEpochResolveCursor(&local.resolve, p) {
		return false
	}
	return local.head == nil || local.head.work == waitSetWorkQueued &&
		validActiveWaitSetRecordFast(p, local.head)
}

func validOwnerLocalCompletion(local *ownerLocalCompletionCursor, p *P) bool {
	if !validOwnerLocalCompletionHeader(local, p) {
		return false
	}
	var tail *WaitSetRecord
	for slow, fast := local.head, local.head; fast != nil && fast.workNext != nil; {
		slow = slow.workNext
		fast = fast.workNext.workNext
		if slow == fast {
			return false
		}
	}
	for record := local.head; record != nil; record = record.workNext {
		if record.work != waitSetWorkQueued || !validActiveWaitSetRecordFast(p, record) {
			return false
		}
		tail = record
	}
	return tail == local.tail
}

func emptyOwnerLocalCompletion(local *ownerLocalCompletionCursor) bool {
	return local != nil && *local == (ownerLocalCompletionCursor{})
}

func canAppendOwnerLocalCompletion(driver *ExecutorDriver, record *WaitSetRecord) bool {
	return driver != nil && validRunningExecutorOwner(driver) &&
		validOwnerLocalCompletionHeader(&driver.local, driver.p) &&
		driver.local.resolve == (publishedEpochResolveCursor{}) &&
		record != nil && record.work == waitSetWorkIdle && record.workNext == nil &&
		validActiveWaitSetRecordFast(driver.p, record)
}

func appendOwnerLocalCompletionUnchecked(driver *ExecutorDriver, record *WaitSetRecord) {
	record.work = waitSetWorkQueued
	if driver.local.tail == nil {
		driver.local.head = record
	} else {
		driver.local.tail.workNext = record
	}
	driver.local.tail = record
}

func ownerLocalCompletionPending(driver *ExecutorDriver) bool {
	// Include a lone tail so malformed selected state enters the exact cold
	// validator and fails closed instead of being skipped by the hot selector.
	return driver != nil && (driver.local.head != nil || driver.local.tail != nil ||
		driver.local.resolve.phase != publishedEpochResolveIdle)
}

func initializeOwnerLocalCompletionResolution(
	driver *ExecutorDriver,
	step *publishedEpochResolveStep,
) bool {
	if driver == nil || step == nil || driver.local.resolve != (publishedEpochResolveCursor{}) ||
		!validOwnerLocalCompletionHeader(&driver.local, driver.p) || driver.local.head == nil ||
		!validAffectedWaitQueueHeader(driver.p) {
		return false
	}
	head, tail := driver.local.head, driver.local.tail
	driver.local.resolve.batchTail = tail
	if !startPublishedEpochWait(&driver.sources, &driver.local.resolve, head) {
		driver.local.resolve.batchTail = nil
		return false
	}
	driver.local.head, driver.local.tail = nil, nil
	return true
}

// resolveOwnerLocalCompletionStep performs one ordinary published-epoch
// resolution reduction, but starts from the exact owner-local FIFO instead of
// rescanning every source and acknowledging a doorbell which was never needed.
// Source-specific ApplyOne and typed materialization remain identical to the
// external path.
func resolveOwnerLocalCompletionStep(
	driver *ExecutorDriver,
	step *publishedEpochResolveStep,
) (ok bool) {
	if driver == nil || step == nil {
		return false
	}
	*step = publishedEpochResolveStep{}
	p, cursor := driver.p, &driver.local.resolve
	if p == nil || !validReadyQueueHeader(p) || !validParkWaitQueueHeader(p) ||
		!validAffectedWaitQueueHeader(p) || !validOwnerLocalCompletionHeader(&driver.local, p) {
		return false
	}
	if cursor.phase == publishedEpochResolveIdle {
		if !initializeOwnerLocalCompletionResolution(driver, step) {
			return false
		}
	} else if !validPublishedEpochResolveCursor(cursor, p) {
		return false
	}

	switch cursor.phase {
	case publishedEpochResolveDiscover:
		ok = resolvePublishedEpochDiscoverStep(&driver.sources, p, cursor, step)
	case publishedEpochResolvePark:
		ok = resolvePublishedEpochParkStep(&driver.sources, p, cursor, step)
	case publishedEpochResolveApply:
		ok = resolvePublishedEpochApplyStep(&driver.sources, p, cursor, step)
	case publishedEpochResolveFinish:
		ok = resolvePublishedEpochFinishStep(cursor, step)
	case publishedEpochResolvePromote:
		ok = resolvePublishedEpochPromoteStep(&driver.sources, p, cursor, step)
	default:
		ok = false
	}
	return ok
}
