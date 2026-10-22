package main

import (
	corort "github.com/xgo-dev/llgo/runtime/internal/runtime"
	_ "sync"
	_ "unsafe"
)

//go:linkname run internal/synctest.Run
func run(func())

//go:linkname wait internal/synctest.Wait
func wait()

// This child does not read getg in source code. The channel adapter must still
// observe its inherited bubble through the installed logical runtime context.
//
//go:noinline
func receiveOnly(ch <-chan struct{}, done *bool) {
	<-ch
	*done = true
}

func main() {
	if _, active := corort.SynctestNow(); active {
		panic("ordinary task acquired bubble time")
	}
	run(func() {
		ch := make(chan struct{})
		done := false
		go receiveOnly(ch, &done)
		wait()
		if done {
			panic("receive did not remain blocked")
		}
		close(ch)
		wait()
		if !done {
			panic("receive did not complete")
		}
	})
	println("synctest context-free child receive ok")
}
