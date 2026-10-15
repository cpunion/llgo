package main

import (
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"
	"weak"
)

//go:linkname run internal/synctest.Run
func run(func())

//go:linkname wait internal/synctest.Wait
func wait()

//go:linkname associate internal/synctest.associate
func associate(unsafe.Pointer) int

//go:linkname disassociate internal/synctest.disassociate
func disassociate(unsafe.Pointer)

//go:linkname associated internal/synctest.isAssociated
func associated(unsafe.Pointer) bool

var global [64]byte

//go:noinline
func disposableAssociation() weak.Pointer[[256]byte] {
	p := new([256]byte)
	p[0] = 42
	if associate(unsafe.Pointer(p)) != 1 {
		panic("heap object not associated")
	}
	return weak.Make(p)
}

func main() {
	p := new([64]byte)
	p[0] = 7
	run(func() {
		if associate(unsafe.Pointer(&global)) != 0 || associated(unsafe.Pointer(&global)) {
			panic("package global acquired heap association")
		}
		if associate(unsafe.Pointer(p)) != 1 || !associated(unsafe.Pointer(p)) {
			panic("missing current association")
		}
		go func() {
			if associate(unsafe.Pointer(p)) != 1 {
				panic("child did not share bubble identity")
			}
		}()
		wait()

		wg := new(sync.WaitGroup)
		wg.Add(1)
		completed := false
		go func() { wg.Wait(); completed = true }()
		wait()
		if completed {
			panic("WaitGroup was not durably blocked")
		}
		wg.Done()
		wait()
		if !completed {
			panic("WaitGroup completion lost publication credit")
		}
		start := time.Now()
		wg.Add(1)
		go func() { time.Sleep(time.Hour); wg.Done() }()
		wg.Wait()
		if time.Since(start) != time.Hour {
			panic("durable WaitGroup prevented fake clock advance")
		}
		w := disposableAssociation()
		for i := 0; i != 8; i++ {
			runtime.GC()
		}
		if w.Value() != nil {
			panic("association kept its object strongly alive")
		}
	})
	if associated(unsafe.Pointer(p)) {
		panic("outside goroutine inherited association")
	}
	run(func() {
		if associate(unsafe.Pointer(p)) != 2 || associated(unsafe.Pointer(p)) {
			panic("different bubble reused old owner identity")
		}
		disassociate(unsafe.Pointer(p))
		if associated(unsafe.Pointer(p)) || associate(unsafe.Pointer(p)) != 1 {
			panic("disassociation/reassociation failed")
		}
	})
	if p[0] != 7 {
		panic("live associated object was collected")
	}
	fmt.Println("synctest heap association/WaitGroup/weak ownership ok")
}
