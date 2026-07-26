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

// Package corofleet is the bounded native-thread creation leaf for the
// stackless scheduler. It accepts no function pointer, callback address, G, P,
// or LLVM coroutine handle: the C adapter has one statically linked owner
// routine and passes only one stable scalar M-directory slot into one
// compiler-validated raw Go ABI.
package corofleet

import (
	_ "unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/clite/pthread"
)

// OwnerCount returns the initial logical execution limit clamped to
// [1, maximum]. It honors one positive decimal GOMAXPROCS environment value
// and otherwise uses the online CPU count. It does not size the fixed physical
// route topology.
//
//llgo:coro sync
//go:linkname OwnerCount C.__llgo_coro_fleet_owner_count_v1
func OwnerCount(maximum uint32) uint32

// StartFactory creates the process-lifetime clean template pthread before any
// managed Go code can alter inherited per-thread process state.
//
//llgo:coro sync
//go:linkname StartFactory C.__llgo_coro_fleet_factory_start_v1
func StartFactory() c.Int

// CreateOwner synchronously asks the clean template pthread to start one
// joinable scheduler owner with default pthread attributes. token is the
// generation-bound physical record identity used for exact join/release; slot
// remains the only scalar passed across C-to-Go. In particular, a locked M
// never creates its own replacement and cannot propagate a modified signal
// mask, namespace, cwd/fs view, credentials, or similar inherited state.
//
//llgo:coro sync
//go:linkname CreateOwner C.__llgo_coro_fleet_owner_create_v3
func CreateOwner(thread *pthread.Thread, token *uint32, slot uint32) c.Int

// TryReuseOwner assigns one acknowledged standby pthread to slot. Zero means
// reused, one means the bounded cache was empty, and every other value is an
// invariant failure. No pthread is created by this operation.
//
//llgo:coro schedulerwait
//go:linkname TryReuseOwner C.__llgo_coro_fleet_owner_try_reuse_v1
func TryReuseOwner(thread *pthread.Thread, token *uint32, slot uint32) c.Int

// OwnerReady completes CreateOwner only after the new raw owner has claimed
// its stable scalar directory slot and execution route.
//
//llgo:coro schedulerwait
//go:linkname OwnerReady C.__llgo_coro_fleet_owner_ready_v1
func OwnerReady(slot uint32) c.Int

// JoinOwner strongly joins one permanent owner and retires its exact physical
// record. The caller releases one SetMaxThreads reservation only after success.
//
//llgo:coro schedulerwait
//go:linkname JoinOwner C.__llgo_coro_fleet_owner_join_v1
func JoinOwner(thread pthread.Thread, token uint32) c.Int

// ReleaseOwner acknowledges that one temporary replacement has returned from
// Go and may be cached. Zero retains the live pthread in standby; one means
// the bounded cache was full and the adapter strongly joined it.
//
//llgo:coro schedulerwait
//go:linkname ReleaseOwner C.__llgo_coro_fleet_owner_release_v1
func ReleaseOwner(thread pthread.Thread, token, slot uint32) c.Int

// RetireSelf detaches a permanently tainted owner record before pthread_exit.
// The process entry thread has no factory record and never calls this leaf.
//
//llgo:coro noblock
//go:linkname RetireSelf C.__llgo_coro_fleet_owner_retire_self_v1
func RetireSelf(slot uint32) c.Int

// StopStandby terminates and strongly joins every cached raw M. joined is the
// exact number of physical thread reservations the target must release.
//
//llgo:coro sync
//go:linkname StopStandby C.__llgo_coro_fleet_owner_stop_standby_v1
func StopStandby(joined *uint32) c.Int

// Yield lets the close owner wait for an already-admitted succession lease
// without monopolizing a CPU while the clean successor finishes publication.
//
//llgo:coro schedulerwait
//go:linkname Yield C.__llgo_coro_fleet_owner_yield_v1
func Yield() c.Int

// StopFactory seals the scalar request rendezvous and strongly joins the clean
// template after every scheduler owner has returned and been joined.
// terminalOwnerToken is zero on the original program M. A clean program
// successor passes its exact token because its wrapper remains live only until
// the immediately following process exit.
//
//llgo:coro sync
//go:linkname StopFactory C.__llgo_coro_fleet_factory_stop_v2
func StopFactory(terminalOwnerToken uint32) c.Int
