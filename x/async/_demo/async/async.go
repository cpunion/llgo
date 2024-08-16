package main

import (
	"time"
	_ "unsafe"

	"github.com/goplus/llgo/c"
	"github.com/goplus/llgo/x/async"
)

//go:linkname Gettid C.__gettid
func Gettid() c.Int

func Sleep(d time.Duration) (co *async.Promise[int]) {
	us := c.Uint(d.Microseconds())
	println("us: ", us)
	async.Async(co, func(resolve func(int)) {
		println("before sleep")
		go func() {
			println("sleeping")
			c.Usleep(us)
			println("after sleep")
			resolve(1)
		}()
	})
	return
}

func main() {
	async.Run(func() (co *async.Promise[async.Void]) {
		println("before call sleep")
		s := Sleep(time.Second * 1)
		println("sleep promise:", s)

		r := async.Await(s)
		println("after await", r)
		return
	})
}
