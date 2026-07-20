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
	"sync"
	"testing"
	"time"
)

func TestPipeRetainsRingBeforePhysicalWait(t *testing.T) {
	var pipe Pipe
	if !pipe.Open() {
		t.Fatal("open retained pipe")
	}
	readFD, writeFD := pipe.readFD, pipe.writeFD
	if !pipe.OwnsDescriptor(uintptr(readFD)) || !pipe.OwnsDescriptor(uintptr(writeFD)) ||
		pipe.OwnsDescriptor(uintptr(readFD+writeFD+1)) {
		t.Fatal("doorbell descriptor identity is not exact")
	}
	if !pipe.Ring() {
		t.Fatal("ring before wait")
	}
	done := make(chan bool, 1)
	go func() { done <- pipe.Wait() }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("retained wait failed")
		}
	case <-time.After(time.Second):
		t.Fatal("ring in CommitSleep-to-poll window was lost")
	}
	if !pipe.Close() || !pipe.Closed() {
		t.Fatal("close retained pipe")
	}
	if pipe.OwnsDescriptor(uintptr(readFD)) || pipe.OwnsDescriptor(uintptr(writeFD)) {
		t.Fatal("closed doorbell retained poll-server identity")
	}
}

func TestPipeConcurrentRingsCoalesceWithoutLoss(t *testing.T) {
	var pipe Pipe
	if !pipe.Open() {
		t.Fatal("open concurrent pipe")
	}
	const rounds = 100
	for round := 0; round < rounds; round++ {
		start := make(chan struct{})
		var wg sync.WaitGroup
		for producer := 0; producer < 16; producer++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if !pipe.Ring() {
					t.Errorf("round %d ring failed", round)
				}
			}()
		}
		close(start)
		if !pipe.Wait() {
			t.Fatalf("round %d wait failed", round)
		}
		wg.Wait()
		// A producer can publish pending immediately after Wait consumes an
		// earlier producer. Drain any coalesced tail before the next round.
		if nativeAtomicLoad(&pipe.pending) != 0 && !pipe.Wait() {
			t.Fatalf("round %d tail wait failed", round)
		}
	}
	if !pipe.Close() {
		t.Fatal("close concurrent pipe")
	}
}

func TestPipeFullIsAlreadyRetainedWake(t *testing.T) {
	var pipe Pipe
	if !pipe.Open() {
		t.Fatal("open saturation pipe")
	}
	var value byte
	for {
		written, errno := nativePipeWrite(pipe.writeFD, &value, 1)
		if written == 1 {
			continue
		}
		if written < 0 && nativeErrInterrupted(errno) {
			continue
		}
		if written < 0 && nativeErrWouldBlock(errno) {
			break
		}
		t.Fatalf("fill pipe = written:%d errno:%d", written, errno)
	}
	if !pipe.Ring() {
		t.Fatal("EAGAIN did not preserve saturated wake")
	}
	if !pipe.Wait() {
		t.Fatal("wait did not drain saturated pipe")
	}
	if !pipe.Close() {
		t.Fatal("close saturation pipe")
	}
}
