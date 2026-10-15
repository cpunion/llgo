package main

import (
	"os"
	"runtime"
)

//go:noinline
func mainPanicSite() {
	panic("main-panic-with-peers") // MAIN_PANIC_MARK
}

func main() {
	switch os.Args[1] {
	case "parked":
		started := make(chan struct{})
		go func() {
			close(started)
			select {}
		}()
		<-started
		runtime.Gosched()
	case "ready":
		started := make(chan struct{})
		go func() {
			close(started)
			for {
				runtime.Gosched()
			}
		}()
		<-started
	case "last":
		// Retain the original last-G terminal-close/reporting path.
	default:
		panic("invalid peer mode")
	}
	mainPanicSite()
}
