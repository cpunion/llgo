package coro

// The selector owns the exact queue member and just consumed its transfer links.
// A completed Yield published this private receipt after the full suspension
// transaction. Retain live preemption/schedule and the exact suspended handle.
func beginExecutorRunYieldPrepared(p *P, g *G, peerRunnable bool) (Action, bool) {
	if p == nil || g == nil || !g.yieldRunPrepared || g.initialRunPrepared || !gPreemptEnabledAtDepthZero(g) ||
		g.active == nil || g.active.handle == nil || g.active.state != FrameSuspended || g.active.header == nil ||
		g.active.header.SuspendReason != uint16(SuspendYield) {
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
	g.yieldRunPrepared = false
	action := Action{Kind: ActionCheckResume, Handle: handle}
	p.action = action
	return action, true
}

// PrepareYieldCompiler fully validated the source-free park and active depth0 frame. No Go
// instruction executes between that hook and this physical resume return.
// Only the live episode, queue/affinity and asynchronous preemption remain.
func resumedExecutorCompilerYield(driver *ExecutorDriver, p *P, g *G, action Action) (Action, bool) {
	frame := g.pending.from
	if p.current != g || g.runP != p || !p.inResume || p.inlineAwaitDepth != 0 || g.state != GRunning ||
		p.action != action || !p.runDecisionTaken || p.runDecision != (RunDecision{}) ||
		action.Kind != ActionResume || action.Flags != 0 || action.Handle == nil || frame == nil ||
		frame != g.active || frame != g.frames || frame.owner != g || frame.handle != action.Handle ||
		frame.state != FrameActive || g.pending.target != nil || g.pending.directChannel ||
		g.queued || g.nextReady != nil || g.waiting || g.runAction != ActionInvalid ||
		!validReadyQueueHeader(p) || p.readyCount == ^uint32(0) || !validOSThreadEnqueue(p, g) {
		return Action{}, false
	}
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return Action{}, false
	}
	if !acknowledgeSuspendedGPreempt(g) {
		return Action{}, false
	}
	g.pending = pendingTransition{}
	frame.state = FrameSuspended
	p.inResume = false
	p.runDecisionTaken = false
	g.state = GRunnable
	g.runP = nil
	p.current = nil
	p.servicePreemptBudget = 0
	p.action = Action{}
	appendReadyUnchecked(p, g)
	return Action{Kind: ActionYield}, true
}
