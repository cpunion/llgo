//go:build llgo
// +build llgo

/*
 * Copyright (c) 2024 The GoPlus Authors (goplus.org). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package async

import (
	"sync"
	"sync/atomic"

	"github.com/goplus/llgo/c/libuv"
	"github.com/goplus/llgo/x/cbind"
)

// Currently Async run chain a future that call chain in the goroutine running `async.Run`.
func Async[T any](fn func(func(T))) Future[T] {
	var result T
	var resultReady atomic.Bool
	var callbacks []func(T)
	var mutex sync.Mutex
	loop := Exec().L

	var a *libuv.Async
	var cb libuv.AsyncCb
	a, cb = cbind.BindF[libuv.Async, libuv.AsyncCb](func(a *libuv.Async) {
		a.Close(nil)
		mutex.Lock()
		currentCallbacks := callbacks
		callbacks = nil
		mutex.Unlock()

		for _, callback := range currentCallbacks {
			callback(result)
		}
	})
	loop.Async(a, cb)

	// Execute fn immediately
	fn(func(v T) {
		result = v
		resultReady.Store(true)
		a.Send()
	})

	return &future[T]{func(chain func(T)) {
		mutex.Lock()
		if resultReady.Load() {
			mutex.Unlock()
			chain(result)
		} else {
			callbacks = append(callbacks, chain)
			mutex.Unlock()
		}
	}}
}

// -----------------------------------------------------------------------------

func Race[T1 any](futures ...Future[T1]) Future[T1] {
	return Async(func(resolve func(T1)) {
		done := atomic.Bool{}
		for _, future := range futures {
			future.Then(func(v T1) {
				if !done.Swap(true) {
					// Just resolve the first one.
					resolve(v)
				}
			})
		}
	})
}

func All[T1 any](futures ...Future[T1]) Future[[]T1] {
	return Async(func(resolve func([]T1)) {
		n := len(futures)
		results := make([]T1, n)
		var done uint32
		for i, future := range futures {
			i := i
			future.Then(func(v T1) {
				results[i] = v
				if atomic.AddUint32(&done, 1) == uint32(n) {
					// All done.
					resolve(results)
				}
			})
		}
	})
}
