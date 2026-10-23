package coro

import (
	"testing"
	"unsafe"
)

func preparedCompilerYieldFixture(t *testing.T) (*P, *ExecutorDriver, *yieldingTestG, Action) {
	t.Helper()
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "prepared-yield")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue")
	}
	_ = runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, _, _, ok := BeginIssuedExecutorResumeRuntimeContext(driver, task.g)
	if !ok {
		t.Fatal("begin resume")
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYieldCompiler(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare yield")
	}
	return p, driver, task, resume
}
func TestPreparedCompilerYieldPreservesTransferAndResume(t *testing.T) {
	p, driver, task, resume := preparedCompilerYieldFixture(t)
	if task.g.pending.kind != pendingCompilerYield {
		t.Fatalf("missing compiler receipt: %d", task.g.pending.kind)
	}
	next, committed, ok := ResumedExecutorRun(driver, p, task.g, resume)
	if !ok || !committed || next.Kind != ActionYield || !task.g.yieldRunPrepared || task.g.runAction != ActionInvalid || p.readyHead != task.g || task.g.pending != (pendingTransition{}) {
		t.Fatalf("yield commit: %+v %t %t", next, committed, ok)
	}
	target := new(P)
	targetDriver, _, _ := bindTestExecutorDriver(t, target)
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, target) {
		t.Fatal("bind mailbox")
	}
	id, ok := PublishPNeutralRunnable(&mailbox, p, task.g)
	if !ok || !ImportPNeutralRunnable(&mailbox, target, id) || !task.g.yieldRunPrepared {
		t.Fatal("yield certificate blocked or lost transfer")
	}
	_ = runnerNextPhysicalAction(t, targetDriver, task, ActionCheckResume)
	if task.g.yieldRunPrepared || task.g.runP != target || task.g.state != GRunning {
		t.Fatal("transferred continuation did not consume receipt")
	}
}
func TestPreparedCompilerYieldPublicBeginKeepsFullValidator(t *testing.T) {
	p, driver, task, resume := preparedCompilerYieldFixture(t)
	_, committed, ok := ResumedExecutorRun(driver, p, task.g, resume)
	if !ok || !committed {
		t.Fatal("yield")
	}
	if dequeueReadyHeadUnchecked(p) != task.g {
		t.Fatal("dequeue")
	}
	task.g.park.expected = 1
	beforeP, beforeG := *p, *task.g
	if _, ok := BeginRunG(p, task.g); ok || *p != beforeP || *task.g != beforeG {
		t.Fatal("publicbegin bypassed park validation")
	}
	task.g.park.expected = 0
	if _, ok := BeginRunG(p, task.g); !ok || task.g.yieldRunPrepared {
		t.Fatal("publicbegin failed to consume valid receipt")
	}
}
func TestPreparedCompilerYieldRejectsChangedIssuedIdentity(t *testing.T) {
	for _, kind := range []string{"wrong-P", "wrong-G", "wrong-action", "wrong-issue", "inline-depth", "not-running", "no-resume", "active-frame", "pending-target"} {
		t.Run(kind, func(t *testing.T) {
			p, driver, task, resume := preparedCompilerYieldFixture(t)
			switch kind {
			case "wrong-P":
				driver.p = new(P)
			case "wrong-G":
				p.current = new(G)
			case "wrong-action":
				resume.Handle = unsafe.Pointer(new(byte))
			case "wrong-issue":
				driver.run.issued = ActionCheckDestroy
			case "inline-depth":
				p.inlineAwaitDepth = 1
			case "not-running":
				task.g.state = GWaiting
			case "no-resume":
				p.inResume = false
			case "active-frame":
				task.g.active = nil
			case "pending-target":
				task.g.pending.target = FrameFromStorage(task.frame.storage)
			}
			beforeP, beforeG, beforeFrame := *p, *task.g, *FrameFromStorage(task.frame.storage)
			next, committed, ok := ResumedExecutorRun(driver, p, task.g, resume)
			if ok || committed || next != (Action{}) || *p != beforeP || *task.g != beforeG || *FrameFromStorage(task.frame.storage) != beforeFrame {
				t.Fatalf("changed issued yield accepted or mutated: %+v %t %t", next, committed, ok)
			}
		})
	}
}
