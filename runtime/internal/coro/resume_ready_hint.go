// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package coro

// ResumeHasReadyPeer reports only immediately local unlocked work. It neither
// grants queue ownership nor consumes a runnable. Any later yield revalidates
// the exact current resume and uses the complete scheduler transition.
func ResumeHasReadyPeer(g *G) bool {
	if !ValidG(g) || !resumeGateTaken(g) {
		return false
	}
	p := g.runP
	return p.current == g && p.osThreadLockOwner == nil &&
		p.osThreadSuspend == osThreadSuspendAttached &&
		p.readyHead != nil && p.readyCount != 0
}
