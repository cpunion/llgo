//go:build llgo

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	_ "unsafe"
)

type pollMutexFastState struct {
	state uint64
	rsema uint32
	wsema uint32
}

// These test-only mirrors exercise the actual standard-library methods. They
// do not replace their bodies or the semaphore implementation.
//
//go:linkname pollMutexFastLock internal/poll.(*fdMutex).rwlock
func pollMutexFastLock(*pollMutexFastState, bool, bool) bool

//go:linkname pollMutexFastUnlock internal/poll.(*fdMutex).rwunlock
func pollMutexFastUnlock(*pollMutexFastState, bool) bool

type pollMutexFastBool bool

//go:linkname pollMutexFastLockNamed internal/poll.(*fdMutex).rwlock
func pollMutexFastLockNamed(*pollMutexFastState, bool, bool) pollMutexFastBool

//go:linkname pollMutexFastUnlockNamed internal/poll.(*fdMutex).rwunlock
func pollMutexFastUnlockNamed(*pollMutexFastState, bool) pollMutexFastBool

func TestPollMutexFastNamedResults(t *testing.T) {
	assertNamed := func(value any, want bool) {
		named, ok := value.(pollMutexFastBool)
		if !ok {
			t.Fatal("named result view was lost")
		}
		if bool(named) != want {
			t.Fatalf("value=%v want=%v", named, want)
		}
	}
	var mu pollMutexFastState
	assertNamed(pollMutexFastLockNamed(&mu, true, true), true)
	assertNamed(pollMutexFastUnlockNamed(&mu, true), false)
	mu.state = 1
	assertNamed(pollMutexFastLockNamed(&mu, true, true), false)
	mu.state = 11
	assertNamed(pollMutexFastUnlockNamed(&mu, true), true)
}

func TestPollMutexFastStates(t *testing.T) {
	const refMask = (1<<20 - 1) << 3
	for _, test := range []struct {
		name                string
		old, want           uint64
		unlock, read, wait  bool
		result, panics      bool
		readSema, writeSema uint32
	}{
		{name: "read", read: true, wait: true, want: 10, result: true},
		{name: "write", wait: true, want: 12, result: true},
		{name: "try-free", read: true, want: 10, result: true},
		{name: "read-beside-write", old: 12, read: true, wait: true, want: 22, result: true},
		{name: "write-beside-read", old: 10, wait: true, want: 22, result: true},
		{name: "closed", old: 1, read: true, wait: true, want: 1},
		{name: "try-busy", old: 10, read: true, want: 10},
		{name: "reference-overflow", old: refMask, read: true, wait: true, want: refMask, panics: true},
		{name: "unlock-read", old: 10, unlock: true, read: true},
		{name: "unlock-write", old: 12, unlock: true},
		{name: "unlock-other-reference", old: 18, unlock: true, read: true, want: 8},
		{name: "unlock-closed-last", old: 11, unlock: true, read: true, want: 1, result: true},
		{name: "unlock-other-waiter", old: 10 | 1<<43, unlock: true, read: true, want: 1 << 43},
		{name: "unlock-read-wakeup", old: 10 | 1<<23, unlock: true, read: true, readSema: 1},
		{name: "unlock-write-wakeup", old: 12 | 1<<43, unlock: true, writeSema: 1},
		{name: "unlock-no-reference", old: 2, unlock: true, read: true, want: 2, panics: true},
		{name: "unlock-no-lock", old: 8, unlock: true, read: true, want: 8, panics: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			mu := pollMutexFastState{state: test.old}
			var result, panicked bool
			func() {
				defer func() { panicked = recover() != nil }()
				if test.unlock {
					result = pollMutexFastUnlock(&mu, test.read)
				} else {
					result = pollMutexFastLock(&mu, test.read, test.wait)
				}
			}()
			if panicked != test.panics || result != test.result || mu.state != test.want || mu.rsema != test.readSema || mu.wsema != test.writeSema {
				t.Fatalf("result=%v panic=%v state=%#x sema=%d/%d; want result=%v panic=%v state=%#x sema=%d/%d", result, panicked, mu.state, mu.rsema, mu.wsema, test.result, test.panics, test.want, test.readSema, test.writeSema)
			}
		})
	}
}

func TestPollMutexFastNil(t *testing.T) {
	for _, unlock := range []bool{false, true} {
		panicked := false
		func() {
			defer func() { panicked = recover() != nil }()
			if unlock {
				pollMutexFastUnlock(nil, true)
			} else {
				pollMutexFastLock(nil, true, true)
			}
		}()
		if !panicked {
			t.Fatal("nil mutex did not panic")
		}
	}
}

func TestPollMutexFastConcurrent(t *testing.T) {
	var mu pollMutexFastState
	var done sync.WaitGroup
	var count int
	done.Add(8)
	for range 8 {
		go func() {
			defer done.Done()
			for range 2000 {
				if !pollMutexFastLock(&mu, false, true) {
					panic("unexpected closed mutex")
				}
				count++
				if pollMutexFastUnlock(&mu, false) {
					panic("unexpected last closed reference")
				}
			}
		}()
	}
	done.Wait()
	if count != 16000 || mu.state != 0 || mu.rsema != 0 || mu.wsema != 0 {
		t.Fatalf("count=%d state=%#x sema=%d/%d", count, mu.state, mu.rsema, mu.wsema)
	}
}

func TestPollMutexFastWake(t *testing.T) {
	for _, read := range []bool{false, true} {
		var mu pollMutexFastState
		if !pollMutexFastLock(&mu, read, true) {
			t.Fatal("initial lock")
		}
		done := make(chan struct{})
		go func() {
			if !pollMutexFastLock(&mu, read, true) {
				panic("waiter lock")
			}
			pollMutexFastUnlock(&mu, read)
			close(done)
		}()
		mask := uint64((1<<20 - 1) << 43)
		if read {
			mask = (1<<20 - 1) << 23
		}
		for spins := 0; atomic.LoadUint64(&mu.state)&mask == 0; spins++ {
			if spins == 100000 {
				t.Fatal("waiter never registered")
			}
			runtime.Gosched()
		}
		pollMutexFastUnlock(&mu, read)
		<-done
		if mu.state != 0 || mu.rsema != 0 || mu.wsema != 0 {
			t.Fatalf("state=%#x sema=%d/%d", mu.state, mu.rsema, mu.wsema)
		}
	}
}
