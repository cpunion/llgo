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

import "unsafe"

// A freestanding core module has no implicit stdout/stderr capability. Treat a
// diagnostic write as consumed so upstream runtime formatting remains bounded
// and cannot retry forever. An embedding-owned logging event can replace this
// single leaf without changing the synchronous runtime.write API.
func runtimeWrite(fd uintptr, p unsafe.Pointer, n int32) int32 {
	_, _ = fd, p
	return n
}
