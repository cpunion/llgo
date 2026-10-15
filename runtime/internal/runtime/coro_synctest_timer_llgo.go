//go:build llgo && llgo_coro && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal) || wasm || tinygo.wasm || baremetal || llgo_coro_host)

package runtime

import "sync/atomic"

// SynctestTimer is an optional fake-clock event, not a goroutine or executor.
// The bubble controller consumes these deadlines only after the shared activity
// ledger proves quiescence. Ordinary timers and ordinary G layouts are unchanged.
type SynctestTimer struct {
	bubble     *coroSynctestBubble
	next       *SynctestTimer
	when       int64
	period     int64
	generation uint32
	active     bool
	fire       func(generation uint32, now, when int64)
	weakLive   *uintptr
}

func NewSynctestTimer(fire func(uint32, int64, int64), weakLive *uintptr) *SynctestTimer {
	b := currentSynctestBubble()
	if b == nil || fire == nil {
		panic("synctest: timer requires a bubble and callback")
	}
	return &SynctestTimer{bubble: b, fire: fire, weakLive: weakLive}
}

// Fake channel timers share collector-owned liveness cells with real timers.
// The pointer keeps the cell itself alive, but never the channel. Prune during
// the controller's existing traversal, before choosing the next fake deadline;
// no global GC observer, timer manager G, callback, or second scheduler is used.
// AfterFunc has no weak cell and keeps its callback until delivery.
func (b *coroSynctestBubble) pruneTimersLocked() {
	link := &b.timers
	for *link != nil {
		t := *link
		if t.weakLive != nil && atomic.LoadUintptr(t.weakLive) == 0 {
			*link, t.next, t.active = t.next, nil, false
			continue
		}
		link = &t.next
	}
}

func (t *SynctestTimer) checkOwner(message string) {
	if t == nil || currentSynctestBubble() != t.bubble {
		coroRuntimeAbort(message)
	}
}

func (t *SynctestTimer) removeLocked() {
	if !t.active {
		return
	}
	link := &t.bubble.timers
	for *link != nil && *link != t {
		link = &(*link).next
	}
	if *link != t {
		panic("synctest: missing active timer")
	}
	*link, t.next, t.active = t.next, nil, false
}

func (t *SynctestTimer) insertLocked() {
	link := &t.bubble.timers
	for *link != nil && (*link).when <= t.when {
		link = &(*link).next
	}
	t.next, *link, t.active = *link, t, true
}

func (t *SynctestTimer) nextGenerationLocked() {
	t.generation++
	if t.generation == 0 {
		panic("synctest: timer generation exhausted")
	}
}

func nextSynctestTimerDeadline(when, period, now int64) int64 {
	const max = int64(1<<63 - 1)
	if period <= 0 {
		return 0
	}
	steps := int64(1)
	if now > when {
		steps += (now - when) / period
	}
	if steps > (max-when)/period {
		return max
	}
	return when + steps*period
}

// takeTimerLocked retires the due generation before callback publication. A
// periodic timer is requeued once, skipping missed ticks instead of bursting.
func (b *coroSynctestBubble) takeTimerLocked() (*SynctestTimer, uint32, int64) {
	t := b.timers
	if t == nil || t.when > b.now {
		return nil, 0, 0
	}
	b.timers, t.next, t.active = t.next, nil, false
	when := t.when
	if t.period > 0 {
		t.when = nextSynctestTimerDeadline(when, t.period, b.now)
		t.insertLocked()
	}
	return t, t.generation, when
}

// Reset returns the previous pending state and an optional immediate delivery.
// The caller publishes that delivery before returning to user code, outside
// both the bubble lock and the channel timer's Stop/Reset serialization lock.
func (t *SynctestTimer) Reset(when, period int64) (pending bool, immediate bool, generation uint32, now int64) {
	t.checkOwner("reset of synctest timer from outside bubble")
	b := t.bubble
	b.acquireLock()
	pending = t.active
	t.removeLocked()
	t.nextGenerationLocked()
	t.when, t.period = when, period
	generation, now = t.generation, b.now
	immediate = when <= now
	if !immediate {
		t.insertLocked()
	} else if period > 0 {
		t.when = nextSynctestTimerDeadline(when, period, now)
		t.insertLocked()
	}
	b.releaseLock()
	return
}

func (t *SynctestTimer) Stop() bool {
	t.checkOwner("stop of synctest timer from outside bubble")
	b := t.bubble
	b.acquireLock()
	pending := t.active
	t.removeLocked()
	t.nextGenerationLocked()
	b.releaseLock()
	return pending
}

// Current excludes callbacks invalidated by a concurrent Stop or Reset. The
// channel bridge holds its send lock while checking and actually publishing.
func (t *SynctestTimer) Current(generation uint32) bool {
	b := t.bubble
	b.acquireLock()
	current := t.generation == generation
	b.releaseLock()
	return current
}
