//go:build llgo.wasm_workers

package main

import (
	"runtime"
	_ "unsafe"
)

//go:linkname schedulerProcID github.com/goplus/llgo/runtime/internal/runtime.SchedulerProcID
func schedulerProcID() int

const workerPayloadValue = 0xabcdef01

func testWorkerCBoundary() {
	for {
		proc := make(chan int)
		start := make(chan bool)
		done := make(chan uint64)
		go func() {
			proc <- schedulerProcID()
			if !<-start {
				done <- 0
				return
			}
			live := &payload{value: workerPayloadValue}
			value := holdPayloadInC(live)
			runtime.Gosched()
			done <- value
		}()
		if <-proc == 0 {
			start <- false
			<-done
			continue
		}
		start <- true
		for cHoldEntered() == 0 {
			runtime.Gosched()
		}
		runtime.GC()
		if value := <-done; value != workerPayloadValue {
			panic("C boundary lost a live Go pointer")
		}
		return
	}
}
