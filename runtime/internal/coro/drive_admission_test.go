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

func TestDriveAdmissionRejectsNewEpochWhileOldPendingIsUntagged(t *testing.T) {
	var admission DriveAdmission
	const oldEpoch, newEpoch = uint32(31), uint32(32)
	if !admission.Acquire() || !admission.PublishEpoch(oldEpoch) {
		t.Fatal("publish old drive epoch")
	}
	if result := admission.Enter(oldEpoch); result != DriveAdmissionDeferred {
		t.Fatalf("defer old drive epoch = %d", result)
	}
	if !admission.ClearEpoch(oldEpoch) {
		t.Fatal("clear old drive epoch")
	}
	if !admission.AdvancePhase() || !admission.PublishEpoch(newEpoch) {
		t.Fatal("same owner did not invalidate old Pending before new epoch")
	}
	if result := admission.Enter(oldEpoch); result != DriveAdmissionStale {
		t.Fatalf("old epoch after phase advance = %d", result)
	}
	if result := admission.Enter(newEpoch); result != DriveAdmissionDeferred {
		t.Fatalf("new epoch after phase advance = %d", result)
	}
	if !admission.ClearEpoch(newEpoch) {
		t.Fatal("clear fresh drive epoch")
	}
	if !admission.AdvancePhase() {
		t.Fatal("drop new epoch Pending before release")
	}
	if _, pending, ok := admission.Finish(); !ok || pending || !admission.CanRelease() {
		t.Fatalf("release phase-advanced drive = pending:%t ok:%t releasable:%t", pending, ok, admission.CanRelease())
	}
}

func TestDriveAdmissionDelayedIdleEpochCASCannotAcquireNewPhase(t *testing.T) {
	var admission DriveAdmission
	const oldEpoch, newEpoch = uint32(51), uint32(52)
	if !admission.Acquire() || !admission.PublishEpoch(oldEpoch) {
		t.Fatal("publish delayed-CAS old epoch")
	}
	if _, pending, ok := admission.Finish(); !ok || pending {
		t.Fatalf("release old epoch owner = pending:%t ok:%t", pending, ok)
	}

	// Deterministically pause an old callback after both exact-epoch checks and
	// its full idle gate/phase load, immediately before the ownership CAS.
	if preemptLoad(&admission.epoch) != oldEpoch {
		t.Fatal("old callback first epoch read")
	}
	oldGate := preemptLoad(&admission.gate)
	if oldGate&driveAdmissionMask != 0 || preemptLoad(&admission.epoch) != oldEpoch {
		t.Fatal("old callback idle phase snapshot")
	}

	if result := admission.Enter(oldEpoch); result != DriveAdmissionAcquired {
		t.Fatalf("exact old callback admission = %d", result)
	}
	if !admission.ClearEpoch(oldEpoch) || !admission.AdvancePhase() ||
		!admission.PublishEpoch(newEpoch) {
		t.Fatal("advance old epoch owner to new phase and epoch")
	}
	if _, pending, ok := admission.Finish(); !ok || pending {
		t.Fatalf("release new epoch owner = pending:%t ok:%t", pending, ok)
	}

	if preemptCompareAndSwap(&admission.gate, oldGate, oldGate|driveAdmissionOwned) {
		t.Fatal("delayed old callback acquired a later idle phase")
	}
	if result := admission.Enter(oldEpoch); result != DriveAdmissionStale {
		t.Fatalf("delayed old callback after failed CAS = %d", result)
	}
	if result := admission.Enter(newEpoch); result != DriveAdmissionAcquired {
		t.Fatalf("new epoch callback after delayed old CAS = %d", result)
	}
	if !admission.ClearEpoch(newEpoch) {
		t.Fatal("clear new epoch after delayed CAS")
	}
	if _, pending, ok := admission.Finish(); !ok || pending || !admission.CanRelease() {
		t.Fatalf("release delayed-CAS test = pending:%t ok:%t releasable:%t",
			pending, ok, admission.CanRelease())
	}
}

func TestDriveAdmissionCleansTerminalSamePhaseABA(t *testing.T) {
	const epoch = uint32(53)
	tests := []struct {
		name   string
		revoke func(*DriveAdmission) bool
	}{
		{
			name: "ClearEpoch",
			revoke: func(admission *DriveAdmission) bool {
				return admission.ClearEpoch(epoch)
			},
		},
		{
			name: "RevokeEpoch",
			revoke: func(admission *DriveAdmission) bool {
				return admission.RevokeEpoch()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var admission DriveAdmission
			if !admission.Acquire() || !admission.AdvancePhase() ||
				!admission.PublishEpoch(epoch) {
				t.Fatal("publish same-phase ABA epoch")
			}
			if _, pending, ok := admission.Finish(); !ok || pending {
				t.Fatalf("release seed owner = pending:%t ok:%t", pending, ok)
			}

			// S is an idle callback snapshot. O then becomes the current owner,
			// while P takes an Owned snapshot immediately before its Pending CAS.
			idleSnapshot := preemptLoad(&admission.gate)
			if idleSnapshot&driveAdmissionMask != 0 ||
				preemptLoad(&admission.epoch) != epoch {
				t.Fatal("capture idle S snapshot")
			}
			if result := admission.Enter(epoch); result != DriveAdmissionAcquired {
				t.Fatalf("acquire O owner = %d", result)
			}
			ownedSnapshot := preemptLoad(&admission.gate)
			if ownedSnapshot != idleSnapshot|driveAdmissionOwned ||
				preemptLoad(&admission.epoch) != epoch {
				t.Fatal("capture owned P snapshot")
			}

			// O terminates and releases the unchanged phase. Delayed raw S and P
			// CAS operations then recreate an owner with an untagged stale hint.
			if !test.revoke(&admission) {
				t.Fatal("revoke O epoch")
			}
			if _, pending, ok := admission.Finish(); !ok || pending {
				t.Fatalf("release O owner = pending:%t ok:%t", pending, ok)
			}
			if !preemptCompareAndSwap(
				&admission.gate,
				idleSnapshot,
				idleSnapshot|driveAdmissionOwned,
			) {
				t.Fatal("apply delayed S owner CAS")
			}
			if !preemptCompareAndSwap(
				&admission.gate,
				ownedSnapshot,
				ownedSnapshot|driveAdmissionPending,
			) {
				t.Fatal("apply delayed P pending CAS")
			}

			if result := admission.releaseStaleEnterOwner(
				epoch,
				idleSnapshot|driveAdmissionOwned,
			); result != DriveAdmissionStale {
				t.Fatalf("clean stale same-phase owner = %d", result)
			}
			if gate := preemptLoad(&admission.gate); gate != idleSnapshot ||
				preemptLoad(&admission.epoch) != 0 || !admission.CanRelease() {
				t.Fatalf("terminal ABA cleanup = gate:%#x epoch:%d releasable:%t",
					gate, preemptLoad(&admission.epoch), admission.CanRelease())
			}
		})
	}
}

func TestDriveAdmissionStaleOwnerCleanupFailsClosed(t *testing.T) {
	const epoch = uint32(59)
	const idle = uint32(3 * driveAdmissionPhaseIncrement)
	tests := []struct {
		name      string
		admission DriveAdmission
		owned     uint32
	}{
		{
			name: "epoch matches again",
			admission: DriveAdmission{
				gate:  idle | driveAdmissionOwned | driveAdmissionPending,
				epoch: epoch,
			},
			owned: idle | driveAdmissionOwned,
		},
		{
			name: "later epoch is live",
			admission: DriveAdmission{
				gate:  idle | driveAdmissionOwned | driveAdmissionPending,
				epoch: epoch + 1,
			},
			owned: idle | driveAdmissionOwned,
		},
		{
			name: "phase changed",
			admission: DriveAdmission{
				gate: idle + driveAdmissionPhaseIncrement + driveAdmissionOwned,
			},
			owned: idle | driveAdmissionOwned,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gate := preemptLoad(&test.admission.gate)
			if result := test.admission.releaseStaleEnterOwner(
				epoch,
				test.owned,
			); result != DriveAdmissionInvalid {
				t.Fatalf("stale-owner invariant result = %d", result)
			}
			if got := preemptLoad(&test.admission.gate); got != gate {
				t.Fatalf("stale-owner invariant changed gate %#x -> %#x", gate, got)
			}
		})
	}
}

func TestDriveAdmissionPhaseExhaustionFailsClosed(t *testing.T) {
	admission := DriveAdmission{gate: driveAdmissionPhaseMask | driveAdmissionOwned}
	if admission.AdvancePhase() {
		t.Fatal("wrapped exhausted drive-admission phase")
	}
	if _, pending, ok := admission.Finish(); !ok || pending || !admission.CanRelease() || admission.CanRecycle() {
		t.Fatalf("release exhausted phase = pending:%t ok:%t releasable:%t recyclable:%t",
			pending, ok, admission.CanRelease(), admission.CanRecycle())
	}
}

func TestDriveAdmissionRejectsCrossModeBeforePending(t *testing.T) {
	var admission DriveAdmission
	const executor, generation, mode, epoch = uint32(8), uint32(12), uint32(2), uint32(61)
	if !admission.Acquire() || !admission.PublishExecutor(executor, generation) ||
		!admission.PublishMode(mode) || !admission.PublishEpoch(epoch) {
		t.Fatal("publish mode-scoped executor epoch")
	}
	if result := admission.EnterMode(mode-1, epoch); result != DriveAdmissionStale {
		t.Fatalf("wrong mode-only admission = %d", result)
	}
	if result := admission.EnterExecutorMode(executor, generation, mode-1, epoch); result != DriveAdmissionStale {
		t.Fatalf("wrong executor-mode admission = %d", result)
	}
	if got, pending, ok := admission.Finish(); !ok || pending || got != 0 {
		t.Fatalf("cross-mode callback changed owner gate = (%d, %t, %t)", got, pending, ok)
	}
	if result := admission.EnterExecutorMode(executor, generation, mode, epoch); result != DriveAdmissionAcquired {
		t.Fatalf("exact executor-mode admission = %d", result)
	}
	if !admission.ClearEpoch(epoch) {
		t.Fatal("clear mode-scoped epoch")
	}
	if _, pending, ok := admission.Finish(); !ok || pending || !admission.CanRelease() {
		t.Fatalf("release mode-scoped admission = pending:%t ok:%t releasable:%t",
			pending, ok, admission.CanRelease())
	}
	if !admission.ResetModeAfterStrongJoin(mode) ||
		!admission.ResetExecutorAfterStrongJoin(executor, generation) || !admission.CanRecycle() {
		t.Fatal("reset mode and executor after modeled strong join")
	}
}

func TestDriveAdmissionRejectsWrongExecutorBeforePending(t *testing.T) {
	var admission DriveAdmission
	const executor, generation, epoch = uint32(5), uint32(9), uint32(41)
	if !admission.Acquire() || !admission.PublishExecutor(executor, generation) ||
		!admission.PublishEpoch(epoch) {
		t.Fatal("publish executor-scoped drive epoch")
	}
	if result := admission.EnterExecutor(executor+1, generation, epoch); result != DriveAdmissionStale {
		t.Fatalf("wrong executor admission = %d", result)
	}
	if result := admission.EnterExecutor(executor, generation+1, epoch); result != DriveAdmissionStale {
		t.Fatalf("wrong generation admission = %d", result)
	}
	if got, pending, ok := admission.Finish(); !ok || pending || got != 0 {
		t.Fatalf("wrong tuple changed owner gate = (%d, %t, %t)", got, pending, ok)
	}
	if result := admission.EnterExecutor(executor, generation, epoch); result != DriveAdmissionAcquired {
		t.Fatalf("exact executor admission = %d", result)
	}
	if !admission.ClearEpoch(epoch) {
		t.Fatal("clear executor-scoped drive epoch")
	}
	if _, pending, ok := admission.Finish(); !ok || pending || !admission.CanRelease() || admission.CanRecycle() {
		t.Fatalf("release executor-scoped episode = pending:%t ok:%t episode:%t allZero:%t",
			pending, ok, admission.CanRelease(), admission.CanRecycle())
	}
	if !admission.ResetExecutorAfterStrongJoin(executor, generation) || !admission.CanRecycle() {
		t.Fatal("reset executor identity after modeled target strong join")
	}
}

func TestDriveAdmissionExecutorIdentityCannotResetDuringEpisode(t *testing.T) {
	var admission DriveAdmission
	const executor, generation, epoch = uint32(6), uint32(10), uint32(43)
	if !admission.Acquire() || !admission.PublishExecutor(executor, generation) ||
		!admission.PublishEpoch(epoch) {
		t.Fatal("publish executor identity and epoch")
	}
	if admission.ResetExecutorAfterStrongJoin(executor, generation) {
		t.Fatal("reset executor identity while owner and epoch were active")
	}
	if !admission.ClearEpoch(epoch) {
		t.Fatal("clear executor reset-test epoch")
	}
	if _, pending, ok := admission.Finish(); !ok || pending || !admission.CanRelease() {
		t.Fatalf("release executor reset-test episode = pending:%t ok:%t episode:%t",
			pending, ok, admission.CanRelease())
	}
	if admission.ResetExecutorAfterStrongJoin(executor+1, generation) ||
		admission.ResetExecutorAfterStrongJoin(executor, generation+1) {
		t.Fatal("wrong tuple reset executor identity")
	}
	if !admission.ResetExecutorAfterStrongJoin(executor, generation) || !admission.CanRecycle() {
		t.Fatal("reset exact executor identity after modeled strong join")
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
