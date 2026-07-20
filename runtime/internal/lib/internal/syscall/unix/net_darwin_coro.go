//go:build darwin && go1.26

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

package unix

import _ "unsafe"

// Go 1.26's Darwin cgo resolver passes these exact FuncPCABI0 targets through
// syscall's private fixed-width carriers. Each alternate declaration publishes
// the carrier width before the function becomes a uintptr. The operations have
// no caller-thread affinity or managed callback, and pointer arguments remain
// rooted by the suspended managed caller until worker completion.

//llgo:coro workeraddr 6
//go:linkname libc_getaddrinfo_trampoline C.getaddrinfo
func libc_getaddrinfo_trampoline()

//llgo:coro workeraddr 6
//go:linkname libc_freeaddrinfo_trampoline C.freeaddrinfo
func libc_freeaddrinfo_trampoline()

//llgo:coro workeraddr 9
//go:linkname libc_getnameinfo_trampoline C.getnameinfo
func libc_getnameinfo_trampoline()

// gai_strerror returns a process-lifetime C string. Its r1 result is therefore
// safe to reconstruct after worker completion but is not a Go heap root.
//
//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=none memory=borrow-until-complete abi=word-call.v1/3+foreign-pointer-result=r1
//go:linkname libc_gai_strerror_trampoline C.gai_strerror
func libc_gai_strerror_trampoline()

//llgo:coro workeraddr 3
//go:linkname libresolv_res_9_ninit_trampoline C.libresolv_res_9_ninit
func libresolv_res_9_ninit_trampoline()

//llgo:coro workeraddr 3
//go:linkname libresolv_res_9_nclose_trampoline C.libresolv_res_9_nclose
func libresolv_res_9_nclose_trampoline()

//llgo:coro workeraddr 6
//go:linkname libresolv_res_9_nsearch_trampoline C.libresolv_res_9_nsearch
func libresolv_res_9_nsearch_trampoline()
