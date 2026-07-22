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

package coro

// sourceScanLimit is one plus the highest physical slot which may have left
// its reusable state during the current source binding. The scheduler owner is
// the sole writer. It only grows while bound: recycling a high slot must not
// hide a still-live lower slot, and allocating a high slot then releasing a
// lower slot must keep the high slot discoverable. A successful unbind starts
// the next binding generation at zero.
//
// This is deliberately only a service-scan bound. Configuration, close,
// empty, and unbind audits continue to visit the full configured capacity so
// an unallocated-tail invariant is never weakened into an active-prefix one.
func raiseSourceScanLimit(limit *uint32, index, capacity uint32) bool {
	if limit == nil || index >= capacity || *limit > capacity {
		return false
	}
	next := index + 1
	if next > *limit {
		*limit = next
	}
	return true
}

func validSourceScanLimit(limit, capacity uint32) bool {
	return limit <= capacity
}
