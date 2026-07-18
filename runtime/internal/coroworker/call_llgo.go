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
// queue, callback, or Go pointer policy; those remain in runtime/internal/coro
// and runtime. Keeping this leaf separate makes it impossible for a target
// adapter to grow a second event loop around one blocking call.
package coroworker

import _ "unsafe"

const (
	LLGoFiles   = "_worker/worker.c"
	LLGoPackage = "link"
)

// Call invokes one uintptr-shaped foreign function on the current native
// worker thread and captures errno before returning. The declaration is
// noblock in coroutine-effect terms because this leaf is legal only inside a
// scheduler-owned plain worker routine; it must never execute on an executor
// P or inside an LLVM coroutine body.
//
//llgo:coro noblock
//go:linkname Call C.__llgo_coro_worker_call_v1
func Call(fn uintptr, argc uint32, args *[MaxArgs]uintptr, result *Result) bool
