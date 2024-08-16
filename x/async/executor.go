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
	"fmt"
	"unsafe"

	"github.com/goplus/llgo/c/libuv"
	"github.com/goplus/llgo/c/pthread"
)

var executorKey pthread.Key

func init() {
	executorKey.Create(nil)
}

func setExecutor(e *executor) {
	executorKey.Set((unsafe.Pointer)(e))
}

func Executor() *executor {
	return (*executor)(executorKey.Get())
}

type executor struct {
	loop   *libuv.Loop
	resume func()
}

func NewExecutor() *executor {
	return &executor{loop: libuv.LoopNew()}
}

func (e *executor) Async(fn func()) func() {
	a := &async{cb: fn}
	r := e.loop.Async(&a.Async, func(pa *libuv.Async) {
		a := (*async)(unsafe.Pointer(pa))
		a.Close(nil)
		a.cb()
	})
	if r != 0 {
		panic("Async failed")
	}
	return func() {
		if r := a.Send(); r != 0 {
			panic("Send failed")
		}
	}
}

func (e *executor) Resume() {
	println("executor: before Resume")
	e.resume()
}

func (e *executor) Run(fn func()) {
	setExecutor(e)
	fn()
	println("executor: before libuv.Run")
	e.loop.Run(libuv.RUN_DEFAULT)
	fmt.Println("====== after libuv.Run")
}
