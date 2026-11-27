package gotest

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTimerHubManyTimersHeapGrowthAndOrdering(t *testing.T) {
	// Exceeds inline per-shard capacity (16) by >16x across multiple buckets
	// to verify heap growth, siftUp/siftDown, and hub retirement.
	const total = 256
	fired := make(chan int, total)
	timers := make([]*time.Timer, total)
	for i := total - 1; i >= 0; i-- {
		id := i
		delay := time.Duration(2+(i%8)*3) * time.Millisecond
		timers[i] = time.AfterFunc(delay, func() {
			fired <- id
		})
	}
	seen := make([]bool, total)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for count := 0; count < total; count++ {
		select {
		case id := <-fired:
			if seen[id] {
				t.Fatalf("timer %d fired twice", id)
			}
			seen[id] = true
		case <-deadline.C:
			t.Fatalf("timed out waiting for timers (%d/%d fired)", count, total)
		}
	}
	for i, ok := range seen {
		if !ok {
			t.Fatalf("timer %d never fired", i)
		}
	}
}

func TestTimerHubAfterFuncOverlappingResetAndStop(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var runs atomic.Int32
	var timer *time.Timer
	timer = time.AfterFunc(2*time.Millisecond, func() {
		n := runs.Add(1)
		started <- struct{}{}
		if n == 1 {
			<-release
		}
	})
	<-started
	// Callback 1 is still running; Reset must return false and schedule a second
	// independent callback without blocking on callback 1.
	if timer.Reset(2 * time.Millisecond) {
		t.Fatal("Reset while AfterFunc callback is running returned true, want false")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("second overlapping AfterFunc callback did not fire while first was blocked")
	}
	close(release)
	if runs.Load() != 2 {
		t.Fatalf("AfterFunc run count = %d, want 2", runs.Load())
	}
	// Reset to a far deadline, then Stop must cancel cleanly and return true.
	if timer.Reset(time.Hour) {
		t.Fatal("Reset after second callback returned true, want false")
	}
	if !timer.Stop() {
		t.Fatal("Stop on pending far AfterFunc returned false, want true")
	}
	if timer.Stop() {
		t.Fatal("second Stop returned true, want false")
	}
}

func TestTimerHubChannelTimerResetSuppressesStaleTick(t *testing.T) {
	timer := time.NewTimer(2 * time.Millisecond)
	defer timer.Stop()
	// Wait until the timer has fired into its channel without receiving.
	time.Sleep(15 * time.Millisecond)
	if !timer.Reset(time.Hour) {
		t.Fatal("Reset on unread expired Go 1.23+ timer returned false, want true")
	}
	select {
	case <-timer.C:
		t.Fatal("received stale value after Reset")
	default:
	}
	// Reset to an earlier deadline while parked on a long deadline must wake hub.
	if !timer.Reset(5 * time.Millisecond) {
		t.Fatal("Reset on pending far timer returned false, want true")
	}
	select {
	case <-timer.C:
	case <-time.After(2 * time.Second):
		t.Fatal("earlier Reset deadline did not wake timer hub")
	}
	// Stopped timer must not deliver after Stop returns.
	timer.Reset(time.Hour)
	if !timer.Stop() {
		t.Fatal("Stop on pending channel timer returned false, want true")
	}
	select {
	case <-timer.C:
		t.Fatal("stopped timer delivered value")
	default:
	}
}

func TestTimerHubConcurrentContextAndIdleTimerStorm(t *testing.T) {
	const (
		workers = 8
		iters   = 400
	)
	var wg sync.WaitGroup
	var firedUnexpected atomic.Int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idle := time.NewTimer(time.Hour)
			defer idle.Stop()
			for i := 0; i < iters; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if !idle.Stop() {
					firedUnexpected.Add(1)
				}
				idle.Reset(time.Hour)
				cancel()
				if ctx.Err() != context.Canceled {
					t.Errorf("worker %d iter %d: ctx.Err() = %v, want Canceled", id, i, ctx.Err())
					return
				}
			}
		}(w)
	}
	wg.Wait()
	if got := firedUnexpected.Load(); got != 0 {
		t.Fatalf("long idle timers unexpectedly fired %d times", got)
	}

	// Also exercise real short timeouts firing concurrently across shards.
	var timeouts atomic.Int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
			defer cancel()
			<-ctx.Done()
			if ctx.Err() == context.DeadlineExceeded {
				timeouts.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := timeouts.Load(); got != workers {
		t.Fatalf("short context timeouts = %d, want %d", got, workers)
	}
}

func TestTimerHubRetiresManagersWhenEmpty(t *testing.T) {
	base := runtime.NumGoroutine()
	timers := make([]*time.Timer, 64)
	for i := range timers {
		timers[i] = time.NewTimer(time.Hour)
	}
	for _, tm := range timers {
		if !tm.Stop() {
			t.Fatal("Stop on far timer returned false")
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > base {
		t.Fatalf("goroutine count after stopping all timers = %d, want <= %d", got, base)
	}
}
