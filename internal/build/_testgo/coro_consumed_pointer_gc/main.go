package main

import (
	"runtime"
	"time"
)

type receiver struct {
	values <-chan int
	done   chan<- struct{}
}

type sender struct {
	values chan<- int
	done   <-chan struct{}
}

func makePair() (*sender, *receiver) {
	values := make(chan int)
	done := make(chan struct{})
	r := &receiver{values, done}
	runtime.SetFinalizer(r, func(r *receiver) { close(r.done) })
	return &sender{values, done}, r
}

func main() {
	// Keep this caller alive after the receiving goroutine exits. Neither the
	// consumed child-result slot nor an early capture-cell address spill should
	// keep r alive for this caller's whole lifetime. Do not set r to nil: doing
	// so would conceal the stale spill that this regression exercises.
	s, r := makePair()
	received := make(chan struct{})
	go func() {
		<-r.values
		close(received)
	}()
	finished := make(chan struct{})
	go func() {
		s.values <- 1
		<-s.done
		close(finished)
	}()
	<-received
	deadline := time.After(3 * time.Second)
	for {
		// Collection may be delayed. Repeated cycles check the implementation's
		// root retirement, not a Go guarantee about prompt finalizer execution.
		runtime.Gosched()
		runtime.GC()
		select {
		case <-finished:
			println("consumed pointer result is collectible with a live caller")
			return
		case <-deadline:
			panic("unreachable receiver retained by live caller")
		default:
		}
	}
}
