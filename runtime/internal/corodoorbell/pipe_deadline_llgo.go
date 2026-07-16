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

package corodoorbell

import "github.com/goplus/llgo/runtime/internal/coroclock"

// WaitDeadline blocks the native executor until either the retained pipe is
// rung or one absolute monotonic deadline is observed due. A physical poll
// timeout is not itself a durable event: the clock is sampled again before
// reached can be returned. Long waits are split into bounded passes so failed
// writes and clock/backend faults cannot leave the owner asleep indefinitely.
func (pipe *Pipe) WaitDeadline(deadline int64) (woke, reached, ok bool) {
	if pipe == nil || deadline < 0 || nativeAtomicLoad(&pipe.open) != 1 || pipe.readFD < 0 {
		return false, false, false
	}
	for {
		now, clockOK := coroclock.MonotonicNano()
		if !clockOK {
			return false, false, false
		}
		timeoutMS, due, timeoutOK := deadlinePollTimeout(now, deadline)
		if !timeoutOK {
			return false, false, false
		}
		if due {
			return false, true, true
		}
		woke, waitOK := pipe.WaitBounded(timeoutMS)
		if !waitOK {
			return false, false, false
		}
		if woke {
			return true, false, true
		}
	}
}
