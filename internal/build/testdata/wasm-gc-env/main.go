// Deliberately do not import os or runtime/debug (which itself imports os).
// Reading GOGC must work in a program with no Go environment API consumers.
package main

import (
	_ "runtime"
	_ "unsafe"
)

//go:linkname setGCPercent runtime/debug.setGCPercent
func setGCPercent(int32) int32

func main() {
	old := setGCPercent(-1)
	if normalized := setGCPercent(old); normalized != -1 {
		panic("invalid old GC percent")
	}
	println("wasm gc startup", old)
}
