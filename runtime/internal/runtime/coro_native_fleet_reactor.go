//go:build (darwin || linux) && !baremetal && ((!llgo && coro_native_fleet_test) || (llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && !coro_runtime_adapter_test))

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
	"github.com/goplus/llgo/runtime/internal/coro"
	"github.com/goplus/llgo/runtime/internal/corodoorbell"
)

const coroNativeFleetPollCapacityV1 = corodoorbell.PollSetCapacity - 1

var _ [coroNativeFleetPollCapacityV1 - coro.PollOperationPageCapacity]byte

// coroNativeFleetPollSetV1 is stable target storage, not coroutine-frame
// storage. One exact physical owner uses its domain's entry while that domain
// is sleeping; callbacks retain only OperationID and never a pointer here.
type coroNativeFleetPollSetV1 struct {
	entries    [corodoorbell.PollSetCapacity]corodoorbell.PollFD
	operations [coroNativeFleetPollCapacityV1]coro.OperationID
}

var coroNativeFleetPollSetsV1 [coroNativeFleetDomainCapacityV1]coroNativeFleetPollSetV1

type coroNativeFleetArmedWaitV1 struct {
	Handle      coro.ExecutorFleetHandle
	Epoch       uint32
	Count       uint32
	Deadline    int64
	HasDeadline bool
}

type coroNativeFleetPhysicalWaitV1 struct {
	handle      coro.ExecutorFleetHandle
	count       uint32
	deadline    int64
	hasDeadline bool
}

type coroNativeFleetPhysicalWaitModeV1 uint8

const (
	coroNativeFleetPhysicalWaitIdleV1 coroNativeFleetPhysicalWaitModeV1 = iota + 1
	coroNativeFleetPhysicalWaitActiveV1
)

type coroNativeFleetPhysicalWaitResultV1 uint8

const (
	coroNativeFleetPhysicalWaitInvalidV1 coroNativeFleetPhysicalWaitResultV1 = iota
	coroNativeFleetPhysicalWaitRetryV1
	coroNativeFleetPhysicalWaitWakeV1
	coroNativeFleetPhysicalWaitDeadlineV1
)

type coroNativeFleetWaitPassResultV1 uint8

const (
	coroNativeFleetWaitPassInvalidV1 coroNativeFleetWaitPassResultV1 = iota
	coroNativeFleetWaitPassRetryV1
	coroNativeFleetWaitPassWakeV1
)

func coroNativeFleetWaitStorageV1(
	handle coro.ExecutorFleetHandle,
) (*coroNativeFleetDomainV1, *coroNativeFleetPollSetV1, bool) {
	domain, ok := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !ok || handle.Route == 0 || handle.Route > coroNativeFleetV1State.domainCount {
		return nil, nil, false
	}
	return domain, &coroNativeFleetPollSetsV1[handle.Route-1], true
}

// coroNativeFleetBuildPhysicalWaitV1 is the sole route-local poll-set
// materializer. Logical idle owners and active replacement owners apply their
// own state gates before calling it, but share the exact doorbell, poll
// snapshot, deadline and stable-storage construction.
func coroNativeFleetBuildPhysicalWaitV1(
	handle coro.ExecutorFleetHandle,
	deadline int64,
	hasDeadline bool,
) (coroNativeFleetPhysicalWaitV1, bool) {
	domain, storage, ok := coroNativeFleetWaitStorageV1(handle)
	if !ok || deadline < 0 || !hasDeadline && deadline != 0 {
		return coroNativeFleetPhysicalWaitV1{}, false
	}
	doorbellFD, ok := domain.doorbell.ReadFD()
	if !ok {
		return coroNativeFleetPhysicalWaitV1{}, false
	}
	for index := range storage.entries {
		storage.entries[index] = corodoorbell.PollFD{}
	}
	for index := range storage.operations {
		storage.operations[index] = coro.OperationID{}
	}
	storage.entries[0] = corodoorbell.PollFD{
		FD:     doorbellFD,
		Events: corodoorbell.PollRead,
	}
	count := uint32(1)
	poll, driver := domain.pollOwnerV1(), domain.driverOwnerV1()
	if poll == nil || driver == nil {
		return coroNativeFleetPhysicalWaitV1{}, false
	}
	configured := coro.PollOperationConfiguredCapacity(poll)
	if configured == 0 || configured > coroNativeFleetPollCapacityV1 {
		return coroNativeFleetPhysicalWaitV1{}, false
	}
	scanLimit, scanOK := coro.PollOperationScanLimit(poll)
	if !scanOK || scanLimit > configured {
		return coroNativeFleetPhysicalWaitV1{}, false
	}
	for index := uint32(0); index < scanLimit; index++ {
		snapshot, active, snapshotOK := coro.SnapshotExecutorPollOperation(driver, index)
		if !snapshotOK {
			return coroNativeFleetPhysicalWaitV1{}, false
		}
		if !active {
			continue
		}
		if !snapshot.ID.Valid() || snapshot.ID.Source() != coro.OperationSourcePoll ||
			uint32(snapshot.ID.Route()) != handle.Route ||
			snapshot.Deadline > 0 &&
				(!hasDeadline || snapshot.Deadline < deadline) {
			return coroNativeFleetPhysicalWaitV1{}, false
		}
		events := int16(0)
		switch snapshot.Interest {
		case coro.PollInterestRead:
			events = corodoorbell.PollRead
		case coro.PollInterestWrite:
			events = corodoorbell.PollWrite
		default:
			return coroNativeFleetPhysicalWaitV1{}, false
		}
		storage.entries[count] = corodoorbell.PollFD{
			FD:     snapshot.FD,
			Events: events,
		}
		storage.operations[count-1] = snapshot.ID
		count++
	}
	return coroNativeFleetPhysicalWaitV1{
		handle:      handle,
		count:       count,
		deadline:    deadline,
		hasDeadline: hasDeadline,
	}, true
}

// coroNativeFleetArmOwnerWaitV1 rebuilds one route-local physical poll set
// after the common idle transaction committed and released its logical owner
// epoch. An event racing this snapshot either makes its source slot inactive
// or leaves a retained doorbell wake; neither path loses the completion.
func coroNativeFleetArmOwnerWaitV1(
	handle coro.ExecutorFleetHandle,
	plan coroNativeFleetOwnerWaitPlanV1,
) (coroNativeFleetArmedWaitV1, bool) {
	domain, _, ok := coroNativeFleetWaitStorageV1(handle)
	if !ok || !plan.Armed || plan.Epoch == 0 || domain.ownerEpoch != 0 ||
		domain.nextOwnerEpoch != plan.Epoch || plan.Deadline < 0 ||
		!plan.HasDeadline && plan.Deadline != 0 {
		return coroNativeFleetArmedWaitV1{}, false
	}
	physical, built := coroNativeFleetBuildPhysicalWaitV1(
		handle,
		plan.Deadline,
		plan.HasDeadline,
	)
	if !built || domain.ownerEpoch != 0 || domain.nextOwnerEpoch != plan.Epoch {
		return coroNativeFleetArmedWaitV1{}, false
	}
	return coroNativeFleetArmedWaitV1{
		Handle:      handle,
		Epoch:       plan.Epoch,
		Count:       physical.count,
		Deadline:    physical.deadline,
		HasDeadline: physical.hasDeadline,
	}, true
}

func coroNativeFleetPhysicalPollPostAcceptedV1(
	result coro.OperationRouteIngressResult,
	mode coroNativeFleetPhysicalWaitModeV1,
) bool {
	switch result.Route {
	case coro.OperationRoutePosted:
		switch mode {
		case coroNativeFleetPhysicalWaitIdleV1:
			return result.Executor == coro.ExecutorRequestIdleWake ||
				result.Executor == coro.ExecutorRequestCoalesced
		case coroNativeFleetPhysicalWaitActiveV1:
			return result.Executor == coro.ExecutorRequestPublished ||
				result.Executor == coro.ExecutorRequestCoalesced
		default:
			return false
		}
	case coro.OperationRoutePostCoalesced:
		return result.Executor == coro.ExecutorRequestInvalid
	default:
		return false
	}
}

// coroNativeFleetWaitPhysicalPassAtV1 is the sole native doorbell/poll wait
// primitive. It knows no owner epoch or replacement baton; callers prove that
// ownership before entry and interpret Deadline according to their logical
// driver state.
func coroNativeFleetWaitPhysicalPassAtV1(
	wait coroNativeFleetPhysicalWaitV1,
	now int64,
	mode coroNativeFleetPhysicalWaitModeV1,
) coroNativeFleetPhysicalWaitResultV1 {
	domain, storage, ok := coroNativeFleetWaitStorageV1(wait.handle)
	if !ok || wait.count == 0 || wait.count > corodoorbell.PollSetCapacity ||
		now < 0 || wait.deadline < 0 ||
		!wait.hasDeadline && wait.deadline != 0 ||
		mode != coroNativeFleetPhysicalWaitIdleV1 &&
			mode != coroNativeFleetPhysicalWaitActiveV1 {
		return coroNativeFleetPhysicalWaitInvalidV1
	}
	if retained, retainedOK := domain.doorbell.ConsumeRetainedWake(); !retainedOK {
		return coroNativeFleetPhysicalWaitInvalidV1
	} else if retained {
		return coroNativeFleetPhysicalWaitWakeV1
	}

	timeoutMS := corodoorbell.PollFaultContainmentMilliseconds
	if wait.hasDeadline {
		var reached bool
		timeoutMS, reached, ok = corodoorbell.DeadlinePollTimeout(now, wait.deadline)
		if !ok {
			return coroNativeFleetPhysicalWaitInvalidV1
		}
		if reached {
			return coroNativeFleetPhysicalWaitDeadlineV1
		}
	}
	for index := uint32(0); index < wait.count; index++ {
		storage.entries[index].Revents = 0
	}
	ready, errno := corodoorbell.WaitPollSet(&storage.entries[0], wait.count, timeoutMS)
	if ready < 0 {
		if corodoorbell.PollInterrupted(errno) {
			return coroNativeFleetPhysicalWaitRetryV1
		}
		return coroNativeFleetPhysicalWaitInvalidV1
	}
	if ready == 0 {
		return coroNativeFleetPhysicalWaitRetryV1
	}

	woke := false
	if storage.entries[0].Revents&corodoorbell.PollBadFD != 0 {
		return coroNativeFleetPhysicalWaitInvalidV1
	}
	if storage.entries[0].Revents&
		(corodoorbell.PollRead|corodoorbell.PollError|corodoorbell.PollHangup) != 0 {
		if !domain.doorbell.Drain() {
			return coroNativeFleetPhysicalWaitInvalidV1
		}
		woke = true
	} else if storage.entries[0].Revents != 0 {
		return coroNativeFleetPhysicalWaitInvalidV1
	}
	for entry := uint32(1); entry < wait.count; entry++ {
		if storage.entries[entry].Revents == 0 {
			continue
		}
		if !coroNativeFleetPhysicalPollPostAcceptedV1(
			coroNativeFleetPostPollV1(
				storage.operations[entry-1],
				coro.PollOperationReady,
			),
			mode,
		) {
			return coroNativeFleetPhysicalWaitInvalidV1
		}
		woke = true
	}
	if !woke {
		return coroNativeFleetPhysicalWaitInvalidV1
	}
	return coroNativeFleetPhysicalWaitWakeV1
}

// coroNativeFleetWaitOwnerPassAtV1 performs at most one fault-containment
// bounded poll call. Retry requires the physical owner to take a fresh
// monotonic sample and re-enter, giving the coordinator a stop-check boundary
// after EINTR and ordinary timeouts. Wake means it may reacquire a new logical
// owner epoch and run the common WakeExecutorAt transition. Source service
// then resumes through the same bounded reducer used before sleep.
func coroNativeFleetWaitOwnerPassAtV1(
	wait coroNativeFleetArmedWaitV1,
	now int64,
) coroNativeFleetWaitPassResultV1 {
	domain, _, ok := coroNativeFleetWaitStorageV1(wait.Handle)
	if !ok || wait.Epoch == 0 || wait.Count == 0 || wait.Count > corodoorbell.PollSetCapacity ||
		domain.ownerEpoch != 0 || domain.nextOwnerEpoch != wait.Epoch || now < 0 ||
		wait.Deadline < 0 || !wait.HasDeadline && wait.Deadline != 0 {
		return coroNativeFleetWaitPassInvalidV1
	}
	physical := coroNativeFleetPhysicalWaitV1{
		handle:      wait.Handle,
		count:       wait.Count,
		deadline:    wait.Deadline,
		hasDeadline: wait.HasDeadline,
	}
	switch coroNativeFleetWaitPhysicalPassAtV1(
		physical,
		now,
		coroNativeFleetPhysicalWaitIdleV1,
	) {
	case coroNativeFleetPhysicalWaitRetryV1:
		return coroNativeFleetWaitPassRetryV1
	case coroNativeFleetPhysicalWaitWakeV1,
		coroNativeFleetPhysicalWaitDeadlineV1:
		return coroNativeFleetWaitPassWakeV1
	default:
		return coroNativeFleetWaitPassInvalidV1
	}
}
