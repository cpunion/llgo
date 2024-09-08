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
	"unsafe"

	"github.com/goplus/llgo/c/libuv"
	"github.com/goplus/llgo/c/pthread"
)

var execKey pthread.Key

func init() {
	execKey.Create(nil)
}

type Executor struct {
	L *libuv.Loop
}

func Exec() *Executor {
	v := execKey.Get()
	if v == nil {
		panic("async.Exec: no executor")
	}
	return (*Executor)(v)
}

func setExec(e *Executor) (old *Executor) {
	old = (*Executor)(execKey.Get())
	execKey.Set(unsafe.Pointer(e))
	return
}

func (e *Executor) Run() {
	e.L.Run(libuv.RUN_DEFAULT)
}

func Run[T any](future Future[T]) T {
	loop := libuv.LoopNew()
	exec := &Executor{loop}
	oldExec := setExec(exec)
	var ret T
	future.Then(func(v T) {
		ret = v
	})
	exec.Run()
	loop.Close()
	setExec(oldExec)
	return ret
}
