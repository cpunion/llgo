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

import "github.com/goplus/llgo/runtime/internal/corodoorbell"

func coroTargetWaitExecutorV1(pipe *corodoorbell.Pipe, deadline int64, hasDeadline bool) bool {
	if pipe == nil || deadline < 0 || !hasDeadline && deadline != 0 {
		return false
	}
	if !hasDeadline {
		return pipe.Wait()
	}
	woke, reached, ok := pipe.WaitDeadline(deadline)
	// Neither outcome completes a timer here. Both only return scheduler
	// ownership; coroProgramWakeExecutorV1 takes a fresh monotonic sample and
	// performs the durable wait+timer source transaction.
	return ok && (woke || reached)
}
