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

import "github.com/goplus/llgo/runtime/internal/coro"

// These are logical target-profile limits, not eager reservations. Every
// source retains its allocation-free inline page; hosted runtimes attach one
// stable page only when the current owner has exhausted all existing slots.
const (
	coroRuntimeTimerCapacityV1  = 64 * coro.TimerRegistrationPageCapacity
	coroRuntimePollCapacityV1   = 16 * coro.PollOperationPageCapacity
	coroRuntimeManualCapacityV1 = 32 * coro.ManualOperationPageCapacity
	coroRuntimeWorkerCapacityV1 = 16 * coro.WorkerOperationPageCapacity
)

func coroCurrentExecutorSourcesV1(
	driver *coro.ExecutorDriver,
	g *coro.G,
) (*coro.P, coro.ExecutorSourceCatalog, bool) {
	return coro.CurrentExecutorSourceCatalog(driver, g)
}

func ensureCoroTimerOperationCapacityV1(
	driver *coro.ExecutorDriver,
	g *coro.G,
	limit uint32,
) bool {
	p, sources, ok := coroCurrentExecutorSourcesV1(driver, g)
	if !ok || sources.Timers == nil || limit == 0 || limit > coro.TimerRegistrationMaximumCapacity {
		return false
	}
	if coro.CanReserveTimerV2(p, sources.Timers) {
		return true
	}
	if coro.TimerRegistrationConfiguredCapacity(sources.Timers) >= limit {
		return false
	}
	page := new(coro.TimerRegistrationPage)
	if page == nil {
		return false
	}
	attached := coro.AttachTimerRegistrationPage(sources.Timers, p, page, nil)
	if !attached {
		block := new(coro.OperationPageDirectoryBlock)
		attached = block != nil && coro.AttachTimerRegistrationPage(sources.Timers, p, page, block)
	}
	return attached &&
		coro.CanReserveTimerV2(p, sources.Timers)
}

func ensureCoroPollOperationCapacityV1(
	driver *coro.ExecutorDriver,
	g *coro.G,
	limit uint32,
) bool {
	p, sources, ok := coroCurrentExecutorSourcesV1(driver, g)
	if !ok || sources.Poll == nil || limit == 0 || limit > coro.PollOperationMaximumCapacity {
		return false
	}
	if coro.CanReservePollOperationV2(p, sources.Poll) {
		return true
	}
	if coro.PollOperationConfiguredCapacity(sources.Poll) >= limit {
		return false
	}
	page := new(coro.PollOperationPage)
	if page == nil {
		return false
	}
	attached := coro.AttachPollOperationPage(sources.Poll, p, page, nil)
	if !attached {
		block := new(coro.OperationPageDirectoryBlock)
		attached = block != nil && coro.AttachPollOperationPage(sources.Poll, p, page, block)
	}
	return attached &&
		coro.CanReservePollOperationV2(p, sources.Poll)
}

func ensureCoroManualOperationCapacityV1(
	driver *coro.ExecutorDriver,
	g *coro.G,
	limit uint32,
) bool {
	p, sources, ok := coroCurrentExecutorSourcesV1(driver, g)
	if !ok || sources.Manual == nil || limit == 0 || limit > coro.ManualOperationMaximumCapacity {
		return false
	}
	if coro.CanReserveManualOperation(p, sources.Manual) {
		return true
	}
	if coro.ManualOperationConfiguredCapacity(sources.Manual) >= limit {
		return false
	}
	page := new(coro.ManualOperationPage)
	if page == nil {
		return false
	}
	attached := coro.AttachManualOperationPage(sources.Manual, p, page, nil)
	if !attached {
		block := new(coro.OperationPageDirectoryBlock)
		attached = block != nil && coro.AttachManualOperationPage(sources.Manual, p, page, block)
	}
	return attached &&
		coro.CanReserveManualOperation(p, sources.Manual)
}

func ensureCoroWorkerOperationCapacityV1(
	driver *coro.ExecutorDriver,
	g *coro.G,
	limit uint32,
) bool {
	p, sources, ok := coroCurrentExecutorSourcesV1(driver, g)
	if !ok || sources.Worker == nil || limit == 0 || limit > coro.WorkerOperationMaximumCapacity {
		return false
	}
	if coro.CanReserveWorkerOperation(p, sources.Worker) {
		return true
	}
	if coro.WorkerOperationConfiguredCapacity(sources.Worker) >= limit {
		return false
	}
	page := new(coro.WorkerOperationPage)
	if page == nil {
		return false
	}
	attached := coro.AttachWorkerOperationPage(sources.Worker, p, page, nil)
	if !attached {
		block := new(coro.OperationPageDirectoryBlock)
		attached = block != nil && coro.AttachWorkerOperationPage(sources.Worker, p, page, block)
	}
	return attached &&
		coro.CanReserveWorkerOperation(p, sources.Worker)
}
