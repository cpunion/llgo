package main

import (
	"github.com/goplus/llgo/internal/build/_testgo/coro_emission/aok"
	"github.com/goplus/llgo/internal/build/_testgo/coro_emission/zmiss"
)

func main() {
	aok.Call()
	zmiss.Missing()
}
