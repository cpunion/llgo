package main

import "runtime"

type debugPayload struct {
	value int
}

//go:noinline
func resumeDebug(value int) int {
	live := &debugPayload{value: value}
	runtime.Gosched()
	runtime.GC()
	return live.value + 1
}

func main() {
	if resumeDebug(41) != 42 {
		panic("resumable debug state was not preserved")
	}
	println("wasm resume debug ok")
}
