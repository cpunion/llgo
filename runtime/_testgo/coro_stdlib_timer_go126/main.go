package main

import "time"

func fail(message string) {
	panic(message)
}

func noTimerValue(channel <-chan time.Time, message string) {
	select {
	case <-channel:
		fail(message)
	default:
	}
}

func main() {
	stopped := time.NewTimer(time.Hour)
	if len(stopped.C) != 0 || cap(stopped.C) != 0 {
		fail("Go 1.23+ Timer channel does not report synchronous len/cap")
	}
	if !stopped.Stop() {
		fail("active Timer.Stop returned false")
	}
	noTimerValue(stopped.C, "Timer.Stop retained a stale channel value")
	resetAt := time.Now()
	if stopped.Reset(25 * time.Millisecond) {
		fail("stopped Timer.Reset returned true")
	}
	select {
	case <-stopped.C:
		if time.Since(resetAt) < 15*time.Millisecond {
			fail("reset Timer fired before its new deadline")
		}
	case <-time.After(2 * time.Second):
		fail("reset Timer did not fire")
	}

	afterAt := time.Now()
	select {
	case <-time.After(20 * time.Millisecond):
		if time.Since(afterAt) < 10*time.Millisecond {
			fail("time.After fired early")
		}
	case <-time.After(2 * time.Second):
		fail("time.After did not fire")
	}

	activeReset := time.NewTimer(time.Second)
	if !activeReset.Reset(20 * time.Millisecond) {
		fail("active Timer.Reset returned false")
	}
	select {
	case <-activeReset.C:
	case <-time.After(2 * time.Second):
		fail("active reset Timer did not fire")
	}
	noTimerValue(activeReset.C, "Timer.Reset retained an old-generation value")

	ticker := time.NewTicker(15 * time.Millisecond)
	if len(ticker.C) != 0 || cap(ticker.C) != 0 {
		fail("Go 1.23+ Ticker channel does not report synchronous len/cap")
	}
	select {
	case <-ticker.C:
	case <-time.After(2 * time.Second):
		fail("Ticker did not fire")
	}
	time.Sleep(55 * time.Millisecond)
	select {
	case <-ticker.C:
	case <-time.After(2 * time.Second):
		fail("Ticker did not deliver its coalesced tick")
	}
	ticker.Stop()
	noTimerValue(ticker.C, "Ticker.Stop retained a stale tick")

	canceledCallback := make(chan struct{}, 1)
	afterFunc := time.AfterFunc(time.Hour, func() { canceledCallback <- struct{}{} })
	if !afterFunc.Stop() {
		fail("active AfterFunc.Stop returned false")
	}
	select {
	case <-canceledCallback:
		fail("stopped AfterFunc callback ran")
	default:
	}

	firedCallback := make(chan struct{}, 1)
	fired := time.AfterFunc(20*time.Millisecond, func() { firedCallback <- struct{}{} })
	select {
	case <-firedCallback:
	case <-time.After(2 * time.Second):
		fail("AfterFunc callback did not run")
	}
	if fired.Stop() {
		fail("fired AfterFunc.Stop returned true")
	}

	resetCallback := make(chan struct{}, 1)
	resetFunc := time.AfterFunc(time.Hour, func() { resetCallback <- struct{}{} })
	if !resetFunc.Reset(20 * time.Millisecond) {
		fail("active AfterFunc.Reset returned false")
	}
	select {
	case <-resetCallback:
	case <-time.After(2 * time.Second):
		fail("reset AfterFunc callback did not run")
	}

	restartedCallback := make(chan struct{}, 1)
	restarted := time.AfterFunc(time.Hour, func() { restartedCallback <- struct{}{} })
	if !restarted.Stop() {
		fail("AfterFunc.Stop before restart returned false")
	}
	if restarted.Reset(20 * time.Millisecond) {
		fail("stopped AfterFunc.Reset returned true")
	}
	select {
	case <-restartedCallback:
	case <-time.After(2 * time.Second):
		fail("restarted AfterFunc callback did not run")
	}
}
