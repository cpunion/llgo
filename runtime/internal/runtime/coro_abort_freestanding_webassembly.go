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

import _ "unsafe"

//go:linkname coroFreestandingAbort llgo.controlTrap
func coroFreestandingAbort()

// Freestanding core modules have no process stderr or exit ABI. A scheduler
// invariant failure is therefore an immediate WebAssembly trap; richer
// diagnostics belong to an embedding-owned host adapter.
func coroRuntimeAbort(string) {
	coroFreestandingAbort()
	for {
	}
}
