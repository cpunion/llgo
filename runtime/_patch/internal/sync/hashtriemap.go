// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license.
// See LICENSES/Go-BSD-3-Clause.txt at this module root for license terms.

//go:build go1.26

//llgo:skipall
package sync

import (
	"sync/atomic"
	"unsafe"
)

type hashFunc func(unsafe.Pointer, uintptr) uintptr
type equalFunc func(unsafe.Pointer, unsafe.Pointer) bool

const (
	nChildrenLog2 = 4
	nChildren     = 1 << nChildrenLog2
)

type node[K comparable, V any] struct {
	isEntry bool
}

type indirect[K comparable, V any] struct {
	node[K, V]
	dead     atomic.Bool
	mu       Mutex
	parent   *indirect[K, V]
	children [nChildren]atomic.Pointer[node[K, V]]
}

type entry[K comparable, V any] struct {
	node[K, V]
	overflow atomic.Pointer[entry[K, V]]
	key      K
	value    V
}

var hashTrieExpungedSentinel byte

func hashTrieExpunged[V any]() *hashTrieBox[V] {
	return (*hashTrieBox[V])(unsafe.Pointer(&hashTrieExpungedSentinel))
}

type hashTrieBox[V any] struct {
	v V
}

type hashTrieSlot[V any] struct {
	p atomic.Pointer[hashTrieBox[V]]
}

func (e *hashTrieSlot[V]) load() (value V, ok bool) {
	p := e.p.Load()
	if p == nil || p == hashTrieExpunged[V]() {
		return value, false
	}
	return p.v, true
}

func (e *hashTrieSlot[V]) trySwap(i *hashTrieBox[V]) (*hashTrieBox[V], bool) {
	expunged := hashTrieExpunged[V]()
	for {
		p := e.p.Load()
		if p == expunged {
			return nil, false
		}
		if e.p.CompareAndSwap(p, i) {
			return p, true
		}
	}
}

func (e *hashTrieSlot[V]) swapLocked(i *hashTrieBox[V]) *hashTrieBox[V] {
	return e.p.Swap(i)
}

func (e *hashTrieSlot[V]) unexpungeLocked() bool {
	return e.p.CompareAndSwap(hashTrieExpunged[V](), nil)
}

func (e *hashTrieSlot[V]) tryExpungeLocked() bool {
	expunged := hashTrieExpunged[V]()
	p := e.p.Load()
	for p == nil {
		if e.p.CompareAndSwap(nil, expunged) {
			return true
		}
		p = e.p.Load()
	}
	return p == expunged
}

func (e *hashTrieSlot[V]) tryLoadOrStore(v V) (actual V, loaded, ok bool) {
	expunged := hashTrieExpunged[V]()
	p := e.p.Load()
	if p == expunged {
		return actual, false, false
	}
	if p != nil {
		return p.v, true, true
	}
	box := &hashTrieBox[V]{v: v}
	for {
		if e.p.CompareAndSwap(nil, box) {
			return v, false, true
		}
		p = e.p.Load()
		if p == expunged {
			return actual, false, false
		}
		if p != nil {
			return p.v, true, true
		}
	}
}

func (e *hashTrieSlot[V]) delete() (value V, ok bool) {
	expunged := hashTrieExpunged[V]()
	for {
		p := e.p.Load()
		if p == nil || p == expunged {
			return value, false
		}
		if e.p.CompareAndSwap(p, nil) {
			return p.v, true
		}
	}
}

func (e *hashTrieSlot[V]) tryCompareAndSwap(old, new V) bool {
	expunged := hashTrieExpunged[V]()
	p := e.p.Load()
	if p == nil || p == expunged || !hashTrieValueEqual(p.v, old) {
		return false
	}
	box := &hashTrieBox[V]{v: new}
	for {
		if e.p.CompareAndSwap(p, box) {
			return true
		}
		p = e.p.Load()
		if p == nil || p == expunged || !hashTrieValueEqual(p.v, old) {
			return false
		}
	}
}

type hashTrieRead[K comparable, V any] struct {
	m       map[K]*hashTrieSlot[V]
	amended bool
}

// HashTrieMap is a compatibility implementation for llgo.
// It preserves the zero-value and concurrency semantics expected by sync.Map
// with lock-free O(1) reads and in-place updates on steady-state keys via an
// atomic read-only map snapshot backed by a mutex-protected dirty map.
type HashTrieMap[K comparable, V any] struct {
	// Keep the leading field layout compatible with the upstream type so
	// imported stdlib code that still selects these fields by index can build.
	inited   atomic.Uint32
	initMu   Mutex
	root     atomic.Pointer[indirect[K, V]]
	keyHash  hashFunc
	valEqual equalFunc
	seed     uintptr

	mu     Mutex
	read   atomic.Pointer[hashTrieRead[K, V]]
	dirty  map[K]*hashTrieSlot[V]
	misses int
}

func (ht *HashTrieMap[K, V]) loadReadOnly() hashTrieRead[K, V] {
	if p := ht.read.Load(); p != nil {
		return *p
	}
	return hashTrieRead[K, V]{}
}

func (ht *HashTrieMap[K, V]) missLocked() {
	ht.misses++
	if ht.misses < len(ht.dirty) {
		return
	}
	ht.read.Store(&hashTrieRead[K, V]{m: ht.dirty})
	ht.dirty = nil
	ht.misses = 0
}

func (ht *HashTrieMap[K, V]) dirtyLocked() {
	if ht.dirty != nil {
		return
	}
	read := ht.loadReadOnly()
	ht.dirty = make(map[K]*hashTrieSlot[V], len(read.m))
	for k, e := range read.m {
		if !e.tryExpungeLocked() {
			ht.dirty[k] = e
		}
	}
}

func (ht *HashTrieMap[K, V]) Load(key K) (value V, ok bool) {
	read := ht.loadReadOnly()
	e, ok := read.m[key]
	if !ok && read.amended {
		ht.mu.Lock()
		read = ht.loadReadOnly()
		e, ok = read.m[key]
		if !ok && read.amended {
			e, ok = ht.dirty[key]
			ht.missLocked()
		}
		ht.mu.Unlock()
	}
	if !ok {
		return value, false
	}
	return e.load()
}

func (ht *HashTrieMap[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	read := ht.loadReadOnly()
	if e, ok := read.m[key]; ok {
		if actual, loaded, ok := e.tryLoadOrStore(value); ok {
			return actual, loaded
		}
	}
	ht.mu.Lock()
	read = ht.loadReadOnly()
	if e, ok := read.m[key]; ok {
		if e.unexpungeLocked() {
			ht.dirty[key] = e
		}
		actual, loaded, _ = e.tryLoadOrStore(value)
	} else if e, ok := ht.dirty[key]; ok {
		actual, loaded, _ = e.tryLoadOrStore(value)
		ht.missLocked()
	} else {
		if !read.amended {
			ht.dirtyLocked()
			ht.read.Store(&hashTrieRead[K, V]{m: read.m, amended: true})
		}
		slot := &hashTrieSlot[V]{}
		slot.p.Store(&hashTrieBox[V]{v: value})
		ht.dirty[key] = slot
		actual, loaded = value, false
	}
	ht.mu.Unlock()
	return actual, loaded
}

func (ht *HashTrieMap[K, V]) Store(key K, value V) {
	_, _ = ht.Swap(key, value)
}

func (ht *HashTrieMap[K, V]) Swap(key K, new V) (previous V, loaded bool) {
	read := ht.loadReadOnly()
	if e, ok := read.m[key]; ok {
		if prev, ok := e.trySwap(&hashTrieBox[V]{v: new}); ok {
			if prev == nil {
				return previous, false
			}
			return prev.v, true
		}
	}
	ht.mu.Lock()
	read = ht.loadReadOnly()
	box := &hashTrieBox[V]{v: new}
	if e, ok := read.m[key]; ok {
		if e.unexpungeLocked() {
			ht.dirty[key] = e
		}
		if prev := e.swapLocked(box); prev != nil {
			previous, loaded = prev.v, true
		}
	} else if e, ok := ht.dirty[key]; ok {
		if prev := e.swapLocked(box); prev != nil {
			previous, loaded = prev.v, true
		}
	} else {
		if !read.amended {
			ht.dirtyLocked()
			ht.read.Store(&hashTrieRead[K, V]{m: read.m, amended: true})
		}
		slot := &hashTrieSlot[V]{}
		slot.p.Store(box)
		ht.dirty[key] = slot
	}
	ht.mu.Unlock()
	return previous, loaded
}

func (ht *HashTrieMap[K, V]) CompareAndSwap(key K, old, new V) bool {
	read := ht.loadReadOnly()
	if e, ok := read.m[key]; ok {
		return e.tryCompareAndSwap(old, new)
	} else if !read.amended {
		return false
	}
	ht.mu.Lock()
	defer ht.mu.Unlock()
	read = ht.loadReadOnly()
	swapped := false
	if e, ok := read.m[key]; ok {
		swapped = e.tryCompareAndSwap(old, new)
	} else if e, ok := ht.dirty[key]; ok {
		swapped = e.tryCompareAndSwap(old, new)
		ht.missLocked()
	}
	return swapped
}

func (ht *HashTrieMap[K, V]) LoadAndDelete(key K) (value V, loaded bool) {
	read := ht.loadReadOnly()
	e, ok := read.m[key]
	if !ok && read.amended {
		ht.mu.Lock()
		read = ht.loadReadOnly()
		e, ok = read.m[key]
		if !ok && read.amended {
			e, ok = ht.dirty[key]
			delete(ht.dirty, key)
			ht.missLocked()
		}
		ht.mu.Unlock()
	}
	if ok {
		return e.delete()
	}
	return value, false
}

func (ht *HashTrieMap[K, V]) Delete(key K) {
	_, _ = ht.LoadAndDelete(key)
}

func (ht *HashTrieMap[K, V]) CompareAndDelete(key K, old V) (deleted bool) {
	read := ht.loadReadOnly()
	e, ok := read.m[key]
	if !ok && read.amended {
		ht.mu.Lock()
		read = ht.loadReadOnly()
		e, ok = read.m[key]
		if !ok && read.amended {
			e, ok = ht.dirty[key]
			ht.missLocked()
		}
		ht.mu.Unlock()
	}
	expunged := hashTrieExpunged[V]()
	for ok {
		p := e.p.Load()
		if p == nil || p == expunged || !hashTrieValueEqual(p.v, old) {
			return false
		}
		if e.p.CompareAndSwap(p, nil) {
			return true
		}
	}
	return false
}

func (ht *HashTrieMap[K, V]) All() func(yield func(K, V) bool) {
	return func(yield func(K, V) bool) {
		read := ht.loadReadOnly()
		if read.amended {
			ht.mu.Lock()
			read = ht.loadReadOnly()
			if read.amended {
				read = hashTrieRead[K, V]{m: ht.dirty}
				copyRead := read
				ht.read.Store(&copyRead)
				ht.dirty = nil
				ht.misses = 0
			}
			ht.mu.Unlock()
		}
		for k, e := range read.m {
			v, ok := e.load()
			if !ok {
				continue
			}
			if !yield(k, v) {
				break
			}
		}
	}
}

func (ht *HashTrieMap[K, V]) Range(yield func(K, V) bool) {
	ht.All()(yield)
}

func (ht *HashTrieMap[K, V]) Clear() {
	read := ht.loadReadOnly()
	if len(read.m) == 0 && !read.amended {
		return
	}
	ht.mu.Lock()
	defer ht.mu.Unlock()
	read = ht.loadReadOnly()
	expunged := hashTrieExpunged[V]()
	if len(read.m) > 0 || read.amended {
		ht.read.Store(&hashTrieRead[K, V]{})
	}
	for _, e := range read.m {
		e.p.Swap(expunged)
	}
	for _, e := range ht.dirty {
		e.p.Swap(expunged)
	}
	ht.dirty = nil
	ht.misses = 0
}

func hashTrieValueEqual[V any](a, b V) bool {
	// Match upstream sync.Map CompareAndSwap/CompareAndDelete behavior:
	// non-comparable values panic during equality comparison.
	return any(a) == any(b)
}
