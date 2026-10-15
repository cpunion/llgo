package main

import (
	"time"
	_ "unsafe"
)

const LLGoFiles = "_wrap/host.c"
const LLGoPackage = "link: --wrap=main"

//go:linkname markPhase C.llgo_baremetal_timer_phase
func markPhase(phase uint32)

func main() {
	time.Sleep(0)
	time.Sleep(-1)
	markPhase(1)
	start := time.Now()
	time.Sleep(20 * time.Nanosecond)
	if time.Since(start) < 20*time.Nanosecond {
		panic("baremetal Sleep returned early")
	}
	t := time.NewTimer(30 * time.Nanosecond)
	if !t.Stop() || t.Reset(50*time.Nanosecond) {
		panic("baremetal Timer Stop/Reset")
	}
	select {
	case <-t.C:
		panic("reset timer fired before host turn")
	default:
	}
	<-t.C
	ticker := time.NewTicker(5 * time.Nanosecond)
	<-ticker.C
	ticker.Stop()
	done := make(chan struct{})
	time.AfterFunc(7*time.Nanosecond, func() { close(done) })
	<-done
	markPhase(2)
}
