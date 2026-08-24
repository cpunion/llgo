package main

import (
	"github.com/xgo-dev/llgo/internal/build/_testgo/coro_emission/aok"
	"github.com/xgo-dev/llgo/internal/build/_testgo/coro_emission/zmiss"
)

func main() {
	aok.Call()
	zmiss.Missing()
}
