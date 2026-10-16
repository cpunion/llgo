/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package coro

import "unsafe"

// BeginCleanupShield is the private compiler boundary for a finite internal
// cleanup call. It uses the existing task-cancellation phase word, adding no
// G, frame, park, completion, or operation fields. A non-nil receipt belongs
// only to this exact outer owner activation; nested calls return nil receipts.
// It grants no recover capability and never disables scheduler preemption.
//
// The compiler must consume the receipt after strong child join and completion
// reconciliation on every lane. It must not expose this scope as a general
// user-Go cancellation mask or destroy an owner with a live outer receipt.
func BeginCleanupShield(g *G, owner unsafe.Pointer) (receipt unsafe.Pointer, valid bool) {
	if owner == nil || !validRecoverTaskActivation(g) || !gPreemptEnabledAtDepthZero(g) || g.active.handle != owner ||
		!validTaskCancelState(g.park.taskCancelKind, g.park.taskCancelPhase) {
		return nil, false
	}
	switch g.park.taskCancelPhase {
	case taskCancelIdle:
		g.park.taskCancelPhase = taskCancelShielded
	case taskCancelRequested:
		g.park.taskCancelPhase = taskCancelShieldedRequested
	case taskCancelShielded, taskCancelShieldedRequested, taskCancelCleanup:
		return nil, true
	default:
		return nil, false
	}
	return owner, true
}

// EndCleanupShield claims a deferred stop after the protected child has joined.
// The compiler must retain that returned kind as the owner's cleanup base,
// while preserving any panic overlay; normal user continuation cannot resume.
// Only the original
// active owner may consume a non-nil receipt. A nested nil receipt cannot end
// the outer scope, and an already claimed cleanup cause remains frozen.
func EndCleanupShield(g *G, owner, receipt unsafe.Pointer) (TaskCancelKind, bool) {
	if owner == nil || !validRecoverTaskActivation(g) || !gPreemptEnabledAtDepthZero(g) || g.active.handle != owner ||
		!validTaskCancelState(g.park.taskCancelKind, g.park.taskCancelPhase) {
		return TaskCancelNone, false
	}
	if receipt == nil {
		return TaskCancelNone, g.park.taskCancelPhase == taskCancelShielded ||
			g.park.taskCancelPhase == taskCancelShieldedRequested ||
			g.park.taskCancelPhase == taskCancelCleanup
	}
	if receipt != owner || !emptyCompletionRecord(&g.active.completion) {
		return TaskCancelNone, false
	}
	switch g.park.taskCancelPhase {
	case taskCancelShielded:
		g.park.taskCancelPhase = taskCancelIdle
	case taskCancelShieldedRequested:
		g.park.taskCancelPhase = taskCancelRequested
		return ClaimTaskCancellation(g.runP, g)
	default:
		return TaskCancelNone, false
	}
	return TaskCancelNone, true
}
