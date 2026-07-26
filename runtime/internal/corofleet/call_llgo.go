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
// joinable scheduler owner with default pthread attributes. slot is a small
// scalar pthread argument, never a Go pointer. In particular, a locked M never
// creates its own replacement and cannot propagate a modified signal mask,
// namespace, cwd/fs view, credentials, or similar inherited state.
//
//llgo:coro sync
//go:linkname CreateOwner C.__llgo_coro_fleet_owner_create_v2
func CreateOwner(thread *pthread.Thread, slot uint32) c.Int

// StopFactory seals the scalar request rendezvous and strongly joins the clean
// template after every scheduler owner has returned and been joined.
//
//llgo:coro sync
//go:linkname StopFactory C.__llgo_coro_fleet_factory_stop_v1
func StopFactory() c.Int
