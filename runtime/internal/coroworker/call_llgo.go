//go:build llgo && (darwin || linux) && !baremetal

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

// Package coroworker contains the native, worker-thread-only foreign-call
// boundary used by the stackless scheduler. It deliberately has no scheduler,
// callback, payload, admission, or Go pointer policy; those remain in
// runtime/internal/coro and runtime. Its C11 queue contains only POD syscall
// words and is the native adapter's bounded transport, not a second scheduler.
// Keeping this leaf separate makes it impossible for a target adapter to grow
// another event loop around one blocking call.
package coroworker

import (
	_ "unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/clite/pthread"
)

// Create starts exactly one scheduler-owned native worker with default pthread
// attributes. The C adapter owns the fixed routine: it waits and performs the
// foreign call entirely on the native stack, then crosses into Go through the
// one POD completion export. No arbitrary Go callback address is accepted.
// pthread_create may still acquire libc, allocator, or collector locks; sync
// records same-thread return without promising lock-free or bounded latency.
//
//llgo:coro sync
//go:linkname Create C.__llgo_coro_worker_create_v1
func Create(thread *pthread.Thread) c.Int

// QueueInit constructs the one native worker transport. Initialization may
// enter the platform semaphore implementation, so it is scheduler-owner sync,
// not managed ingress and not a noblock leaf.
//
//llgo:coro sync
//go:linkname QueueInit C.__llgo_coro_worker_queue_init_v1
func QueueInit() bool

// QueueCanRelease is the exact zero-lifecycle query used before initialization.
//
//llgo:coro sync
//go:linkname QueueCanRelease C.__llgo_coro_worker_queue_can_release_v1
func QueueCanRelease() bool

// QueueReserve owns the next free sequence slot without publishing it. The
// caller is the single owner P, all participating atomics were proved lock-free
// by QueueInit, and no OS wait or callback is reachable from this leaf.
//
//llgo:coro noblock
//go:linkname QueueReserve C.__llgo_coro_worker_queue_reserve_v1
func QueueReserve() bool

// QueueCancelReservation rolls back the only unpublished owner reservation.
//
//llgo:coro noblock
//go:linkname QueueCancelReservation C.__llgo_coro_worker_queue_cancel_reservation_v1
func QueueCancelReservation() bool

// QueueSubmitReserved release-publishes one POD job and emits one platform
// semaphore signal. sem_post and Mach semaphore_signal never wait for worker
// progress; full capacity was already rejected by QueueReserve.
//
//llgo:coro noblock
//go:linkname QueueSubmitReserved C.__llgo_coro_worker_queue_submit_reserved_v1
func QueueSubmitReserved(job *Job) bool

// QueueStop seals producer ingress and emits one terminal wake per raw worker.
// It neither drains nor joins workers, but its platform signals may enter libc
// or the kernel and are therefore kept on the scheduler-owner sync path.
//
//llgo:coro sync
//go:linkname QueueStop C.__llgo_coro_worker_queue_stop_v1
func QueueStop(workerCount uint32) bool

// QueueDestroyAfterJoin releases the native semaphore and resets all queue
// atomics. The caller must have joined every worker and the C leaf independently
// verifies that every published position was consumed. It performs no wait,
// join, callback, or managed coroutine operation itself.
//
//llgo:coro sync
//go:linkname QueueDestroyAfterJoin C.__llgo_coro_worker_queue_destroy_after_join_v1
func QueueDestroyAfterJoin() bool
