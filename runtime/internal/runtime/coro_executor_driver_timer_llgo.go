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

func coroProgramBindExecutorDriverV1(driver *coro.ExecutorDriver, p *coroP, registry *coro.ExecutorRegistry, handle coro.ExecutorHandle, waits *coro.WaitRegistrationTable) bool {
	return coro.BindExecutorSourceCatalog(driver, p, registry, handle, coro.ExecutorSourceCatalog{
		Waits:   waits,
		Timers:  &coroProgramTimerTableV1State,
		Worker:  &coroProgramWorkerSourceV1State,
		Channel: &coroProgramChannelSourceV1State,
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
	_, _, _, ok := coro.PollExecutorAt(driver, now)
	return ok
}

func coroProgramWakeExecutorV1(driver *coro.ExecutorDriver) bool {
	now, clockOK := coroclock.MonotonicNano()
	if !clockOK {
		return false
	}
	_, _, _, ok := coro.WakeExecutorAt(driver, now)
	return ok
}
