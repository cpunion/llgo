package coro

import (
	"sync"
	"testing"
)

func domainTestWake(t *testing.T, generation uint32) OperationID {
	t.Helper()
	id, ok := MakeOperationID(OperationSourceManual, 1, generation)
	if !ok {
		t.Fatal("invalid notification identity")
	}
	return id
}

func domainCheck(t *testing.T, d *ExecutionDomainActivity, live, running, leases uint32) {
	t.Helper()
	s := d.Snapshot()
	if s.Live != live || s.Running != running || s.Leases != leases {
		t.Fatalf("domain state = %+v; want live=%d running=%d leases=%d", s, live, running, leases)
	}
}

func TestExecutionDomainControllerCancellationDisarmsExactWake(t *testing.T) {
	for _, active := range []bool{false, true} {
		var d ExecutionDomainActivity
		var m ExecutionDomainMember
		if active && !d.Join(&m) {
			t.Fatal("join")
		}
		id := domainTestWake(t, 33)
		post, ok := d.ArmWake(id)
		if !ok || post.Valid() == active {
			t.Fatal("arm")
		}
		if d.DisarmWake(domainTestWake(t, 34)) || !d.DisarmWake(id) || d.DisarmWake(id) {
			t.Fatal("disarm must accept only the exact live controller generation")
		}
		// The host notifier deliberately has no target. A late publication is
		// accepted only because cleanup retired the exact controller identity.
		if !d.PublishWake(id) {
			t.Fatal("late canceled notification was treated as a live source error")
		}
		if d.Snapshot().Leases != 0 || d.Snapshot().WakeIssued {
			t.Fatal("canceled notification retained its lease")
		}
		if active {
			m.Leave()
		}
		if !d.Close() {
			t.Fatal("canceled controller prevented close")
		}
	}
}

func TestExecutionDomainLiveNotificationFailureIsNotSilentlyAccepted(t *testing.T) {
	var d ExecutionDomainActivity
	id := domainTestWake(t, 40)
	if post, ok := d.ArmWake(id); !ok || post != id {
		t.Fatal("arm")
	}
	if d.PublishWake(id) {
		t.Fatal("missing host target accepted a still-armed notification")
	}
	if !d.DisarmWake(id) || !d.Close() {
		t.Fatal("cleanup")
	}
}

func TestExecutionDomainDurableWaitAndNotificationLease(t *testing.T) {
	var d ExecutionDomainActivity
	var m ExecutionDomainMember
	if !d.Join(&m) {
		t.Fatal("join")
	}
	wake := domainTestWake(t, 1)
	if post, ok := d.ArmWake(wake); !ok || post.Valid() {
		t.Fatal("running task reported idle")
	}
	if !m.PrepareDurable() {
		t.Fatal("prepare")
	}
	domainCheck(t, &d, 1, 1, 0)
	if post, ok := m.Park(); !ok || post != wake {
		t.Fatal("committed durable park did not transfer notification")
	}
	domainCheck(t, &d, 1, 0, 1)
	if _, ok := d.Release(); ok {
		t.Fatal("ordinary release consumed the notification lease")
	}
	if !m.Published() || !m.Published() || !m.Resume() {
		t.Fatal("publication/resume")
	}
	domainCheck(t, &d, 1, 1, 1)
	if !d.ConsumeWake(wake) || d.ConsumeWake(wake) {
		t.Fatal("notification must be consumed exactly once")
	}
	if post, ok := m.Leave(); !ok || post.Valid() || !d.Close() {
		t.Fatal("strong close")
	}
	if d.Acquire() || d.Join(new(ExecutionDomainMember)) {
		t.Fatal("closed domain admitted work")
	}
}

func TestExecutionDomainPublicationBeforeParkAndNondurableWait(t *testing.T) {
	for _, durable := range []bool{false, true} {
		var d ExecutionDomainActivity
		var m ExecutionDomainMember
		if !d.Join(&m) {
			t.Fatal("join")
		}
		wake := domainTestWake(t, 1)
		if _, ok := d.ArmWake(wake); !ok {
			t.Fatal("arm")
		}
		if durable && (!m.PrepareDurable() || !m.Published()) {
			t.Fatal("prepare and publish")
		}
		if post, ok := m.Park(); !ok || post.Valid() {
			t.Fatal("nondurable or already published wait reported idle")
		}
		domainCheck(t, &d, 1, 1, 0)
		if !m.Resume() {
			t.Fatal("resume")
		}
		if post, ok := m.Leave(); !ok || post != wake {
			t.Fatal("terminal transition did not notify")
		}
		if !d.ConsumeWake(wake) || !d.Close() {
			t.Fatal("close")
		}
	}
}

func TestExecutionDomainExternalLeaseAndRegistrationRepair(t *testing.T) {
	var d ExecutionDomainActivity
	if !d.Acquire() {
		t.Fatal("external producer admission")
	}
	wake := domainTestWake(t, 1)
	if post, ok := d.ArmWake(wake); !ok || post.Valid() {
		t.Fatal("external work reported idle")
	}
	if post, ok := d.Release(); !ok || post != wake {
		t.Fatal("last external producer did not notify")
	}
	if !d.ConsumeWake(wake) {
		t.Fatal("consume")
	}
	wake = domainTestWake(t, 2)
	if post, ok := d.ArmWake(wake); !ok || post != wake {
		t.Fatal("registration lost a preceding idle edge")
	}
	if !d.ConsumeWake(wake) || !d.Close() {
		t.Fatal("close")
	}
}

func TestExecutionDomainConcurrentPublishPark(t *testing.T) {
	for iteration := 0; iteration < 1000; iteration++ {
		var d ExecutionDomainActivity
		var m ExecutionDomainMember
		if !d.Join(&m) || !m.PrepareDurable() {
			t.Fatal("prepare")
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, ok := m.Park(); !ok {
				t.Error("park")
			}
		}()
		go func() {
			defer wg.Done()
			if !m.Published() {
				t.Error("publish")
			}
		}()
		wg.Wait()
		domainCheck(t, &d, 1, 1, 0)
		if !m.Resume() {
			t.Fatal("resume")
		}
	}
}

func TestExecutionDomainChildAdmissionPrecedesParentExit(t *testing.T) {
	var d ExecutionDomainActivity
	var parent, child ExecutionDomainMember
	if !d.Join(&parent) {
		t.Fatal("parent")
	}
	wake := domainTestWake(t, 1)
	if _, ok := d.ArmWake(wake); !ok || !d.Join(&child) {
		t.Fatal("child admission")
	}
	if post, ok := parent.Leave(); !ok || post.Valid() {
		t.Fatal("parent exit hid admitted child")
	}
	domainCheck(t, &d, 1, 1, 0)
	if post, ok := child.Leave(); !ok || post != wake {
		t.Fatal("child exit")
	}
	if !d.ConsumeWake(wake) || !d.Close() {
		t.Fatal("close")
	}
}
