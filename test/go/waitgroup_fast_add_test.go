//go:build go1.26 && (amd64 || arm64)

// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package gotest

import (
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

// Match the real 64-bit standard-library layout before constructing states.
// The compiler must preserve even the original update-before-panic behavior.
func waitGroupStateWords(t *testing.T, wg *sync.WaitGroup) (*uint64, *uint32) {
	t.Helper()
	if unsafe.Sizeof(*wg) != 16 || unsafe.Alignof(*wg) != 8 {
		t.Fatal("unexpected 64-bit WaitGroup layout")
	}
	return (*uint64)(unsafe.Pointer(wg)), (*uint32)(unsafe.Add(unsafe.Pointer(wg), 8))
}

func TestWaitGroupFastAddCounterTransitions(t *testing.T) {
	const negative = "sync: negative WaitGroup counter"
	const misuse = "sync: WaitGroup misuse: Add called concurrently with Wait"
	for _, test := range []struct {
		name      string
		before    uint64
		delta     int
		after     uint64
		sema      uint32
		panicText string
	}{
		{"first-add", 0, 1, 1 << 32, 0, ""},
		{"zero-add", 0, 0, 0, 0, ""},
		{"decrement-no-waiters", 1 << 32, -1, 0, 0, ""},
		{"decrement-with-waiters", 2<<32 | 3, -1, 1<<32 | 3, 0, ""},
		{"wake-one", 1<<32 | 1, -1, 0, 1, ""},
		{"wake-on-zero-delta", 1, 0, 0, 1, ""},
		{"misuse-after-update", 1, 1, 1<<32 | 1, 0, misuse},
		{"negative-after-update", 0, -1, 0xffffffff00000000, 0, negative},
		{"overflow-after-update", 0x7fffffff00000000, 1, 0x8000000000000000, 0, negative},
		{"wide-positive-delta", 0, 1 << 32, 0, 0, ""},
		{"wide-negative-delta", 0, -1 << 32, 0, 0, ""},
		{"restore-after-negative", 0xffffffff00000000, 1, 0, 0, ""},
		{"wide-misuse-after-update", 1, 1 << 32, 1, 0, misuse},
	} {
		t.Run(test.name, func(t *testing.T) {
			var wg sync.WaitGroup
			state, sema := waitGroupStateWords(t, &wg)
			atomic.StoreUint64(state, test.before)
			var recovered any
			func() { defer func() { recovered = recover() }(); wg.Add(test.delta) }()
			if test.panicText == "" {
				if recovered != nil {
					t.Fatalf("unexpected panic %v", recovered)
				}
			} else if got, ok := recovered.(string); !ok || got != test.panicText {
				t.Fatalf("panic=%v want %q", recovered, test.panicText)
			}
			if got := atomic.LoadUint64(state); got != test.after {
				t.Fatalf("state=%#x want %#x", got, test.after)
			}
			if got := atomic.LoadUint32(sema); got != test.sema {
				t.Fatalf("sema=%d want %d", got, test.sema)
			}
		})
	}
}

func TestWaitGroupFastAddNilReceiver(t *testing.T) {
	var wg *sync.WaitGroup
	defer func() {
		if recover() == nil {
			t.Fatal("nil receiver did not panic")
		}
	}()
	wg.Add(1)
}

func TestWaitGroupFastAddConcurrentCounter(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	done := make(chan struct{}, 8)
	for range 8 {
		go func() {
			for range 2000 {
				wg.Add(1)
				wg.Add(-1)
			}
			done <- struct{}{}
		}()
	}
	for range 8 {
		<-done
	}
	wg.Done()
	wg.Wait()
	state, sema := waitGroupStateWords(t, &wg)
	if atomic.LoadUint64(state) != 0 || atomic.LoadUint32(sema) != 0 {
		t.Fatal("concurrent counter lost an update")
	}
}

func TestWaitGroupFastAddRepeatedWakeAndReuse(t *testing.T) {
	for range 100 {
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go wg.Done()
		}
		wg.Wait()
		wg.Add(1)
		go wg.Done()
		wg.Wait()
	}
}
