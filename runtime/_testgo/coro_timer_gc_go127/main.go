package main

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"
	"weak"
)

type emptyInterface struct {
	typePointer unsafe.Pointer
	data        unsafe.Pointer
}

type timerReferences struct {
	timers   []weak.Pointer[time.Timer]
	tickers  []weak.Pointer[time.Ticker]
	channels []weak.Pointer[byte]
}

func channelReference(c <-chan time.Time) weak.Pointer[byte] {
	var boxed any = c
	return weak.Make((*byte)((*emptyInterface)(unsafe.Pointer(&boxed)).data))
}

// The publishing G ends before collection, so no conservative native stack or
// suspended producer frame can accidentally keep its last temporary alive.
func discardedTimers(out chan timerReferences) {
	var refs timerReferences
	for i := 0; i < 32; i++ {
		timer := time.NewTimer(time.Hour)
		ticker := time.NewTicker(time.Hour)
		refs.timers = append(refs.timers, weak.Make(timer))
		refs.tickers = append(refs.tickers, weak.Make(ticker))
		refs.channels = append(refs.channels, channelReference(timer.C), channelReference(ticker.C))
	}
	out <- refs
}

func allCollected(refs timerReferences) bool {
	for _, w := range refs.timers {
		if w.Value() != nil {
			return false
		}
	}
	for _, w := range refs.tickers {
		if w.Value() != nil {
			return false
		}
	}
	for _, w := range refs.channels {
		if w.Value() != nil {
			return false
		}
	}
	return true
}

func main() {
	runtime.GC()
	baseline := runtime.NumGoroutine()
	out := make(chan timerReferences)
	go discardedTimers(out)
	refs := <-out
	for i := 0; i < 12; i++ {
		runtime.GC()
		for j := 0; j < 128; j++ {
			runtime.Gosched()
		}
	}
	if !allCollected(refs) {
		panic("unreachable future Timer/Ticker or channel remained strongly alive")
	}
	if n := runtime.NumGoroutine(); n > baseline {
		println("timer manager count", baseline, n)
		panic("collected future timer retained parked manager until deadline")
	}

	// Keeping only the public Timer must keep its channel usable across a
	// collection, even though the parked manager no longer owns either object.
	kept := time.NewTimer(time.Hour)
	runtime.GC()
	if !kept.Reset(0) {
		panic("live public Timer lost its pending generation during collection")
	}
	select {
	case <-kept.C:
	default:
		panic("live public Timer lost its channel during collection")
	}

	// Keeping only C must retain the delivery, but not the public Timer object.
	channel := time.NewTimer(15 * time.Millisecond).C
	runtime.GC()
	<-channel

	// AfterFunc has different ownership: its callback remains live even when
	// callers discard the public Timer. It must not use channel weak semantics.
	done := make(chan struct{})
	time.AfterFunc(15*time.Millisecond, func() { close(done) })
	runtime.GC()
	<-done
	const success = "timer/ticker collection and manager retirement ok\n"
	if n, err := syscall.Write(1, []byte(success)); n != len(success) || err != nil {
		panic("timer acceptance completion marker write failed")
	}
}
