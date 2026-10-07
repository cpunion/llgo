package main

/*
#include <stdint.h>
*/
import "C"

import "example.com/foreigncallback/dep"

//export GoMainCallback
func GoMainCallback(value C.int) C.int { return C.int(dep.Result(int32(value))) }

func main() {
	if dep.Run(0) != 504 || dep.Run(1) != 504 || dep.Count() != 24 {
		panic("foreign callback allocation/GC or result failed")
	}
	println("ok")
}
