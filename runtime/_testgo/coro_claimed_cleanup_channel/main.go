//go:build darwin || linux

package main

import (
	"runtime"
	_ "unsafe"
)

const LLGoFiles = "wrap/cleanup.c"

//go:linkname install C.llgo_claimed_cleanup_install
func install()

//go:linkname openFD C.llgo_claimed_cleanup_open
func openFD() int32

//go:linkname closeFD C.llgo_claimed_cleanup_close
func closeFD(int32)

//go:linkname mark C.llgo_claimed_cleanup_mark
func mark(int32)

func deferredClose(fd int32, entered chan struct{}, release <-chan struct{}) {
	mark(1)
	close(entered)
	<-release
	closeFD(fd)
}

func owner(ready, entered chan struct{}, release <-chan struct{}) {
	fd := openFD()
	if fd < 0 {
		panic("claimed cleanup fixture could not open its fd")
	}
	defer deferredClose(fd, entered, release)
	close(ready)
	var never chan struct{}
	<-never
}

func sentinel(ready chan struct{}, entered <-chan struct{}, release chan struct{}) {
	defer func() {
		<-entered
		close(release)
		mark(2)
	}()
	close(ready)
	var never chan struct{}
	<-never
}

func main() {
	runtime.GOMAXPROCS(1)
	install()
	ownerReady := make(chan struct{})
	sentinelReady := make(chan struct{})
	entered := make(chan struct{})
	release := make(chan struct{})
	go owner(ownerReady, entered, release)
	go sentinel(sentinelReady, entered, release)
	<-ownerReady
	<-sentinelReady
	// This is an LLGo structured-shutdown regression, not a Go promise to run
	// every goroutine's defer after main returns. Both tasks have claimed their
	// stop before entering cleanup. Whichever runs first must park in cleanup;
	// its peer's channel close must deliver that wait without losing the cause.
}
