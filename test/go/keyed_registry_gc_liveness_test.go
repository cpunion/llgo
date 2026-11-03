//go:build go1.25 && llgo && llgo_coro && !nogc && !baremetal

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import (
	"runtime"
	"sync"
	"testing"
	"testing/synctest"
	"weak"
)

type keyedGCWaitGroupObject struct {
	group   sync.WaitGroup
	padding [256]byte
}

type keyedGCNotifyObject struct {
	mutex     sync.Mutex
	condition sync.Cond
	padding   [256]byte
}

// These helpers must finish before the collector runs. The test goroutine
// retains only weak handles; no ready message or closure retains the object.
//
//go:noinline
func startKeyedGCWaitGroup(done chan<- struct{}) weak.Pointer[keyedGCWaitGroupObject] {
	object := new(keyedGCWaitGroupObject)
	object.group.Add(1)
	handle := weak.Make(object)
	go keyedGCWaitGroupWaiter(object, done)
	return handle
}

//go:noinline
func keyedGCWaitGroupWaiter(object *keyedGCWaitGroupObject, done chan<- struct{}) {
	object.group.Wait()
	done <- struct{}{}
}

//go:noinline
func startKeyedGCNotify(done chan<- struct{}) weak.Pointer[keyedGCNotifyObject] {
	object := new(keyedGCNotifyObject)
	object.condition.L = &object.mutex
	handle := weak.Make(object)
	go keyedGCNotifyWaiter(object, done)
	return handle
}

//go:noinline
func keyedGCNotifyWaiter(object *keyedGCNotifyObject, done chan<- struct{}) {
	object.mutex.Lock()
	object.condition.Wait()
	object.mutex.Unlock()
	done <- struct{}{}
}

//go:noinline
func keyedGCWeakAlive[T any](handle weak.Pointer[T]) bool {
	return handle.Value() != nil
}

//go:noinline
func releaseKeyedGCWaitGroup(handle weak.Pointer[keyedGCWaitGroupObject]) bool {
	object := handle.Value()
	if object == nil {
		return false
	}
	object.group.Done()
	return true
}

//go:noinline
func releaseKeyedGCNotify(handle weak.Pointer[keyedGCNotifyObject]) bool {
	object := handle.Value()
	if object == nil {
		return false
	}
	object.mutex.Lock()
	object.condition.Broadcast()
	object.mutex.Unlock()
	return true
}

func checkKeyedGCWaiterLiveness[T any](t *testing.T, start func(chan<- struct{}) weak.Pointer[T], release func(weak.Pointer[T]) bool) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		// 512 waiters exercise multiple pages of unscanned scalar registry storage.
		// Keep ample room in the 1024-slot route for collector and
		// execution-domain services.
		const population = 512
		done := make(chan struct{}, population)
		handles := make([]weak.Pointer[T], population)
		for index := range handles {
			handles[index] = start(done)
		}
		// Every waiter is now durably blocked. In particular, the WaitGroup
		// waiter has no source-level use of its receiver after Wait returns.
		synctest.Wait()
		if len(done) != 0 {
			t.Fatal("waiter returned before release")
		}
		for cycle := range 16 {
			runtime.GC()
			for index, handle := range handles {
				if !keyedGCWeakAlive(handle) {
					t.Fatalf("parked object %d collected in GC cycle %d", index, cycle)
				}
			}
		}
		for index, handle := range handles {
			if !release(handle) {
				t.Fatalf("parked object %d unavailable at release", index)
			}
		}
		for range population {
			<-done
		}
		synctest.Wait()
		// Retirement intentionally preserves scalar payload bits so that
		// concurrent claim/registration never races a post-retirement clear.
		// Reuse the same free-slot prefix with fresh blocked objects before
		// testing old-object collection. Otherwise stale integer keys in the
		// baseline's scanned array would retain every first-wave object.
		replacements := make([]weak.Pointer[T], population)
		for index := range replacements {
			replacements[index] = start(done)
		}
		synctest.Wait()
		// As with the existing finalizer controls, conservative stale roots
		// can retain a few objects. A majority must become collectible after
		// all waiter frames have retired; there is no pressure allocation.
		for range 32 {
			runtime.Gosched()
			runtime.GC()
			dead := 0
			for _, handle := range handles {
				if !keyedGCWeakAlive(handle) {
					dead++
				}
			}
			if dead > population/2 {
				t.Logf("parked=%d cycles=16 retired_collected=%d replacement_waiters=%d", population, dead, population)
				for index, handle := range replacements {
					if !release(handle) {
						t.Fatalf("replacement parked object %d unavailable", index)
					}
				}
				for range population {
					<-done
				}
				synctest.Wait()
				return
			}
		}
		t.Fatal("retired keyed waiters still retain a majority of their objects")
	})
}

func TestKeyedGCWaitGroupObjectLiveness(t *testing.T) {
	checkKeyedGCWaiterLiveness(t, startKeyedGCWaitGroup, releaseKeyedGCWaitGroup)
}

func TestKeyedGCNotifyObjectLiveness(t *testing.T) {
	checkKeyedGCWaiterLiveness(t, startKeyedGCNotify, releaseKeyedGCNotify)
}
