//go:build llgo && llgo_coro && llgo_coro_native_pipe && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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

	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/clite/pthread"
	"github.com/goplus/llgo/runtime/internal/coro"
	"github.com/goplus/llgo/runtime/internal/corofleet"
	"github.com/goplus/llgo/runtime/internal/coroworker"
)

// __llgo_coro_os_thread_foreign_call_v1 is the sole same-M blocking foreign
// boundary. The compiler selects it dynamically only while the current G owns
// this P/M island through LockOSThread. All ordinary calls continue through
// the shared any-thread worker pool. This owner detaches the active resume,
// reserves one scalar-slot replacement M, releases its managed-execution
// permit before creating the replacement thread, and strongly rejoins that
// replacement before restoring the resume. Releasing first is mandatory:
// execution-quota ownership belongs to the route, so a replacement which
// starts while its parent still holds that route is a fail-closed double
// acquire rather than ordinary quota contention.
//
//export __llgo_coro_os_thread_foreign_call_v1
func __llgo_coro_os_thread_foreign_call_v1(
	g unsafe.Pointer,
	function uintptr,
	argc uint32,
	a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr,
	r1, r2, errno *uintptr,
) {
	task := (*coro.G)(g)
	if function == 0 || argc > coroworker.MaxArgs ||
		r1 == nil || r2 == nil || errno == nil ||
		r1 == r2 || r1 == errno || r2 == errno ||
		!coro.CurrentOSThreadLocked(task) {
		coroRuntimeAbort("invalid locked-thread foreign call")
	}
	driver, _, _, ownerOK := coro.CurrentExecutorDriver(task)
	parent, domain, parentSlot, ownerEpoch, physicalOK :=
		coroNativeMCurrentOwnerV1(driver)
	if !ownerOK || !physicalOK ||
		!coro.DetachExecutorResume(&parent.resume, driver, task) {
		coroRuntimeAbort("locked-thread foreign call cannot detach active resume")
	}
	baton, begun := parent.handoff.Begin(ownerEpoch)
	if !begun {
		if !coro.RestoreExecutorResume(&parent.resume) {
			coroRuntimeAbort("locked-thread foreign call handoff rollback failed")
		}
		coroRuntimeAbort("locked-thread foreign call cannot begin domain handoff")
	}
	childSlot, child, allocated := coroNativeMAllocateReplacementV1(
		parentSlot,
		domain.handle,
		baton,
	)
	if !allocated {
		if parent.handoff.RequestReturn(baton) !=
			coro.ExecutionDomainHandoffReturnUnclaimed ||
			!parent.handoff.Complete(baton) ||
			!coro.RestoreExecutorResume(&parent.resume) {
			coroRuntimeAbort("locked-thread foreign call allocation rollback failed")
		}
		coroRuntimeAbort("locked-thread foreign call exhausted M directory")
	}
	if !coroTargetReleaseManagedExecutionV1(driver) {
		coroRuntimeAbort("locked-thread foreign call cannot release managed execution permit")
	}
	if corofleet.CreateOwner(&child.thread, childSlot) != 0 || child.thread == nil {
		child.thread = nil
		if parent.handoff.RequestReturn(baton) !=
			coro.ExecutionDomainHandoffReturnUnclaimed ||
			!parent.handoff.Complete(baton) ||
			!coroNativeMReleaseUnstartedReplacementV1(childSlot) ||
			!coroTargetReenterManagedExecutionV1(driver) ||
			!coro.RestoreExecutorResume(&parent.resume) {
			coroRuntimeAbort("locked-thread foreign call pthread rollback failed")
		}
		coroRuntimeAbort("locked-thread foreign call cannot create replacement M")
	}
	args := [coroworker.MaxArgs]uintptr{a0, a1, a2, a3, a4, a5, a6, a7, a8}
	var result coroworker.Result
	callOK := coroworker.Call(function, argc, &args, &result)
	returnResult := parent.handoff.RequestReturn(baton)
	ringOK := returnResult == coro.ExecutionDomainHandoffReturnClaimed &&
		domain.doorbell.Ring()
	for ringOK && !parent.handoff.Returned(baton) {
		if corofleet.Yield() != 0 {
			ringOK = false
		}
	}
	returnedSlot, returnedOwner, returned := coroNativeMReplacementLineageOwnerV1(
		childSlot,
		child,
		parent,
		baton,
	)
	var threadResult c.Pointer
	joinOK := returned && returnedOwner.thread != nil &&
		pthread.Join(returnedOwner.thread, &threadResult) == 0 &&
		threadResult == nil
	returned = returned && joinOK &&
		coroNativeMOwnerLifecycleLoadV1(returnedOwner) == coroNativeMOwnerReturnedV1 &&
		coroNativeAtomicLoadV1(
			&coroNativeMDirectoryV1State.active[domain.handle.Route-1],
		) == parentSlot
	// Recycle while the parent baton still proves Returned. Completing the
	// baton first would erase the exact lineage identity and make a scalar slot
	// alone insufficient authority to return this storage to the free pool.
	if !ringOK || !joinOK || !returned ||
		!coroNativeMRecycleReplacementV1(returnedSlot) ||
		!parent.handoff.Complete(baton) {
		coroRuntimeAbort("locked-thread foreign call replacement join failed")
	}
	if !coroTargetReenterManagedExecutionV1(driver) ||
		!coro.RestoreExecutorResume(&parent.resume) {
		coroRuntimeAbort("locked-thread foreign call cannot reacquire managed execution")
	}
	if !callOK {
		coroRuntimeAbort("locked-thread foreign call failed")
	}
	*r1, *r2, *errno = result.R1, result.R2, result.Errno
}
