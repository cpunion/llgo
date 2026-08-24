package main

import (
	"github.com/xgo-dev/llgo/cl/_testgo/runtest/bar"
	"github.com/xgo-dev/llgo/cl/_testgo/runtest/foo"
)

func Zoo() int {
	return 3
}

func main() {
	println("foo.Foo()", foo.Foo())
	println("bar.Bar()", bar.Bar())
	println("Zoo()", Zoo())
}
