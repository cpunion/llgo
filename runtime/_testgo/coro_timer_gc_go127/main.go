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

func channelPointer(c <-chan time.Time) unsafe.Pointer {
	var boxed any = c
	return (*emptyInterface)(unsafe.Pointer(&boxed)).data
}

func channelReference(c <-chan time.Time) weak.Pointer[byte] {
	return weak.Make((*byte)(channelPointer(c)))
}

var nativePageSize uintptr
var gcControl string
var retainedTimer *time.Timer

// A conservative collector may scan its mmap endpoint hint as a pointer to
// the first object in an adjacent page. The linked collector's own metadata
// then owns that object, independently of the timer manager under test. Select
// all 32 Timer/Ticker pairs away from native page starts; every selected weak
// pointer must still clear and every manager must retire within the same limit.
func collectorPageStart(p unsafe.Pointer) bool {
	return nativePageSize != 0 && uintptr(p)&(nativePageSize-1) == 0
}

// Producer temporaries are confined to this G, which can retire while main
// collects. The final manager count also requires this producer to have ended.
func discardedTimers(out chan timerReferences) {
	var refs timerReferences
	for attempt := 0; len(refs.timers) < 32; attempt++ {
		if attempt >= 128 {
			panic("could not select 32 timer pairs away from collector page boundaries")
		}
		timer := time.NewTimer(time.Hour)
		ticker := time.NewTicker(time.Hour)
		if collectorPageStart(unsafe.Pointer(timer)) || collectorPageStart(unsafe.Pointer(ticker)) ||
			collectorPageStart(channelPointer(timer.C)) || collectorPageStart(channelPointer(ticker.C)) {
			timer.Stop()
			ticker.Stop()
			continue
		}
		if gcControl == "keep-timer" && len(refs.timers) == 0 {
			retainedTimer = timer
		}
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
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		nativePageSize = uintptr(syscall.Getpagesize())
	}
	gcControl, _ = syscall.Getenv("LLGO_TIMER_GC_CONTROL")
	if gcControl != "" && gcControl != "keep-timer" && gcControl != "extra-g" {
		panic("unknown timer GC negative control")
	}
	runtime.GC()
	baseline := runtime.NumGoroutine()
	if gcControl == "extra-g" {
		go func() { select {} }()
	}
	out := make(chan timerReferences)
	go discardedTimers(out)
	refs := <-out
	// Weak clearing only requests each timer owner's executor; neither GC
	// nor a fixed number of local Gosched calls joins managers on another P.
	// Wait for BOTH weak clearing and manager retirement. A manager count is
	// not a weak-clearing acknowledgement either. Stopping on the count alone
	// can omit the collections still needed to clear the last weak pointer.
	// The deadline is still far below the hour-long timer deadline. Sleep parks
	// this G without creating a timer manager and lets remote owners complete.
	deadline := time.Now().Add(5 * time.Second)
	collections := 0
	for {
		runtime.GC()
		collections++
		for j := 0; j < 128; j++ {
			runtime.Gosched()
		}
		if allCollected(refs) && runtime.NumGoroutine() <= baseline {
			break
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !allCollected(refs) {
		println("timer collection attempts", collections, "goroutines", baseline, runtime.NumGoroutine())
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
