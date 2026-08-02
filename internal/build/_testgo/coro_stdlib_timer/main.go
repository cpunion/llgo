package main

import (
	"context"
	"os"
	"sync"
	"time"
)

const demandPageConcurrency = 600

func fail(code int) {
	os.Exit(code)
}

func elapsedAtLeast(start time.Time, minimum time.Duration) bool {
	return time.Since(start) >= minimum
}

func main() {
	// Since Go 1.23, timer channels present synchronous len/cap semantics.
	timer := time.NewTimer(30 * time.Millisecond)
	if len(timer.C) != 0 || cap(timer.C) != 0 {
		fail(10)
	}
	started := time.Now()
	<-timer.C
	if !elapsedAtLeast(started, 15*time.Millisecond) || timer.Stop() {
		fail(11)
	}

	// Stop must suppress a not-yet-delivered value. Reset of a stopped timer
	// reports false and starts one fresh logical generation.
	timer = time.NewTimer(200 * time.Millisecond)
	if !timer.Stop() {
		fail(12)
	}
	select {
	case <-timer.C:
		fail(13)
	case <-time.After(20 * time.Millisecond):
	}
	started = time.Now()
	if timer.Reset(30 * time.Millisecond) {
		fail(14)
	}
	<-timer.C
	if !elapsedAtLeast(started, 15*time.Millisecond) {
		fail(15)
	}

	// Reset of an active timer reports true, changes its deadline, and cannot
	// leave an old generation visible after the replacement fires.
	timer = time.NewTimer(200 * time.Millisecond)
	started = time.Now()
	if !timer.Reset(30 * time.Millisecond) {
		fail(16)
	}
	<-timer.C
	if !elapsedAtLeast(started, 15*time.Millisecond) {
		fail(17)
	}
	select {
	case <-timer.C:
		fail(18)
	case <-time.After(20 * time.Millisecond):
	}

	started = time.Now()
	<-time.After(25 * time.Millisecond)
	if !elapsedAtLeast(started, 12*time.Millisecond) {
		fail(19)
	}

	ticker := time.NewTicker(15 * time.Millisecond)
	if len(ticker.C) != 0 || cap(ticker.C) != 0 {
		fail(20)
	}
	previous := time.Time{}
	for range 3 {
		current := <-ticker.C
		if !previous.IsZero() && !current.After(previous) {
			fail(21)
		}
		previous = current
	}
	ticker.Stop()
	select {
	case <-ticker.C:
		fail(22)
	case <-time.After(25 * time.Millisecond):
	}

	callback := make(chan int)
	after := time.AfterFunc(150*time.Millisecond, func() { callback <- 1 })
	if !after.Stop() {
		fail(23)
	}
	select {
	case <-callback:
		fail(24)
	case <-time.After(20 * time.Millisecond):
	}
	if after.Reset(25 * time.Millisecond) {
		fail(25)
	}
	select {
	case value := <-callback:
		if value != 1 {
			fail(26)
		}
	case <-time.After(250 * time.Millisecond):
		fail(27)
	}
	if after.Stop() {
		fail(28)
	}

	// A real multi-event select must arm both waits, choose the first ready
	// generation, and leave the losing timer independently cancellable.
	fast := time.NewTimer(20 * time.Millisecond)
	slow := time.NewTimer(250 * time.Millisecond)
	select {
	case <-fast.C:
	case <-slow.C:
		fail(29)
	}
	if !slow.Stop() {
		fail(30)
	}
	select {
	case <-slow.C:
		fail(31)
	default:
	}

	// Cancellation remains ordinary Go: a timer callback closes context.Done,
	// and select cancels the pending branch without a private Future/Await API.
	ctx, cancel := context.WithCancel(context.Background())
	cancelTimer := time.AfterFunc(20*time.Millisecond, cancel)
	select {
	case <-ctx.Done():
		if ctx.Err() != context.Canceled {
			fail(32)
		}
	case <-time.After(250 * time.Millisecond):
		fail(33)
	}
	if cancelTimer.Stop() {
		fail(34)
	}

	// Exceed eight executors' aggregate inline source capacity. Every child
	// first parks on the same channel, then installs an independent timer after
	// close wakes the cohort. Hosted runtimes must grow both catalogs in stable
	// 64-operation pages without changing ordinary Go call style.
	var demandReady, demandDone sync.WaitGroup
	demandReady.Add(demandPageConcurrency)
	demandDone.Add(demandPageConcurrency)
	demandStart := make(chan struct{})
	for range demandPageConcurrency {
		go func() {
			demandReady.Done()
			<-demandStart
			time.Sleep(time.Millisecond)
			demandDone.Done()
		}()
	}
	demandReady.Wait()
	close(demandStart)
	demandDone.Wait()
}
