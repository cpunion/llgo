//go:build coro_synctest_timer_test && !llgo

package runtime

import (
	"sync"
	"sync/atomic"
	"testing"
)

// This named source-island fixture runs the production optional timer queue
// without an LLVM compiler, target event loop or physical clock.
type coroSynctestBubble struct {
	lock   sync.Mutex
	timers *SynctestTimer
	now    int64
}

func (b *coroSynctestBubble) acquireLock() { b.lock.Lock() }
func (b *coroSynctestBubble) releaseLock() { b.lock.Unlock() }

var timerTestBubble *coroSynctestBubble

func currentSynctestBubble() *coroSynctestBubble { return timerTestBubble }

func coroRuntimeAbort(message string) { panic(message) }

func TestSynctestTimerQueueAndGeneration(t *testing.T) {
	b := &coroSynctestBubble{now: 1000}
	timerTestBubble = b
	defer func() { timerTestBubble = nil }()
	callback := func(uint32, int64, int64) { t.Fatal("queue ran managed callback under lock") }
	one, two := NewSynctestTimer(callback, nil), NewSynctestTimer(callback, nil)
	if pending, due, _, now := one.Reset(1200, 0); pending || due || now != 1000 {
		t.Fatal("future reset")
	}
	if pending, due, generation, _ := two.Reset(900, 100); pending || !due || !two.Current(generation) {
		t.Fatal("immediate periodic reset")
	}
	if b.timers != two || two.when != 1100 || two.next != one {
		t.Fatal("missed periodic ticks did not skip to future deadline")
	}
	if timer, _, _ := b.takeTimerLocked(); timer != nil {
		t.Fatal("future timer became falsely ready")
	}
	b.now = 1100
	timer, generation, when := b.takeTimerLocked()
	if timer != two || when != 1100 || two.when != 1200 || !two.Current(generation) {
		t.Fatal("periodic delivery")
	}
	if !two.Stop() || two.Current(generation) || two.Stop() {
		t.Fatal("Stop did not invalidate exact delivered generation")
	}
	b.now = 1200
	timer, generation, when = b.takeTimerLocked()
	if timer != one || when != 1200 || one.active || !one.Current(generation) || b.timers != nil {
		t.Fatal("one-shot retired before managed callback")
	}
	if pending, due, fresh, _ := one.Reset(1200, 0); pending || !due || fresh == generation || one.Current(generation) {
		t.Fatal("Reset(0) generation did not supersede old delivery")
	}
}

func TestSynctestTimerRejectsForeignBubble(t *testing.T) {
	timerTestBubble = new(coroSynctestBubble)
	defer func() { timerTestBubble = nil }()
	timer := NewSynctestTimer(func(uint32, int64, int64) {}, nil)
	timerTestBubble = new(coroSynctestBubble)
	defer func() {
		if recover() == nil {
			t.Fatal("foreign Stop accepted")
		}
	}()
	timer.Stop()
}

func TestSynctestTimerDeadlineSaturates(t *testing.T) {
	const max = int64(1<<63 - 1)
	if got := nextSynctestTimerDeadline(max-10, 20, max-5); got != max {
		t.Fatalf("overflow deadline = %d", got)
	}
}

func TestSynctestTimerPrunesWeakChannelsBeforeChoosingDeadline(t *testing.T) {
	b := &coroSynctestBubble{now: 1000}
	timerTestBubble = b
	defer func() { timerTestBubble = nil }()
	deadCell, liveCell := new(uintptr), new(uintptr)
	*deadCell, *liveCell = 1, 1
	callback := func(uint32, int64, int64) { t.Fatal("pruning ran managed callback") }
	dead := NewSynctestTimer(callback, deadCell)
	live := NewSynctestTimer(callback, liveCell)
	afterFunc := NewSynctestTimer(callback, nil)
	dead.Reset(1100, 100)
	live.Reset(1200, 0)
	afterFunc.Reset(1300, 0)
	atomic.StoreUintptr(deadCell, 0)
	b.pruneTimersLocked()
	if dead.active || dead.next != nil || b.timers != live || live.next != afterFunc {
		t.Fatal("dead channel kept a deadline or pruning removed live ownership")
	}
	atomic.StoreUintptr(liveCell, 0)
	b.pruneTimersLocked()
	if live.active || b.timers != afterFunc || !afterFunc.active {
		t.Fatal("AfterFunc callback incorrectly acquired weak-channel lifetime")
	}
}
