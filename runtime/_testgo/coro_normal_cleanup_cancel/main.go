//go:build darwin || linux

package main

import (
	"runtime"
	_ "unsafe"
)

const LLGoFiles = "wrap/cleanup.c"

//go:linkname install C.llgo_normal_cleanup_install
func install()

//go:linkname openFD C.llgo_normal_cleanup_open
func openFD() int32

//go:linkname closeFD C.llgo_normal_cleanup_close
func closeFD(int32)

//go:linkname started C.llgo_normal_cleanup_started
func started() int32

//go:linkname released C.llgo_normal_cleanup_released
func released() int32

//go:linkname markStarted C.llgo_normal_cleanup_mark_started
func markStarted()

//go:linkname release C.llgo_normal_cleanup_release
func release()

func deferredClose(fd int32) {
	markStarted()
	// This defer started during NORMAL return. Main's subsequent shutdown
	// must not abandon it between these awaits and the final physical close.
	for released() == 0 {
		runtime.Gosched()
	}
	closeFD(fd)
}

func owner() {
	fd := openFD()
	if fd < 0 {
		panic("normal cleanup fixture could not open its fd")
	}
	defer deferredClose(fd)
}

func sentinel(ready chan<- struct{}) {
	defer release()
	ready <- struct{}{}
	for {
		runtime.Gosched()
	}
}

func main() {
	runtime.GOMAXPROCS(1)
	install()
	ready := make(chan struct{})
	go sentinel(ready)
	<-ready
	go owner()
	for started() == 0 {
		runtime.Gosched()
	}
	// Only shutdown cleanup releases the already-running normal defer. The
	// C atexit checker requires explicit close, not process fd reclamation.
}
