package main

import (
	"os"
	"time"
	_ "unsafe"
)

const LLGoFiles = "wrap/reentry.c"

//go:linkname foreign C.coro_foreign_funcval_probe
func foreign()

//llgo:coro contract foreign.v1 scope=declaration progress=may-block affinity=any-thread reentry=managed-callback memory=borrow-until-return
//go:linkname reenter C.coro_foreign_funcval_reenter
func reenter(func(int32) int32, int32) int32

func invoke(callback func()) {
	recovered := false
	func() {
		defer func() {
			value := recover()
			err, ok := value.(error)
			if !ok || err.Error() != "runtime error: invalid memory address or nil pointer dereference" {
				panic("missing foreign fault")
			}
			recovered = true
		}()
		callback()
	}()
	if !recovered {
		panic("foreign fault did not recover")
	}
}

func callback(value int32) int32 {
	time.Sleep(time.Millisecond)
	if value == 2 {
		invoke(foreign)
	}
	if value == 3 {
		panic("callback-escape")
	}
	return value + 10
}

func callbackEscape() {
	recovered := false
	func() {
		defer func() {
			if recover() != "callback-escape" {
				panic("incorrect callback escape")
			}
			recovered = true
		}()
		reenter(callback, 3)
	}()
	if !recovered {
		panic("callback escape did not recover")
	}
}

func main() {
	for i := 0; i < 3; i++ {
		invoke(func() { reenter(callback, 1) })
		if reenter(callback, 2) != 12 {
			panic("nested callback result")
		}
		callbackEscape()
		if reenter(callback, 0) != 10 {
			panic("normal callback result")
		}
	}
	os.Stdout.WriteString("REENTRY_FAULT_OK\n")
}
