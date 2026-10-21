// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import (
	"runtime"
	"sync"
	"testing"
)

func TestSemaphoreBriefWaitPublishesBeforeReturn(t *testing.T) {
	var done sync.WaitGroup
	for i := 1; i <= 256; i++ {
		value := 0
		done.Add(1)
		go func(want int) {
			value = want
			done.Done()
		}(i)
		done.Wait()
		if value != i {
			t.Fatalf("Wait returned with value %d, want %d", value, i)
		}
	}
}

func TestSemaphoreBriefWaitRetainsContendedPark(t *testing.T) {
	var mutex sync.Mutex
	var ready, done sync.WaitGroup
	const workers = 32
	ready.Add(workers)
	done.Add(workers)
	mutex.Lock()
	value := 0
	for i := 0; i < workers; i++ {
		go func() {
			ready.Done()
			mutex.Lock()
			value++
			mutex.Unlock()
			done.Done()
		}()
	}
	ready.Wait()
	// The owner retains the mutex while local peers get a bounded opportunity
	// and then must park. Unlock must still wake every queued contender.
	for i := 0; i < 4; i++ {
		runtime.Gosched()
	}
	mutex.Unlock()
	done.Wait()
	if value != workers {
		t.Fatalf("contended semaphore completed %d workers, want %d", value, workers)
	}
}

func TestSemaphoreBriefWaitLockedOwner(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var done sync.WaitGroup
	done.Add(1)
	go done.Done()
	done.Wait()
}
