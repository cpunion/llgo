package coro

import "unsafe"

// ExecutionDomainTask is sparse adapter-owned membership. Controllers carry
// the context needed by descendants but are not counted as application tasks.
// Storage must remain rooted until DetachExecutionDomainTask succeeds.
type ExecutionDomainTask struct {
	Context               unsafe.Pointer
	Activity              *ExecutionDomainActivity
	task                  *G
	next                  *ExecutionDomainTask
	member                ExecutionDomainMember
	control               bool
	manual                OperationID
	manualNext            *ExecutionDomainTask
	inheritanceSuppressed uint32
}

// Keeping the optional index outside G preserves the exact ordinary task and
// root-frame layouts. Its accesses are compiler-elided when no domain exists.
var executionDomainTaskIndex struct {
	lock          uint32
	buckets       [64]*ExecutionDomainTask
	manualBuckets [64]*ExecutionDomainTask
}

func executionDomainIndexLock() {
	for !preemptCompareAndSwap(&executionDomainTaskIndex.lock, 0, 1) {
	}
}

func executionDomainIndexUnlock() { preemptStore(&executionDomainTaskIndex.lock, 0) }

func executionDomainTaskBucket(task *G) uintptr {
	return (uintptr(unsafe.Pointer(task)) >> 4) % uintptr(len(executionDomainTaskIndex.buckets))
}

func executionDomainTaskLocked(task *G) *ExecutionDomainTask {
	for entry := executionDomainTaskIndex.buckets[executionDomainTaskBucket(task)]; entry != nil; entry = entry.next {
		if entry.task == task {
			return entry
		}
	}
	return nil
}

func LookupExecutionDomainTask(task *G) *ExecutionDomainTask {
	if task == nil {
		return nil
	}
	executionDomainIndexLock()
	entry := executionDomainTaskLocked(task)
	executionDomainIndexUnlock()
	return entry
}

// InheritableExecutionDomainTask differs from lookup only during a scoped
// detached spawn (for example, finalizer callbacks). Suppression never changes
// the parent's bubble identity, wait accounting or ordinary G/frame layout.
func InheritableExecutionDomainTask(task *G) *ExecutionDomainTask {
	if task == nil {
		return nil
	}
	executionDomainIndexLock()
	entry := executionDomainTaskLocked(task)
	if entry != nil && entry.inheritanceSuppressed != 0 {
		entry = nil
	}
	executionDomainIndexUnlock()
	return entry
}

func SuppressExecutionDomainInheritance(task *G) bool {
	executionDomainIndexLock()
	entry := executionDomainTaskLocked(task)
	ok := entry != nil && entry.inheritanceSuppressed != ^uint32(0)
	if ok {
		entry.inheritanceSuppressed++
	}
	executionDomainIndexUnlock()
	return ok
}

func RestoreExecutionDomainInheritance(task *G) bool {
	executionDomainIndexLock()
	entry := executionDomainTaskLocked(task)
	ok := entry != nil && entry.inheritanceSuppressed != 0
	if ok {
		entry.inheritanceSuppressed--
	}
	executionDomainIndexUnlock()
	return ok
}

func AttachExecutionDomainTask(task *G, entry *ExecutionDomainTask, controller bool) bool {
	if task == nil || entry == nil || entry.Activity == nil || entry.task != nil || entry.next != nil {
		return false
	}
	executionDomainIndexLock()
	if executionDomainTaskLocked(task) != nil || !controller && !entry.Activity.Join(&entry.member) {
		executionDomainIndexUnlock()
		return false
	}
	bucket := executionDomainTaskBucket(task)
	entry.task, entry.control = task, controller
	entry.next = executionDomainTaskIndex.buckets[bucket]
	executionDomainTaskIndex.buckets[bucket] = entry
	executionDomainIndexUnlock()
	return true
}

// DetachExecutionDomainTask is also the terminal scheduler hook. Admission is
// removed before the G allocation can be reused; the returned notification owns
// its own domain lease and never retains the retired task address.
func DetachExecutionDomainTask(task *G) (OperationID, bool) {
	executionDomainIndexLock()
	link := &executionDomainTaskIndex.buckets[executionDomainTaskBucket(task)]
	for *link != nil && (*link).task != task {
		link = &(*link).next
	}
	entry := *link
	if entry == nil {
		executionDomainIndexUnlock()
		return OperationID{}, true
	}
	id, ok := OperationID{}, true
	if !entry.control {
		id, ok = entry.member.Leave()
	}
	if ok {
		removeExecutionDomainManualLocked(entry)
		*link = entry.next
		entry.task, entry.next = nil, nil
	}
	executionDomainIndexUnlock()
	return id, ok
}

// PrepareExecutionDomainDurableWait must precede exposing a source endpoint to
// any producer. Ordinary and controller tasks require no accounting change.
func PrepareExecutionDomainDurableWait(task *G) bool {
	executionDomainIndexLock()
	entry := executionDomainTaskLocked(task)
	ok := entry == nil || entry.control || entry.member.PrepareDurable()
	executionDomainIndexUnlock()
	return ok
}

// PublishExecutionDomainTask records an irreversible completion before its
// source mailbox/ready queue is published. It is safe from a foreign P while
// that source's admission pins the exact task generation.
func PublishExecutionDomainTask(task *G) bool {
	executionDomainIndexLock()
	entry := executionDomainTaskLocked(task)
	ok := publishExecutionDomainEntryLocked(entry)
	executionDomainIndexUnlock()
	return ok
}

func publishExecutionDomainEntryLocked(entry *ExecutionDomainTask) bool {
	ok := true
	if entry != nil && !entry.control {
		d := entry.Activity
		d.acquireLock()
		ok = entry.member.alive
		if ok && entry.member.prepared {
			entry.member.published = true
			if !entry.member.active {
				entry.member.active = true
				d.running++
			}
		}
		d.releaseLock()
	}
	return ok
}

func executionDomainManualBucket(id OperationID) uint32 {
	return (id.SourceSlot ^ id.Generation) % uint32(len(executionDomainTaskIndex.manualBuckets))
}

func removeExecutionDomainManualLocked(entry *ExecutionDomainTask) {
	if !entry.manual.Valid() {
		return
	}
	link := &executionDomainTaskIndex.manualBuckets[executionDomainManualBucket(entry.manual)]
	for *link != nil && *link != entry {
		link = &(*link).manualNext
	}
	if *link == entry {
		*link = entry.manualNext
	}
	entry.manual, entry.manualNext = OperationID{}, nil
}

// BindExecutionDomainManualWait retains only a bubbled task's exact generation
// before exposing a keyed waiter. The source producer remains pointer-free:
// optional lookup by OperationID supplies its activity credit before Posted.
func BindExecutionDomainManualWait(task *G, id OperationID) bool {
	if !id.Valid() || id.Source() != OperationSourceManual {
		return false
	}
	executionDomainIndexLock()
	entry := executionDomainTaskLocked(task)
	ok := true
	if entry != nil && !entry.control {
		ok = !entry.manual.Valid() && entry.member.PrepareDurable()
		if ok {
			bucket := executionDomainManualBucket(id)
			entry.manual, entry.manualNext = id, executionDomainTaskIndex.manualBuckets[bucket]
			executionDomainTaskIndex.manualBuckets[bucket] = entry
		}
	}
	executionDomainIndexUnlock()
	return ok
}

func executionDomainManualLocked(id OperationID) *ExecutionDomainTask {
	for entry := executionDomainTaskIndex.manualBuckets[executionDomainManualBucket(id)]; entry != nil; entry = entry.manualNext {
		if entry.manual == id {
			return entry
		}
	}
	return nil
}

func ExecutionDomainManualContext(id OperationID) unsafe.Pointer {
	executionDomainIndexLock()
	entry := executionDomainManualLocked(id)
	var context unsafe.Pointer
	if entry != nil {
		context = entry.Context
	}
	executionDomainIndexUnlock()
	return context
}

func PublishExecutionDomainManual(id OperationID) bool {
	executionDomainIndexLock()
	ok := publishExecutionDomainEntryLocked(executionDomainManualLocked(id))
	executionDomainIndexUnlock()
	return ok
}

func parkExecutionDomainTask(task *G) bool {
	executionDomainIndexLock()
	entry := executionDomainTaskLocked(task)
	id, ok := OperationID{}, true
	if entry != nil && !entry.control {
		id, ok = entry.member.Park()
	}
	executionDomainIndexUnlock()
	return ok && (!id.Valid() || entry.Activity.PublishWake(id))
}

func resumeExecutionDomainTask(task *G) bool {
	executionDomainIndexLock()
	entry := executionDomainTaskLocked(task)
	if entry != nil {
		removeExecutionDomainManualLocked(entry)
	}
	ok := entry == nil || entry.control || entry.member.Resume()
	executionDomainIndexUnlock()
	return ok
}

func retireExecutionDomainTask(task *G) bool {
	entry := LookupExecutionDomainTask(task)
	id, ok := DetachExecutionDomainTask(task)
	return ok && (!id.Valid() || entry.Activity.PublishWake(id))
}
