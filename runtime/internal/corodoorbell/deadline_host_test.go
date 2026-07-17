//go:build !llgo && (darwin || linux) && !baremetal

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

package corodoorbell

import (
	"reflect"
	"syscall"
	"testing"
)

func TestDeadlineWaitPassResamplesAfterEveryInterrupt(t *testing.T) {
	var pipe Pipe
	if !pipe.Open() {
		t.Fatal("open deadline pipe")
	}
	t.Cleanup(func() {
		nativePipePollForWaitTestHook = nil
		if !pipe.Close() {
			t.Error("close deadline pipe")
		}
	})

	const (
		deadline         = int64(2_500_000)
		interruptAdvance = int64(600_000)
		wantPolls        = 5
	)
	var now int64
	var timeouts []int32
	nativePipePollForWaitTestHook = func(fd int32, timeoutMS int32) (int, int16, int32) {
		if fd != pipe.readFD {
			t.Fatalf("poll fd = %d, want %d", fd, pipe.readFD)
		}
		timeouts = append(timeouts, timeoutMS)
		now += interruptAdvance
		if len(timeouts) > wantPolls {
			// Bound a regression that retries EINTR inside one pass instead of
			// hanging this test under an endless synthetic signal storm.
			return 0, 0, 0
		}
		return -1, 0, int32(syscall.EINTR)
	}

	for pass := 0; ; pass++ {
		if pass > wantPolls {
			t.Fatalf("deadline was not observed after %d passes", pass)
		}
		woke, reached, ok := pipe.waitDeadlinePass(now, deadline)
		if !ok {
			t.Fatalf("deadline pass %d failed", pass)
		}
		if woke {
			t.Fatalf("deadline pass %d reported an unexpected wake", pass)
		}
		if reached {
			break
		}
	}

	if want := []int32{3, 2, 2, 1, 1}; !reflect.DeepEqual(timeouts, want) {
		t.Fatalf("poll timeouts = %v, want %v", timeouts, want)
	}
	if now < deadline {
		t.Fatalf("deadline reached at %d, before %d", now, deadline)
	}
}

func TestDeadlineWaitPassRetainsWakeAcrossInterrupt(t *testing.T) {
	var pipe Pipe
	if !pipe.Open() {
		t.Fatal("open deadline wake pipe")
	}
	t.Cleanup(func() {
		nativePipePollForWaitTestHook = nil
		if !pipe.Close() {
			t.Error("close deadline wake pipe")
		}
	})

	polls := 0
	ringOK := false
	nativePipePollForWaitTestHook = func(fd int32, timeoutMS int32) (int, int16, int32) {
		polls++
		if polls > 1 {
			return -1, 0, int32(syscall.EINVAL)
		}
		// This publishes after waitBoundedInterruptible cleared pending and
		// immediately before its synthetic interrupted poll returns.
		ringOK = pipe.Ring()
		return -1, 0, int32(syscall.EINTR)
	}

	woke, reached, ok := pipe.waitDeadlinePass(0, 10_000_000)
	if woke || reached || !ok || !ringOK {
		t.Fatalf("interrupted pass = (%t, %t, %t), ring = %t", woke, reached, ok, ringOK)
	}
	woke, reached, ok = pipe.waitDeadlinePass(1, 10_000_000)
	if !woke || reached || !ok {
		t.Fatalf("retained wake pass = (%t, %t, %t)", woke, reached, ok)
	}
	if polls != 1 {
		t.Fatalf("physical polls = %d, want 1", polls)
	}
}
