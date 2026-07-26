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
)

func TestPhysicalThreadCapacityLifecycle(t *testing.T) {
	var capacity PhysicalThreadCapacity
	if _, _, ok := capacity.Usage(); ok ||
		capacity.Start(0, PhysicalThreadDefaultLimit) ||
		capacity.Start(1, 0) ||
		!capacity.Start(1, PhysicalThreadDefaultLimit) ||
		capacity.Start(1, PhysicalThreadDefaultLimit) {
		t.Fatal("physical thread capacity start contract failed")
	}
	if limit, live, ok := capacity.Usage(); !ok ||
		limit != PhysicalThreadDefaultLimit || live != 1 {
		t.Fatalf("initial physical thread usage = (%d, %d, %t)", limit, live, ok)
	}
	if accepted, ok := capacity.Reserve(); !accepted || !ok {
		t.Fatalf("reserve second physical thread = (%t, %t)", accepted, ok)
	}
	if previous, live, within, ok := capacity.SetLimit(2); !ok || !within ||
		previous != PhysicalThreadDefaultLimit || live != 2 {
		t.Fatalf("shrink physical thread limit = (%d, %d, %t, %t)", previous, live, within, ok)
	}
	if accepted, ok := capacity.Reserve(); accepted || !ok {
		t.Fatalf("reserve at physical thread limit = (%t, %t)", accepted, ok)
	}
	if previous, live, within, ok := capacity.SetLimit(1); !ok || within ||
		previous != 2 || live != 2 {
		t.Fatalf("fatal physical thread shrink = (%d, %d, %t, %t)", previous, live, within, ok)
	}
	if !capacity.Release() {
		t.Fatal("release second physical thread")
	}
	if _, live, within, ok := capacity.SetLimit(1); !ok || !within || live != 1 {
		t.Fatalf("restore physical thread limit = (_, %d, %t, %t)", live, within, ok)
	}
	if capacity.Release() {
		t.Fatal("program physical thread must remain accounted")
	}
}

func TestPhysicalThreadCapacityConcurrentReservationNeverExceedsLimit(t *testing.T) {
	const (
		limit   = uint32(8)
		workers = 32
		rounds  = 2_000
	)
	var capacity PhysicalThreadCapacity
	if !capacity.Start(1, limit) {
		t.Fatal("start physical thread capacity")
	}
	var active atomic.Int32
	var maximum atomic.Int32
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wait.Done()
			for index := 0; index < rounds; index++ {
				accepted, ok := capacity.Reserve()
				if !ok {
					t.Error("physical thread capacity became invalid")
					return
				}
				if !accepted {
					continue
				}
				current := active.Add(1) + 1
				for {
					seen := maximum.Load()
					if current <= seen || maximum.CompareAndSwap(seen, current) {
						break
					}
				}
				active.Add(-1)
				if !capacity.Release() {
					t.Error("release physical thread capacity")
					return
				}
			}
		}()
	}
	wait.Wait()
	if got := maximum.Load(); got > int32(limit) {
		t.Fatalf("maximum physical thread reservations = %d, limit %d", got, limit)
	}
	if got := active.Load(); got != 0 {
		t.Fatalf("active test reservations = %d", got)
	}
	if gotLimit, live, ok := capacity.Usage(); !ok || gotLimit != limit || live != 1 {
		t.Fatalf("final physical thread usage = (%d, %d, %t)", gotLimit, live, ok)
	}
}
