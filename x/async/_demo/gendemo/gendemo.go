package main

import (
	"fmt"

	"github.com/goplus/llgo/x/async"
)

func GenInts() (co *async.Promise[int]) {
	println("gen: 1")
	async.Yield(co, 1)
	println("gen: 2")
	async.Yield(co, 2)
	println("gen: 3")
	async.Yield(co, 3)
	return
}

func main() {
	co := GenInts()
	for !co.Done() {
		fmt.Printf("got: %v\n", co.Value())
		co.Next()
	}
	fmt.Printf("done\n")
}
