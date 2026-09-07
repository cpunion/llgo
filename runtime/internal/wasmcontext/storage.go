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

package wasmcontext

import "unsafe"

const (
	// LLGo uses fixed-size Fiber stacks. 128 KiB is the smallest default that
	// accommodates standard-library call depths such as crypto/x509 signing;
	// 64 KiB reproducibly crosses the Fiber boundary under that workload.
	defaultStackSize = uintptr(128 << 10)
	// Asyncify records the native host-call continuation, not the complete Go
	// call stack, so its independently-tested default can remain smaller.
	defaultAsyncifyStackSize = uintptr(64 << 10)
	stackAlignment           = uintptr(16)
)

func allocStorage(stackSize uintptr, alloc func(uintptr) unsafe.Pointer, free func(unsafe.Pointer)) (stack unsafe.Pointer, normalizedStackSize uintptr, asyncifyStack unsafe.Pointer, asyncifySize uintptr, ok bool) {
	customStackSize := stackSize != 0
	if stackSize == 0 {
		stackSize = defaultStackSize
	}
	stackSize = alignStackSize(stackSize)
	asyncifySize = defaultAsyncifyStackSize
	if customStackSize && stackSize > asyncifySize {
		asyncifySize = stackSize
	}

	stack = alloc(stackSize)
	if stack == nil {
		return
	}
	asyncifyStack = alloc(asyncifySize)
	if asyncifyStack == nil {
		free(stack)
		stack = nil
		return
	}
	return stack, stackSize, asyncifyStack, asyncifySize, true
}

func freeStorage(stack, asyncifyStack unsafe.Pointer, free func(unsafe.Pointer)) {
	if stack != nil {
		free(stack)
	}
	if asyncifyStack != nil {
		free(asyncifyStack)
	}
}

func alignStackSize(size uintptr) uintptr {
	return (size + stackAlignment - 1) &^ (stackAlignment - 1)
}
