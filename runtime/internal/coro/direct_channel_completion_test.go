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

package coro

import (
	"runtime"
	"sync"
	"testing"
	"unsafe"
)

func TestDirectChannelCompletionInboxConcurrentReuse(t *testing.T) {
	const (
		producers  = 8
		iterations = 500
	)
	p := new(P)
	driver := &ExecutorDriver{
		magic: executorDriverMagic,
		state: executorDriverActive,
		p:     p,
		route: RouteID(1),
	}
	p.executor = driver
	stub := unsafe.Pointer(&driver.directChannelStub)
	preemptStorePointer(&driver.directChannelHead, stub)
	driver.directChannelTail = stub

	nodes := make([]DirectChannelCompletion, producers)
	ack := make([]chan struct{}, producers)
	index := make(map[*DirectChannelCompletion]int, producers)
	for producer := range nodes {
		node := &nodes[producer]
		node.owner = driver
		node.route = driver.route
		preemptStore(&node.state, uint32(directChannelCompletionMatched))
		ack[producer] = make(chan struct{}, 1)
		index[node] = producer
	}

	errors := make(chan int, producers)
	var group sync.WaitGroup
	group.Add(producers)
	for producer := range nodes {
		go func(producer int) {
			defer group.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				if !PublishExecutorDirectChannelCompletion(driver, &nodes[producer]) {
					errors <- producer
					return
				}
				<-ack[producer]
			}
		}(producer)
	}

	seen := make([]int, producers)
	for completed := 0; completed < producers*iterations; {
		node, ok := takeExecutorDirectChannelCompletion(driver)
		if !ok {
			t.Fatal("take concurrent direct channel completion")
		}
		if node == nil {
			select {
			case producer := <-errors:
				t.Fatalf("producer %d could not publish direct channel completion", producer)
			default:
			}
			runtime.Gosched()
			continue
		}
		producer, known := index[node]
		if !known {
			t.Fatalf("take unknown direct channel completion %p", node)
		}
		seen[producer]++
		completed++
		preemptStore(&node.state, uint32(directChannelCompletionMatched))
		ack[producer] <- struct{}{}
	}
	group.Wait()
	select {
	case producer := <-errors:
		t.Fatalf("producer %d could not publish direct channel completion", producer)
	default:
	}
	for producer, count := range seen {
		if count != iterations {
			t.Fatalf("producer %d completions = %d, want %d", producer, count, iterations)
		}
	}
	if !executorDirectChannelInboxIdle(driver) || executorDirectChannelCompletionPending(driver) {
		t.Fatal("direct channel completion inbox retained a node")
	}
}

func TestDirectChannelCompletionPublishesToSleepingOwner(t *testing.T) {
	p := new(P)
	driver := &ExecutorDriver{
		magic: executorDriverMagic,
		state: executorDriverSleeping,
		p:     p,
		route: RouteID(1),
	}
	p.executor = driver
	stub := unsafe.Pointer(&driver.directChannelStub)
	preemptStorePointer(&driver.directChannelHead, stub)
	driver.directChannelTail = stub

	completion := &DirectChannelCompletion{
		owner: driver,
		route: driver.route,
		state: uint32(directChannelCompletionMatched),
	}
	if !PublishExecutorDirectChannelCompletion(driver, completion) {
		t.Fatal("publish direct channel completion to sleeping owner")
	}
	got, ok := takeExecutorDirectChannelCompletion(driver)
	if !ok || got != completion {
		t.Fatalf("take sleeping-owner completion = (%p, %t), want (%p, true)", got, ok, completion)
	}
}
