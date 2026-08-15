//go:build !nogc

package main

import "runtime"

type windowsGCProbe struct {
	value int
}

//go:noinline
func makeWindowsGCProbe(finalized chan<- int) {
	probe := &windowsGCProbe{value: 42}
	runtime.SetFinalizer(probe, func(value *windowsGCProbe) {
		finalized <- value.value
	})
}

func checkGC() {
	finalized := make(chan int, 1)
	created := make(chan struct{})
	go func() {
		makeWindowsGCProbe(finalized)
		close(created)
	}()
	<-created

	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	for attempt := 0; attempt < 8; attempt++ {
		runtime.Gosched()
		runtime.GC()
		select {
		case value := <-finalized:
			if value != 42 {
				panic("Windows GC finalizer observed a corrupt object")
			}
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			if after.NumGC <= before.NumGC {
				panic("Windows runtime.GC did not advance MemStats.NumGC")
			}
			return
		default:
		}
	}
	panic("Windows GC did not run the finalizer")
}
