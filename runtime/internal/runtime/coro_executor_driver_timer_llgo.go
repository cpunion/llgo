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

// Native keeps the target-neutral 64-slot page granularity. These are logical
// limits reached through demand-paged growth, not process-global reservations.
// Timers retain a larger limit because ordinary Go code can create more live
// Timer/Ticker values than blocking file operations.
const (
	coroNativeTimerCapacityV1 = coroRuntimeTimerCapacityV1
	coroNativePollCapacityV1  = coroRuntimePollCapacityV1
)

// CoroMonotonicNano is the target-neutral clock consumed by Timer/Park and the
// standard-library timer bridge. Native targets sample their local monotonic
// clock; host-pull targets provide the same contract from published POD time.
func CoroMonotonicNano() (int64, bool) {
	return coroclock.MonotonicNano()
}

func coroProgramBindExecutorDriverV1(driver *coro.ExecutorDriver, p *coroP, registry *coro.ExecutorRegistry, handle coro.ExecutorHandle) bool {
	if coro.TimerRegistrationConfiguredCapacity(&coroProgramTimerTableV1State) != coro.TimerRegistrationPageCapacity ||
		coro.PollOperationConfiguredCapacity(&coroProgramPollSourceV1State) != coro.PollOperationPageCapacity ||
		coro.ManualOperationConfiguredCapacity(&coroProgramManualSourceV2State) != coro.ManualOperationPageCapacity ||
		coro.ChannelOperationConfiguredCapacity(&coroProgramChannelSourceV1State) != coro.ChannelOperationPageCapacity ||
		coroNativeTimerCapacityV1 != coroNativeTimerPageCountV1*coro.TimerRegistrationPageCapacity ||
		coroNativePollCapacityV1 != coroNativeSourcePageCountV1*coro.PollOperationPageCapacity ||
		coroNativeWorkerCapacityV1 != coroRuntimeWorkerCapacityV1 ||
		coroNativeWorkerQueueSizeV1 != coroNativeWorkerCapacityV1 {
		return false
	}
	var worker *coro.WorkerOperationSource
	if coroProgramWorkerCapabilityV2() {
		if coro.WorkerOperationConfiguredCapacity(&coroProgramWorkerSourceV1State) != coro.WorkerOperationPageCapacity {
			return false
		}
		worker = &coroProgramWorkerSourceV1State
	}
	return coro.BindExecutorSourceCatalog(driver, p, registry, handle, coro.ExecutorSourceCatalog{
		Timers:  &coroProgramTimerTableV1State,
		Poll:    &coroProgramPollSourceV1State,
		Manual:  &coroProgramManualSourceV2State,
		Worker:  worker,
		Channel: &coroProgramChannelSourceV1State,
		Control: &coroProgramTaskControlSourceV1State,
	})
}

func coroProgramNextRunStepV1(
	_ *coro.ExecutorDriver,
	run *coro.ExecutorRunSliceCapability,
	combineDispatch bool,
) (coro.ExecutorRunStep, bool) {
	if combineDispatch {
		if step, ok := run.NextBeforeTimeCombined(); ok {
			return step, true
		}
	} else if step, ok := run.NextBeforeTime(); ok {
		return step, true
	}
	now, ok := coroclock.MonotonicNano()
	if !ok {
		return coro.ExecutorRunStep{}, false
	}
	if combineDispatch {
		return run.NextAtCombined(now)
	}
	return run.NextAt(now)
}

func coroProgramPrepareExecutorSleepV1(driver *coro.ExecutorDriver) (sleep bool, deadline int64, hasDeadline, ok bool) {
	if ready, fastOK := coroNativeTryFastWorkerCompletionV1(driver); !fastOK {
		return false, 0, false, false
	} else if ready {
		return false, 0, false, true
	}
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
	return coro.WakeExecutorAt(driver, now)
}
