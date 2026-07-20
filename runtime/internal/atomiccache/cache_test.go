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

package atomiccache

import (
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

func TestPairTableConcurrentInternReturnsCanonicalWinner(t *testing.T) {
	var table PairTable
	firstKey := new(byte)
	secondKey := new(byte)
	const contenders = 128
	values := make([]*byte, contenders)
	results := make([]unsafe.Pointer, contenders)
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(contenders)
	for index := range results {
		values[index] = new(byte)
		go func(index int) {
			defer group.Done()
			<-start
			results[index] = table.Intern(
				unsafe.Pointer(firstKey),
				unsafe.Pointer(secondKey),
				unsafe.Pointer(values[index]),
			)
		}(index)
	}
	close(start)
	group.Wait()

	winner := results[0]
	if winner == nil {
		t.Fatal("nil canonical pair-table winner")
	}
	for index, result := range results {
		if result != winner {
			t.Fatalf("result %d = %p, want canonical winner %p", index, result, winner)
		}
	}
	if got := table.Find(unsafe.Pointer(firstKey), unsafe.Pointer(secondKey)); got != winner {
		t.Fatalf("Find = %p, want canonical winner %p", got, winner)
	}

	matches := 0
	for entry := table.load(); entry != nil; entry = entry.next {
		if entry.first == unsafe.Pointer(firstKey) && entry.second == unsafe.Pointer(secondKey) {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("published entries for one identity pair = %d, want 1", matches)
	}
}

func TestWeakTableConcurrentInternReturnsCanonicalWinner(t *testing.T) {
	var table WeakTable
	const key = uintptr(0x12345000)
	const contenders = 128
	results := make([]*WeakHandle, contenders)
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(contenders)
	for index := range results {
		go func(index int) {
			defer group.Done()
			candidate := &WeakHandle{Key: key, Live: 1}
			<-start
			results[index], _ = table.InternWeak(candidate)
		}(index)
	}
	close(start)
	group.Wait()

	winner := results[0]
	if winner == nil {
		t.Fatal("nil canonical weak-table winner")
	}
	for index, result := range results {
		if result != winner {
			t.Fatalf("result %d = %p, want canonical winner %p", index, result, winner)
		}
	}

	matches := 0
	for raw := atomic.LoadPointer(table.bucket(key)); raw != nil; {
		handle := (*WeakHandle)(raw)
		if handle.Key == key && atomic.LoadUint32(&handle.Live) != 0 {
			matches++
		}
		raw = atomic.LoadPointer(&handle.next)
	}
	if matches != 1 {
		t.Fatalf("published live handles for one key = %d, want 1", matches)
	}
}

func TestWeakTableManagedInternPrunesRawTombstone(t *testing.T) {
	var table WeakTable
	const key = uintptr(0x56789000)

	first := &WeakHandle{Key: key, Live: 1}
	if winner, published := table.InternWeak(first); winner != first || !published {
		t.Fatalf("first intern = (%p, %t), want (%p, true)", winner, published, first)
	}

	// This is the entire operation permitted in the raw finalizer callback.
	atomic.StoreUint32(&first.Live, 0)

	second := &WeakHandle{Key: key, Live: 1}
	if winner, published := table.InternWeak(second); winner != second || !published {
		t.Fatalf("replacement intern = (%p, %t), want (%p, true)", winner, published, second)
	}
	if head := (*WeakHandle)(atomic.LoadPointer(table.bucket(key))); head != second {
		t.Fatalf("bucket head after prune = %p, want replacement %p", head, second)
	}
	if next := atomic.LoadPointer(&second.next); next != nil {
		t.Fatalf("replacement retained tombstone next = %p, want nil", next)
	}
}

func TestWeakTableConcurrentPrunePreservesEveryLiveSuffix(t *testing.T) {
	const (
		entries = 16
		rounds  = 100
	)
	for round := 0; round < rounds; round++ {
		var table WeakTable
		handles := make([]*WeakHandle, entries)
		keys := make([]uintptr, entries)
		// Values below 8192 separated by 512 collide under weakBucketCount's
		// hash, forcing every prune and publication through one linked bucket.
		for index := range handles {
			keys[index] = uintptr(8 + index*512)
			handles[index] = &WeakHandle{Key: keys[index], Live: 1}
			winner, published := table.InternWeak(handles[index])
			if winner != handles[index] || !published {
				t.Fatalf("round %d initial intern %d = (%p, %t)", round, index, winner, published)
			}
		}
		for index := 0; index < entries; index += 2 {
			atomic.StoreUint32(&handles[index].Live, 0)
		}

		results := make([]*WeakHandle, entries)
		start := make(chan struct{})
		var group sync.WaitGroup
		group.Add(entries)
		for index := range results {
			go func(index int) {
				defer group.Done()
				candidate := &WeakHandle{Key: keys[index], Live: 1}
				<-start
				results[index], _ = table.InternWeak(candidate)
			}(index)
		}
		close(start)
		group.Wait()

		reachable := make(map[*WeakHandle]bool, entries)
		for raw := atomic.LoadPointer(table.bucket(keys[0])); raw != nil; {
			handle := (*WeakHandle)(raw)
			if atomic.LoadUint32(&handle.Live) != 0 {
				if reachable[handle] {
					t.Fatalf("round %d reachable chain contains duplicate handle %p", round, handle)
				}
				reachable[handle] = true
			}
			raw = atomic.LoadPointer(&handle.next)
		}
		if len(reachable) != entries {
			t.Fatalf("round %d reachable live handles = %d, want %d", round, len(reachable), entries)
		}
		for index, winner := range results {
			if winner == nil || !reachable[winner] || winner.Key != keys[index] {
				t.Fatalf("round %d result %d = %p (key %#x), not its reachable canonical winner", round, index, winner, keys[index])
			}
		}
	}
}
