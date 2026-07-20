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

// Package atomiccache contains the small lock-free publication primitives used
// by runtime metadata whose readers may be preempted. It deliberately owns no
// scheduler or native-thread state.
package atomiccache

import (
	"unsafe"
)

type pairEntry struct {
	first  unsafe.Pointer
	second unsafe.Pointer
	value  unsafe.Pointer
	next   *pairEntry
}

// PairTable is a monotonic cache keyed by an identity pair. Entries are fully
// initialized before publication and never removed, so a reader may retain and
// traverse any loaded snapshot across coroutine preemption.
type PairTable struct {
	head unsafe.Pointer // *pairEntry
}

func (table *PairTable) load() *pairEntry {
	return (*pairEntry)(loadPointer(&table.head))
}

func findPair(head *pairEntry, first, second unsafe.Pointer) unsafe.Pointer {
	for entry := head; entry != nil; entry = entry.next {
		if entry.first == first && entry.second == second {
			return entry.value
		}
	}
	return nil
}

func (table *PairTable) Find(first, second unsafe.Pointer) unsafe.Pointer {
	return findPair(table.load(), first, second)
}

// Intern returns the canonical value for (first, second). A failed CAS always
// causes a complete rescan of the new head snapshot before publication is
// retried, so racing publishers cannot both become canonical winners.
func (table *PairTable) Intern(first, second, value unsafe.Pointer) unsafe.Pointer {
	candidate := &pairEntry{first: first, second: second, value: value}
	for {
		head := table.load()
		if winner := findPair(head, first, second); winner != nil {
			return winner
		}
		candidate.next = head
		if compareAndSwapPointer(&table.head, unsafe.Pointer(head), unsafe.Pointer(candidate)) {
			return value
		}
	}
}

const weakBucketCount = 64

// WeakHandle is the stable object returned to weak-pointer callers. Key is a
// deliberately non-GC-visible address. Live is the sole field written by a raw
// collector callback; next is managed by WeakTable in ordinary managed code.
type WeakHandle struct {
	Key  uintptr
	Live uint32
	next unsafe.Pointer // *WeakHandle
}

// WeakTable interns live handles by address. Raw callbacks only tombstone a
// WeakHandle; managed callers opportunistically unlink tombstones.
type WeakTable struct {
	buckets [weakBucketCount]unsafe.Pointer // *WeakHandle
}

func (table *WeakTable) bucket(key uintptr) *unsafe.Pointer {
	index := ((key >> 3) ^ (key >> 13)) & (weakBucketCount - 1)
	return &table.buckets[index]
}

// PruneWeak removes currently visible tombstones from key's bucket. It is a
// managed operation and may be preempted at its traversal/retry backedges.
func (table *WeakTable) PruneWeak(key uintptr) {
	prune(table.bucket(key))
}

// prune removes tombstones from one bucket. Every link is atomic and removed
// nodes are left to the GC, so a preempted reader retaining an old node remains
// valid. Insertions change only the bucket head. A successful prune CAS replaces
// exactly one tombstoned successor with the successor it observed; competing
// changes to that same link make the CAS fail, while a CAS into an already
// detached predecessor cannot remove anything from the reachable chain. Thus
// pruning may leave a dead node for a later pass but cannot lose a live suffix.
// No raw callback enters this retry loop.
func prune(bucket *unsafe.Pointer) {
restart:
	link := bucket
	for {
		raw := loadPointer(link)
		if raw == nil {
			return
		}
		handle := (*WeakHandle)(raw)
		next := loadPointer(&handle.next)
		if loadUint32(&handle.Live) == 0 {
			if !compareAndSwapPointer(link, raw, next) {
				goto restart
			}
			continue
		}
		link = &handle.next
	}
}

// InternWeak publishes candidate only after scanning the exact head snapshot
// used by its CAS. published reports whether candidate won publication.
func (table *WeakTable) InternWeak(candidate *WeakHandle) (winner *WeakHandle, published bool) {
	bucket := table.bucket(candidate.Key)
	for {
		prune(bucket)
		head := loadPointer(bucket)
		for raw := head; raw != nil; {
			handle := (*WeakHandle)(raw)
			if handle.Key == candidate.Key && loadUint32(&handle.Live) != 0 {
				return handle, false
			}
			raw = loadPointer(&handle.next)
		}
		candidate.next = head
		if compareAndSwapPointer(bucket, head, unsafe.Pointer(candidate)) {
			return candidate, true
		}
	}
}
