package coro

// ExecutionDomainActivity accounts for a set of logical tasks independently of
// physical P/M ownership. It is optional, caller-owned storage: neither G nor a
// coroutine frame acquires a field. A runnable task, a non-durable wait, and an
// admitted external producer all prevent quiescence. A durable wait does not.
//
// All operations are non-suspending. The lock protects short scalar transitions
// only; allocation, source publication and user code must happen outside it.
// A quiescence notification transfers one activity lease to its consumer before
// the lock is released. Thus another P cannot observe a second false idle edge
// between notification publication and the controller becoming runnable.
type ExecutionDomainActivity struct {
	lock       uint32
	live       uint32
	running    uint32
	leases     uint32
	wake       OperationID
	wakeIssued bool
	closed     bool
}

// ExecutionDomainMember is allocated only for a task in an execution domain.
// The scheduler adapter owns its lifetime and sparse task-to-member attachment.
// prepared and published close the publish-before-park race without assigning
// every external operation or every ordinary G another permanent field.
type ExecutionDomainMember struct {
	domain    *ExecutionDomainActivity
	alive     bool
	active    bool
	prepared  bool
	published bool
}

type ExecutionDomainSnapshot struct {
	Live, Running, Leases uint32
	WakeIssued            bool
	Closed                bool
}

func (d *ExecutionDomainActivity) acquireLock() {
	for !preemptCompareAndSwap(&d.lock, 0, 1) {
	}
}

func (d *ExecutionDomainActivity) releaseLock() { preemptStore(&d.lock, 0) }

func (d *ExecutionDomainActivity) Snapshot() ExecutionDomainSnapshot {
	d.acquireLock()
	s := ExecutionDomainSnapshot{d.live, d.running, d.leases, d.wakeIssued, d.closed}
	d.releaseLock()
	return s
}

// signalLocked transfers the registered one-shot notification to its caller.
// The caller must publish it with the existing ManualOperationSource protocol;
// consuming that exact notification releases the transferred lease.
func (d *ExecutionDomainActivity) signalLocked() OperationID {
	if d.running != 0 || d.leases != 0 || d.wakeIssued || !d.wake.Valid() {
		return OperationID{}
	}
	d.wakeIssued = true
	d.leases++
	return d.wake
}

func (d *ExecutionDomainActivity) Join(m *ExecutionDomainMember) bool {
	if d == nil || m == nil {
		return false
	}
	d.acquireLock()
	ok := !d.closed && m.domain == nil && d.live != ^uint32(0) && d.running != ^uint32(0)
	if ok {
		*m = ExecutionDomainMember{domain: d, alive: true, active: true}
		d.live++
		d.running++
	}
	d.releaseLock()
	return ok
}

// PrepareDurable precedes publication of any event which can resume this task.
// It does not make the task idle: only the scheduler's committed park does.
func (m *ExecutionDomainMember) PrepareDurable() bool {
	if m == nil || m.domain == nil {
		return false
	}
	d := m.domain
	d.acquireLock()
	ok := m.alive && m.active && !m.prepared
	if ok {
		m.prepared, m.published = true, false
	}
	d.releaseLock()
	return ok
}

// Published is called before a wake becomes visible to the executor. It also
// works before the waiter has physically suspended, and duplicate/select-loser
// publications cannot create multiple activity credits for the same wait.
func (m *ExecutionDomainMember) Published() bool {
	if m == nil || m.domain == nil {
		return false
	}
	d := m.domain
	d.acquireLock()
	ok := m.alive && m.prepared
	if ok {
		m.published = true
		if !m.active {
			m.active = true
			d.running++
		}
	}
	d.releaseLock()
	return ok
}

// Park is called at the committed scheduler transition, not from the Go-facing
// wait wrapper. A non-durable block deliberately remains active.
func (m *ExecutionDomainMember) Park() (OperationID, bool) {
	if m == nil || m.domain == nil {
		return OperationID{}, false
	}
	d := m.domain
	d.acquireLock()
	ok := m.alive && m.active
	if ok && m.prepared && !m.published {
		m.active = false
		d.running--
	}
	id := OperationID{}
	if ok {
		id = d.signalLocked()
	}
	d.releaseLock()
	return id, ok
}

// Resume retires the wait's preparation only after its sources have been
// strongly detached. A nondurable wait and a pre-published durable wait already
// own their activity unit and therefore do not increment it again.
func (m *ExecutionDomainMember) Resume() bool {
	if m == nil || m.domain == nil {
		return false
	}
	d := m.domain
	d.acquireLock()
	ok := m.alive
	if ok {
		if !m.active {
			m.active = true
			d.running++
		}
		m.prepared, m.published = false, false
	}
	d.releaseLock()
	return ok
}

func (m *ExecutionDomainMember) Leave() (OperationID, bool) {
	if m == nil || m.domain == nil {
		return OperationID{}, false
	}
	d := m.domain
	d.acquireLock()
	ok := m.alive
	if ok {
		if m.active {
			d.running--
		}
		d.live--
		m.alive, m.active, m.prepared, m.published = false, false, false, false
	}
	id := OperationID{}
	if ok {
		id = d.signalLocked()
	}
	d.releaseLock()
	return id, ok
}

func (d *ExecutionDomainActivity) Acquire() bool {
	if d == nil {
		return false
	}
	d.acquireLock()
	ok := !d.closed && d.leases != ^uint32(0)
	if ok {
		d.leases++
	}
	d.releaseLock()
	return ok
}

func (d *ExecutionDomainActivity) Release() (OperationID, bool) {
	if d == nil {
		return OperationID{}, false
	}
	d.acquireLock()
	// The notification's lease can only be released by ConsumeWake.
	ok := d.leases > 0 && (!d.wakeIssued || d.leases > 1)
	if ok {
		d.leases--
	}
	id := OperationID{}
	if ok {
		id = d.signalLocked()
	}
	d.releaseLock()
	return id, ok
}

// ArmWake publishes the controller's already-prepared Manual operation. The
// final check repairs a quiescence edge preceding registration, without polling.
func (d *ExecutionDomainActivity) ArmWake(id OperationID) (OperationID, bool) {
	if d == nil || !id.Valid() || id.Source() != OperationSourceManual {
		return OperationID{}, false
	}
	d.acquireLock()
	ok := !d.closed && !d.wake.Valid() && !d.wakeIssued
	if ok {
		d.wake = id
	}
	post := OperationID{}
	if ok {
		post = d.signalLocked()
	}
	d.releaseLock()
	return post, ok
}

func (d *ExecutionDomainActivity) ConsumeWake(id OperationID) bool {
	if d == nil {
		return false
	}
	d.acquireLock()
	ok := id.Valid() && d.wake == id && d.wakeIssued && d.leases > 0
	if ok {
		d.wake, d.wakeIssued = OperationID{}, false
		d.leases--
	}
	d.releaseLock()
	return ok
}

// DisarmWake is the controller's typed cancellation cleanup. Unlike consuming
// a completed wake it also accepts a notification which was never issued.
func (d *ExecutionDomainActivity) DisarmWake(id OperationID) bool {
	d.acquireLock()
	ok := id.Valid() && d.wake == id
	if ok {
		if d.wakeIssued {
			d.leases--
		}
		d.wake, d.wakeIssued = OperationID{}, false
	}
	d.releaseLock()
	return ok
}

// PublishWake joins a late producer with controller cancellation. A retired
// source is harmless only if this exact wake was disarmed or already consumed;
// an armed notification which cannot be published remains an invariant failure.
func (d *ExecutionDomainActivity) PublishWake(id OperationID) bool {
	if !id.Valid() || executionDomainPostNotification(id) {
		return true
	}
	d.acquireLock()
	retired := d.wake != id
	d.releaseLock()
	return retired
}

// Close is a strong lifecycle gate, not cancellation. The adapter must first
// detach all task memberships, producers and registered controller operations.
func (d *ExecutionDomainActivity) Close() bool {
	if d == nil {
		return false
	}
	d.acquireLock()
	ok := !d.closed && d.live == 0 && d.running == 0 && d.leases == 0 && !d.wake.Valid()
	if ok {
		d.closed = true
	}
	d.releaseLock()
	return ok
}
