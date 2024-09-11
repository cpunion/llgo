package timeout

import (
	"time"

	"github.com/goplus/llgo/cmd/demo/abibug/async"
)

func Timeout(d time.Duration) async.Future[async.Void] {
	return async.Async(func(resolve func(async.Void)) {
		go func() {
			time.Sleep(d)
			resolve(async.Void{})
		}()
	})
}
