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

import "testing"

func TestDriveAdmissionDefersReentryUntilBeginOwnerFinishes(t *testing.T) {
	var admission DriveAdmission
	const epoch = uint32(7)
	if !admission.Acquire() || !admission.PublishEpoch(epoch) {
		t.Fatal("acquire and publish drive admission")
	}
	if result := admission.Enter(epoch); result != DriveAdmissionDeferred {
		t.Fatalf("reentrant admission = %d, want deferred", result)
	}
	got, pending, ok := admission.Finish()
	if !ok || !pending || got != epoch {
		t.Fatalf("claim reentrant admission = (%d, %t, %t), want (%d, true, true)", got, pending, ok, epoch)
	}
	if !admission.ClearEpoch(epoch) {
		t.Fatal("clear claimed drive epoch")
	}
	if got, pending, ok = admission.Finish(); !ok || pending || got != 0 || !admission.CanRelease() {
		t.Fatalf("release drive admission = (%d, %t, %t), releasable=%t", got, pending, ok, admission.CanRelease())
	}
}

func TestDriveAdmissionSerializesConcurrentContinuation(t *testing.T) {
	var admission DriveAdmission
	const epoch = uint32(11)
	if !admission.Acquire() || !admission.PublishEpoch(epoch) {
		t.Fatal("seed drive admission")
	}
	if _, pending, ok := admission.Finish(); !ok || pending {
		t.Fatalf("release seed owner = pending:%t ok:%t", pending, ok)
	}

	acquired := make(chan DriveAdmissionResult, 1)
	release := make(chan struct{})
	go func() {
		result := admission.Enter(epoch)
		acquired <- result
		if result == DriveAdmissionAcquired {
			<-release
		}
	}()
	if result := <-acquired; result != DriveAdmissionAcquired {
		t.Fatalf("first concurrent admission = %d, want acquired", result)
	}
	if result := admission.Enter(epoch); result != DriveAdmissionDeferred {
		t.Fatalf("second concurrent admission = %d, want deferred", result)
	}
	close(release)

	got, pending, ok := admission.Finish()
	if !ok || !pending || got != epoch {
		t.Fatalf("claim concurrent admission = (%d, %t, %t), want (%d, true, true)", got, pending, ok, epoch)
	}
	if !admission.ClearEpoch(epoch) {
		t.Fatal("clear concurrent drive epoch")
	}
	if _, pending, ok = admission.Finish(); !ok || pending || !admission.CanRelease() {
		t.Fatalf("release concurrent admission = pending:%t ok:%t releasable:%t", pending, ok, admission.CanRelease())
	}
}

func TestDriveAdmissionRejectsStaleEpochWithoutOwnership(t *testing.T) {
	var admission DriveAdmission
	if result := admission.Enter(1); result != DriveAdmissionStale || !admission.CanRelease() {
		t.Fatalf("stale admission = %d, releasable=%t", result, admission.CanRelease())
	}
	if !admission.Acquire() || !admission.PublishEpoch(3) {
		t.Fatal("publish current admission epoch")
	}
	if result := admission.Enter(2); result != DriveAdmissionStale {
		t.Fatalf("old epoch admission = %d, want stale", result)
	}
	if !admission.ClearEpoch(3) {
		t.Fatal("clear current admission epoch")
	}
	if _, pending, ok := admission.Finish(); !ok || pending || !admission.CanRelease() {
		t.Fatalf("release stale admission test = pending:%t ok:%t releasable:%t", pending, ok, admission.CanRelease())
	}
}

func TestDriveAdmissionRevokeEnterRace(t *testing.T) {
	const epoch = uint32(17)
	for iteration := 0; iteration < 500; iteration++ {
		var admission DriveAdmission
		if !admission.Acquire() || !admission.PublishEpoch(epoch) {
			t.Fatal("seed revoke race")
		}
		start := make(chan struct{})
		entered := make(chan DriveAdmissionResult, 1)
		go func() {
			<-start
			entered <- admission.Enter(epoch)
		}()
		close(start)
		if !admission.RevokeEpoch() {
			t.Fatal("revoke published admission epoch")
		}
		result := <-entered
		if result != DriveAdmissionDeferred && result != DriveAdmissionStale {
			t.Fatalf("revoke race admission = %d", result)
		}
		for {
			got, pending, ok := admission.Finish()
			if !ok {
				t.Fatal("finish revoke race")
			}
			if !pending {
				break
			}
			if got != 0 {
				t.Fatalf("revoked pending epoch = %d, want zero", got)
			}
		}
		if !admission.CanRelease() {
			t.Fatal("revoke race retained admission")
		}
	}
}

func TestDriveAdmissionFinishEnterRace(t *testing.T) {
	const epoch = uint32(23)
	for iteration := 0; iteration < 500; iteration++ {
		var admission DriveAdmission
		if !admission.Acquire() || !admission.PublishEpoch(epoch) {
			t.Fatal("seed finish race")
		}
		start := make(chan struct{})
		type finishResult struct {
			epoch   uint32
			pending bool
			ok      bool
		}
		finished := make(chan finishResult, 1)
		entered := make(chan DriveAdmissionResult, 1)
		go func() {
			<-start
			got, pending, ok := admission.Finish()
			finished <- finishResult{epoch: got, pending: pending, ok: ok}
		}()
		go func() {
			<-start
			entered <- admission.Enter(epoch)
		}()
		close(start)
		finish := <-finished
		entry := <-entered
		if !finish.ok {
			t.Fatal("finish/enter race rejected owner")
		}
		switch {
		case finish.pending && finish.epoch == epoch && entry == DriveAdmissionDeferred:
			// The original owner claimed the callback and still owns the gate.
		case !finish.pending && finish.epoch == 0 && entry == DriveAdmissionAcquired:
			// Finish released first and the callback became the next owner.
		default:
			t.Fatalf("finish/enter race = finish:(%d,%t) enter:%d", finish.epoch, finish.pending, entry)
		}
		if !admission.ClearEpoch(epoch) {
			t.Fatal("clear finish-race epoch")
		}
		if _, pending, ok := admission.Finish(); !ok || pending || !admission.CanRelease() {
			t.Fatalf("release finish-race owner = pending:%t ok:%t releasable:%t", pending, ok, admission.CanRelease())
		}
	}
}
