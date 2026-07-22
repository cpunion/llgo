//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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
	"github.com/goplus/llgo/runtime/internal/coroclock"
)

// Native keeps the target-neutral 64-slot page granularity. Poll, manual,
// worker, and channel sources reserve 1024 simultaneous entries.
// Timers have an independent 4096-entry reservation because ordinary Go code
// can create substantially more live Timer/Ticker values than blocking file or
// synchronization operations. Embedded and bare-metal profiles do not compile
// this file and retain the inline page unless they provide their own storage.
const (
	coroNativeSourcePageCountV1 = 16
	coroNativeTimerPageCountV1  = 64
	coroNativeTimerCapacityV1   = coroNativeTimerPageCountV1 * coro.TimerRegistrationPageCapacity
	coroNativePollCapacityV1    = coroNativeSourcePageCountV1 * coro.PollOperationPageCapacity
	coroNativeWorkerCapacityV1  = coroNativeSourcePageCountV1 * coro.WorkerOperationPageCapacity
	coroNativeChannelCapacityV1 = coroNativeSourcePageCountV1 * coro.ChannelOperationPageCapacity
)

var (
	coroProgramTimerExtraPagesV1State   [coroNativeTimerPageCountV1 - 1]coro.TimerRegistrationPage
	coroProgramPollExtraPagesV1State    [coroNativeSourcePageCountV1 - 1]coro.PollOperationPage
	coroProgramManualExtraPagesV2State  [coroNativeManualPageCountV2 - 1]coro.ManualOperationPage
	coroProgramWorkerExtraPagesV1State  [coroNativeSourcePageCountV1 - 1]coro.WorkerOperationPage
	coroProgramChannelExtraPagesV1State [coroNativeSourcePageCountV1 - 1]coro.ChannelOperationPage
)

func coroProgramBindExecutorDriverV1(driver *coro.ExecutorDriver, p *coroP, registry *coro.ExecutorRegistry, handle coro.ExecutorHandle) bool {
	if !coro.ConfigureTimerRegistrationPages(&coroProgramTimerTableV1State, coroProgramTimerExtraPagesV1State[:]) ||
		!coro.ConfigurePollOperationPages(&coroProgramPollSourceV1State, coroProgramPollExtraPagesV1State[:]) ||
		!coro.ConfigureManualOperationPages(&coroProgramManualSourceV2State, coroProgramManualExtraPagesV2State[:]) ||
		!coro.ConfigureWorkerOperationPages(&coroProgramWorkerSourceV1State, coroProgramWorkerExtraPagesV1State[:]) ||
		!coro.ConfigureChannelOperationPages(&coroProgramChannelSourceV1State, coroProgramChannelExtraPagesV1State[:]) ||
		coro.TimerRegistrationConfiguredCapacity(&coroProgramTimerTableV1State) != coroNativeTimerCapacityV1 ||
		coro.PollOperationConfiguredCapacity(&coroProgramPollSourceV1State) != coroNativePollCapacityV1 ||
		coro.ManualOperationConfiguredCapacity(&coroProgramManualSourceV2State) != coroNativeSourcePageCountV1*coro.ManualOperationPageCapacity ||
		coro.WorkerOperationConfiguredCapacity(&coroProgramWorkerSourceV1State) != coroNativeWorkerCapacityV1 ||
		coro.ChannelOperationConfiguredCapacity(&coroProgramChannelSourceV1State) != coroNativeChannelCapacityV1 ||
		coroNativeWorkerCapacityV1 != coroNativeSourcePageCountV1*coro.ManualOperationPageCapacity ||
		coroNativeChannelCapacityV1 != coroNativeSourcePageCountV1*coro.ManualOperationPageCapacity ||
		coroNativeWorkerQueueSizeV1 != coroNativeWorkerCapacityV1 {
		return false
	}
	return coro.BindExecutorSourceCatalog(driver, p, registry, handle, coro.ExecutorSourceCatalog{
		Timers:  &coroProgramTimerTableV1State,
		Poll:    &coroProgramPollSourceV1State,
		Manual:  &coroProgramManualSourceV2State,
		Worker:  &coroProgramWorkerSourceV1State,
		Channel: &coroProgramChannelSourceV1State,
		Control: &coroProgramTaskControlSourceV1State,
	})
}

func coroProgramNextRunStepV1(driver *coro.ExecutorDriver) (coro.ExecutorRunStep, bool) {
	now, ok := coroclock.MonotonicNano()
	if !ok {
		return coro.ExecutorRunStep{}, false
	}
	return coro.NextExecutorRunStepAt(driver, now)
}

func coroProgramPrepareExecutorSleepV1(driver *coro.ExecutorDriver) (sleep bool, deadline int64, hasDeadline, ok bool) {
	now, ok := coroclock.MonotonicNano()
	if !ok {
		return false, 0, false, false
	}
	prepared, ok := coro.PrepareExecutorSleepAt(driver, now)
	if !ok || !prepared {
		return false, 0, false, ok
	}
	freshNow, ok := coroclock.MonotonicNano()
	if !ok {
		// Commit with an invalid timestamp restores the active driver before the
		// runtime fails closed, so no armed idle gate survives a clock failure.
		_, _, _, _ = coro.CommitExecutorSleepAt(driver, -1)
		return false, 0, false, false
	}
	return coro.CommitExecutorSleepAt(driver, freshNow)
}

func coroProgramPollExecutorV1(driver *coro.ExecutorDriver) bool {
	now, clockOK := coroclock.MonotonicNano()
	if !clockOK {
		return false
	}
	_, _, ok := coro.PollExecutorAt(driver, now)
	return ok
}

func coroProgramWakeExecutorV1(driver *coro.ExecutorDriver) bool {
	now, clockOK := coroclock.MonotonicNano()
	if !clockOK {
		return false
	}
	_, _, ok := coro.WakeExecutorAt(driver, now)
	return ok
}
