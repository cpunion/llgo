//go:build llgo && llgo_coro && !coro_runtime_adapter_test && (wasm || baremetal || llgo_coro_host) && !(llgo_coro_native_pipe && (darwin || linux) && !baremetal)

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

// The host profile deliberately keeps the inline fixed-capacity pages. This is
// deterministic for embedded/baremetal and requires no allocator. Timer,
// channel, and task-control are the capabilities currently wired into
// the host ingress. Poll and worker sources stay absent and therefore fail
// closed instead of pretending that a JS, WASI, RTOS, or IRQ backend exists.
func coroProgramBindExecutorDriverV1(driver *coro.ExecutorDriver, p *coroP, registry *coro.ExecutorRegistry, handle coro.ExecutorHandle) bool {
	return coro.BindExecutorSourceCatalog(driver, p, registry, handle, coro.ExecutorSourceCatalog{
		Timers: &coroProgramTimerTableV1State, Channel: &coroProgramChannelSourceV1State,
		Control: &coroProgramTaskControlSourceV1State,
	})
}

func coroProgramNextRunStepV1(driver *coro.ExecutorDriver) (coro.ExecutorRunStep, bool) {
	now, ok := coroHostClockV1State.Snapshot()
	if !ok {
		return coro.ExecutorRunStep{}, false
	}
	return coro.NextExecutorRunStepAt(driver, now)
}

func coroProgramPrepareExecutorSleepV1(driver *coro.ExecutorDriver) (sleep bool, deadline int64, hasDeadline, ok bool) {
	now, ok := coroHostClockV1State.Snapshot()
	if !ok {
		return false, 0, false, false
	}
	prepared, ok := coro.PrepareExecutorSleepAt(driver, now)
	if !ok || !prepared {
		return false, 0, false, ok
	}
	// The host publishes time before every clean entry. Reusing the same sample
	// here is intentional: no host turn can advance while this bounded slice is
	// executing, and a later entry will publish a fresh sample.
	return coro.CommitExecutorSleepAt(driver, now)
}

func coroProgramPollExecutorV1(driver *coro.ExecutorDriver) bool {
	now, clockOK := coroHostClockV1State.Snapshot()
	if !clockOK {
		return false
	}
	_, _, ok := coro.PollExecutorAt(driver, now)
	return ok
}

func coroProgramWakeExecutorV1(driver *coro.ExecutorDriver) bool {
	now, clockOK := coroHostClockV1State.Snapshot()
	if !clockOK {
		return false
	}
	_, _, ok := coro.WakeExecutorAt(driver, now)
	return ok
}
