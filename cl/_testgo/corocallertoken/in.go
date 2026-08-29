package main

import (
	"runtime"
	"strings"
	"time"
)

func nested(depth int) {
	if depth > 0 {
		nested(depth - 1)
	} else {
		time.Sleep(time.Millisecond)
	}
	_, file, line, ok := runtime.Caller(0)
	if !ok || line != 15 || !strings.HasSuffix(file, "corocallertoken/in.go") {
		panic("bad logical caller location")
	}
}

var cleaned bool

func withCleanup() {
	defer func() { cleaned = true }()
	nested(256)
}

func main() {
	withCleanup()
	if !cleaned {
		panic("deferred cleanup did not run")
	}
}
