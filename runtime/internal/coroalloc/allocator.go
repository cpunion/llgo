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

// Package coroalloc owns the phase-0 coroutine frame allocator boundary. It
// intentionally has no package initialization, callback, interface, or
// function-value dispatch: the selected target backend is linked statically.
package coroalloc

import "unsafe"

type bootstrapState uint8

const (
	bootstrapUninitialized bootstrapState = iota
	bootstrapInitializing
	bootstrapReady
	bootstrapFailed
)

type bootstrapDecision uint8

const (
	bootstrapReject bootstrapDecision = iota
	bootstrapStart
	bootstrapAlreadyReady
)

var state bootstrapState

func beginBootstrap(current bootstrapState) (bootstrapState, bootstrapDecision) {
	switch current {
	case bootstrapUninitialized:
		return bootstrapInitializing, bootstrapStart
	case bootstrapReady:
		return bootstrapReady, bootstrapAlreadyReady
	case bootstrapInitializing, bootstrapFailed:
		return bootstrapFailed, bootstrapReject
	default:
		return bootstrapFailed, bootstrapReject
	}
}

func finishBootstrap(current bootstrapState, success bool) (bootstrapState, bool) {
	if current != bootstrapInitializing || !success {
		return bootstrapFailed, false
	}
	return bootstrapReady, true
}

// Bootstrap initializes the statically selected frame allocator backend. The
// process-entry path is single-threaded until this function returns; after a
// successful transition state is immutable and may be read by scheduler
// workers. A recursive or failed initialization permanently fails closed.
func Bootstrap() bool {
	next, decision := beginBootstrap(state)
	state = next
	switch decision {
	case bootstrapAlreadyReady:
		return true
	case bootstrapStart:
		next, success := finishBootstrap(state, backendBootstrap())
		state = next
		return success
	default:
		return false
	}
}

// Ready reports whether Bootstrap completed successfully.
func Ready() bool {
	return state == bootstrapReady
}

// AllocFrame allocates one zero-filled, explicitly owned, GC-visible
// coroutine frame range. Zero initialization is a backend contract: BDWGC and
// tinygogc already clear scanned allocations, while libc profiles use calloc.
// A caller cannot accidentally rely on a backend's implicit lazy init.
func AllocFrame(size uintptr) unsafe.Pointer {
	if !backendAllocationsAreZeroed || !Ready() || size == 0 {
		return nil
	}
	return alloc(size)
}

// FreeFrame releases a range previously returned by AllocFrame. Backends that
// reclaim through a tracing collector may deliberately implement physical
// free as a no-op, but still validate allocator readiness through this API.
func FreeFrame(ptr unsafe.Pointer, size uintptr) bool {
	if !Ready() || ptr == nil || size == 0 {
		return false
	}
	return free(ptr, size)
}

// AllocTask allocates zero-filled, pointer-containing scheduler task storage.
// It uses the same statically selected scanned/root backend as coroutine frames: BDWGC's
// uncollectable allocation is conservatively scanned, tinygogc sees the task
// through the scheduler's static P/parent links, and leaking WebAssembly
// profiles have no tracing collector that an ordinary malloc range could hide
// pointers from.
func AllocTask(size uintptr) unsafe.Pointer {
	if !backendAllocationsAreZeroed || !Ready() || size == 0 {
		return nil
	}
	return alloc(size)
}

// FreeTask performs the physical half of the scheduler's exactly-once task
// retirement protocol. The caller must first unlink and logically release the
// G through coro.ReleaseTaskStorage.
func FreeTask(ptr unsafe.Pointer, size uintptr) bool {
	if !Ready() || ptr == nil || size == 0 {
		return false
	}
	return free(ptr, size)
}

func alloc(size uintptr) unsafe.Pointer {
	allocationSize := cacheAllocationSize(size)
	if ptr := cacheTake(allocationSize); ptr != nil {
		return ptr
	}
	return backendAllocFrame(allocationSize)
}

func free(ptr unsafe.Pointer, size uintptr) bool {
	allocationSize := cacheAllocationSize(size)
	if cachePut(ptr, allocationSize) {
		return true
	}
	return backendFreeFrame(ptr, allocationSize)
}
