// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package coro

import "testing"

func TestResumeHasReadyPeerDoesNotConsumeOrGrantOwnership(t *testing.T) {
	fixture := newInlineAwaitFixtureForCompiler(t, true)
	if ResumeHasReadyPeer(nil) || ResumeHasReadyPeer(new(G)) || ResumeHasReadyPeer(fixture.g) {
		t.Fatal("missing resume or empty local queue reported a peer")
	}
	peer := newYieldingTestG(t, "ready peer")
	if !Enqueue(fixture.p, peer.g) {
		t.Fatal("enqueue ready peer")
	}
	head, tail, count := fixture.p.readyHead, fixture.p.readyTail, fixture.p.readyCount
	for attempt := 0; attempt < 2; attempt++ {
		if !ResumeHasReadyPeer(fixture.g) {
			t.Fatal("active unlocked resume did not observe its local peer")
		}
		if fixture.p.readyHead != head || fixture.p.readyTail != tail || fixture.p.readyCount != count || !peer.g.queued {
			t.Fatal("readiness hint consumed or rewrote the local queue")
		}
	}
	if ResumeHasReadyPeer(peer.g) {
		t.Fatal("queued peer acquired current-resume authority")
	}
	fixture.p.osThreadLockOwner = fixture.g
	if ResumeHasReadyPeer(fixture.g) {
		t.Fatal("locked resume reported an immediately usable peer")
	}
	fixture.p.osThreadLockOwner = nil
	fixture.p.runDecisionTaken = false
	if ResumeHasReadyPeer(fixture.g) {
		t.Fatal("unconsumed resume decision reported a peer")
	}
	fixture.p.runDecisionTaken = true
	if !EnterCritical(fixture.g) {
		t.Fatal("enter critical resume")
	}
	if ResumeHasReadyPeer(fixture.g) {
		t.Fatal("critical resume reported a yield opportunity")
	}
	if _, ok := ExitCritical(fixture.g); !ok {
		t.Fatal("exit critical resume")
	}
	if !ResumeHasReadyPeer(fixture.g) {
		t.Fatal("restored unlocked resume lost its local peer")
	}
}
