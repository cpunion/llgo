//go:build !windows

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

// Package thread exposes the hosted runtime's native thread and TLS backend.
package thread

import (
	_ "unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
)

const LLGoPackage = "link"

//llgo:type C
type RoutineFunc func(c.Pointer) c.Pointer

//llgo:type C
type KeyDestructor func(c.Pointer)

type nativeThread *struct {
	unused [8]byte
}

// Thread is the native identity returned by pthread creation and lookup.
// It is exposed only so audited runtime adapters can join or compare their
// compiler-owned host threads; ordinary Go code uses CreateDetached.
type Thread = nativeThread

// Self and Equal only inspect the calling thread's native identity. Their
// live coroutine-runtime uses belong to compiler-proved raw-host closures, so
// they do not need a declaration-wide scheduling exception.
//
//go:linkname Self C.pthread_self
func Self() Thread

//go:linkname Equal C.pthread_equal
func Equal(left, right Thread) c.Int

// pthread_attr_t is opaque and varies across Unix targets. Sixteen pointer
// slots preserve the existing conservative storage bound and native alignment.
type nativeAttr struct {
	_ [16]uintptr
}

const pthreadCreateDetached = 1

// llgo:link (*nativeAttr).Init C.pthread_attr_init
func (attr *nativeAttr) Init() c.Int { return 0 }

// llgo:link (*nativeAttr).Destroy C.pthread_attr_destroy
func (attr *nativeAttr) Destroy() c.Int { return 0 }

// llgo:link (*nativeAttr).SetDetached C.pthread_attr_setdetachstate
func (attr *nativeAttr) SetDetached(detached c.Int) c.Int { return 0 }

// llgo:link (*nativeAttr).SetStackSize C.pthread_attr_setstacksize
func (attr *nativeAttr) SetStackSize(stackSize uintptr) c.Int { return 0 }

//go:linkname pthreadExit C.pthread_exit
func pthreadExit(retval c.Pointer)

// CreateDetached starts a detached host thread using the native pthread
// backend. GC-enabled builds select the collector-aware create entry point.
func CreateDetached(stackSize uintptr, routine RoutineFunc, arg c.Pointer) c.Int {
	var attr nativeAttr
	if ret := attr.Init(); ret != 0 {
		return ret
	}
	if ret := attr.SetDetached(pthreadCreateDetached); ret != 0 {
		_ = attr.Destroy()
		return ret
	}
	if stackSize != 0 {
		if ret := attr.SetStackSize(stackSize); ret != 0 {
			_ = attr.Destroy()
			return ret
		}
	}
	var native nativeThread
	ret := create(&native, &attr, routine, arg)
	// Once create succeeds, the caller-owned context belongs to the detached
	// thread. An attribute destroy failure cannot be reported as create failure.
	_ = attr.Destroy()
	return ret
}

func Exit() {
	pthreadExit(nil)
}

// Join waits for a joinable runtime-owned host thread. Every live coroutine
// occurrence is in an audited raw-host shutdown closure; managed callers keep
// the foreign-wait effect inferred from the selected native declaration.
func Join(native Thread, retval *c.Pointer) c.Int {
	return join(native, retval)
}

type Key c.Uint

// Create only allocates host TLS metadata and returns synchronously.
//
// llgo:link (*Key).Create C.pthread_key_create
//
//llgo:coro sync
func (key *Key) Create(destructor KeyDestructor) c.Int { return 0 }

// llgo:link Key.Delete C.pthread_key_delete
func (key Key) Delete() c.Int { return 0 }

// Get and Set are executor-local TLS accesses on the scheduler hot path.
//
// llgo:link Key.Get C.pthread_getspecific
//
//llgo:coro noblock
func (key Key) Get() c.Pointer { return nil }

// llgo:link Key.Set C.pthread_setspecific
//
//llgo:coro noblock
func (key Key) Set(value c.Pointer) c.Int { return 0 }
