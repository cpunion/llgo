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
	loop *libuv.Loop
}

func NewExecutor() *executor {
	return &executor{libuv.LoopNew()}
}

func (e *executor) Async(fn func()) func() {
	a := e.loop.NewAsync(fn)
	return func() {
		a.Send()
	}
}

func (e *executor) Run(fn func()) {
	setExecutor(e)
	fn()
	if libuv.Run(e.loop, libuv.RUN_DEFAULT) != 0 {
		panic("libuv.Run failed")
	}
}
