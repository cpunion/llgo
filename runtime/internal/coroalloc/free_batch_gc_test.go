//go:build !llgo && !nogc && !baremetal && !wasm && !tinygo.wasm

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

package coroalloc

import (
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

func TestGCFreeBatcherSerializesAndDrainsAll(t *testing.T) {
	var batcher gcFreeBatcher
	var activeDrainers atomic.Int32
	var totalFreed atomic.Int64

	type fakeNode struct {
		next unsafe.Pointer
		id   uint64
	}

	freeOne := func(ptr unsafe.Pointer) {
		if activeDrainers.Add(1) != 1 {
			t.Errorf("concurrent drainer detected")
		}
		totalFreed.Add(1)
		activeDrainers.Add(-1)
	}

	drain := func() {
		for {
			batch := batcher.takeBatch()
			if batch == nil {
				if !batcher.releaseDrain() {
					return
				}
				continue
			}
			for node := batch; node != nil; {
				next := *(*unsafe.Pointer)(node)
				*(*unsafe.Pointer)(node) = nil
				freeOne(node)
				node = next
			}
		}
	}

	const (
		workers = 8
		perWork = 5000
	)
	nodes := make([]fakeNode, workers*perWork)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		base := w * perWork
		go func() {
			defer wg.Done()
			for i := 0; i < perWork; i++ {
				ptr := unsafe.Pointer(&nodes[base+i])
				if batcher.tryAcquireDrain() {
					freeOne(ptr)
					drain()
					continue
				}
				batcher.pushPending(ptr)
				if batcher.tryAcquireDrain() {
					drain()
				}
			}
		}()
	}
	wg.Wait()

	if got, want := totalFreed.Load(), int64(workers*perWork); got != want {
		t.Fatalf("freed %d objects, want %d", got, want)
	}
	if batcher.takeBatch() != nil {
		t.Fatal("pending free queue is not empty after drain")
	}
}
