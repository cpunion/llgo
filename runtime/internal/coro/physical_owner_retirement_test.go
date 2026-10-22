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
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

func TestPhysicalOwnerRetirementJoinsReadersPhysicalAndLineage(t *testing.T) {
	for iteration := 0; iteration < 1024; iteration++ {
		var retirement PhysicalOwnerRetirement
		if !retirement.Open() || !retirement.AcquireMetadata() || !retirement.Close(true) {
			t.Fatal("prepare live reader and pinned lineage")
		}
		var claims atomic.Int32
		var failures atomic.Int32
		var joined sync.WaitGroup
		for _, release := range []func() (bool, bool){
			retirement.ReleaseMetadata, retirement.ReleasePhysical, retirement.ReleaseLineage,
		} {
			joined.Add(1)
			go func(release func() (bool, bool)) {
				defer joined.Done()
				claim, ok := release()
				if !ok {
					failures.Add(1)
				}
				if claim {
					claims.Add(1)
				}
			}(release)
		}
		joined.Wait()
		if claims.Load() != 1 || failures.Load() != 0 || retirement.AcquireMetadata() {
			t.Fatal("retirement did not grant exactly one quiescent reclaim")
		}
		if !retirement.Reset() || retirement.AcquireMetadata() || !retirement.Open() {
			t.Fatal("reused slot became readable before initialization")
		}
		if !retirement.Close(false) {
			t.Fatal("close reused non-root owner")
		}
		if claim, ok := retirement.ReleasePhysical(); !claim || !ok {
			t.Fatal("reused non-root slot was not reclaimable")
		}
	}
}

func TestPhysicalOwnerRetirementAllowsLateReaderBeforeReclaim(t *testing.T) {
	var retirement PhysicalOwnerRetirement
	if !retirement.Close(true) || !retirement.AcquireMetadata() {
		t.Fatal("observe a retiring canonical owner")
	}
	if claim, ok := retirement.ReleasePhysical(); claim || !ok {
		t.Fatal("physical departure discarded live references")
	}
	if claim, ok := retirement.ReleaseLineage(); claim || !ok {
		t.Fatal("lineage release discarded live reader")
	}
	if !retirement.AcquireMetadata() {
		t.Fatal("late reader could not retain still-live metadata")
	}
	if claim, ok := retirement.ReleaseMetadata(); claim || !ok {
		t.Fatal("first reader reclaimed another reader's metadata")
	}
	if claim, ok := retirement.ReleaseMetadata(); !claim || !ok {
		t.Fatal("last reader did not own reclaim")
	}
	if _, ok := retirement.ReleaseMetadata(); ok {
		t.Fatal("accepted duplicate release")
	}
	if unsafe.Sizeof(retirement) != 4 {
		t.Fatal("native retirement record grew beyond one scalar word")
	}
}
