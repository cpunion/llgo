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

// Package corotimer contains allocation-free timer arithmetic shared by the
// stackless coroutine runtime adapters. It owns no clock or platform state.
package corotimer

const maxDeadline = int64(^uint64(0) >> 1)

// DeadlineAfter converts one monotonic sample and a relative delay to an
// absolute deadline. Non-positive delays are immediately due; positive
// overflow saturates instead of wrapping into the past.
func DeadlineAfter(now, delay int64) (int64, bool) {
	if now < 0 {
		return 0, false
	}
	if delay <= 0 {
		return now, true
	}
	if delay > maxDeadline-now {
		return maxDeadline, true
	}
	return now + delay, true
}
