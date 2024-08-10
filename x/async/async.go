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
	_ "unsafe"

	"github.com/goplus/llgo/c/libuv"
)

const (
	LLGoPackage = true
)

type Void = [0]byte

// -----------------------------------------------------------------------------

type Promise[TOut any] struct {
	hdl   unsafe.Pointer
	e     *executor
	value TOut
}

// coAsync instruction injects a valid Promise object and then calls promise.async(...).
// llgo:link (*Promise).Async llgo.coAsync
func (p *Promise[TOut]) Async(fn func(resolve func(TOut))) *Promise[TOut] {
	panic("should not executed")
}

// call by llgo.coAsync
func (p *Promise[TOut]) async(fn func(resolve func(TOut))) {
	p.e = Executor()
	fmt.Printf("%T\n", fn)
	a := &async{cb: func() {
		println("async cb")
		p.Resume()
		println("after Resume")
	}}
	r := p.e.loop.Async(&a.Async, func(pa *libuv.Async) {
		a := (*async)(unsafe.Pointer(pa))
		a.Close(nil)
		a.cb()
	})
	if r != 0 {
		panic("Async failed")
	}
	println("before fn")
	fn(func(v TOut) {
		p.setValue(v)
		if r := a.Send(); r != 0 {
			panic("Send failed")
		}
	})
}

// llgo:link (*Promise).Await llgo.coAwait
func (p *Promise[TOut]) Await() TOut {
	panic("should not executed")
}

// call by llgo.coAwait
// p.Await() in f() will be translated to:
//
//	 f.promise.suspend()
//		p.afterAwait()
func (p *Promise[TOut]) afterAwait() TOut {
	println("afterAwait", p)
	return p.value
}

// llgo:link (*Promise).Return llgo.coReturn
func (p *Promise[TOut]) Return(v TOut) {
	panic("should not executed")
}

// llgo:link (*Promise).Yield llgo.coYield
func (p *Promise[TOut]) Yield(v TOut) {}

// call by llgo.coYield
func (p *Promise[TOut]) setValue(v TOut) {
	p.value = v
}

// llgo:link (*Promise).Suspend llgo.coSuspend
func (p *Promise[TOut]) Suspend() {}

// llgo:link (*Promise).Resume llgo.coResume
func (p *Promise[TOut]) Resume() {
	panic("should not executed")
}

// llgo:link (*Promise).Next llgo.coResume
func (p *Promise[TOut]) Next() {
	panic("should not executed")
}

func (p *Promise[TOut]) Value() TOut {
	return p.value
}

// llgo:link (*Promise).Done llgo.coDone
func (p *Promise[TOut]) Done() bool {
	panic("should not executed")
}

type promise interface {
	Done() bool
}

func Run[TOut any](fn func() *Promise[TOut]) TOut {
	var value TOut
	e := NewExecutor()
	e.Run(func() promise {
		return fn()
	})
	return value
}

// -----------------------------------------------------------------------------

type async struct {
	libuv.Async
	cb func()
}
