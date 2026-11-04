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
	"fmt"
	"runtime"
	"sync"
	"testing"
)

func TestWorkerGrowthJoinsExactlyCreatedThreads(t *testing.T) {
	for count := uint32(0); count <= 4; count++ {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var growth WorkerGrowth
			if !growth.CanRelease() || growth.Start(0) || !growth.Start(4) || growth.Start(4) {
				t.Fatal("invalid factory initialization")
			}
			created, limit, status := growth.TryAcquire()
			if status != WorkerGrowthAcquired || created != 0 || limit != 4 {
				t.Fatalf("initial creator = %d %d %d", created, limit, status)
			}
			for next := uint32(1); next <= count; next++ {
				if !growth.PublishNext(next) {
					t.Fatal("could not publish a created thread")
				}
			}
			if growth.ResetAfterJoin(count) || !growth.Release() {
				t.Fatal("reset requires a sealed factory")
			}
			created, status = growth.TrySeal()
			if status != WorkerGrowthAcquired || created != count || growth.CanRelease() {
				t.Fatalf("sealed count = %d, status = %d", created, status)
			}
			if growth.ResetAfterJoin(count+1) || count > 0 && growth.ResetAfterJoin(count-1) {
				t.Fatal("accepted incomplete or excess join accounting")
			}
			if !growth.ResetAfterJoin(count) || !growth.CanRelease() || !growth.Start(4) {
				t.Fatal("joined factory was not reusable")
			}
		})
	}
}

func TestWorkerGrowthConcurrentCreatorsPublishThreadIDs(t *testing.T) {
	var growth WorkerGrowth
	if !growth.Start(4) {
		t.Fatal("start")
	}
	var ids [4]uint32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := uint32(1); worker <= 32; worker++ {
		wg.Add(1)
		go func(id uint32) {
			defer wg.Done()
			<-start
			for {
				created, limit, status := growth.TryAcquire()
				if status == WorkerGrowthBusy {
					runtime.Gosched()
					continue
				}
				if status != WorkerGrowthAcquired {
					t.Errorf("unexpected creator status %d", status)
					return
				}
				if created < limit {
					if ids[created] != 0 {
						t.Error("two creators wrote the same thread slot")
					}
					ids[created] = id
					if !growth.PublishNext(created + 1) {
						t.Error("publication failed")
					}
				}
				if !growth.Release() {
					t.Error("creator lost its lease")
				}
				return
			}
		}(worker)
	}
	close(start)
	wg.Wait()
	if created, limit, ok := growth.Snapshot(); !ok || created != 4 || limit != 4 {
		t.Fatalf("final capacity = %d %d %t", created, limit, ok)
	}
	if created, status := growth.TrySeal(); created != 4 || status != WorkerGrowthAcquired {
		t.Fatalf("seal = %d %d", created, status)
	}
	for slot, id := range ids {
		if id == 0 {
			t.Errorf("thread slot %d was published before its ID", slot)
		}
	}
}

func TestWorkerGrowthSealExcludesAdmittedCreator(t *testing.T) {
	var growth WorkerGrowth
	if !growth.Start(4) {
		t.Fatal("start")
	}
	if _, _, status := growth.TryAcquire(); status != WorkerGrowthAcquired {
		t.Fatal("acquire")
	}
	var wg sync.WaitGroup
	for index := 0; index < 32; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, status := growth.TrySeal(); status != WorkerGrowthBusy {
				t.Error("close overtook admitted creation")
			}
			if _, _, status := growth.TryAcquire(); status != WorkerGrowthBusy {
				t.Error("another creator entered")
			}
		}()
	}
	wg.Wait()
	if !growth.PublishNext(1) || !growth.Release() {
		t.Fatal("admitted creator could not finish")
	}
	if created, status := growth.TrySeal(); created != 1 || status != WorkerGrowthAcquired {
		t.Fatalf("close must observe the admitted thread: %d %d", created, status)
	}
	if _, _, status := growth.TryAcquire(); status != WorkerGrowthClosed {
		t.Fatal("creation admitted after seal")
	}
	if growth.PublishNext(2) || growth.Release() || growth.Fail() {
		t.Fatal("creator changed a sealed factory")
	}
}

func TestWorkerGrowthFailureCannotReuseLiveThreads(t *testing.T) {
	for count := uint32(0); count <= 4; count++ {
		var growth WorkerGrowth
		if !growth.Start(4) {
			t.Fatal("start")
		}
		if _, _, status := growth.TryAcquire(); status != WorkerGrowthAcquired {
			t.Fatal("acquire")
		}
		for next := uint32(1); next <= count; next++ {
			if !growth.PublishNext(next) {
				t.Fatal("publish")
			}
		}
		if !growth.Fail() || growth.Release() || growth.PublishNext(count+1) ||
			growth.ResetAfterJoin(count) || growth.CanRelease() || growth.Start(4) {
			t.Fatal("failed factory was reused")
		}
		if _, _, status := growth.TryAcquire(); status != WorkerGrowthClosed {
			t.Fatal("failed factory admitted creation")
		}
		if preemptLoad(&growth.created) != count {
			t.Fatal("failure lost actual thread accounting")
		}
	}
}

func TestWorkerGrowthRejectsSkippedOrUnleasedPublication(t *testing.T) {
	var growth WorkerGrowth
	if growth.PublishNext(1) || growth.Release() || growth.Fail() || growth.ResetAfterJoin(0) ||
		!growth.Start(4) || growth.PublishNext(1) {
		t.Fatal("accepted unleased publication")
	}
	if _, _, status := growth.TryAcquire(); status != WorkerGrowthAcquired {
		t.Fatal("acquire")
	}
	if growth.PublishNext(0) || growth.PublishNext(2) || growth.PublishNext(5) ||
		!growth.PublishNext(1) || growth.PublishNext(1) || growth.PublishNext(3) {
		t.Fatal("accepted a duplicate, missing, or excess thread")
	}
	if created, limit, ok := growth.Snapshot(); !ok || created != 1 || limit != 4 {
		t.Fatal("invalid publication changed actual thread count")
	}
}
