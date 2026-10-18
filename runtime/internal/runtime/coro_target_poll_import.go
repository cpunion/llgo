//go:build (llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test) || coro_poll_owner_test

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

import "github.com/xgo-dev/llgo/runtime/internal/coro"

// coroTargetPostPollEventsV1 imports readiness using the retained exact IDs.
// A producer may publish readiness or closing after the physical snapshot.
// Duplicate means that mailbox fact is already durable; preserve its result
// and return to the reducer just as for a first publication.
func coroTargetPostPollEventsV1(count uint32) (posted, ok bool) {
	if count == 0 || count > uint32(len(coroNativePollEntriesV1)) {
		return false, false
	}
	for entry := uint32(1); entry < count; entry++ {
		if coroNativePollEntriesV1[entry].Revents == 0 {
			continue
		}
		if result := coro.PostExecutorPollEvent(
			&coroProgramExecutorDriverV1State,
			coroNativePollIDsV2[entry-1],
			coro.PollOperationReady,
		); result != coro.PollOperationPosted && result != coro.PollOperationPostDuplicate {
			return false, false
		}
		posted = true
	}
	return posted, true
}
