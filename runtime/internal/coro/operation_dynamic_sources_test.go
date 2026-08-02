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

import (
	"testing"
	"unsafe"
)

func TestOperationDynamicPageDirectoryIsSparseAndPublishesCompleteBlocks(t *testing.T) {
	pointerSize := unsafe.Sizeof(uintptr(0))
	if operationPageDirectoryBlockCapacity != 64 || operationPageDirectoryBlockCount != 8 ||
		unsafe.Sizeof(operationDynamicPageDirectory{}) > 9*pointerSize {
		t.Fatalf("dynamic directory layout: block=%d roots=%d size=%d pointer=%d",
			operationPageDirectoryBlockCapacity, operationPageDirectoryBlockCount,
			unsafe.Sizeof(operationDynamicPageDirectory{}), pointerSize)
	}

	directory := new(operationDynamicPageDirectory)
	first := unsafe.Pointer(new(byte))
	if directory.publish(first, nil) || directory.published() != 0 || directory.page(0) != nil {
		t.Fatal("missing first directory block partially published a page")
	}
	firstBlock := new(OperationPageDirectoryBlock)
	if !directory.publish(first, firstBlock) || directory.published() != 1 || directory.page(0) != first {
		t.Fatal("publish first sparse directory block")
	}
	for index := uint32(1); index < operationPageDirectoryBlockCapacity; index++ {
		page := unsafe.Pointer(new(byte))
		if directory.publish(page, new(OperationPageDirectoryBlock)) ||
			!directory.publish(page, nil) || directory.page(index) != page {
			t.Fatalf("publish first-block page %d", index)
		}
	}
	second := unsafe.Pointer(new(byte))
	if directory.publish(second, nil) || directory.published() != operationPageDirectoryBlockCapacity ||
		!directory.publish(second, new(OperationPageDirectoryBlock)) ||
		directory.page(operationPageDirectoryBlockCapacity) != second {
		t.Fatal("second directory block publication was not atomic")
	}
	if directory.publish(first, nil) {
		t.Fatal("dynamic directory accepted a duplicate page")
	}
}

func TestOperationSourcesAttachStablePagesWhileBound(t *testing.T) {
	if TimerRegistrationMaximumCapacity != 511*TimerRegistrationPageCapacity ||
		PollOperationMaximumCapacity != 511*PollOperationPageCapacity ||
		ManualOperationMaximumCapacity != 511*ManualOperationPageCapacity ||
		WorkerOperationMaximumCapacity != 511*WorkerOperationPageCapacity {
		t.Fatal("dynamic source maximum no longer matches OperationID encoding")
	}

	p := new(P)
	timers := new(TimerRegistrationTable)
	timerPage := new(TimerRegistrationPage)
	if !bindTimerRegistrationTableAtRoute(timers, p, RouteID(2)) ||
		!AttachTimerRegistrationPage(timers, p, timerPage, new(OperationPageDirectoryBlock)) ||
		TimerRegistrationConfiguredCapacity(timers) != 2*TimerRegistrationPageCapacity ||
		!CanReserveTimerV2(p, timers) ||
		AttachTimerRegistrationPage(timers, p, timerPage, nil) {
		t.Fatalf("attach timer page: capacity=%d", TimerRegistrationConfiguredCapacity(timers))
	}
	if slot, ok := timerRegistrationSlotAt(timers, TimerRegistrationPageCapacity); !ok ||
		slot != &timerPage.slots[0] {
		t.Fatal("dynamic timer slot address changed")
	}
	if !unbindTimerRegistrationTable(timers, p) || !timers.CanRelease() ||
		ConfigureTimerRegistrationPages(timers, make([]TimerRegistrationPage, 1)) {
		t.Fatal("dynamic timer catalog allowed static slot remapping")
	}

	poll := new(PollOperationSource)
	pollPage := new(PollOperationPage)
	if !BindPollOperationSourceAtRoute(poll, p, RouteID(3)) ||
		!AttachPollOperationPage(poll, p, pollPage, new(OperationPageDirectoryBlock)) ||
		PollOperationConfiguredCapacity(poll) != 2*PollOperationPageCapacity ||
		!CanReservePollOperationV2(p, poll) ||
		AttachPollOperationPage(poll, p, pollPage, nil) {
		t.Fatalf("attach poll page: capacity=%d", PollOperationConfiguredCapacity(poll))
	}
	if slot, ok := pollOperationSlotAt(poll, PollOperationPageCapacity); !ok ||
		slot != &pollPage.slots[0] {
		t.Fatal("dynamic poll slot address changed")
	}
	if !UnbindPollOperationSource(poll, p) || !poll.CanRelease() ||
		ConfigurePollOperationPages(poll, make([]PollOperationPage, 1)) {
		t.Fatal("dynamic poll catalog allowed static slot remapping")
	}

	manual := new(ManualOperationSource)
	manualPage := new(ManualOperationPage)
	if !BindManualOperationSourceAtRoute(manual, p, RouteID(4)) ||
		!AttachManualOperationPage(manual, p, manualPage, new(OperationPageDirectoryBlock)) ||
		ManualOperationConfiguredCapacity(manual) != 2*ManualOperationPageCapacity ||
		!CanReserveManualOperation(p, manual) ||
		AttachManualOperationPage(manual, p, manualPage, nil) {
		t.Fatalf("attach manual page: capacity=%d", ManualOperationConfiguredCapacity(manual))
	}
	if slot, ok := manualOperationSlotAt(manual, ManualOperationPageCapacity); !ok ||
		slot != &manualPage.slots[0] {
		t.Fatal("dynamic manual slot address changed")
	}
	if !UnbindManualOperationSource(manual, p) || !manual.CanRelease() ||
		ConfigureManualOperationPages(manual, make([]ManualOperationPage, 1)) {
		t.Fatal("dynamic manual catalog allowed static slot remapping")
	}

	worker := new(WorkerOperationSource)
	workerPage := new(WorkerOperationPage)
	if !BindWorkerOperationSourceAtRoute(worker, p, RouteID(5)) ||
		!AttachWorkerOperationPage(worker, p, workerPage, new(OperationPageDirectoryBlock)) ||
		WorkerOperationConfiguredCapacity(worker) != 2*WorkerOperationPageCapacity ||
		!CanReserveWorkerOperation(p, worker) ||
		AttachWorkerOperationPage(worker, p, workerPage, nil) {
		t.Fatalf("attach worker page: capacity=%d", WorkerOperationConfiguredCapacity(worker))
	}
	if slot, ok := workerOperationSlotAt(worker, WorkerOperationPageCapacity); !ok ||
		slot != &workerPage.slots[0] {
		t.Fatal("dynamic worker slot address changed")
	}
	if !UnbindWorkerOperationSource(worker, p) || !worker.CanRelease() ||
		ConfigureWorkerOperationPages(worker, make([]WorkerOperationPage, 1)) {
		t.Fatal("dynamic worker catalog allowed static slot remapping")
	}
}

func TestOperationSourcesRejectNonPristineDynamicPages(t *testing.T) {
	p := new(P)

	timers := new(TimerRegistrationTable)
	dirtyTimer := new(TimerRegistrationPage)
	dirtyTimer.slots[0].state = timerRegistrationActive
	if !bindTimerRegistrationTable(timers, p) || AttachTimerRegistrationPage(timers, p, dirtyTimer, new(OperationPageDirectoryBlock)) {
		t.Fatal("timer accepted non-pristine dynamic page")
	}

	poll := new(PollOperationSource)
	dirtyPoll := new(PollOperationPage)
	dirtyPoll.slots[0].state = pollOperationActive
	if !BindPollOperationSource(poll, p) || AttachPollOperationPage(poll, p, dirtyPoll, new(OperationPageDirectoryBlock)) {
		t.Fatal("poll accepted non-pristine dynamic page")
	}

	manual := new(ManualOperationSource)
	dirtyManual := new(ManualOperationPage)
	dirtyManual.slots[0].mailbox = uint32(manualOperationMailboxPosted)
	if !BindManualOperationSource(manual, p) || AttachManualOperationPage(manual, p, dirtyManual, new(OperationPageDirectoryBlock)) {
		t.Fatal("manual accepted non-pristine dynamic page")
	}

	worker := new(WorkerOperationSource)
	dirtyWorker := new(WorkerOperationPage)
	dirtyWorker.slots[0].submitted = true
	if !BindWorkerOperationSource(worker, p) || AttachWorkerOperationPage(worker, p, dirtyWorker, new(OperationPageDirectoryBlock)) {
		t.Fatal("worker accepted non-pristine dynamic page")
	}
}
