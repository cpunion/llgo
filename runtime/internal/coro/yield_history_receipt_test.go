package coro

import "testing"

func retainedHistoryYieldFixture(t *testing.T, malformed func(*ParkState)) (*P, *ExecutorDriver, *yieldingTestG, Action, bool) {
	t.Helper()
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "retained-history-yield")
	// Model the source-free history left after a successful wait delivery.
	// The native WaitGroup controls exercise the producer/consumer transaction.
	task.g.park = ParkState{ticket: ParkTicket{epoch: 17, generation: 3}, phase: parkDelivered}
	if !releasableParkState(&task.g.park) || !Enqueue(p, task.g) {
		t.Fatal("valid retained history was not runnable")
	}
	_ = runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, _, _, ok := BeginIssuedExecutorResumeRuntimeContext(driver, task.g)
	if !ok {
		t.Fatal("begin resume")
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if malformed != nil {
		malformed(&task.g.park)
	}
	beforePark, beforePending, beforeP := task.g.park, task.g.pending, *p
	prepared := PrepareYieldCompiler(task.g, task.handle, task.frame.header)
	if task.g.park != beforePark || !prepared && (task.g.pending != beforePending || *p != beforeP) {
		t.Fatal("yield preparation changed park history or mutated a rejected episode")
	}
	return p, driver, task, resume, prepared
}

func TestCompilerYieldRetainedHistoryReceipt(t *testing.T) {
	p, driver, task, resume, ok := retainedHistoryYieldFixture(t, nil)
	if !ok || task.g.pending.kind != pendingCompilerYield {
		t.Fatalf("retained history missing compiler yield receipt: ok=%t kind=%d", ok, task.g.pending.kind)
	}
	history := task.g.park
	next, committed, ok := ResumedExecutorRun(driver, p, task.g, resume)
	if !ok || !committed || next.Kind != ActionYield || !task.g.yieldRunPrepared || task.g.park != history {
		t.Fatal("retained history yield commit")
	}
	target := new(P)
	targetDriver, _, _ := bindTestExecutorDriver(t, target)
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, target) {
		t.Fatal("bind mailbox")
	}
	// Retained delivered tickets remain source-P-affine under the existing
	// transfer protocol. The zero-history transfer case has its own AL control.
	beforeP, beforeTarget, beforeMailbox, beforeG := *p, *target, mailbox, *task.g
	id, published := PublishPNeutralRunnable(&mailbox, p, task.g)
	if published || id != (RunnableTransferID{}) || *p != beforeP || *target != beforeTarget || mailbox != beforeMailbox || *task.g != beforeG {
		t.Fatal("retained history transfer rejection changed ownership")
	}
	_ = targetDriver // The target stays idle after the rejected publication.
	_ = runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	if task.g.yieldRunPrepared || task.g.runP != p || task.g.park != history {
		t.Fatal("local yield did not consume the exact receipt")
	}
	if _, _, _, ok := BeginIssuedExecutorResumeRuntimeContext(driver, task.g); !ok {
		t.Fatal("resume local history")
	}
	takeNormalRunnerDecision(t, task.g)
	if task.g.park != history {
		t.Fatal("ordinary resume cleared historical ticket")
	}
}

func TestCompilerYieldRetainedHistoryRejectsMalformedPark(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ParkState)
	}{
		{"zero-ticket", func(p *ParkState) { p.ticket = ParkTicket{} }},
		{"stale-idle", func(p *ParkState) { p.phase = parkIdle }},
		{"unexpected-expected", func(p *ParkState) { p.expected = 1 }},
		{"attached", func(p *ParkState) { p.attached = 1 }},
		{"seed", func(p *ParkState) { p.seed = 1 }},
		{"direct-channel", func(p *ParkState) { p.directChannel = true }},
		{"resolving", func(p *ParkState) { p.resolving = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, _, ok := retainedHistoryYieldFixture(t, tt.mutate)
			if ok {
				t.Fatal("malformed retained history accepted")
			}
		})
	}
}
