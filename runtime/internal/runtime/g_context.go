//go:build llgo && !baremetal && !nintendoswitch

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

// LLGoNeedsLocalContext tells the compiler that hosted runtime state occupies
// the stack-rooted entry context even when no source-level TLS/GLS variable is
// linked.
const LLGoNeedsLocalContext = true

// getg resolves the native TLS context anchor, then addresses g at its fixed
// offset in the stack-rooted entry context.
func getg() *g {
	ctx := (*LocalContext)(unsafe.Pointer(currentLocalContext))
	if ctx == nil {
		panic("runtime: getg called outside a Go entry context")
	}
	return &ctx.g
}

func getPanic(gp *g) unsafe.Pointer {
	return gp.panic_
}

func setPanic(gp *g, ptr unsafe.Pointer) {
	gp.panic_ = ptr
}
