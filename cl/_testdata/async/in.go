package async

import (
	"github.com/goplus/llgo/x/async"
)

func GenInts() (co *async.Promise[int]) {
	async.Yield(co, 1)
	async.Yield(co, 2)
	async.Yield(co, 3)
	return
}

func WrapGenInts() *async.Promise[int] {
	return GenInts()
}

func UseGenInts() int {
	co := WrapGenInts()
	r := 0
	for !co.Done() {
		v := co.Value()
		r += v
		co.Next()
	}
	return r
}
