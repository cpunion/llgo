// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package coro

// beginExecutorRunInitialPrepared consumes only CommitSpawnCompiler's private
// initial-root receipt. The bounded selector just removed the exact queue
// member under its owner/affinity proof. No user code, frame transition or
// source publication can occur between the initial commit and this first run.
// A task stop takes the ordinary full BeginRunG path instead. Async preemption
// and schedule requests remain live gates here as on every continuation.
func beginExecutorRunInitialPrepared(p *P, g *G, peerRunnable bool) (Action, bool) {
	if p == nil || g == nil || !g.initialRunPrepared ||
		!gPreemptEnabledAtDepthZero(g) || g.active == nil ||
		g.active.handle == nil || g.active.state != FrameInitialSuspended {
		return Action{}, false
	}
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return Action{}, false
	}
	handle := g.active.handle
	budget := servicePreemptSafepointBudget
	if peerRunnable {
		budget = preemptCheckpointStride
	}
	p.current = g
	p.servicePreemptBudget = budget
	g.state = GRunning
	g.runP = p
	g.initialRunPrepared = false
	action := Action{Kind: ActionCheckResume, Handle: handle}
	p.action = action
	return action, true
}
