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

//go:linkname mark C.llgo_normal_cleanup_mark
func mark(int32)

func deferredClose(fd int32, release <-chan struct{}) {
	mark(1)
	// This defer started during NORMAL return. Main's subsequent shutdown
	// must not abandon it between these awaits and the final physical close.
	<-release
	closeFD(fd)
}

func owner(release <-chan struct{}) {
	fd := openFD()
	if fd < 0 {
		panic("normal cleanup fixture could not open its fd")
	}
	defer deferredClose(fd, release)
}

func sentinel(ready chan<- struct{}, release chan struct{}) {
	defer func() {
		close(release)
		mark(2)
	}()
	ready <- struct{}{}
	for {
		runtime.Gosched()
	}
}

func main() {
	runtime.GOMAXPROCS(1)
	install()
	ready := make(chan struct{})
	release := make(chan struct{})
	go sentinel(ready, release)
	<-ready
	go owner(release)
	for started() == 0 {
		runtime.Gosched()
	}
	// Only shutdown cleanup releases the already-running normal defer. The
	// C atexit checker requires explicit close, not process fd reclamation.
}
