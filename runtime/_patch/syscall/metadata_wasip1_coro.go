//go:build llgo && llgo_coro && wasip1

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

package syscall

// Go's WASI Preview 1 package uses these exact metadata operations during
// package initialization. They inspect or update descriptor metadata and do
// not wait for descriptor readiness. The //go:wasmimport signature and
// //go:noescape directive prove the physical import ABI and borrow lifetime;
// this source patch supplies only the irreducible progress fact which that
// signature cannot express.
//
// Blocking data-plane operations such as fd_read, fd_write, and sock_accept
// deliberately remain unannotated. LLGo's public syscall replacements route
// those through the coroutine host-operation adapter instead.
//
//llgo:annotate fd_fdstat_get coro noblock
//llgo:annotate fd_fdstat_set_flags coro noblock
//llgo:annotate fd_prestat_get coro noblock
//llgo:annotate fd_prestat_dir_name coro noblock
