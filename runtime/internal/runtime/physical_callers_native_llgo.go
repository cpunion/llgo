//go:build llgo && !baremetal && !wasm && !tinygo.wasm

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

// cPhysicalCallers copies return PCs while its native frame and the complete
// chain are alive. It is same-thread and non-retaining; moving it to a worker
// would inspect the worker's unrelated stack.
//
//llgo:coro sync
//go:linkname cPhysicalCallers C.llgo_fp_callers
func cPhysicalCallers(skip int, pc *uintptr, capacity int, textLow, textHigh uintptr) int

// PhysicalCallers copies the current native frame chain. A zero text range is
// the bounded-by-capacity raw mode used at panic time; a non-zero range stops
// the walk when it leaves LLGo program text.
func PhysicalCallers(skip int, pc []uintptr, textLow, textHigh uintptr) int {
	if len(pc) == 0 {
		return 0
	}
	return cPhysicalCallers(skip, &pc[0], len(pc), textLow, textHigh)
}
