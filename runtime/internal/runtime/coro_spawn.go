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
	"github.com/goplus/llgo/runtime/internal/coroalloc"
)

// Version one is production-enabled only for plans whose spawn targets are
// proven YieldOnly/AwaitStructured. Command shutdown rejects every wait queue
// and directly destroys ready children deepest-to-root.
const coroSpawnProductionEnabledV1 = true

func coroSpawnBeginV1(parentPointer unsafe.Pointer) (unsafe.Pointer, bool) {
	parent := (*coroG)(parentPointer)
	if !coro.CanBeginSpawn(parent) || !coroalloc.Ready() {
		return nil, false
	}
	size := coro.TaskStorageSize()
	raw := coroalloc.AllocTask(size)
	if raw == nil {
		return nil, false
	}
	coro.Zero(raw, size)
	child := (*coroG)(raw)
	if !coro.BeginSpawn(parent, child, raw, size) {
		coro.Zero(raw, size)
		if !coroalloc.FreeTask(raw) {
			return nil, false
		}
		return nil, false
	}
	return raw, true
}

func coroSpawnCommitV1(parentPointer, childPointer, handle unsafe.Pointer) bool {
	parent, child := (*coroG)(parentPointer), (*coroG)(childPointer)
	return coro.CommitSpawn(parent, child, handle) && coroTargetRecordReadySpawnV1(parent, child)
}

// coroReleaseCompletedTask performs the physical half of spawned-G
// retirement. A platform producer may retain only a POD operation handle, never
// a child G pointer. The durable source owns that handle until it is quiesced
// and retired.
func coroReleaseCompletedTask(g *coroG) bool {
	// A compiler resume gate turns task cancellation into ordinary terminal
	// frame completion after source-specific park cleanup. The cancellation
	// record remains sticky until the G is physically dead; acknowledge it here
	// before applying the normal reclaimability/storage transfer contract.
	if !coro.ReclaimableG(g) &&
		!coro.AcknowledgeTaskCancellation(g, coro.TaskCancelAbort) &&
		!coro.AcknowledgeTaskCancellation(g, coro.TaskCancelShutdown) {
		return false
	}
	owned, ok := coro.TaskStorageOwned(g)
	if !ok {
		return false
	}
	if !owned {
		return true
	}
	raw, size, ok := coro.ReleaseTaskStorage(g)
	if !ok {
		return false
	}
	coro.Zero(raw, size)
	return coroalloc.FreeTask(raw)
}

//export __llgo_coro_spawn_begin_v1
func __llgo_coro_spawn_begin_v1(parent unsafe.Pointer) unsafe.Pointer {
	if !coroSpawnProductionEnabledV1 {
		coroRuntimeAbort("coroutine goroutine spawn is not production-enabled")
		return nil
	}
	child, ok := coroSpawnBeginV1(parent)
	if !ok {
		coroRuntimeAbort("invalid coroutine goroutine spawn begin")
		return nil
	}
	return child
}

//export __llgo_coro_spawn_commit_v1
func __llgo_coro_spawn_commit_v1(parent, child, handle unsafe.Pointer) {
	if !coroSpawnProductionEnabledV1 {
		coroRuntimeAbort("coroutine goroutine spawn is not production-enabled")
		return
	}
	if !coroSpawnCommitV1(parent, child, handle) {
		// A published LLVM handle is never destroyed here. Scheduler ownership is
		// exclusive; malformed commit is therefore a terminal ABI violation.
		coroRuntimeAbort("invalid coroutine goroutine spawn commit")
	}
}
