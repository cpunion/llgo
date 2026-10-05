//go:build wasip1

package main

import (
	"os"
	"runtime"
	"time"
	_ "unsafe"
)

const LLGoFiles = "_wrap/probe.c"

//go:linkname probeInit C.llgo_thread_exit_probe_init
func probeInit() int32

//go:linkname probeSet C.llgo_thread_exit_probe_set
func probeSet() int32

//go:linkname probeDestructors C.llgo_thread_exit_probe_destructors
func probeDestructors() uint32

//go:linkname probePages C.llgo_thread_exit_probe_pages
func probePages() uint32

//go:linkname probeCThreads C.llgo_thread_exit_probe_c_threads
func probeCThreads() int32

//go:noinline
func privateRoot() *[1024]byte {
	root := new([1024]byte)
	root[0] = 42
	return root
}

func main() {
	if probeInit() != 0 {
		panic("pthread key creation failed")
	}
	useGoexit := len(os.Args) > 1 && os.Args[1] == "goexit"
	var warmPages, peakPages uint32
	for i := range 400 {
		done := make(chan struct{})
		go func() {
			if probeSet() != 0 {
				panic("pthread key assignment failed")
			}
			// Exercise the worker's private root while other threads are
			// parked, then retire its runtime and libc TLS in either mode.
			root := privateRoot()
			if i%25 == 0 {
				runtime.GC()
			}
			defer func() {
				if root[0] != 42 || recover() != nil {
					panic("Goexit defer or private root lost")
				}
				close(done)
			}()
			if useGoexit {
				runtime.Goexit()
			}
		}()
		<-done
		deadline := time.Now().Add(2 * time.Second)
		for probeDestructors() != uint32(i+1) {
			if time.Now().After(deadline) {
				panic("thread TLS destructor did not complete")
			}
			time.Sleep(time.Millisecond)
		}
		// A TLS destructor precedes libc's final thread-list removal.
		time.Sleep(time.Millisecond)
		pages := probePages()
		if i == 31 {
			warmPages = pages
		}
		if i >= 31 && pages > peakPages {
			peakPages = pages
		}
	}
	// Each detached stack is 1 MiB. Allow a few overlapping exits and
	// allocator bookkeeping, but never a stack per retired goroutine.
	if peakPages > warmPages+(8<<20)/65536 {
		panic("retired pthread stacks kept growing linear memory")
	}
	if probeCThreads() != 0 {
		panic("C pthread return/exit/cleanup/join failed")
	}
	println("wasi thread exit memory pages", warmPages, peakPages)
	println("wasi thread exit resources ok")
}
