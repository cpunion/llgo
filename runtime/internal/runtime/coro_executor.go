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

// The first production runner owns one statically addressed executor domain.
// Platform callback ABIs retain only coroProgramExecutorHandleV1State and wait
// registration handles; none of these Go objects or their addresses crosses a
// target callback boundary.
var (
	coroProgramExecutorRegistryV1State coro.ExecutorRegistry
	coroProgramWaitTableV1State        coro.WaitRegistrationTable
	coroProgramExecutorDriverV1State   coro.ExecutorDriver
	coroProgramExecutorHandleV1State   coro.ExecutorHandle
	coroProgramExecutorBoundV1State    bool
)

type coroTargetDispatchResultV1 uint8

const (
	coroTargetDispatchInvalidV1 coroTargetDispatchResultV1 = iota
	coroTargetDispatchCompleteV1
	coroTargetDispatchPendingV1
)

func coroProgramBindExecutorV1() bool {
	if coroProgramExecutorBoundV1State ||
		coroProgramExecutorHandleV1State != (coro.ExecutorHandle{}) ||
		coroProgramExecutorDriverV1State != (coro.ExecutorDriver{}) ||
		!coroProgramExecutorRegistryV1State.CanRelease() ||
		!coroProgramWaitTableV1State.CanRelease() {
		return false
	}
	handle, ok := coroProgramExecutorRegistryV1State.Register()
	if !ok || !coro.BindExecutor(
		&coroProgramExecutorDriverV1State,
		&coroProgramPV1State,
		&coroProgramExecutorRegistryV1State,
		handle,
		&coroProgramWaitTableV1State,
	) {
		return false
	}
	coroProgramExecutorHandleV1State = handle
	coroProgramExecutorBoundV1State = true
	return true
}

func coroProgramExecutorRetiredV1() bool {
	if !coroProgramExecutorBoundV1State ||
		coroProgramExecutorHandleV1State == (coro.ExecutorHandle{}) ||
		coroProgramExecutorDriverV1State != (coro.ExecutorDriver{}) ||
		!coroProgramExecutorRegistryV1State.CanRelease() ||
		!coroProgramWaitTableV1State.CanRelease() {
		return false
	}
	coroProgramExecutorBoundV1State = false
	coroProgramExecutorHandleV1State = coro.ExecutorHandle{}
	return true
}
