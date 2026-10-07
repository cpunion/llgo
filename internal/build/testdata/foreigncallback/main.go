package main

/*
#include <stdint.h>
int32_t Double(int32_t);
int32_t add(int32_t, int32_t);
*/
import "C"

import (
	_ "example.com/foreigncallback/capi"
	"example.com/foreigncallback/dep"
)

//export GoMainCallback
func GoMainCallback(value C.int) C.int { return C.int(dep.Result(int32(value))) }

func main() {
	if C.Double(21) != 42 || C.add(20, 22) != 42 {
		panic("package C direct exports failed")
	}
	if dep.Run(0) != 504 || dep.Run(1) != 504 || dep.Count() != 24 {
		panic("foreign callback allocation/GC or result failed")
	}
	println("ok")
}
