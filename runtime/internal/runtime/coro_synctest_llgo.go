//go:build llgo && llgo_coro && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal) || wasm || tinygo.wasm || baremetal || llgo_coro_host)

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/coro"
	catomic "github.com/xgo-dev/llgo/runtime/internal/sync/atomic"
)

//go:linkname coroDomainSuspendV1 llgo.coroPark
func coroDomainSuspendV1(state *CoroKeyedParkV2, reserved uint32)

func coroDomainPark(p *coroDomainParkV1) {
	if !coroPrepareKeyedStateV2(&p.state, coroKeyedParkDomainV1, uintptr(unsafe.Pointer(p)), 0) {
		coroRuntimeAbort("cannot prepare execution-domain wait")
		return
	}
	coroDomainSuspendV1(&p.state, 0)
}

func coroPublishDomainParkV1(task *coro.G, state *CoroKeyedParkV2) bool {
	p := (*coroDomainParkV1)(unsafe.Pointer(state))
	if p == nil || state.key != uintptr(unsafe.Pointer(p)) || p.bubble == nil || p.operation.Valid() {
		return false
	}
	p.operation = state.operation
	if p.controller {
		post, ok := p.bubble.activity.ArmWake(p.operation)
		return ok && p.bubble.activity.PublishWake(post)
	}
	if p.event == nil {
		return false
	}
	event := p.event
	event.operation = p.operation
	if event.when != 0 {
		b := p.bubble
		b.acquireLock()
		link := &b.sleepers
		for *link != nil && (*link).when <= event.when {
			link = &(*link).next
		}
		event.next, *link = *link, event
		b.releaseLock()
	}
	return true
}

func coroPostDomainEvent(b *coroSynctestBubble, event *coroDomainEventV1) bool {
	if event == nil || !event.operation.Valid() {
		return false
	}
	id := event.operation
	if coro.PublishExecutionDomainManual(id) && __llgo_coro_domain_post_v1(id.SourceSlot, id.Generation) {
		return true
	}
	b.acquireLock()
	canceled := event.canceled
	b.releaseLock()
	return canceled
}

// SynctestRun uses the ordinary executor's Manual/Park path for its controller.
// It never polls the runnable queue or substitutes repeated Gosched for Wait.
func SynctestRun(f func()) {
	if currentSynctestBubble() != nil {
		panic("synctest.Run called from within a synctest bubble")
	}
	b := &coroSynctestBubble{now: 946684800000000000}
	if !EnterExecutionDomain(unsafe.Pointer(b), &b.activity) {
		panic("synctest: execution-domain entry failed")
	}
	defer LeaveExecutionDomain()
	go func() {
		defer func() { catomic.Store(&b.mainDone, 1) }()
		f()
	}()
	for {
		p := coroDomainParkV1{bubble: b, controller: true}
		coroDomainPark(&p)
		if !b.activity.ConsumeWake(p.operation) {
			panic("synctest: invalid controller notification")
		}
		s := b.activity.Snapshot()
		if s.Running != 0 || s.Leases != 0 {
			continue
		}
		if s.Live == 0 {
			if !b.activity.Close() {
				panic("synctest: execution domain did not close")
			}
			b.acquireLock()
			b.timers = nil
			b.releaseLock()
			return
		}
		b.acquireLock()
		b.pruneTimersLocked()
		waiter := b.waiter
		// Due work has priority over Wait, but Wait never advances the clock.
		// Physical I/O and mutex waits still count as running in the ledger.
		next := int64(0)
		if b.sleepers != nil {
			next = b.sleepers.when
		}
		if b.timers != nil && (next == 0 || b.timers.when < next) {
			next = b.timers.when
		}
		if next > b.now && waiter == nil && catomic.Load(&b.mainDone) == 0 {
			b.now = next
		}
		var due *coroDomainEventV1
		for b.sleepers != nil && b.sleepers.when <= b.now {
			p := b.sleepers
			b.sleepers = p.next
			p.next, due = due, p
		}
		// Extract one timer before releasing the lock: callback code is managed
		// and may spawn, reset a timer, or publish a channel completion.
		timer, generation, when := b.takeTimerLocked()
		now := b.now
		b.releaseLock()
		if timer != nil {
			timer.fire(generation, now, when)
		}
		if due != nil {
			for due != nil {
				p := due
				due, p.next = p.next, nil
				if !coroPostDomainEvent(b, p) {
					panic("synctest: invalid sleep notification")
				}
			}
			continue
		}
		if timer != nil {
			continue
		}
		if waiter != nil {
			if !coroPostDomainEvent(b, waiter) {
				panic("synctest: invalid Wait notification")
			}
			continue
		}
		if catomic.Load(&b.mainDone) != 0 {
			panic("deadlock: main bubble goroutine has exited but blocked goroutines remain")
		}
		panic("deadlock: all goroutines in bubble are blocked")
	}
}

// SynctestNow returns the bubble clock only; executor deadlines continue using
// the real monotonic clock, including while another domain is under test.
func SynctestNow() (int64, bool) {
	// Gate the optional service before its lock-bearing body is colored. The
	// ordinary context lookup still handles tasks outside an enabled bubble.
	if !coro.ExecutionDomainEnabled() {
		return 0, false
	}
	b := currentSynctestBubble()
	if b == nil {
		return 0, false
	}
	b.acquireLock()
	now := b.now
	b.releaseLock()
	return now, true
}

// SynctestSleep returns false outside a bubble, selecting the real timer path.
// Inside one it owns an ordinary Manual operation, not a falsely ready Timer.
func SynctestSleep(delay int64) bool {
	b := currentSynctestBubble()
	if b == nil {
		return false
	}
	if delay <= 0 {
		return true
	}
	b.acquireLock()
	when := b.now + delay
	if when < b.now {
		when = 1<<63 - 1
	}
	b.releaseLock()
	p := coroDomainParkV1{bubble: b, event: &coroDomainEventV1{when: when}}
	coroDomainPark(&p)
	return true
}

func SynctestIsInBubble() bool { return currentSynctestBubble() != nil }

func SynctestAcquire() any {
	b := currentSynctestBubble()
	if b == nil {
		return nil
	}
	if !b.activity.Acquire() {
		panic("synctest: acquire on closed bubble")
	}
	return b
}

func SynctestRelease(bubble any) {
	b, ok := bubble.(*coroSynctestBubble)
	if !ok || b == nil {
		panic("synctest: invalid bubble reference")
	}
	post, ok := b.activity.Release()
	if !ok || !b.activity.PublishWake(post) {
		panic("synctest: invalid bubble release")
	}
}

func SynctestInBubble(bubble any, f func()) {
	if currentSynctestBubble() != nil {
		panic("goroutine is already bubbled")
	}
	b, ok := bubble.(*coroSynctestBubble)
	if !ok || b == nil || !b.activity.Acquire() {
		panic("synctest: invalid bubble reference")
	}
	defer SynctestRelease(bubble)
	if !EnterExecutionDomain(unsafe.Pointer(b), &b.activity) {
		panic("synctest: callback domain entry failed")
	}
	defer LeaveExecutionDomain()
	f()
}

func SynctestWait() {
	b := currentSynctestBubble()
	if b == nil {
		panic("synctest.Wait called from outside a synctest bubble")
	}
	p := coroDomainParkV1{bubble: b, event: new(coroDomainEventV1)}
	b.acquireLock()
	if b.waiter != nil {
		b.releaseLock()
		panic("synctest.Wait already in progress")
	}
	b.waiter = p.event
	b.releaseLock()
	coroDomainPark(&p)
	b.acquireLock()
	b.waiter = nil
	b.releaseLock()
}
