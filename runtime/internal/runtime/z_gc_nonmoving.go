//go:build (baremetal && !nogc) || (wasm && llgo.wasm.gc.linear)

/*
 * Copyright (c) 2024 The XGo Authors (xgo.dev). All rights reserved.
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

	"github.com/xgo-dev/llgo/runtime/internal/runtime/tinygogc"
	psync "github.com/xgo-dev/llgo/runtime/internal/sync"
	"github.com/xgo-dev/llgo/runtime/internal/sync/atomic"
)

func AllocU(size uintptr) unsafe.Pointer {
	ret := tinygogc.Alloc(size)
	recordMemProfileAlloc(size)
	return ret
}

func AllocZ(size uintptr) unsafe.Pointer {
	ret := tinygogc.Alloc(size)
	recordMemProfileAlloc(size)
	return ret
}

func AllocRoot(size uintptr) unsafe.Pointer {
	return tinygogc.Alloc(size)
}

func FreeRoot(ptr unsafe.Pointer) {
	tinygogc.Free(ptr)
}

type cleanupEntry struct {
	fn     func()
	cancel func()
	slot   *cleanupSlot
	id     uint64
	state  int32
}

const (
	cleanupActive int32 = iota
	cleanupStopped
	cleanupRunning
	cleanupDone
)

type cleanupSlot struct {
	entry      unsafe.Pointer
	nextFree   unsafe.Pointer
	index      uint32
	generation uint32
}

var cleanupSlots struct {
	once psync.Once
	mu   psync.Mutex
	all  []*cleanupSlot
	free unsafe.Pointer
}

func initCleanupSlots() {
	cleanupSlots.mu.Init(nil)
}

func freeCleanupSlot(e *cleanupEntry) {
	slot := e.slot
	if _, ok := atomic.CompareAndExchange(&slot.entry, unsafe.Pointer(e), nil); !ok {
		return
	}
	for {
		head := atomic.Load(&cleanupSlots.free)
		atomic.Store(&slot.nextFree, head)
		if _, ok := atomic.CompareAndExchange(&cleanupSlots.free, head, unsafe.Pointer(slot)); ok {
			return
		}
	}
}

func popCleanupSlot() *cleanupSlot {
	for {
		head := atomic.Load(&cleanupSlots.free)
		if head == nil {
			return nil
		}
		slot := (*cleanupSlot)(head)
		next := atomic.Load(&slot.nextFree)
		if _, ok := atomic.CompareAndExchange(&cleanupSlots.free, head, next); ok {
			atomic.Store(&slot.nextFree, nil)
			return slot
		}
	}
}

func newCancelableCleanup(cleanup func()) *cleanupEntry {
	cleanupSlots.once.Do(initCleanupSlots)
	cleanupSlots.mu.Lock()
	slot := popCleanupSlot()
	if slot == nil {
		if uint64(len(cleanupSlots.all)) >= uint64(^uint32(0)) {
			cleanupSlots.mu.Unlock()
			panic("runtime: too many pending cleanups")
		}
		slot = &cleanupSlot{index: uint32(len(cleanupSlots.all))}
		cleanupSlots.all = append(cleanupSlots.all, slot)
	}
	slot.generation++
	if slot.generation == 0 {
		slot.generation++
	}
	id := uint64(slot.generation)<<32 | uint64(slot.index+1)
	e := &cleanupEntry{fn: cleanup, slot: slot, id: id}
	atomic.Store(&slot.entry, unsafe.Pointer(e))
	cleanupSlots.mu.Unlock()
	return e
}

func registerCleanupPtr(ptr unsafe.Pointer, e *cleanupEntry) bool {
	var registered bool
	e.cancel, registered = tinygogc.AddCleanup(ptr, func(unsafe.Pointer) {
		_, run := atomic.CompareAndExchange(&e.state, cleanupActive, cleanupRunning)
		if run {
			e.fn()
		}
		atomic.Store(&e.state, cleanupDone)
		if e.id != 0 {
			freeCleanupSlot(e)
		}
		e.fn = nil
		e.cancel = nil
	})
	return registered
}

// AddCleanupPtr attaches cleanup to ptr without retaining ptr itself.
func AddCleanupPtr(ptr unsafe.Pointer, cleanup func()) (cancel func()) {
	e := &cleanupEntry{fn: cleanup}
	if !registerCleanupPtr(ptr, e) {
		panic("runtime.AddCleanup: pointer not in allocated block")
	}
	return func() {
		if _, stopped := atomic.CompareAndExchange(&e.state, cleanupActive, cleanupStopped); stopped {
			e.cancel()
			e.fn = nil
			e.cancel = nil
		}
	}
}

func AddCancelableCleanupPtr(ptr unsafe.Pointer, cleanup func()) uint64 {
	e := newCancelableCleanup(cleanup)
	if !registerCleanupPtr(ptr, e) {
		atomic.Store(&e.state, cleanupStopped)
		freeCleanupSlot(e)
		panic("runtime.AddCleanup: pointer not in allocated block")
	}
	return e.id
}

func StopCleanupPtr(id uint64) {
	if id == 0 {
		return
	}
	cleanupSlots.once.Do(initCleanupSlots)
	index := uint64(uint32(id) - 1)
	cleanupSlots.mu.Lock()
	if index < uint64(len(cleanupSlots.all)) {
		slot := cleanupSlots.all[index]
		if e := (*cleanupEntry)(atomic.Load(&slot.entry)); e != nil && e.id == id {
			if _, stopped := atomic.CompareAndExchange(&e.state, cleanupActive, cleanupStopped); stopped {
				e.cancel()
				e.fn = nil
				e.cancel = nil
				freeCleanupSlot(e)
			}
		}
	}
	cleanupSlots.mu.Unlock()
}
