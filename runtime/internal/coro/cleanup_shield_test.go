package coro

import (
	"testing"
	"unsafe"
)

func TestCleanupShieldDefersNewTaskStopThroughJoinedChild(t *testing.T) {
	var receipt unsafe.Pointer
	fixture := newAwaitCompletionFixtureBeforeAwait(t, func(g *G, parent, _ *testFrame) {
		var valid bool
		receipt, valid = BeginCleanupShield(g, parent.handle)
		if !valid || receipt != parent.handle {
			t.Fatal("begin exact outer cleanup scope")
		}
		if nested, valid := BeginCleanupShield(g, parent.handle); !valid || nested != nil {
			t.Fatal("nested cleanup acquired another outer receipt")
		}
		if kind, valid := EndCleanupShield(g, parent.handle, nil); !valid || kind != TaskCancelNone || g.park.taskCancelPhase != taskCancelShielded {
			t.Fatal("nested receipt ended outer cleanup")
		}
	})
	g, p := fixture.g, fixture.p
	if !RequestTaskCancellation(p, g, TaskCancelAbort) ||
		!RequestTaskCancellation(p, g, TaskCancelShutdown) ||
		g.park.taskCancelPhase != taskCancelShieldedRequested ||
		g.park.taskCancelKind != TaskCancelShutdown {
		t.Fatal("shield did not preserve strongest pending stop")
	}
	if kind, claimed := ClaimTaskCancellation(p, g); claimed || kind != TaskCancelNone {
		t.Fatal("shielded child claimed task cancellation")
	}
	for _, owner := range []unsafe.Pointer{fixture.parent.handle, fixture.child.handle} {
		if _, valid := EndCleanupShield(g, owner, receipt); valid {
			t.Fatal("child consumed the outer owner's receipt")
		}
	}
	if snapshot, recovered, valid := TakeRecover(g, fixture.child.handle); !valid || recovered || snapshot != (RecoverSnapshot{}) {
		t.Fatal("cleanup shield granted parent recover permission")
	}
	completeRecoverChild(t, fixture)
	if _, valid := EndCleanupShield(g, fixture.parent.handle, receipt); valid {
		t.Fatal("outer scope ended before consuming child completion")
	}
	if snapshot, valid := ConsumeAwaitCompletion(g, fixture.parent.handle); !valid || snapshot.Status != CompletionReturn {
		t.Fatal("consume shielded child normal completion")
	}
	if kind, valid := EndCleanupShield(g, fixture.parent.handle, receipt); !valid || kind != TaskCancelShutdown || g.park.taskCancelPhase != taskCancelCleanup {
		t.Fatal("joined outer scope lost deferred cancellation")
	}
	if _, valid := EndCleanupShield(g, fixture.parent.handle, receipt); valid {
		t.Fatal("outer receipt consumed twice")
	}
	if kind, claimed := ClaimTaskCancellation(p, g); claimed || kind != TaskCancelNone {
		t.Fatal("deferred stop claimed twice after scope end")
	}
	fixture.keepAlive()
}

func TestCleanupShieldPreservesAlreadyClaimedCause(t *testing.T) {
	fixture := newAwaitCompletionFixtureBeforeAwait(t, func(g *G, parent, _ *testFrame) {
		if !RequestTaskCancellation(g.runP, g, TaskCancelAbort) {
			t.Fatal("request cancellation")
		}
		if kind, valid := ClaimTaskCancellation(g.runP, g); !valid || kind != TaskCancelAbort {
			t.Fatal("claim cancellation")
		}
		if receipt, valid := BeginCleanupShield(g, parent.handle); !valid || receipt != nil {
			t.Fatal("already claimed cleanup acquired outer scope")
		}
		if kind, valid := EndCleanupShield(g, parent.handle, nil); !valid || kind != TaskCancelNone {
			t.Fatal("end nested cleanup scope")
		}
		if !RequestTaskCancellation(g.runP, g, TaskCancelShutdown) ||
			g.park.taskCancelPhase != taskCancelCleanup || g.park.taskCancelKind != TaskCancelAbort {
			t.Fatal("cleanup shield changed frozen cancellation cause")
		}
	})
	fixture.keepAlive()
}

func TestCleanupShieldPreservesOperationCancellation(t *testing.T) {
	fixture := newAwaitCompletionFixtureBeforeAwait(t, func(g *G, parent, _ *testFrame) {
		if _, valid := BeginCleanupShield(g, parent.handle); !valid {
			t.Fatal("begin cleanup scope")
		}
	})
	g := fixture.g
	if !RequestTaskCancellation(fixture.p, g, TaskCancelAbort) {
		t.Fatal("defer a task stop before cleanup's next operation")
	}
	ticket, valid := BeginParkSet(&g.park, 0, 17)
	if !valid || !SealParkSet(&g.park, ticket) || !CommitParkSet(&g.park, ticket) {
		t.Fatal("park from cleanup with deferred task stop")
	}
	if kind, valid := ParkCancelKindOf(&g.park, ticket); valid || kind != ParkCancelNone {
		t.Fatal("deferred task stop canceled cleanup's new park")
	}
	if !RequestParkCancel(&g.park, ticket, ParkCancelOperation) {
		t.Fatal("cleanup shield disabled operation cancellation")
	}
	if kind, valid := ParkCancelKindOf(&g.park, ticket); !valid || kind != ParkCancelOperation {
		t.Fatal("cleanup shield changed operation cancellation kind")
	}
	fixture.keepAlive()
}

func TestCleanupShieldPendingEntryAndAllocationFreeBalancedScope(t *testing.T) {
	fixture := newAwaitCompletionFixtureBeforeAwait(t, func(g *G, parent, _ *testFrame) {
		before := preemptLoad(&g.preempt)
		allocations := testing.AllocsPerRun(1000, func() {
			receipt, valid := BeginCleanupShield(g, parent.handle)
			if !valid || receipt != parent.handle {
				t.Fatal("balanced compiler cleanup scope")
			}
			if kind, valid := EndCleanupShield(g, parent.handle, receipt); !valid || kind != TaskCancelNone {
				t.Fatal("balanced compiler cleanup scope end")
			}
		})
		if allocations != 0 || preemptLoad(&g.preempt) != before {
			t.Fatalf("cleanup scope allocations=%g, preemption state changed=%t", allocations, preemptLoad(&g.preempt) != before)
		}
		if !RequestTaskCancellation(g.runP, g, TaskCancelAbort) {
			t.Fatal("request stop before cleanup entry")
		}
		if receipt, valid := BeginCleanupShield(g, parent.handle); !valid || receipt != parent.handle ||
			g.park.taskCancelPhase != taskCancelShieldedRequested || g.park.taskCancelKind != TaskCancelAbort {
			t.Fatal("cleanup entry discarded an already pending stop")
		}
	})
	fixture.keepAlive()
}
