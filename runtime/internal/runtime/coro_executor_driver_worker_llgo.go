//go:build llgo && llgo_coro && llgo_coro_native_pipe && !llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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

// The 32-bit POSIX profile has the same bounded pthread worker and pipe
// doorbell as the full native fleet, but deliberately has no timer or poll
// source until its libc time ABI is verified. Bind the Worker source here so a
// managed blocking foreign call always parks its G and completes through the
// pipe; it must never fall back to running on the executor thread.
func coroProgramBindExecutorDriverV1(driver *coro.ExecutorDriver, p *coroP, registry *coro.ExecutorRegistry, handle coro.ExecutorHandle) bool {
	workerEnabled := coroProgramWorkerCapabilityV2()
	if workerEnabled && (coro.WorkerOperationConfiguredCapacity(&coroProgramWorkerSourceV1State) != coro.WorkerOperationPageCapacity ||
		coroNativeWorkerCapacityV1 != coroRuntimeWorkerCapacityV1) ||
		coroNativeWorkerQueueSizeV1 != coroNativeWorkerCapacityV1 {
		return false
	}
	var worker *coro.WorkerOperationSource
	if workerEnabled {
		worker = &coroProgramWorkerSourceV1State
	}
	return coro.BindExecutorSourceCatalog(driver, p, registry, handle, coro.ExecutorSourceCatalog{
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
		return run.NextCombined()
	}
	return run.Next()
}

func coroProgramPrepareExecutorSleepV1(driver *coro.ExecutorDriver) (sleep bool, deadline int64, hasDeadline, ok bool) {
	if ready, fastOK := coroNativeTryFastWorkerCompletionV1(driver); !fastOK {
		return false, 0, false, false
	} else if ready {
		return false, 0, false, true
	}
	sleep, ok = coro.PrepareExecutorSleep(driver)
	return sleep, 0, false, ok
}

func coroProgramPollExecutorV1(driver *coro.ExecutorDriver) bool {
	_, _, ok := coro.PollExecutor(driver)
	return ok
}

func coroProgramWakeExecutorV1(driver *coro.ExecutorDriver) bool {
	return coro.WakeExecutor(driver)
}
