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

// Package corofleet is the fixed native-thread creation leaf for the
// stackless scheduler. It accepts no function pointer, callback address, G,
// P, or LLVM coroutine handle: the C adapter has one statically linked owner
// routine and that routine enters one compiler-validated raw Go ABI.
package corofleet

import (
	_ "unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/clite/pthread"
)

// CreatePeer starts exactly one joinable scheduler peer with default pthread
// attributes. Thread creation may enter libc, allocator, or collector locks;
// sync records same-thread return without claiming a bounded nonblocking leaf.
//
//llgo:coro sync
//go:linkname CreatePeer C.__llgo_coro_fleet_owner_create_v1
func CreatePeer(thread *pthread.Thread) c.Int
