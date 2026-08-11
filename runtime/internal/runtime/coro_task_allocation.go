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

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

// coroTaskAllocation is the runtime-owned physical envelope for one spawned
// logical G. task must remain the first field: compiler-generated coroutine
// factories receive the allocation base as their opaque G pointer, while the
// target-neutral scheduler owns only the fixed-size task prefix. The runtime
// context has the same lifetime and scanned/root requirement, so retaining it
// in the tail removes one allocator transaction without adding a pool, cache,
// target callback, or scheduler dependency on runtime types.
type coroTaskAllocation struct {
	task    coro.G
	context coroRuntimeContext
}

const (
	coroTaskAllocationTaskOffset    = unsafe.Offsetof(coroTaskAllocation{}.task)
	coroTaskAllocationContextOffset = unsafe.Offsetof(coroTaskAllocation{}.context)
	coroTaskAllocationSize          = unsafe.Sizeof(coroTaskAllocation{})
)

// Go preserves declaration order, but make the allocation-base ABI an actual
// compile-time equality rather than a convention checked for every spawn.
var _ [coroTaskAllocationTaskOffset]byte = [0]byte{}

func coroTaskAllocationAt(raw unsafe.Pointer) (*coro.G, *coroRuntimeContext, uintptr, bool) {
	if raw == nil || uintptr(raw)%unsafe.Alignof(coroTaskAllocation{}) != 0 {
		return nil, nil, 0, false
	}
	allocation := (*coroTaskAllocation)(raw)
	task := &allocation.task
	context := &allocation.context
	if unsafe.Pointer(task) != raw ||
		unsafe.Pointer(context) != unsafe.Add(raw, coroTaskAllocationContextOffset) {
		return nil, nil, 0, false
	}
	return task, context, coroTaskAllocationSize, true
}

func coroTaskAllocationContext(task *coro.G) (*coroRuntimeContext, bool) {
	if task == nil {
		return nil, false
	}
	actual, context, _, ok := coroTaskAllocationAt(unsafe.Pointer(task))
	return context, ok && actual == task
}
