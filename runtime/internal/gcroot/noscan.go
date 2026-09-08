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

package gcroot

// SplitStaticRoots removes the fully covered, aligned words of an immutable
// metadata range from [start, end). Its results describe [start, beforeEnd)
// and [afterStart, end). Ordinary Go and C globals outside that range remain
// conservative roots. Partial boundary words are scanned, never discarded.
// The linker supplies the range before initialization; no runtime registration
// or allocation is needed, including while the first collection is starting.
func SplitStaticRoots(start, end, noScanStart, noScanEnd, alignment uintptr) (beforeEnd, afterStart uintptr) {
	if start >= end || alignment == 0 || alignment&(alignment-1) != 0 || start&(alignment-1) != 0 ||
		noScanStart >= noScanEnd || noScanStart >= end || noScanEnd <= start {
		return end, end
	}
	if noScanStart < start {
		noScanStart = start
	}
	if noScanEnd > end {
		noScanEnd = end
	}
	if noScanStart > ^uintptr(0)-(alignment-1) {
		return end, end
	}
	beforeEnd = (noScanStart + alignment - 1) &^ (alignment - 1)
	afterStart = noScanEnd &^ (alignment - 1)
	if beforeEnd >= afterStart {
		return end, end
	}
	return
}
