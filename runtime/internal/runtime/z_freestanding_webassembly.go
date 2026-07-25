//go:build wasip2 || wasm_unknown

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

// A freestanding core module has neither a process exit ABI nor an output
// stream. Managed panic/defer/recover use explicit coroutine status; a panic
// that escapes the last raw/plain boundary can only terminate by trapping.
func Rethrow(_ *Defer) {
	coroFreestandingAbort()
	for {
	}
}
