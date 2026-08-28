//go:build llgo && !baremetal && !wasm && !tinygo.wasm

/*
 * Copyright (c) 2025 The XGo Authors (xgo.dev). All rights reserved.
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

// Package tls provides generic storage backed by the host thread-local
// storage API. When built with the GC-enabled configuration (llgo && !nogc),
// TLS slots are automatically registered with the BDWGC garbage collector so
// pointers stored in thread-local state remain visible to the collector.
// Builds without GC integration (llgo && nogc) simply use host TLS without
// root registration.
//
// Basic usage:
//
//	h := tls.Alloc[int](nil)
//	h.Set(42)
//	val := h.Get() // returns 42
//
// With destructor:
//
//	h := tls.Alloc[*Resource](func(r **Resource) {
//	    if r != nil && *r != nil {
//	        (*r).Close()
//	    }
//	})
//
// Build tags:
//   - llgo && !nogc: Enables GC-aware slot registration via BDWGC
//   - llgo && nogc:  Disables GC integration; TLS acts as plain host TLS
//
// Logical WebAssembly targets use the single-host-thread implementation in
// tls_webassembly.go and never acquire a host-thread dependency merely because a
// named target reuses a Linux/ARM frontend.
package tls

import (
	"unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	"github.com/xgo-dev/llgo/runtime/internal/thread"
)

type Handle[T any] struct {
	key        thread.Key
	destructor func(*T)
}

// StaticHandle is a TLS handle whose thread-exit cleanup has no dynamic Go
// callback. It is suitable for caches and runtime bookkeeping that only need
// their GC root and slot allocation released. Keeping it distinct from Handle
// also lets the compiler prove its C callback closure without function-value
// dispatch.
type StaticHandle[T any] struct {
	key thread.Key
}

// Alloc creates a handle backed by the host thread-local storage API.
func Alloc[T any](destructor func(*T)) Handle[T] {
	var key thread.Key
	if ret := key.Create(thread.KeyDestructor(slotDestructor[T])); ret != 0 {
		c.Fprintf(c.Stderr, c.Str("tls: thread-local key creation failed (error=%d)\n"), ret)
		panic("tls: failed to create thread local storage key")
	}
	return Handle[T]{key: key, destructor: destructor}
}

// AllocStatic creates a TLS handle with fixed, callback-free cleanup.
func AllocStatic[T any]() StaticHandle[T] {
	var key thread.Key
	if ret := key.Create(thread.KeyDestructor(staticSlotDestructor[T])); ret != 0 {
		c.Fprintf(c.Stderr, c.Str("tls: thread-local key creation failed (error=%d)\n"), ret)
		panic("tls: failed to create thread local storage key")
	}
	return StaticHandle[T]{key: key}
}

// Get returns the value stored in the current thread's slot.
func (h Handle[T]) Get() T {
	return get[T](h.key)
}

// Set stores v in the current thread's slot, creating it if necessary.
func (h Handle[T]) Set(v T) {
	set(h.key, h.destructor, v)
}

// Clear zeroes the current thread's slot value without freeing the slot.
func (h Handle[T]) Clear() {
	clear[T](h.key)
}

func (h StaticHandle[T]) Get() T {
	return get[T](h.key)
}

func (h StaticHandle[T]) Set(v T) {
	set(h.key, nil, v)
}

func (h StaticHandle[T]) Clear() {
	clear[T](h.key)
}

func get[T any](key thread.Key) T {
	if ptr := key.Get(); ptr != nil {
		return (*slot[T])(ptr).value
	}
	var zero T
	return zero
}

func set[T any](key thread.Key, destructor func(*T), value T) {
	s := ensureSlot(key, destructor)
	s.value = value
}

func clear[T any](key thread.Key) {
	if ptr := key.Get(); ptr != nil {
		s := (*slot[T])(ptr)
		var zero T
		s.value = zero
	}
}

func ensureSlot[T any](key thread.Key, destructor func(*T)) *slot[T] {
	if ptr := key.Get(); ptr != nil {
		return (*slot[T])(ptr)
	}
	size := unsafe.Sizeof(slot[T]{})
	mem := c.Calloc(1, size)
	if mem == nil {
		panic("tls: failed to allocate thread slot")
	}
	s := (*slot[T])(mem)
	s.destructor = destructor
	if existing := key.Get(); existing != nil {
		c.Free(mem)
		return (*slot[T])(existing)
	}
	if ret := key.Set(mem); ret != 0 {
		c.Free(mem)
		c.Fprintf(c.Stderr, c.Str("tls: thread-local value installation failed (error=%d)\n"), ret)
		panic("tls: failed to set thread local storage value")
	}
	registerSlot(s)
	return s
}

func slotDestructor[T any](ptr c.Pointer) {
	s := (*slot[T])(ptr)
	if s == nil {
		return
	}
	if s.destructor != nil {
		s.destructor(&s.value)
	}
	releaseSlot(ptr, s)
}

func staticSlotDestructor[T any](ptr c.Pointer) {
	s := (*slot[T])(ptr)
	if s == nil {
		return
	}
	releaseSlot(ptr, s)
}

func releaseSlot[T any](ptr c.Pointer, s *slot[T]) {
	deregisterSlot(s)
	var zero T
	s.value = zero
	s.destructor = nil
	c.Free(ptr)
}
