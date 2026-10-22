//go:build go1.25

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestWaitGroupBriefWaitEmptyAndNil(t *testing.T) {
	var empty sync.WaitGroup
	for range 1000 {
		empty.Wait()
	}
	var nilGroup *sync.WaitGroup
	defer func() {
		if recover() == nil {
			t.Fatal("nil WaitGroup.Wait did not panic")
		}
	}()
	nilGroup.Wait()
}

func TestWaitGroupBriefWaitMultipleWaitersAndReuse(t *testing.T) {
	var work sync.WaitGroup
	for range 40 {
		var joined sync.WaitGroup
		work.Add(1)
		joined.Add(16)
		ready := make(chan struct{}, 16)
		value := 0
		for range 16 {
			go func() {
				ready <- struct{}{}
				work.Wait()
				if value != 42 {
					t.Error("Wait returned before the worker published its result")
				}
				joined.Done()
			}()
		}
		for range 16 {
			<-ready
		}
		for range 4 {
			runtime.Gosched()
		}
		value = 42
		work.Done()
		// Reuse only after every Wait has returned; Done may wake several
		// published waiters as well as a peer which has not published yet.
		joined.Wait()
	}
}

func TestWaitGroupBriefWaitNestedGo(t *testing.T) {
	var group sync.WaitGroup
	var completed atomic.Int64
	for range 30 {
		group.Go(func() {
			completed.Add(1)
			group.Go(func() { completed.Add(1) })
		})
	}
	group.Wait()
	if got := completed.Load(); got != 60 {
		t.Fatalf("nested Go completed %d tasks, want 60", got)
	}
}
