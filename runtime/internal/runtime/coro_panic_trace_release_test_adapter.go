//go:build coro_runtime_adapter_test

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

// The host runtime-adapter island allocates any synthetic frame backing from
// the Go test heap rather than coroalloc. It still exercises the exact trace
// ownership transition, but reclamation belongs to the host GC.
func coroReleaseDiscardedPanicTraceV1(task *coro.G) {
	if !coro.PanicTraceDiscardPending(task) {
		return
	}
	for {
		raw, total, ok := coro.TakeDiscardedPanicTraceFrame(task)
		if !ok {
			coroRuntimeAbort("invalid discarded coroutine panic trace")
		}
		if raw == nil {
			return
		}
		coro.Zero(raw, total)
	}
}
