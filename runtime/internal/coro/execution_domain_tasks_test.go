package coro

import "testing"

func TestExecutionDomainDetachedSpawnScopePreservesParent(t *testing.T) {
	var d ExecutionDomainActivity
	parent := new(G)
	entry := &ExecutionDomainTask{Activity: &d}
	if !AttachExecutionDomainTask(parent, entry, false) {
		t.Fatal("attach")
	}
	defer DetachExecutionDomainTask(parent)
	if InheritableExecutionDomainTask(parent) != entry || RestoreExecutionDomainInheritance(parent) ||
		!SuppressExecutionDomainInheritance(parent) || !SuppressExecutionDomainInheritance(parent) {
		t.Fatal("scope entry")
	}
	domainCheck(t, &d, 1, 1, 0)
	if InheritableExecutionDomainTask(parent) != nil || LookupExecutionDomainTask(parent) != entry {
		t.Fatal("suppression changed identity or failed to exclude child inheritance")
	}
	if !RestoreExecutionDomainInheritance(parent) || InheritableExecutionDomainTask(parent) != nil ||
		!RestoreExecutionDomainInheritance(parent) || InheritableExecutionDomainTask(parent) != entry ||
		RestoreExecutionDomainInheritance(parent) {
		t.Fatal("scope nesting/restoration")
	}
	domainCheck(t, &d, 1, 1, 0)
	if SuppressExecutionDomainInheritance(new(G)) {
		t.Fatal("ordinary G unexpectedly acquired optional membership")
	}
}

func TestExecutionDomainManualPublicationAndGenerationRetirement(t *testing.T) {
	var d ExecutionDomainActivity
	task := new(G)
	entry := &ExecutionDomainTask{Activity: &d}
	if !AttachExecutionDomainTask(task, entry, false) {
		t.Fatal("attach")
	}
	defer DetachExecutionDomainTask(task)
	p, source := new(P), new(ManualOperationSource)
	if !BindManualOperationSource(source, p) {
		t.Fatal("bind source")
	}
	_, _, ids := reserveManualWaitSet(t, source, p, 5, []uint32{1})
	id := ids[0]
	if !BindExecutionDomainManualWait(task, id) || !parkExecutionDomainTask(task) {
		t.Fatal("bind and park")
	}
	domainCheck(t, &d, 1, 0, 0)
	if source.Post(id) != ManualOperationPosted {
		t.Fatal("post")
	}
	// A foreign producer only had an OperationID, but the future continuation
	// must already exclude quiescence before its owner drains the mailbox.
	domainCheck(t, &d, 1, 1, 0)
	if !resumeExecutionDomainTask(task) || entry.manual.Valid() {
		t.Fatal("old generation not retired at resume")
	}
	next := id
	next.Generation++
	if !BindExecutionDomainManualWait(task, next) || !parkExecutionDomainTask(task) ||
		!PublishExecutionDomainManual(id) {
		t.Fatal("next wait/stale publication")
	}
	domainCheck(t, &d, 1, 0, 0)
	if !PublishExecutionDomainManual(next) {
		t.Fatal("next publication")
	}
	domainCheck(t, &d, 1, 1, 0)
}

func TestExecutionDomainSparseMembershipDoesNotCountController(t *testing.T) {
	var d ExecutionDomainActivity
	controller, child := new(G), new(G)
	root := &ExecutionDomainTask{Activity: &d}
	entry := &ExecutionDomainTask{Activity: &d}
	if !AttachExecutionDomainTask(controller, root, true) ||
		!AttachExecutionDomainTask(child, entry, false) {
		t.Fatal("attach")
	}
	defer DetachExecutionDomainTask(controller)
	defer DetachExecutionDomainTask(child)
	domainCheck(t, &d, 1, 1, 0)
	if LookupExecutionDomainTask(controller) != root || LookupExecutionDomainTask(child) != entry ||
		LookupExecutionDomainTask(new(G)) != nil {
		t.Fatal("sparse lookup")
	}
	if !PrepareExecutionDomainDurableWait(child) || !parkExecutionDomainTask(child) {
		t.Fatal("durable park")
	}
	domainCheck(t, &d, 1, 0, 0)
	if !resumeExecutionDomainTask(child) || !retireExecutionDomainTask(child) {
		t.Fatal("resume and terminal retirement")
	}
	if LookupExecutionDomainTask(child) != nil {
		t.Fatal("terminal task address retained")
	}
	domainCheck(t, &d, 0, 0, 0)
}

func TestExecutionDomainDirectPublicationPrecedesOwnerDrain(t *testing.T) {
	var d ExecutionDomainActivity
	task := new(G)
	entry := &ExecutionDomainTask{Activity: &d}
	if !AttachExecutionDomainTask(task, entry, false) {
		t.Fatal("attach")
	}
	defer DetachExecutionDomainTask(task)
	if !PrepareExecutionDomainDurableWait(task) || !parkExecutionDomainTask(task) {
		t.Fatal("park")
	}
	domainCheck(t, &d, 1, 0, 0)
	owner := new(ExecutorDriver)
	wait := &WaitSetRecord{g: task}
	completion := &DirectChannelCompletion{
		owner: owner, wait: wait, route: 1, state: uint32(directChannelCompletionEffect),
	}
	gotOwner, route, ok := FinishDirectChannelCompletion(completion, 1, 1)
	if !ok || gotOwner != owner || route != 1 {
		t.Fatal("irreversible channel publication")
	}
	// No inbox has been consumed and no G has been enqueued. Nevertheless its
	// future continuation must already prevent fake-time advancement.
	domainCheck(t, &d, 1, 1, 0)
	if !resumeExecutionDomainTask(task) {
		t.Fatal("owner promotion")
	}
	domainCheck(t, &d, 1, 1, 0)
}
