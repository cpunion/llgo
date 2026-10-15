package main

import (
	"runtime"
	"syscall"
	"time"
	"unsafe"
	"weak"
)

//go:linkname run internal/synctest.Run
func run(func())

//go:linkname wait internal/synctest.Wait
func wait()

type timerRefs struct {
	public  weak.Pointer[time.Timer]
	channel weak.Pointer[byte]
	kept    <-chan time.Time
}

func timerReferences(keepChannel bool) timerRefs {
	t := time.NewTimer(time.Hour)
	var boxed any = t.C
	iface := (*struct {
		typ  unsafe.Pointer
		data unsafe.Pointer
	})(unsafe.Pointer(&boxed))
	refs := timerRefs{public: weak.Make(t), channel: weak.Make((*byte)(iface.data))}
	if keepChannel {
		refs.kept = t.C
	}
	return refs
}

func collect() {
	for i := 0; i < 8; i++ {
		runtime.GC()
	}
}

func main() {
	run(func() {
		out := make(chan timerRefs)
		go func() { out <- timerReferences(false) }()
		dead := <-out
		wait()
		collect()
		if dead.public.Value() != nil || dead.channel.Value() != nil {
			panic("fake timer queue retained unreachable public timer/channel")
		}
		start := time.Now()
		wait()
		if time.Now() != start {
			panic("discarded fake timer advanced clock during Wait")
		}

		go func() { out <- timerReferences(true) }()
		channelOnly := <-out
		wait()
		collect()
		if channelOnly.public.Value() != nil || channelOnly.channel.Value() == nil {
			panic("fake timer did not separate public ownership from live channel")
		}
		<-channelOnly.kept
		if time.Since(start) != time.Hour {
			panic("retained fake channel did not deliver at its deadline")
		}

		kept := time.NewTimer(time.Hour)
		collect()
		if !kept.Reset(0) {
			panic("public fake Timer did not keep channel and generation live")
		}
		select {
		case <-kept.C:
		default:
			panic("live fake Timer.Reset(0) did not publish a value")
		}

		done := make(chan struct{})
		time.AfterFunc(time.Hour, func() { close(done) })
		collect()
		<-done
	})
	const success = "synctest timer collection and retained ownership ok\n"
	if n, err := syscall.Write(1, []byte(success)); n != len(success) || err != nil {
		panic("synctest timer acceptance completion marker write failed")
	}
}
