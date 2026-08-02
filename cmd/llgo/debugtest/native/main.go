package main

/*
#include "bridge.h"
*/
import "C"

import "os"

//go:noinline
func explicitPanic() {
	panic("native debugger panic") // LLDB_STOP: explicit_panic
}

//go:noinline
func divideByZero(divisor int) int {
	return 42 / divisor // LLDB_STOP: divide_by_zero
}

//go:noinline
func invalidMemory(pointer *int) int {
	return *pointer // LLDB_STOP: invalid_memory
}

//go:noinline
func hostTrap() {
	C.llgo_debug_trap() // LLDB_STOP: go_trap_caller
}

//go:noinline
func crossHostBoundary(value int) int {
	result := C.llgo_debug_host_bridge(C.int(value)) // LLDB_STOP: go_host_caller
	return int(result)
}

//go:noinline
//export llgo_debug_go_callback
func llgo_debug_go_callback(value C.int) C.int {
	result := value + 1 // LLDB_STOP: go_callback
	return result
}

func main() {
	if len(os.Args) != 2 {
		panic("expected one debug scenario")
	}
	switch os.Args[1] {
	case "panic":
		explicitPanic()
	case "divide":
		println(divideByZero(len(os.Args) - 2))
	case "invalid-memory":
		println(invalidMemory(nil))
	case "trap":
		hostTrap()
	case "boundary":
		if got := crossHostBoundary(40); got != 42 {
			panic("bad host boundary result")
		}
	case "inline":
		if got := optimizedInlineCaller(len(os.Args) * 10); got != 46 {
			panic("bad optimized inline result")
		}
	default:
		panic("unknown debug scenario")
	}
}
