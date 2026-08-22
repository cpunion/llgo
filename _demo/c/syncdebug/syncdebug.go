package main

import (
	"io"
	"os"
	"sync"
	"unsafe"

	"github.com/goplus/lib/c"
	"github.com/goplus/lib/c/pthread"
	llsync "github.com/goplus/lib/c/pthread/sync"
)

type L struct {
	mu sync.Mutex
	s  string
	i  int
	w  io.Writer
}

var (
	workerCond  llsync.Cond
	workerMutex llsync.Mutex
	workerReady bool
	workerSem   llsync.Sem
)

func syncWorker(c.Pointer) c.Pointer {
	workerMutex.Lock()
	workerReady = true
	if workerCond.Signal() != 0 {
		workerMutex.Unlock()
		return c.Pointer(c.Str("condition signal failed"))
	}
	workerMutex.Unlock()
	if workerSem.Post() != 0 {
		return c.Pointer(c.Str("semaphore post failed"))
	}
	return nil
}

func main() {
	l := &L{s: "hello", i: 123, w: os.Stdout}
	println("sizeof(L):", unsafe.Sizeof(L{}))
	println("sizeof(sync.Mutex):", unsafe.Sizeof(sync.Mutex{}))
	println("sizeof(llsync.Mutex):", unsafe.Sizeof(llsync.Mutex{}))
	println("l:", l, "l.s:", l.s, "l.i:", l.i, "l.w:", l.w)
	l.mu.Lock()
	println("locked")
	println("l:", l, "l.s:", l.s, "l.i:", l.i, "l.w:", l.w)
	l.w.Write([]byte(l.s))
	l.w.Write([]byte("\n"))
	l.mu.Unlock()

	var once llsync.Once
	onceCount := 0
	delta := 2
	if once.Do(func() { onceCount += delta }) != 0 || once.Do(func() { onceCount++ }) != 0 {
		panic("once failed")
	}
	if onceCount != 2 {
		panic("once ran more than once")
	}

	var rw llsync.RWLock
	if rw.Init(nil) != 0 {
		panic("rwlock init failed")
	}
	rw.Lock()
	value := 42
	rw.Unlock()
	rw.RLock()
	if value != 42 {
		panic("rwlock value changed")
	}
	rw.RUnlock()
	rw.Destroy()

	if workerMutex.Init(nil) != 0 || workerCond.Init(nil) != 0 || workerSem.Init(0, 0) != 0 {
		panic("worker synchronization init failed")
	}
	workerMutex.Lock()
	var thread pthread.Thread
	if pthread.Create(&thread, nil, syncWorker, nil) != 0 {
		panic("thread create failed")
	}
	for !workerReady {
		if workerCond.Wait(&workerMutex) != 0 {
			panic("condition wait failed")
		}
	}
	workerMutex.Unlock()
	if workerSem.Wait() != 0 {
		panic("semaphore wait failed")
	}
	var result c.Pointer
	if pthread.Join(thread, &result) != 0 {
		panic("thread join failed")
	}
	if result != nil {
		panic(c.GoString((*c.Char)(result)))
	}
	if workerSem.Destroy() != 0 {
		panic("semaphore destroy failed")
	}
	workerCond.Destroy()
	workerMutex.Destroy()
	println("C synchronization passed")
}
