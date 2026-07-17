//go:build coro_runtime_adapter_test || !(llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal)

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

func coroProgramBindExecutorDriverV1(driver *coro.ExecutorDriver, p *coroP, registry *coro.ExecutorRegistry, handle coro.ExecutorHandle, waits *coro.WaitRegistrationTable) bool {
	return coro.BindExecutor(driver, p, registry, handle, waits)
}

func coroProgramNextRunStepV1(driver *coro.ExecutorDriver) (coro.ExecutorRunStep, bool) {
	return coro.NextExecutorRunStep(driver)
}

func coroProgramPrepareExecutorSleepV1(driver *coro.ExecutorDriver) (sleep bool, deadline int64, hasDeadline, ok bool) {
	sleep, ok = coro.PrepareExecutorSleep(driver)
	return sleep, 0, false, ok
}

func coroProgramPollExecutorV1(driver *coro.ExecutorDriver) bool {
	_, _, ok := coro.PollExecutor(driver)
	return ok
}

func coroProgramWakeExecutorV1(driver *coro.ExecutorDriver) bool {
	_, _, ok := coro.WakeExecutor(driver)
	return ok
}
