package main

import (
	"github.com/goplus/lib/c"
)

func main() {
	c.Printf(c.Str("Hello %d\n"), sum(1, 2, 3, 4))
}

func sum(args ...int) (ret int) {
	for _, v := range args {
		ret += v
	}
	return
}
