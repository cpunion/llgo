package main

import (
	"time"

	"github.com/goplus/llgo/x/async"
)

func Sleep(d time.Duration) (co *async.Promise[int]) {
	return co.Async(func(resolve func(int)) {
		go func() {
			time.Sleep(d)
			resolve(1)
		}()
	})
}

func main() {
	async.Run(func() {
		println("before sleep")
		r := Sleep(1 * time.Second).Await()
		println("after sleep: ", r)
	})
}
