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

package gotest

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentLockedGoroutineRing(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for _, procs := range []int{1, 4} {
		runtime.GOMAXPROCS(procs)
		for _, participants := range []int{2, 8, 16, 64} {
			t.Run(fmt.Sprintf("p%d-n%d", procs, participants), func(t *testing.T) {
				baseline := runtime.NumGoroutine()
				var count atomic.Int32
				for round := 0; round < 3; round++ {
					ready := make(chan struct{}, participants)
					start := make(chan struct{})
					links := make([]chan int, participants)
					for i := range links {
						links[i] = make(chan int)
					}
					var done sync.WaitGroup
					done.Add(participants)
					for i := 0; i < participants; i++ {
						go func(i int) {
							runtime.LockOSThread()
							runtime.LockOSThread()
							runtime.UnlockOSThread()
							defer done.Done()
							ready <- struct{}{}
							<-start
							if i == 0 {
								links[1] <- 1
								if got := <-links[0]; got != participants {
									panic("locked goroutine ring payload")
								}
							} else {
								got := <-links[i]
								runtime.Gosched()
								links[(i+1)%participants] <- got + 1
							}
							count.Add(1)
							if i%2 != 0 {
								runtime.Goexit()
							}
						}(i)
					}
					for i := 0; i < participants; i++ {
						<-ready
					}
					close(start)
					done.Wait()
				}
				for spin := 0; runtime.NumGoroutine() != baseline && spin < 100000; spin++ {
					runtime.Gosched()
				}
				if count.Load() != int32(3*participants) || runtime.NumGoroutine() != baseline {
					t.Fatal("locked goroutines did not finish and retire")
				}
			})
		}
	}
}

func TestLockedThreadRetirementReusesDirectory(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for _, lockedParent := range []bool{false, true} {
		t.Run(fmt.Sprintf("locked-parent-%t", lockedParent), func(t *testing.T) {
			if lockedParent {
				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
			}
			finished := make(chan struct{})
			go func() {
				// The concurrent thread count is small. A directory bound must
				// limit live threads, not the total number created in process life.
				for i := 0; i < 12000; i++ {
					done := make(chan struct{})
					go func() {
						runtime.LockOSThread()
						defer close(done)
					}()
					<-done
					runtime.Gosched()
				}
				close(finished)
			}()
			<-finished
		})
	}
}
